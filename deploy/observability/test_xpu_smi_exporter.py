#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

from unittest import mock

from deploy.observability.xpu_smi_exporter import MetricsCollector


XPUMD_SAMPLE_1 = """
# HELP hw_gpu_info Information about the GPU device.
# TYPE hw_gpu_info gauge
hw_gpu_info{hw_id="gpu-uuid-0",hw_model="Intel(R) Arc(TM) Pro B60 Graphics",hw_name="gpu-1",hw_vendor="Intel(R) Corporation",pci_bdf="0000:03:00.0",pci_device_id="e211",pci_vendor_id="8086"} 1
hw_gpu_info{hw_id="gpu-uuid-1",hw_model="Intel(R) Arc(TM) Pro B60 Graphics",hw_name="gpu-2",hw_vendor="Intel(R) Corporation",pci_bdf="0000:05:00.0",pci_device_id="e211",pci_vendor_id="8086"} 1
# HELP hw_memory_size_bytes Size of the memory.
# TYPE hw_memory_size_bytes gauge
hw_memory_size_bytes{hw_id="gpu-uuid-0",hw_memory_location="device",hw_memory_type="gddr6",hw_name="mem-1",pci_bdf="0000:03:00.0"} 25669140480
hw_memory_size_bytes{hw_id="gpu-uuid-1",hw_memory_location="device",hw_memory_type="gddr6",hw_name="mem-1",pci_bdf="0000:05:00.0"} 25669140480
# HELP hw_memory_usage_bytes Memory used.
# TYPE hw_memory_usage_bytes gauge
hw_memory_usage_bytes{hw_id="gpu-uuid-0",hw_memory_location="device",hw_memory_type="gddr6",hw_name="mem-1",pci_bdf="0000:03:00.0"} 8589934592
hw_memory_usage_bytes{hw_id="gpu-uuid-1",hw_memory_location="device",hw_memory_type="gddr6",hw_name="mem-1",pci_bdf="0000:05:00.0"} 4294967296
# HELP hw_power_watts Power.
# TYPE hw_power_watts gauge
hw_power_watts{hw_id="gpu-uuid-0",hw_sensor_location="card",hw_name="power-1",pci_bdf="0000:03:00.0"} 31.5
hw_power_watts{hw_id="gpu-uuid-1",hw_sensor_location="card",hw_name="power-1",pci_bdf="0000:05:00.0"} 29.5
# HELP hw_frequency_hertz Frequency.
# TYPE hw_frequency_hertz gauge
hw_frequency_hertz{aggregation="avg",hw_frequency_domain="gpu",hw_id="gpu-uuid-0",hw_name="freq-2",pci_bdf="0000:03:00.0"} 960000000
hw_frequency_hertz{aggregation="avg",hw_frequency_domain="gpu",hw_id="gpu-uuid-1",hw_name="freq-2",pci_bdf="0000:05:00.0"} 480000000
# HELP hw_temperature_celsius Temperature.
# TYPE hw_temperature_celsius gauge
hw_temperature_celsius{hw_id="gpu-uuid-0",hw_sensor_location="gpu",statistic="max",hw_name="temp-2",pci_bdf="0000:03:00.0"} 36
hw_temperature_celsius{hw_id="gpu-uuid-0",hw_sensor_location="memory",statistic="max",hw_name="temp-3",pci_bdf="0000:03:00.0"} 40
hw_temperature_celsius{hw_id="gpu-uuid-1",hw_sensor_location="gpu",statistic="max",hw_name="temp-2",pci_bdf="0000:05:00.0"} 34
hw_temperature_celsius{hw_id="gpu-uuid-1",hw_sensor_location="memory",statistic="max",hw_name="temp-3",pci_bdf="0000:05:00.0"} 39
# HELP hw_gpu_io_bytes_total PCIe bytes.
# TYPE hw_gpu_io_bytes_total counter
hw_gpu_io_bytes_total{hw_id="gpu-uuid-0",network_io_direction="receive",hw_name="gpu-1-pci",pci_bdf="0000:03:00.0"} 1000
hw_gpu_io_bytes_total{hw_id="gpu-uuid-0",network_io_direction="transmit",hw_name="gpu-1-pci",pci_bdf="0000:03:00.0"} 2000
hw_gpu_io_bytes_total{hw_id="gpu-uuid-1",network_io_direction="receive",hw_name="gpu-2-pci",pci_bdf="0000:05:00.0"} 3000
hw_gpu_io_bytes_total{hw_id="gpu-uuid-1",network_io_direction="transmit",hw_name="gpu-2-pci",pci_bdf="0000:05:00.0"} 4000
# HELP hw_memory_io_bytes_total Memory bytes.
# TYPE hw_memory_io_bytes_total counter
hw_memory_io_bytes_total{hw_id="gpu-uuid-0",network_io_direction="receive",hw_memory_location="device",hw_name="mem-1",pci_bdf="0000:03:00.0"} 5000
hw_memory_io_bytes_total{hw_id="gpu-uuid-0",network_io_direction="transmit",hw_memory_location="device",hw_name="mem-1",pci_bdf="0000:03:00.0"} 6000
hw_memory_io_bytes_total{hw_id="gpu-uuid-1",network_io_direction="receive",hw_memory_location="device",hw_name="mem-1",pci_bdf="0000:05:00.0"} 7000
hw_memory_io_bytes_total{hw_id="gpu-uuid-1",network_io_direction="transmit",hw_memory_location="device",hw_name="mem-1",pci_bdf="0000:05:00.0"} 8000
"""

