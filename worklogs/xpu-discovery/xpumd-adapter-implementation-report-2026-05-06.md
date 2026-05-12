# XPUMD Adapter 实现与验证报告（2026-05-06）

## 背景与目标

这次工作的目标不是让 operator 直接消费 Intel XPUMD v2 metrics，而是实现更接近 NVIDIA exporter 的形态：

1. `xpumd` 作为直接接触 Intel 设备的 base collector
2. 我们自己的 `xpu_smi_exporter.py` 作为同 pod sidecar
3. sidecar 抓取 XPUMD `/metrics`，并继续对外暴露既有的兼容 schema
4. operator、dashboard、告警尽量继续沿用原来的 `xpu_device_*` / `xpu_memory_*` 合约

这样做的核心收益是：

- 不再要求 exporter sidecar 挂载宿主机 `xpu-smi` 二进制、动态库、`/tmp/xpum_up.sock`
- 由 Intel 官方 `xpumd` 负责设备侧采集
- 我们只保留 schema 适配与兼容层

---

## 实现思路

### 1. exporter 改成双后端

文件：`deploy/observability/xpu_smi_exporter.py`

原始 exporter 只支持通过本地 `xpu-smi` 子进程采集。现在改成双后端：

- `--source xpu-smi`：保留旧路径
- `--source xpumd`：新路径，直接抓取 XPUMD Prometheus metrics
- `--source auto`：优先尝试 XPUMD，失败时回退到 `xpu-smi`

另外补了启动期重试逻辑，避免同 pod 部署时 exporter 先于 XPUMD 完成启动而直接退出。

### 2. XPUMD -> 兼容 schema 的映射

目前适配的主要映射关系如下：

| XPUMD metric | 对外兼容 metric |
|---|---|
| `hw_gpu_info` | `xpu_device_info` |
| `hw_memory_size_bytes` | `xpu_memory_total_bytes` |
| `hw_memory_usage_bytes` | `xpu_memory_used_bytes` |
| total - used | `xpu_memory_free_bytes` |
| used / total | `xpu_memory_utilization_ratio` |
| `hw_frequency_hertz` | `xpu_frequency_mhz` |
| `hw_power_watts` | `xpu_power_watts` |
| `hw_temperature_celsius` | `xpu_temperature_celsius{location=*}` |
| `hw_gpu_io_bytes_total` | `xpu_pcie_*_bytes_per_second` |
| `hw_memory_io_bytes_total` | `xpu_memory_*_bytes_per_second` |

其中 IO 类指标不是 XPUMD 直接给速率，而是 exporter 在两次采样之间根据 counter 差值自行计算。

### 3. engine util contract 的处理

XPUMD 目前没有提供与老 exporter 完全对齐的 engine-group utilization families，但现有告警和 dashboard 仍依赖：

- `xpu_engine_group_compute_engine_util`
- `xpu_engine_group_render_engine_util`
- `xpu_engine_group_media_engine_util`
- `xpu_engine_group_copy_engine_util`

为避免 XPUMD 模式下直接触发 `absent(...)` 类告警，当前 exporter 在 XPUMD 模式下会为这些指标继续输出每卡的 series，但值为 `NaN`，表示：

- contract 仍存在
- 但当前数据源无法给出真实数值

这比直接输出 `0` 更安全，因为不会伪造“设备空闲”的语义。

### 4. operator 侧发现的共存去重问题

在远端集群验证时，集群里同时存在：

- 旧的 `xpumd-test`
- 新的 `xpumd-adapter-test`

两者都被 operator 视为 Intel metrics exporter pod，结果同一物理节点被重复计数，出现：

- `nodesWithGPUs=2`
- `totalGpus=6`

因此补了一个额外修复：

文件：`deploy/operator/internal/gpu/discovery.go`

- 在 `discoverPrometheusPods()` 中，按 `pod.Spec.NodeName` 去重后再统计 `NodesWithGPUs`

并增加回归测试，确保同一节点上多个 Intel exporter pod 不会重复累计。

### 5. profiler 侧顺手修掉的 XPU KV buffer 问题

文件：`components/src/dynamo/profiler/utils/dgd_generation.py`

