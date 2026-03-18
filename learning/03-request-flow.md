# Chat Completion 请求流程追踪

本文档追踪单个 Chat Completion 请求通过 Dynamo 系统的完整流程，详细说明涉及的组件及其交互顺序。

---

## Aggregated Serving 模式

### 流程图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         Aggregated Serving Flow                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Client                                                                 │
│     │                                                                   │
│     │ HTTP/OpenAI API                                                   │
│     ▼                                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │           Frontend                    │                               │
│  │  - 接收请求                           │                               │
│  │  - 预处理 (chat template, tokenize)   │                               │
│  │  - 验证请求                           │                               │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     │ Request Plane (TCP/HTTP/NATS)                     │
│                     ▼                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │            Router                     │      ◄── Event Plane (KV events)│
│  │  - 选择最优 worker                    │                               │
│  │  - KV-aware routing 或负载均衡        │      ◄── Discovery Plane       │
│  └──────────────────┬───────────────────┘        (worker 发现)          │
│                     │                                                   │
│                     │ Request Plane                                     │
│                     ▼                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │            Worker                     │                               │
│  │  (vLLM / SGLang / TRT-LLM)            │                               │
│  │                                       │                               │
│  │  ┌─────────────────────────────────┐  │                               │
│  │  │ Prefill: 计算 KV cache          │  │                               │
│  │  └─────────────────────────────────┘  │                               │
│  │              ↓                        │                               │
│  │  ┌─────────────────────────────────┐  │                               │
│  │  │ Decode: 生成 tokens             │  │                               │
│  │  └─────────────────────────────────┘  │                               │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     │ Streaming Response                                │
│                     ▼                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │           Frontend                    │                               │
│  │  - 后处理 (detokenization)            │                               │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     │ HTTP Stream                                       │
│                     ▼                                                   │
│  Client                                                                 │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 详细步骤

| Step | 操作 | 组件 | 说明 |
|------|------|------|------|
| 1 | **REQUEST** | Client → Frontend | HTTP 客户端发送 API 请求到 Frontend (OpenAI 兼容服务器，端口 8000) |
| 2 | **PREPROCESS** | Frontend | 预处理请求 (应用 chat template, tokenization)，验证请求 |
| 3 | **ROUTE** | Router | Router 使用 KV-aware routing 或负载均衡选择 worker |
| 4 | **PREFILL** | Worker | 执行 prefill 计算，生成 KV cache |
| 5 | **DECODE** | Worker | 使用 KV cache 生成 tokens |
| 6 | **RESPONSE** | Worker → Frontend → Client | 生成的 tokens 流式返回 |

---

## Disaggregated Serving 模式

### 流程图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                       Disaggregated Serving Flow                        │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Client                                                                 │
│     │                                                                   │
│     │ S1: REQUEST (HTTP API Call)                                       │
│     ▼                                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │           Frontend                    │                               │
│  │  S2: PREPROCESS (Tokenize & Validate) │                               │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     │                                                   │
│                     ▼                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │         PrefillRouter                 │                               │
│  │  S3: ROUTE TO PREFILL                 │      ◄── Event Plane         │
│  │     (KV-aware routing 或负载均衡)      │      ◄── Discovery Plane     │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     │ Request Plane                                     │
│                     ▼                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │        Prefill Worker                 │                               │
│  │  S4: PREFILL (Compute KV Cache)       │                               │
│  │  S5: RETURN METADATA                  │                               │
│  │     (disaggregated_params)            │                               │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     │ 返回传输元数据                                     │
│                     ▼                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │         PrefillRouter                 │                               │
│  │  S6: ROUTE TO DECODE                  │                               │
│  │     (注入 prefill 结果到 decode 请求)  │                               │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     │ Request Plane                                     │
│                     ▼                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │        Decode Worker                  │                               │
│  │  S7: KV TRANSFER (NIXL GPU-to-GPU)    │◄──── Prefill Worker KV Cache │
│  │  S8: DECODE (Generate Tokens)         │                               │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     │ S9: RESPONSE (Stream Tokens)                      │
│                     ▼                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │           Frontend                    │                               │
│  │  - 后处理 (detokenization)            │                               │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     │ HTTP Response                                     │
│                     ▼                                                   │
│  Client                                                                 │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 详细步骤 (按阶段)

#### 🔵 阶段 1: Main Request Flow (请求入口)

| Step | 操作 | 组件 | 说明 |
|------|------|------|------|
| **S1** | REQUEST | Client → Frontend | HTTP 客户端发送 API 请求到 Frontend (OpenAI 兼容服务器，端口 8000) |
| **S2** | PREPROCESS | Frontend | 预处理请求 (应用 chat template, tokenization)，验证请求 |
| **S3** | ROUTE TO PREFILL | PrefillRouter | PrefillRouter 使用 KV-aware routing 或负载均衡选择 prefill worker |

#### 🟢 阶段 2: Prefill Flow (Prefill 处理)

| Step | 操作 | 组件 | 说明 |
|------|------|------|------|
| **S4** | PREFILL | Prefill Worker | 执行 prefill 计算，生成 KV cache |
| **S5** | RETURN METADATA | Prefill Worker → Router | 返回 `disaggregated_params` (后端特定的传输元数据) |

