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

package gpu

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ai-dynamo/dynamo/deploy/operator/internal/hardware"
)

// newFakeClient creates a fake Kubernetes client with the given objects
func newFakeClient(objs ...client.Object) client.Reader {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}

func TestDiscoverGPUs_SingleNode(t *testing.T) {
	ctx := context.Background()

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-1",
			Labels: map[string]string{
				hardware.LabelGPUCount:   "8",
				hardware.LabelGPUProduct: "H100-SXM5-80GB",
				hardware.LabelGPUMemory:  "81920",
			},
		},
	}

	k8sClient := newFakeClient(node)

	gpuInfo, err := DiscoverGPUs(ctx, k8sClient)
	require.NoError(t, err)
	require.NotNil(t, gpuInfo)

	assert.Equal(t, 8, gpuInfo.GPUsPerNode)
	assert.Equal(t, "H100-SXM5-80GB", gpuInfo.Model)
	assert.Equal(t, 81920, gpuInfo.VRAMPerGPU)
	assert.Equal(t, "h100_sxm", string(gpuInfo.System))
}

func TestDiscoverGPUs_MultipleNodesHomogeneous(t *testing.T) {
	ctx := context.Background()

	// Multiple nodes with same GPU configuration
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-1",
			Labels: map[string]string{
				hardware.LabelGPUCount:   "8",
				hardware.LabelGPUProduct: "H100-SXM5-80GB",
				hardware.LabelGPUMemory:  "81920",
			},
		},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-2",
			Labels: map[string]string{
				hardware.LabelGPUCount:   "8",
				hardware.LabelGPUProduct: "H100-SXM5-80GB",
				hardware.LabelGPUMemory:  "81920",
			},
		},
	}

	k8sClient := newFakeClient(node1, node2)

	gpuInfo, err := DiscoverGPUs(ctx, k8sClient)
	require.NoError(t, err)
	require.NotNil(t, gpuInfo)

	assert.Equal(t, 8, gpuInfo.GPUsPerNode)
	assert.Equal(t, "H100-SXM5-80GB", gpuInfo.Model)
	assert.Equal(t, 81920, gpuInfo.VRAMPerGPU)
}

func TestDiscoverGPUs_MultipleNodesHeterogeneous_HigherGPUCountWins(t *testing.T) {
	ctx := context.Background()

	// Node with fewer GPUs
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-1",
			Labels: map[string]string{
				hardware.LabelGPUCount:   "4",
				hardware.LabelGPUProduct: "A100-SXM4-40GB",
				hardware.LabelGPUMemory:  "40960",
			},
		},
	}

	// Node with more GPUs (should win)
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-2",
			Labels: map[string]string{
				hardware.LabelGPUCount:   "8",
				hardware.LabelGPUProduct: "H100-SXM5-80GB",
				hardware.LabelGPUMemory:  "81920",
			},
		},
	}

	k8sClient := newFakeClient(node1, node2)

	gpuInfo, err := DiscoverGPUs(ctx, k8sClient)
	require.NoError(t, err)
	require.NotNil(t, gpuInfo)

	// Should prefer node with 8 GPUs over node with 4 GPUs
	assert.Equal(t, 8, gpuInfo.GPUsPerNode)
	assert.Equal(t, "H100-SXM5-80GB", gpuInfo.Model)
	assert.Equal(t, 81920, gpuInfo.VRAMPerGPU)
}

func TestDiscoverGPUs_MultipleNodesHeterogeneous_HigherVRAMWins(t *testing.T) {
	ctx := context.Background()

	// Node with same GPU count but less VRAM
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-1",
			Labels: map[string]string{
				hardware.LabelGPUCount:   "8",
				hardware.LabelGPUProduct: "A100-SXM4-40GB",
				hardware.LabelGPUMemory:  "40960",
			},
		},
	}

	// Node with same GPU count but more VRAM (should win)
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-2",
			Labels: map[string]string{
				hardware.LabelGPUCount:   "8",
				hardware.LabelGPUProduct: "H100-SXM5-80GB",
				hardware.LabelGPUMemory:  "81920",
			},
		},
	}

	k8sClient := newFakeClient(node1, node2)

	gpuInfo, err := DiscoverGPUs(ctx, k8sClient)
	require.NoError(t, err)
	require.NotNil(t, gpuInfo)

	// Should prefer node with higher VRAM when GPU count is equal
	assert.Equal(t, 8, gpuInfo.GPUsPerNode)
	assert.Equal(t, "H100-SXM5-80GB", gpuInfo.Model)
	assert.Equal(t, 81920, gpuInfo.VRAMPerGPU)
}

