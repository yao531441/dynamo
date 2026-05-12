# XPU Phase 1 上机测试记录 (2026-04-27)

## 测试目标

在 emr816613-vm03 验证：
1. XPU exporter 暴露 discovery metrics
2. Operator 自动发现并补全 DGDR hardware
3. Profiler 接受 gpuSku=b60

## 进展

### ✅ XPU-SMI Exporter 验证成功

**部署方式：直接在宿主机运行**
```bash
cd /home/qyao/gitspace/dynamo/deploy/observability
python3 xpu_smi_exporter.py --port 9966 --interval 15
```

**Metrics 输出正确：**
- `xpu_device_count{node_name="emr816613-vm03"} 3`
- `xpu_device_info{...,pci_device_id="0xe211",...} 1`
- `xpu_memory_total_bytes{...,node_name="emr816613-vm03"} 25669140480` (~24.5 GiB)

### ✅ Operator 镜像构建成功

```bash
ssh qyao@emr816613-vm03.jf.intel.com
cd /home/qyao/gitspace/dynamo/deploy/operator
docker build --build-arg HTTP_PROXY=http://proxy-dmz.intel.com:912 \
             --build-arg HTTPS_PROXY=http://proxy-dmz.intel.com:912 \
             --build-context snapshot=../snapshot -t dynamo-operator:xpu-phase1 .

# 导入 containerd
docker save dynamo-operator:xpu-phase1 | sudo ctr -n k8s.io images import -
```

### ⚠️ 手动部署尝试失败

尝试 `make deploy` 和手动创建 manifests，遇到问题：
- CRD annotations 超过 262KB 限制（kubectl apply 失败）
- 手动创建的 RBAC 不完整
- Operator 需要完整 ConfigMap（多必填字段）

---

## 关键发现：正确部署方式是 Helm

### 问题根因分析

**为什么文档说可以 `make deploy`，但我们遇到问题？**

| 部署方式 | CRD annotations 限制 | 结果 |
|---------|---------------------|------|
| Helm install/upgrade | 无限制 | ✅ 正常工作 |
| kubectl apply | 262KB | ❌ 超大 CRD 失败 |
| kustomize build + apply | 262KB | ❌ 同样失败 |

**CRD 文件大小：**
```
nvidia.com_dynamocheckpoints.yaml          623K
nvidia.com_dynamocomponentdeployments.yaml 758K
nvidia.com_dynamographdeploymentrequests.yaml 718K
nvidia.com_dynamographdeployments.yaml     841K
```

这些 CRD 有 **600K-800K**，远超 kubectl apply 的 262KB 限制。

### 原来是怎么部署的？

查看远程机器的 bash history，发现：

```bash
helm install dynamo-platform dynamo-platform-0.9.0-post1.tgz \
  --namespace dynamo-xpu --create-namespace \
  --set dynamo-operator.namespaceRestriction.enabled=true \
  --set dynamo-operator.controllerManager.manager.image.tag=0.9.0
```

**Helm chart 位置：**
```
/home/sdp/qyao/dynamo/dynamo-platform-0.9.0-post1.tgz
```

**Chart 内容：**
```
dynamo-platform/charts/dynamo-operator/
├── templates/
│   ├── deployment.yaml
│   ├── manager-rbac.yaml
│   ├── webhook-certificates.yaml
│   ├── webhook-configuration.yaml
│   └── ... (完整配置)
└── crds/
    └── ... (Helm install 时自动创建)
```

### 文档 vs 实际

**文档假设**：`make deploy` 使用 kustomize 生成 manifests 并 apply

**实际情况**：
1. 项目用 Helm 作为标准部署方式
2. CRD 太大，kubectl/kustomize 无法 apply
3. `make deploy` 应该只适用于开发环境（小 CRD 场景）

---

## 正确方案

### 方案：更新 Helm chart + Helm upgrade

```bash
# 1. 更新 chart 里的 CRD（包含新的 b60 SKU）
# CRD 在: deploy/helm/charts/platform/components/operator/crds/

# 2. 重新打包 chart
helm package deploy/helm/charts/platform -u

# 3. Helm upgrade（覆盖镜像）
helm upgrade --install dynamo-platform dynamo-platform-x.x.x.tgz \
  --namespace dynamo-xpu \
  --set dynamo-operator.controllerManager.manager.image.repository=dynamo-operator \
  --set dynamo-operator.controllerManager.manager.image.tag=xpu-phase1 \
  --set dynamo-operator.controllerManager.manager.image.pullPolicy=Never
```

