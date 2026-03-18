# Dynamo 架构的三个平面 (Three Planes)

Dynamo 架构中有三个独立的通信平面，每个平面负责不同的功能，使用不同的传输协议。

## 概览

| Plane (平面) | 功能 | 传输协议 |
|-------------|------|---------|
| **Discovery Plane** (发现平面) | 服务发现与协调 | etcd 或 Kubernetes CRDs |
| **Request Plane** (请求平面) | 组件间的 RPC 请求通信 | TCP / HTTP / NATS |
| **Event Plane** (事件平面) | Pub/Sub 事件交换 | NATS 或 ZMQ |

这三个平面是**独立的**，可以混合配置。例如：使用 TCP 作为请求平面，同时使用 NATS 作为事件平面。

---

## 1. Discovery Plane (发现平面)

### 功能

服务发现与协调，让组件在运行时相互发现。Workers 注册其端点，Frontends 自动发现它们。

### 传输协议

| 部署环境 | 后端 | 配置 |
|----------|------|------|
| **Kubernetes** (with Dynamo operator) | Native K8s (CRDs, EndpointSlices) | 自动设置 `DYN_DISCOVERY_BACKEND=kubernetes` |
| **Bare metal / Local** (默认) | etcd | `ETCD_ENDPOINTS` (默认 `http://localhost:2379`) |
| **Local Development** (无需外部依赖) | file | `--discovery-backend file` (前端和 workers 需共享磁盘) |

### Kubernetes Discovery

当在 Kubernetes 上运行时，使用原生 K8s 资源：

1. Workers 通过创建 **DynamoWorkerMetadata** CR 注册端点
2. **EndpointSlices** 向系统发出 pod 就绪信号
3. 组件通过 watch CRD 变化来发现可用的 workers

**优点**：
- 不需要外部 etcd 集群
- 与 Kubernetes pod 生命周期原生集成
- Pod 终止时自动清理
- 支持标准 Kubernetes RBAC

### etcd Discovery (默认)

当 `DYN_DISCOVERY_BACKEND` 未设置或设置为 `etcd` 时使用。

**配置**：

| 环境变量 | 描述 | 默认值 |
|----------|------|--------|
| `ETCD_ENDPOINTS` | etcd URL 列表 (逗号分隔) | `http://localhost:2379` |
| `ETCD_AUTH_USERNAME` | Basic auth 用户名 | None |
| `ETCD_AUTH_PASSWORD` | Basic auth 密码 | None |
| `ETCD_AUTH_CA` | CA 证书路径 (TLS) | None |
| `ETCD_AUTH_CLIENT_CERT` | 客户端证书路径 | None |
| `ETCD_AUTH_CLIENT_KEY` | 客户端密钥路径 | None |

**服务注册**：

Workers 在 etcd 中注册端点，使用以下 key 层级结构：
```
/services/{namespace}/{component}/{endpoint}/{instance_id}
```

例如：
```
/services/vllm-agg/backend/generate/694d98147d54be25
```

**Lease-Based 清理**：

每个 runtime 维护一个 lease (默认 TTL: 10秒)。如果 worker 崩溃或失去连接：
1. Keep-alive 心跳停止
2. Lease 在 TTL 后过期
3. 所有注册的端点自动删除
4. 客户端收到删除事件并重新路由流量到健康的 workers

---

## 2. Request Plane (请求平面)

### 功能

处理组件间的 RPC 请求通信 (frontend → router → worker)。

### 传输协议

| 请求平面 | 适用场景 | 特点 |
|---------|---------|------|
| **TCP** (默认) | 低延迟直接通信 | 直接连接，最小开销 |
| **HTTP** | 标准部署，调试 | HTTP/2 协议，易于观测，广泛兼容 |
| **NATS** | 生产部署 + KV routing | 需要 NATS 基础设施，提供 pub/sub 模式 |

### 配置

通过 `DYN_REQUEST_PLANE` 环境变量设置：

```bash
export DYN_REQUEST_PLANE=<mode>
```

