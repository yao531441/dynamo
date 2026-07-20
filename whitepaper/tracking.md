# Dynamo Whitepaper — Case Verification Status Tracker

Status legend: ✅ verified working / 🚧 documented from official sources, not yet run hands-on /
📝 outline only / ⛔ blocked / ➖ not applicable (no Intel XPU support exists upstream)

## Chapter 0 — Common installation

| Item | Status | Owner | Notes |
|---|---|---|---|
| Kubernetes + Dynamo Platform install (CRDs + operator) | 🚧 | | Rewritten from `docs/kubernetes/installation-guide.md`, Intel-specific notes (skip GPU Operator, use Intel resource drivers) added |
| Intel resource drivers for Kubernetes + DeviceClass `gpu.intel.com` | 🚧 | | External dependency, not part of dynamo repo itself |
| Custom `vllm-runtime-xpu` image build | 🚧 | | From `container/render.py` + `container/README.md` |
| HF token secret | 🚧 | | Standard step, same pattern across every template |

## Chapter 1 — Intel XPU vLLM backend templates (`examples/backends/vllm/deploy/xpu/`)

| # | Case | File | Status | Owner | Notes |
|---|---|---|---|---|---|
| 1 | Aggregated | `agg_xpu_dra.yaml` | 🚧 | | |
| 2 | Aggregated + Tracing | `agg_tracing_xpu_dra.yaml` | 🚧 | | Needs an OTEL collector to fully validate |
| 3 | Aggregated + KV Router | `agg_router_xpu_dra.yaml` | 🚧 | | KV-aware routing is limited in aggregated mode (see whitepaper §1.3) |
| 4 | Aggregated + KV Router (no-KV-events) | `agg_router_kv_approx_xpu_dra.yaml` | 🚧 | | |
| 5 | Disaggregated | `disagg_xpu_dra.yaml` | 🚧 | | First deep-dive / sample case for this whitepaper |
| 6 | Disaggregated + Tracing | `disagg_tracing_xpu_dra.yaml` | 🚧 | | |
| 7 | Disaggregated + Planner | `disagg_planner_xpu_dra.yaml` | 🚧 | | Sample profiling data is illustrative only, not measured on real Intel XPU hardware — replace before trusting Planner's scaling decisions |
| 8 | Disaggregated + KV Router | `disagg_router_xpu_dra.yaml` | 🚧 | | Recommended combo for genuine KV-aware routing on Intel XPU |

## Chapter 2 — Global Planner

| Case | File | Status | Owner | Notes |
|---|---|---|---|---|
| Global Planner (multi-pool, TP1) | `examples/global_planner/global-planner-vllm-test-xpu-dra.yaml` | 🚧 | | Requires RWX StorageClass |

## Chapter 3 — Recipe: Qwen3-VL-32B-FP8 heterogeneous hardware disaggregation

| Case | File | Status | Owner | Notes |
|---|---|---|---|---|
| Hetero XPU (encode) + NVIDIA GPU (decode) disaggregation | `recipes/qwen3-vl-32b-fp8/vllm/hetero_hardware_disagg/` | 🚧 | | Requires RDMA connectivity between an Intel XPU node and an NVIDIA GPU node — hardest prerequisite of any case in this whitepaper |

## Chapter 4 — Feasible but not yet templated for Intel XPU vs. genuinely NVIDIA-locked

| Feature | Path | Status |
|---|---|---|
| LoRA adapter serving | `backends/vllm/deploy/lora/` | ⚠️ feasible, untemplated — plain vLLM, already proven on XPU via bash launch script |
| GAIE integration | `backends/vllm/deploy/gaie/` | ⚠️ feasible, untemplated — same EPP/InferencePool pattern proven hardware-agnostic on llm-d side |
| Multi-node disaggregation | `backends/vllm/deploy/disagg-multinode.yaml` | ⚠️ feasible, untemplated — same NIXL split as XPU-proven `xpu/disagg_xpu_dra.yaml`, just multi-node |
| GMS sidecar / failover | `backends/vllm/deploy/agg_gms.yaml`, `agg_failover.yaml`, `gms-failover.yaml` | ⚠️ feasible, in-progress — GMS's own design doc plans an Intel XPU backend ("Phase 2"), not yet built |
| SGLang backend (all patterns) | `backends/sglang/deploy/` | ❓ unconfirmed — no XPU mention in dynamo docs; SGLang engine's own Intel XPU support not independently verified |
| KVBM (multi-tier KV cache) | `backends/vllm/deploy/agg_kvbm.yaml`, `disagg_kvbm*.yaml` | 🚧 active WIP — unmerged PRs #7946, #10520 add XPU support to KVBM v2, tracked by DEP #9313 |
| TensorRT-LLM backend (incl. multimodal) | `backends/trtllm/` | ➖ NVIDIA-locked — TensorRT is a proprietary NVIDIA-only compiler/runtime |
| Triton Server backend | `backends/tritonserver/` | ❓ unconfirmed — not investigated in depth |
| Recipes other than `qwen3-vl-32b-fp8` | `recipes/` | ➖ no other XPU content found; each would need individual feasibility review |

## Recommended next steps

1. Validate Chapter 1 cases on a real Intel XPU cluster, starting with **Disaggregated**
   (§1.5, already deep-dived) since it is the most representative pattern, then
   **Disaggregated + KV Router** (§1.8) as the "fully featured" case.
2. Stand up a minimal OTEL collector (e.g. Grafana Tempo) to validate the two tracing variants
   (§1.2, §1.6) end-to-end rather than just confirming the pods start.
3. Profile real Intel XPU prefill/decode throughput curves and replace the sample data in
   `disagg_planner_xpu_dra.yaml` (§1.7) before treating Planner recommendations as trustworthy.
4. Validate the Chapter 3 heterogeneous recipe hands-on — this is the highest-complexity case
   (cross-vendor RDMA + NIXL) and the most likely to surface real-world issues not visible from
   reading the YAML alone.
