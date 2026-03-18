# Inference Gateway EPP 路由决策

本文档详细解释 Inference Gateway 的 EPP (Endpoint Picker Plugin) 如何决定路由到哪个 backend pod。

---

## 相关代码位置索引

| 组件 | 文件路径 | 描述 |
|------|---------|------|
| **KV Indexer** | `lib/kv-router/src/indexer/kv_indexer.rs` | KVIndexer 核心实现，管理 Radix Tree |
| **Radix Tree** | `lib/kv-router/src/indexer/radix_tree.rs` | 前缀树数据结构，`find_matches()` 方法 |
| **Worker Selector** | `lib/kv-router/src/scheduling/selector.rs` | `DefaultWorkerSelector`，cost 计算和 softmax 采样 |
| **Router Config** | `lib/kv-router/src/scheduling/config.rs` | `KvRouterConfig` 配置结构体 |
| **KvRouter** | `lib/llm/src/kv_router.rs` | 高层 KvRouter，整合 indexer 和 scheduler |
| **Prefill Router** | `lib/llm/src/kv_router/prefill_router.rs` | Disaggregated 模式的 prefill 路由 |
| **KV Publisher** | `lib/llm/src/kv_router/publisher.rs` | KV event 发布，ZMQ 监听 |
| **EPP Plugin (Go)** | `deploy/inference-gateway/epp/pkg/plugins/dynamo_kv_scorer/plugin.go` | Go FFI 绑定，调用 Rust router |
| **Decode Scorer** | `deploy/inference-gateway/epp/pkg/plugins/disagg/decode_scorer.go` | HTTP header 定义和设置 |
| **NvExt Headers** | `lib/llm/src/protocols/openai/nvext.rs` | Header 常量和 `apply_header_routing_overrides()` |
| **RouterMode** | `lib/runtime/src/pipeline/network/egress/push_router.rs` | `RouterMode` enum (RoundRobin/Random/KV/Direct) |
| **Python Config** | `components/src/dynamo/router/backend_args.py` | Python CLI 参数定义 |

---

## 架构概述

Inference Gateway 使用 **Gateway API Inference Extension (GAIE)** 框架，其中 **EPP (Endpoint Picker Plugin)** 负责智能路由决策。

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    Inference Gateway 架构                                │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Client Request                                                         │
│       │                                                                 │
│       ▼                                                                 │
│  ┌──────────────────────────────────────┐                               │
│  │     Inference Gateway (kGateway)      │                               │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     ▼                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │   EPP (Endpoint Picker Plugin)        │                               │
│  │   ┌────────────────────────────────┐  │                               │
│  │   │     Dynamo KV Router 逻辑      │  │                               │
│  │   │  - Tokenization                │  │                               │
│  │   │  - KV Indexer (Radix Tree)     │  │                               │
│  │   │  - Cost Calculation            │  │                               │
│  │   │  - Worker Selection            │  │                               │
│  │   └────────────────────────────────┘  │                               │
│  └──────────────────┬───────────────────┘                               │
│                     │                                                   │
│                     │ HTTP Headers (x-worker-instance-id)               │
│                     ▼                                                   │
│  ┌──────────────────────────────────────┐                               │
│  │   Backend Pods (Workers with          │                               │
│  │   Frontend Sidecar)                   │                               │
│  │                                       │                               │
│  │   --router-mode direct                │                               │
│  │   (尊重 EPP 的路由决策)                │                               │
│  └──────────────────────────────────────┘                               │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## EPP 路由决策流程

### 步骤 1: 接收请求并 Tokenize

EPP 接收 HTTP 请求后，使用模型的 tokenizer 将 prompt 转换为 tokens：

```
Prompt: "Hello, how are you?"
    ↓
Tokenizer (模型特定)
    ↓
Token IDs: [15496, 11, 703, 389, 345, 30]
```

**注意**：Dynamo EPP 插件使用 **token-aware** KV routing，这是与标准 GAIE EPP 的关键区别。标准 EPP 不进行 tokenization，因此无法进行精确的 KV cache 匹配。

> **代码位置**:
> - Tokenizer 实现: `lib/llm/src/tokenizers/`
> - Tokenizer API: `lib/llm/src/tokenizers.rs`
> - EPP Go FFI 调用: `deploy/inference-gateway/epp/pkg/plugins/dynamo_kv_scorer/plugin.go:401-445` (`CallRoutePrefillRequest`)

### 步骤 2: 查询 KV Indexer (Radix Tree)

