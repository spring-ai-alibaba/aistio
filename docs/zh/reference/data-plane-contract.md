# HTTP 数据面契约

HTTP 数据面契约定义 `aistiod` 如何探测 Agent 应用。控制面直接访问 Pod IP，
默认端口为 `8080`，所有当前实现的路径都位于 `/agentscope`。

## 契约级别

| 级别 | 必需端点 | 控制面用途 |
| --- | --- | --- |
| Level 1 | `GET /agentscope/info`、`GET /agentscope/health` | 发现和健康检查 |
| Level 2 | Level 1，加会话列表和状态 | 会话观测 |
| Level 3 | Level 2，加压缩和终止 | 会话控制 |

`/info` 返回的 `contractLevel` 是数据面对自身能力的声明。控制面不会在握手
阶段逐个验证所有高等级端点。

## GET /agentscope/info

返回 Agent 元数据。状态码必须为 `200`，响应体为 JSON：

```json
{
  "name": "customer-support-agent",
  "displayName": "客服助手",
  "description": "处理客户咨询",
  "runtime": "agentscope-java",
  "version": "1.0.0",
  "sdkVersion": "1.0.0",
  "contractLevel": 3,
  "capabilities": [
    "session-reporting",
    "context-compression"
  ],
  "port": 8080,
  "sessionAffinity": "none",
  "agentConfig": {
    "modelProvider": "DashScope",
    "model": "qwen-max",
    "tools": [
      "search_docs"
    ],
    "maxTurns": 50
  }
}
```

字段说明：

| 字段 | 必需 | 说明 |
| --- | --- | --- |
| `name` | 建议 | 数据面报告的 Agent 名称 |
| `runtime` | 是 | runtime 标识 |
| `contractLevel` | 是 | `1`、`2` 或 `3` |
| `port` | 否 | 自动发现创建 BYO Agent 时使用的端口 |
| `capabilities` | 否 | 数据面声明的附加能力 |
| `agentConfig` | 否 | 模型、工具和最大轮次摘要 |

控制面会把主要字段写入 `Agent.status.dataPlaneInfo`。

## GET /agentscope/health

健康时返回 `200`。当前 prober 不解析响应体，其他状态码均视为不健康。

```http
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ok"}
```

## GET /agentscope/sessions

Level 2 端点返回当前实例的会话快照：

```json
{
  "sessions": [
    {
      "id": "session-123",
      "phase": "Active",
      "startedAt": "2026-07-11T08:00:00Z",
      "lastActiveAt": "2026-07-11T08:02:00Z",
      "messageCount": 12,
      "tokenUsage": {
        "promptTokens": 2400,
        "completionTokens": 560
      },
      "contextPressure": 0.42,
      "taskSummary": {
        "total": 3,
        "pending": 1,
        "inProgress": 1,
        "completed": 1
      }
    }
  ]
}
```

`phase` 应使用 `Active`、`Idle`、`Compressing` 或 `Terminated`。
对于控制面创建且 Deployment 与 Agent 同名的工作负载，HTTP
SessionPoller 每 15 秒调用一次该端点。BYO `workloadRef` 不进入该轮询路径。

## GET /agentscope/sessions/{id}/state

Level 2 端点返回详细状态：

```json
{
  "sessionId": "session-123",
  "summary": "正在处理退款请求",
  "currentIter": 4,
  "contextPressure": {
    "usedTokens": 4200,
    "maxTokens": 10000,
    "ratio": 0.42
  },
  "tasks": [
    {
      "id": "lookup-order",
      "subject": "查询订单",
      "state": "completed"
    }
  ]
}
```

prober 已实现该客户端方法，但 v0.2.0 的生产控制器没有调用它。控制面 REST
`/state` 接口读取的是 `AgentSession.status.state`，不会透传请求到本端点。

## POST /agentscope/sessions/{id}/compress

Level 3 端点触发上下文压缩。成功接收命令时返回 `200`。当前控制面不发送
请求体。

```http
POST /agentscope/sessions/session-123/compress HTTP/1.1
```

数据面负责压缩算法、持久化和错误恢复。控制面把 HTTP `200` 解释为命令
已派发，不验证压缩结果。

## POST /agentscope/sessions/{id}/terminate

Level 3 端点终止会话。成功接收命令时返回 `200`，请求体为空：

```http
POST /agentscope/sessions/session-123/terminate HTTP/1.1
```

数据面负责停止推理、取消工具调用和释放资源。

## 超时与错误处理

HTTP client 的总超时为 5 秒。非 `200` 状态会被视为错误。健康探测发生网络
错误时返回“不健康”，而信息、会话和命令请求会把错误记录到日志或 Event。

## 当前路径限制

`BYOSpec.contractPath` 和 `agentscope.io/contract-path` 已出现在 API 或
控制器常量中，但 HTTP prober 仍固定拼接 `/agentscope`。数据面应在
v0.2.0 中保留上述标准路径。

## 多副本要求

进入 HTTP SessionPoller 的工作负载只会选择一个 Ready Pod。多副本数据面如
需完整会话视图，应让任意实例都能返回全局会话，或者提供外部共享路由。否则
其他实例的会话可能缺失或被标记为终止。BYO `workloadRef` 当前不使用该
轮询器。
