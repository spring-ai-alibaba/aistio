# 核心概念

本页说明 Aistio `v0.2.0` 的资源模型和协议边界。所有自定义资源都使用
`agentscope.io/v1alpha1`。

## Agent

`Agent` 是主要管理对象。它包含两种互斥类型：

- `Declarative`：控制面创建并更新 ConfigMap、Deployment 和 Service。
- `BYO`：使用自定义镜像创建工作负载，或通过 `workloadRef` 纳管现有
  Deployment。

当前只有 `agentscope-java` 注册了声明式适配器。Schema 中的 `runtime` 是
开放字符串，但默认启用的 Webhook 会直接拒绝使用未注册 runtime 的声明式或
BYO image Agent。关闭 Webhook 后，控制器会把 `Accepted` 条件设为 `False`，
原因为 `UnsupportedRuntime`。

`status.managementMode` 表示实际管理方式：

- `CP-Managed`：Aistio 创建并更新工作负载。
- `Adopted`：Aistio 只观察外部 Deployment。

## ModelConfig

`ModelConfig` 描述模型提供方、模型标识、选项和凭据引用。provider 可选值为：

- `DashScope`
- `OpenAI`
- `Anthropic`
- `Gemini`
- `Ollama`
- `Moonshot`
- `Custom`

控制器检查引用的 Secret 和 key，并在状态中保存短哈希。Aistio 不调用模型
提供方，也不代理模型请求。

## MCPServer

`MCPServer` 注册 Model Context Protocol（MCP）服务：

- `Remote` 支持 `STREAMABLE_HTTP` 和 `SSE` 配置。控制面通过
  JSON-RPC 执行 initialize 和 `tools/list`。
- `Stdio` 可以保存 command、args 和 env，但控制面不会启动进程或发现工具。

`status.discoveredTools` 只记录名称和描述。Agent 上的 `toolNames` 和
`requireApproval` 会作为配置交给数据面，控制面本身不执行工具调用审批。

## AgentSession

`AgentSession` 保存数据面报告的会话快照。状态阶段包括：

- `Active`
- `Idle`
- `Compressing`
- `Terminated`

快照可以包含消息数、Token 用量、上下文压力和任务状态。压缩或终止命令写入
`spec.commands`，随后由控制器发送给数据面。

## 数据面契约级别

HTTP 契约按能力分为 3 级：

| 级别 | 要求 |
| --- | --- |
| Level 1 | `GET /agentscope/info` 和 `GET /agentscope/health` |
| Level 2 | Level 1，加会话列表和会话状态 |
| Level 3 | Level 2，加会话压缩和终止命令 |

对于控制面创建且 Deployment 与 Agent 同名的工作负载，控制面每 15 秒轮询
Level 2 及以上的数据面；BYO `workloadRef` 不进入该 HTTP 轮询路径。Level 3
的命令仍由数据面决定如何实现。

## ASDP

Agent Service Discovery Protocol（ASDP，Agent 服务发现协议）是 Aistio
的双向 gRPC 协议。数据面先发送握手，之后可以：

- 接收 Agent、Tool、Skill、Override 和 Model 配置。
- 返回配置 ACK 或 NACK。
- 上报会话快照、Team 事件和心跳。
- 接收会话命令、Team 事件和心跳。

ASDP 默认在 `aistiod` 进程中启用，但 Chart 的 Service 暴露存在
v0.2.0 限制。详见 [ASDP 参考](../reference/asdp.md)。

## 实验性资源

`AgentTeam`、`TeamTask` 和 `TeamMessage` 组成实验性的协作状态模型。
控制面能够创建会话资源、维护任务账本和传递部分 Team 事件，但不会保证
Agent 数据面真正启动成员推理循环。

默认安装不注册 SandboxBroker，`SandboxClaim.status` 因而保持为空。启用实验
控制器后，Claim 只进入 `Pending`；其 `Provisioned` 条件会写为 `False`，
原因为 `NotImplemented`。

## 状态条件

资源通过 `status.conditions` 表达控制器判断。常见类型包括：

- `Accepted`：Schema 和控制器是否接受配置。
- `Ready`：工作负载或会话是否就绪。
- `DataPlaneConnected`：HTTP 数据面探测是否成功。
- `Discovered`：MCP 工具发现是否成功。
- `Provisioned`：Sandbox 是否完成供应。

排查问题时，应同时检查 `status.conditions[].reason`、Kubernetes Event
和 `aistiod` 日志。

`allowedNamespaces`、自定义健康探针等部分字段尚未接入控制器执行路径。
判断能力时应以 reconcile 代码为准，不能只依据 CRD 中存在字段。