EPP 维护一个全局的 **KVIndexer**，基于 Radix Tree (前缀树) 实现。它通过订阅 workers 发布的 KV events 来追踪每个 worker 的缓存状态。

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         Radix Tree 结构                                  │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│                          Root                                          │
│                           │                                            │
│              ┌────────────┼────────────┐                               │
│              │            │            │                               │
│         Block A       Block B      Block F                             │
│         (W1, W2)      (W1)         (W3)                                │
│              │            │                                            │
│         Block C       Block D                                          │
│         (W1)          (W2)                                             │
│              │                                                         │
│         Block E                                                        │
│         (W1)                                                           │
│                                                                         │
│  W = Worker, 每个 node 记录哪些 workers 缓存了该 block                   │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

**KV Events 类型**：
- **KV stored event**: Worker 缓存了新的 block
- **KV removed event**: Worker 移除了 block (eviction)

**find_matches_for_request(tokens)** 返回：
```python
{
    "worker-1": 5,  # worker-1 缓存了 5 个匹配的 blocks
    "worker-2": 3,  # worker-2 缓存了 3 个匹配的 blocks
    "worker-3": 0   # worker-3 没有匹配的 blocks
}
```

> **代码位置**:
> - `lib/kv-router/src/indexer/kv_indexer.rs:366-416` - `KvIndexer::find_matches()` 和 `find_matches_for_request()`
> - `lib/kv-router/src/indexer/radix_tree.rs:156-316` - `RadixTree::find_matches()` 核心遍历逻辑
> - `lib/kv-router/src/indexer/radix_tree.rs:323-464` - `RadixTree::apply_event()` 处理 KV store/remove 事件
> - `lib/llm/src/kv_router/publisher.rs` - KV event 发布和 ZMQ 监听

### 步骤 3: 计算每个 Worker 的 Cost

对于每个候选 worker，计算 routing cost：

**Cost 公式**：
```
cost = overlap_score_weight × prefill_blocks + decode_blocks
```

**其中**：
- **prefill_blocks** = (总 tokens - 缓存命中 tokens) / block_size
- **decode_blocks** = worker 当前活跃的 decode blocks (负载指标)
- **overlap_score_weight**: 平衡缓存优化 vs 负载均衡 (默认 1.0)

**计算示例** (overlap_score_weight = 1.0, block_size = 16):

假设请求有 128 个 tokens：

| Worker | 缓存命中 blocks | 命中 tokens | 需要计算的 tokens | Prefill Blocks | Decode Blocks | Cost |
|--------|----------------|------------|------------------|----------------|---------------|------|
| Worker 1 | 2 blocks | 32 | 96 | 6 | 10 | 1.0×6 + 10 = **16** |
| Worker 2 | 5 blocks | 80 | 48 | 3 | 5 | 1.0×3 + 5 = **8** ✓ (最低) |
| Worker 3 | 8 blocks | 128 | 0 | 0 | 9 | 1.0×0 + 9 = **9** |

> **代码位置**:
> - `lib/kv-router/src/scheduling/selector.rs:95-220` - `DefaultWorkerSelector::select_worker()` 完整实现
> - `lib/kv-router/src/scheduling/selector.rs:121-125` - `overlap_weight` 从配置读取
> - `lib/kv-router/src/scheduling/selector.rs:138-159` - 遍历所有 worker 计算 logit (cost)
> - 公式实现 (line 148): `let logit = overlap_weight * potential_prefill_block + decode_block;`

### 步骤 4: Worker 选择

**温度 = 0 (默认)**：选择 cost 最低的 worker (确定性选择)

**温度 > 0**：使用 softmax 采样，让低分 worker 也有机会 (探索)

```python
# Softmax sampling with temperature
def select_worker(costs, temperature):
    if temperature == 0:
        return argmin(costs)

    # Convert costs to logits (negative because lower cost = better)
    logits = [-c / temperature for c in costs]

    # Softmax
    probs = softmax(logits)

    # Sample
    return random_choice(workers, weights=probs)
```

> **代码位置**:
> - `lib/kv-router/src/scheduling/selector.rs:26-79` - `softmax_sample()` 函数
> - `lib/kv-router/src/scheduling/selector.rs:35-44` - 温度=0 时返回最小 logit 的 worker(s)
> - `lib/kv-router/src/scheduling/selector.rs:53-64` - 温度>0 时 softmax 概率计算
> - `lib/kv-router/src/scheduling/selector.rs:169-186` - ties 时使用 tree_size 作为 tie-breaker
> - `lib/kv-router/src/scheduling/config.rs:28-34` - `KvRouterConfig` 中的 `overlap_score_weight` 和 `router_temperature`

