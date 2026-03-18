# LeaderWorkerSet (LWS) 详解

本文档解释什么是 LeaderWorkerSet，以及为什么 Dynamo 需要它来支持多节点推理。

---

## 什么是 LeaderWorkerSet?

**LeaderWorkerSet (LWS)** 是一个 Kubernetes 原生的 API，用于管理需要 **leader-worker 拓扑结构** 的分布式工作负载。它是 Kubernetes SIGs 的一个项目，专门解决多节点协同工作的场景。

### 架构图

```
┌─ LeaderWorkerSet ─────────────────────────────────────────────────────┐
│                                                                        │
│  ┌─ Leader Pod ─────────────────────────────────────────────────────┐  │
│  │                                                                  │  │
│  │  - 运行主进程 (如 Ray head, MPI orchestrator)                     │  │
│  │  - 提供协调服务                                                    │  │
│  │  - 稳定的网络身份 (Service)                                        │  │
│  │  - 健康检查活跃                                                    │  │
│  │                                                                  │  │
│  └──────────────────────────────────────────────────────────────────┘  │
│                                                                        │
│  ┌─ Worker Pod 1 ────────┐  ┌─ Worker Pod 2 ────────┐                 │
│  │                       │  │                       │                 │
│  │  - 加入集群            │  │  - 加入集群            │                 │
│  │  - 执行计算任务         │  │  - 执行计算任务         │                 │
│  │  - 无健康检查 (通常)    │  │  - 无健康检查          │                 │
│  │                       │  │                       │                 │
│  └───────────────────────┘  └───────────────────────┘                 │
│                                                                        │
│  启动顺序: Leader 必须先就绪，然后 Workers 才启动                        │
│                                                                        │
└────────────────────────────────────────────────────────────────────────┘
```

---

## LWS 解决的问题

| 问题 | 传统 Deployment 的局限 | LWS 的解决方案 |
|------|----------------------|---------------|
| **角色区分** | 所有 Pod 相同，无法区分 leader/worker | 支持 Leader 和 Worker 不同配置 |
| **启动顺序** | 无法保证 leader 先于 worker 启动 | 保证 leader 就绪后才启动 workers |
| **网络身份** | Pod IP 动态变化，workers 难以发现 leader | Leader 有稳定的 Service 地址 |
| **规模调整** | 需要手动协调所有节点 | 统一管理整个 worker group |
| **Gang Scheduling** | 不支持 | 配合 Volcano 实现原子调度 |

### 详细说明

#### 1. 角色区分

```yaml
# LWS 支持不同的 Pod 配置
spec:
  leaderTemplate:
    spec:
      containers:
        - name: leader
          resources:
            limits:
              gpu: "8"
  workerTemplate:
    spec:
      containers:
        - name: worker
          resources:
            limits:
              gpu: "4"
```

#### 2. 启动顺序保证

```
时间线:
────────────────────────────────────────────────────────────────────►

Leader Pod:
  ├─ Pending ──► Running ──► Ready ──────────────────────────────────►
                                        │
                                        │ Leader 就绪
                                        ▼
Worker Pod 1:
              ├─ Pending ──► Running ──► Ready ────────────────────────►
                                        │
                                        ▼
Worker Pod 2:
                            ├─ Pending ──► Running ──► Ready ──────────►
```

#### 3. 稳定的网络身份

```yaml
# Leader 有稳定的 Service
apiVersion: v1
kind: Service
metadata:
  name: my-lws-leader
spec:
  selector:
    leaderworkerset.sigs.k8s.io/name: my-lws
    role: leader
  ports:
    - port: 6379
```

Workers 可以通过 `my-lws-leader.namespace.svc.cluster.local:6379` 连接到 leader。

---

## 为什么 Dynamo 需要 LWS?

### 分布式推理需要协同节点

Dynamo 的 **多节点推理** 场景天然符合 Leader-Worker 拓扑：

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    多节点 Tensor Parallel 示例                            │
│                                                                         │
│                         Llama-70B (TP=8)                                │
│                                                                         │
│  Node 1 (Leader)                     Node 2 (Worker)                    │
│  ┌─────────────────────────────┐     ┌─────────────────────────────┐   │
│  │                             │     │                             │   │
│  │  GPU 0,1,2,3               │     │  GPU 4,5,6,7               │   │
│  │                             │     │                             │   │
│  │  Ray Head                  │◄───►│  Ray Worker                 │   │
│  │  vLLM Main Process         │     │  Ray Agent (block)          │   │
│  │                             │     │                             │   │
│  │  Health Probes: ✓ ON       │     │  Health Probes: ✗ OFF       │   │
│  │                             │     │                             │   │
│  └─────────────────────────────┘     └─────────────────────────────┘   │
│                                                                         │
│  通信: NVLink / InfiniBand / RoCE                                       │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 不同后端的具体需求

