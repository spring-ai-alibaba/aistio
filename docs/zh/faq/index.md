# 常见问题

## Aistio 是什么项目

Aistio 是 Kubernetes 原生的 Agent 控制平面。它用 CRD 管理 Agent、模型
配置、MCP 服务和会话，并通过 HTTP 契约与 ASDP 连接数据面。

它不是 Agent 开发 SDK、LLM 网关或透明 Service Mesh。

## 当前版本可以用于生产吗

`v0.2.0` 和 `v1alpha1` 更适合受控 PoC、协议验证和继续开发。默认
ASDP 部署链路、CLI、模型凭据交付、多副本会话、Team 和 Sandbox 仍有明确
缺口。

生产评估前至少应修复这些断链，增加真实 Kubernetes E2E，并制定 CRD
不兼容升级方案。

## 支持哪些 Agent runtime

声明式模式只注册 `agentscope-java`。其他 runtime 可以通过 BYO
`workloadRef` 纳管，但应用必须自行实现数据面契约。

填写其他未注册的 runtime 名称不会自动获得声明式适配。

## Aistio 会代理模型请求吗

不会。`ModelConfig` 只保存 provider、model、options 和 Secret 引用。
模型调用仍由数据面执行。

## 为什么模型 Secret 已更新，状态没有变化

ModelConfig controller 没有 watch Secret。只修改 Secret 不会触发
ModelConfig reconcile。更新 ModelConfig annotation 可以触发重新计算短哈希，
但 Secret 值仍不会通过 ASDP 推送或自动挂载到 Agent Pod。

## Remote MCP 和 Stdio MCP 有什么区别

控制面会连接 Remote MCP，执行 initialize 和 `tools/list`。Stdio 只保存
配置，控制面不会启动进程或发现工具。

## requireApproval 会阻止工具执行吗

不一定。该字段作为配置交给数据面，控制面没有工具调用审批状态机。数据面
必须自行执行审批规则。

## 为什么 aistioctl install 显示成功但集群没有资源

`aistioctl install` 在 v0.2.0 只输出文字。请使用：

```bash
./install/install.sh
```

或直接使用 `helm upgrade --install`。

## 为什么 verify-install 没发现缺少 CRD

`aistioctl verify-install` 的 CRD 和 Deployment 检查仍是空实现。请使用：

```bash
kubectl get crds | grep agentscope.io
kubectl -n aistio-system rollout status deploy/aistio-controller
```

## 为什么 ASDP 无法连接

默认 Chart 的 Service 不暴露 `15010`。此外，Java 适配器注入的
`aistiod.aistio-system.svc` 与默认 Service
`aistio-controller.aistio-system.svc` 不一致。

应先修正 Service 暴露和数据面地址，再检查 TLS、NetworkPolicy 和握手字段。

## 为什么自定义 contractPath 没有生效

v0.2.0 的 HTTP prober 固定请求 `/agentscope`。Schema 虽然包含
`contractPath`，实现尚未使用。数据面当前必须保留标准路径。

## 为什么多副本 Agent 的会话不完整

对于控制面创建的工作负载，HTTP SessionPoller 只查询一个 Ready Pod。其他
实例的会话不会被聚合，命令也不会精确定向。BYO `workloadRef` 被该轮询器
排除，而且命令控制器仍按 Agent 名查找 Deployment。数据面需要提供共享会话
视图或粘性路由。

## BYO 应该选择 image 还是 workloadRef

选择 `workloadRef`。BYO image 的 Java Pod 会挂载一个控制器没有创建的
ConfigMap，当前链路无法可靠启动。

## AgentTeam 已经能自动协作吗

不能这样承诺。控制面有 Team CRD、任务账本、消息 outbox 和部分恢复状态机，
但创建成员 AgentSession 不会真正启动数据面推理循环。

## SandboxClaim 会创建隔离环境吗

不会。默认安装不协调 Claim，状态保持为空；启用实验控制器后，Claim 会保持
`Pending`，`Provisioned=False`，reason 为 `NotImplemented`。

## 文档与源码冲突时以什么为准

依次检查：

1. `api/v1alpha1`
2. `internal/controller`、`internal/httpapi`、`internal/asdp`
3. `helm/aistio`
4. 当前版本文档

API 仍是 alpha。升级后应重新核对源码和生成 CRD。