func TestDiscoverGPUs_MixedNodesWithAndWithoutGPUs(t *testing.T) {
	ctx := context.Background()

	// CPU-only node (no GPU labels)
	cpuNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "cpu-node-1",
			Labels: map[string]string{},
		},
	}

	// GPU node
	gpuNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-1",
			Labels: map[string]string{
				hardware.LabelGPUCount:   "8",
				hardware.LabelGPUProduct: "H100-SXM5-80GB",
				hardware.LabelGPUMemory:  "81920",
			},
		},
	}

	k8sClient := newFakeClient(cpuNode, gpuNode)

	gpuInfo, err := DiscoverGPUs(ctx, k8sClient)
	require.NoError(t, err)
	require.NotNil(t, gpuInfo)

	// Should find the GPU node and ignore CPU-only node
	assert.Equal(t, 8, gpuInfo.GPUsPerNode)
	assert.Equal(t, "H100-SXM5-80GB", gpuInfo.Model)
}

func TestDiscoverGPUs_NoNodes(t *testing.T) {
	ctx := context.Background()
	k8sClient := newFakeClient() // Empty cluster

	gpuInfo, err := DiscoverGPUs(ctx, k8sClient)
	assert.Error(t, err)
	assert.Nil(t, gpuInfo)
	assert.Contains(t, err.Error(), "no nodes found")
}

func TestDiscoverGPUs_NoGPUNodes(t *testing.T) {
	ctx := context.Background()

	// Only CPU nodes
	cpuNode1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "cpu-node-1",
			Labels: map[string]string{},
		},
	}
	cpuNode2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cpu-node-2",
			Labels: map[string]string{
				"node-type": "cpu-only",
			},
		},
	}

	k8sClient := newFakeClient(cpuNode1, cpuNode2)

	gpuInfo, err := DiscoverGPUs(ctx, k8sClient)
	assert.Error(t, err)
	assert.Nil(t, gpuInfo)
	assert.Contains(t, err.Error(), "no nodes with NVIDIA GPU Feature Discovery labels found")
}

func TestInferHardwareSystem(t *testing.T) {
	tests := []struct {
		gpuProduct     string
		expectedSystem string
		description    string
	}{
		{"H100-SXM5-80GB", "h100_sxm", "H100 SXM variant"},
		{"H100-PCIE-80GB", "h100_sxm", "H100 PCIe variant (mapped to SXM)"},
		{"H200-SXM5-141GB", "h200_sxm", "H200 SXM variant"},
		{"A100-SXM4-40GB", "a100_sxm", "A100 SXM variant"},
		{"A100-PCIE-80GB", "a100_sxm", "A100 PCIe variant (mapped to SXM)"},
		{"L40S", "l40s", "L40S"},
		{"NVIDIA L40S", "l40s", "L40S with prefix"},
		{"B200-SXM", "b200_sxm", "B200 SXM"},
		{"GB200", "gb200_sxm", "GB200"},
		{"Tesla V100-SXM2-16GB", "", "V100 (not in mapping)"},
		{"RTX 4090", "", "Consumer GPU (not in mapping)"},
		{"Unknown-GPU", "", "Unknown GPU"},
		{"", "", "Empty string"},
		// GFD product names as seen in real cluster labels (regression for GPUSKU bug)
		{"NVIDIA-B200", "b200_sxm", "B200 with NVIDIA prefix (GFD label format)"},
		{"NVIDIA-H200-SXM5-141GB", "h200_sxm", "H200 with NVIDIA prefix (GFD label format)"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := InferHardwareSystem(tt.gpuProduct)
			assert.Equal(t, tt.expectedSystem, string(result), "Failed for GPU: %s", tt.gpuProduct)
		})
	}
}

func TestInferHardwareSystem_CaseInsensitive(t *testing.T) {
	// Test that inference is case-insensitive
	variants := []string{
		"h100-sxm5-80gb",
		"H100-SXM5-80GB",
		"H100-sxm5-80GB",
		"h100-SXM5-80gb",
	}

	for _, variant := range variants {
		result := InferHardwareSystem(variant)
		assert.Equal(t, "h100_sxm", string(result), "Should handle case variations: %s", variant)
	}
}

