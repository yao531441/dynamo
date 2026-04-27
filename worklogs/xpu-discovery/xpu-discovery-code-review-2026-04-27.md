XPU Discovery Code Review — 2026-04-27

概述
-----
对今日新增的 XPU-discovery 相关修改（实现 Intel XPU b60 支持）进行了代码审查，参考 worklogs/xpu-discovery 的测试与分析记录。审查覆盖 operator discovery、Intel xpu-smi exporter、profiler DGD 生成与 pydantic 类型、CRD 更新与示例 YAML。

关键结论（高层）
----------------
- b60 已端到端新增：CRD、operator 发现逻辑、exporter 指标、profiler pydantic 类型均已包含 b60。总体实现思路合理，遵循现有发现→推断→注入 的流程。
- 存在若干需尽快修复的问题：exporter 鲁棒性、DGD 生成未将 b60 转换为 Intel allocation、节点去重计数缺陷、SKU 推断映射过于宽泛。
- 本地验证受限：Python 依赖与 controller envtest 二进制缺失导致若干集成/端到端测试被跳过或失败，运维层面仍需真实带 XPU 的集群验证。

主要发现与建议（按优先级）
----------------
1) 高优先级
- Profiler: 生成 DGD 时仅注入 vLLM 环境变量/kv 配置，但没有把 DGD 的资源指向 Intel 分配模型（resourceClaims/ResourceClaimTemplateName）。结果：即便检测到 b60，最终 Pod 可能仍被调度到默认 NVIDIA 设备类。建议将 DGD 生成逻辑改为输出与 examples/backends/vllm/*_xpu_dra.yaml 相同的 Intel Allocation 结构，并增加单元测试覆盖。
- Exporter: deploy/observability/xpu_smi_exporter.py 在 __init__ 时做一次 discovery，后续 collect 依赖首次发现结果，可能遇到 xpumd 启动竞态或主机绑定问题导致内存总量为 0 或错误。建议：在 collect() 中定期/按需刷新 discovery，增加重试与超时容错，优先尝试官方 xpumanager 容器作为替代以减少 host-mount surface。

2) 中等优先级
- Operator: deploy/operator/internal/gpu/discovery.go 的节点计数逻辑在统计 discoverPrometheusPods 结果时未做 Node 去重（pod-基数统计可能重复计数同一节点）。建议先基于 pod spec 的 spec.nodeName 做唯一化再统计 NodeWithGPUs 与 totalGPUs。
- SKU 推断映射: 目前 gpuRules 中直接以 token E211 → b60 映射，判定较为宽泛。建议维护一张明确的 Intel PCI-ID -> SKU 表，并结合 VRAM/设备数阈值做二次确认（可通过 feature-flag 控制开关以便回滚）。

3) 低优先级 / 可选改进
- Exporter 容器化：调研使用官方 xpumanager/xpu-exporter 镜像做 metrics 源的 PoC，减少 host 依赖并提升长期维护性。
- 文档与 CRD 发布流程：CRD 更新已在 config/crd 与 helm chart 中同步，建议在 release note 中明确 CRD 升级步骤与兼容性影响。

测试与验证
-------------
- 本地 Go 单元测试（deploy/operator 内的 gpu 包）通过。controller integration tests 在本地因 envtest/etcd 缺失失败或被跳过。Python 单元测试受 dynamo.planner 依赖缺失，多数被跳过。
- 需要在具备 Intel XPU 的测试集群上做 E2E 验证，重点验证：发现流程（exporter -> operator → SKU 推断）、DGD 生成并被调度为 Intel allocation、vLLM 在 b60 上的运行行为。

遗留问题调研评估
------------------
- 已列出遗留问题并给出修复建议（上文）。调研方向与方法合理：结合工作日志的手动部署命令、示例 YAML 与官方 xpumanager 文档来验证 metric schema 与分配模型。
- 若要推进，建议优先落实 DGD 生成修正与 exporter 鲁棒性改进，然后在 CI 或私有测试集群上补充回归测试。

下一步建议（可行动项）
---------------------
1. PR：实现 DGD -> Intel allocation 的转换（参考 examples/backends/vllm/*_xpu_dra.yaml），并添加单元测试验证生成的 DGD 结构。
2. PR：改进 xpu_smi_exporter.py 的 discovery 重试/刷新逻辑，或替换为使用官方 xpumanager 容器并提供兼容层。
3. PR：修复 discoverPrometheusPods 的节点去重计数逻辑，增加回归测试。
4. 运营：在带 Intel XPU 的测试集群上做 E2E 测试并记录步骤到 worklogs/xpu-discovery。

参考文件
---------
- deploy/operator/internal/gpu/discovery.go (+ discovery_test.go)
- deploy/observability/xpu_smi_exporter.py
- components/src/dynamo/profiler/utils/dgd_generation.py
- components/src/dynamo/profiler/utils/dgdr_v1beta1_types.py
- deploy/operator/config/crd/bases/nvidia.com_dynamographdeploymentrequests.yaml
- examples/backends/vllm/deploy/disagg_xpu_dra.yaml
- worklogs/xpu-discovery/xpu-phase1-test-log-2026-04-27.md

审查者备注
----------
本次审查结合代码与工作日志进行，因本地环境限制（缺少特定二进制与依赖），部分验证属于静态审查与推理。建议尽快在具备 Intel XPU 的环境中做端到端验证。

— 结束 —
