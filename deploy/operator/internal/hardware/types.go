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

// Package hardware provides hardware abstraction for accelerator discovery
// across different vendors (NVIDIA, Intel, AMD, etc.).
//
// This package defines the core types and interfaces for hardware discovery,
// allowing the operator to support multiple accelerator vendors through a
// unified API.
package hardware

import (
	nvidiacomv1beta1 "github.com/ai-dynamo/dynamo/deploy/operator/api/v1beta1"
)

// AcceleratorType represents the type of accelerator hardware.
type AcceleratorType string

const (
	// AcceleratorTypeNVIDIA represents NVIDIA GPUs.
	AcceleratorTypeNVIDIA AcceleratorType = "nvidia"
	// AcceleratorTypeIntel represents Intel XPUs (Gaudi, etc.).
	AcceleratorTypeIntel AcceleratorType = "intel"
	// AcceleratorTypeAMD represents AMD GPUs.
	AcceleratorTypeAMD AcceleratorType = "amd"
	// AcceleratorTypeAuto indicates automatic detection.
	AcceleratorTypeAuto AcceleratorType = "auto"
)

// AcceleratorInfo contains discovered accelerator configuration from cluster nodes.
// This is the unified type that replaces the GPU-specific GPUInfo type from the gpu package.
type AcceleratorInfo struct {
	// Type is the accelerator vendor type (nvidia, intel, amd).
	Type AcceleratorType

	// Model is the accelerator product name (e.g., "H100-SXM5-80GB", "Gaudi3").
	Model string

	// MemoryMB is the memory per accelerator in MiB.
	MemoryMB int

	// CountPerNode is the number of accelerators per node.
	CountPerNode int

	// NodesCount is the number of nodes with this accelerator type.
	NodesCount int

	// CloudProvider is the detected cloud provider (aws, gcp, aks, other, unknown).
	CloudProvider string

	// ResourceName is the Kubernetes resource name for this accelerator
	// (e.g., "nvidia.com/gpu", "intel.com/gpu").
	ResourceName string

	// Labels contains additional labels for node selection.
	Labels map[string]string

	// System is the AIC hardware system identifier (e.g., "h100_sxm", "gaudi3").
	// This maps to GPUSKUType in the API.
	System nvidiacomv1beta1.GPUSKUType

	// NodeName is the name of the node (for single-node discovery results).
	NodeName string

	// MIGEnabled indicates if MIG is enabled (NVIDIA-specific).
	MIGEnabled bool

	// MIGProfiles contains MIG profile counts if MIG is enabled.
	MIGProfiles map[string]int
}

// IsNVIDIA returns true if this accelerator is an NVIDIA GPU.
func (a *AcceleratorInfo) IsNVIDIA() bool {
	return a.Type == AcceleratorTypeNVIDIA
}

// IsIntel returns true if this accelerator is an Intel XPU.
func (a *AcceleratorInfo) IsIntel() bool {
	return a.Type == AcceleratorTypeIntel
}

// IsAMD returns true if this accelerator is an AMD GPU.
func (a *AcceleratorInfo) IsAMD() bool {
	return a.Type == AcceleratorTypeAMD
}

// TotalCount returns the total number of accelerators in the cluster.
func (a *AcceleratorInfo) TotalCount() int {
	return a.CountPerNode * a.NodesCount
}