| 后端 | Leader 角色 | Worker 角色 | 协调机制 |
|------|-----------|------------|---------|
| **vLLM (Ray)** | Ray head + vLLM main process | Ray agent (blocks) | Ray placement group |
| **vLLM (MP)** | 主进程 (`--node-rank 0`) | 从进程 (`--node-rank N`) | `--distributed-executor-backend mp` |
| **SGLang** | 主进程 (`--node-rank 0`) | 从进程 (`--node-rank N`) | `--dist-init-addr` |
| **TensorRT-LLM** | MPI orchestrator (`mpirun`) | SSH daemon | MPI + SSH |

> **注意**: vLLM 支持两种分布式执行后端：
> - **Ray** (默认): 使用 Ray 进行分布式协调，Leader 启动 Ray head，Worker 启动 Ray agent
> - **MP** (Multiprocessing): 使用内置的 multiprocessing 后端，通过 `--distributed-executor-backend mp` 启用，注入 `--nnodes`, `--node-rank`, `--master-addr` 参数

---

## Dynamo Operator 的自动配置

Dynamo Operator 会自动为 LWS 部署注入正确的配置。

### 整体架构流程

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                        Dynamo 多节点部署流程                                          │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  用户提交 DynamoGraphDeployment CRD                                                  │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ apiVersion: nvidia.com/v1alpha1                                              │   │
│  │ kind: DynamoGraphDeployment                                                  │   │
│  │ spec:                                                                        │   │
│  │   services:                                                                  │   │
│  │     VllmWorker:                                                              │   │
│  │       multinode:                                                             │   │
│  │         nodeCount: 2        ◄── 触发多节点检测                               │   │
│  │       resources:                                                             │   │
│  │         limits:                                                              │   │
│  │           gpu: "4"                                                           │   │
│  │       extraPodSpec:                                                          │   │
│  │         mainContainer:                                                       │   │
│  │           args: [--model, "Qwen/Qwen3-32B", --tensor-parallel-size, "8"]     │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                      │                                              │
│                                      ▼                                              │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ DynamoGraphDeploymentReconciler (控制器)                                      │   │
│  │ 文件: deploy/operator/internal/controller/dynamographdeployment_controller.go│   │
│  │                                                                              │   │
│  │ 1. 检测多节点: HasAnyMultinodeService()                                       │   │
│  │    → 检查 spec.services[*].multinode.nodeCount > 1                          │   │
│  │                                                                              │   │
│  │ 2. Orchestrator 选择:                                                        │   │
│  │    ┌──────────────────┐                                                      │   │
│  │    │ isGrovePathway()? │──Yes──► reconcileGroveResources()                  │   │
│  │    └────────┬─────────┘                                                      │   │
│  │             │ No                                                             │   │
│  │             ▼                                                                │   │
│  │    reconcileDynamoComponentsDeployments()                                     │   │
│  │    → 为每个 service 创建 DynamoComponentDeployment CRD                        │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                      │                                              │
│                                      ▼                                              │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ DynamoComponentDeploymentReconciler (控制器)                                  │   │
│  │ 文件: deploy/operator/internal/controller/dynamocomponentdeployment_controller.go│
│  │                                                                              │   │
│  │ 3. 检测部署类型:                                                              │   │
│  │    ┌──────────────────────────────────────────┐                              │   │
│  │    │ LWSEnabled && IsMultinode()?             │                              │   │
│  │    │ → nodeCount > 1                          │                              │   │
│  │    └──────────────┬───────────────────────────┘                              │   │
│  │                   │                                                          │   │
│  │         ┌────────┴────────┐                                                  │   │
│  │         │ Yes             │ No                                               │   │
│  │         ▼                 ▼                                                  │   │
│  │  reconcileLeaderWorkerSetResources()   reconcileDeploymentResources()        │   │
│  │  → 创建 LWS CRD                        → 创建 Deployment CRD                 │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                      │                                              │
│                                      ▼                                              │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ generateLeaderWorkerSet() - 生成 LWS 资源                                     │   │
│  │                                                                              │   │
│  │ 4. 分别生成 Leader 和 Worker Pod 模板:                                        │   │
│  │    generateLeaderPodTemplateSpec()  ──► RoleLeader                           │   │
│  │    generateWorkerPodTemplateSpec()  ──► RoleWorker                           │   │
│  │                                                                              │   │
│  │ 5. 调用 GenerateBasePodSpecForController() 注入后端配置                        │   │
│  │    → 检测后端框架 (vllm/sglang/trtllm)                                        │   │
│  │    → 调用对应 backend.UpdateContainer() 注入参数                              │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                      │                                              │
│                                      ▼                                              │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │ 最终创建的 Kubernetes 资源                                                    │   │
│  │                                                                              │   │
│  │ LeaderWorkerSet CRD (每个 DCD replica 一个)                                   │   │
│  │ ├── leaderTemplate: Pod (运行主进程)                                          │   │
│  │ └── workerTemplate: Pod×(nodeCount-1) (运行 worker 进程)                      │   │
│  │                                                                              │   │
│  │ Volcano PodGroup (Gang Scheduling)                                           │   │
│  │ └── 确保所有 Pod 同时启动                                                     │   │
│  │                                                                              │   │
│  │ Service (为 Leader 提供稳定网络身份)                                           │   │
│  └── <lws-name>.<namespace>.svc.cluster.local                                   │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### 关键代码路径

