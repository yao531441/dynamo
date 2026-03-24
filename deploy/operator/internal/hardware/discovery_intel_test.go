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
	"testing"

	nvidiacomv1beta1 "github.com/ai-dynamo/dynamo/deploy/operator/api/v1beta1"
	"github.com/ai-dynamo/dynamo/deploy/operator/internal/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewIntelDiscovery(t *testing.T) {
	d := NewIntelDiscovery()
	require.NotNil(t, d)
}

func TestIntelDiscovery_Type(t *testing.T) {
	d := NewIntelDiscovery()
	assert.Equal(t, AcceleratorTypeIntel, d.Type())
}

func TestIntelDiscovery_Priority(t *testing.T) {
	d := NewIntelDiscovery()
	assert.Equal(t, 50, d.Priority())
}

func TestIntelDiscovery_Discover(t *testing.T) {
	ctx := context.Background()
	d := NewIntelDiscovery()

	info, err := d.Discover(ctx, nil)

	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "Intel XPU auto-discovery not implemented")
	assert.Contains(t, err.Error(), "spec.hardware.gpuSku")
}

func TestIsIntelAccelerator(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"gaudi3", true},
		{"gaudi2", true},
		{"gaudi", true},
		{"Gaudi3", true},
		{"Gaudi2", true},
		{"Gaudi", true},
		{"HLS-Gaudi3", true},
		{"HLS-Gaudi2", true},
		{"H100-SXM5-80GB", false},
		{"A100", false},
		{"", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := IsIntelAccelerator(tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewAcceleratorInfoFromIntelSKU(t *testing.T) {
	tests := []struct {
		name           string
		sku            nvidiacomv1beta1.GPUSKUType
		expectedModel  string
		expectedMemory int
		expectedType   AcceleratorType
		expectedSystem nvidiacomv1beta1.GPUSKUType
	}{
		{
			name:           "Gaudi3",
			sku:            nvidiacomv1beta1.GPUSKUTypeGaudi3,
			expectedModel:  "Gaudi3",
			expectedMemory: 128 * 1024, // 128 GB HBM3
			expectedType:   AcceleratorTypeIntel,
			expectedSystem: nvidiacomv1beta1.GPUSKUTypeGaudi3,
		},
		{
			name:           "Gaudi2",
			sku:            nvidiacomv1beta1.GPUSKUTypeGaudi2,
			expectedModel:  "Gaudi2",
			expectedMemory: 96 * 1024, // 96 GB HBM2e
			expectedType:   AcceleratorTypeIntel,
			expectedSystem: nvidiacomv1beta1.GPUSKUTypeGaudi2,
		},
		{
			name:           "Unknown SKU",
			sku:            "unknown_intel_sku",
			expectedModel:  "unknown_intel_sku",
			expectedMemory: 0,
			expectedType:   AcceleratorTypeIntel,
			expectedSystem: "unknown_intel_sku",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := NewAcceleratorInfoFromIntelSKU(tt.sku)

			require.NotNil(t, info)
			assert.Equal(t, tt.expectedType, info.Type)
			assert.Equal(t, tt.expectedModel, info.Model)
			assert.Equal(t, tt.expectedMemory, info.MemoryMB)
			assert.Equal(t, consts.KubeResourceGPUIntel, info.ResourceName)
			assert.Equal(t, tt.expectedSystem, info.System)

			// Verify IsIntel returns true
			assert.True(t, info.IsIntel())
		})
	}
}
