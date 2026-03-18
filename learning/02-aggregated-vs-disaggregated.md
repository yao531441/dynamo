# Aggregated Serving vs Disaggregated Serving

Dynamo 支持两种服务模式：Aggregated Serving (聚合服务) 和 Disaggregated Serving (分离服务)。理解两者的区别对于选择正确的部署架构至关重要。

## 概览

| 特性 | Aggregated Serving | Disaggregated Serving |
|------|-------------------|----------------------|
| **架构** | Prefill + Decode 在同一 worker | Prefill 和 Decode 在不同 workers |
| **复杂度** | 简单，易于部署 | 复杂，需要协调 |
| **基础设施** | 不需要 RDMA | **需要 RDMA** (InfiniBand/RoCE) |
| **GPU 利用率** | 固定分配 | 可独立优化 |
| **扩展性** | 整体扩展 | 可独立扩展 prefill/decode |
| **性能** | 均衡场景下较好 | 长输入/短输出场景显著提升 |

---

## Aggregated Serving (聚合服务)

### 定义

Prefill 和 Decode 阶段在**同一个 worker/engine** 中执行。这是传统的服务模式。

### 特点

- **架构简单**：部署容易，运维成本低
- **不需要 RDMA**：无需特殊网络基础设施
- **GPU 资源统一分配**：Prefill 和 Decode 共享相同的 GPU 配置

### 请求流程

```
Client
   ↓ HTTP/OpenAI API
Frontend (接收请求、预处理)
   ↓ Request Plane
Router (选择最优 worker)
   ↓ Request Plane
Worker (vLLM/SGLang/TRT-LLM)
   ├── Prefill (计算 KV cache)
   └── Decode (生成 tokens)
   ↓ Streaming Response
Frontend
   ↓ HTTP Stream
Client
```

### 适用场景

- 简单部署，无 RDMA 基础设施
- 负载均衡场景 (ISL/OSL 比例在 2:1 到 10:1)
- 快速原型开发和测试
- 中小规模部署

---

## Disaggregated Serving (分离服务)

### 定义

Prefill 和 Decode 阶段在**不同的 workers/engines** 中执行。这是 Dynamo 的高性能服务模式。

### 执行流程

1. **Prefill engine** 计算 prefill 阶段并生成 KV cache
2. **Prefill engine** 通过 NIXL 将 KV cache 传输到 decode engine
3. **Decode engine** 计算 decode 阶段

```
Client
   ↓ HTTP/OpenAI API
Frontend (接收请求、预处理)
   ↓ Request Plane
PrefillRouter
   ├── 选择 prefill worker (KV-aware routing)
   ↓ Request Plane
Prefill Worker (计算 KV cache)
   ↓ 返回 disaggregated_params (传输元数据)
PrefillRouter
   ├── 选择 decode worker
   ↓ Request Plane
Decode Worker
   ├── 通过 NIXL 接收 KV cache (GPU-to-GPU)
   └── Decode (生成 tokens)
   ↓ Streaming Response
Frontend
   ↓ HTTP Stream
Client
```

### 特点

- **需要 RDMA**：KV cache 传输需要 InfiniBand 或 RoCE
- **可独立扩展**：Prefill 和 decode workers 可以独立扩缩容
- **硬件优化**：可以为不同阶段配置不同的硬件 (如 decode 用更大的 TP)
- **非阻塞传输**：NIXL 支持 GPU 继续服务其他请求的同时进行传输

### 后端特定的传输元数据

| 后端 | 传输元数据格式 | 特点 |
|------|--------------|------|
| **SGLang** | `bootstrap_info` (host, port, room_id) | Prefill 可作为后台任务，decode 立即开始 |
| **vLLM** | `kv_transfer_params` (block IDs, remote worker info) | Prefill 同步运行，decode 等待 prefill 完成 |
| **TRT-LLM** | `opaque_state` (序列化内部元数据) | Prefill 同步运行 |

### Runtime-Reconfigurable xPyD

Dynamo 支持运行时重新配置 xPyD (x prefill workers, y decode workers)：

- **添加 worker**：Worker 在 discovery service 注册并发布其 RuntimeConfig
- **移除 worker**：Worker 排空活跃请求并从 discovery 注销

Router 自动通过 discovery service 发现新 workers 并将其纳入路由决策。

### 适用场景

- **超长输入** (ISL > 8000) + 短输出
- 需要独立扩展 prefill/decode 容量
- 需要优化特定的 TTFT 或 ITL SLA
- 多节点部署 (可达 2x+ 吞吐提升)

---

## 性能对比

### 单节点 vs 多节点

