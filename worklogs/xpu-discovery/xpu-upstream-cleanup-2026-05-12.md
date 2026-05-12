# XPU Upstream Cleanup Backup (2026-05-12)

## Purpose

This note preserves the parts of the Intel XPU bring-up work that were useful
during local development but should not remain in the cleaned-up code commit.

The cleaned code change is now scoped to:

1. direct XPUMD-based Intel XPU discovery in the operator
2. `b60` support for DGDR/profiler
3. minimal DGDR XPU runtime support needed for B60

## Changes intentionally removed from the clean commit

### 1. Abandoned adapter path

The following files were removed from the clean code change because they belong
to the discarded XPUMD-to-compatibility-exporter path rather than the final
direct-XPUMD design:

- `deploy/observability/k8s/xpumd-adapter/apply.sh`
- `deploy/observability/k8s/xpumd-adapter/pod.yaml`
- `deploy/observability/k8s/xpumd-adapter/test-examples/test-xpumd-direct.yaml`
- `deploy/observability/k8s/xpumd-adapter/xpumd-config.yaml`
- `deploy/observability/test_xpu_smi_exporter.py`
- the XPUMD adapter extensions that had been added to
  `deploy/observability/xpu_smi_exporter.py`

Reason:

- upstream target is **operator consumes XPUMD metrics directly**
- adapter/sidecar compatibility logic makes the change noisier and mixes two
  competing designs in one review

### 2. Local-only validation manifests

The following manifest was removed because it is tied to one machine / one
cluster setup and is not portable upstream:

- `tests/e2e/xpu/vllm-inference-test-final.yaml`

### 3. Detailed local worklogs

These detailed logs were removed from the clean code diff and collapsed into
this summary note:

- `worklogs/xpu-discovery/dgdr-e2e-bringup-2026-04-29.md`
- `worklogs/xpu-discovery/xpu-phase1-test-log-2026-04-27.md`
- `worklogs/xpu-discovery/xpumd-adapter-implementation-report-2026-05-06.md`

Reason:

- they are useful as local engineering history
- they are not part of the code change that should be reviewed upstream

## Live cluster validation summary

Validation was run against the actual Intel XPU cluster referenced by the
`xpu-discovery` worklogs.

### Result 1: direct XPUMD discovery path works

On the real cluster, the operator created a profiling job from a DGDR and
auto-filled hardware as:

- `gpuSku=b60`
- `vramMb=24480`
- `totalGpus=3`
- `numGpusPerNode=3`

This confirms the direct XPUMD discovery path is functioning.

### Result 2: old profiler image was the real blocker

Using the stock profiler image alone, the profiling pod failed because the
image's bundled Python code still rejected `b60` in the profiler-side enum
validation.

This showed the remaining issue was **image freshness**, not the operator
discovery logic.

### Result 3: current source passes end-to-end DGDR control flow

By mounting the current repository source into the profiling job and setting:

- `PYTHONPATH=/workspace:/workspace/components/src`

the DGDR completed successfully:

- profiling pod exited `0`
- output ConfigMap was written
- DGDR advanced to `Ready`
- a generated DGD spec was produced

This is enough evidence that the cleaned code path is viable even though full
vLLM runtime deployment was not used as the success criterion for this check.

## Practical implication

For the final usable XPU/B60 flow, the remaining operational requirement is to
build and deploy profiler/runtime images that include the current Python
changes. The operator/discovery control flow itself has already been validated.
