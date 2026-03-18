# Dynamo CRD 类型详解

Dynamo 提供了三个主要的 Custom Resource Definitions (CRDs)，用于在 Kubernetes 上管理推理工作负载。

## 概览

| CRD | 缩写 | 功能描述 |
|-----|------|---------|
| **DynamoGraphDeployment** | **DGD** | 部署完整的推理管道 |
| **DynamoGraphDeploymentRequest** | **DGDR** | SLA 驱动的高级部署请求接口 |
| **DynamoGraphDeploymentScalingAdapter** | **DGDSA** | 为 DGD 中的服务提供自动扩缩容接口 |

## 代码位置

| CRD | 类型定义 | 控制器 |
|-----|---------|--------|
| **DGD** | `deploy/operator/api/v1alpha1/dynamographdeployment_types.go` | `deploy/operator/internal/controller/dynamographdeployment_controller.go` |
| **DGDR** | `deploy/operator/api/v1alpha1/dynamographdeploymentrequest_types.go` | `deploy/operator/internal/controller/dynamographdeploymentrequest_controller.go` |
| **DGDSA** | `deploy/operator/api/v1alpha1/dynamographdeploymentscalingadapter_types.go` | `deploy/operator/internal/controller/dynamographdeploymentscalingadapter_controller.go` |

**其他相关文件**：

- `deploy/operator/api/v1alpha1/common.go` - 通用类型定义（如 `DynamoComponentDeploymentSharedSpec`）
- `deploy/operator/api/v1beta1/` - DGDR 的 v1beta1 版本定义
- `deploy/operator/api/v1alpha1/zz_generated.deepcopy.go` - kubebuilder 自动生成的 deep copy 代码
- `deploy/operator/internal/webhook/validation/` - CRD 验证逻辑
- `deploy/operator/internal/webhook/defaulting/` - CRD 默认值设置

---

## 1. DynamoGraphDeployment (DGD)

### 功能

部署完整的推理管道。这是最基础的 CRD，定义推理图的完整配置。

### 核心能力

- 定义多个 `services` (Frontend、Workers 等)
- 支持 aggregated 和 disaggregated serving
- 支持多节点部署
- 集成 PVC、Secrets、ConfigMaps

### 示例配置

```yaml
apiVersion: nvidia.com/v1alpha1
kind: DynamoGraphDeployment
metadata:
  name: dynamo-agg
  namespace: my-namespace
spec:
  backendFramework: vllm
  pvcs:
    - name: model-cache
      create: false
  services:
    Frontend:
      componentType: frontend
      replicas: 1
      volumeMounts:
        - name: model-cache
          mountPoint: /opt/models
      envs:
        - name: HF_HOME
          value: /opt/models
      extraPodSpec:
        mainContainer:
          image: nvcr.io/nvidia/ai-dynamo/vllm-runtime:0.8.0

    VLLMWorker:
      envFromSecret: hf-token-secret
      componentType: worker
      replicas: 4
      resources:
        limits:
          gpu: "2"
      sharedMemory:
        size: 16Gi
      volumeMounts:
        - name: model-cache
          mountPoint: /opt/models
      envs:
        - name: HF_HOME
          value: /opt/models
      extraPodSpec:
        mainContainer:
          image: nvcr.io/nvidia/ai-dynamo/vllm-runtime:0.8.0
          args:
            - --model
            - "Qwen/Qwen3-32B-FP8"
            - --tensor-parallel-size
            - "2"
```

### Service 配置字段

| 字段 | 描述 |
|------|------|
| `componentType` | 组件类型 (`frontend`, `worker`) |
| `subComponentType` | 子类型 (`prefill`, `decode`) |
| `replicas` | Pod 副本数 |
| `resources` | CPU/GPU/内存资源 |
| `envs` | 环境变量 |
| `envFromSecret` | 从 Secret 注入环境变量 |
| `volumeMounts` | PVC 挂载 |
| `sharedMemory` | /dev/shm 配置 |
| `extraPodSpec` | 自定义 Pod 配置 |
| `scalingAdapter` | 启用 DGDSA |
| `multinode` | 多节点配置 |