#### 🟠 阶段 3: Decode Routing Flow (Decode 路由)

| Step | 操作 | 组件 | 说明 |
|------|------|------|------|
| **S6** | ROUTE TO DECODE | PrefillRouter | 注入 prefill 结果到 decode 请求，路由到 decode worker |
| **S7** | KV TRANSFER | Decode Worker ↔ Prefill Worker | 通过 **NIXL** 直接 GPU-to-GPU KV cache 传输 |

#### 🟣 阶段 4: Completion Flow (完成响应)

| Step | 操作 | 组件 | 说明 |
|------|------|------|------|
| **S8** | DECODE | Decode Worker | 使用传输过来的 KV cache 生成 tokens |
| **S9** | RESPONSE | Decode Worker → Frontend → Client | 生成的 tokens 流式返回，Frontend 进行后处理 |

---

## 涉及的基础设施

### Discovery Plane (服务发现)

```
┌─────────────────────────────────────────────────────────────┐
│                    Discovery Plane                          │
│                                                             │
│  Kubernetes 环境:                                            │
│  - DynamoWorkerMetadata CRD                                 │
│  - EndpointSlices                                           │
│                                                             │
│  Bare Metal 环境:                                            │
│  - etcd (key: /services/{namespace}/{component}/...)        │
│  - Lease-based cleanup (TTL: 10s)                           │
│                                                             │
│  功能:                                                       │
│  - Workers 注册端点                                          │
│  - Frontends/Routers 发现可用 workers                        │
│  - 故障检测和自动清理                                         │
└─────────────────────────────────────────────────────────────┘
```

### Request Plane (请求传输)

```
┌─────────────────────────────────────────────────────────────┐
│                    Request Plane                            │
│                                                             │
│  TCP (默认):                                                 │
│  - 直接 TCP 连接                                             │
│  - 最低延迟                                                  │
│                                                             │
│  HTTP/2:                                                    │
│  - 标准 HTTP 协议                                            │
│  - 易于调试                                                  │
│                                                             │
│  NATS:                                                      │
│  - 消息代理                                                  │
│  - 持久化支持                                                │
│                                                             │
│  功能:                                                       │
│  - Frontend ↔ Router 通信                                   │
│  - Router ↔ Worker 通信                                     │
│  - Worker ↔ Worker 通信 (disaggregated)                     │
└─────────────────────────────────────────────────────────────┘
```

### Event Plane (事件交换)

```
┌─────────────────────────────────────────────────────────────┐
│                    Event Plane                              │
│                                                             │
│  NATS (默认) / ZMQ:                                         │
│  - KV cache 事件 (stored/removed)                           │
│  - Worker 负载指标                                           │
│  - Sequence 追踪事件                                         │
│                                                             │
│  功能:                                                       │
│  - Workers 发布 KV cache 状态                                │
│  - Router 订阅以做 KV-aware routing                         │
│  - Router replica 同步                                       │
└─────────────────────────────────────────────────────────────┘
```

### Planner (自动扩缩容)

```
┌─────────────────────────────────────────────────────────────┐
│                       Planner                               │
│                                                             │
│  连接:                                                       │
│  - Frontend → Planner: 指标收集                             │
│  - Planner → Workers: 资源扩缩容命令                         │
│                                                             │
│  功能:                                                       │
│  - 基于实时需求动态调度 GPU                                   │
│  - 零停机时间调整                                            │
│  - 例如: 长序列请求增加时自动扩展 prefill workers             │
└─────────────────────────────────────────────────────────────┘
```

---

## NIXL KV Cache 传输

在 Disaggregated Serving 中，KV cache 通过 NIXL (NVIDIA Inference tranXfer Library) 传输：

### 传输过程

```
┌──────────────────────┐                      ┌──────────────────────┐
│   Prefill Worker     │                      │    Decode Worker     │
│                      │                      │                      │
│  ┌────────────────┐  │     NIXL            │  ┌────────────────┐  │
│  │  GPU VRAM      │  │  ──────────────────►│  │  GPU VRAM      │  │
│  │  KV Cache      │  │   NVLink/IB/RoCE    │  │  KV Cache      │  │
│  └────────────────┘  │                      │  └────────────────┘  │
│                      │                      │                      │
│  传输元数据:          │                      │                      │
│  - SGLang: bootstrap │                      │                      │
│  - vLLM: block IDs   │                      │                      │
│  - TRT-LLM: opaque   │                      │                      │
└──────────────────────┘                      └──────────────────────┘
```

### NIXL 特点

- **非阻塞传输**：GPU 可以在传输期间继续服务其他请求
- **多传输支持**：NVLink, InfiniBand/UCX, PCIe
- **统一 API**：抽象异构内存 (remote memory, storage)
- **智能传输选择**：自动选择最佳传输机制

---

## 相关文档

- [架构流程设计文档](../docs/design-docs/dynamo-flow.md)
- [整体架构](../docs/design-docs/architecture.md)
- [Disaggregated Serving 设计](../docs/design-docs/disagg-serving.md)
- [Router 设计](../docs/design-docs/router-design.md)