之前 `enable_vllm_xpu_runtime()` 只会给 prefill worker 的 `--kv-transfer-config` 注入：

```json
"kv_buffer_device": "xpu"
```

但 decode worker 同样会带这个参数，且 XPU 启动脚本里也需要它，因此这次顺手扩展为：

- 所有带 `--kv-transfer-config` 的 vLLM worker 都改成 `kv_buffer_device=xpu`

---

## 部署形态

为了让这条路径可复现，新增了可直接执行的 sidecar 示例目录：

- `deploy/observability/k8s/xpumd-adapter/apply.sh`
- `deploy/observability/k8s/xpumd-adapter/xpumd-config.yaml`
- `deploy/observability/k8s/xpumd-adapter/pod.yaml`

使用方式：

```bash
bash deploy/observability/k8s/xpumd-adapter/apply.sh
```

其中 `apply.sh` 会自动从仓库里的 `deploy/observability/xpu_smi_exporter.py` 生成：

- `ConfigMap/xpu-smi-exporter-script`

所以不需要手工再创建脚本 ConfigMap；同时 `NAMESPACE=...` 覆盖也会同步作用于 `xpumd-config` 和 pod manifest。

---

## 测试过程

### A. 本地单元测试

1. `python3 -m py_compile deploy/observability/xpu_smi_exporter.py deploy/observability/test_xpu_smi_exporter.py`
2. `python3 -m pytest -q deploy/observability/test_xpu_smi_exporter.py`
3. `cd deploy/operator && go test ./internal/gpu/...`
4. `python3 -m pytest -q components/src/dynamo/profiler/tests/unit/test_dgd_generation_aic.py`

覆盖点包括：

- XPUMD sample metrics 能否生成兼容 schema
- engine util placeholder 是否保留
- operator 是否支持 Intel exporter 多 pod 同节点去重
- profiler 是否对带 `--kv-transfer-config` 的 XPU worker 注入 `kv_buffer_device=xpu`

### B. 远端集群验证

目标集群：

- context: `intel-xpu-tunnel`
- node: `emr816613-vm03`
- namespace: `intel-xpumd`

验证流程：

1. 用 sidecar 方式部署 `xpumd-adapter-test`
2. port-forward `:9966`
3. 抓取 exporter 输出，确认兼容 schema 存在
4. 创建最小 DGDR：`xpumd-adapter-discovery-validate`
5. 查看 operator 日志，确认它抓的是 `:9966/metrics` 而不是 `:8080/metrics`
6. 验证后清理临时 DGDR、test pod、临时 ConfigMap

### C. 远端关键证据

#### sidecar 输出

实际抓到的兼容指标包括：

```text
xpu_device_count{node_name="emr816613-vm03"} 3
xpu_device_info{device_id="0",device_name="Intel(R) Arc(TM) Pro B60 Graphics",pci_device_id="0xe211",...} 1
xpu_memory_total_bytes{device_id="0",node_name="emr816613-vm03"} 25669140480
```

还确认存在：

- `xpu_memory_used_bytes`
- `xpu_memory_free_bytes`
- `xpu_memory_utilization_ratio`
- `xpu_frequency_mhz`
- `xpu_power_watts`
- `xpu_temperature_celsius`
- `xpu_pcie_*_bytes_per_second`
- `xpu_memory_*_bytes_per_second`
- `xpu_engine_group_*_util`（XPUMD 模式下为 `NaN` placeholder）

#### operator 日志

创建 `xpumd-adapter-discovery-validate` 后，日志里明确看到：

```text
Parsed Intel XPU info ... node="emr816613-vm03" gpuCount=3 model="Intel(R) Arc(TM) Pro B60 Graphics" pciDeviceID="0xe211" vramMiB=24480 system="b60"
Scraped GPU metrics exporter pod ... pod="xpumd-adapter-test" endpoint="http://10.233.114.122:9966/metrics" sku="b60"
```

这证明当前链路已经变成：

`xpumd -> exporter sidecar(:9966) -> operator`

而不是：

`xpumd -> operator(:8080)`

---

## 遇到的问题与处理

### 问题 1：XPUMD 适配最初没有保留 engine util contract

**现象**：

