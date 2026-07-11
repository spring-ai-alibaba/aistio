# Aistio

Aistio 是面向 AI Agent 工作负载的 Kubernetes 原生控制平面。它通过自定义资源（CRD）管理 Agent、模型配置、MCP 服务、会话和实验性团队协作，并通过 HTTP 数据面契约与 ASDP gRPC 协议连接 Agent 运行时。

当前版本为 `0.2.0`，API 版本为 `agentscope.io/v1alpha1`。项目仍处于技术预览阶段，不应直接视为生产级 Agent Service Mesh。

## 项目边界

Aistio 负责控制面能力：

- 根据 `Agent` 资源创建或纳管 Kubernetes 工作负载。
- 记录模型和 MCP 服务配置。
- 探测数据面健康状态并同步会话摘要。
- 通过 ASDP 推送配置并接收状态上报。
- 暴露 Kubernetes API、REST API 和 `aistioctl` 命令行入口。

Aistio 不执行以下工作：

- 不直接调用大语言模型，也不是模型推理网关。
- 不执行 Agent 的推理循环或工具调用。
- 不提供透明 sidecar 流量代理。
- 不替代 Agent 运行时。数据面仍需实现 HTTP 契约或 ASDP 协议。

## 能力状态

| 能力 | 当前状态 | 说明 |
| --- | --- | --- |
| 声明式 Agent | 可用范围受限 | 控制面可创建 ConfigMap、Deployment 和 Service；内置适配器仅支持 `agentscope-java` |
| BYO 工作负载 | 已实现 | 可通过标签或 REST API 纳管现有 Deployment，并同步副本和健康状态 |
| 固定副本调整 | 已实现 | 修改 `spec.declarative.replicas` 或 BYO image 的 `spec.byo.replicas` 后更新 Deployment；尚未实现 HPA 自动扩缩容 |
| 数据面 HTTP 契约 | 已实现 | 支持健康、元数据、会话观测及手动压缩/终止命令 |
| ModelConfig | 部分实现 | 可校验 Secret 引用并下发配置；本项目不代理模型请求 |
| Remote MCP | 部分实现 | 支持 Streamable HTTP POST，并可解析 JSON 或 SSE 响应；传统 SSE transport 和 Stdio 工具发现尚未实现 |
| ASDP | 技术预览 | 协议、连接、配置 ACK/NACK 和会话上报已有实现；部署集成仍需完善 |
| AgentTeam | 实验性 | 任务、消息和状态机已有实现，真实多 Agent 执行闭环尚未完成 |
| Sandbox | 未实现 | 默认不协调 `SandboxClaim`；启用实验控制器后也只会停留在 `Pending` |

## 架构

```text
kubectl / aistioctl / REST API
                │
                ▼
      agentscope.io/v1alpha1 CRD
                │
                ▼
             aistiod
  ┌─────────────┼────────────────────┐
  │             │                    │
  ▼             ▼                    ▼
Kubernetes      HTTP 数据面契约       ASDP gRPC
资源协调        健康与会话探测         配置与状态
  │             │                    │
  └─────────────┴──────────┬─────────┘
                           ▼
                     Agent 数据面 Pod
```

控制面状态保存在 Kubernetes API 中。Agent 数据面负责模型调用、工具执行和业务逻辑。

## 自定义资源

| 资源 | 用途 |
| --- | --- |
| `Agent` | 声明或纳管 Agent 工作负载 |
| `ModelConfig` | 保存模型提供商、模型名称和 Secret 引用 |
| `MCPServer` | 注册 Remote 或 Stdio MCP 服务 |
| `AgentSession` | 记录会话状态、Token 用量和控制命令 |
| `AgentTeam` | 定义实验性多 Agent 团队 |
| `TeamTask` | 保存团队任务 |
| `TeamMessage` | 保存团队消息投递记录 |
| `SandboxClaim` | 描述实验性沙箱申请；当前尚未完成供应 |

## 环境要求

- Go 1.26.5 或更高版本
- Kubernetes 1.28 或更高版本
- Helm 3
- kubectl

## 安装控制面

使用 Helm 安装：

```bash
helm upgrade --install aistio ./helm/aistio \
  --namespace aistio-system \
  --create-namespace
```

检查控制面：

```bash
kubectl -n aistio-system rollout status deployment/aistio-controller
kubectl get crds | grep agentscope.io
kubectl -n aistio-system port-forward service/aistio-controller 8080:8080
curl http://127.0.0.1:8080/api/v1/version
```

当前 Chart 的 ASDP、Team 和 Sandbox 集成仍处于技术预览阶段。使用这些能力前，请先阅读[部署与配置](docs/zh/operations/deployment.md)和[实验性能力](docs/zh/experimental/teams-and-sandbox.md)。

## 创建 Agent

先创建模型凭据：

```bash
kubectl create secret generic dashscope-credentials \
  --from-literal=api-key='<your-api-key>'
```

创建模型配置：

```yaml
apiVersion: agentscope.io/v1alpha1
kind: ModelConfig
metadata:
  name: qwen-max
spec:
  provider: DashScope
  model: qwen-max
  apiKeySecret: dashscope-credentials
  apiKeySecretKey: api-key
```

创建声明式 Agent：

```yaml
apiVersion: agentscope.io/v1alpha1
kind: Agent
metadata:
  name: customer-support
spec:
  type: Declarative
  runtime: agentscope-java
  declarative:
    agentConfig:
      systemMessage: "你是一个客户支持助手。"
      modelConfigRef: qwen-max
      maxTurns: 50
    replicas: 1
```

应用资源：

```bash
kubectl apply -f model.yaml
kubectl apply -f agent.yaml
kubectl get agents
```

## 纳管现有工作负载

为现有 Deployment 添加标签：

```bash
kubectl label deployment my-agent agentscope.io/managed=true
```

控制面会创建对应的 BYO `Agent` 资源。数据面至少需要实现：

- `GET /agentscope/info`
- `GET /agentscope/health`

完整契约见[数据面 HTTP 契约](docs/zh/reference/data-plane-contract.md)。

## 文档

- [Aistio 文档首页](docs/zh/intro.md)
- [安装指南](docs/zh/getting-started/installation.md)
- [架构](docs/zh/concepts/architecture.md)
- [Agent 与 BYO](docs/zh/guides/agents.md)
- [模型与 MCP](docs/zh/guides/model-and-mcp.md)
- [ASDP 协议](docs/zh/reference/asdp.md)
- [CLI 参考](docs/zh/reference/cli.md)
- [REST API 参考](docs/zh/reference/rest-api.md)
- [部署与运维](docs/zh/operations/deployment.md)

## 本地开发

```bash
make build
make vet
make test
make helm-lint
```

envtest 集成测试需要额外的 Kubernetes 测试二进制：

```bash
make test-integration
```

贡献约定见 [CONTRIBUTING_zh.md](CONTRIBUTING_zh.md)。

## 许可证

Aistio 使用 [Apache License 2.0](LICENSE)。