### 状态生命周期

```
initializing → pending → successful
                       ↘ failed
```

### 状态字段

| 字段 | 描述 |
|------|------|
| `state` | 高层状态 (`initializing`, `pending`, `successful`, `failed`) |
| `conditions` | 详细条件列表 |
| `services` | 每个 service 的副本状态 |
| `rollingUpdate` | 滚动更新状态 |

---

## 2. DynamoGraphDeploymentRequest (DGDR)

### 功能

SLA 驱动的高级部署请求接口。用户指定模型、后端和性能约束，系统自动生成最优的 DGD 配置。

### 核心能力

- 自动 profiling (通过 AIC 模拟或真实 GPU)
- 根据 SLA 目标 (TTFT, TPOT) 自动优化配置
- 自动 GPU 发现
- 可选自动创建 DGD (`autoApply: true`)

### 示例配置

```yaml
apiVersion: nvidia.com/v1alpha1
kind: DynamoGraphDeploymentRequest
metadata:
  name: my-deployment-request
  namespace: my-namespace
spec:
  model: Qwen/Qwen3-32B-FP8
  backend: vllm
  autoApply: true
  deploymentOverrides:
    name: my-optimized-deployment
    labels:
      environment: production
  profilingConfig:
    config:
      sla:
        ttft: 600      # Time to First Token (ms)
        tpot: 16.67    # Time Per Output Token (ms)
      input:
        isl: 4000      # Input Sequence Length
        osl: 500       # Output Sequence Length
```

### 状态生命周期

```
Initializing → Pending → Profiling → Deploying → Ready
                                                  ↓
                                         DeploymentDeleted / Failed
```

**状态说明**：

| 状态 | 描述 |
|------|------|
| `Initializing` | 验证 spec，准备 profiling |
| `Pending` | 准备运行 profiling job |
| `Profiling` | 运行 profiling (online 或 AIC) |
| `Deploying` | Profiling 完成，创建 DGD (autoApply=true) |
| `Ready` | 终态，DGD 就绪或 spec 可用 |
| `DeploymentDeleted` | 自动创建的 DGD 被手动删除 |
| `Failed` | 失败 |

### 关键特性

1. **自动 GPU 发现**：从集群节点自动检测 GPU 配置
2. **Profiling 后不可变**：Profiling 开始后 spec 不可修改
3. **生成 DGD Spec**：在 `status.generatedDeployment` 中存储生成的配置
4. **自动部署**：`autoApply: true` 自动创建 DGD

### 输出示例

```
********************************************************************************
*                     Dynamo aiconfigurator Final Results                      *
********************************************************************************
  ----------------------------------------------------------------------------
  Input Configuration & SLA Target:
    Model: Qwen/Qwen3-32B-FP8 (is_moe: False)
    Total GPUs: 8
    Best Experiment Chosen: disagg at 446.85 tokens/s/gpu (disagg 1.38x better)
  ----------------------------------------------------------------------------
  Overall Best Configuration:
    - Best Throughput: 3,574.80 tokens/s
    - Per-GPU Throughput: 446.85 tokens/s/gpu
    - TTFT: 453.18ms
    - TPOT: 18.66ms
  ----------------------------------------------------------------------------
```

---

## 3. DynamoGraphDeploymentScalingAdapter (DGDSA)

### 功能

为 DGD 中的单个服务提供自动扩缩容接口。实现 Kubernetes `scale` subresource，支持 HPA、KEDA、Planner 等自动扩缩器。

### 核心能力

- 实现 Kubernetes Scale Subresource
- 集成 HPA、KEDA、Planner
- 作为扩缩器与 DGD 之间的中介，防止冲突

