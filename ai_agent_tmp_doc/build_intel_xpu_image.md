# 在网络正常机器构建 Dynamo XPU 镜像指南

> 目标：在一台外网可访问的机器上，完整构建 `rendered.Dockerfile` 的 XPU 镜像，然后导入到当前 K8s 集群使用。

## 1. 前提

- 机器可访问：`github.com`、`apt.repos.intel.com`、容器镜像仓库
- 已安装：`docker`、`python3`、`pip`
- 源码目录：`/path/to/dynamo`

## 2. 准备源码与渲染 Dockerfile

```bash
cd /path/to/dynamo
python3 -m pip install --user jinja2 pyyaml
python3 container/render.py --framework vllm --device xpu --target runtime --output-short-filename
```

如需本地 key（可选，建议保留）：
- 确保仓库根目录有 `GPG-PUB-KEY-INTEL-SW-PRODUCTS.PUB`

## 3. 完整构建镜像（推荐命令）

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
  --build-arg ftp_proxy=$ftp_proxy \
  .
```

> 无代理环境可以去掉 proxy build-arg。

## 4. 本地验收（至少做这 3 项）

```bash
# A) 镜像存在
docker images | grep dynamo | grep vllm-xpu-base-proxyfix

# B) Python 导入
docker run --rm --entrypoint python3 dynamo:vllm-xpu-base-proxyfix -c "import dynamo, dynamo.vllm, vllm; print('IMPORT_OK')"

# C) 入口可执行（仅检查命令能拉起参数解析）
docker run --rm --entrypoint python3 dynamo:vllm-xpu-base-proxyfix -m dynamo.vllm --help >/dev/null && echo ENTRYPOINT_OK
```

## 5. 导出镜像并拷贝到集群机器

```bash
docker save -o /tmp/dynamo-vllm-xpu-base-proxyfix.tar dynamo:vllm-xpu-base-proxyfix
# 然后通过 scp/rsync 等方式传到集群节点
```

## 6. 在集群节点导入（containerd / k8s.io）

```bash
nerdctl -n k8s.io load -i /tmp/dynamo-vllm-xpu-base-proxyfix.tar
nerdctl -n k8s.io images | grep dynamo | grep vllm-xpu-base-proxyfix
```

## 7. 替换当前 DRA decode worker 镜像

```bash
kubectl -n dynamo-system patch dynamographdeployment vllm-agg-xpu-dra --type='merge' -p '{
  "spec":{
    "services":{
      "VllmDecodeWorker":{
        "extraPodSpec":{
          "mainContainer":{
            "image":"dynamo:vllm-xpu-base-proxyfix"
          }
        }
      }
    }
  }
}'
```

## 8. 验证替换结果

```bash
kubectl -n dynamo-system get pods -l nvidia.com/selector=vllm-agg-xpu-dra-vllmdecodeworker -o wide
kubectl -n dynamo-system logs deploy/vllm-agg-xpu-dra-vllmdecodeworker --tail=200
kubectl -n dynamo-system get dgd vllm-agg-xpu-dra
```

若 worker Ready，再继续：

```bash
kubectl -n dynamo-system port-forward svc/vllm-agg-xpu-dra-frontend 8000:8000
curl http://127.0.0.1:8000/v1/models
```
