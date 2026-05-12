# DGDR E2E Bring-up Log — 2026-04-29

## Goal

Bring the Intel XPU service up from `DynamoGraphDeploymentRequest` to a working inference endpoint on `emr816613-vm03`.

## Environment snapshot

- Cluster: `intel-xpu-tunnel` (`emr816613-vm03`)
- Namespace in progress: `dynamo-platform`
- Node allocatable: `gpu.intel.com/xe = 3`
- Relevant running components at start:
  - `dynamo-platform-dynamo-operator-controller-manager`
  - `grove-operator`
  - `xpu-smi-exporter`

## Findings so far

1. The Intel allocation code fix is already synced to the remote host and validated separately.
2. `dynamo-platform-nats` from the chart is stuck because its PVC is `Pending`; the namespace currently has no usable NATS endpoint.
3. `nvcr.io/nvidia/ai-dynamo/dynamo-frontend:1.0.0` can run `dynamo.profiler` **if** the repo root is mounted and `PYTHONPATH=/workspace:/workspace/components/src`.
4. The local XPU runtime tar exists on the node:
   - `/home/qyao/gitspace/dynamo-vllm-xpu-base-proxyfix.tar`
5. Importing that tar into host `containerd` via a privileged pod likely succeeded before the pod was evicted for ephemeral-storage pressure; kubelet can now complete a pod with:
   - `docker.io/library/dynamo:vllm-xpu-base-proxyfix`

## Progress update

- Added `hf-token-secret` to `dynamo-platform`.
- Deleted the stale `test-xpu-discovery` DGDR and its failed profiling pods.
- Scaled the broken chart-managed `dynamo-platform-nats` StatefulSet to `0`.
- Added an ephemeral `dynamo-platform-nats-ephemeral` Deployment matching the existing `dynamo-platform-nats` Service selectors so the namespace now has a usable NATS endpoint.
- Diagnosed the main cluster blocker as node-level `DiskPressure`; after freeing space and vacuuming host journals, the node condition flipped back to `DiskPressure=False`.
- Re-ran the profiling environment probe pod. It no longer gets rejected/evicted immediately; it now reaches the normal image-pull phase for `nvcr.io/nvidia/ai-dynamo/dynamo-frontend:1.0.0`.

## Current plan

1. Patch `dynamo-platform` with a minimal NATS deployment that satisfies the existing service name.
2. Recreate `hf-token-secret` in `dynamo-platform`.
3. Create a fresh DGDR that:
   - uses `nvcr.io/nvidia/ai-dynamo/dynamo-frontend:1.0.0` for profiling
   - mounts the repo into the profiling job via `spec.overrides.profilingJob`
   - points generated worker services to `docker.io/library/dynamo:vllm-xpu-base-proxyfix`
   - keeps Intel XPU worker requirements (`gpuType`, `supplementalGroups`, `block-size 64`)
4. Watch profiling → DGD → deployment → inference.

## Notes

- If DGDR still fails after the above, the next suspects are:
  - profiling job Python path / mount details
  - worker image availability in `containerd`
  - runtime-specific XPU flags or model access

## Final outcome

- `xpu-e2e-agg` successfully completed the practical bring-up path from `DGDR` profiling output to a live Intel XPU frontend + worker pair in `dynamo-platform`.
- Verified live pods at the end:
  - `xpu-e2e-agg-frontend-787b6c8d6d-d59nz`
  - `xpu-e2e-agg-vllmdecodeworker-c5d255f3-789bc4fb9b-t6z68`
- Verified inference path:
  - `GET /v1/models` returned `Qwen/Qwen3-0.6B`
  - `POST /v1/chat/completions` returned a normal completion payload

## What finally unblocked the service

1. **Stopped depending on the local `NeverPull` runtime image**
   - The original generated DGD pointed frontend/worker at `docker.io/library/dynamo:vllm-xpu-base-proxyfix`.
   - Repeated `containerd` import attempts kept re-triggering node `DiskPressure`.
   - Switched runtime image to:
     - `amr-registry.caas.intel.com/aiops/catalog/intel-vllm-dynamo:main-20260401-r1`