| 配置 | Aggregated | Disaggregated | 提升 |
|------|-----------|---------------|------|
| 单节点 (Llama 70B) | 基准 | +30% throughput/GPU | 1.3x |
| 双节点 (Llama 70B) | 基准 | +100%+ throughput/GPU | 2x+ |

*测试条件：H100, R1 Distilled Llama 70B FP8, vLLM, 3K ISL / 150 OSL*

### KV-aware Routing 效果

| 指标 | Random Routing | Dynamo KV-aware Routing | 提升 |
|------|---------------|------------------------|------|
| TTFT | 基准 | 3x 更快 | 3x |
| 平均请求延迟 | 基准 | 2x 更快 | 2x |

*测试条件：100K 请求，R1, Llama 70B FP8, 2 节点 H100, 平均 4K ISL / 800 OSL*

---

## 选择指南

### 决策流程图

```
                    开始
                      │
                      ▼
            ┌─────────────────┐
            │ 有 RDMA 基础设施？ │
            └────────┬────────┘
                     │
        ┌────────────┴────────────┐
        │ No                      │ Yes
        ▼                         ▼
┌───────────────┐         ┌───────────────────┐
│ Aggregated    │         │ ISL/OSL 比例？     │
│ (推荐)        │         └─────────┬─────────┘
└───────────────┘                   │
                          ┌─────────┴─────────┐
                          │ 2:1 - 10:1        │ >10:1 或 <2:1
                          ▼                   ▼
                  ┌───────────────┐   ┌───────────────────┐
                  │ Aggregated    │   │ Disaggregated     │
                  │ (通常更好)    │   │ (推荐)            │
                  └───────────────┘   └───────────────────┘
```

### 详细选择表

| 场景 | 推荐架构 | 原因 |
|------|---------|------|
| 无 RDMA 基础设施 | **Aggregated** | Disaggregated RDMA 缺失时性能下降 40x |
| 简单部署/测试 | **Aggregated** | 部署简单，无需特殊网络 |
| ISL/OSL 比例 2:1 - 10:1 | **Aggregated** | 均衡场景下两者性能接近 |
| ISL > 8000 + 短输出 | **Disaggregated** | 长输入场景 disaggregated 优势明显 |
| 需要独立扩展 prefill/decode | **Disaggregated** | 可按需扩展各阶段容量 |
| 多节点部署 | **Disaggregated** | 多节点下 disaggregated 吞吐提升显著 |
| 需要优化特定 SLA (TTFT/ITL) | **Disaggregated** | 可针对不同阶段独立优化 |

---

## 部署示例

### Aggregated 部署

```yaml
apiVersion: nvidia.com/v1alpha1
kind: DynamoGraphDeployment
metadata:
  name: dynamo-agg
spec:
  backendFramework: vllm
  services:
    Frontend:
      componentType: frontend
      replicas: 1
    VLLMWorker:
      componentType: worker
      replicas: 4
      resources:
        limits:
          gpu: "2"
      extraPodSpec:
        mainContainer:
          args:
            - --model
            - "Qwen/Qwen3-32B-FP8"
            - --tensor-parallel-size
            - "2"
```

### Disaggregated 部署

```yaml
apiVersion: nvidia.com/v1alpha1
kind: DynamoGraphDeployment
metadata:
  name: dynamo-disagg
spec:
  backendFramework: vllm
  services:
    Frontend:
      componentType: frontend
      replicas: 1
    VLLMPrefillWorker:
      componentType: worker
      subComponentType: prefill
      replicas: 2
      resources:
        limits:
          gpu: "2"
          rdma/ib: "2"      # RDMA 资源
      extraPodSpec:
        mainContainer:
          securityContext:
            capabilities:
              add: ["IPC_LOCK"]
          args:
            - --model
            - "Qwen/Qwen3-32B-FP8"
            - --tensor-parallel-size
            - "2"
            - --max-num-seqs
            - "1"
            - --disaggregation-mode
            - prefill
    VLLMDecodeWorker:
      componentType: worker
      subComponentType: decode
      replicas: 1
      resources:
        limits:
          gpu: "4"
          rdma/ib: "4"      # RDMA 资源
      extraPodSpec:
        mainContainer:
          securityContext:
            capabilities:
              add: ["IPC_LOCK"]
          args:
            - --model
            - "Qwen/Qwen3-32B-FP8"
            - --tensor-parallel-size
            - "4"
            - --max-num-seqs
            - "1024"
            - --disaggregation-mode
            - decode
```

---

## 相关文档

- [Disaggregated Serving 设计文档](../docs/design-docs/disagg-serving.md)
- [Disaggregated Serving 功能指南](../docs/features/disaggregated-serving/README.md)
- [AIConfigurator 使用指南](../docs/features/disaggregated-serving/README.md)
