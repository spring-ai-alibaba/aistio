# Aistio

Aistio 是一个 Kubernetes 原生的 AI Agent 控制平面。它使用
`agentscope.io/v1alpha1` 自定义资源描述 Agent、模型配置、MCP 服务和会话，
并由 `aistiod` 将期望状态协调为 Kubernetes 工作负载。

当前文档对应 Aistio `v0.2.0`。API 仍处于 `v1alpha1` 阶段，资源字段和行为
可能发生不兼容变更。

## 项目定位

Aistio 管理 Agent 的部署配置、运行状态和数据面连接。模型推理、提示词执行和
工具调用仍由 Agent 数据面进程负责。

Aistio 不是以下组件：

- 不是用于编写推理循环的 Agent SDK。
- 不是代理模型请求的 LLM 网关。
- 不是带透明流量劫持或 sidecar 代理的 Service Mesh。
- 不是 Sandbox 运行时。当前 Sandbox 供应尚未实现。

## 当前能力

| 能力 | v0.2.0 状态 |
| --- | --- |
| 声明式 Agent | 可创建 ConfigMap、Deployment 和 Service；仅注册了 `agentscope-java` 适配器 |
| BYO 工作负载 | 可发现或纳管现有 Deployment，并以只读方式同步副本和健康状态 |
| 模型配置 | 可校验 `ModelConfig` 和 Secret 引用；不代理模型请求 |
| MCP 注册 | 可发现 Remote MCP 服务的工具；Stdio 不执行控制面工具发现 |
| 会话管理 | 可同步会话快照，并在契约级别满足要求时下发压缩或终止命令 |
| ASDP | 提供双向 gRPC 流、配置 ACK/NACK、会话上报和心跳 |
| AgentTeam | 实验性控制面状态机，尚未形成完整的 Agent 执行闭环 |
| SandboxClaim | 默认不协调；启用实验控制器后仅接受请求并保持 `Pending`，不创建真实 Sandbox |

## 主要组件

- `aistiod`：运行 Kubernetes 控制器、REST API、数据面探测、Webhook 和
  Agent Service Discovery Protocol（ASDP，Agent 服务发现协议）gRPC 服务。
- `aistioctl`：调用 REST API 的开发辅助 CLI。部分安装、校验和实验性命令
  在 v0.2.0 中尚不完整。
- `connector`：连接 ASDP 的 Go 客户端实现。其公共 API 当前仍引用仓库
  `internal/asdp` 类型，不应视为稳定的外部 SDK。
- Helm Chart：位于 `helm/aistio`，Chart 和应用版本均为 `0.2.0`。

## 自定义资源

Aistio 注册以下 8 种资源：

- `Agent`
- `ModelConfig`
- `MCPServer`
- `AgentSession`
- `AgentTeam`
- `TeamTask`
- `TeamMessage`
- `SandboxClaim`

前 4 种资源属于核心路径。Team、Task、Message 和 Sandbox 相关控制器只在
实验功能开启后注册。

## 阅读路径

首次使用时，按以下顺序阅读：

1. [安装 Aistio](getting-started/installation.md)
2. [理解架构](concepts/architecture.md)
3. [了解核心概念](concepts/key-concepts.md)
4. [管理声明式 Agent](guides/agents.md) 或 [纳管现有工作负载](guides/byo.md)
5. [配置模型和 MCP](guides/model-and-mcp.md)
6. [观测与控制会话](guides/sessions.md)

接口和运维资料位于 [参考文档](reference/data-plane-contract.md) 与
[运维指南](operations/deployment.md)。

## 事实来源

项目行为以当前仓库源码为准：

- API 定义：`api/v1alpha1`
- 控制器：`internal/controller`
- 数据面适配器：`internal/adapter`
- REST API：`internal/httpapi`
- ASDP：`internal/asdp`
- 部署配置：`helm/aistio`

项目仓库为
[spring-ai-alibaba/aistio](https://github.com/spring-ai-alibaba/aistio)。
