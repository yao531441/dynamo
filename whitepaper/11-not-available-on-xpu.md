# Chapter 4 — Feasible but Not Yet Supported on Intel XPU

## Answering "what could be supported but isn't yet?"

Within `examples/backends/vllm/deploy/`, only 8 of the ~20 `DynamoGraphDeployment` templates
have an Intel XPU (`xpu/`) counterpart (see Chapter 1). The other vLLM-backend templates below
use the same vLLM engine already proven on Intel XPU, with no hardware-specific blocker found —
these are gaps, not fundamental limitations:

| Template | Path | Why it looks feasible on Intel XPU |
|---|---|---|
| LoRA adapter serving | `backends/vllm/deploy/lora/agg_lora.yaml`, `lora/multimodal/agg_qwen_lora.yaml` | Plain vLLM, no CUDA-specific field. Already confirmed working on Intel XPU via the bash launch-script path (`backends/vllm/launch/lora/xpu/agg_lora_xpu.sh`) — the K8s YAML gap is just a templating gap |
| GAIE (Gateway API Inference Extension) integration | `backends/vllm/deploy/gaie/{agg,disagg}.yaml` + `http-route.yaml` | Same routing-layer pattern (EPP/InferencePool) already proven hardware-agnostic elsewhere |
| Multi-node disaggregation | `backends/vllm/deploy/disagg-multinode.yaml` | Same NIXL prefill/decode split as the XPU-proven `xpu/disagg_xpu_dra.yaml`, just spread across nodes |
| GPU Memory Service (GMS) sidecar / failover | `backends/vllm/deploy/agg_gms.yaml`, `agg_failover.yaml`, `gms-failover.yaml` | Kubernetes DRA itself is vendor-neutral (Intel already has its own DRA driver, used by all 8 Chapter 1 templates). The real dependency is GMS's own device backend — its design doc (`lib/gpu_memory_service/GMS_MULTI_DEVICE.md`) shows CUDA support done and an Intel XPU backend planned as "Phase 2," just not implemented yet. Not a hard vendor lock-in, just unfinished |
| KVBM (multi-tier KV cache: GPU→CPU→SSD→remote) | `backends/vllm/deploy/agg_kvbm.yaml`, `disagg_kvbm*.yaml` | Intel XPU support is real, active work-in-progress, not yet merged: PRs [#7946](https://github.com/ai-dynamo/dynamo/pull/7946) and [#10520](https://github.com/ai-dynamo/dynamo/pull/10520) add SYCL/Level-Zero XPU support to KVBM v2, tracked by DEP issue [#9313](https://github.com/ai-dynamo/dynamo/issues/9313), still in design discussion |
| SGLang backend (all patterns) | `backends/sglang/deploy/{agg,agg_router,disagg,disagg_planner,disagg-multinode,agg_gms}.yaml` | No `xpu/` directory anywhere; SGLang engine's own Intel XPU support not independently verified — flagged as unconfirmed |
| Triton Server backend | `backends/tritonserver/` | No `xpu/` directory anywhere; NVIDIA Triton Inference Server has historically been NVIDIA-GPU-centric, but no explicit hard-blocker statement found in the repo — flagged as unconfirmed rather than a confirmed lock-in |

None of these have been hands-on tested on Intel XPU — "feasible" here means "no repo evidence
of a hardware blocker," not "verified working." Treat as a backlog of candidate work, not a
completed case.


## Genuinely NVIDIA-specific (real hardware/vendor lock-in found)

These have an explicit, documented dependency that does not currently have an Intel XPU
equivalent:

| Feature | Path | Lock-in reason |
|---|---|---|
| TensorRT-LLM backend (all patterns, incl. multimodal: Qwen3-VL, Llama4, LLaVA, Qwen2-VL) | `backends/trtllm/` | TensorRT-LLM is NVIDIA's proprietary compiler/runtime (TensorRT), fundamentally CUDA/NVIDIA-GPU-only — no Intel XPU port exists upstream in TensorRT-LLM itself, so this is a hard vendor lock-in, not a templating gap |

## Recipes (`recipes/`) other than the Chapter 3 hetero case

Confirmed via `grep -r xpu recipes/`: no other recipe under `recipes/` references Intel XPU as of
this writing. Each recipe would need to be checked individually against the "feasible" criteria
above (does it use vLLM/SGLang with no NVIDIA-only sidecar features) before assuming portability.

## If you want to close these gaps

Building and validating any of the "feasible" candidates above on real Intel XPU hardware, then
contributing the resulting `xpu/` template upstream, is the most direct way to expand real Intel
XPU coverage — following the structural pattern of Chapter 1's existing 8 templates (Intel DRA
device class `gpu.intel.com`, XCCL/`ze_copy` transport where NIXL/UCX is involved, custom
`vllm-runtime-xpu` image).
