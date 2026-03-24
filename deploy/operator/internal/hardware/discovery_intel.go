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

	nvidiacomv1beta1 "github.com/ai-dynamo/dynamo/deploy/operator/api/v1beta1"
	"github.com/ai-dynamo/dynamo/deploy/operator/internal/consts"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IntelDiscovery implements the Discovery interface for Intel XPUs.
// Phase 1: Stub implementation that returns an error for auto-discovery.
// Users must provide manual hardware configuration.
type IntelDiscovery struct{}

// NewIntelDiscovery creates a new Intel discovery instance.
func NewIntelDiscovery() *IntelDiscovery {
	return &IntelDiscovery{}
}

// Type returns the accelerator type for Intel.
func (d *IntelDiscovery) Type() AcceleratorType {
	return AcceleratorTypeIntel
}

// Priority returns the discovery priority for Intel (lower than NVIDIA).
func (d *IntelDiscovery) Priority() int {
	return 50
}

// Discover attempts to discover Intel XPU hardware.
// Phase 1: Returns an error indicating that auto-discovery is not implemented.
// Users must provide manual hardware configuration via spec.hardware.
func (d *IntelDiscovery) Discover(ctx context.Context, k8sClient client.Reader) (*AcceleratorInfo, error) {
	// Phase 1: No auto-discovery for Intel XPUs.
	// Return an error that guides users to provide manual configuration.
	return nil, fmt.Errorf("Intel XPU auto-discovery not implemented; please provide manual hardware configuration via spec.hardware.gpuSku (gaudi3 or gaudi2), spec.hardware.vramMb, and spec.hardware.numGpusPerNode")
}

// IntelAcceleratorModels maps Intel accelerator model names to their system identifiers.
var IntelAcceleratorModels = map[string]string{
	"gaudi3":     "Gaudi3",
	"gaudi2":     "Gaudi2",
	"gaudi":      "Gaudi",
	"Gaudi3":     "Gaudi3",
	"Gaudi2":     "Gaudi2",
	"Gaudi":      "Gaudi",
	"HLS-Gaudi3": "Gaudi3",
	"HLS-Gaudi2": "Gaudi2",
}

// IsIntelAccelerator checks if the given model name is an Intel accelerator.
func IsIntelAccelerator(model string) bool {
	_, ok := IntelAcceleratorModels[model]
	return ok
}

// NewAcceleratorInfoFromIntelSKU creates AcceleratorInfo for an Intel SKU type.
// This is used when users manually specify an Intel accelerator type.
func NewAcceleratorInfoFromIntelSKU(sku nvidiacomv1beta1.GPUSKUType) *AcceleratorInfo {
	// Map SKU to typical configuration
	switch sku {
	case nvidiacomv1beta1.GPUSKUTypeGaudi3:
		return &AcceleratorInfo{
			Type:         AcceleratorTypeIntel,
			Model:        "Gaudi3",
			MemoryMB:     128 * 1024, // 128 GB HBM3
			ResourceName: consts.KubeResourceGPUIntel,
			System:       nvidiacomv1beta1.GPUSKUTypeGaudi3,
		}
	case nvidiacomv1beta1.GPUSKUTypeGaudi2:
		return &AcceleratorInfo{
			Type:         AcceleratorTypeIntel,
			Model:        "Gaudi2",
			MemoryMB:     96 * 1024, // 96 GB HBM2e
			ResourceName: consts.KubeResourceGPUIntel,
			System:       nvidiacomv1beta1.GPUSKUTypeGaudi2,
		}
	default:
		return &AcceleratorInfo{
			Type:         AcceleratorTypeIntel,
			Model:        string(sku),
			ResourceName: consts.KubeResourceGPUIntel,
			System:       sku,
		}
	}
}
