/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package hardware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	nvidiacomv1beta1 "github.com/ai-dynamo/dynamo/deploy/operator/api/v1beta1"
	"github.com/ai-dynamo/dynamo/deploy/operator/internal/consts"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	defaultDCGMEndpointTemplate = "http://{POD_IP}:9400/metrics"

	// NVIDIA GPU Feature Discovery (GFD) label keys
	LabelGPUCount   = "nvidia.com/gpu.count"
	LabelGPUProduct = "nvidia.com/gpu.product"
	LabelGPUMemory  = "nvidia.com/gpu.memory"

	// DCGM exporter label constants
	LabelApp                     = "app"
	LabelAppKubernetesName       = "app.kubernetes.io/name"
	LabelValueNvidiaDCGMExporter = "nvidia-dcgm-exporter"
	LabelValueDCGMExporter       = "dcgm-exporter"
	LabelValueGPUOperator        = "gpu-operator"
	GPUOperatorNamespace         = "gpu-operator"

	requestTimeout      = 5 * time.Second
	dialTimeout         = 3 * time.Second
	tlsHandshakeTimeout = 3 * time.Second

	// Cloud provider constants
	CloudProviderGCP     = "gcp"
	CloudProviderAWS     = "aws"
	CloudProviderAKS     = "aks"
	CloudProviderOther   = "other"
	CloudProviderUnknown = "unknown"
)

// awsInstanceTypePrefixes matches known GPU/accelerator instance families on EKS.
var awsInstanceTypePrefixes = []string{
	"p3.", "p3dn.", "p4d.", "p4de.", "p5.", // GPU instances
	"g3.", "g4dn.", "g4ad.", "g5.", "g6.", // GPU instances
	"inf1.", "inf2.", // Inferentia
	"trn1.", "trn1n.", // Trainium
}

// gcpMachineSeries matches known GCP accelerator-optimised machine series on GKE.
var gcpMachineSeries = []string{
	"a2-", // A100 GPU machines
	"a3-", // H100 GPU machines
	"g2-", // L4 GPU machines
}

// ScrapeMetricsFunc is the function type for scraping DCGM metrics.
type ScrapeMetricsFunc func(ctx context.Context, endpoint string) (*AcceleratorInfo, error)

// NVIDIADiscovery implements the Discovery interface for NVIDIA GPUs.
// It uses DCGM exporter metrics to discover GPU configuration.
type NVIDIADiscovery struct {
	Scraper ScrapeMetricsFunc
}

// NewNVIDIADiscovery creates a new NVIDIA discovery instance.
func NewNVIDIADiscovery(scraper ScrapeMetricsFunc) *NVIDIADiscovery {
	return &NVIDIADiscovery{
		Scraper: scraper,
	}
}

// Type returns the accelerator type for NVIDIA.
func (d *NVIDIADiscovery) Type() AcceleratorType {
	return AcceleratorTypeNVIDIA
}

// Priority returns the discovery priority for NVIDIA (highest).
func (d *NVIDIADiscovery) Priority() int {
	return 100
}

