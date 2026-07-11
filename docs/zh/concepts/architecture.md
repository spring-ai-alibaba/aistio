# 架构

Aistio 将 Kubernetes API 作为期望状态和运行状态的事实来源。控制面协调
自定义资源、Kubernetes 工作负载和 Agent 数据面，但不执行模型推理。

## 组件关系

```{mermaid}
flowchart TB
    U[kubectl / aistioctl / REST 客户端]
    K[Kubernetes API 与 agentscope.io/v1alpha1]
    C[aistiod 控制器]
    H[HTTP 数据面契约]
    G[ASDP gRPC]
    W[Deployment / Service / ConfigMap]
    D[Agent 数据面 Pod]

    U --> K
    U --> C
    K <--> C
    C --> W
    W --> D
    C <--> H
    H <--> D
    C <--> G
    G <--> D
```

REST API 和 `aistioctl` 最终也读写 Kubernetes 自定义资源。Aistio
没有独立数据库，持久状态主要位于 Kubernetes API Server 所使用的 etcd。

## 控制面

`aistiod` 在一个进程内运行以下核心能力：

| 模块 | 职责 |
| --- | --- |
| Agent controller | 为控制面管理的 Agent 协调 ConfigMap、Deployment 和 Service |
| Discovery controller | 发现带管理标签的现有 Deployment，并创建 BYO Agent |
| BYO workload controller | 只读观察被纳管的 Deployment，更新 Agent 状态 |
| ModelConfig controller | 校验 provider、model 和 Secret key，记录短哈希 |
| MCPServer controller | 校验 MCP 配置，并周期发现 Remote MCP 工具 |
| Session controllers | 同步会话快照，处理压缩和终止命令 |
| REST API | 提供 Agent、会话、模型和 MCP 的管理接口 |
| ASDP server | 维护数据面双向 gRPC 流，推送配置并接收状态 |
| Admission Webhook | 对 Agent、AgentTeam、AgentSession、ModelConfig 和 MCPServer 做默认值或补充校验 |

AgentTeam、TeamTask、TeamMessage 和 SandboxBroker 属于实验路径。只有
`--enable-experimental=true` 时，相关控制器和 REST 路由才会注册。

## 数据面

Agent 数据面是实际运行推理和工具调用的应用进程。Aistio 通过两种协议与其
交互：

- HTTP 数据面契约用于信息发现、健康探测、会话查询和会话命令。
- ASDP 用于长连接、配置热更新、ACK/NACK、会话上报、心跳和实验性 Team
  事件。

数据面必须显式实现协议。Aistio 不注入 sidecar，也不透明拦截模型或工具流量。

## 资源协调

控制面管理的声明式 Agent 会拥有以下 Kubernetes 对象：

| 对象 | 用途 |
| --- | --- |
| `<agent>-config` ConfigMap | 保存启动时的 `agent-config.json` |
| `<agent>` Deployment | 运行 Agent 数据面 |
| `<agent>` Service | 暴露数据面 HTTP 端口 |
| AgentSession | 保存从数据面自动同步的会话状态 |

自动同步的 AgentSession 和工作负载子对象通过 owner reference 关联 Agent。
REST `POST /sessions` 创建的 AgentSession 只有 Agent label，但 Agent
finalizer 仍会按 label 清理它们。Kubernetes 垃圾回收器负责其余从属资源。

BYO `workloadRef` 不建立 Deployment 的 owner reference，也不会修改
现有 Deployment。控制面只同步副本、健康和契约信息。

## 配置路径

声明式 Agent 有两条配置路径：

1. 启动配置写入 ConfigMap，并挂载为
   `/app/config/agent-config.json`。
2. Agent、ModelConfig 或 MCPServer 发生变化时，ConfigPushWatcher 通过
   ASDP 推送 Agent、Model、Tool 或 Skill 配置。

ModelConfig 的 ASDP payload 只包含 `ModelConfig.spec`，其中保存 Secret
名称和 key，不包含 Secret 值。当前自动生成的 Agent Pod 也不会挂载
ModelConfig 所引用的 Secret，因此数据面必须另行获得模型凭据。

## 高可用行为

启用 leader election 后，常规 reconcile loop 由 leader 执行。ASDP server
和配置监听器会在每个控制面副本上运行，使每个副本都能服务自己持有的数据面
连接。

v0.2.0 的 Helm Service 只在 `experimental.enabled=true` 时暴露 gRPC
端口。默认 Chart 因此不能通过 Service 访问 ASDP。部署前应阅读
[部署与升级](../operations/deployment.md)。

## 设计边界

- Aistio 负责控制面，不负责模型请求代理。
- 固定副本数由 Agent 资源管理；项目未创建 HPA。
- 控制面创建的工作负载只轮询一个 Ready Pod；BYO `workloadRef` 不进入
  HTTP 会话轮询，多副本会话并非精确聚合。
- `contractPath` 存在于 BYO Schema，但当前 HTTP prober 固定请求
  `/agentscope`。
- API 为 `v1alpha1`，升级前必须检查 CRD 和数据迁移影响。