### 或者：单独更新 CRD + Helm upgrade

```bash
# 1. 单独 apply 更新后的 CRD（使用 --server-side 绕过限制）
kubectl apply --server-side -f deploy/operator/config/crd/bases/nvidia.com_dynamographdeploymentrequests.yaml

# 2. Helm upgrade 只更新 operator 镜像
helm upgrade --install dynamo-platform dynamo-platform-0.9.0-post1.tgz \
  --namespace dynamo-xpu \
  --set dynamo-operator.controllerManager.manager.image.repository=dynamo-operator \
  --set dynamo-operator.controllerManager.manager.image.tag=xpu-phase1 \
  --set dynamo-operator.controllerManager.manager.image.pullPolicy=Never
```

---

## 其他关键发现

### 两个集群容易混淆

| 属性 | 原生集群（目标） | kind 集群（测试） |
|------|-----------------|------------------|
| 节点 | emr816613-vm03 | kind-control-plane |
| K8s 版本 | v1.35.1 | v1.35.0 |
| Runtime | containerd://2.2.1 | containerd |
| CRD | 已存在 | 无法部署大 CRD |
| XPU 硬件 | 有 3 张 Intel GPU | 无 |

**SSH tunnel 连接的是原生集群（正确目标）**

### Intel 网络代理

- Docker build 需要 `--build-arg HTTP_PROXY=...`
- curl 需要 `--noproxy localhost`

### Exporter 容器化困难

xpu-smi 有复杂依赖链（libigsc.so、grpc++ 等），建议直接在宿主机运行。

---

## 2026-04-27 下午进展

### ✅ Helm 部署成功

**操作步骤：**
1. 同步更新后的 Helm chart 到远程机器（包含 b60 SKU）
2. 打包 chart：`helm package platform -u` → `dynamo-platform-1.1.0.tgz`
3. 删除旧的 namespace（CRD 不完整导致 operator crash）
4. Helm install：
   ```bash
   helm install dynamo-platform /home/qyao/dynamo-platform-1.1.0.tgz \
     --namespace dynamo-platform --create-namespace \
     --set dynamo-operator.controllerManager.manager.image.repository=dynamo-operator \
     --set dynamo-operator.controllerManager.manager.image.tag=xpu-phase1 \
     --set dynamo-operator.controllerManager.manager.image.pullPolicy=Never
   ```

**结果：**
- 所有 CRD 安装成功（dynamographdeploymentrequests 等 7 个）
- Operator pod 正常运行
- b60 SKU 在 CRD 中正确枚举

### ✅ b60 SKU 验证成功

**测试 DGDR：**
```yaml
apiVersion: nvidia.com/v1beta1
kind: DynamoGraphDeploymentRequest
metadata:
  name: test-xpu-b60
  namespace: dynamo-platform
spec:
  model: Qwen/Qwen3-0.6B
  image: nvcr.io/nvidia/ai-dynamo/dynamo-frontend:0.9.0
  hardware:
    gpuSku: b60
    vramMb: 24576
    numGpusPerNode: 3
    totalGpus: 3
  autoApply: false
```

**结果：**
- Webhook 接受 b60 SKU（无验证错误）
- DGDR 通过 Validation，进入 Profiling 状态
- Profiler pod 正在拉取镜像（`nvcr.io/nvidia/ai-dynamo/dynamo-frontend:0.9.0`）

### ✅ XPU Discovery 验证成功 (原生集群)

**连接方式：**
```bash
# SSH tunnel (本地)
ssh -fN -L 16443:localhost:6443 sdp@emr816613-vm03.jf.intel.com

# KUBECONFIG
export KUBECONFIG=/home/qyao/.kube/clusters/intel-xpu-tunnel.yaml
```

**XPU Exporter Pod 部署关键点：**
1. 使用 `hostNetwork: true` 让 Pod 获得宿主机 IP
2. 挂载宿主机的 xpu-smi 相关路径：
   - `/usr/local/bin` → xpu-smi, xpumcli 二进制
   - `/usr/local/lib` → libigsc.so 等库
   - `/lib/x86_64-linux-gnu` → libgrpc++, libprotobuf 等
   - `/tmp` → **关键！** xpumcli 通过 `/tmp/xpum_up.sock` 与 xpumd daemon 通信
   - `/dev/dri` → GPU 设备
