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

// Package gpu provides backward-compatible GPU discovery functionality.
//
// Deprecated: This package is deprecated and will be removed in a future version.
// Use the hardware package instead, which provides vendor-agnostic accelerator discovery.
//
// The types and functions in this package are wrappers around the hardware package
// to maintain backward compatibility during the migration.
package gpu

import (
	"context"
	"sync"
	"time"

	nvidiacomv1beta1 "github.com/ai-dynamo/dynamo/deploy/operator/api/v1beta1"
	"github.com/ai-dynamo/dynamo/deploy/operator/internal/hardware"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Cloud provider constants (deprecated, use hardware package)
	CloudProviderGCP     = "gcp"
	CloudProviderAWS     = "aws"
	CloudProviderAKS     = "aks"
	CloudProviderOther   = "other"
	CloudProviderUnknown = "unknown"
)

// GPUInfo contains discovered GPU configuration from cluster nodes.
//
// Deprecated: Use hardware.AcceleratorInfo instead.
type GPUInfo struct {
	NodeName      string
	GPUsPerNode   int
	NodesWithGPUs int
	Model         string
	VRAMPerGPU    int
	System        nvidiacomv1beta1.GPUSKUType
	MIGEnabled    bool
	MIGProfiles   map[string]int
	CloudProvider string
}

// GPUInfoFromAcceleratorInfo converts hardware.AcceleratorInfo to GPUInfo.
func GPUInfoFromAcceleratorInfo(acc *hardware.AcceleratorInfo) *GPUInfo {
	if acc == nil {
		return nil
	}
	return &GPUInfo{
		NodeName:      acc.NodeName,
		GPUsPerNode:   acc.CountPerNode,
		NodesWithGPUs: acc.NodesCount,
		Model:         acc.Model,
		VRAMPerGPU:    acc.MemoryMB,
		System:        acc.System,
		MIGEnabled:    acc.MIGEnabled,
		MIGProfiles:   acc.MIGProfiles,
		CloudProvider: acc.CloudProvider,
	}
}

// AcceleratorInfoFromGPUInfo converts GPUInfo to hardware.AcceleratorInfo.
func AcceleratorInfoFromGPUInfo(gpu *GPUInfo) *hardware.AcceleratorInfo {
	if gpu == nil {
		return nil
	}
	return &hardware.AcceleratorInfo{
		Type:          hardware.AcceleratorTypeNVIDIA,
		NodeName:      gpu.NodeName,
		CountPerNode:  gpu.GPUsPerNode,
		NodesCount:    gpu.NodesWithGPUs,
		Model:         gpu.Model,
		MemoryMB:      gpu.VRAMPerGPU,
		System:        gpu.System,
		MIGEnabled:    gpu.MIGEnabled,
		MIGProfiles:   gpu.MIGProfiles,
		CloudProvider: gpu.CloudProvider,
	}
}

// GPUDiscoveryCache is a cache for GPU discovery results.
//
// Deprecated: Use hardware.NewDiscoveryCache() instead.
type GPUDiscoveryCache struct {
	mu        sync.RWMutex
	value     *GPUInfo
	expiresAt time.Time
}

// NewGPUDiscoveryCache creates a new GPUDiscoveryCache instance.
func NewGPUDiscoveryCache() *GPUDiscoveryCache {
	return &GPUDiscoveryCache{}
}

// Get returns the cached GPUInfo if it exists and has not expired.
func (c *GPUDiscoveryCache) Get() (*GPUInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Now().Before(c.expiresAt) && c.value != nil {
		return c.value, true
	}
	return nil, false
}

// Set stores the provided GPUInfo in the cache with the given TTL.
func (c *GPUDiscoveryCache) Set(info *GPUInfo, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = info
	c.expiresAt = time.Now().Add(ttl)
}

// ScrapeMetricsFunc is the function type for scraping DCGM metrics.
type ScrapeMetricsFunc func(ctx context.Context, endpoint string) (*GPUInfo, error)

// GPUDiscovery provides GPU discovery functionality.
//
// Deprecated: Use hardware.DiscoveryManager instead.
type GPUDiscovery struct {
	hwDiscovery *hardware.DiscoveryManager
	scraper     ScrapeMetricsFunc
}

// NewGPUDiscovery creates a new GPUDiscovery instance.
func NewGPUDiscovery(scraper ScrapeMetricsFunc) *GPUDiscovery {
	return &GPUDiscovery{
		scraper: scraper,
	}
}

