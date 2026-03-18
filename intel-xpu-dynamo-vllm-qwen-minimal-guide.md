# Intel XPU 上启动 Dynamo vLLM（Qwen3-0.6B）最小测试指南

> 目标：在你现有的 K8s + Intel XPU 节点上，最快完成一次 **Dynamo + vLLM + Qwen/Qwen3-0.6B** 的“可启动、可调用”测试。

---

## 1) 先看哪些文档（按顺序）

1. **平台安装总流程**  
   `docs/kubernetes/installation-guide.md`  
   用途：安装 Dynamo Platform（operator/CRDs/etcd/nats），这是后续部署 DGD 的前提。

2. **K8s 部署入口与 first model 示例**  
   `docs/kubernetes/README.md`  
   用途：看最短上手路径、核心 CR 概念、基础验证命令。

3. **vLLM 示例说明**  
   `examples/backends/vllm/deploy/README.md`  
   用途：明确 `agg.yaml` 适合最简单测试、默认模型就是 Qwen3-0.6B。

4. **最小可跑模板**  
   `examples/backends/vllm/deploy/agg.yaml`  
   用途：直接改这个文件进行部署（镜像、资源配置）。

5. **API 字段细节（重点看 gpuType）**  
   `docs/kubernetes/api-reference.md`（`ResourceItem.gpuType`）  
   用途：确认如何把默认 `nvidia.com/gpu` 改为 Intel 资源键（如 `gpu.intel.com/xe`）。

---

## 2) 部署前关键前提（Intel XPU）

在当前仓库中，官方支持矩阵和公开 vLLM runtime 镜像主要围绕 NVIDIA CUDA 生态。  
你要在 Intel XPU 上跑通，**必须**同时满足：

- K8s 中可见 Intel 设备资源（通常 `gpu.intel.com/xe`）
- 你有可用的 XPU 兼容 runtime 镜像（能运行 `python3 -m dynamo.vllm`）
- 节点可访问 HuggingFace（用于拉 `Qwen/Qwen3-0.6B`）

---

## 3) 最小部署步骤（可直接执行的骨架）

### Step A. 设置变量并安装平台

```bash
export NAMESPACE=dynamo-system
export RELEASE_VERSION=<your-dynamo-version>

./deploy/pre-deployment/pre-deployment-check.sh

helm fetch https://helm.ngc.nvidia.com/nvidia/ai-dynamo/charts/dynamo-platform-${RELEASE_VERSION}.tgz
helm install dynamo-platform dynamo-platform-${RELEASE_VERSION}.tgz \
  --namespace ${NAMESPACE} --create-namespace

kubectl get pods -n ${NAMESPACE}
```

### Step B. 创建 HF Token Secret

```bash
export HF_TOKEN=<your_hf_token>
kubectl create secret generic hf-token-secret \
  --from-literal=HF_TOKEN="${HF_TOKEN}" \
  -n ${NAMESPACE}
```

### Step C. 基于 `agg.yaml` 做最小修改

编辑 `examples/backends/vllm/deploy/agg.yaml`，至少改 2 类内容：
1) 把镜像改为你的 XPU 兼容镜像  
2) Worker 资源指定 Intel GPU 类型

示例（关键片段）：

```yaml
spec:
  services:
    Frontend:
      extraPodSpec:
        mainContainer:
          image: <your-xpu-runtime-image>:<tag>
    VllmDecodeWorker:
      resources:
        limits:
          gpu: "1"
          gpuType: "gpu.intel.com/xe"
      extraPodSpec:
        mainContainer:
          image: <your-xpu-runtime-image>:<tag>
          command:
            - python3
            - -m
            - dynamo.vllm
          args:
            - --model
            - Qwen/Qwen3-0.6B
```

### Step D. 部署并检查状态

```bash
kubectl apply -f examples/backends/vllm/deploy/agg.yaml -n ${NAMESPACE}
kubectl get dynamographdeployment -n ${NAMESPACE}
kubectl get pods -n ${NAMESPACE} -w
```

### Step E. 做最小连通性验证

```bash
kubectl port-forward svc/vllm-agg-frontend 8000:8000 -n ${NAMESPACE}
```

另开终端：

```bash
curl http://localhost:8000/v1/models

curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen/Qwen3-0.6B",
    "messages": [{"role":"user","content":"你好，做个自我介绍"}],
    "stream": false,
    "max_tokens": 32
  }'
```

---

## 4) 注意事项（首次成功率最相关）

1. **兼容性是第一风险**  
   Intel XPU 路径不等同于官方默认 NVIDIA 路径；镜像与设备插件匹配是决定性能否启动的关键。

2. **`gpuType` 不改会调度失败**  
   默认是 `nvidia.com/gpu`，Intel 环境需改为 `gpu.intel.com/xe`（按你集群实际资源键为准）。

3. **HF Token 必须可用**  
   Qwen 从 HF 拉取，token 缺失或权限不足会导致 worker 初始化失败。

4. **先追求“能跑通”再优化**  
   第一轮只做 agg 最小路径，不引入 router/disagg/planner/multinode。

---

## 5) 快速排障清单

### 5.1 Pod Pending（调度不上）

```bash
kubectl describe pod <worker-pod> -n ${NAMESPACE}
kubectl get nodes -o json | jq '.items[].status.allocatable'
```

重点看是否请求了不存在的 GPU 资源键。

### 5.2 Pod CrashLoopBackOff / Init 失败

```bash
kubectl logs <worker-pod> -n ${NAMESPACE} --previous
kubectl logs <frontend-pod> -n ${NAMESPACE}
```

重点看镜像拉取错误、HF 拉模报错、runtime backend 报错。

### 5.3 接口不通

```bash
kubectl get svc -n ${NAMESPACE} | grep frontend
kubectl port-forward svc/vllm-agg-frontend 8000:8000 -n ${NAMESPACE}
curl -v http://localhost:8000/v1/models
```

---

## 6) 你可以直接从这里开始

如果你现在就要执行，建议按下面顺序：

1. 安装平台（Step A）  
2. 建 HF Secret（Step B）  
3. 改 `agg.yaml`（Step C）  
4. `kubectl apply` + 观察 pod（Step D）  
5. `/v1/models` + `chat/completions`（Step E）

只要这 5 步过了，你的“最简单测试”就完成了。
