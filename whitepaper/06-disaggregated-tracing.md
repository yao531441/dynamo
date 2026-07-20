# 1.6 Disaggregated + Tracing (`disagg_tracing_xpu_dra.yaml`)

**Status**: documented from repo, not yet run hands-on.

**Overview**: Same disaggregated topology as §1.5, plus the same OpenTelemetry tracing pattern
as §1.2 — `DYN_LOGGING_JSONL`/`OTEL_EXPORT_ENABLED` at the deployment level, and per-service
`OTEL_SERVICE_NAME` (`dynamo-frontend`, `dynamo-worker-decode`, `dynamo-worker-prefill`).

**Prerequisites**: Chapter 0 complete + observability collector as in §1.2.

**Deployment Steps**:

```bash
kubectl apply -f examples/backends/vllm/deploy/xpu/disagg_tracing_xpu_dra.yaml -n ${NAMESPACE}
```

**Verification / Inference Test**: same pattern as §1.5, Deployment name
`vllm-disagg-tracing-xpu-frontend`.

**Troubleshooting**: same as §1.2 (tracing endpoint reachability) and §1.5 (KV transfer).

**Cleanup**: `kubectl delete -f .../disagg_tracing_xpu_dra.yaml -n ${NAMESPACE}`

**Config Reference**: identical to §1.5 plus the tracing env vars from §1.2, applied per-role
(`dynamo-worker-decode` / `dynamo-worker-prefill` service names instead of a single
`dynamo-worker-vllm`).
