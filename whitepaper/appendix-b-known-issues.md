# Appendix B — Known Issues / Workarounds

- **DRA GPU count not auto-discovered by Planner** (§1.7): the Planner cannot read GPU counts
  directly from `ResourceClaim`s the way it reads `resources.limits.gpu` on NVIDIA. Manually set
  `prefill_engine_num_gpu`/`decode_engine_num_gpu` in the Planner's `--config` JSON to match your
  actual per-worker TP size, and keep them in sync whenever you change replica/TP configuration.
- **Aggregated mode + KV Router limitation** (§1.3): `VllmDecodeWorker` in aggregated mode does
  not emit `BlockStored` KV events (only produced during prefill in disaggregated mode), so KV
  Router falls back to non-cache-aware routing. Use §1.8 (`disagg_router_xpu_dra.yaml`) if
  genuine KV-aware routing matters for your use case.
- **Custom XPU image required for every worker** (§0.3): forgetting to build/push
  `vllm-runtime-xpu` (or leaving the placeholder tag `my-tag` unedited) is the most likely
  first-run failure — pods will `ImagePullBackOff` or `CrashLoopBackOff` immediately.
- **DRA API version mismatch**: all 8 templates use `apiVersion: resource.k8s.io/v1`. Clusters
  running older Kubernetes with only `resource.k8s.io/v1alpha3` or `v1beta1` DRA support will
  fail to apply these templates as-is; upgrade Kubernetes or check the Intel resource drivers'
  compatibility matrix.