- XPUMD 模式下 exporter 没有输出 `xpu_engine_group_compute_engine_util`
- 现有 alert 规则有 `absent(xpu_engine_group_compute_engine_util)`

**处理**：

- exporter 在 XPUMD 模式下显式输出 engine util placeholder series
- 值为 `NaN`，避免伪造 0，又能保住 contract

### 问题 2：同一节点多个 Intel exporter pod 会被重复计数

**现象**：

- 因为集群还留着旧的 `xpumd-test`
- operator 统计 `NodesWithGPUs` 时按 pod 数量算，而不是按节点算

**处理**：

- `discoverPrometheusPods()` 按 `nodeName` 去重
- 增加回归测试

### 问题 3：最初的 sidecar pod 示例不是“一条命令可复现”

**现象**：

- Pod YAML 依赖两个 ConfigMap
- 但最开始只保存了 Pod manifest，本身不可直接运行

**处理**：

- 改成 `deploy/observability/k8s/xpumd-adapter/` 可执行目录
- 由 `apply.sh` 自动生成 `xpu-smi-exporter-script` ConfigMap
- 保留 `xpumd-config.yaml`

### 问题 4：XPU KV buffer 只改了 prefill worker

**现象**：

- profiler 原始修复只作用于 prefill
- decode worker 若自带 `--kv-transfer-config`，仍会保留默认 buffer device

**处理**：

- 把 `kv_buffer_device=xpu` 注入扩展到所有带 `--kv-transfer-config` 的 vLLM worker

---

## 最终变更清单

### 代码

- `deploy/observability/xpu_smi_exporter.py`
- `deploy/observability/test_xpu_smi_exporter.py`
- `deploy/operator/internal/gpu/discovery.go`
- `deploy/operator/internal/gpu/discovery_test.go`
- `components/src/dynamo/profiler/utils/dgd_generation.py`
- `components/src/dynamo/profiler/tests/unit/test_dgd_generation_aic.py`

### 部署样例

- `deploy/observability/k8s/xpumd-adapter/apply.sh`
- `deploy/observability/k8s/xpumd-adapter/xpumd-config.yaml`
- `deploy/observability/k8s/xpumd-adapter/pod.yaml`

### 记录

- `worklogs/xpu-discovery/dgdr-e2e-bringup-2026-04-29.md`
- `worklogs/xpu-discovery/xpu-phase1-test-log-2026-04-27.md`

---

## 复现建议

如果后面你切分支后想快速回到这个状态，建议按下面顺序：

1. checkout 到包含本次提交的分支/commit
2. 运行本地回归：
   - `python3 -m pytest -q deploy/observability/test_xpu_smi_exporter.py`
   - `cd deploy/operator && go test ./internal/gpu/...`
   - `python3 -m pytest -q components/src/dynamo/profiler/tests/unit/test_dgd_generation_aic.py`
3. 连接 Intel 集群：
   - `export KUBECONFIG=/home/qyao/.kube/clusters/intel-xpu-tunnel.yaml`
4. 部署 sidecar 方案：
   - `bash deploy/observability/k8s/xpumd-adapter/apply.sh`
5. 验证 sidecar：
   - `kubectl -n intel-xpumd port-forward pod/xpumd-adapter-test 19966:9966`
   - `curl --noproxy '*' http://127.0.0.1:19966/metrics`
6. 验证 operator discovery：
   - 创建一个空 hardware 的 DGDR
   - 检查 operator 日志中是否抓取 `:9966/metrics`
7. 验证完成后清理临时资源

---

## 总结

这次已经把目标架构从“operator 直连 XPUMD”真正推进到了“XPUMD base pod + compatibility exporter sidecar”：

- 设备采集由 Intel 官方 XPUMD 承担
- exporter 不再依赖宿主机 `xpu-smi` 生态
- 对 Dynamo 来说，发现链路仍然保持 legacy contract
- 远端 Intel 集群已经完成实测闭环

如果后面继续往产品化推进，下一步最值得做的是：

1. 把 sidecar 镜像单独固化，而不是运行通用 `python:3.10-slim`
2. 明确 XPUMD 模式下 engine util 的长期方案（真实指标替代 NaN placeholder）
3. 把这条 sidecar 路径整理成正式 deployment guide，而不是仅保存在 worklog 中
