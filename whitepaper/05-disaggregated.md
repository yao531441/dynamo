# 1.5 Disaggregated (`disagg_xpu_dra.yaml`) — Deep-Dive Sample Case

**Status**: 🚧 documented from repo, not yet run hands-on. (Written as the reference sample for
this whitepaper; follow this structure when completing the remaining chapters.)

**Overview**: True Prefill/Decode disaggregation on Intel XPU. Two distinct worker roles
(`VllmPrefillWorker`, `VllmDecodeWorker`), each with `subComponentType: prefill`/`decode`, and
KV cache handed off between them over NIXL (`kv_connector: NixlConnector`) with
`kv_buffer_device: xpu` (i.e., the KV transfer buffer itself lives in XPU device memory, not
host/CPU RAM).

**Prerequisites**: Chapter 0 complete. Both prefill and decode workers need their own XPU
allocation (2 total GPU claims for this template as shipped, since both replicas default to 1).

**Deployment Steps**:

```bash
kubectl apply -f examples/backends/vllm/deploy/xpu/disagg_xpu_dra.yaml -n ${NAMESPACE}
```

**Verification**:

```bash
kubectl get resourceclaim -n ${NAMESPACE}       # expect 2 claims (prefill + decode)
kubectl get dynamographdeployment -n ${NAMESPACE}
kubectl get pods -n ${NAMESPACE}
kubectl logs deployment/vllm-disagg-xpu-dra-vllmprefillworker -n ${NAMESPACE}
kubectl logs deployment/vllm-disagg-xpu-dra-vllmdecodeworker -n ${NAMESPACE}
```

**Inference Test**:

```bash
kubectl port-forward deployment/vllm-disagg-xpu-dra-frontend 8000:8000 -n ${NAMESPACE}
curl localhost:8000/v1/models
curl localhost:8000/v1/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"Qwen/Qwen3-0.6B","prompt":"Explain prefill/decode disaggregation","max_tokens":50}'
```

**Troubleshooting**:
- KV transfer failures between prefill and decode workers typically show up as NIXL connection
  errors in the decode worker's log — confirm both workers were scheduled with XPU devices
  (`kubectl describe resourceclaim`) and that `kv_buffer_device` matches on both sides
  (`kv_role: kv_both` on prefill, `kv_role: kv_consumer` on decode — both set to `"xpu"`).
- If either worker Pending forever, check `kubectl get resourceslices` for available device
  count — you need at least 2 free XPU devices across the cluster for this template.

**Cleanup**: `kubectl delete -f examples/backends/vllm/deploy/xpu/disagg_xpu_dra.yaml -n ${NAMESPACE}`

**Config Reference**:

| Field | Value | Notes |
|---|---|---|
| `DynamoGraphDeployment` name | `vllm-disagg-xpu-dra` | |
| Services | `Frontend`, `VllmPrefillWorker`, `VllmDecodeWorker` (1 replica each) | |
| `kv_connector` | `NixlConnector` | |
| Prefill `kv_role` | `kv_both` | |
| Decode `kv_role` | `kv_consumer` | |
| `kv_buffer_device` | `xpu` | KV buffer lives in XPU memory, not CPU RAM |
| `--block-size` | `64` | |
| Total GPU claims | 2 (1 prefill + 1 decode) | |
