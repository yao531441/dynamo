#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Intel XPU Prometheus compatibility exporter.

This exporter preserves Dynamo's existing Intel metric contract while allowing
the data source to be either:

1. `xpu-smi` on the local host (legacy mode)
2. Intel XPUMD Prometheus metrics (adapter mode)

The XPUMD path is intended for a same-pod deployment:

    xpumd container -> exporter sidecar -> Prometheus / operator discovery

The exporter keeps emitting the existing `xpu_device_*` metric families so the
operator and dashboards can continue consuming a stable schema.
"""

import argparse
import json
import logging
import math
import os
import re
import socket
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib import error as urllib_error
from urllib import request as urllib_request

logging.basicConfig(
    level=logging.INFO,
    format="[%(asctime)s] %(levelname)s %(name)s: %(message)s",
)
logger = logging.getLogger("xpu-smi-exporter")

DEFAULT_XPUMD_ENDPOINT = os.environ.get("XPUMD_ENDPOINT", "http://127.0.0.1:8080/metrics")
PROM_LINE_RE = re.compile(r"^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{.*\})?\s+([^\s]+)$")
LABEL_RE = re.compile(r'([a-zA-Z_][a-zA-Z0-9_]*)="((?:\\.|[^"])*)"')

# xpu-smi dump metric IDs
# 0=GPU Util%, 1=Power(W), 2=Freq(MHz), 3=CoreTemp(C), 4=MemTemp(C),
# 5=MemUtil%, 6=MemRead(kB/s), 7=MemWrite(kB/s), 18=MemUsed(MiB),
# 19=PCIeRead(kB/s), 20=PCIeWrite(kB/s),
# 31=ComputeEngGrp%, 32=RenderEngGrp%, 33=MediaEngGrp%, 34=CopyEngGrp%
DUMP_METRICS = "0,1,2,3,4,5,6,7,18,19,20,31,32,33,34"

# Metric name in dump header -> (prometheus_name, help, type, unit_conversion, extra_labels)
# unit_conversion: multiply raw value by this factor
# extra_labels: additional Prometheus labels (e.g. location for temperature)
DUMP_HEADER_MAP = {
    "GPU Utilization (%)": (
        "xpu_gpu_utilization_percent",
        "GPU utilization percentage",
        "gauge",
        1,
        {},
    ),
    "GPU Power (W)": (
        "xpu_power_watts",
        "GPU power consumption in watts",
        "gauge",
        1,
        {},
    ),
    "GPU Frequency (MHz)": (
        "xpu_frequency_mhz",
        "GPU core frequency in MHz",
        "gauge",
        1,
        {},
    ),
    "GPU Core Temperature (Celsius Degree)": (
        "xpu_temperature_celsius",
        "XPU temperature in Celsius",
        "gauge",
        1,
        {"location": "gpu"},
    ),
    "GPU Memory Temperature (Celsius Degree)": (
        "xpu_temperature_celsius",
        "XPU temperature in Celsius",
        "gauge",
        1,
        {"location": "memory"},
    ),
    "GPU Memory Utilization (%)": (
        "xpu_memory_utilization_percent",
        "GPU memory utilization percentage",
        "gauge",
        1,
        {},
    ),
    "GPU Memory Read (kB/s)": (
        "xpu_memory_read_bytes_per_second",
        "GPU memory read throughput in bytes per second",
        "gauge",
        1024,
        {},
    ),
    "GPU Memory Write (kB/s)": (
        "xpu_memory_write_bytes_per_second",
        "GPU memory write throughput in bytes per second",
        "gauge",
        1024,
        {},
    ),
    "GPU Memory Used (MiB)": (
        "xpu_memory_used_bytes",
        "GPU memory used in bytes",
        "gauge",
        1048576,
        {},
    ),
    "PCIe Read (kB/s)": (
        "xpu_pcie_read_bytes_per_second",
        "PCIe read throughput in bytes per second",
        "gauge",
        1024,
        {},
    ),
    "PCIe Write (kB/s)": (
        "xpu_pcie_write_bytes_per_second",
        "PCIe write throughput in bytes per second",
        "gauge",
        1024,
        {},
    ),
    "Compute engine group utilization (%)": (
        "xpu_engine_group_compute_engine_util",
        "Compute engine group utilization percentage",
        "gauge",
        1,
        {},
    ),
    "Render engine group utilization (%)": (
        "xpu_engine_group_render_engine_util",
        "Render engine group utilization percentage",
        "gauge",
        1,
        {},
    ),
    "Media engine group utilization (%)": (
        "xpu_engine_group_media_engine_util",
        "Media engine group utilization percentage",
        "gauge",
        1,
        {},
    ),
    "Copy engine group utilization (%)": (
        "xpu_engine_group_copy_engine_util",
        "Copy engine group utilization percentage",
        "gauge",
        1,
        {},
    ),
}

ENGINE_GROUP_PLACEHOLDERS = (
    (
        "xpu_engine_group_compute_engine_util",
        "Compute engine group utilization percentage",
    ),
    (
        "xpu_engine_group_render_engine_util",
        "Render engine group utilization percentage",
    ),
    (
        "xpu_engine_group_media_engine_util",
        "Media engine group utilization percentage",
    ),
    (
        "xpu_engine_group_copy_engine_util",
        "Copy engine group utilization percentage",
    ),
)


class MetricsCollector:
    """Collects XPU metrics from either xpu-smi or XPUMD."""

    XPU_SMI_CMD = os.environ.get("XPU_SMI_PATH", "xpu-smi")

    def __init__(
        self,
        interval: int = 5,
        source: str = "auto",
        xpumd_endpoint: str = DEFAULT_XPUMD_ENDPOINT,
    ):
        self._lock = threading.Lock()
        self._metrics: dict = {}
        self._devices: list = []
        self._device_info: dict = {}
        self._device_memory_total: dict = {}
        self._device_lookup: dict = {}
        self._counter_state: dict = {}
        self._node_name = os.environ.get("NODE_NAME") or socket.gethostname()
        self._interval = interval
        self._source_mode = source
        self._active_source = None
        self._xpumd_endpoint = xpumd_endpoint
        self._initialize_source()

    def _initialize_source(self):
        if self._source_mode in {"auto", "xpumd"}:
            try:
                self._discover_devices_from_xpumd()
                if self._devices:
                    self._active_source = "xpumd"
                    logger.info(
                        "Using XPUMD adapter mode against %s (%d device(s))",
                        self._xpumd_endpoint,
                        len(self._devices),
                    )
                    return
            except Exception as exc:
                if self._source_mode == "xpumd":
                    logger.error("XPUMD mode initialization failed: %s", exc)
                    self._devices = []
                    return
                logger.info("XPUMD auto-detection failed, falling back to xpu-smi: %s", exc)

        self._active_source = "xpu-smi"
        self._discover_devices_from_xpu_smi()
        if self._devices:
            logger.info("Using legacy xpu-smi mode (%d device(s))", len(self._devices))

    def wait_for_devices(self, retries: int, delay: int) -> bool:
        for attempt in range(1, retries + 1):
            if self._devices:
                return True
            logger.info(
                "Waiting for XPU devices to become available (attempt %d/%d)",
                attempt,
                retries,
            )
            time.sleep(delay)
            self._initialize_source()
        return bool(self._devices)

    def _discover_devices_from_xpu_smi(self):
        try:
            result = subprocess.run(
                [self.XPU_SMI_CMD, "discovery", "-j"],
                capture_output=True,
                text=True,
                timeout=10,
                check=False,
            )
            data = json.loads(result.stdout)
            self._devices = []
            self._device_info = {}
            self._device_lookup = {}
            self._device_memory_total = {}
            for device in data.get("device_list", []):
                if "device_id" not in device:
                    continue
                dev_id = str(device["device_id"])
                self._devices.append(dev_id)
                self._device_info[dev_id] = {
                    "device_name": device.get("device_name", ""),
                    "pci_device_id": self._normalize_pci_device_id(
                        device.get("pci_device_id", "")
                    ),
                    "uuid": device.get("uuid", ""),
                    "vendor_name": device.get("vendor_name", ""),
                }
                self._device_lookup[dev_id] = dev_id
            for dev_id in self._devices:
                self._get_device_memory_total_from_xpu_smi(dev_id)
            logger.info("Discovered %d XPU device(s): %s", len(self._devices), self._devices)
        except Exception as exc:
            logger.error("Failed to discover devices via xpu-smi: %s", exc)
            self._devices = []

    def _get_device_memory_total_from_xpu_smi(self, device_id: str):
        try:
            result = subprocess.run(
                [self.XPU_SMI_CMD, "discovery", "-d", device_id, "-j"],
                capture_output=True,
                text=True,
                timeout=10,
                check=False,
            )
            data = json.loads(result.stdout)
            total = int(data.get("memory_physical_size_byte", 0))
            self._device_memory_total[device_id] = total
        except Exception as exc:
            logger.warning("Failed to get xpu-smi memory total for device %s: %s", device_id, exc)

    def _fetch_xpumd_metrics(self) -> dict:
        req = urllib_request.Request(
            self._xpumd_endpoint,
            headers={"Accept": "text/plain; version=0.0.4"},
        )
        try:
            with urllib_request.urlopen(req, timeout=10) as resp:
                payload = resp.read().decode("utf-8")
        except urllib_error.URLError as exc:
            raise RuntimeError(f"failed to fetch XPUMD metrics from {self._xpumd_endpoint}: {exc}") from exc
        return self._parse_prometheus_text(payload)

    def _discover_devices_from_xpumd(self, families: dict | None = None):
        if families is None:
            families = self._fetch_xpumd_metrics()
        info_series = families.get("hw_gpu_info", [])
        if not info_series:
            raise RuntimeError(f"XPUMD endpoint {self._xpumd_endpoint} exposed no hw_gpu_info metrics")

        self._devices = []
        self._device_info = {}
        self._device_memory_total = {}
        self._device_lookup = {}

        def sort_key(series: dict) -> tuple:
            labels = series["labels"]
            return (
                labels.get("pci_bdf", ""),
                labels.get("hw_id", ""),
            )

        for index, series in enumerate(sorted(info_series, key=sort_key)):
            labels = series["labels"]
            device_id = str(index)
            hw_id = labels.get("hw_id") or labels.get("pci_bdf") or device_id
            pci_device_id = self._normalize_pci_device_id(labels.get("pci_device_id", ""))
            self._devices.append(device_id)
            self._device_lookup[hw_id] = device_id
            pci_bdf = labels.get("pci_bdf")
            if pci_bdf:
                self._device_lookup[pci_bdf] = device_id
            self._device_info[device_id] = {
                "device_name": labels.get("hw_model") or labels.get("hw_name", ""),
                "pci_device_id": pci_device_id,
                "uuid": hw_id,
                "vendor_name": labels.get("hw_vendor", ""),
            }

        for series in families.get("hw_memory_size_bytes", []):
            labels = series["labels"]
            if labels.get("hw_memory_location") not in {"", "device"}:
                continue
            device_id = self._lookup_device_id(labels)
            if device_id is None:
                continue
            self._device_memory_total[device_id] = int(series["value"])

    def _lookup_device_id(self, labels: dict) -> str | None:
        for key in ("hw_id", "pci_bdf"):
            value = labels.get(key)
            if value and value in self._device_lookup:
                return self._device_lookup[value]
        return None

    def _parse_prometheus_text(self, payload: str) -> dict:
        families: dict[str, list[dict]] = {}
        for line in payload.splitlines():
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            match = PROM_LINE_RE.match(line)
            if not match:
                continue
            metric_name = match.group(1)
            label_block = match.group(2) or ""
            value_str = match.group(3)
            try:
                value = float(value_str)
            except ValueError:
                continue
            labels = {}
            if label_block:
                for key, raw in LABEL_RE.findall(label_block):
                    labels[key] = bytes(raw, "utf-8").decode("unicode_escape")
            families.setdefault(metric_name, []).append({"labels": labels, "value": value})
        return families

    def _metric_entry(
        self,
        name: str,
        value: float,
        help_text: str,
        labels: dict,
        metric_type: str = "gauge",
    ) -> dict:
        return {
            "name": name,
            "value": value,
            "help": help_text,
            "type": metric_type,
            "labels": labels,
        }

    def _normalize_pci_device_id(self, value: str) -> str:
        value = str(value).strip()
        if not value:
            return ""
        if value.lower().startswith("0x"):
            return value.lower()
        return f"0x{value.lower()}"

    def _series_to_value_map(self, series_list: list[dict], location_filter: str | None = None) -> dict:
        results = {}
        for series in series_list:
            labels = series["labels"]
            if location_filter is not None and labels.get("hw_memory_location") not in {
                location_filter,
                "",
            }:
                continue
            device_id = self._lookup_device_id(labels)
            if device_id is None:
                continue
            results[device_id] = series["value"]
        return results

    def _select_frequency(self, families: dict) -> dict:
        results = {}
        for series in families.get("hw_frequency_hertz", []):
            labels = series["labels"]
            if labels.get("hw_frequency_domain") != "gpu":
                continue
            if labels.get("aggregation") != "avg":
                continue
            device_id = self._lookup_device_id(labels)
            if device_id is None:
                continue
            results[device_id] = series["value"] / 1_000_000.0
        return results

    def _select_power(self, families: dict) -> dict:
        sensor_priority = {"card": 0, "package": 1, "gpu": 2, "memory": 3}
        results = {}
        for series in families.get("hw_power_watts", []):
            labels = series["labels"]
            device_id = self._lookup_device_id(labels)
            if device_id is None:
                continue
            priority = sensor_priority.get(labels.get("hw_sensor_location", ""), 100)
            current = results.get(device_id)
            if current is None or priority < current[0]:
                results[device_id] = (priority, series["value"])
        return {device_id: value for device_id, (_, value) in results.items()}

    def _select_temperature(self, families: dict, sensor_location: str) -> dict:
        results = {}
        for series in families.get("hw_temperature_celsius", []):
            labels = series["labels"]
            if labels.get("hw_sensor_location") != sensor_location:
                continue
            if labels.get("statistic") not in {"", "max"}:
                continue
            device_id = self._lookup_device_id(labels)
            if device_id is None:
                continue
            results[device_id] = series["value"]
        return results

    def _compute_counter_rates(self, families: dict, metric_name: str) -> dict:
        now = time.time()
        results = {}
        for series in families.get(metric_name, []):
            labels = series["labels"]
            device_id = self._lookup_device_id(labels)
            if device_id is None:
                continue
            direction = labels.get("network_io_direction")
            if not direction:
                continue
            state_key = (metric_name, device_id, direction)
            current_value = series["value"]
            previous = self._counter_state.get(state_key)
            self._counter_state[state_key] = (current_value, now)
            if previous is None:
                continue
            previous_value, previous_time = previous
            elapsed = now - previous_time
            if elapsed <= 0 or current_value < previous_value:
                continue
            results[(device_id, direction)] = (current_value - previous_value) / elapsed
        return results

    def _collect_from_xpu_smi_dump(self, device_id: str) -> dict:
        metrics = {}
        try:
            result = subprocess.run(
                [
                    self.XPU_SMI_CMD,
                    "dump",
                    "-d",
                    device_id,
                    "-m",
                    DUMP_METRICS,
                    "-n",
                    "1",
                ],
                capture_output=True,
                text=True,
                timeout=15,
                check=False,
            )
            lines = result.stdout.strip().split("\n")
            if len(lines) < 2:
                return metrics
            headers = [item.strip() for item in lines[0].split(",")]
            values = [item.strip() for item in lines[-1].split(",")]
            if len(headers) != len(values):
                logger.warning("Header/value count mismatch for xpu-smi dump: %s vs %s", len(headers), len(values))
                return metrics
            for index in range(2, len(headers)):
                header = headers[index]
                raw_value = values[index]
                if raw_value in {"", "N/A"}:
                    continue
                mapping = DUMP_HEADER_MAP.get(header)
                if not mapping:
                    continue
                prom_name, help_text, metric_type, conversion, extra_labels = mapping
                try:
                    value = float(raw_value) * conversion
                except ValueError:
                    continue
                labels = {"device_id": device_id, "node_name": self._node_name, **extra_labels}
                label_suffix = "_".join(f"{k}={v}" for k, v in sorted(extra_labels.items()))
                metric_key = f"{prom_name}:{label_suffix}" if label_suffix else prom_name
                metrics[metric_key] = self._metric_entry(
                    prom_name, value, help_text, labels, metric_type
                )
        except subprocess.TimeoutExpired:
            logger.warning("xpu-smi dump timed out for device %s", device_id)
        except Exception as exc:
            logger.warning("Error collecting dump metrics for device %s: %s", device_id, exc)
        return metrics

    def _collect_from_xpu_smi_stats(self, device_id: str) -> dict:
        metrics = {}
        try:
            result = subprocess.run(
                [self.XPU_SMI_CMD, "stats", "-d", device_id, "-j"],
                capture_output=True,
                text=True,
                timeout=10,
                check=False,
            )
            data = json.loads(result.stdout)
            labels = {"device_id": device_id, "node_name": self._node_name}
            for entry in data.get("device_level", []):
                if entry.get("metrics_type") != "XPUM_STATS_POWER":
                    continue
                value = entry.get("value")
                if value is None:
                    continue
                metrics["xpu_power_watts"] = self._metric_entry(
                    "xpu_power_watts",
                    float(value),
                    "GPU power consumption in watts",
                    labels,
                )

            tile_data = data.get("tile_level", [])
            if tile_data:
                mem_used_sum = 0.0
                mem_util_sum = 0.0
                freq_sum = 0.0
                tile_count = 0
                for tile in tile_data:
                    tile_count += 1
                    for entry in tile.get("data_list", []):
                        value = entry.get("value")
                        if value is None:
                            continue
                        metrics_type = entry.get("metrics_type", "")
                        if metrics_type == "XPUM_STATS_MEMORY_USED":
                            mem_used_sum += float(value)
                        elif metrics_type == "XPUM_STATS_MEMORY_UTILIZATION":
                            mem_util_sum += float(value)
                        elif metrics_type == "XPUM_STATS_GPU_FREQUENCY":
                            freq_sum += float(value)

                if tile_count > 0:
                    mem_used_bytes = mem_used_sum * 1048576
                    metrics["xpu_memory_used_bytes"] = self._metric_entry(
                        "xpu_memory_used_bytes",
                        mem_used_bytes,
                        "GPU memory used in bytes",
                        labels,
                    )
                    total = self._device_memory_total.get(device_id, 0)
                    if total > 0:
                        metrics["xpu_memory_free_bytes"] = self._metric_entry(
                            "xpu_memory_free_bytes",
                            max(0, total - mem_used_bytes),
                            "GPU memory free in bytes",
                            labels,
                        )
                        metrics["xpu_memory_utilization_ratio"] = self._metric_entry(
                            "xpu_memory_utilization_ratio",
                            min(max(mem_used_bytes / total, 0.0), 1.0),
                            "GPU memory utilization as a ratio between 0 and 1",
                            labels,
                        )
                    metrics["xpu_frequency_mhz"] = self._metric_entry(
                        "xpu_frequency_mhz",
                        freq_sum / tile_count,
                        "GPU core frequency in MHz",
                        labels,
                    )
                    if mem_util_sum > 0:
                        metrics["xpu_memory_utilization_percent"] = self._metric_entry(
                            "xpu_memory_utilization_percent",
                            mem_util_sum / tile_count,
                            "GPU memory utilization percentage",
                            labels,
                        )
        except subprocess.TimeoutExpired:
            logger.warning("xpu-smi stats timed out for device %s", device_id)
        except Exception as exc:
            logger.warning("Error collecting stats for device %s: %s", device_id, exc)
        return metrics

    def _collect_from_xpumd(self):
        families = self._fetch_xpumd_metrics()
        self._discover_devices_from_xpumd(families)

        all_metrics = {
            "xpu_device_info": [],
            "xpu_memory_total_bytes": [],
            "xpu_device_count": [
                self._metric_entry(
                    "xpu_device_count",
                    len(self._devices),
                    "Number of Intel XPUs visible on the node",
                    {"node_name": self._node_name},
                )
            ],
        }

        power_by_device = self._select_power(families)
        freq_by_device = self._select_frequency(families)
        total_memory_by_device = self._series_to_value_map(
            families.get("hw_memory_size_bytes", []), location_filter="device"
        )
        used_memory_by_device = self._series_to_value_map(
            families.get("hw_memory_usage_bytes", []), location_filter="device"
        )
        gpu_temp_by_device = self._select_temperature(families, "gpu")
        memory_temp_by_device = self._select_temperature(families, "memory")
        pcie_rates = self._compute_counter_rates(families, "hw_gpu_io_bytes_total")
        memory_rates = self._compute_counter_rates(families, "hw_memory_io_bytes_total")

        for device_id in self._devices:
            info = self._device_info.get(device_id, {})
            labels = {
                "device_id": str(device_id),
                "device_name": info.get("device_name", ""),
                "pci_device_id": info.get("pci_device_id", ""),
                "uuid": info.get("uuid", ""),
                "vendor_name": info.get("vendor_name", ""),
                "node_name": self._node_name,
            }
            all_metrics["xpu_device_info"].append(
                self._metric_entry(
                    "xpu_device_info",
                    1,
                    "Static device identity for an Intel XPU",
                    labels,
                )
            )

            total_memory = int(total_memory_by_device.get(device_id, self._device_memory_total.get(device_id, 0)))
            if total_memory > 0:
                all_metrics["xpu_memory_total_bytes"].append(
                    self._metric_entry(
                        "xpu_memory_total_bytes",
                        total_memory,
                        "Total physical memory of an Intel XPU in bytes",
                        {"device_id": str(device_id), "node_name": self._node_name},
                    )
                )

            compat_labels = {"device_id": str(device_id), "node_name": self._node_name}

            if device_id in power_by_device:
                all_metrics.setdefault("xpu_power_watts", []).append(
                    self._metric_entry(
                        "xpu_power_watts",
                        power_by_device[device_id],
                        "GPU power consumption in watts",
                        compat_labels,
                    )
                )
            if device_id in freq_by_device:
                all_metrics.setdefault("xpu_frequency_mhz", []).append(
                    self._metric_entry(
                        "xpu_frequency_mhz",
                        freq_by_device[device_id],
                        "GPU core frequency in MHz",
                        compat_labels,
                    )
                )
            if device_id in used_memory_by_device:
                used_memory = used_memory_by_device[device_id]
                all_metrics.setdefault("xpu_memory_used_bytes", []).append(
                    self._metric_entry(
                        "xpu_memory_used_bytes",
                        used_memory,
                        "GPU memory used in bytes",
                        compat_labels,
                    )
                )
                if total_memory > 0:
                    all_metrics.setdefault("xpu_memory_free_bytes", []).append(
                        self._metric_entry(
                            "xpu_memory_free_bytes",
                            max(0, total_memory - used_memory),
                            "GPU memory free in bytes",
                            compat_labels,
                        )
                    )
                    all_metrics.setdefault("xpu_memory_utilization_ratio", []).append(
                        self._metric_entry(
                            "xpu_memory_utilization_ratio",
                            min(max(used_memory / total_memory, 0.0), 1.0),
                            "GPU memory utilization as a ratio between 0 and 1",
                            compat_labels,
                        )
                    )
            if device_id in gpu_temp_by_device:
                all_metrics.setdefault("xpu_temperature_celsius:location=gpu", []).append(
                    self._metric_entry(
                        "xpu_temperature_celsius",
                        gpu_temp_by_device[device_id],
                        "XPU temperature in Celsius",
                        {**compat_labels, "location": "gpu"},
                    )
                )
            if device_id in memory_temp_by_device:
                all_metrics.setdefault("xpu_temperature_celsius:location=memory", []).append(
                    self._metric_entry(
                        "xpu_temperature_celsius",
                        memory_temp_by_device[device_id],
                        "XPU temperature in Celsius",
                        {**compat_labels, "location": "memory"},
                    )
                )
            read_rate = pcie_rates.get((device_id, "receive"))
            if read_rate is not None:
                all_metrics.setdefault("xpu_pcie_read_bytes_per_second", []).append(
                    self._metric_entry(
                        "xpu_pcie_read_bytes_per_second",
                        read_rate,
                        "PCIe read throughput in bytes per second",
                        compat_labels,
                    )
                )
            write_rate = pcie_rates.get((device_id, "transmit"))
            if write_rate is not None:
                all_metrics.setdefault("xpu_pcie_write_bytes_per_second", []).append(
                    self._metric_entry(
                        "xpu_pcie_write_bytes_per_second",
                        write_rate,
                        "PCIe write throughput in bytes per second",
                        compat_labels,
                    )
                )
            memory_read_rate = memory_rates.get((device_id, "receive"))
            if memory_read_rate is not None:
                all_metrics.setdefault("xpu_memory_read_bytes_per_second", []).append(
                    self._metric_entry(
                        "xpu_memory_read_bytes_per_second",
                        memory_read_rate,
                        "GPU memory read throughput in bytes per second",
                        compat_labels,
                    )
                )
            memory_write_rate = memory_rates.get((device_id, "transmit"))
            if memory_write_rate is not None:
                all_metrics.setdefault("xpu_memory_write_bytes_per_second", []).append(
                    self._metric_entry(
                        "xpu_memory_write_bytes_per_second",
                        memory_write_rate,
                        "GPU memory write throughput in bytes per second",
                        compat_labels,
                    )
                )
            for metric_name, help_text in ENGINE_GROUP_PLACEHOLDERS:
                all_metrics.setdefault(metric_name, []).append(
                    self._metric_entry(
                        metric_name,
                        math.nan,
                        help_text,
                        compat_labels,
                    )
                )

        with self._lock:
            self._metrics = all_metrics

    def _collect_from_xpu_smi(self):
        all_metrics = {
            "xpu_device_info": [],
            "xpu_memory_total_bytes": [],
            "xpu_device_count": [
                self._metric_entry(
                    "xpu_device_count",
                    len(self._devices),
                    "Number of Intel XPUs visible on the node",
                    {"node_name": self._node_name},
                )
            ],
        }
        for device_id in self._devices:
            info = self._device_info.get(device_id, {})
            all_metrics["xpu_device_info"].append(
                self._metric_entry(
                    "xpu_device_info",
                    1,
                    "Static device identity for an Intel XPU",
                    {
                        "device_id": str(device_id),
                        "device_name": info.get("device_name", ""),
                        "pci_device_id": info.get("pci_device_id", ""),
                        "uuid": info.get("uuid", ""),
                        "vendor_name": info.get("vendor_name", ""),
                        "node_name": self._node_name,
                    },
                )
            )
            total_memory = self._device_memory_total.get(device_id)
            if total_memory is not None:
                all_metrics["xpu_memory_total_bytes"].append(
                    self._metric_entry(
                        "xpu_memory_total_bytes",
                        total_memory,
                        "Total physical memory of an Intel XPU in bytes",
                        {"device_id": str(device_id), "node_name": self._node_name},
                    )
                )

        for device_id in self._devices:
            dump_metrics = self._collect_from_xpu_smi_dump(device_id)
            stats_metrics = self._collect_from_xpu_smi_stats(device_id)
            merged = {**stats_metrics, **dump_metrics}
            if "xpu_memory_used_bytes" in stats_metrics:
                merged["xpu_memory_used_bytes"] = stats_metrics["xpu_memory_used_bytes"]
            if "xpu_memory_free_bytes" in stats_metrics:
                merged["xpu_memory_free_bytes"] = stats_metrics["xpu_memory_free_bytes"]
            for name, data in merged.items():
                all_metrics.setdefault(name, []).append(data)

        with self._lock:
            self._metrics = all_metrics

    def start_background_collection(self):
        def loop():
            while True:
                try:
                    self.collect()
                except Exception as exc:
                    logger.error("Background collection error: %s", exc)
                time.sleep(self._interval)

        thread = threading.Thread(target=loop, daemon=True)
        thread.start()
        logger.info(
            "Background collection started (interval=%ss, source=%s)",
            self._interval,
            self._active_source,
        )

    def collect(self):
        if self._active_source == "xpumd":
            self._collect_from_xpumd()
            return
        self._collect_from_xpu_smi()

    def format_prometheus(self) -> str:
        with self._lock:
            metrics = self._metrics.copy()

        grouped: dict = {}
        for key, entries in metrics.items():
            for entry in entries:
                metric_name = entry.get("name", key)
                grouped.setdefault(metric_name, []).append(entry)

        lines = []
        for metric_name, entries in sorted(grouped.items()):
            if not entries:
                continue
            first = entries[0]
            lines.append(f"# HELP {metric_name} {first['help']}")
            lines.append(f"# TYPE {metric_name} {first['type']}")
            for entry in entries:
                label_parts = ",".join(
                    f'{key}="{value}"' for key, value in sorted(entry["labels"].items())
                )
                value = entry["value"]
                if isinstance(value, float) and math.isnan(value):
                    value = "NaN"
                lines.append(f"{metric_name}{{{label_parts}}} {value}")
        lines.append("")
        return "\n".join(lines)


class MetricsHandler(BaseHTTPRequestHandler):
    collector: MetricsCollector = None

    def do_GET(self):
        try:
            if self.path in {"/metrics", "/"}:
                output = self.collector.format_prometheus()
                self.send_response(200)
                self.send_header(
                    "Content-Type", "text/plain; version=0.0.4; charset=utf-8"
                )
                self.end_headers()
                self.wfile.write(output.encode("utf-8"))
            elif self.path == "/healthz":
                self.send_response(200)
                self.send_header("Content-Type", "text/plain")
                self.end_headers()
                self.wfile.write(b"ok\n")
            else:
                self.send_response(404)
                self.end_headers()
        except BrokenPipeError:
            pass

    def log_message(self, fmt, *args):
        pass


def main():
    parser = argparse.ArgumentParser(description="Intel XPU Prometheus compatibility exporter")
    parser.add_argument(
        "--port",
        type=int,
        default=9966,
        help="Port to expose Prometheus metrics on (default: 9966)",
    )
    parser.add_argument(
        "--interval",
        type=int,
        default=5,
        help="Seconds between background metric collections (default: 5)",
    )
    parser.add_argument(
        "--source",
        choices=["auto", "xpu-smi", "xpumd"],
        default=os.environ.get("XPU_EXPORTER_SOURCE", "auto"),
        help="Metric source backend: auto, xpu-smi, or xpumd (default: auto)",
    )
    parser.add_argument(
        "--xpumd-endpoint",
        default=DEFAULT_XPUMD_ENDPOINT,
        help=f"XPUMD Prometheus endpoint (default: {DEFAULT_XPUMD_ENDPOINT})",
    )
    parser.add_argument(
        "--startup-retries",
        type=int,
        default=12,
        help="Number of retries while waiting for the source backend to expose devices (default: 12)",
    )
    parser.add_argument(
        "--startup-retry-delay",
        type=int,
        default=5,
        help="Seconds to wait between startup retries (default: 5)",
    )
    args = parser.parse_args()

    collector = MetricsCollector(
        interval=args.interval,
        source=args.source,
        xpumd_endpoint=args.xpumd_endpoint,
    )
    if not collector._devices and args.startup_retries > 0:
        collector.wait_for_devices(args.startup_retries, args.startup_retry_delay)
    if not collector._devices:
        logger.error("No XPU devices found for source %s. Exiting.", args.source)
        sys.exit(1)

    collector.collect()
    initial = collector.format_prometheus()
    logger.info(
        "Initial collection complete from %s, %d bytes of metrics",
        collector._active_source,
        len(initial),
    )

    collector.start_background_collection()
    MetricsHandler.collector = collector

    server = HTTPServer(("0.0.0.0", args.port), MetricsHandler)
    logger.info(
        "Serving XPU metrics on http://0.0.0.0:%d/metrics (source=%s)",
        args.port,
        collector._active_source,
    )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        logger.info("Shutting down exporter")
        server.shutdown()


if __name__ == "__main__":
    main()
