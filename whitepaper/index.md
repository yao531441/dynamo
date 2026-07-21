<!--
Dynamo on Intel XPU — Deployment Whitepaper
This is the index/table-of-contents file. Each chapter/case is its own Markdown file in this
directory so no single document grows unmanageably long. Convert individual files or the whole
directory to PDF/Word with pandoc — see ../README.md.
Template reference: ../template.md
-->

# Dynamo on Intel XPU — Deployment Whitepaper

> **Scope**: This document covers deploying [Dynamo](https://github.com/ai-dynamo/dynamo) on
> Intel XPU (GPU) hardware in Kubernetes, using the official Intel XPU templates shipped in the
> Dynamo repository. It replaces informal/outdated internal notes on Dynamo + Intel XPU with a
> single source of truth, driven directly from the current state of the `dynamo` repository.
>
> All content below is derived from reading the actual repository files (YAML templates,
> `README.md` files, `docs/kubernetes/*`). Nothing has been executed against a live Intel XPU
> Kubernetes cluster yet — see the Status line in each chapter, and the companion `tracking.md`
> file for the current per-case validation status. Validate on real hardware before treating any
> command as final.
>
> **API version note**: Chapter 1's 8 templates use the `nvidia.com/v1alpha1` CRD schema
> (`services:` map) — this is what the repo ships for all 8 Intel XPU patterns today. Dynamo is
> mid-migration to a newer `nvidia.com/v1beta1` schema (`components:` list, a real structural
> change, not just a version bump), but **XPU coverage under `v1beta1` is only partial**: just 2
> of the 8 patterns (`agg`, `disagg`) have been ported so far — see the companion document
> `to_fi.md` (shipped alongside this whitepaper, not included in this compiled PDF) for the full
> breakdown. If you're on a newer Dynamo Platform release that expects `v1beta1`, check
> `deploy/v1beta1/xpu/` for the latest state before assuming all 8 patterns are available there.

## Why this document exists / scope note

**Dynamo's Intel XPU support is concentrated in a single
directory**: [`examples/backends/vllm/deploy/xpu/`](../../dynamo/examples/backends/vllm/deploy/xpu/).
This directory ships **8 flat DRA (Dynamic Resource Allocation) YAML templates** covering
combinations of serving pattern (aggregated / disaggregated) × optional add-ons (tracing,
KV-aware routing, planner-based autoscaling). There is also one Intel XPU example under
`examples/global_planner/`.

A repo-wide search (`grep -r xpu examples/ docs/ recipes/` across the `dynamo` repository) turns
up **no Intel XPU-specific content** under most other vLLM-backend templates, the SGLang backend,
the TensorRT-LLM backend, and most of `recipes/`. It **does** turn up one Intel XPU recipe under
`recipes/qwen3-vl-32b-fp8/` — a heterogeneous Intel XPU (encode) + NVIDIA GPU (decode)
disaggregation, written up in full in Chapter 3. Chapter 4 distinguishes templates that look
**feasible but simply haven't been templated for Intel XPU yet** (LoRA, GAIE, multi-node
disaggregation, GMS/failover — all with no hard hardware blocker found) from the one **genuine
NVIDIA-only dependency** found (TensorRT-LLM's proprietary compiler).

**Bottom line**: as of this writing, if you want to run Dynamo on Intel XPU, your options are
the 8 templates in Chapter 1, the Global Planner example in Chapter 2, and the heterogeneous
Qwen3-VL recipe in Chapter 3. Chapter 4 catalogs real candidates for future Intel XPU work
(LoRA, GAIE, multi-node disagg, GMS/failover, SGLang) versus the one genuine NVIDIA-only dead
end confirmed so far (TensorRT-LLM).

## Table of Contents

> Status labels used below: **Verified** (ran hands-on and confirmed working) / **In Progress**
> (documented from official sources, not yet run hands-on) / **N/A** (not applicable — no Intel
> XPU support exists upstream)

- [00-common-installation.md](00-common-installation.md) — Chapter 0. Common Installation (applies to every case below)
- **Chapter 1. Intel XPU vLLM Backend Templates** (the 8 DRA templates) — all fully written (In Progress)
  - [01-aggregated.md](01-aggregated.md) — 1.1 Aggregated (`agg_xpu_dra.yaml`)
  - [02-aggregated-tracing.md](02-aggregated-tracing.md) — 1.2 Aggregated + Tracing (`agg_tracing_xpu_dra.yaml`)
  - [03-aggregated-kv-router.md](03-aggregated-kv-router.md) — 1.3 Aggregated + KV Router (`agg_router_xpu_dra.yaml`)
  - [04-aggregated-kv-router-no-events.md](04-aggregated-kv-router-no-events.md) — 1.4 Aggregated + KV Router, No-KV-Events (`agg_router_kv_approx_xpu_dra.yaml`)
  - [05-disaggregated.md](05-disaggregated.md) — 1.5 Disaggregated (`disagg_xpu_dra.yaml`) — deep-dive sample case
  - [06-disaggregated-tracing.md](06-disaggregated-tracing.md) — 1.6 Disaggregated + Tracing (`disagg_tracing_xpu_dra.yaml`)
  - [07-disaggregated-planner.md](07-disaggregated-planner.md) — 1.7 Disaggregated + Planner (`disagg_planner_xpu_dra.yaml`)
  - [08-disaggregated-kv-router.md](08-disaggregated-kv-router.md) — 1.8 Disaggregated + KV Router (`disagg_router_xpu_dra.yaml`)
- [09-global-planner.md](09-global-planner.md) — Chapter 2. Global Planner on Intel XPU (`global-planner-vllm-test-xpu-dra.yaml`) (In Progress)
- [10-recipe-qwen3-vl-hetero-xpu-gpu.md](10-recipe-qwen3-vl-hetero-xpu-gpu.md) — Chapter 3. Recipe: Qwen3-VL-32B-FP8 Heterogeneous Hardware Disaggregation (Intel XPU encode + NVIDIA GPU decode) (In Progress)
- [11-not-available-on-xpu.md](11-not-available-on-xpu.md) — Chapter 4. Feasible-but-Untemplated vs. Genuinely NVIDIA-Locked (gap analysis, not a deployment case — the Verified/In Progress/N/A labels above don't apply)
- [appendix-a-env-vars.md](appendix-a-env-vars.md) — Appendix A. Environment Variables Reference
- [appendix-b-known-issues.md](appendix-b-known-issues.md) — Appendix B. Known Issues / Workarounds

> `tracking.md` and `to_fi.md` in this directory are separate, continuously-updated tracking
> documents — useful when browsing the repository directly, but they are not chapters of this
> whitepaper and are not included in the compiled PDF/Word export.
