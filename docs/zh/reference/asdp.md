# ASDP 协议

Agent Service Discovery Protocol（ASDP，Agent 服务发现协议）是 Aistio
的 gRPC 双向流协议。服务定义位于 `internal/asdp/asdp.proto`，包名为
`agentscope.protocol.v1`。

ASDP 在 v0.2.0 中可以工作，但 protobuf 的 `go_package` 位于
`internal/asdp`。协议和 Connector 公共 API 尚未形成稳定的外部兼容承诺。

## 服务定义

```protobuf
service AgentDataPlaneService {
  rpc Connect(stream Upstream) returns (stream Downstream);
}
```

一个连接复用配置、会话、Team 和心跳消息。默认监听地址为 `:15010`。

## 连接流程

1. 数据面建立 gRPC 双向流。
2. 第一条上行消息必须包含 `ConnectRequest`。
3. `UpstreamMeta` 必须提供 `agent_name`、`instance_id` 和
   `namespace`。
4. 控制面返回 `ConnectResponse`。
5. 连接被接受后，控制面推送当前已缓存的完整配置快照。
6. 后续变更按配置类型增量推送，数据面返回 ACK 或 NACK。

重复的 `namespace/instance_id` 会替换旧连接。连接中断后，Connector 使用
指数退避重连，稳态上限约为 30 秒；当前倍增顺序可能出现一次 32 秒退避。

## 上行消息

| 消息 | 用途 |
| --- | --- |
| `ConnectRequest` | 报告 runtime、SDK 版本、能力和会话亲和策略 |
| `ConfigAck` | 按 version 和 nonce 接受或拒绝配置 |
| `SessionReport` | 批量上报会话、Token 和上下文压力 |
| `TeamEventReport` | 上报实验性 Team 事件 |
| `Heartbeat` | 保持连接活跃 |

`SessionReport` 会写入或更新 AgentSession。Team 事件只有实验控制器开启后
才有完整的接收端。

## 下行消息

| 消息 | 用途 |
| --- | --- |
| `ConnectResponse` | 接受或拒绝握手，并报告控制面版本 |
| `ConfigPush` | 下发带 version 和 nonce 的配置 |
| `SessionCommand` | 下发会话命令 |
| `TeamEvent` | 发送实验性 Team 消息 |
| `Heartbeat` | 心跳回显 |

配置类型包括：

- `CONFIG_TYPE_AGENT`
- `CONFIG_TYPE_TOOL`
- `CONFIG_TYPE_SKILL`
- `CONFIG_TYPE_OVERRIDE`
- `CONFIG_TYPE_MODEL`

控制面按 JSON 序列化 `resources`。相同内容不会重复生成新版本。没有数据面
连接时，最新快照保留在当前 `aistiod` 进程内，实例重连后做全量同步。

协议和 Connector 已实现 `SessionCommand`，但 v0.2.0 的
AgentSession controller 仍通过 HTTP prober 发送压缩和终止命令，没有调用
ASDP Distributor 的会话命令方法。

快照不是 Kubernetes 持久资源。控制面进程重启后，需要由 informer 事件重新
建立内存快照。

## ACK 和 NACK

数据面处理 `ConfigPush` 后应返回：

- 相同的 `config_type`
- 相同的 `version`
- 相同的 `nonce`
- `accepted=true`，或 `accepted=false` 和 `reject_reason`

控制面记录 ACK/NACK 指标和日志，但不会自动回滚 Kubernetes 资源。

## TLS 和 mTLS

`aistiod` 支持：

- 不配置证书时使用明文 gRPC。
- 配置 server cert 和 key 时使用 TLS 1.2 及以上。
- 同时配置 CA 时要求并校验客户端证书。

mTLS 模式还会用客户端证书身份校验 namespace 和 Agent。证书部署参数为：

- `--grpc-tls-cert`
- `--grpc-tls-key`
- `--grpc-tls-ca`

Helm profile 提供 `experimental.grpcTLS` 值，但当前 Deployment 模板没有把
这些值转换为证书挂载和命令行参数。使用前需要自行补齐部署配置。

## Connector 状态

仓库的 `connector` 包实现：

- 握手。
- 自动重连。
- 配置回调与 ACK/NACK。
- 会话命令回调。
- 每 10 秒一次的会话批量上报。
- 可选 TLS 和客户端证书。

但 `connector.Config.OnConfigPush` 和 `UpdateSessions` 暴露了
`internal/asdp` 类型。Go 的 `internal` 导入规则使仓库外 module 难以
使用这些 API。外部数据面应暂时根据 `asdp.proto` 自行生成客户端，或等待
项目导出稳定协议包。

## Helm 部署限制

`aistiod` 默认启用 ASDP，Helm Deployment 也声明 gRPC container port。
但是 Service 模板只在 `experimental.enabled=true` 时暴露 `15010`。

此外，`agentscope-java` 适配器当前注入
`aistiod.aistio-system.svc:15010`，默认 Helm Service 实际为
`aistio-controller.aistio-system.svc:15010`。

因此默认 Chart 不能直接形成 ASDP 链路。使用方必须先修正 Service 暴露和
数据面控制面地址；仅开启 experimental 会同时启用未达到生产状态的 Team 和
Sandbox 控制器。