func TestInferHardwareSystem_SpacesAndDashes(t *testing.T) {
	// Test that spaces and dashes are normalized
	variants := []string{
		"H100-SXM5-80GB",
		"H100 SXM5 80GB",
		"H100SXM580GB",
		"H100-SXM5 80GB",
	}

	for _, variant := range variants {
		result := InferHardwareSystem(variant)
		assert.Equal(t, "h100_sxm", string(result), "Should normalize spaces/dashes: %s", variant)
	}
}

func TestScrapeMetricsEndpoint(t *testing.T) {
	ctx := context.TODO()

	// Prepare a fake HTTP server to simulate Prometheus metrics
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := fmt.Fprintln(w, `# HELP DCGM_FI_DEV_GPU_TEMP GPU temperature`)
		require.NoError(t, err)
		_, err = fmt.Fprintln(w, `# TYPE DCGM_FI_DEV_GPU_TEMP gauge`)
		require.NoError(t, err)
		_, err = fmt.Fprintln(w, `DCGM_FI_DEV_GPU_TEMP{gpu="0",modelName="NVIDIA A100",Hostname="test-node"} 50`)
		require.NoError(t, err)

		_, err = fmt.Fprintln(w, `# HELP DCGM_FI_DEV_FB_FREE Framebuffer free`)
		require.NoError(t, err)
		_, err = fmt.Fprintln(w, `# TYPE DCGM_FI_DEV_FB_FREE gauge`)
		require.NoError(t, err)
		_, err = fmt.Fprintln(w, `DCGM_FI_DEV_FB_FREE{gpu="0",Hostname="test-node"} 10000`)
		require.NoError(t, err)

		_, err = fmt.Fprintln(w, `# HELP DCGM_FI_DEV_FB_USED Framebuffer used`)
		require.NoError(t, err)
		_, err = fmt.Fprintln(w, `# TYPE DCGM_FI_DEV_FB_USED gauge`)
		require.NoError(t, err)
		_, err = fmt.Fprintln(w, `DCGM_FI_DEV_FB_USED{gpu="0",Hostname="test-node"} 2000`)
		require.NoError(t, err)

		_, err = fmt.Fprintln(w, `# HELP DCGM_FI_DEV_FB_RESERVED Framebuffer reserved`)
		require.NoError(t, err)
		_, err = fmt.Fprintln(w, `# TYPE DCGM_FI_DEV_FB_RESERVED gauge`)
		require.NoError(t, err)
		_, err = fmt.Fprintln(w, `DCGM_FI_DEV_FB_RESERVED{gpu="0",Hostname="test-node"} 500`)
		require.NoError(t, err)
	}))
	defer server.Close()

	t.Run("successful scrape", func(t *testing.T) {
		info, err := ScrapeMetricsEndpoint(ctx, server.URL)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if info == nil {
			t.Fatal("expected non-nil GPUInfo")
		}
	})

	t.Run("404 response", func(t *testing.T) {
		badServer := httptest.NewServer(http.NotFoundHandler())
		defer badServer.Close()

		_, err := ScrapeMetricsEndpoint(ctx, badServer.URL)
		expectedErr := fmt.Sprintf("metrics endpoint %s returned status 404", badServer.URL)
		if err == nil || err.Error() != expectedErr {
			t.Fatalf("expected %q, got %v", expectedErr, err)
		}
	})

	t.Run("invalid metrics", func(t *testing.T) {
		invalidServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := fmt.Fprintln(w, `not a prometheus format`)
			require.NoError(t, err)
		}))
		defer invalidServer.Close()

		_, err := ScrapeMetricsEndpoint(ctx, invalidServer.URL)
		if err == nil {
			t.Fatal("expected parse error, got nil")
		}
	})
}

