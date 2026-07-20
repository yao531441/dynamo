# 1.8 Disaggregated + KV Router (`disagg_router_xpu_dra.yaml`)

**Status**: documented from repo, not yet run hands-on.

**Overview**: The combination that upstream explicitly recommends for genuine KV-aware routing
on Intel XPU — disaggregated P/D (so real `BlockStored` KV events are emitted during prefill)
plus `DYN_ROUTER_MODE=kv` on the Frontend, `--kv-events-config` on prefill/decode workers, and
`--enable-prefix-caching`. 2 decode worker replicas by default (vs. 1 for the plain disagg case
in §1.5) to give the router something to route between.

**Prerequisites**: Chapter 0 complete.

**Deployment Steps**:

```bash
kubectl apply -f examples/backends/vllm/deploy/xpu/disagg_router_xpu_dra.yaml -n ${NAMESPACE}
```

**Verification**:

```bash
kubectl get pods -n ${NAMESPACE}   # expect Frontend, 2x VllmDecodeWorker, VllmPrefillWorker
kubectl get resourceclaim -n ${NAMESPACE}   # expect 3 claims total
```

**Inference Test**: same as §1.5, Deployment name `xpu-disagg-router-frontend`. Send several
concurrent requests with overlapping prompt prefixes and observe (via logs/metrics) that
requests sharing a KV prefix tend to route to the same decode worker.

**Troubleshooting**: same KV-transfer and DRA-scheduling checks as §1.5; additionally, if
routing doesn't appear cache-aware, confirm `--kv-events-config` is actually enabled on the
**prefill** worker (KV events are emitted during prefill, not decode, per the note in §1.3).

**Cleanup**: `kubectl delete -f examples/backends/vllm/deploy/xpu/disagg_router_xpu_dra.yaml -n ${NAMESPACE}`

**Config Reference**:

| Field | Value | Notes |
|---|---|---|
| `DynamoGraphDeployment` name | `xpu-disagg-router` | |
| `DYN_ROUTER_MODE` | `kv` (Frontend) | fully effective here (unlike §1.3), since prefill emits real KV events |
| Decode worker replicas | 2 | |
| Total GPU claims | 3 (2 decode + 1 prefill) | |

---
