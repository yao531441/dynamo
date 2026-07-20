# 1.1 Aggregated (`agg_xpu_dra.yaml`)

**Status**: 🚧 documented from repo, not yet run hands-on.

**Overview**: The simplest pattern — a single `VllmDecodeWorker` role handles both prefill and
decode for every request (no P/D separation). Good starting point to validate that DRA GPU
allocation and the XPU runtime image work at all, before adding routing/tracing/disaggregation
on top.

**Prerequisites**: Chapter 0 complete.

**Deployment Steps**:

```bash
export NAMESPACE=my-ns
kubectl apply -f examples/backends/vllm/deploy/xpu/agg_xpu_dra.yaml -n ${NAMESPACE}
```

**Verification**:

```bash
kubectl get resourceclaim -n ${NAMESPACE}
kubectl get resourceslices
kubectl get dynamographdeployment -n ${NAMESPACE}
kubectl get pods -n ${NAMESPACE}
```

**Inference Test**:

```bash
kubectl port-forward deployment/vllm-agg-xpu-dra-frontend 8000:8000 -n ${NAMESPACE}
curl localhost:8000/v1/models
curl localhost:8000/v1/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"Qwen/Qwen3-0.6B","prompt":"Hello","max_tokens":20}'
```

**Troubleshooting**:
- If the `VllmDecodeWorker` pod stays `Pending`, check `kubectl describe resourceclaim` — a
  common cause is the Intel resource driver not yet reporting available devices
  (`kubectl get resourceslices` should list your XPU nodes).
- If the worker crashes on startup complaining about missing device permissions, see §0.5.

**Cleanup**:

```bash
kubectl delete -f examples/backends/vllm/deploy/xpu/agg_xpu_dra.yaml -n ${NAMESPACE}
```

**Config Reference**:

| Field | Value | Notes |
|---|---|---|
| `DynamoGraphDeployment` name | `vllm-agg-xpu-dra` | |
| Services | `Frontend` (1 replica), `VllmDecodeWorker` (1 replica, aggregated) | |
| Model | `Qwen/Qwen3-0.6B` | override via `--model` arg |
| `VLLM_TARGET_DEVICE` | `xpu` | |
| GPU claim | 1× `gpu.intel.com` per worker replica | |
| `ephemeral-storage` request | `2Gi` | increase for larger models |