func TestDiscoverGPUsFromDCGM_CacheHit(t *testing.T) {
	ctx := context.Background()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dcgm-pod",
			Namespace: "default",
			Labels: map[string]string{
				hardware.LabelApp: hardware.LabelValueNvidiaDCGMExporter,
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.1",
		},
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pod).
		Build()

	cache := NewGPUDiscoveryCache()

	callCount := 0

	mockScraper := func(ctx context.Context, endpoint string) (*GPUInfo, error) {
		callCount++
		return &GPUInfo{
			NodeName:    "node-a",
			GPUsPerNode: 4,
			Model:       "A100",
			VRAMPerGPU:  40960,
			MIGEnabled:  false,
			MIGProfiles: map[string]int{},
			System:      "DGX",
		}, nil
	}

	discovery := NewGPUDiscovery(mockScraper)

	// First call → should scrape
	info1, err := discovery.DiscoverGPUsFromDCGM(ctx, k8sClient, cache)
	require.NoError(t, err)
	require.NotNil(t, info1)
	require.Equal(t, 1, callCount)

	// Second call → should hit cache
	info2, err := discovery.DiscoverGPUsFromDCGM(ctx, k8sClient, cache)
	require.NoError(t, err)
	require.NotNil(t, info2)

	// Scrape should NOT be called again
	require.Equal(t, 1, callCount)

	require.Equal(t, info1, info2)
}

func TestDiscoverGPUsFromDCGM_GPUOperatorInstalled_DCgmNotEnabled(t *testing.T) {
	ctx := context.Background()

	gpuOperatorPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-operator-abc",
			Namespace: "gpu-operator",
			Labels: map[string]string{
				hardware.LabelApp: hardware.LabelValueGPUOperator,
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(gpuOperatorPod).
		Build()

	cache := NewGPUDiscoveryCache()

	dummyScraper := func(ctx context.Context, endpoint string) (*GPUInfo, error) {
		return nil, fmt.Errorf("should not be called")
	}

	discovery := NewGPUDiscovery(dummyScraper)

	info, err := discovery.DiscoverGPUsFromDCGM(ctx, k8sClient, cache)

	require.Nil(t, info)
	require.Error(t, err)
	require.Contains(t, err.Error(), "DCGM is not enabled in the GPU Operator")
}

func TestDiscoverGPUsFromDCGM_NoGPUOperator_NoDCGM(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	cache := NewGPUDiscoveryCache()

	dummyScraper := func(ctx context.Context, endpoint string) (*GPUInfo, error) {
		return nil, fmt.Errorf("should not be called")
	}

	discovery := NewGPUDiscovery(dummyScraper)

	info, err := discovery.DiscoverGPUsFromDCGM(ctx, k8sClient, cache)

	require.Nil(t, info)
	require.Error(t, err)

	require.True(
		t,
		strings.Contains(err.Error(), "gpu operator is not installed"),
	)
}

//
// ---- Fake client that forces List error ----
//

type errorListClient struct {
	client.Reader
}

func (e *errorListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return errors.New("forced list error")
}

func TestGetCloudProviderInfo(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name    string
		node    corev1.Node
		want    string
		wantErr bool
	}{
		{
			name: "AKS via providerID",
			node: corev1.Node{
				Spec: corev1.NodeSpec{
					ProviderID: "azure:///subscriptions/xxx/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
				},
			},
			want:    "aks",
			wantErr: false,
		},
		{
			name: "AWS via providerID",
			node: corev1.Node{
				Spec: corev1.NodeSpec{
					ProviderID: "aws:///us-west-2/i-0123456789abcdef0",
				},
			},
			want:    "aws",
			wantErr: false,
		},
		{
			name: "GCP via providerID",
			node: corev1.Node{
				Spec: corev1.NodeSpec{
					ProviderID: "gce://project/zone/instance",
				},
			},
			want:    "gcp",
			wantErr: false,
		},
		{
			name: "AKS via label",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"kubernetes.azure.com/cluster": "mycluster",
					},
				},
			},
			want:    "aks",
			wantErr: false,
		},
		{
			name: "AWS via label",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"eks.amazonaws.com/nodegroup": "ng-1",
					},
				},
			},
			want:    "aws",
			wantErr: false,
		},
		{
			name: "GCP via label",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"cloud.google.com/gke-nodepool": "np-1",
					},
				},
			},
			want:    "gcp",
			wantErr: false,
		},
		{
			name: "Other node",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"custom-label": "foo",
					},
				},
			},
			want:    "other",
			wantErr: false,
		},
		{
			name:    "No nodes",
			node:    corev1.Node{}, // will not add to client
			want:    "unknown",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			var k8sClient client.Reader

			if tt.name != "No nodes" {
				k8sClient = fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&tt.node).
					Build()
			} else {
				k8sClient = fake.NewClientBuilder().
					WithScheme(scheme).
					Build()
			}

			got, err := GetCloudProviderInfo(ctx, k8sClient)
			if (err != nil) != tt.wantErr {
				t.Errorf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
