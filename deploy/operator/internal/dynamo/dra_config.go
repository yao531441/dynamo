/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package dynamo

import (
	"strconv"

	"github.com/ai-dynamo/dynamo/deploy/operator/api/v1alpha1"
	"github.com/ai-dynamo/dynamo/deploy/operator/internal/dra"
)

type DRAConfig struct {
	GPUCount        int
	DeviceClassName string
}

func resolveGMSDeviceClass(gms *v1alpha1.GPUMemoryServiceSpec) string {
	if gms == nil || !gms.Enabled {
		return ""
	}
	if gms.DeviceClassName != "" {
		return gms.DeviceClassName
	}
	return dra.DefaultDeviceClassName
}

func ResolveDeviceClassForComponent(spec *v1alpha1.DynamoComponentDeploymentSharedSpec) string {
	if spec == nil {
		return ""
	}
	if deviceClassName := resolveGMSDeviceClass(spec.GPUMemoryService); deviceClassName != "" {
		return deviceClassName
	}
	return spec.DeviceClassName
}

func ResolveDRAConfigForComponent(spec *v1alpha1.DynamoComponentDeploymentSharedSpec) DRAConfig {
	if spec == nil {
		return DRAConfig{}
	}

	gpuCount := 0
	switch {
	case spec.Resources != nil && spec.Resources.Limits != nil && spec.Resources.Limits.GPU != "":
		gpuCount, _ = strconv.Atoi(spec.Resources.Limits.GPU)
	case spec.Resources != nil && spec.Resources.Requests != nil && spec.Resources.Requests.GPU != "":
		gpuCount, _ = strconv.Atoi(spec.Resources.Requests.GPU)
	}
	if gpuCount <= 0 {
		return DRAConfig{}
	}

	deviceClassName := ResolveDeviceClassForComponent(spec)
	if deviceClassName == "" {
		return DRAConfig{}
	}

	return DRAConfig{
		GPUCount:        gpuCount,
		DeviceClassName: deviceClassName,
	}
}

func RequiresDRAForComponent(spec *v1alpha1.DynamoComponentDeploymentSharedSpec) bool {
	if spec == nil {
		return false
	}
	return (spec.GPUMemoryService != nil && spec.GPUMemoryService.Enabled) || spec.DeviceClassName != ""
}