其中 `<mode>` 可以是：`tcp` (默认)、`nats`、`http`

### TCP (默认)

```bash
# TCP 是默认值，无需显式设置
export DYN_REQUEST_PLANE=tcp

# 可选：配置 TCP 服务器主机和端口
export DYN_TCP_RPC_HOST=0.0.0.0  # 默认主机
export DYN_TCP_RPC_PORT=9999     # 可选：指定固定端口
```

**适用场景**：
- 简单部署，直接服务间通信
- 最小基础设施要求
- 低延迟需求

### HTTP/2

```bash
export DYN_HTTP_RPC_HOST=0.0.0.0      # 默认主机
export DYN_HTTP_RPC_PORT=8888         # 默认端口
export DYN_HTTP_RPC_ROOT_PATH=/v1/rpc # 默认路径

export DYN_REQUEST_PLANE=http
```

**适用场景**：
- 标准 HTTP 兼容性部署
- 调试场景 (使用 curl, 浏览器工具)
- 与 HTTP 基础设施集成
- 负载均衡器和代理

### NATS

```bash
export DYN_REQUEST_PLANE=nats
export NATS_SERVER=nats://nats-server:4222
```

**适用场景**：
- 生产部署和服务发现
- KV-aware routing (需要 NATS 进行事件传输)
- 需要消息重放和持久化功能

**限制**：
- NATS 不支持超过 16MB 的 payload (大 payload 请使用 TCP)

---

## 3. Event Plane (事件平面)

### 功能

Pub/Sub 层，用于近实时事件交换。包括 KV cache 更新、worker 负载指标、sequence 追踪事件等。

### 传输协议

| 传输 | 外部基础设施 | 设置复杂度 | 最佳场景 |
|------|------------|-----------|---------|
| **NATS** (默认) | 需要 NATS 服务器 | 简单 - 指向 NATS 服务器 | 大规模部署 |
| **ZMQ** | 无 (peer-to-peer) | 自动 - workers 绑定 socket 并通过 discovery 注册 | 低运维开销 |

### 配置

```bash
# 使用 NATS (默认 - 无需显式设置)
export DYN_EVENT_PLANE=nats

# 使用 ZMQ
export DYN_EVENT_PLANE=zmq
```

### NATS Transport

- 需要运行中的 NATS 服务器
- Events 发布到按 namespace 和 component 分类的 NATS subjects
- 内置重连和短暂断连期间的消息缓冲

```bash
export NATS_SERVER=nats://nats-server:4222
export DYN_EVENT_PLANE=nats

# Start workers
python3 -m dynamo.vllm --model Qwen/Qwen3-0.6B \
    --kv-events-config '{"publisher":"nats","topic":"kv-events","enable_kv_cache_events":true}'
```

### ZMQ Transport

- 无需外部服务器
- 每个 worker 绑定一个 ZMQ PUB socket 并在 discovery 系统中注册其地址
- 订阅者自动发现并连接到所有活跃的发布者
- 当发布者增减时，订阅者动态调整连接

```bash
export DYN_EVENT_PLANE=zmq

python3 -m dynamo.vllm --model Qwen/Qwen3-0.6B \
  --kv-events-config '{"publisher":"zmq","endpoint":"tcp://*:20080","enable_kv_cache_events":true}'
```

### 禁用 Event Plane

如果不需要 KV-aware routing，可以完全禁用事件平面：

```bash
python3 -m dynamo.frontend --router-mode kv --no-router-kv-events
```

禁用后：
- Router 回退到基于预测的 cache-aware routing
- 不需要 NATS 服务器或 ZMQ sockets
- TTL 过期和 LRU 清理保持预测状态不过期

---

## 相关文档

- [Discovery Plane 设计文档](../docs/design-docs/discovery-plane.md)
- [Request Plane 设计文档](../docs/design-docs/request-plane.md)
- [Event Plane 设计文档](../docs/design-docs/event-plane.md)
- [分布式运行时架构](../docs/design-docs/distributed-runtime.md)
