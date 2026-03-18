# Dynamo XPU 镜像构建指南

本文档记录了在代理环境下构建 Dynamo XPU 镜像的完整步骤。

## 📋 前置条件

### 1. 系统要求
- Docker 已安装并运行
- Python 3.10+
- 至少 100GB 磁盘空间（镜像大小约 28GB）
- 网络可访问代理服务器

### 2. 代理环境配置
确保以下环境变量已设置：

```bash
# 检查代理设置
env | grep -i proxy

# 应该输出类似：
# http_proxy=http://proxy-dmz.intel.com:912
# https_proxy=http://proxy-dmz.intel.com:912
# no_proxy=127.0.0.1,localhost,10.239.14.46
```

## 🚀 构建步骤

### 步骤 1: 准备源码

```bash
# 进入 Dynamo 源码目录
cd /path/to/dynamo

# 安装渲染依赖
python3 -m pip install --user jinja2 pyyaml
```

### 步骤 2: 渲染 Dockerfile

```bash
# 生成 XPU 版本的 Dockerfile
python3 container/render.py \
  --framework vllm \
  --device xpu \
  --target runtime \
  --output-short-filename
```

这将在 `container/rendered.Dockerfile` 生成 Dockerfile。

### 步骤 3: 修改 Dockerfile 添加代理支持

**关键步骤**：由于 Docker Build 的沙箱特性，需要在 Dockerfile 中显式添加代理配置。

在 `container/rendered.Dockerfile` 中进行以下修改：

#### 3.1 在文件开头（第 10 行后）添加全局 ARG：

```dockerfile
# Proxy settings for build environment
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG NO_PROXY
ARG http_proxy
ARG https_proxy
ARG no_proxy
```

#### 3.2 在每个 FROM 阶段后添加 ENV：

为每个 FROM 指令添加代理环境变量。例如：

```dockerfile
FROM ${BASE_IMAGE}:${BASE_IMAGE_TAG} AS dynamo_base

# Proxy settings for this stage
ENV http_proxy=http://proxy-dmz.intel.com:912
ENV https_proxy=http://proxy-dmz.intel.com:912
ENV no_proxy=localhost,127.0.0.1

# ... 后续指令
```

**注意**：需要对 Dockerfile 中的**每个 FROM 阶段**都添加这些 ENV 设置。

#### 3.3 移除可能导致超时的 PPA 添加（可选）

如果遇到 `add-apt-repository` 超时，可以移除 Launchpad PPA：

```dockerfile
# 原命令：
# RUN wget -O- https://apt.repos.intel.com/intel-gpg-keys/GPG-PUB-KEY-INTEL-SW-PRODUCTS.PUB | gpg --dearmor ... && \
#     add-apt-repository -y ppa:kobuk-team/intel-graphics

# 修改为（移除 add-apt-repository）：
RUN wget -O- https://apt.repos.intel.com/intel-gpg-keys/GPG-PUB-KEY-INTEL-SW-PRODUCTS.PUB | gpg --dearmor ...
```

### 步骤 4: 执行构建

使用以下命令执行构建：

```bash
cd /path/to/dynamo

docker build --network=host \
  -f container/rendered.Dockerfile \
  -t dynamo:vllm-xpu-base-proxyfix \
  --build-arg VLLM_REF=v0.14.0 \
  --build-arg HTTP_PROXY=$HTTP_PROXY \
  --build-arg HTTPS_PROXY=$HTTPS_PROXY \
  --build-arg NO_PROXY=$NO_PROXY \
  --build-arg http_proxy=$http_proxy \
  --build-arg https_proxy=$https_proxy \
  --build-arg no_proxy=$no_proxy \
  .
```

**参数说明**：
- `--network=host`: 使用宿主机网络
- `-t dynamo:vllm-xpu-base-proxyfix`: 镜像标签
- `--build-arg VLLM_REF=v0.14.0`: vLLM 版本
- 代理相关的 `--build-arg`: 传递代理环境变量

**构建时间**：约 18-30 分钟（取决于网络速度）

## ✅ 验证构建

### 1. 检查镜像

```bash
docker images | grep dynamo
```

预期输出：
```
dynamo:vllm-xpu-base-proxyfix   772de859d06b       28.6GB             0B
```

### 2. 测试 Python 导入

```bash
docker run --rm dynamo:vllm-xpu-base-proxyfix \
  python3 -c "import dynamo; import vllm; print('✅ IMPORT SUCCESS')"
```

### 3. 测试入口点

```bash
docker run --rm --entrypoint python3 \
  dynamo:vllm-xpu-base-proxyfix \
  -m dynamo.vllm --help 2>&1 | head -20
```

## 📦 导出镜像

构建成功后，导出镜像以便在其他机器使用：

```bash
# 导出镜像
docker save -o /tmp/dynamo-vllm-xpu-base-proxyfix.tar \
  dynamo:vllm-xpu-base-proxyfix

# 压缩（可选，可减小 30-50% 体积）
gzip /tmp/dynamo-vllm-xpu-base-proxyfix.tar

# 传输到目标机器
scp /tmp/dynamo-vllm-xpu-base-proxyfix.tar.gz \
  user@target-host:/tmp/

# 在目标机器导入
ssh user@target-host "docker load -i /tmp/dynamo-vllm-xpu-base-proxyfix.tar.gz"
```

## 🔧 常见问题

### 1. 构建过程中出现网络超时

**症状**：`dial tcp 52.x.x.x:443: i/o timeout`

**解决方案**：
- 确保 Dockerfile 中已添加代理 ENV
- 检查宿主机的代理环境变量是否正确设置
- 使用 `docker build --network=host` 参数

### 2. `add-apt-repository` 超时

**症状**：Launchpad PPA 添加时超时

**解决方案**：从 Dockerfile 中移除 `add-apt-repository -y ppa:kobuk-team/intel-graphics` 行

### 3. 镜像构建成功但无法运行

**症状**：容器启动后立即退出

**排查步骤**：
```bash
# 检查容器日志
docker logs <container-id>

# 交互式运行容器进行调试
docker run -it --rm dynamo:vllm-xpu-base-proxyfix bash
```

### 4. 磁盘空间不足

**症状**：构建过程中出现 `no space left on device`

**解决方案**：
```bash
# 清理 Docker 缓存
docker system prune -a

# 检查磁盘空间
df -h

# 清理旧镜像
docker image prune
```

## 📚 参考文档

- [Dynamo Container README](container/README.md)
- [vLLM XPU Build Guide](docs/backends/vllm/README.md)
- [Intel oneAPI Installation Guide](https://www.intel.com/content/www/us/en/developer/tools/oneapi/base-toolkit-download.html)

---

**文档版本**: v1.0  
**最后更新**: 2026-03-05  
**维护者**: Dynamo Build Team