XPUMD_SAMPLE_2 = XPUMD_SAMPLE_1.replace('} 1000', '} 1300').replace('} 2000', '} 2400').replace('} 3000', '} 3600').replace('} 4000', '} 4900').replace('} 5000', '} 5600').replace('} 6000', '} 6900').replace('} 7000', '} 7800').replace('} 8000', '} 9100')


def test_xpumd_adapter_emits_compatibility_metrics():
    payloads = iter([XPUMD_SAMPLE_1, XPUMD_SAMPLE_1, XPUMD_SAMPLE_2])
    timestamps = iter([100.0, 100.0, 100.0, 101.0, 101.0, 101.0])

    with mock.patch.object(
        MetricsCollector,
        "_fetch_xpumd_metrics",
        side_effect=lambda: MetricsCollector._parse_prometheus_text(None, next(payloads)),
    ), mock.patch("deploy.observability.xpu_smi_exporter.time.time", side_effect=lambda: next(timestamps)):
        collector = MetricsCollector(source="xpumd", xpumd_endpoint="http://xpumd:8080/metrics")
        collector.collect()
        collector.collect()

    output = collector.format_prometheus()

    assert 'xpu_device_count{node_name="' in output
    assert 'xpu_device_count{node_name="' in output and "} 2" in output
    assert 'xpu_device_info{device_id="0",device_name="Intel(R) Arc(TM) Pro B60 Graphics"' in output
    assert 'pci_device_id="0xe211"' in output
    assert 'uuid="gpu-uuid-0"' in output
    assert 'xpu_memory_total_bytes{device_id="0",node_name="' in output
    assert 'xpu_memory_used_bytes{device_id="0",node_name="' in output
    assert 'xpu_memory_free_bytes{device_id="0",node_name="' in output
    assert 'xpu_memory_utilization_ratio{device_id="0",node_name="' in output
    assert 'xpu_frequency_mhz{device_id="0",node_name="' in output
    assert 'xpu_power_watts{device_id="0",node_name="' in output
    assert 'xpu_temperature_celsius{device_id="0",location="gpu",node_name="' in output
    assert 'xpu_temperature_celsius{device_id="0",location="memory",node_name="' in output
    assert 'xpu_pcie_read_bytes_per_second{device_id="0",node_name="' in output
    assert 'xpu_pcie_write_bytes_per_second{device_id="0",node_name="' in output
    assert 'xpu_memory_read_bytes_per_second{device_id="0",node_name="' in output
    assert 'xpu_memory_write_bytes_per_second{device_id="0",node_name="' in output
    assert 'xpu_engine_group_compute_engine_util{device_id="0",node_name="' in output
    assert 'xpu_engine_group_copy_engine_util{device_id="0",node_name="' in output
    assert "NaN" in output
