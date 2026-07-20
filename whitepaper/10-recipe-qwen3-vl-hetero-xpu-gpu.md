# Chapter 3 — Recipe: Qwen3-VL-32B-FP8 Heterogeneous Hardware Disaggregation (Intel XPU Encode + NVIDIA GPU Decode) (In Progress)

**Status**: documented from official repo sources, not yet run hands-on.

## Overview

`recipes/qwen3-vl-32b-fp8/` is a production-recipe deployment (distinct from the `examples/`
tree) for **Qwen/Qwen3-VL-32B-Instruct-FP8**, a 32B FP8-quantized vision-language model. It ships
two configurations:

| Configuration | Hardware | Mode |
|---|---|---|
| `vllm/agg/` | 1× NVIDIA H100/H200 | Aggregated (vision + decode combined) — NVIDIA only, no Intel XPU |
| `vllm/hetero_hardware_disagg/` | **1× Intel XPU + 1× NVIDIA GPU** | Disaggregated — **this is the Intel XPU case** |

Unlike every other Intel XPU case in this whitepaper (the 8 DRA templates in Chapter 1, which run
**entirely** on Intel XPU), this recipe is **heterogeneous**: it deliberately splits the pipeline
across two different accelerator vendors in the same deployment —

- **EncodeWorker** — runs the vision-encoding stage on **Intel XPU** (`gpu.intel.com` DRA device
  class), producing image embeddings.
- **VllmDecodeWorker** — runs autoregressive text decode on **NVIDIA GPU** (`gpu.nvidia.com` DRA
  device class), consuming those embeddings.
- Embeddings are transferred between the two workers over **RDMA** via a NIXL `NixlConnector`
  (`kv_buffer_device: xpu` on the encode side, `kv_buffer_device: cuda` on the decode side),
  using `UCX_TLS` that includes both `ze_copy` (Intel Level-Zero copy transport) and `cuda_copy`.

Source: `recipes/qwen3-vl-32b-fp8/README.md`, `vllm/hetero_hardware_disagg/deploy.yaml`,
`vllm/hetero_hardware_disagg/intel_xpu_rdma_template.yaml`,
`vllm/hetero_hardware_disagg/nvidia_gpu_rdma_template.yaml`.

## Why this differs from Chapter 1's DRA templates

| | Chapter 1 (8 DRA templates) | This recipe |
|---|---|---|
| Location | `examples/backends/vllm/deploy/xpu/` | `recipes/qwen3-vl-32b-fp8/vllm/hetero_hardware_disagg/` |
| Hardware | 100% Intel XPU | Intel XPU (encode) **+** NVIDIA GPU (decode) in the same deployment |
| Purpose | General serving patterns (aggregated/disaggregated × tracing/KV-router/planner) for any model | A specific production recipe for one model (Qwen3-VL-32B-FP8), demonstrating cross-vendor hardware disaggregation |
| DRA device class | `gpu.intel.com` only | `gpu.intel.com` (encode) + `gpu.nvidia.com` (decode), both requesting `rdma-dranet` |

If your goal is "run Dynamo entirely on Intel XPU," use Chapter 1. If your goal is "offload
multimodal encode to Intel XPU while keeping decode on existing NVIDIA GPU capacity" (e.g. to free
up NVIDIA GPU capacity for decode-bound workloads while reusing available Intel XPU nodes for the
comparatively lighter encode stage), this recipe is the direct reference.

## Prerequisites

- Dynamo Platform installed (see [Common Installation](00-common-installation.md)).
- A cluster with **both** an Intel XPU node/pool and an NVIDIA GPU node/pool, each exposed via
  Kubernetes DRA (`gpu.intel.com` and `gpu.nvidia.com` `DeviceClass`es respectively).
- **RDMA-capable network interfaces** between the Intel XPU and NVIDIA GPU nodes — this is a hard
  requirement, not optional; the recipe explicitly depends on a `rdma-dranet` DRA device class on
  both sides for the NIXL embedding transfer.
- A HuggingFace token with access to Qwen models, stored as `hf-token-secret`.
- A `StorageClass` supporting `ReadWriteMany` for the shared model cache PVC.

## Deployment Steps

```bash
export NAMESPACE=dynamo-demo
kubectl create namespace ${NAMESPACE}

kubectl create secret generic hf-token-secret \
  --from-literal=HF_TOKEN="<your-token>" \
  -n ${NAMESPACE}

# 1. Update storageClassName in model-cache/model-cache.yaml to match your cluster, then:
kubectl apply -f model-cache/ -n ${NAMESPACE}
kubectl wait --for=condition=Complete job/model-download -n ${NAMESPACE} --timeout=3600s

# 2. Apply the DRA ResourceClaimTemplates (both, before the deployment)
kubectl apply -f vllm/hetero_hardware_disagg/intel_xpu_rdma_template.yaml -n ${NAMESPACE}
kubectl apply -f vllm/hetero_hardware_disagg/nvidia_gpu_rdma_template.yaml -n ${NAMESPACE}

# 3. Deploy
kubectl apply -f vllm/hetero_hardware_disagg/deploy.yaml -n ${NAMESPACE}
```

