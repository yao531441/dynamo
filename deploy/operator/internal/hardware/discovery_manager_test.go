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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// mockDiscovery is a mock implementation of the Discovery interface for testing.
type mockDiscovery struct {
	accType   AcceleratorType
	priority  int
	info      *AcceleratorInfo
	err       error
	callCount int
}

func (m *mockDiscovery) Type() AcceleratorType {
	return m.accType
}

func (m *mockDiscovery) Priority() int {
	return m.priority
}

func (m *mockDiscovery) Discover(ctx context.Context, k8sClient client.Reader) (*AcceleratorInfo, error) {
	m.callCount++
	return m.info, m.err
}

func TestNewDiscoveryManager(t *testing.T) {
	// Test that discoverers are sorted by priority (highest first)
	d1 := &mockDiscovery{accType: AcceleratorTypeIntel, priority: 50}
	d2 := &mockDiscovery{accType: AcceleratorTypeNVIDIA, priority: 100}
	d3 := &mockDiscovery{accType: AcceleratorTypeAMD, priority: 30}

	dm := NewDiscoveryManager(d1, d2, d3)

	require.Len(t, dm.discoverers, 3)
	// Should be sorted: NVIDIA (100) -> Intel (50) -> AMD (30)
	assert.Equal(t, AcceleratorTypeNVIDIA, dm.discoverers[0].Type())
	assert.Equal(t, AcceleratorTypeIntel, dm.discoverers[1].Type())
	assert.Equal(t, AcceleratorTypeAMD, dm.discoverers[2].Type())
}

func TestDiscoveryManager_DiscoverAll_Success(t *testing.T) {
	ctx := context.Background()

	expectedInfo := &AcceleratorInfo{
		Type:         AcceleratorTypeNVIDIA,
		Model:        "H100-SXM5-80GB",
		MemoryMB:     81920,
		CountPerNode: 8,
	}

	// NVIDIA succeeds first
	nvidia := &mockDiscovery{
		accType:  AcceleratorTypeNVIDIA,
		priority: 100,
		info:     expectedInfo,
	}
	// Intel fails
	intel := &mockDiscovery{
		accType:  AcceleratorTypeIntel,
		priority: 50,
		err:      errors.New("intel discovery failed"),
	}

	dm := NewDiscoveryManager(intel, nvidia)
	info, err := dm.DiscoverAll(ctx, nil)

	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, expectedInfo, info)
	// NVIDIA should be called (highest priority), Intel should not be called
	assert.Equal(t, 1, nvidia.callCount)
	assert.Equal(t, 0, intel.callCount)
}

func TestDiscoveryManager_DiscoverAll_AllFail(t *testing.T) {
	ctx := context.Background()

	nvidia := &mockDiscovery{
		accType:  AcceleratorTypeNVIDIA,
		priority: 100,
		err:      errors.New("nvidia discovery failed"),
	}
	intel := &mockDiscovery{
		accType:  AcceleratorTypeIntel,
		priority: 50,
		err:      errors.New("intel discovery failed"),
	}

	dm := NewDiscoveryManager(nvidia, intel)
	info, err := dm.DiscoverAll(ctx, nil)

	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "all hardware discovery attempts failed")
	assert.Contains(t, err.Error(), "nvidia discovery failed")
	assert.Contains(t, err.Error(), "intel discovery failed")
}

func TestDiscoveryManager_DiscoverAll_NoDiscoverers(t *testing.T) {
	ctx := context.Background()

	dm := NewDiscoveryManager()
	info, err := dm.DiscoverAll(ctx, nil)

	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "no hardware discoverers registered")
}

func TestDiscoveryManager_DiscoverAll_CacheHit(t *testing.T) {
	ctx := context.Background()

	expectedInfo := &AcceleratorInfo{
		Type:         AcceleratorTypeNVIDIA,
		Model:        "H100-SXM5-80GB",
		MemoryMB:     81920,
		CountPerNode: 8,
	}

	nvidia := &mockDiscovery{
		accType:  AcceleratorTypeNVIDIA,
		priority: 100,
		info:     expectedInfo,
	}

	dm := NewDiscoveryManager(nvidia)

	// First call - should hit discoverer
	info1, err := dm.DiscoverAll(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, nvidia.callCount)

	// Second call - should hit cache (callCount should not increase)
	info2, err := dm.DiscoverAll(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, nvidia.callCount)
	assert.Equal(t, info1, info2)
}