### 工作原理

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         DGDSA 工作流程                                   │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐               │
│  │    HPA      │     │    KEDA     │     │   Planner   │               │
│  └──────┬──────┘     └──────┬──────┘     └──────┬──────┘               │
│         │                   │                   │                       │
│         └───────────────────┼───────────────────┘                       │
│                             │                                           │
│                             ▼                                           │
│              ┌─────────────────────────────┐                           │
│              │            DGDSA             │                           │
│              │                             │                           │
│              │  spec.replicas: 4           │                           │
│              │  dgdRef:                    │                           │
│              │    name: my-dgd             │                           │
│              │    serviceName: VLLMWorker  │                           │
│              └──────────────┬──────────────┘                           │
│                             │                                           │
│                             │ 同步 replicas                             │
│                             ▼                                           │
│              ┌─────────────────────────────┐                           │
│              │            DGD               │                           │
│              │                             │                           │
│              │  services:                  │                           │
│              │    VLLMWorker:              │                           │
│              │      replicas: 4 ←── 自动调整│                           │
│              └─────────────────────────────┘                           │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 示例配置

```yaml
apiVersion: nvidia.com/v1alpha1
kind: DynamoGraphDeploymentScalingAdapter
metadata:
  name: vllm-worker-scaler
  namespace: my-namespace
spec:
  replicas: 4  # 由 HPA/KEDA 自动调整
  dgdRef:
    name: my-dynamo-deployment      # DGD 名称
    serviceName: VLLMWorker         # 要扩缩容的 service
```

### 状态字段

| 字段 | 描述 |
|------|------|
| `replicas` | 当前副本数 (从 DGD 同步) |
| `selector` | Pod label selector (HPA 兼容) |
| `lastScaleTime` | 最后扩缩容时间 |

### 在 DGD 中启用 DGDSA

```yaml
apiVersion: nvidia.com/v1alpha1
kind: DynamoGraphDeployment
metadata:
  name: my-dynamo-deployment
spec:
  services:
    VLLMWorker:
      componentType: worker
      replicas: 4
      scalingAdapter:
        enabled: true  # 启用 DGDSA
```

---

## 三者关系

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         CRD 关系图                                       │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                         DGDR                                     │   │
│  │                 (用户请求 + SLA 目标)                             │   │
│  │                                                                  │   │
│  │  - model: Qwen/Qwen3-32B                                        │   │
│  │  - backend: vllm                                                │   │
│  │  - sla: ttft=600ms, tpot=16ms                                   │   │
│  │                                                                  │   │
│  └──────────────────────────┬──────────────────────────────────────┘   │
│                             │                                          │
│                             │ profiling + 优化                         │
│                             ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                          DGD                                     │   │
│  │                 (完整推理管道配置)                                 │   │
│  │                                                                  │   │
│  │  services:                                                       │   │
│  │    Frontend: ...                                                 │   │
│  │    VLLMWorker:                                                   │   │
│  │      replicas: 4                                                 │   │
│  │      scalingAdapter: enabled                                     │   │
│  │                                                                  │   │
│  └──────────────────────────┬──────────────────────────────────────┘   │
│                             │                                          │
│                             │ 引用                                      │
│                             ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                         DGDSA                                    │   │
│  │              (自动扩缩容接口)                                      │   │
│  │                                                                  │   │
│  │  dgdRef:                                                         │   │
│  │    name: my-dgd                                                  │   │
│  │    serviceName: VLLMWorker                                       │   │
│  │  replicas: 4  ←── 被 HPA/KEDA/Planner 控制                       │   │
│  │                                                                  │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 使用场景

| 场景 | 推荐使用的 CRD |
|------|--------------|
| 手动定义完整配置 | **DGD** 直接使用 |
| SLA 驱动自动优化 | **DGDR** → 自动生成 DGD |
| 需要自动扩缩容 | **DGD** + **DGDSA** + HPA/KEDA |
| 生产部署 + SLA + 自动扩缩 | **DGDR** → **DGD** + **DGDSA** |