// Discover performs NVIDIA GPU discovery using DCGM exporter metrics.
func (d *NVIDIADiscovery) Discover(ctx context.Context, k8sClient client.Reader) (*AcceleratorInfo, error) {
	// List DCGM exporter pods
	dcgmPods, err := listDCGMExporterPods(ctx, k8sClient)
	if err != nil && !strings.Contains(err.Error(), "no DCGM exporter pods found") {
		return nil, fmt.Errorf("listing DCGM exporter pods failed: %w", err)
	}

	// If no pods found
	if len(dcgmPods) == 0 {
		gpuPods, err := listGPUOperatorRunningPods(ctx, k8sClient)
		if len(gpuPods) > 0 {
			return nil, fmt.Errorf("DCGM is not enabled in the GPU Operator (check GPU Operator configuration and permissions)")
		}
		return nil, err
	}

	// Scrape each running pod individually
	var bestNode *AcceleratorInfo
	var scrapeErrors []error
	nodesWithGPUs := 0

	for _, pod := range dcgmPods {
		if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			continue
		}

		endpoint := buildDCGMEndpoint(pod.Status.PodIP)
		info, err := d.Scraper(ctx, endpoint)
		if err != nil {
			scrapeErrors = append(scrapeErrors, fmt.Errorf("pod %s (%s): %w", pod.Name, pod.Status.PodIP, err))
			continue
		}
		// Increment NodesCount for every node that successfully reports GPU metrics
		nodesWithGPUs++
		// Select best node: highest GPU count, tie-breaker by VRAM
		if bestNode == nil ||
			info.CountPerNode > bestNode.CountPerNode ||
			(info.CountPerNode == bestNode.CountPerNode &&
				info.MemoryMB > bestNode.MemoryMB) {

			bestNode = info
		}
	}

	if bestNode == nil {
		if len(scrapeErrors) > 0 {
			return nil, fmt.Errorf("failed to scrape any DCGM exporter pod: %v", scrapeErrors)
		}
		return nil, fmt.Errorf("no GPU metrics could be parsed from any DCGM pod")
	}

	// Infer cloud provider for the best node
	cloudProvider, err := GetCloudProviderInfo(ctx, k8sClient)
	if err != nil {
		cloudProvider = CloudProviderUnknown
	}
	bestNode.CloudProvider = cloudProvider
	bestNode.NodesCount = nodesWithGPUs
	bestNode.ResourceName = consts.KubeResourceGPUNvidia

	return bestNode, nil
}

// buildDCGMEndpoint builds the DCGM metrics endpoint URL from a pod IP.
func buildDCGMEndpoint(podIP string) string {
	template := os.Getenv("DCGM_METRICS_ENDPOINT_TEMPLATE")
	if template == "" {
		template = defaultDCGMEndpointTemplate
	}

	return strings.ReplaceAll(template, "{POD_IP}", podIP)
}

// listDCGMExporterPods lists DCGM exporter pods across all namespaces.
func listDCGMExporterPods(ctx context.Context, k8sClient client.Reader) ([]corev1.Pod, error) {
	var result []corev1.Pod
	seen := make(map[string]struct{})

	selectors := []client.MatchingLabels{
		{LabelApp: LabelValueNvidiaDCGMExporter},
		{LabelApp: LabelValueDCGMExporter},
		{LabelAppKubernetesName: LabelValueDCGMExporter},
	}

	var lastErr error

	for _, selector := range selectors {
		podList := &corev1.PodList{}

		err := k8sClient.List(ctx, podList, selector)
		if err != nil {
			lastErr = fmt.Errorf("list pods: %w", err)
			continue
		}

		for _, pod := range podList.Items {
			key := pod.Namespace + "/" + pod.Name

			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				result = append(result, pod)
			}
		}
	}

	if len(result) > 0 {
		return result, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}

	return nil, fmt.Errorf("no DCGM exporter pods found")
}

// listGPUOperatorRunningPods lists GPU Operator pods in the gpu-operator namespace.
func listGPUOperatorRunningPods(ctx context.Context, k8sClient client.Reader) ([]corev1.Pod, error) {
	var result []corev1.Pod
	seen := make(map[string]struct{})

	selectors := []client.MatchingLabels{
		{LabelApp: LabelValueGPUOperator},
		{LabelAppKubernetesName: LabelValueGPUOperator},
	}

	var lastErr error

	for _, selector := range selectors {
		podList := &corev1.PodList{}

		err := k8sClient.List(
			ctx,
			podList,
			client.InNamespace(GPUOperatorNamespace),
			selector,
		)
		if err != nil {
			lastErr = fmt.Errorf("list gpu operator pods: %w", err)
			continue
		}

		for _, pod := range podList.Items {
			if pod.Status.Phase != corev1.PodRunning {
				continue
			}

			key := pod.Namespace + "/" + pod.Name

			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				result = append(result, pod)
			}
		}
	}

	if len(result) > 0 {
		return result, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}

	return nil, fmt.Errorf(
		"gpu operator is not installed %s",
		GPUOperatorNamespace,
	)
}