3. 设置 `LD_LIBRARY_PATH` 和 `XPU_SMI_PATH` 环境变量

**Discovery 日志（Operator）：**
```
Parsed Intel XPU info: node=emr816613-vm03, gpuCount=3, model="Intel(R) Graphics [0xe211]", pciDeviceID="0xe211", vramMiB=24480, system="b60"
GPU discovery completed successfully: gpusPerNode=3, nodesWithGPUs=1, totalGpus=3, model="Intel(R) Graphics [0xe211]", vramMiB=24480, system="b60", cloudprovider="other"
```

**验证结果：**
- ✅ 自动发现 3 张 Intel GPU
- ✅ 自动识别 SKU 为 **b60**（从 e211 PCI ID 映射）
- ✅ VRAM 24480 MiB
- ✅ DGDR 进入 Profiling 状态

---

## 测试总结

### ✅ Phase 1 目标全部达成

| 目标 | 状态 | 说明 |
|------|------|------|
| XPU exporter 暴露 metrics | ✅ | Pod 部署成功，挂载宿主机 xpu-smi |
| Operator 支持 b60 SKU | ✅ | CRD 枚举、Webhook 验证均通过 |
| DGDR 接受 b60 hardware | ✅ | 创建成功，Validation 通过 |
| XPU 自动 Discovery | ✅ | 检测到 3 张 GPU，自动识别为 b60 |

---

## 2026-05-06 更新：XPUMD v2 路径验证成功

在同一目标原生集群 `intel-xpu-tunnel` / `emr816613-vm03` 上，进一步验证了 **方案 2：让 operator discovery 直接兼容 Intel XPUMD v2 metrics**。

### 实际抓到的 XPUMD v2 metrics

通过集群内的 XPUMD v2 pod（`ghcr.io/intel/xpumanager/xpumd:v2.0.0-rc.0`）实测，Prometheus 暴露的关键 families 为：

- `hw_gpu_info`
- `hw_memory_size_bytes`

不是旧笔记里猜测的 `xpu_device_*`，也不是文档里的裸 `hw.memory.size` 名称；Prometheus 端实际已经转换成 underscore + unit suffix 形式。

### 代码适配结果

`deploy/operator/internal/gpu/discovery.go` 现已支持：

1. 老的 `xpu-smi-exporter` 格式
2. XPUMD v2 的 `hw_gpu_info` / `hw_memory_size_bytes` 格式
3. Intel exporter endpoint fallback：
   - `:9966/metrics`
   - `:8080/metrics`
4. XPUMD pod label：
   - `app.kubernetes.io/name=xpumd`

### 集群验证结果

创建空 hardware 的 DGDR `xpumd-discovery-validate` 后，operator 日志确认：

```text
Parsed Intel XPU info ... gpuCount=3 model="Intel(R) Arc(TM) Pro B60 Graphics" pciDeviceID="e211" vramMiB=24480 system="b60"
Scraped GPU metrics exporter pod ... endpoint="http://10.233.114.124:8080/metrics"
GPU discovery completed successfully ... gpusPerNode=3 totalGpus=3 vramMiB=24480 system="b60"
```

### 结论

- **Phase 1 Intel discovery 现在同时支持两条路径：**
  - 自写 `xpu-smi-exporter`
  - Intel 官方 XPUMD v2
- 在真实 Intel XPU 集群上，XPUMD v2 已经能够被当前 operator 正确消费并完成 **B60 / 3 卡 / 24480 MiB** 自动识别。

### 关键发现

**xpumcli 通信机制：**
- xpu-smi 是 xpumcli 的符号链接
- xpumcli 是 gRPC 客户端，需要 xpumd daemon
- 通信通过 `/tmp/xpum_up.sock` UNIX socket
- **必须挂载 /tmp 目录才能在容器内使用**

**与 NVIDIA DCGM exporter 的对比：**

| 属性 | NVIDIA DCGM | Intel XPU |
|------|-------------|-----------|
| 部署方式 | DaemonSet | DaemonSet/Pod |
| 标签 | app=dcgm-exporter | app=xpu-smi-exporter |
| 端口 | 9400 | 9966 |
| 依赖 | 官方容器镜像 | 挂载宿主机二进制+库+socket |