| 步骤 | 文件 | 函数 | 说明 |
|------|------|------|------|
| 1. 检测多节点 | `dynamographdeployment_controller.go:323` | `HasAnyMultinodeService()` | 遍历 services 检查 `nodeCount > 1` |
| 2. Orchestrator 选择 | `dynamographdeployment_controller.go:343-348` | `isGrovePathway()` | Grove 可用且未禁用 → Grove，否则 → DCD |
| 3. 创建 DCD | `dynamographdeployment_controller.go:348` | `reconcileDynamoComponentsDeployments()` | 为每个 service 创建 DynamoComponentDeployment |
| 4. 检测 LWS 需求 | `dynamocomponentdeployment_controller.go:186` | `IsMultinode()` | 检查 `nodeCount > 1` |
| 5. 创建 LWS | `dynamocomponentdeployment_controller.go:187` | `reconcileLeaderWorkerSetResources()` | 创建 LeaderWorkerSet + Volcano PodGroup |
| 6. 生成 Pod 模板 | `dynamocomponentdeployment_controller.go:674,683` | `generateLeaderPodTemplateSpec()` / `generateWorkerPodTemplateSpec()` | 分别生成 Leader/Worker 模板 |
| 7. 注入后端配置 | `dynamocomponentdeployment_controller.go:1052` | `GenerateBasePodSpecForController()` | 调用后端注入逻辑 |

### 回答你的问题

#### Q: 是哪个部件检查到了 multinode 配置？

**A: 两层检测：**

1. **DynamoGraphDeploymentReconciler** 检测 DGD 是否有任何多节点服务:
   ```go
   // dynamographdeployment_controller.go:323
   hasMultinode := dynamoDeployment.HasAnyMultinodeService()

   // dynamographdeployment_types.go:314-318
   func (s *DynamoGraphDeployment) HasAnyMultinodeService() bool {
       for _, svc := range s.Spec.Services {
           if svc != nil && svc.GetNumberOfNodes() > 1 {
               return true
           }
       }
       return false
   }
   ```

2. **DynamoComponentDeploymentReconciler** 检测单个 DCD 是否需要多节点:
   ```go
   // dynamocomponentdeployment_controller.go:186
   if r.RuntimeConfig.LWSEnabled && dynamoComponentDeployment.IsMultinode() {
       componentReconcileResult, err = r.reconcileLeaderWorkerSetResources(ctx, dynamoComponentDeployment)
   }
   ```

#### Q: 发现需要 LWS 介入后，分别启动后端的流程？

**A: 不是"分别启动"，而是"同时创建 Leader + Worker Pod 模板"：**

```go
// dynamocomponentdeployment_controller.go:674-686
leaderPodTemplateSpec, err := r.generateLeaderPodTemplateSpec(ctx, opt, kubeName, leaderPodLabels, instanceID)
workerPodTemplateSpec, err := r.generateWorkerPodTemplateSpec(ctx, opt, kubeName, workerPodLabels, instanceID)

// 然后组装成 LeaderWorkerSet
leaderWorkerSet.Spec = leaderworkersetv1.LeaderWorkerSetSpec{
    LeaderWorkerTemplate: leaderworkersetv1.LeaderWorkerTemplate{
        LeaderTemplate: leaderPodTemplateSpec,   // Leader Pod 模板
        WorkerTemplate: *workerPodTemplateSpec,  // Worker Pod 模板
        Size:           &groupSize,              // nodeCount
    },
}
```

**关键点**: Leader 和 Worker 的区分是通过 `role` 参数传递给 `GenerateBasePodSpecForController()`，然后后端代码根据 role 注入不同的参数：

```go
// dynamocomponentdeployment_controller.go:1052
podSpec, err := dynamo.GenerateBasePodSpecForController(
    opt.dynamoComponentDeployment,
    r.DockerSecretRetriever,
    r.Config,
    role,  // <-- RoleLeader 或 RoleWorker
    commonconsts.MultinodeDeploymentTypeLWS,
    checkpointInfo,
)
```