---

## 其他 CRDs

除了三个主要 CRD，Dynamo 还提供：

### DynamoComponentDeployment (DCD)

部署单个组件。通常由 DGD 自动创建和管理。

### DynamoModel

管理模型生命周期，如加载 LoRA adapters。

```yaml
apiVersion: nvidia.com/v1alpha1
kind: DynamoModel
metadata:
  name: my-lora-adapter
spec:
  modelName: meta-llama/Llama-3.3-70B-Instruct-lora
  baseModelName: meta-llama/Llama-3.3-70B-Instruct
  modelType: lora
  source:
    hub:
      repoId: my-org/my-lora-adapter
```

### DynamoCheckpoint

管理容器 checkpoint，用于快速冷启动。

---

## DGDR Profiling 机制详解

### SLA 自动优化原理

DGDR 通过 **两种策略** 实现 SLA 目标 (TTFT, TPOT) 的自动优化配置：

#### 1. RAPID 策略（默认）- AIC 模拟

**不做真实 benchmark**，而是使用 **AIConfigurator (AIC)** 进行性能模拟：

```
用户 SLA 目标 → AIC 性能数据库查询 → 模拟计算 TTFT/TPOT → 选择最优配置
```

**核心机制**：
- `aiconfigurator` 是 NVIDIA 内部的性能预测工具
- 它有一个**预构建的性能数据库**，包含各种模型/GPU/后端组合的基准数据
- 通过 `AIConfiguratorPerfEstimator` 类估算：
  - Prefill 时间 (TTFT)
  - Decode 时间 (ITL/TPOT)
  - 最大 batch size、KV cache tokens 等

**代码路径**：
- `components/src/dynamo/profiler/rapid.py:run_rapid()` → 调用 `aiconfigurator.sdk.task.TaskRunner.run()` 进行模拟

#### 2. THOROUGH 策略 - 真实 GPU Benchmark

**实际部署并运行 benchmark**：

```
枚举配置候选 → 部署每个候选 → 运行 AIPerf benchmark → 测量真实 TTFT/TPOT → 选择最优
```

**流程**（`thorough.py`）：
1. **枚举阶段**：使用 `enumerate_profiling_configs()` 生成 prefill 和 decode 候选配置
2. **Benchmark 阶段**：
   - 部署每个候选配置到 K8s
   - 运行 `aiperf` 工具测量真实性能
   - Prefill：测量 TTFT
   - Decode：扫描不同并发度，测量 ITL 和吞吐
3. **选择阶段**：根据 SLA 目标从结果中选择最优配置

#### 选择模式（Picking Mode）

两种策略都支持三种选择模式：

| 模式 | 说明 |
|------|------|
| `default` | 在 GPU 预算内最大化吞吐，满足 SLA 约束 |
| `autoscale` | 计算需要多少 GPU 才能满足 SLA |
| `load_match` | 根据目标请求率/并发度匹配最优配置 |

#### 策略对比

| 策略 | 方式 | 时间 | 准确度 | 适用场景 |
|------|------|------|--------|---------|
| **RAPID** | AIC 模拟（性能数据库） | 秒级 | 预估值 | 快速评估、开发测试 |
| **THOROUGH** | 真实 GPU benchmark | 分钟~小时 | 实测值 | 生产部署、AIC 不支持的模型 |

---

### 非 NVIDIA GPU 场景（如 Intel XPU）

Intel XPU **不在 AIC 性能数据库中**，所以 `aic_supported = False`。

#### 代码路径

