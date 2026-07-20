# 1.2 Aggregated + Tracing (`agg_tracing_xpu_dra.yaml`)

**Status**: 🚧 documented from repo, not yet run hands-on.

**Overview**: Same aggregated topology as §1.1, plus OpenTelemetry tracing instrumentation.
Adds `DYN_LOGGING_JSONL=true` and `OTEL_EXPORT_ENABLED=true` at the `DynamoGraphDeployment`
level, and per-service `OTEL_SERVICE_NAME` env vars (`dynamo-frontend`, `dynamo-worker-vllm`).

**Prerequisites**: Chapter 0 complete, plus an OpenTelemetry collector reachable from the
cluster (the template's default `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` points at
`http://tempo.observability.svc.cluster.local:4317` — a Grafana Tempo instance in an
`observability` namespace; update or remove this if you use a different backend or haven't
deployed one). See `docs/observability/tracing.md` in the repo for collector setup.

**Deployment Steps**:

```bash
# Edit OTEL_EXPORTER_OTLP_TRACES_ENDPOINT first if your collector endpoint differs
kubectl apply -f examples/backends/vllm/deploy/xpu/agg_tracing_xpu_dra.yaml -n ${NAMESPACE}
```

**Verification / Inference Test**: same as §1.1, but against Deployment
`vllm-agg-tracing-xpu-frontend`. Additionally confirm traces are arriving at your collector
(e.g. search for service name `dynamo-worker-vllm` in Tempo/Jaeger UI).

**Troubleshooting**:
- No traces appearing: confirm `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is reachable from the pod's
  network namespace (`kubectl exec` into the pod and `curl`/`nc` the endpoint).
- If you have no observability stack deployed, remove the tracing env vars rather than pointing
  at a non-existent endpoint, to avoid startup delays from repeated failed export attempts.

**Cleanup**: `kubectl delete -f .../agg_tracing_xpu_dra.yaml -n ${NAMESPACE}`

**Config Reference**:

| Field | Value | Notes |
|---|---|---|
| `DynamoGraphDeployment` name | `vllm-agg-tracing-xpu` | |
| Extra env (deployment-level) | `DYN_LOGGING_JSONL=true`, `OTEL_EXPORT_ENABLED=true`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | |
| Extra env (Frontend) | `OTEL_SERVICE_NAME=dynamo-frontend` | |
| Extra env (Worker) | `OTEL_SERVICE_NAME=dynamo-worker-vllm` | |
