<!--
SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
SPDX-License-Identifier: Apache-2.0
-->

# GAIE Deployments on Intel XPU

Intel XPU variants of the [GAIE](../) (Gateway API Inference Extension)
`DynamoGraphDeployment` templates, using Kubernetes Dynamic Resource
Allocation (DRA) instead of `nvidia.com/gpu` resource limits.

## Available Templates

| File | Pattern | Description |
|------|---------|-------------|
| `agg.yaml` | Aggregated | Single decode worker + Epp, XPU via DRA |
| `disagg.yaml` | Disaggregated | Separate prefill/decode workers + Epp, XPU via DRA |

Both are adapted from [`../agg.yaml`](../agg.yaml) and
[`../disagg.yaml`](../disagg.yaml). The `Epp` (endpoint picker) component,
its scheduling profiles, and `http-route.yaml` are hardware-agnostic and
unchanged -- reuse [`../http-route.yaml`](../http-route.yaml) as-is.

## Key Differences from the NVIDIA GAIE Templates

| Aspect | NVIDIA (`../agg.yaml`, `../disagg.yaml`) | Intel XPU |
|--------|-------------------------------------------|-----------|
| GPU Allocation | `resources.limits."nvidia.com/gpu"` | DRA `ResourceClaimTemplate` (`gpu.intel.com`) |
| Worker Image | `vllm-runtime` | `vllm-runtime-xpu` |
| Device Target | Default (CUDA) | `VLLM_TARGET_DEVICE: xpu` |
| KV Transfer (disagg) | `kv_buffer_device: "cpu"` + `UCX_TLS=tcp,self` (CUDA/NIXL workaround, see comments in `../disagg.yaml`) | `kv_buffer_device: "xpu"` |
| Frontend sidecar | `vllm-runtime` | Unchanged (`vllm-runtime`; doesn't run vLLM, no XPU needed) |

## Prerequisites

Same as [`../../xpu/README.md`](../../xpu/README.md):

1. **Kubernetes v1.34+** with DRA API v1 enabled.
2. **[Intel resource drivers for Kubernetes](https://github.com/intel/intel-resource-drivers-for-kubernetes)** installed with DeviceClass `gpu.intel.com`.
3. **Custom XPU runtime image** (`vllm-runtime-xpu`) built via
   `python container/render.py --framework=vllm --device=xpu --target=runtime`.
4. **GAIE / Gateway API Inference Extension** installed in-cluster -- see
   [Gateway API docs](../../../../../../docs/kubernetes/gateway-api/README.mdx).
5. **HuggingFace token secret** (`hf-token-secret`).

## Deploy

```bash
export NAMESPACE=gaie-dynamo
kubectl apply -f agg.yaml -n $NAMESPACE       # or disagg.yaml
kubectl apply -f ../http-route.yaml -n $NAMESPACE

kubectl get resourceclaim -n $NAMESPACE
kubectl get dynamographdeployment -n $NAMESPACE
kubectl get pods -n $NAMESPACE
```

## Further Reading

- [GAIE Deployment Templates](../) (NVIDIA, non-XPU)
- [Intel XPU DRA Deployment Templates](../../xpu/README.md) (non-GAIE)
- [Gateway API Inference Extension Setup](../../../../../../docs/kubernetes/gateway-api/README.mdx)