```
profile_sla.py:run_profile()
    │
    ├── check_model_hardware_support(model, system, backend)
    │   └── 返回 False（Intel XPU 不在 AIC 数据库中）
    │
    └── _execute_strategy()
         │
         ├── if searchStrategy == RAPID:
         │   └── rapid.py:run_rapid()
         │        │
         │        └── if not aic_supported:  ←── Intel XPU 走这里
         │            └── _run_naive_fallback()
         │                 ├── build_naive_generator_params()  # 生成默认配置
         │                 ├── generate_backend_artifacts()     # 生成 DGD YAML
         │                 └── 返回 { best_config_df: 空, best_latencies: 0 }
         │
         └── if searchStrategy == THOROUGH:
             └── thorough.py:run_thorough()
                  ├── enumerate_profiling_configs()  # 枚举候选配置
                  ├── _benchmark_prefill_candidates() # 真实部署 + benchmark
                  ├── _benchmark_decode_candidates()  # 真实部署 + benchmark
                  └── _pick_thorough_best_config()    # 选择最优
```

#### 关键代码位置

| 步骤 | 文件 | 函数 | 说明 |
|------|------|------|------|
| 检查 AIC 支持 | `profile_sla.py:311` | `check_model_hardware_support()` | Intel XPU 返回 `False` |
| RAPID fallback | `rapid.py:316-317` | `if not aic_supported` | XPU 走 naive 路径 |
| Naive 配置生成 | `rapid.py:106-147` | `_run_naive_fallback()` | 无性能数据，生成默认配置 |
| THOROUGH benchmark | `thorough.py:305-458` | `run_thorough()` | 真实 GPU benchmark |

#### Intel XPU 的两种选择

##### 选项 1: RAPID + Naive Fallback（默认）

```yaml
spec:
  searchStrategy: rapid  # 或不指定（默认）
```

**结果**：
- 调用 `_run_naive_fallback()`
- 生成一个**无优化**的默认配置
- `best_config_df` 为空，`best_latencies` 全是 0
- **没有 SLA 优化**，只是生成一个能跑的配置

##### 选项 2: THOROUGH（需要显式指定）

```yaml
spec:
  searchStrategy: thorough
  backend: vllm  # 必须指定具体后端，不能用 auto
```

**结果**：
- 真实部署 + benchmark
- 获得**实测**的 TTFT/TPOT 数据
- 根据 SLA 目标选择最优配置
- **耗时更长，但结果准确**

#### Planner Throughput Scaling 的限制

在 `dgdr_validate.py:159-166` 有验证逻辑：

```python
if (
    planner_sweep_mode == PlannerPreDeploymentSweepMode.Rapid
    and not aic_supported
):
    raise ValueError(
        "AIC does not support this model/hardware/backend combination. "
        "pre_deployment_sweeping_mode can only be 'thorough'..."
    )
```

**如果启用了 Planner 的 throughput scaling 功能**，Intel XPU **必须**使用 `thorough` 模式，否则会报错。

#### 场景总结

| 场景 | RAPID 策略 | THOROUGH 策略 |
|------|-----------|--------------|
| Intel XPU + 无 Planner | Naive 配置（无优化） | ✅ 真实 benchmark |
| Intel XPU + Planner throughput scaling | ❌ 报错 | ✅ 必须使用 |

**建议**：对于 Intel XPU，如果需要 SLA 优化，应该使用 `searchStrategy: thorough`。

---

## DGDSA 详解

### `scalingAdapter.enabled` vs 直接设置 `replicas` 的区别

| 方式 | 谁控制 replicas | 能否用 HPA/KEDA |
|------|----------------|----------------|
| 只设置 `replicas: 4` | DGD 直接管理 | ❌ 不能 |
| `scalingAdapter.enabled: true` + `replicas: 4` | DGDSA 管理 | ✅ 可以 |

#### 方式 1：只设置 `replicas: 4`（无 scalingAdapter）

```yaml
services:
  VLLMWorker:
    componentType: worker
    replicas: 4  # 直接在 DGD 中写死
```

**效果**：
- DGD 控制器直接管理 replicas
- **HPA/KEDA 无法介入**（HPA 需要 Scale subresource）
- 要扩缩容必须手动修改 DGD