2. **Recovered the operator/webhook**
   - The controller had to be switched from the missing local image `dynamo-operator:xpu-phase1` to:
     - `nvcr.io/nvidia/ai-dynamo/kubernetes-operator:1.0.0`
   - Restored the full container `command` / `args` / probes / mounts after an intermediate patch dropped them.

3. **Freed enough node disk to keep kubelet stable**
   - Removed the stale local XPU runtime image and old operator image from Docker.
   - Deleted large exited Dynamo containers and many evicted pods.
   - This brought the node back under kubelet eviction thresholds and allowed the operator and Intel GPU plugin to recover.

4. **Restored Intel GPU allocatable resources**
   - `gpu.intel.com/xe` temporarily dropped to `0` because `intel-gpu-plugin` had been evicted.
   - Restarting the DaemonSet restored node capacity/allocatable back to `3`.

5. **Freed stale GPU consumers**
   - Scaled the stale `vllm-test/vllm-xpu` deployment to `0` and deleted its failed pods so the new worker could schedule.

6. **Bypassed failing online model download**
   - The worker could not resolve the Intel proxy hostname from inside the pod.
   - Mounted the host Hugging Face cache:
     - host: `/home/qyao/.cache/huggingface`
     - container: `/home/dynamo/.cache/huggingface`
   - Replaced the generated worker `--model` argument with the local snapshot path:
     - `/home/dynamo/.cache/huggingface/hub/models--Qwen--Qwen3-0.6B/snapshots/c1899de289a04d12100db370d81485cdf75e47ca`

7. **Fixed frontend worker discovery**
   - The running worker registered under the suffixed namespace:
     - `dynamo-platform-xpu-e2e-agg-c5d255f3`
   - Frontend originally lacked `DYN_NAMESPACE_WORKER_SUFFIX`, so `/v1/models` stayed empty.
   - Injected:
     - `DYN_NAMESPACE_WORKER_SUFFIX=c5d255f3`
   - Also mounted the same Hugging Face cache into the frontend so its model loading path could resolve the worker-reported local source path.

## Residual note

- The `DynamoGraphDeployment` status still reports a reconcile failure on the K8s discovery role:
  - `failed to sync the k8s discovery role: spec not found in source object`
- Despite that stale CR status, the live service path is working and inference is successful.

## 2026-05-06 follow-up: XPUMD v2 discovery validation

- Cluster used again:
  - `intel-xpu-tunnel` on `emr816613-vm03`
  - namespace: `dynamo-platform`
- Goal:
  - validate the new **solution 2** path where operator discovery parses Intel XPUMD v2 metrics directly instead of relying on the custom `xpu_smi_exporter.py` metric contract only.

### Code path validated

- `deploy/operator/internal/gpu/discovery.go` was updated so Intel discovery now:
  - accepts both legacy `xpu_device_info` / `xpu_memory_total_bytes` / `xpu_device_count`
  - and XPUMD v2 Prometheus families such as:
    - `hw_gpu_info`
    - `hw_memory_size_bytes`
  - recognizes XPUMD pods via `app.kubernetes.io/name=xpumd`
  - tries Intel endpoints in order:
    - `http://<podIP>:9966/metrics`
    - `http://<podIP>:8080/metrics`

### Cluster-side validation steps

1. Rebuilt and rolled out a patched operator image on `dynamo-platform`.
2. Recovered node stability after kubelet started evicting pods for ephemeral storage:
   - reclaimed ~54.6 GiB from Docker caches/images
   - restarted `intel-gpu-plugin`
   - confirmed:
     - `DiskPressure=False`
     - `gpu.intel.com/xe=3`
3. Deployed a privileged XPUMD v2 pod in `intel-xpumd` with:
   - image: `ghcr.io/intel/xpumanager/xpumd:v2.0.0-rc.0`
   - `/dev/dri` hostPath mount
   - `/run/xpumd` hostPath mount
   - Prometheus exporter enabled on `:8080`
4. Port-forwarded `/metrics` and confirmed live XPUMD metric names on this cluster:
   - `hw_gpu_info`
   - `hw_memory_size_bytes`
   - `hw_memory_usage_bytes`
   - `hw_memory_bandwidth_*`