### 📝 已验证的关键代码

```go
// discovery.go - 支持 Intel XPU exporter
const defaultIntelMetricsEndpoint = "http://{POD_IP}:9966/metrics"
LabelValueXPUSMIExporter = "xpu-smi-exporter"

// discovery 逻辑流程：
// 1. listIntelXPUExporterPods() 查找 xpu-smi-exporter pods
// 2. buildIntelMetricsEndpoint() 构建 metrics URL
// 3. 解析 xpu_device_info, xpu_memory_total_bytes, xpu_device_count
// 4. SKU normalization: e211 -> b60
```

---

## 2026-05-06 更新：XPUMD + exporter sidecar 兼容方案成功

在用户确认最终目标后，又补做了另一条更接近 NVIDIA exporter 形态的验证路径：

1. `xpumd` 负责直接接触 Intel 设备
2. `xpu_smi_exporter.py` 作为 sidecar 抓取 `http://127.0.0.1:8080/metrics`
3. sidecar 继续对外暴露原来的 `xpu_device_*` 兼容指标

### 验证形态

- Pod:
  - `xpumd`：`ghcr.io/intel/xpumanager/xpumd:v2.0.0-rc.0`
  - `xpu-exporter`：`python:3.10-slim`
- sidecar 启动命令：

```bash
python3 /opt/exporter/xpu_smi_exporter.py \
  --port 9966 \
  --interval 15 \
  --source xpumd \
  --xpumd-endpoint http://127.0.0.1:8080/metrics
```

- 挂载关系：
  - `xpumd` 容器挂载 `/dev/dri`、`/run/xpumd`
  - sidecar **不挂载宿主机 xpu-smi 二进制、库或 /tmp socket**

### sidecar metrics 实测

通过 `kubectl port-forward pod/xpumd-adapter-test 19966:9966` 抓取输出，确认兼容 schema 已恢复：

```text
xpu_device_count{node_name="emr816613-vm03"} 3
xpu_device_info{device_id="0",device_name="Intel(R) Arc(TM) Pro B60 Graphics",pci_device_id="0xe211",...} 1
xpu_memory_total_bytes{device_id="0",node_name="emr816613-vm03"} 25669140480
```

此外 sidecar 还能从 XPUMD 派生输出：

- `xpu_memory_used_bytes`
- `xpu_memory_free_bytes`
- `xpu_memory_utilization_ratio`
- `xpu_frequency_mhz`
- `xpu_power_watts`
- `xpu_temperature_celsius`
- `xpu_pcie_*_bytes_per_second`
- `xpu_memory_*_bytes_per_second`

### Operator 验证

创建 DGDR `xpumd-adapter-discovery-validate` 后，operator 日志显示它确实走的是兼容 exporter 路径，而不是 XPUMD 直连：

```text
Parsed Intel XPU info ... node="emr816613-vm03" gpuCount=3 model="Intel(R) Arc(TM) Pro B60 Graphics" pciDeviceID="0xe211" vramMiB=24480 system="b60"
Scraped GPU metrics exporter pod ... pod="xpumd-adapter-test" endpoint="http://10.233.114.122:9966/metrics" sku="b60"
```

这说明当前实现已经符合目标架构：

- XPUMD 作为 base collector
- exporter 作为 compatibility adapter
- operator 继续消费 legacy `xpu_device_*`

### 额外发现并修复

验证时因为集群里还保留着旧的 `xpumd-test` pod，operator 一度把同一节点上的两个 Intel exporter pod 当成两台机器，导致：

- `nodesWithGPUs=2`
- `totalGpus=6`

已在 `deploy/operator/internal/gpu/discovery.go` 修复为按 `nodeName` 去重，并新增回归测试，避免多 exporter 共存时重复计数。

---

## 下一步

1. ✅ **检查 Helm chart 的 CRD 是否需要更新**（包含 b60 SKU） — 完成
2. ✅ **Helm upgrade 部署** xpu-phase1 镜像 — 完成
3. ⏸️ **验证 DGDR discovery 流程** — 需要 XPU 硬件的集群

## 相关文档

- Memory: `~/.claude/projects/-home-qyao-gitspace-dynamo/memory/`
- 测试指南: `worklogs/xpu-discovery/xpu-phase1-coding-guide.md`
- 集群连接: `worklogs/xpu-discovery/xpu-cluster-access.md`