#### 方式 2：启用 `scalingAdapter.enabled: true`

```yaml
services:
  VLLMWorker:
    componentType: worker
    replicas: 4  # 初始值
    scalingAdapter:
      enabled: true  # 创建 DGDSA
```

**效果**：
1. DGD 控制器创建一个 `DynamoGraphDeploymentScalingAdapter` CR
2. **DGDSA 拥有 replicas 的控制权**
3. HPA/KEDA/Planner 可以通过 DGDSA 的 Scale subresource 自动调整 replicas

#### 工作流程对比

```
┌─────────────────────────────────────────────────────────────────────────┐
│  方式 1: 只设置 replicas                                                 │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  用户 ──修改 DGD──> replicas: 4                                         │
│                          │                                              │
│                          ▼                                              │
│                    DGD 控制器直接创建 4 个 Pod                           │
│                                                                         │
│  HPA/KEDA ──❌ 无法介入（没有 Scale subresource）                        │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│  方式 2: scalingAdapter.enabled = true                                  │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  DGD 控制器创建 DGDSA                                                    │
│        │                                                                │
│        ▼                                                                │
│  ┌─────────────────────────────────────┐                               │
│  │            DGDSA                     │ ←── HPA/KEDA 通过 Scale API   │
│  │  spec.replicas: 4                   │     修改这个值                 │
│  │  implements Scale subresource       │                               │
│  └──────────────┬──────────────────────┘                               │
│                 │                                                       │
│                 │ 同步到 DGD                                             │
│                 ▼                                                       │
│  DGD.services.VLLMWorker.replicas = 4                                  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

#### 场景总结

| 场景 | 推荐配置 |
|------|---------|
| 固定副本数，不需要自动扩缩 | 只设置 `replicas` |
| 需要 HPA/KEDA 自动扩缩 | `scalingAdapter.enabled: true` |
| 需要 Planner 智能扩缩 | `scalingAdapter.enabled: true` |

**`scalingAdapter.enabled: true` 的核心价值是暴露 Kubernetes Scale subresource**，让标准的自动扩缩器（HPA/KEDA）可以控制 replicas。

### DGDSA 与 Intel XPU 兼容性

**DGDSA 完全不依赖 GPU 类型**，它是一个纯粹的 Kubernetes 层面的抽象。

控制器逻辑（`dynamographdeploymentscalingadapter_controller.go`）：

1. **读取 DGDSA** → 获取目标 replicas 和 DGD 引用
2. **读取 DGD** → 找到对应的 service
3. **比较 replicas** → 如果不同，更新 DGD 的 `spec.services[name].replicas`
4. **构建 Pod selector** → 用于 HPA 兼容性

**没有任何 GPU 相关的逻辑**。

| 组件 | Intel XPU 兼容性 |
|------|-----------------|
| **DGD** | ✅ 完全支持（通过 `gpuType` 字段） |
| **DGDSA** | ✅ 完全支持（GPU 无关） |
| **DGDR - RAPID** | ⚠️ Naive fallback（无优化） |
| **DGDR - THOROUGH** | ✅ 支持（需要真实 benchmark） |

---

## 自动扩缩容触发机制

### HPA (Horizontal Pod Autoscaler)

HPA 基于**实时指标**扩缩容，当指标超过/低于阈值时触发：

```
当前指标值 / 阈值 = 期望副本数比例

例如：CPU 使用率 80%，阈值 50%
      80 / 50 = 1.6 → 副本数增加到 1.6 倍
```

**常见触发条件**：

| 指标类型 | 示例配置 | 触发条件 |
|---------|---------|---------|
| CPU | 平均 CPU > 50% | 扩容 |
| 内存 | 平均内存 > 80% | 扩容 |
| 自定义 | 请求数/秒 > 1000 | 扩容 |

**HPA 示例**：

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: vllm-worker-hpa
spec:
  scaleTargetRef:
    apiVersion: nvidia.com/v1alpha1
    kind: DynamoGraphDeploymentScalingAdapter
    name: my-dgd-vllmworker  # 指向 DGDSA
  minReplicas: 2
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 50  # CPU > 50% 触发扩容
```