后端代码根据 role 决定注入什么参数（参考 `backend_vllm.go`）：
- `RoleLeader`: 注入 `ray start --head` 或 `--node-rank 0`
- `RoleWorker`: 注入 `ray start --address=... --block` 或 `--node-rank <rank> --headless`

#### Q: 是否代表会生成新的 Deployment？

**A: 不会生成 Deployment，而是生成 LeaderWorkerSet CRD：**

| 场景 | 生成的资源 | 说明 |
|------|-----------|------|
| **单节点** | `Deployment` | 标准 K8s Deployment |
| **多节点 (LWS 模式)** | `LeaderWorkerSet` + `Volcano PodGroup` | LWS CRD 管理 Leader + Worker Pods |

**资源对比：**

```
单节点部署:
┌──────────────────┐
│ Deployment       │
│ └── Pod × N      │  (所有 Pod 相同)
└──────────────────┘

多节点部署 (LWS):
┌──────────────────────────────────┐
│ LeaderWorkerSet                  │
│ ├── Leader Pod × 1               │  (运行主进程)
│ ├── Worker Pod × (nodeCount-1)   │  (运行从进程)
│ └── Service (Leader 网络身份)     │
├──────────────────────────────────┤
│ Volcano PodGroup                 │  (Gang Scheduling)
└──────────────────────────────────┘
```

**特别注意**: 如果 DCD 的 `replicas > 1`，会创建多个 LeaderWorkerSet（每个 LWS 有 1 个 Leader + N 个 Worker）：

```go
// dynamocomponentdeployment_controller.go:311-336
for i := range int(desiredReplicas) {
    // 每个 replica 创建一个 LWS
    lwsObj, err := r.generateLeaderWorkerSet(ctx, generateResourceOption{
        dynamoComponentDeployment: dynamoComponentDeployment,
        instanceID:                &i,  // 第 i 个 LWS
    })
    leaderWorkerSets = append(leaderWorkerSets, lwsObj)
}
```

这意味着：如果 `replicas: 2` 且 `nodeCount: 3`，会创建：
- 2 个 LeaderWorkerSet
- 每个 LWS 有 1 Leader + 2 Worker = 3 Pod
- 总共 6 个 Pod

### vLLM 多节点

**用户配置**:
```yaml
services:
  VllmWorker:
    multinode:
      nodeCount: 2
    resources:
      limits:
        gpu: "4"
    extraPodSpec:
      mainContainer:
        args:
          - --model
          - "Qwen/Qwen3-32B"
          - --tensor-parallel-size
          - "8"
```

**Operator 自动注入 (默认使用 Ray 后端)**:

```
Leader Pod:
  ┌────────────────────────────────────────────────────────────────┐
  │ command:                                                       │
  │   ray start --head --port=6379 &&                              │
  │   python -m dynamo.vllm                                        │
  │     --distributed-executor-backend ray                         │
  │     --model Qwen/Qwen3-32B                                     │
  │     --tensor-parallel-size 8                                   │
  │                                                                │
  │ probes: liveness ✓, readiness ✓, startup ✓                    │
  └────────────────────────────────────────────────────────────────┘

Worker Pod:
  ┌────────────────────────────────────────────────────────────────┐
  │ command:                                                       │
  │   ray start --address=<leader-hostname>:6379 --block           │
  │                                                                │
  │ probes: 全部移除 (worker 只运行 ray agent)                      │
  └────────────────────────────────────────────────────────────────┘
```

**使用 MP 后端** (通过 annotation `nvidia.com/vllm-distributed-executor-backend: "mp"`):

```
Leader Pod:
  ┌────────────────────────────────────────────────────────────────┐
  │ 注入 flags:                                                    │
  │   --distributed-executor-backend mp                            │
  │   --nnodes 2                                                   │
  │   --master-addr <leader-hostname>                              │
  │   --master-port 29500                                          │
  │   --node-rank 0                                                │
  │                                                                │
  │ probes: 全部活跃                                               │
  └────────────────────────────────────────────────────────────────┘

Worker Pod:
  ┌────────────────────────────────────────────────────────────────┐
  │ 注入 flags:                                                    │
  │   --distributed-executor-backend mp                            │
  │   --nnodes 2                                                   │
  │   --master-addr <leader-hostname>                              │
  │   --master-port 29500                                          │
  │   --node-rank <dynamic-rank>                                   │
  │   --headless                                                   │
  │                                                                │
  │ probes: 全部移除                                               │
  └────────────────────────────────────────────────────────────────┘
```

### SGLang 多节点

**Operator 自动注入**:

```
Leader Pod:
  ┌────────────────────────────────────────────────────────────────┐
  │ 注入 flags:                                                    │
  │   --dist-init-addr <leader-hostname>:29500                     │
  │   --nnodes 2                                                   │
  │   --node-rank 0                                                │
  │                                                                │
  │ probes: 全部活跃                                               │
  └────────────────────────────────────────────────────────────────┘

Worker Pod:
  ┌────────────────────────────────────────────────────────────────┐
  │ 注入 flags:                                                    │
  │   --dist-init-addr <leader-hostname>:29500                     │
  │   --nnodes 2                                                   │
  │   --node-rank <dynamic-rank>  # 从 pod stateful identity 计算  │
  │                                                                │
  │ probes: 全部移除                                               │
  └────────────────────────────────────────────────────────────────┘
```

### TensorRT-LLM 多节点

**Operator 自动注入**:

```
Leader Pod:
  ┌────────────────────────────────────────────────────────────────┐
  │ 配置:                                                          │
  │   - SSH keys 自动配置                                          │
  │   - 命令包装在 mpirun 中:                                       │
  │     mpirun -host <worker1>,<worker2> ... <original-command>    │
  │   - SSH port: 2222                                             │
  │                                                                │
  │ probes: 全部活跃                                               │
  └────────────────────────────────────────────────────────────────┘

Worker Pod:
  ┌────────────────────────────────────────────────────────────────┐
  │ 配置:                                                          │
  │   - 命令替换为 SSH daemon setup                                 │
  │   - 生成 host keys                                             │
  │   - 配置 authorized_keys for leader access                     │
  │                                                                │
  │ probes:                                                        │
  │   - liveness: 移除                                             │
  │   - startup: 移除                                              │
  │   - readiness: TCP socket check on SSH port 2222               │
  └────────────────────────────────────────────────────────────────┘
```

---

## LWS vs Grove vs Deployment

Dynamo 支持三种多节点编排方式：

| 特性 | LWS + Volcano | Grove + KAI Scheduler | Deployment |
|------|--------------|----------------------|------------|
| **适用场景** | 简单多节点部署 | 生产级 AI 工作负载 | 单节点部署 |
| **拓扑感知** | ❌ | ✅ 网络拓扑感知调度 | N/A |
| **Gang Scheduling** | ✅ (通过 Volcano) | ✅ (通过 KAI) | ❌ |
| **自动扩缩容** | 有限 | ✅ 多级 HPA | ✅ 标准 HPA |
| **启动顺序** | ✅ Leader 优先 | ✅ 可自定义 | N/A |
| **复杂度** | 低 | 高 | 最低 |
| **网络拓扑优化** | ❌ | ✅ | N/A |
| **资源感知滚动更新** | ❌ | ✅ | 标准 |

---

## Orchestrator 选择逻辑

```
┌─────────────────────────────────────────────────────────────────────────┐
│               多节点部署 Orchestrator 选择逻辑                            │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│                    开始                                                  │
│                     │                                                   │
│                     ▼                                                   │
│         ┌───────────────────────┐                                       │
│         │ Grove + KAI 可用？     │                                       │
│         └───────────┬───────────┘                                       │
│                     │                                                   │
│         ┌───────────┴───────────┐                                       │
│         │ Yes                   │ No                                    │
│         ▼                       ▼                                       │
│  ┌──────────────────┐    ┌────────────────────┐                         │
│  │ 使用 Grove        │    │ LWS + Volcano 可用？│                         │
│  │ (默认，推荐)      │    └──────────┬─────────┘                         │
│  │                  │               │                                   │
│  │ - 拓扑感知调度    │    ┌──────────┴─────────┐                         │
│  │ - 多级自动扩缩容  │    │ Yes                │ No                      │
│  │ - 高级队列管理    │    ▼                    ▼                         │
│  │                  │    │ 使用 LWS           │ 报错:                    │
│  └──────────────────┘    │                    │ 需要安装编排组件          │
│                          │ - 基本 gang        │                          │
│                          │   scheduling       │                          │
│                          │ - Leader-worker    │                          │
│                          │   拓扑             │                          │
│                          └────────────────────┘                          │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 强制使用 LWS

如果 Grove 可用但想使用 LWS：

```yaml
apiVersion: nvidia.com/v1alpha1
kind: DynamoGraphDeployment
metadata:
  name: my-multinode-deployment
  annotations:
    nvidia.com/enable-grove: "false"
spec:
  # ... deployment spec