func TestDiscoveryManager_DiscoverByType(t *testing.T) {
	ctx := context.Background()

	nvidiaInfo := &AcceleratorInfo{
		Type:         AcceleratorTypeNVIDIA,
		Model:        "H100-SXM5-80GB",
		MemoryMB:     81920,
		CountPerNode: 8,
	}
	intelInfo := &AcceleratorInfo{
		Type:         AcceleratorTypeIntel,
		Model:        "Gaudi3",
		MemoryMB:     128 * 1024,
		CountPerNode: 8,
	}

	nvidia := &mockDiscovery{
		accType:  AcceleratorTypeNVIDIA,
		priority: 100,
		info:     nvidiaInfo,
	}
	intel := &mockDiscovery{
		accType:  AcceleratorTypeIntel,
		priority: 50,
		info:     intelInfo,
	}

	dm := NewDiscoveryManager(nvidia, intel)

	// Discover NVIDIA
	info, err := dm.DiscoverByType(ctx, nil, AcceleratorTypeNVIDIA)
	require.NoError(t, err)
	assert.Equal(t, nvidiaInfo, info)
	assert.Equal(t, 1, nvidia.callCount)
	assert.Equal(t, 0, intel.callCount)

	// Discover Intel
	info, err = dm.DiscoverByType(ctx, nil, AcceleratorTypeIntel)
	require.NoError(t, err)
	assert.Equal(t, intelInfo, info)
	assert.Equal(t, 1, intel.callCount)
}

func TestDiscoveryManager_DiscoverByType_NotFound(t *testing.T) {
	ctx := context.Background()

	nvidia := &mockDiscovery{
		accType:  AcceleratorTypeNVIDIA,
		priority: 100,
	}

	dm := NewDiscoveryManager(nvidia)

	info, err := dm.DiscoverByType(ctx, nil, AcceleratorTypeAMD)
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "no discoverer registered for accelerator type: amd")
}

func TestDiscoveryManager_DiscoverWithFallback_Auto(t *testing.T) {
	ctx := context.Background()

	expectedInfo := &AcceleratorInfo{
		Type:         AcceleratorTypeNVIDIA,
		Model:        "H100-SXM5-80GB",
		MemoryMB:     81920,
		CountPerNode: 8,
	}

	nvidia := &mockDiscovery{
		accType:  AcceleratorTypeNVIDIA,
		priority: 100,
		info:     expectedInfo,
	}

	dm := NewDiscoveryManager(nvidia)

	// "auto" should use DiscoverAll
	info, err := dm.DiscoverWithFallback(ctx, nil, AcceleratorTypeAuto)
	require.NoError(t, err)
	assert.Equal(t, expectedInfo, info)

	// Empty string should also use DiscoverAll
	nvidia.callCount = 0
	info, err = dm.DiscoverWithFallback(ctx, nil, "")
	require.NoError(t, err)
	assert.Equal(t, expectedInfo, info)
}

func TestDiscoveryManager_DiscoverWithFallback_Specific(t *testing.T) {
	ctx := context.Background()

	intelInfo := &AcceleratorInfo{
		Type:         AcceleratorTypeIntel,
		Model:        "Gaudi3",
		MemoryMB:     128 * 1024,
		CountPerNode: 8,
	}

	nvidia := &mockDiscovery{
		accType:  AcceleratorTypeNVIDIA,
		priority: 100,
		err:      errors.New("should not be called"),
	}
	intel := &mockDiscovery{
		accType:  AcceleratorTypeIntel,
		priority: 50,
		info:     intelInfo,
	}

	dm := NewDiscoveryManager(nvidia, intel)

	// Specific type should only use that discoverer
	info, err := dm.DiscoverWithFallback(ctx, nil, AcceleratorTypeIntel)
	require.NoError(t, err)
	assert.Equal(t, intelInfo, info)
	assert.Equal(t, 0, nvidia.callCount) // NVIDIA should not be called
	assert.Equal(t, 1, intel.callCount)
}

