# 1.7 Disaggregated + Planner (`disagg_planner_xpu_dra.yaml`)

**Overview**: Adds a `Planner` component that consumes pre-profiled throughput/latency curves
(shipped as a `ConfigMap` named `planner-profile-data`, containing `prefill_raw_data.json` and
`decode_raw_data.json`) to make scaling decisions. The template ships **example** profiling data
points (ISL/TTFT/throughput-per-GPU for prefill; KV-usage/context-length/ITL/throughput for
decode) — these are illustrative sample curves, not measured on real Intel XPU hardware, and
must be replaced with your own profiling results before using the Planner for real capacity
decisions (see `dynamo profile` tooling in the repo, if targeting production use).

**Prerequisites**: Chapter 0 complete, plus (recommended) your own hardware-profiled
`prefill_raw_data.json`/`decode_raw_data.json` if you intend to trust the Planner's scaling
recommendations rather than just exercising the deployment mechanics.

**Deployment Steps**:

```bash
kubectl apply -f examples/backends/vllm/deploy/xpu/disagg_planner_xpu_dra.yaml -n ${NAMESPACE}
```

**Verification**:

```bash
kubectl get configmap planner-profile-data -n ${NAMESPACE}
kubectl get pods -n ${NAMESPACE}     # expect Frontend, Planner, VllmDecodeWorker, VllmPrefillWorker
kubectl logs deployment/vllm-disagg-planner-xpu-planner -n ${NAMESPACE}
```

**Inference Test**: same as §1.5, Deployment name `vllm-disagg-planner-xpu-frontend`.

**Troubleshooting**:
- The Planner's `--config` JSON hard-codes `prefill_engine_num_gpu: 1` and
  `decode_engine_num_gpu: 1` — the upstream comment flags this as a "KEY FIX: Add GPU counts
  here for DRA fallback" workaround, because the Planner cannot auto-discover GPU counts from
  DRA `ResourceClaim`s the way it can from `resources.limits.gpu` on NVIDIA. If you change
  replica counts or TP size, update these values manually or the Planner's scaling math will be
  wrong.
- `throughput_adjustment_interval_seconds: 60` controls how often the Planner re-evaluates
  scaling — reduce for faster reaction in a demo, increase to avoid thrashing in production.

**Cleanup**: `kubectl delete -f examples/backends/vllm/deploy/xpu/disagg_planner_xpu_dra.yaml -n ${NAMESPACE}`

**Config Reference**:

| Field | Value | Notes |
|---|---|---|
| `DynamoGraphDeployment` name | `vllm-disagg-planner-xpu` | |
| Extra component | `Planner` (`dynamo.planner`, `dynamo-planner:my-tag` image) | CPU-only, no XPU claim |
| `prefill_engine_num_gpu` / `decode_engine_num_gpu` | `1` / `1` | manual DRA fallback values — must match actual TP size |
| Profile data | `ConfigMap/planner-profile-data` | replace sample curves with real Intel XPU profiling data |
| `throughput_adjustment_interval_seconds` | `60` | |