```

---

## 安装要求

### 使用 LWS 需要安装

**1. LWS CRD**:
```bash
# 参考: https://github.com/kubernetes-sigs/lws#installation
kubectl apply -f https://github.com/kubernetes-sigs/lws/releases/latest/download/install.yaml
```

**2. Volcano Scheduler** (Gang Scheduling 依赖):
```bash
# 参考: https://volcano.sh/en/docs/installation/
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/volcano/release-1.9/installer/volcano-development.yaml
```

### 验证安装

```bash
# 检查 LWS CRD
kubectl get crd leaderworkersets

# 检查 Volcano
kubectl get pods -n volcano-system
```

---

## Rolling Update 行为

LWS 部署的滚动更新使用原生 LWS 机制：

- **策略**: `maxUnavailable: 1`, `maxSurge: 0`
- **行为**: 旧和新 workers 在同一 Dynamo namespace 中
- **警告**: 新 workers 可能与旧 workers 通信，确保版本兼容

```
┌─ LeaderWorkerSet: vllm-disagg ────────────────────────────────────────┐
│                                                                        │
│  ┌─ Leader ─────────────┐                                             │
│  │ Pod (v1) ✓           │  不变                                        │
│  └──────────────────────┘                                             │
│                                                                        │
│  ┌─ Workers ─────────────────────────────────────────────────────────┐│
│  │                                                                   ││
│  │  ┌────────────────────┐  ┌─────────────────────┐                  ││
│  │  │ Pod (v2) ✓ NEW     │  │ Pod (v1) Terminating│  滚动一个一个更新   ││
│  │  └────────────────────┘  └─────────────────────┘                  ││
│  │                                                                   ││
│  └───────────────────────────────────────────────────────────────────┘│
│                                                                        │
│  ┌─ Dynamo Namespace: vllm-disagg ───────────────────────────────────┐│
│  │ 所有 v1 和 v2 pods 都注册，可以相互发现                             ││
│  └───────────────────────────────────────────────────────────────────┘│
│                                                                        │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 总结

| 问题 | 答案 |
|------|------|
| **LWS 是什么** | Kubernetes API，管理 leader-worker 拓扑的分布式工作负载 |
| **核心能力** | 角色区分、启动顺序保证、稳定网络身份、gang scheduling |
| **Dynamo 为什么需要** | 多节点推理需要 leader 协调多个 worker 节点 |
| **替代方案** | Grove (更高级，拓扑感知)，Deployment (仅单节点) |
| **推荐场景** | LWS: 简单多节点；Grove: 生产级大规模部署 |

---

## 相关源码

以下是本文档涉及的核心代码文件，方便读者查阅验证：

### 后端框架实现

| 后端 | 源码文件 | 说明 |
|------|---------|------|
| **vLLM** | `deploy/operator/internal/dynamo/backend_vllm.go` | vLLM 多节点配置注入 (Ray/MP 后端) |
| **SGLang** | `deploy/operator/internal/dynamo/backend_sglang.go` | SGLang 多节点配置注入 |
| **TensorRT-LLM** | `deploy/operator/internal/dynamo/backend_trtllm.go` | TRT-LLM 多节点配置 (mpirun/SSH) |

### 编排器实现

| 组件 | 源码文件 | 说明 |
|------|---------|------|
| **LWS Deployer** | `deploy/operator/internal/dynamo/lws.go` | LWS 环境变量 (`$LWS_LEADER_ADDRESS`, `$LWS_WORKER_INDEX`) |
| **Grove Deployer** | `deploy/operator/internal/dynamo/grove.go` | Grove 环境变量 (`$GROVE_PCSG_NAME`, `$GROVE_PCLQ_POD_INDEX`) |

### 控制器逻辑

| 功能 | 源码文件 | 关键函数/行号 |
|------|---------|--------------|
| **Orchestrator 选择** | `deploy/operator/internal/controller/dynamographdeployment_controller.go` | `isGrovePathway()` (L358-367) |
| **LWS 可用性检测** | 同上 | L332-336: 检测 `LWSEnabled` 状态 |
| **运行时配置** | `deploy/operator/internal/controller_common/runtime.go` | `RuntimeConfig` 结构体 (GroveEnabled, LWSEnabled) |
| **启动检测** | `deploy/operator/cmd/main.go` | L375-440: Grove/LWS/Volcano/KAI 检测逻辑 |

### 常量定义

| 常量 | 源码文件 | 值 |
|------|---------|-----|
| `VLLMPort` | `deploy/operator/internal/dynamo/backend_vllm.go:16` | `"6379"` (Ray 端口) |
| `VLLMMpMasterPort` | `deploy/operator/internal/consts/consts.go:99` | `"29500"` (MP 协调端口) |
| `SglangPort` | `deploy/operator/internal/dynamo/backend_sglang.go:13` | `"29500"` |
| `MpiRunSshPort` | `deploy/operator/internal/consts/consts.go:30` | `2222` (SSH 端口) |
| `KubeAnnotationEnableGrove` | `deploy/operator/internal/consts/consts.go:41` | `"nvidia.com/enable-grove"` |
| `KubeAnnotationVLLMDistributedExecutorBackend` | `deploy/operator/internal/consts/consts.go:96` | `"nvidia.com/vllm-distributed-executor-backend"` |

