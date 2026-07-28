# 1.4 Aggregated + KV Router, No-KV-Events Mode (`agg_router_kv_approx_xpu_dra.yaml`)

**Overview**: A variant of §1.3 using `--no-kv-events` on the Frontend
(`dynamo.frontend --router-mode kv --no-kv-events`) and `enable_kv_cache_events: false` on the
worker. Instead of consuming the ZMQ-published KV events that §1.3 uses, the router predicts
cache state locally from its own routing decisions with TTL-based expiration/pruning. The
template's own comments describe this as avoiding a "NATS or JetStream" dependency — that
appears to refer to an alternate/older KV-event distribution path, since §1.3's actual
`--kv-events-config` uses a ZMQ publisher, not NATS; either way, this mode has no external
message-bus dependency of its own. Benefit: simpler deployment for lightweight scenarios.

**Prerequisites**: Chapter 0 complete. No message-bus dependency needed for this variant.

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