### Operator log proof

Creating DGDR `xpumd-discovery-validate` with **no hardware block** triggered live discovery from the XPUMD pod. Operator logs showed:

```text
Parsed Intel XPU info ... gpuCount=3 model="Intel(R) Arc(TM) Pro B60 Graphics" pciDeviceID="e211" vramMiB=24480 system="b60"
Scraped GPU metrics exporter pod ... source="intel-xpu" pod="xpumd-test" endpoint="http://10.233.114.124:8080/metrics" sku="b60"
GPU discovery completed successfully ... gpusPerNode=3 totalGpus=3 vramMiB=24480 system="b60"
```

### Result

- **XPUMD v2 discovery path works on the real Intel XPU cluster** after the operator parser changes.
- The compatibility work is no longer just theoretical:
  - the operator consumed live XPUMD metrics
  - selected the XPUMD pod by label
  - used the `:8080/metrics` fallback
  - inferred **B60 / 3 GPUs / 24480 MiB** correctly.

## 2026-05-06 follow-up: XPUMD base pod + compatibility exporter sidecar

- The previous direct-operator XPUMD support was **not** the final target architecture.
- The desired shape was validated on the same cluster as:
  - container 1: `ghcr.io/intel/xpumanager/xpumd:v2.0.0-rc.0`
  - container 2: `python:3.10-slim` running `deploy/observability/xpu_smi_exporter.py --source xpumd --xpumd-endpoint http://127.0.0.1:8080/metrics`
- Important deployment property:
  - only the `xpumd` container mounts `/dev/dri` and `/run/xpumd`
  - the exporter sidecar has **no host binary/library mounts**
  - metrics are bridged over localhost inside the pod

### Sidecar output proof

- Port-forwarding the exporter sidecar and scraping `:9966/metrics` returned the legacy-compatible schema again, including:
  - `xpu_device_count{node_name="emr816613-vm03"} 3`
  - `xpu_device_info{device_id="0",device_name="Intel(R) Arc(TM) Pro B60 Graphics",pci_device_id="0xe211",...} 1`
  - `xpu_memory_total_bytes{device_id="0",node_name="emr816613-vm03"} 25669140480`
  - `xpu_memory_used_bytes`, `xpu_memory_free_bytes`, `xpu_memory_utilization_ratio`
  - `xpu_frequency_mhz`, `xpu_power_watts`, `xpu_temperature_celsius`
  - `xpu_pcie_*_bytes_per_second`, `xpu_memory_*_bytes_per_second`

### Operator proof

- Creating DGDR `xpumd-adapter-discovery-validate` showed the operator scraping the adapter pod through the legacy endpoint:

```text
Parsed Intel XPU info ... node="emr816613-vm03" gpuCount=3 model="Intel(R) Arc(TM) Pro B60 Graphics" pciDeviceID="0xe211" vramMiB=24480 system="b60"
Scraped GPU metrics exporter pod ... source="intel-xpu" pod="xpumd-adapter-test" endpoint="http://10.233.114.122:9966/metrics" sku="b60"
GPU discovery completed successfully ...
```

### Extra bug found during validation

- Because the cluster still had the old standalone `xpumd-test` pod, the operator counted both Intel exporter pods on the **same node** as separate nodes and temporarily reported:
  - `nodesWithGPUs=2`
  - `totalGpus=6`
- Root cause:
  - `discoverPrometheusPods()` counted matching exporter pods instead of deduplicating by `pod.Spec.NodeName`.
- Fix applied in code:
  - dedupe Intel/NVIDIA exporter results by node name before computing `NodesWithGPUs`
  - added a regression test covering multiple Intel exporter pods on one node

### Repo artifacts

- Added adapter-aware exporter implementation:
  - `deploy/observability/xpu_smi_exporter.py`
- Added XPUMD adapter unit coverage:
  - `deploy/observability/test_xpu_smi_exporter.py`
- Added a reference pod manifest for the validated sidecar shape:
  - `deploy/observability/k8s/xpumd-adapter-pod.yaml`