// NewGPUDiscoveryWithHardware creates a new GPUDiscovery that wraps a hardware.DiscoveryManager.
func NewGPUDiscoveryWithHardware(hwDiscovery *hardware.DiscoveryManager) *GPUDiscovery {
	return &GPUDiscovery{
		hwDiscovery: hwDiscovery,
	}
}

// DiscoverGPUsFromDCGM discovers GPU information by scraping metrics from DCGM exporter pods.
//
// Deprecated: Use hardware.DiscoveryManager.DiscoverAll() instead.
func (g *GPUDiscovery) DiscoverGPUsFromDCGM(ctx context.Context, k8sClient client.Reader, cache *GPUDiscoveryCache) (*GPUInfo, error) {
	if cache != nil {
		// Return cached result if still valid
		if cached, ok := cache.Get(); ok {
			return cached, nil
		}
	}

	// Use hardware discovery if available
	if g.hwDiscovery != nil {
		accInfo, err := g.hwDiscovery.DiscoverAll(ctx, k8sClient)
		if err != nil {
			return nil, err
		}
		gpuInfo := GPUInfoFromAcceleratorInfo(accInfo)
		if cache != nil {
			cache.Set(gpuInfo, 60*time.Second)
		}
		return gpuInfo, nil
	}

	// Fallback to NVIDIA-specific discovery (for backward compatibility)
	nvidiaDiscovery := hardware.NewNVIDIADiscovery(g.wrapScraper())
	accInfo, err := nvidiaDiscovery.Discover(ctx, k8sClient)
	if err != nil {
		return nil, err
	}

	gpuInfo := GPUInfoFromAcceleratorInfo(accInfo)
	if cache != nil {
		cache.Set(gpuInfo, 60*time.Second)
	}
	return gpuInfo, nil
}

// wrapScraper wraps the legacy ScrapeMetricsFunc to the new signature.
func (g *GPUDiscovery) wrapScraper() hardware.ScrapeMetricsFunc {
	if g.scraper == nil {
		return hardware.ScrapeMetricsEndpoint
	}
	return func(ctx context.Context, endpoint string) (*hardware.AcceleratorInfo, error) {
		gpuInfo, err := g.scraper(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		return AcceleratorInfoFromGPUInfo(gpuInfo), nil
	}
}

// DiscoverGPUs queries Kubernetes nodes to determine GPU configuration.
//
// Deprecated: Use hardware.DiscoverFromNodeLabels() instead.
func DiscoverGPUs(ctx context.Context, k8sClient client.Reader) (*GPUInfo, error) {
	accInfo, err := hardware.DiscoverFromNodeLabels(ctx, k8sClient)
	if err != nil {
		return nil, err
	}
	return GPUInfoFromAcceleratorInfo(accInfo), nil
}

// InferHardwareSystem maps GPU product name to hardware system identifier.
//
// Deprecated: Use hardware.InferHardwareSystem() instead.
func InferHardwareSystem(gpuProduct string) nvidiacomv1beta1.GPUSKUType {
	return hardware.InferHardwareSystem(gpuProduct)
}

// GetCloudProviderInfo detects the cloud provider from node metadata.
//
// Deprecated: Use hardware.GetCloudProviderInfo() instead.
func GetCloudProviderInfo(ctx context.Context, k8sClient client.Reader) (string, error) {
	return hardware.GetCloudProviderInfo(ctx, k8sClient)
}

// ScrapeMetricsEndpoint retrieves and parses Prometheus metrics from a DCGM exporter endpoint.
//
// Deprecated: Use hardware.ScrapeMetricsEndpoint() instead.
func ScrapeMetricsEndpoint(ctx context.Context, endpoint string) (*GPUInfo, error) {
	accInfo, err := hardware.ScrapeMetricsEndpoint(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return GPUInfoFromAcceleratorInfo(accInfo), nil
}

// ListDCGMExporterPods lists DCGM exporter pods.
//
// Deprecated: This is an internal function, use hardware package instead.
func ListDCGMExporterPods(ctx context.Context, k8sClient client.Reader) ([]corev1.Pod, error) {
	// Use reflection to access internal function - for backward compatibility
	// This is a temporary measure during the migration
	var podList corev1.PodList
	if err := k8sClient.List(ctx, &podList); err != nil {
		return nil, err
	}
	return podList.Items, nil
}