### 步骤 5: 通过 HTTP Headers 传递路由决策

EPP 设置 HTTP headers 来通知选中的 worker：

| Header | 描述 |
|--------|------|
| `x-worker-instance-id` | Primary worker ID (decode worker) |
| `x-prefill-instance-id` | Prefill worker ID (仅 disaggregated 模式) |

> **代码位置**:
> - `lib/llm/src/protocols/openai/nvext.rs:12-13` - Header 常量定义
> - `lib/llm/src/protocols/openai/nvext.rs:23-47` - `apply_header_routing_overrides()` 函数
> - `deploy/inference-gateway/epp/pkg/plugins/disagg/decode_scorer.go:40-41` - Go 端 header 常量

### 步骤 6: Frontend Sidecar 执行路由

每个 worker pod 有一个 **Frontend Sidecar**，配置 `--router-mode direct`：

- Frontend 收到请求后读取 headers
- 直接将请求转发到 header 中指定的 worker
- 不再自己做路由决策

> **代码位置**:
> - `lib/runtime/src/pipeline/network/egress/push_router.rs:92-108` - `RouterMode` enum 定义，包含 `Direct` 变体
> - `lib/runtime/src/pipeline/network/egress/push_router.rs:105-107` - `is_direct_routing()` 方法
> - `lib/runtime/src/pipeline/network/egress/push_router.rs:224-229` - `direct()` 方法实现

---

## Disaggregated Serving 模式下的路由

在 disaggregated 模式下，EPP 需要同时选择 **prefill worker** 和 **decode worker**：

```
┌─────────────────────────────────────────────────────────────────────────┐
│                  Disaggregated Routing 流程                              │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  1. 选择 Prefill Worker                                                 │
│     ┌──────────────────────────────────────────────────────────────┐   │
│     │  优先级:                                                       │   │
│     │  1. KV cache overlap (哪个 worker 缓存了最多的前缀)            │   │
│     │  2. Prefill 负载                                               │   │
│     │  3. Worker 可用性                                              │   │
│     └──────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  2. 选择 Decode Worker                                                  │
│     ┌──────────────────────────────────────────────────────────────┐   │
│     │  优先级:                                                       │   │
│     │  1. Decode 负载 (最少活跃 blocks)                              │   │
│     │  2. Decode capacity                                            │   │
│     │  3. Worker 可用性                                              │   │
│     └──────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  3. 设置 Headers:                                                       │
│     x-prefill-instance-id: <选中的 prefill worker>                      │
│     x-worker-instance-id: <选中的 decode worker>                        │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

**Graceful Degradation**：如果没有 prefill workers 可用，系统会自动降级到 aggregated 模式 (除非设置了 `DYN_ENFORCE_DISAGG=true`)。

> **代码位置**:
> - `lib/llm/src/kv_router/prefill_router.rs:98-118` - `PrefillRouter` 结构体定义
> - `lib/llm/src/kv_router/prefill_router.rs:191-279` - `activate()` 激活 prefill router
> - `lib/llm/src/kv_router/prefill_router.rs:284-359` - `resolve_prefill_worker()` 选择 prefill worker
> - `lib/llm/src/kv_router/prefill_router.rs:369-456` - `execute_prefill()` 执行 prefill 请求
> - `lib/llm/src/kv_router/prefill_router.rs:501-543` - `query_prefill_worker()` 查询最佳 prefill worker
> - `lib/llm/src/kv_router/prefill_router.rs:567-722` - `generate()` 主入口，处理 disaggregated 路由

---

## 关键配置参数

### 环境变量

| 环境变量 | 默认值 | 描述 |
|---------|--------|------|
| `DYN_ROUTER_KV_OVERLAP_SCORE_WEIGHT` / `DYN_OVERLAP_SCORE_WEIGHT` | 1.0 | 缓存命中 vs 负载的权重。高值偏向 TTFT 优化，低值偏向负载均衡 |
| `DYN_ROUTER_TEMPERATURE` | 0.0 | Worker 选择的温度。0=确定性，>0=探索 |
| `DYN_ROUTER_USE_KV_EVENTS` | true | 是否监听 KV events |
| `DYN_ENFORCE_DISAGG` | false | 严格强制 disaggregated 模式，无 prefill workers 时请求失败 |
| `DYN_ROUTER_REPLICA_SYNC` | false | 启用 router replica 同步 |
| `DYN_ROUTER_TRACK_ACTIVE_BLOCKS` | true | 追踪活跃 blocks |
| `DYN_ROUTER_TRACK_OUTPUT_BLOCKS` | false | 追踪输出 blocks |

> **注意**: Python 组件使用 `DYN_ROUTER_KV_OVERLAP_SCORE_WEIGHT`，而 Rust/C 绑定使用 `DYN_OVERLAP_SCORE_WEIGHT`。两者功能相同。

> **代码位置**:
> - `lib/kv-router/src/scheduling/config.rs:26-95` - `KvRouterConfig` 结构体定义和默认值
> - `lib/kv-router/src/scheduling/config.rs:96-117` - `Default` 实现，包含所有默认值
> - `components/src/dynamo/router/backend_args.py:78-95` - Python CLI 参数定义 (overlap_score_weight, temperature)
> - `components/src/dynamo/router/backend_args.py:97-105` - `--router-kv-events` 参数
> - `deploy/inference-gateway/epp/pkg/plugins/dynamo_kv_scorer/plugin.go:122-128` - Go 端从环境变量读取配置

### 配置示例

```yaml
apiVersion: nvidia.com/v1alpha1
kind: DynamoGraphDeployment
metadata:
  name: my-deployment