### KEDA (Kubernetes Event-driven Autoscaling)

KEDA 基于**外部事件源**扩缩容，更适合事件驱动场景：

```
队列长度 / 阈值 = 期望副本数

例如：Kafka 消息 500 条，每副本处理 100 条
      500 / 100 = 5 → 需要 5 个副本
```

**KEDA 支持的事件源**：

| 事件源 | 触发条件 |
|--------|---------|
| Kafka | 消息积压量 |
| RabbitMQ | 队列深度 |
| Prometheus | 自定义指标 |
| Redis | List 长度 |

**KEDA 示例**：

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: vllm-worker-scaler
spec:
  scaleTargetRef:
    apiVersion: nvidia.com/v1alpha1
    kind: DynamoGraphDeploymentScalingAdapter
    name: my-dgd-vllmworker  # 指向 DGDSA
  minReplicaCount: 1
  maxReplicaCount: 20
  triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus:9090
      metricName: vllm_request_queue_size
      threshold: "100"  # 请求队列 > 100 触发扩容
      query: vllm_request_queue_size
```

### LLM 推理服务的扩缩容时机

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    LLM 推理扩缩容时机                                    │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐              │
│  │   请求队列   │ ──> │  KV Cache   │ ──> │   GPU 显存   │              │
│  │   积压增加   │     │  使用率升高  │     │   压力增大   │              │
│  └─────────────┘     └─────────────┘     └─────────────┘              │
│         │                   │                   │                      │
│         ▼                   ▼                   ▼                      │
│  ┌─────────────────────────────────────────────────────────────────┐  │
│  │              指标超过阈值 ──> HPA/KEDA 计算新副本数              │  │
│  └─────────────────────────────────────────────────────────────────┘  │
│         │                                                              │
│         ▼                                                              │
│  ┌─────────────────────────────────────────────────────────────────┐  │
│  │              更新 DGDSA spec.replicas                            │  │
│  └─────────────────────────────────────────────────────────────────┘  │
│         │                                                              │
│         ▼                                                              │
│  ┌─────────────────────────────────────────────────────────────────┐  │
│  │              DGDSA 同步到 DGD ──> 创建新 Pod                     │  │
│  └─────────────────────────────────────────────────────────────────┘  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

**实际有用的指标**：

| 指标 | 说明 | 适合的扩缩器 |
|------|------|-------------|
| `vllm:num_requests_waiting` | 等待处理的请求数 | KEDA (Prometheus) |
| `vllm:gpu_cache_usage_perc` | KV Cache 使用率 | HPA |
| `vllm:avg_prompt_throughput` | Prompt 处理吞吐 | 自定义 |
| CPU/Memory | 基础资源指标 | HPA (内置) |

### 扩缩器对比

| 扩缩器 | 触发依据 | 适合场景 |
|--------|---------|---------|
| **HPA** | 实时资源指标（CPU/内存/自定义） | 负载波动、资源压力 |
| **KEDA** | 外部事件源（队列、消息数等） | 事件驱动、请求队列 |
| **Planner** | SLA 目标 + 性能预测 | Dynamo 特有的智能扩缩 |

对于 LLM 推理，**KEDA + Prometheus 指标**（如请求队列长度）通常比 HPA + CPU 更有意义，因为 GPU 推理的瓶颈往往不是 CPU。

---

## 相关文档

- [Dynamo Operator 文档](../docs/kubernetes/dynamo-operator.md)
- [API Reference](../docs/kubernetes/api-reference.md)
- [创建部署指南](../docs/kubernetes/deployment/create-deployment.md)
- [DynamoModel 指南](../docs/kubernetes/deployment/dynamomodel-guide.md)
