# Appendix A — Environment Variables Reference

| Variable | Where set | Purpose |
|---|---|---|
| `VLLM_TARGET_DEVICE` | worker container | Must be `xpu` for every Intel XPU worker |
| `HF_TOKEN` | `hf-token-secret` (via `envFromSecret`) | HuggingFace model download auth |
| `DYN_ROUTER_MODE` | Frontend container | `kv` enables KV-aware routing |
| `DYN_LOGGING_JSONL` | deployment-level env | JSON-lines structured logging, used with tracing |
| `OTEL_EXPORT_ENABLED` | deployment-level env | Enables OpenTelemetry trace export |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | deployment-level env | OTLP collector endpoint (update per environment) |
| `OTEL_SERVICE_NAME` | per-service container env | Service name shown in trace UI |
