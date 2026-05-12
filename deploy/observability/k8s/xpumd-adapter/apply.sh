#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../../../.." && pwd)"
EXPORTER_SCRIPT="${REPO_ROOT}/deploy/observability/xpu_smi_exporter.py"
NAMESPACE="${NAMESPACE:-intel-xpumd}"

kubectl get namespace "${NAMESPACE}" >/dev/null 2>&1 || kubectl create namespace "${NAMESPACE}"
sed "s/namespace: intel-xpumd/namespace: ${NAMESPACE}/g" "${SCRIPT_DIR}/xpumd-config.yaml" | kubectl apply -f -
kubectl -n "${NAMESPACE}" create configmap xpu-smi-exporter-script \
  --from-file=xpu_smi_exporter.py="${EXPORTER_SCRIPT}" \
  --dry-run=client -o yaml | kubectl apply -f -
sed "s/namespace: intel-xpumd/namespace: ${NAMESPACE}/g" "${SCRIPT_DIR}/pod.yaml" | kubectl apply -f -
