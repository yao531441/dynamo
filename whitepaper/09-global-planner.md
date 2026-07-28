# Chapter 2 — Global Planner on Intel XPU

**Source**: `examples/global_planner/global-planner-vllm-test-xpu-dra.yaml`, documented in
`examples/global_planner/README.md`.

**Overview**: A single-endpoint, multi-pool deployment pattern: one `Frontend`, a
`GlobalRouter`, and a `GlobalPlanner`, fronting **2 TP1 prefill pools + 1 TP1 decode pool** on
Intel XPU (the XPU/DRA variant uses TP1 everywhere because each DRA `ResourceClaimTemplate`
requests exactly one XPU device per worker — there is no multi-GPU-per-claim tensor-parallel
config in this template). This differs from Chapter 1's simpler single-DGD templates by
demonstrating Dynamo's cluster-wide GPU budget / multi-pool planning capability rather than a
single fixed topology.

**Prerequisites**: Chapter 0 complete, plus an **RWX (ReadWriteMany) StorageClass** available in
the cluster (`STORAGE_CLASS_NAME` env var below) for shared state between planner components.

**Deployment Steps**:

```bash
export K8S_NAMESPACE=my-ns
export DYNAMO_IMAGE=<dynamo-image>
export DYNAMO_VLLM_IMAGE=<vllm-xpu-image>   # from §0.3, must be built for XPU
export MODEL_NAME=Qwen/Qwen3-0.6B
export STORAGE_CLASS_NAME=<rwx-storage-class>

kubectl create secret generic hf-token-secret \
  --from-literal=HF_TOKEN=<your-token> -n ${K8S_NAMESPACE}

envsubst < examples/global_planner/global-planner-vllm-test-xpu-dra.yaml \
  | kubectl apply -n ${K8S_NAMESPACE} -f -
```

**Verification**:

```bash
kubectl get dynamographdeployment -n ${K8S_NAMESPACE}
kubectl get pods -n ${K8S_NAMESPACE}
kubectl get resourceclaim -n ${K8S_NAMESPACE}
```

**Inference Test**: port-forward the Frontend Deployment (`gp-ctrl-frontend`, following the
`{DynamoGraphDeployment name}-frontend` pattern from §1.1) and send completion requests as in
§1.1:

```bash
kubectl port-forward deployment/gp-ctrl-frontend 8000:8000 -n ${K8S_NAMESPACE}
curl localhost:8000/v1/completions \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"${MODEL_NAME}\",\"prompt\":\"Hello\",\"max_tokens\":20}"
```

Observe (via `GlobalRouter`/`GlobalPlanner` logs) how requests are distributed across the 2
prefill pools and 1 decode pool.

**Troubleshooting**:
- `envsubst` silently leaves variables unset if you forget to `export` one of them — always
  `echo` the rendered YAML or diff against the template before applying if a field looks empty.
- Because every pool is TP1, do not attempt to scale a single pool's tensor-parallel size
  without first re-checking whether the DRA `ResourceClaimTemplate` needs to request more than
  1 device — the shipped template does not do this.

**Cleanup**:

```bash
envsubst < examples/global_planner/global-planner-vllm-test-xpu-dra.yaml \
  | kubectl delete -n ${K8S_NAMESPACE} -f -
```

**Config Reference**:

| Field | Value | Notes |
|---|---|---|
| Pattern | Single-endpoint, multi-pool | Frontend + GlobalRouter + GlobalPlanner |
| Prefill pools | 2× TP1 | XPU/DRA variant, 1 device per pool |
| Decode pool | 1× TP1 | |
| Required extra | RWX `StorageClass` | for planner shared state |
| Key difference from GPU (NVIDIA) variant | DRA `ResourceClaimTemplate` instead of `resources.limits.gpu`; `VLLM_TARGET_DEVICE=xpu`; TP1-only pools | |

---