spec:
  services:
    EPP:
      componentType: epp
      eppConfig:
        config:
          # EPP 配置
      envs:
        - name: DYN_ROUTER_KV_OVERLAP_SCORE_WEIGHT
          value: "1.5"
        - name: DYN_ROUTER_TEMPERATURE
          value: "0.1"
```

---

## 与标准 Dynamo Frontend Router 的区别

| 特性 | Dynamo Frontend Router | EPP (GAIE) |
|------|----------------------|------------|
| **路由位置** | Frontend 进程内 | Gateway 层 |
| **Tokenization** | 在 Frontend 中 | 在 EPP 中 |
| **KV-aware** | ✅ Token-aware | ✅ Token-aware (Dynamo 插件) |
| **标准 GAIE EPP** | N/A | ❌ 不 token-aware (无 tokenizer) |
| **路由传递** | 直接调用 worker | 通过 HTTP headers |
| **部署位置** | 与 worker 同 pod | 独立部署 |

---

## 流程图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         EPP 路由流程时序图                                │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Client          Gateway           EPP            KVI          Worker   │
│    │               │                │               │              │     │
│    │ HTTP Request  │                │               │              │     │
│    │──────────────►│                │               │              │     │
│    │               │ Route Request  │               │              │     │
│    │               │───────────────►│               │              │     │
│    │               │                │ Tokenize      │              │     │
│    │               │                │─────────────┐ │              │     │
│    │               │                │             │ │              │     │
│    │               │                │◄────────────┘ │              │     │
│    │               │                │               │              │     │
│    │               │                │ find_matches  │              │     │
│    │               │                │──────────────►│              │     │
│    │               │                │               │              │     │
│    │               │                │ {W1:5,W2:3}   │              │     │
│    │               │                │◄──────────────│              │     │
│    │               │                │               │              │     │
│    │               │                │ Calculate     │              │     │
│    │               │                │ cost per      │              │     │
│    │               │                │ worker        │              │     │
│    │               │                │               │              │     │
│    │               │                │ Select lowest │              │     │
│    │               │                │ cost worker   │              │     │
│    │               │                │               │              │     │
│    │               │ Response with  │               │              │     │
│    │               │ headers        │               │              │     │
│    │               │◄───────────────│               │              │     │
│    │               │                │               │              │     │
│    │               │ Forward to worker-2             │              │     │
│    │               │ (x-worker-instance-id: worker-2)│              │     │
│    │               │────────────────────────────────────────────────►│     │
│    │               │                │               │              │     │
│    │               │                │               │   Response   │     │
│    │               │◄───────────────────────────────────────────────│     │
│    │               │                │               │              │     │
│    │  Response     │                │               │              │     │
│    │◄──────────────│                │               │              │     │
│    │               │                │               │              │     │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 相关文档

- [Inference Gateway 集成指南](../docs/kubernetes/inference-gateway.md)
- [Router 设计文档](../docs/design-docs/router-design.md)
- [Router 指南](../docs/components/router/router-guide.md)
