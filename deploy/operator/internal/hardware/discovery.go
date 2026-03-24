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
	"sort"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Discovery is the interface for accelerator hardware discovery.
// Each vendor implementation (NVIDIA, Intel, AMD) implements this interface.
type Discovery interface {
	// Type returns the accelerator type this discoverer handles.
	Type() AcceleratorType

	// Discover performs hardware discovery and returns accelerator information.
	// Returns an error if discovery fails or is not supported.
	Discover(ctx context.Context, k8sClient client.Reader) (*AcceleratorInfo, error)

	// Priority determines the order of discovery attempts.
	// Higher priority values are tried first. NVIDIA has highest priority (100).
	Priority() int
}

// DiscoveryManager manages multiple hardware discoverers and provides
// a unified interface for accelerator discovery across vendors.
type DiscoveryManager struct {
	discoverers []Discovery
	cache       *discoveryCache
}

// discoveryCache provides thread-safe caching for discovery results.
type discoveryCache struct {
	mu        sync.RWMutex
	value     *AcceleratorInfo
	expiresAt time.Time
}

// NewDiscoveryCache creates a new discovery cache.
func NewDiscoveryCache() *discoveryCache {
	return &discoveryCache{}
}

// Get returns the cached AcceleratorInfo if it exists and has not expired.
func (c *discoveryCache) Get() (*AcceleratorInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Now().Before(c.expiresAt) && c.value != nil {
		return c.value, true
	}
	return nil, false
}

// Set stores the AcceleratorInfo in the cache with the given TTL.
func (c *discoveryCache) Set(info *AcceleratorInfo, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = info
	c.expiresAt = time.Now().Add(ttl)
}

// NewDiscoveryManager creates a new DiscoveryManager with the given discoverers.
// Discoverers are sorted by priority (highest first) on initialization.
func NewDiscoveryManager(discoverers ...Discovery) *DiscoveryManager {
	// Sort discoverers by priority (highest first)
	sort.Slice(discoverers, func(i, j int) bool {
		return discoverers[i].Priority() > discoverers[j].Priority()
	})
	return &DiscoveryManager{
		discoverers: discoverers,
		cache:       NewDiscoveryCache(),
	}
}

// DiscoverAll attempts to discover accelerators from all registered discoverers,
// returning the first successful result.
//
// The function tries discoverers in priority order (highest first) and returns
// the first successful discovery. If all discoverers fail, an aggregated error
// is returned.
//
// Results are cached for 60 seconds to avoid repeated discovery attempts.
func (m *DiscoveryManager) DiscoverAll(ctx context.Context, k8sClient client.Reader) (*AcceleratorInfo, error) {
	// Check cache first
	if cached, ok := m.cache.Get(); ok {
		return cached, nil
	}

	var errors []error

	for _, d := range m.discoverers {
		info, err := d.Discover(ctx, k8sClient)
		if err != nil {
			errors = append(errors, fmt.Errorf("%s discovery failed: %w", d.Type(), err))
			continue
		}
		// Cache successful result
		m.cache.Set(info, 60*time.Second)
		return info, nil
	}

	// All discoverers failed
	if len(errors) == 0 {
		return nil, fmt.Errorf("no hardware discoverers registered")
	}
	return nil, fmt.Errorf("all hardware discovery attempts failed: %v", errors)
}

// DiscoverByType attempts to discover accelerators of a specific type.
// This bypasses the cache and directly invokes the discoverer for the given type.
//
// Returns an error if no discoverer is registered for the given type.
func (m *DiscoveryManager) DiscoverByType(ctx context.Context, k8sClient client.Reader, accType AcceleratorType) (*AcceleratorInfo, error) {
	for _, d := range m.discoverers {
		if d.Type() == accType {
			return d.Discover(ctx, k8sClient)
		}
	}
	return nil, fmt.Errorf("no discoverer registered for accelerator type: %s", accType)
}

// DiscoverWithFallback attempts to discover accelerators with fallback behavior.
// If accType is "auto", it tries all discoverers in priority order.
// If accType is specific (nvidia, intel, amd), it only tries that discoverer.
//
// When a specific type is requested and fails, the function returns an error
// rather than falling back to other discoverers.
func (m *DiscoveryManager) DiscoverWithFallback(ctx context.Context, k8sClient client.Reader, accType AcceleratorType) (*AcceleratorInfo, error) {
	if accType == AcceleratorTypeAuto || accType == "" {
		return m.DiscoverAll(ctx, k8sClient)
	}
	return m.DiscoverByType(ctx, k8sClient, accType)
}

// HasDiscoverer returns true if a discoverer for the given type is registered.
func (m *DiscoveryManager) HasDiscoverer(accType AcceleratorType) bool {
	for _, d := range m.discoverers {
		if d.Type() == accType {
			return true
		}
	}
	return false
}

// RegisteredTypes returns a list of registered accelerator types.
func (m *DiscoveryManager) RegisteredTypes() []AcceleratorType {
	types := make([]AcceleratorType, len(m.discoverers))
	for i, d := range m.discoverers {
		types[i] = d.Type()
	}
	return types
}

// ClearCache clears the discovery cache.
func (m *DiscoveryManager) ClearCache() {
	m.cache.mu.Lock()
	defer m.cache.mu.Unlock()
	m.cache.value = nil
	m.cache.expiresAt = time.Time{}
}