### 关键代码片段

#### vLLM Ray 后端注入 (`backend_vllm.go:207-228`)
```go
case RoleLeader:
    container.Args = []string{fmt.Sprintf("ray start --head --port=%s && %s %s %s",
        VLLMPort, fullCommand, originalArgs, "--distributed-executor-backend ray")}
case RoleWorker:
    container.Args = []string{fmt.Sprintf("ray start --address=%s:%s --block",
        leaderHostname, VLLMPort)}
```

#### vLLM MP 后端注入 (`backend_vllm.go:188-205`)
```go
mpFlags := fmt.Sprintf("--distributed-executor-backend mp --nnodes %d --master-addr %s --master-port %s",
    numberOfNodes, leaderHostname, commonconsts.VLLMMpMasterPort)
case RoleLeader:
    mpFlags += " --node-rank 0"
case RoleWorker:
    mpFlags += fmt.Sprintf(" --node-rank %s --headless", nodeRank)
```

#### SGLang 多节点注入 (`backend_sglang.go:70-84`)
```go
distInitAddr := fmt.Sprintf("%s:%s", multinodeDeployer.GetLeaderHostname(serviceName), SglangPort)
flags := fmt.Sprintf("--dist-init-addr %s --nnodes %d --node-rank %s",
    distInitAddr, numberOfNodes, nodeRank)
```

#### TRT-LLM Worker Probe 配置 (`backend_trtllm.go:44-59`)
```go
if role == RoleWorker {
    container.LivenessProbe = nil
    container.StartupProbe = nil
    container.ReadinessProbe = &corev1.Probe{
        ProbeHandler: corev1.ProbeHandler{
            TCPSocket: &corev1.TCPSocketAction{
                Port: intstr.FromInt(commonconsts.MpiRunSshPort), // 2222
            },
        },
        ...
    }
}
```

#### Orchestrator 选择逻辑 (`dynamographdeployment_controller.go:358-367`)
```go
func (r *DynamoGraphDeploymentReconciler) isGrovePathway(dgd *...) bool {
    enableGrove := true
    if dgd.Annotations != nil &&
       strings.ToLower(dgd.Annotations[consts.KubeAnnotationEnableGrove]) == "false" {
        enableGrove = false
    }
    return enableGrove && r.RuntimeConfig.GroveEnabled
}
```

#### LWS 不可用时报错 (`dynamographdeployment_controller.go:332-336`)
```go
if !r.isGrovePathway(dynamoDeployment) && hasMultinode && !r.RuntimeConfig.LWSEnabled {
    return ReconcileResult{}, fmt.Errorf("no multinode orchestrator available")
}
```

### 测试文件

| 测试文件 | 说明 |
|---------|------|
| `deploy/operator/internal/dynamo/backend_vllm_test.go` | vLLM 后端单元测试 |
| `deploy/operator/internal/dynamo/backend_sglang_test.go` | SGLang 后端单元测试 |
| `deploy/operator/internal/dynamo/backend_trtllm_test.go` | TRT-LLM 后端单元测试 |
| `deploy/operator/internal/dynamo/grove_test.go` | Grove 编排器测试 |

---

## Q&A: Intel XPU 多节点部署

### Q: 如果用户指定 Intel XPU，LeaderWorkerSet 层面需要代码适配吗？

**A: 不需要，LeaderWorkerSet 层面无需任何改动。**

LeaderWorkerSet 是 GPU 类型无关的 Kubernetes 编排 CRD，只管理 Leader-Worker Pod 拓扑结构。GPU 类型的区分发生在更底层的两个地方：

#### 1. 资源请求层面 - `gpuType` 字段

```go
// api/v1alpha1/common.go:79-92
type ResourceItem struct {
    GPU string `json:"gpu,omitempty"`
    // GPUType can specify a custom GPU type, e.g. "gpu.intel.com/xe"
    // By default if not specified, the GPU type is "nvidia.com/gpu"
    GPUType string `json:"gpuType,omitempty"`
}

// controller_common/resource.go:586-594
func getGPUResourceName(resourceItem *v1alpha1.ResourceItem) corev1.ResourceName {
    if resourceItem.GPUType != "" {
        return corev1.ResourceName(resourceItem.GPUType)  // 例如 "gpu.intel.com/xe"
    }
    return corev1.ResourceName(consts.KubeResourceGPUNvidia)  // 默认 "nvidia.com/gpu"
}
```

