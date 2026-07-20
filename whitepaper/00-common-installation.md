# Chapter 0 — Common Installation

All 8 templates in Chapter 1, plus the Global Planner example in Chapter 2, share the same
cluster-level prerequisites. Do this section once per cluster.

## 0.1 Cluster Requirements

- **Kubernetes v1.34+** with the DRA (Dynamic Resource Allocation) `resource.k8s.io/v1` API
  enabled (DRA graduated to GA/stable behavior tracked by the templates; older DRA API versions
  such as `v1alpha3`/`v1beta1` are not compatible with the `apiVersion: resource.k8s.io/v1`
  used by every current template).
- Intel XPU (GPU) nodes, with the Intel GPU kernel driver installed on each node.
- **[Intel resource drivers for Kubernetes](https://github.com/intel/intel-resource-drivers-for-kubernetes)**
  installed, exposing a `DeviceClass` named `gpu.intel.com`. Verify with:
  ```bash
  kubectl get deviceclass gpu.intel.com
  ```
- Sufficient node resources for the model under test — the default model across all
  Chapter 1/2 templates is the small `Qwen/Qwen3-0.6B`, chosen deliberately by the upstream repo
  to keep XPU examples runnable on a single GPU.

## 0.2 Install the Dynamo Kubernetes Platform

Follow `docs/kubernetes/installation-guide.md` in the `dynamo` repo. Summary of the steps that
apply regardless of accelerator vendor:

```bash
export NAMESPACE=dynamo-cloud
export RELEASE_VERSION=<dynamo-release-version>   # e.g. v0.x.y, see repo releases

# 1. Install core CRDs (cluster-scoped, install once per cluster)
helm install dynamo-crds \
  oci://ghcr.io/ai-dynamo/dynamo-crds \
  --version ${RELEASE_VERSION} \
  --namespace default

# 2. Install the Dynamo Platform (operator + supporting services) into your namespace
kubectl create namespace ${NAMESPACE} 2>/dev/null || true
helm install dynamo-platform \
  oci://ghcr.io/ai-dynamo/dynamo-platform \
  --version ${RELEASE_VERSION} \
  --namespace ${NAMESPACE}
```

> **Intel XPU note**: unlike NVIDIA deployments, you do **not** need the NVIDIA GPU Operator.
> Skip any GPU-Operator-specific step in the installation guide; the Intel device plugin/DRA
> driver from step 0.1 takes its place.

Confirm the operator is healthy:

```bash
kubectl get pods -n ${NAMESPACE}
kubectl get crd | grep dynamo
```

## 0.3 Build an Intel XPU vLLM Runtime Image

None of the 8 templates work with the stock `vllm-runtime` image — worker containers require a
dedicated `vllm-runtime-xpu` image built with `VLLM_TARGET_DEVICE=xpu`. Build it from the repo's
`container/` tooling:

```bash
cd dynamo/container
python render.py --framework=vllm --device=xpu --target=runtime
docker build -t <your-registry>/vllm-runtime-xpu:my-tag \
  -f vllm-runtime-xpu-amd64-rendered.Dockerfile .
docker push <your-registry>/vllm-runtime-xpu:my-tag
```

See `container/README.md` for the full, up-to-date build instructions and base image options.
Substitute `<your-registry>/vllm-runtime-xpu:my-tag` for every occurrence of
`nvcr.io/nvidia/ai-dynamo/vllm-runtime-xpu:my-tag` in the templates below, either by editing the
YAML or by using `envsubst` with an image-name environment variable if your fork of the template
parameterizes it.

The Frontend and Planner components do **not** need the XPU image — they run on the default
`vllm-runtime` / `dynamo-planner` images (CPU-only control-plane components).

## 0.4 HuggingFace Token Secret

Every template references `envFromSecret: hf-token-secret`. Create it once per namespace:

```bash
export HF_TOKEN=<your-huggingface-token>
kubectl create secret generic hf-token-secret \
  --from-literal=HF_TOKEN=${HF_TOKEN} \
  -n ${NAMESPACE}
```

## 0.5 Optional: `securityContext` for Render/Video Group Access

Every worker container block in every template includes a commented-out `securityContext`
snippet:

```yaml
# NOTE: Uncomment if your environment requires specific group access
# securityContext:
#   runAsUser: 1000
#   runAsGroup: 1000
#   supplementalGroups:
#     - 44   # render group
#     - 991  # video group
```

Uncomment and adjust the GIDs if your cluster's Pod Security Admission policy or your node
image's `/dev/dri` device permissions require the container process to belong to specific
`render`/`video` groups to access the Intel GPU. GIDs 44/991 are the upstream template's
defaults and may differ from your actual node OS image — check
`getent group render video` on a node.

---
