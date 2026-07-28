# 1.3 Aggregated + KV Router (`agg_router_xpu_dra.yaml`)

**Overview**: Adds Dynamo's KV-aware Router (`DYN_ROUTER_MODE=kv` on the Frontend) in front of
2 aggregated worker replicas. **Important upstream-documented limitation**: in Aggregated mode,
`VllmDecodeWorker` does not emit `BlockStored` KV cache events (those are only produced during
the prefill phase in Disaggregated mode), so the KV Router **cannot** make cache-aware routing
decisions here — it effectively falls back to simpler load-based routing. For genuine KV-aware
routing, use §1.8 (`disagg_router_xpu_dra.yaml`) instead. This case is documented for
completeness, but readers who actually need KV-aware routing should go straight to §1.8.

**Prerequisites**: Chapter 0 complete.

**Deployment Steps**:

```bash
kubectl apply -f examples/backends/vllm/deploy/xpu/agg_router_xpu_dra.yaml -n ${NAMESPACE}
```

**Verification / Inference Test**: same pattern as §1.1, Deployment name
`xpu-agg-router-frontend`, 2 worker replicas.

**Troubleshooting**: If you expected KV-cache-aware load balancing and don't see it, this is
expected behavior per the limitation above — not a bug.

**Cleanup**: `kubectl delete -f .../agg_router_xpu_dra.yaml -n ${NAMESPACE}`

**Config Reference**:

| Field | Value | Notes |
|---|---|---|
| `DynamoGraphDeployment` name | `xpu-agg-router` | |
| `DYN_ROUTER_MODE` | `kv` (Frontend) | limited effectiveness in aggregated mode, see above |
| Worker replicas | 2 | |
| `--kv-events-config` | ZMQ publisher on `tcp://*:20080`, topic `kv-events` | |
| `--enable-prefix-caching` | set | |