**这已经支持了 Intel XPU！** 用户只需在 YAML 中指定：

```yaml
resources:
  limits:
    gpu: "4"
    gpuType: "gpu.intel.com/xe"  # Intel XPU
```

#### 2. 多节点推理逻辑层面 - GPU 数量计算

```go
// backend_vllm.go:337-345
func getContainerGPUs(resources *v1alpha1.Resources) int64 {
    if resources == nil || resources.Limits == nil || resources.Limits.GPU == "" {
        return 0
    }
    if gpus, err := strconv.ParseInt(resources.Limits.GPU, 10, 64); err == nil {
        return gpus  // 只关心数量，不关心类型
    }
    return 0
}
```

**关键点**：多节点判断逻辑只关心 GPU **数量**，不关心 GPU **类型**：
- 判断是否需要多节点：`getWorldSize() > containerGPUs`
- 计算 TP/PP 分布
- 注入 Ray/MP 后端参数

这些逻辑对 NVIDIA GPU 和 Intel XPU 完全相同。

### 真正需要适配的地方

| 层级 | 是否需要适配 XPU | 说明 |
|------|-----------------|------|
| **LeaderWorkerSet CRD** | ❌ 不需要 | 纯 Kubernetes 编排，GPU 类型无关 |
| **Dynamo Operator** | ❌ 不需要 | 已通过 `gpuType` 字段支持 |
| **vLLM/SGLang/TRT-LLM 后端注入逻辑** | ❌ 不需要 | 只关心 GPU 数量 |
| **容器镜像** | ✅ **必须适配** | 需要 XPU 版本的 runtime 镜像 |
| **vLLM 本身** | ✅ **必须支持 XPU** | 需要 `vllm` 包支持 Intel XPU |
| **Kubernetes 节点** | ✅ **必须配置** | Intel GPU 设备插件安装 |
| **通信库** | ⚠️ 可能需要 | 多节点通信可能需要 OneCCL 替代 NCCL |

### XPU 多节点部署示例

```yaml
apiVersion: nvidia.com/v1alpha1
kind: DynamoGraphDeployment
metadata:
  name: xpu-multinode-test
spec:
  services:
    VllmWorker:
      multinode:
        nodeCount: 2        # 多节点
      resources:
        limits:
          gpu: "4"
          gpuType: "gpu.intel.com/xe"  # Intel XPU
      extraPodSpec:
        mainContainer:
          image: your-xpu-vllm-image:v1.0  # XPU 兼容镜像
          args:
            - --model
            - Qwen/Qwen3-32B
            - --tensor-parallel-size
            - "8"
```

### XPU 多节点架构图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                      XPU 多节点部署架构                                   │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  用户配置                                                                │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ gpuType: "gpu.intel.com/xe"                                      │   │
│  │ nodeCount: 2                                                     │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              │                                          │
│                              ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ Dynamo Operator (无需改动)                                        │   │
│  │ - 检测 nodeCount > 1 → 多节点                                     │   │
│  │ - 生成 LeaderWorkerSet                                           │   │
│  │ - 资源请求使用 "gpu.intel.com/xe"                                 │   │
│  │ - 注入 Ray/MP 参数 (GPU 类型无关)                                  │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              │                                          │
│                              ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ LeaderWorkerSet (无需改动)                                        │   │
│  │ - 创建 Leader Pod + Worker Pod                                    │   │
│  │ - Pod 资源请求: gpu.intel.com/xe: 4                               │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              │                                          │
│                              ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ Kubernetes 节点 (需要配置)                                        │   │
│  │ - Intel GPU 设备插件                                              │   │
│  │ - 节点资源: gpu.intel.com/xe: 4                                   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                              │                                          │
│                              ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ 容器镜像 (需要适配)                                               │   │
│  │ - vLLM with XPU support                                          │   │
│  │ - Intel oneAPI / IPEX                                            │   │
│  │ - 多节点通信: OneCCL (替代 NCCL)                                  │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 结论

如果想在 Intel XPU 上使用多节点推理，只需要：
1. 使用 XPU 兼容的容器镜像
2. 在 YAML 中指定 `gpuType: "gpu.intel.com/xe"`
3. 确保 Kubernetes 节点已安装 Intel GPU 设备插件

**Operator 和 LWS 层面完全不需要代码改动。**

---

## 相关文档

- [多节点部署指南](../docs/kubernetes/deployment/multinode-deployment.md)
- [滚动更新指南](../docs/kubernetes/rolling-update.md)
- [安装指南](../docs/kubernetes/installation-guide.md)
- [LWS GitHub](https://github.com/kubernetes-sigs/lws)
- [Volcano 官网](https://volcano.sh/)
