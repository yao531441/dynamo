# SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for non-NVIDIA accelerator device-class derivation.

Covers ``apply_accelerator_device_class`` (the dgd_generation helper that maps
``gpuSku`` -> worker ``deviceClassName`` + accelerator env) and the
``profile_sla._apply_device_class_to_final_config`` orchestration wrapper.

Red line: NVIDIA-observable behaviour must not change. These tests assert that
NVIDIA / unknown SKUs and mocker deployments are strict no-ops.
"""

import pytest

pytestmark = [
    pytest.mark.pre_merge,
    pytest.mark.gpu_0,
    pytest.mark.unit,
    pytest.mark.planner,
]

try:
    from dynamo.profiler.profile_sla import _apply_device_class_to_final_config
    from dynamo.profiler.utils.dgd_generation import apply_accelerator_device_class
    from dynamo.profiler.utils.dgdr_v1beta1_types import (
        DynamoGraphDeploymentRequestSpec,
        FeaturesSpec,
        GPUSKUType,
        HardwareSpec,
        MockerSpec,
        SLASpec,
        WorkloadSpec,
    )
except ImportError as e:
    pytest.skip(f"Skip (missing dependency): {e}", allow_module_level=True)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _worker(**extra) -> dict:
    svc = {"componentType": "worker", "replicas": 1}
    svc.update(extra)
    return svc


def _config(**services) -> dict:
    """Build a minimal v1alpha1-style DGD config dict around *services*."""
    if not services:
        services = {
            "Frontend": {"componentType": "frontend", "replicas": 1},
            "VllmDecodeWorker": _worker(),
        }
    return {
        "apiVersion": "nvidia.com/v1alpha1",
        "kind": "DynamoGraphDeployment",
        "spec": {"services": services},
    }


def _workers(config: dict) -> dict:
    return config["spec"]["services"]


def _env_of(svc: dict) -> list:
    return svc.get("extraPodSpec", {}).get("mainContainer", {}).get("env", [])


def _env_value(svc: dict, name: str):
    for e in _env_of(svc):
        if e.get("name") == name:
            return e.get("value")
    return None


def _make_dgdr(**overrides) -> DynamoGraphDeploymentRequestSpec:
    base = dict(
        model="Qwen/Qwen3-0.6B",
        backend="vllm",
        image="nvcr.io/nvidia/ai-dynamo/vllm-runtime:latest",
        hardware=HardwareSpec(gpuSku="b60", totalGpus=2, numGpusPerNode=2),
        workload=WorkloadSpec(isl=4000, osl=1000),
        sla=SLASpec(ttft=2000.0, itl=50.0),
    )
    base.update(overrides)
    return DynamoGraphDeploymentRequestSpec(**base)


# ---------------------------------------------------------------------------
# apply_accelerator_device_class — Intel / AMD
# ---------------------------------------------------------------------------


class TestApplyAcceleratorDeviceClassNonNvidia:
    def test_b60_sets_intel_device_class_and_xpu_env(self):
        cfg = _config(Worker=_worker())
        apply_accelerator_device_class(cfg, "b60")
        worker = _workers(cfg)["Worker"]
        assert worker["deviceClassName"] == "gpu.intel.com"
        assert _env_value(worker, "VLLM_TARGET_DEVICE") == "xpu"

    def test_b60_enum_value_accepted(self):
        cfg = _config(Worker=_worker())
        apply_accelerator_device_class(cfg, GPUSKUType.B60)
        assert _workers(cfg)["Worker"]["deviceClassName"] == "gpu.intel.com"

    def test_mixed_case_sku_normalized(self):
        cfg = _config(Worker=_worker())
        apply_accelerator_device_class(cfg, "B60")
        assert _workers(cfg)["Worker"]["deviceClassName"] == "gpu.intel.com"

    def test_mi300_sets_amd_device_class_without_xpu_env(self):
        cfg = _config(Worker=_worker())
        apply_accelerator_device_class(cfg, "mi300")
        worker = _workers(cfg)["Worker"]
        assert worker["deviceClassName"] == "gpu.amd.com"
        # AMD must NOT receive the Intel-only XPU flag.
        assert _env_value(worker, "VLLM_TARGET_DEVICE") is None

    def test_mi200_sets_amd_device_class(self):
        cfg = _config(Worker=_worker())
        apply_accelerator_device_class(cfg, "mi200")
        assert _workers(cfg)["Worker"]["deviceClassName"] == "gpu.amd.com"

    def test_all_worker_services_updated(self):
        cfg = _config(
            Frontend={"componentType": "frontend"},
            VllmPrefillWorker=_worker(),
            VllmDecodeWorker=_worker(),
        )
        apply_accelerator_device_class(cfg, "b60")
        svcs = _workers(cfg)
        assert svcs["VllmPrefillWorker"]["deviceClassName"] == "gpu.intel.com"
        assert svcs["VllmDecodeWorker"]["deviceClassName"] == "gpu.intel.com"
        assert "deviceClassName" not in svcs["Frontend"]


# ---------------------------------------------------------------------------
# apply_accelerator_device_class — NVIDIA / unknown are strict no-ops
# ---------------------------------------------------------------------------


class TestApplyAcceleratorDeviceClassNvidiaNoop:
    @pytest.mark.parametrize("sku", ["h200_sxm", "h100_sxm", "b200_sxm", "l40s"])
    def test_nvidia_sku_is_noop(self, sku):
        cfg = _config(Worker=_worker())
        before = _config(Worker=_worker())
        apply_accelerator_device_class(cfg, sku)
        assert cfg == before  # byte-for-byte unchanged

    def test_unknown_sku_is_noop(self):
        cfg = _config(Worker=_worker())
        apply_accelerator_device_class(cfg, "totally-unknown")
        assert "deviceClassName" not in _workers(cfg)["Worker"]

    def test_none_sku_is_noop(self):
        cfg = _config(Worker=_worker())
        apply_accelerator_device_class(cfg, None)
        assert "deviceClassName" not in _workers(cfg)["Worker"]


# ---------------------------------------------------------------------------
# apply_accelerator_device_class — idempotency / override precedence
# ---------------------------------------------------------------------------


class TestApplyAcceleratorDeviceClassIdempotent:
    def test_existing_device_class_preserved(self):
        cfg = _config(Worker=_worker(deviceClassName="gpu.nvidia.com"))
        apply_accelerator_device_class(cfg, "b60")
        # User-supplied value wins; derivation must not clobber it.
        assert _workers(cfg)["Worker"]["deviceClassName"] == "gpu.nvidia.com"

    def test_explicit_empty_device_class_preserved(self):
        # "" is PR-1's documented "standalone DRA off" opt-out: derivation must
        # leave it untouched rather than forcing the worker onto DRA.
        cfg = _config(Worker=_worker(deviceClassName=""))
        apply_accelerator_device_class(cfg, "b60")
        assert _workers(cfg)["Worker"]["deviceClassName"] == ""

    def test_existing_xpu_env_preserved(self):
        worker = _worker(
            extraPodSpec={
                "mainContainer": {
                    "env": [{"name": "VLLM_TARGET_DEVICE", "value": "cuda"}]
                }
            }
        )
        cfg = _config(Worker=worker)
        apply_accelerator_device_class(cfg, "b60")
        w = _workers(cfg)["Worker"]
        assert w["deviceClassName"] == "gpu.intel.com"
        # Existing env entry is not duplicated or overwritten.
        xpu_entries = [e for e in _env_of(w) if e.get("name") == "VLLM_TARGET_DEVICE"]
        assert xpu_entries == [{"name": "VLLM_TARGET_DEVICE", "value": "cuda"}]

    def test_idempotent_when_applied_twice(self):
        cfg = _config(Worker=_worker())
        apply_accelerator_device_class(cfg, "b60")
        snapshot = _config(Worker=_workers(cfg)["Worker"])
        apply_accelerator_device_class(cfg, "b60")
        assert cfg == snapshot

    def test_existing_other_env_extended_not_replaced(self):
        worker = _worker(
            extraPodSpec={"mainContainer": {"env": [{"name": "FOO", "value": "bar"}]}}
        )
        cfg = _config(Worker=worker)
        apply_accelerator_device_class(cfg, "b60")
        env = _env_of(_workers(cfg)["Worker"])
        names = {e["name"] for e in env}
        assert names == {"FOO", "VLLM_TARGET_DEVICE"}

    def test_gms_enabled_worker_skipped(self):
        # GMS owns its own DRA device class; the operator rejects deviceClassName
        # combined with gpuMemoryService, so derivation must skip the worker
        # entirely (no device class, no XPU env).
        cfg = _config(Worker=_worker(gpuMemoryService={"enabled": True}))
        apply_accelerator_device_class(cfg, "b60")
        w = _workers(cfg)["Worker"]
        assert "deviceClassName" not in w
        assert all(e.get("name") != "VLLM_TARGET_DEVICE" for e in _env_of(w))


# ---------------------------------------------------------------------------
# _apply_device_class_to_final_config — orchestration wrapper
# ---------------------------------------------------------------------------


class TestApplyDeviceClassToFinalConfig:
    def test_dict_shape_b60(self):
        cfg = _config(Worker=_worker())
        out = _apply_device_class_to_final_config(cfg, _make_dgdr())
        assert _workers(out)["Worker"]["deviceClassName"] == "gpu.intel.com"

    def test_list_shape_applies_to_last_element(self):
        configmap = {"apiVersion": "v1", "kind": "ConfigMap", "data": {}}
        dgd = _config(Worker=_worker())
        out = _apply_device_class_to_final_config([configmap, dgd], _make_dgdr())
        # ConfigMap untouched; DGD (last element) updated.
        assert out[0] == {"apiVersion": "v1", "kind": "ConfigMap", "data": {}}
        assert _workers(out[-1])["Worker"]["deviceClassName"] == "gpu.intel.com"

    def test_mocker_enabled_skips_injection(self):
        dgdr = _make_dgdr(features=FeaturesSpec(mocker=MockerSpec(enabled=True)))
        cfg = _config(Worker=_worker())
        out = _apply_device_class_to_final_config(cfg, dgdr)
        assert "deviceClassName" not in _workers(out)["Worker"]

    def test_nvidia_sku_is_noop(self):
        dgdr = _make_dgdr(
            hardware=HardwareSpec(gpuSku="h200_sxm", totalGpus=8, numGpusPerNode=8)
        )
        cfg = _config(Worker=_worker())
        out = _apply_device_class_to_final_config(cfg, dgdr)
        assert "deviceClassName" not in _workers(out)["Worker"]

    def test_none_final_config_is_noop(self):
        assert _apply_device_class_to_final_config(None, _make_dgdr()) is None

    def test_empty_list_is_noop(self):
        assert _apply_device_class_to_final_config([], _make_dgdr()) == []