// ScrapeMetricsEndpoint retrieves and parses Prometheus metrics from a DCGM exporter pod endpoint.
func ScrapeMetricsEndpoint(ctx context.Context, endpoint string) (*AcceleratorInfo, error) {
	// Set a timeout for the request
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	// Create a custom HTTP client with transport-level timeouts
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: tlsHandshakeTimeout,
	}
	httpClient := &http.Client{
		Transport: transport,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", endpoint, err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s failed: %w", endpoint, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.FromContext(ctx).V(1).Info("failed to close response body", "err", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"metrics endpoint %s returned status %d",
			endpoint,
			resp.StatusCode,
		)
	}

	parser := expfmt.NewTextParser(model.UTF8Validation)
	metricFamilies, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse prometheus metrics: %w", err)
	}

	return parseMetrics(ctx, metricFamilies)
}

// parseMetrics extracts accelerator information from DCGM Prometheus metrics.
func parseMetrics(ctx context.Context, families map[string]*dto.MetricFamily) (*AcceleratorInfo, error) {
	logger := log.FromContext(ctx)

	getLabel := func(m *dto.Metric, name string) string {
		for _, l := range m.GetLabel() {
			if l.GetName() == name {
				return l.GetValue()
			}
		}
		return ""
	}

	// Track unique GPUs
	gpuSet := map[string]struct{}{}

	var model string
	var vram int
	var hostName string

	fbFree := map[string]float64{}
	fbUsed := map[string]float64{}
	fbReserved := map[string]float64{}

	// --- Detect GPUs + Model + Hostname ---
	if mf, ok := families["DCGM_FI_DEV_GPU_TEMP"]; ok {
		for _, m := range mf.Metric {
			gpuID := getLabel(m, "gpu")
			if gpuID == "" {
				continue
			}
			gpuSet[gpuID] = struct{}{}

			// Extract model from label
			if model == "" {
				model = getLabel(m, "modelName")
			}

			// Extract Hostname label
			if hostName == "" {
				hostName = getLabel(m, "Hostname")
			}
		}
	}

	// --- Collect framebuffer metrics ---
	if mf, ok := families["DCGM_FI_DEV_FB_FREE"]; ok {
		for _, m := range mf.Metric {
			gpuID := getLabel(m, "gpu")
			if gpuID == "" {
				continue
			}
			fbFree[gpuID] = m.GetGauge().GetValue()

			if hostName == "" {
				hostName = getLabel(m, "Hostname")
			}
		}
	}

	if mf, ok := families["DCGM_FI_DEV_FB_USED"]; ok {
		for _, m := range mf.Metric {
			gpuID := getLabel(m, "gpu")
			if gpuID == "" {
				continue
			}
			fbUsed[gpuID] = m.GetGauge().GetValue()

			if hostName == "" {
				hostName = getLabel(m, "Hostname")
			}
		}
	}

	if mf, ok := families["DCGM_FI_DEV_FB_RESERVED"]; ok {
		for _, m := range mf.Metric {
			gpuID := getLabel(m, "gpu")
			if gpuID == "" {
				continue
			}
			fbReserved[gpuID] = m.GetGauge().GetValue()

			if hostName == "" {
				hostName = getLabel(m, "Hostname")
			}
		}
	}

	// --- Calculate Max VRAM
	for gpuID := range gpuSet {
		total := int(fbFree[gpuID] + fbUsed[gpuID] + fbReserved[gpuID])
		if total > vram {
			vram = total
		}
	}

	gpuCount := len(gpuSet)

	if gpuCount == 0 {
		return nil, fmt.Errorf("no GPUs detected from DCGM metrics")
	}

	// --- Infer system from model ---
	system := InferHardwareSystem(model)

	logger.Info("Parsed GPU info",
		"node", hostName,
		"gpuCount", gpuCount,
		"model", model,
		"vramMiB", vram,
		"system", system,
	)

	return &AcceleratorInfo{
		Type:         AcceleratorTypeNVIDIA,
		NodeName:     hostName,
		CountPerNode: gpuCount,
		Model:        model,
		MemoryMB:     vram,
		MIGEnabled:   false,
		MIGProfiles:  map[string]int{},
		System:       system,
		ResourceName: consts.KubeResourceGPUNvidia,
	}, nil
}