func TestDiscoveryManager_HasDiscoverer(t *testing.T) {
	nvidia := &mockDiscovery{accType: AcceleratorTypeNVIDIA, priority: 100}
	intel := &mockDiscovery{accType: AcceleratorTypeIntel, priority: 50}

	dm := NewDiscoveryManager(nvidia, intel)

	assert.True(t, dm.HasDiscoverer(AcceleratorTypeNVIDIA))
	assert.True(t, dm.HasDiscoverer(AcceleratorTypeIntel))
	assert.False(t, dm.HasDiscoverer(AcceleratorTypeAMD))
	assert.False(t, dm.HasDiscoverer(AcceleratorTypeAuto))
}

func TestDiscoveryManager_RegisteredTypes(t *testing.T) {
	nvidia := &mockDiscovery{accType: AcceleratorTypeNVIDIA, priority: 100}
	intel := &mockDiscovery{accType: AcceleratorTypeIntel, priority: 50}
	amd := &mockDiscovery{accType: AcceleratorTypeAMD, priority: 30}

	dm := NewDiscoveryManager(nvidia, intel, amd)

	types := dm.RegisteredTypes()
	assert.Len(t, types, 3)
	// Should be sorted by priority
	assert.Equal(t, AcceleratorTypeNVIDIA, types[0])
	assert.Equal(t, AcceleratorTypeIntel, types[1])
	assert.Equal(t, AcceleratorTypeAMD, types[2])
}

func TestDiscoveryManager_ClearCache(t *testing.T) {
	ctx := context.Background()

	expectedInfo := &AcceleratorInfo{
		Type:         AcceleratorTypeNVIDIA,
		Model:        "H100-SXM5-80GB",
		MemoryMB:     81920,
		CountPerNode: 8,
	}

	nvidia := &mockDiscovery{
		accType:  AcceleratorTypeNVIDIA,
		priority: 100,
		info:     expectedInfo,
	}

	dm := NewDiscoveryManager(nvidia)

	// First call - cache miss
	_, err := dm.DiscoverAll(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, nvidia.callCount)

	// Clear cache
	dm.ClearCache()

	// Second call - should hit discoverer again
	_, err = dm.DiscoverAll(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, nvidia.callCount)
}

func TestDiscoveryCache_GetSet(t *testing.T) {
	cache := NewDiscoveryCache()

	// Initially empty
	info, ok := cache.Get()
	assert.Nil(t, info)
	assert.False(t, ok)

	// Set value
	expectedInfo := &AcceleratorInfo{
		Type:         AcceleratorTypeNVIDIA,
		Model:        "H100-SXM5-80GB",
		MemoryMB:     81920,
		CountPerNode: 8,
	}
	cache.Set(expectedInfo, 60*time.Second)

	// Get value
	info, ok = cache.Get()
	assert.True(t, ok)
	assert.Equal(t, expectedInfo, info)
}

func TestDiscoveryCache_Expiration(t *testing.T) {
	cache := NewDiscoveryCache()

	expectedInfo := &AcceleratorInfo{
		Type:         AcceleratorTypeNVIDIA,
		Model:        "H100-SXM5-80GB",
		MemoryMB:     81920,
		CountPerNode: 8,
	}

	// Set with very short TTL
	cache.Set(expectedInfo, 1*time.Millisecond)

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Should be expired
	info, ok := cache.Get()
	assert.False(t, ok)
	assert.Nil(t, info)
}

func TestDiscoveryCache_Overwrite(t *testing.T) {
	cache := NewDiscoveryCache()

	info1 := &AcceleratorInfo{Type: AcceleratorTypeNVIDIA, Model: "H100"}
	info2 := &AcceleratorInfo{Type: AcceleratorTypeIntel, Model: "Gaudi3"}

	cache.Set(info1, 60*time.Second)
	cache.Set(info2, 60*time.Second)

	info, ok := cache.Get()
	assert.True(t, ok)
	assert.Equal(t, info2, info) // Should have the second value
}