## Verification

```bash
kubectl get pods -n ${NAMESPACE} -l nvidia.com/dynamo-graph-deployment-name=qwen3-vl-32b-fp8-vllm-disagg
kubectl get resourceclaims -n ${NAMESPACE}
kubectl logs -n ${NAMESPACE} deploy/qwen3-vl-32b-fp8-vllm-disagg-encodeworker | grep -i "nixl\|xpu"
kubectl logs -n ${NAMESPACE} deploy/qwen3-vl-32b-fp8-vllm-disagg-vllmdecodeworker | grep -i "nixl\|cuda"
```

## Inference Test

```bash
kubectl port-forward svc/qwen3-vl-32b-fp8-vllm-disagg-frontend 8000:8000 -n ${NAMESPACE}

# Text-only request
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen/Qwen3-VL-32B-Instruct-FP8",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 50
  }'

# Multimodal (image) request — exercises the Intel XPU encode path
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen/Qwen3-VL-32B-Instruct-FP8",
    "messages": [
      {
        "role": "user",
        "content": [
          {"type": "image_url", "image_url": {"url": "https://upload.wikimedia.org/wikipedia/commons/thumb/4/47/PNG_transparency_demonstration_1.png/300px-PNG_transparency_demonstration_1.png"}},
          {"type": "text", "text": "Describe this image in detail."}
        ]
      }
    ],
    "max_tokens": 256
  }'
```

## Benchmarking

```bash
./benchmark/run-benchmark.sh --config disagg -n ${NAMESPACE}
```

## Troubleshooting

- **`ResourceClaimTemplate` not found errors on deploy**: both `intel_xpu_rdma_template.yaml` and
  `nvidia_gpu_rdma_template.yaml` must be applied *before* `deploy.yaml` — the `deploy.yaml`
  references both templates by name (`intel-xpu-rdma-template`, `nvidia-gpu-rdma-template`).
- **NIXL handshake failures between encode and decode workers**: confirm RDMA connectivity between
  the two nodes independently of Dynamo first (e.g. `ib_write_bw`/`ucx_perftest`), and check that
  `UCX_TLS` includes the transport for each side (`ze_copy` for Intel XPU, `cuda_copy` for NVIDIA).
  `kv_connector_extra_config.enforce_handshake_compat: false` is already set to relax version
  matching between the two heterogeneous vLLM builds.
- **EncodeWorker OOM / low throughput**: `--gpu-memory-utilization 0.7` on the encode side is
  conservative — adjust based on the Intel XPU device's actual HBM/VRAM size.
- **Model download stuck**: confirm `storageClassName` supports `ReadWriteMany` and the PVC is
  `Bound`, not `Pending`.

## Cleanup

```bash
kubectl delete -f vllm/hetero_hardware_disagg/deploy.yaml -n ${NAMESPACE}
kubectl delete -f vllm/hetero_hardware_disagg/intel_xpu_rdma_template.yaml -n ${NAMESPACE}
kubectl delete -f vllm/hetero_hardware_disagg/nvidia_gpu_rdma_template.yaml -n ${NAMESPACE}
kubectl delete -f model-cache/ -n ${NAMESPACE}
kubectl delete secret hf-token-secret -n ${NAMESPACE}
```

## Config Reference

| Field | Value | Notes |
|---|---|---|
| Model | `Qwen/Qwen3-VL-32B-Instruct-FP8` | 32B params, FP8-quantized weights and KV cache |
| Encode image | `nvcr.io/nvidia/ai-dynamo/vllm-runtime-xpu:1.2.0` | Intel XPU vLLM runtime |
| Decode image | `nvcr.io/nvidia/ai-dynamo/vllm-runtime:1.2.0` | standard (NVIDIA) vLLM runtime |
| Intel XPU DRA device class | `gpu.intel.com` | requested alongside `rdma-dranet` in `intel-xpu-rdma-template` |
| NVIDIA GPU DRA device class | `gpu.nvidia.com` | requested alongside `rdma-dranet` in `nvidia-gpu-rdma-template` |
| Embedding transfer | NIXL `NixlConnector`, `DYN_VLLM_EMBEDDING_TRANSFER_MODE=nixl-read` | encode `kv_buffer_device: xpu`, decode `kv_buffer_device: cuda` |
| Encode-side `UCX_TLS` | `ib,rc,ud,rc_verbs,ud_verbs,ze_copy` | `ze_copy` = Intel Level-Zero copy transport |
| Decode-side `UCX_TLS` | `ib,rc,ud,rc_verbs,ud_verbs,cuda_copy` | |
| Decode-side KV cache dtype | `fp8` | |
| Shared memory per worker | 80Gi | both Encode and Decode workers |