// InferHardwareSystem maps GPU product name to hardware system identifier.
func InferHardwareSystem(gpuProduct string) nvidiacomv1beta1.GPUSKUType {
	if gpuProduct == "" {
		return ""
	}

	// Normalize: uppercase, remove spaces/dashes for pattern matching
	normalized := strings.ToUpper(strings.ReplaceAll(gpuProduct, "-", ""))
	normalized = strings.ReplaceAll(normalized, " ", "")

	// Map common NVIDIA datacenter GPU products to AIC hardware system identifiers.
	patterns := []struct {
		pattern string
		system  nvidiacomv1beta1.GPUSKUType
	}{
		{"GB200", nvidiacomv1beta1.GPUSKUTypeGB200SXM},
		{"H200", nvidiacomv1beta1.GPUSKUTypeH200SXM},
		{"H100", nvidiacomv1beta1.GPUSKUTypeH100SXM},
		{"B200", nvidiacomv1beta1.GPUSKUTypeB200SXM},
		{"A100", nvidiacomv1beta1.GPUSKUTypeA100SXM},
		{"L40S", nvidiacomv1beta1.GPUSKUTypeL40S},
	}

	for _, p := range patterns {
		if strings.Contains(normalized, p.pattern) {
			return p.system
		}
	}

	// Unknown GPU type, return empty value.
	return ""
}

// DiscoverFromNodeLabels discovers GPU information from Kubernetes node labels.
// This is an alternative discovery method that doesn't require DCGM exporter.
func DiscoverFromNodeLabels(ctx context.Context, k8sClient client.Reader) (*AcceleratorInfo, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting GPU discovery from cluster nodes")

	// List all nodes in the cluster
	nodeList := &corev1.NodeList{}
	if err := k8sClient.List(ctx, nodeList); err != nil {
		return nil, fmt.Errorf("failed to list cluster nodes: %w", err)
	}

	if len(nodeList.Items) == 0 {
		return nil, fmt.Errorf("no nodes found in cluster")
	}

	logger.Info("Found cluster nodes", "count", len(nodeList.Items))

	// Track the best GPU configuration found
	var bestInfo *AcceleratorInfo
	nodesWithGPUs := 0

	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		info, err := extractGPUInfoFromNode(node)
		if err != nil {
			logger.V(1).Info("Skipping node without valid GPU info",
				"node", node.Name,
				"reason", err.Error())
			continue
		}

		nodesWithGPUs++
		logger.Info("Found GPU node",
			"node", node.Name,
			"gpus", info.CountPerNode,
			"model", info.Model,
			"vram", info.MemoryMB)

		// Select best configuration: prefer higher GPU count, then higher VRAM
		if bestInfo == nil ||
			info.CountPerNode > bestInfo.CountPerNode ||
			(info.CountPerNode == bestInfo.CountPerNode && info.MemoryMB > bestInfo.MemoryMB) {
			bestInfo = info
		}
	}

	if bestInfo == nil {
		return nil, fmt.Errorf("no nodes with NVIDIA GPU Feature Discovery labels found (checked %d nodes). "+
			"Ensure GPU nodes have labels: %s, %s, %s",
			len(nodeList.Items), LabelGPUCount, LabelGPUProduct, LabelGPUMemory)
	}

	// Infer cloud provider
	cloudProvider, err := GetCloudProviderInfo(ctx, k8sClient)
	if err != nil {
		cloudProvider = CloudProviderUnknown
	}

	// Infer hardware system from GPU model
	bestInfo.System = InferHardwareSystem(bestInfo.Model)
	bestInfo.NodesCount = nodesWithGPUs
	bestInfo.CloudProvider = cloudProvider
	bestInfo.ResourceName = consts.KubeResourceGPUNvidia

	logger.Info("GPU discovery completed",
		"gpusPerNode", bestInfo.CountPerNode,
		"nodesWithGPUs", bestInfo.NodesCount,
		"totalGpus", bestInfo.TotalCount(),
		"model", bestInfo.Model,
		"vram", bestInfo.MemoryMB,
		"system", bestInfo.System)

	return bestInfo, nil
}

