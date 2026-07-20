# 1.4 Aggregated + KV Router, No-KV-Events Mode (`agg_router_kv_approx_xpu_dra.yaml`)

**Status**: 🚧 documented from repo, not yet run hands-on.

**Overview**: A variant of §1.3 using `--no-kv-events` on the Frontend
(`dynamo.frontend --router-mode kv --no-kv-events`) and `enable_kv_cache_events: false` on the
worker. Instead of consuming real KV events over NATS/JetStream, the router predicts cache state
locally from its own routing decisions with TTL-based expiration/pruning. Benefits per upstream
comments: no NATS/JetStream dependency, simpler deployment for lightweight scenarios.

**Prerequisites**: Chapter 0 complete. No message-bus (NATS) dependency needed for this variant.

**Deployment Steps**:

```bash
kubectl apply -f examples/backends/vllm/deploy/xpu/agg_router_kv_approx_xpu_dra.yaml -n ${NAMESPACE}
```

**Verification / Inference Test**: same pattern as §1.1, Deployment name `xpu-agg-kvap-frontend`.

**Troubleshooting**: Because routing is based on local prediction rather than real KV events,
expect the router's cache-hit assumptions to drift under high churn — this is an inherent
trade-off of the approximate mode, not a misconfiguration.

**Cleanup**: `kubectl delete -f .../agg_router_kv_approx_xpu_dra.yaml -n ${NAMESPACE}`

**Config Reference**:

| Field | Value | Notes |
|---|---|---|
| `DynamoGraphDeployment` name | `xpu-agg-kvap` | |
| Frontend command | `dynamo.frontend --router-mode kv --no-kv-events` | |
| `--kv-events-config` | `{"enable_kv_cache_events": false}` | |