// extractGPUInfoFromNode extracts GPU information from a single node's labels.
func extractGPUInfoFromNode(node *corev1.Node) (*AcceleratorInfo, error) {
	labels := node.Labels
	if labels == nil {
		return nil, fmt.Errorf("node has no labels")
	}

	gpuCountStr, ok := labels[LabelGPUCount]
	if !ok {
		return nil, fmt.Errorf("missing label %s", LabelGPUCount)
	}
	gpuCount, err := strconv.Atoi(gpuCountStr)
	if err != nil || gpuCount <= 0 {
		return nil, fmt.Errorf("invalid GPU count: %s", gpuCountStr)
	}

	gpuModel, ok := labels[LabelGPUProduct]
	if !ok || gpuModel == "" {
		return nil, fmt.Errorf("missing or empty label %s", LabelGPUProduct)
	}

	// Extract VRAM (memory in MiB)
	gpuMemoryStr, ok := labels[LabelGPUMemory]
	if !ok {
		return nil, fmt.Errorf("missing label %s", LabelGPUMemory)
	}
	gpuMemory, err := strconv.Atoi(gpuMemoryStr)
	if err != nil || gpuMemory <= 0 {
		return nil, fmt.Errorf("invalid GPU memory: %s", gpuMemoryStr)
	}

	return &AcceleratorInfo{
		Type:         AcceleratorTypeNVIDIA,
		Model:        gpuModel,
		MemoryMB:     gpuMemory,
		CountPerNode: gpuCount,
		NodeName:     node.Name,
	}, nil
}

// GetCloudProviderInfo detects the cloud provider from node metadata.
func GetCloudProviderInfo(ctx context.Context, k8sClient client.Reader) (string, error) {
	var nodeList corev1.NodeList
	if err := k8sClient.List(ctx, &nodeList); err != nil {
		return CloudProviderUnknown, fmt.Errorf("failed to list nodes: %w", err)
	}
	if len(nodeList.Items) == 0 {
		return CloudProviderUnknown, fmt.Errorf("no nodes found in cluster")
	}
	// Use first node as representative (assumes homogeneous control plane)
	node := nodeList.Items[0]
	providerID := strings.ToLower(node.Spec.ProviderID)
	labels := node.Labels
	instanceType := strings.ToLower(labels["node.kubernetes.io/instance-type"])

	// ---- Primary Detection: providerID ----
	switch {
	case strings.Contains(providerID, "azure"):
		return CloudProviderAKS, nil
	case strings.Contains(providerID, "aws"):
		return CloudProviderAWS, nil
	case strings.Contains(providerID, "gce"):
		return CloudProviderGCP, nil
	}

	// ---- Secondary Detection: Node Labels ----
	// AKS labels
	if _, ok := labels["kubernetes.azure.com/cluster"]; ok {
		return CloudProviderAKS, nil
	}
	if strings.Contains(instanceType, "standard_") {
		return CloudProviderAKS, nil
	}

	// EKS labels
	if _, ok := labels["eks.amazonaws.com/nodegroup"]; ok {
		return CloudProviderAWS, nil
	}
	if isAWSInstanceType(instanceType) {
		return CloudProviderAWS, nil
	}

	// GKE labels
	if _, ok := labels["cloud.google.com/gke-nodepool"]; ok {
		return CloudProviderGCP, nil
	}
	if isGCPInstanceType(instanceType) {
		return CloudProviderGCP, nil
	}

	return CloudProviderOther, nil
}

func isGCPInstanceType(instanceType string) bool {
	for _, prefix := range gcpMachineSeries {
		if strings.HasPrefix(instanceType, prefix) {
			return true
		}
	}
	return false
}

func isAWSInstanceType(instanceType string) bool {
	for _, prefix := range awsInstanceTypePrefixes {
		if strings.HasPrefix(instanceType, prefix) {
			return true
		}
	}
	return false
}
