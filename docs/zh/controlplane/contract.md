# 数据面契约一致性文档

本文档定义控制面与数据面之间的 HTTP 契约 API。无论 Agent 采用 Declarative 还是 BYO 部署模式，数据面都需要实现一套标准的 HTTP 接口，控制面通过这些接口与数据面交互。这类似于 Istio 要求 Envoy 实现 xDS 协议。

契约分为三个等级（Contract Level），数据面按自身能力实现其中之一。

---

## 契约等级（Contract Level）

### Level 1 -- 最小可纳管

实现 Level 1 即可被控制面发现与纳管。

| 端点 | 方法 | 说明 |
|------|------|------|
| `/agentscope/info` | GET | 返回 agent 元数据 |
| `/agentscope/health` | GET | 健康检查，返回 200 表示健康 |

#### `GET /agentscope/info`

控制面发现数据面后调用的第一个接口。返回 agent 元数据，控制面据此填充 Agent CRD 的 `status.dataPlaneInfo`。

**请求：**

```
GET /agentscope/info HTTP/1.1
Host: <agent-pod-ip>:8080
```

**响应（200 OK）：**

```json
{
  "name": "customer-support-agent",
  "displayName": "客服助手",
  "description": "处理客户咨询的智能体",
  "runtime": "agentscope-java",
  "version": "1.2.0",
  "sdkVersion": "0.8.0",
  "contractLevel": 3,
  "capabilities": [
    "session-reporting",
    "hot-reload",
    "context-compression",
    "sandbox-request"
  ],
  "agentConfig": {
    "modelProvider": "DashScope",
    "model": "qwen-max",
    "tools": ["search_docs", "get_faq", "create_ticket"],
    "maxTurns": 50
  },
  "port": 8080
}
```

**字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | agent 标识名称 |
| `displayName` | string | 否 | 显示名称 |
| `description` | string | 否 | 描述信息 |
| `runtime` | string | 是 | 运行时类型：`agentscope-java` / `agentscope-go` / `langchain` / `custom` |
| `version` | string | 否 | 数据面应用版本 |
| `sdkVersion` | string | 否 | AgentScope SDK 版本 |
| `contractLevel` | int32 | 是 | 实现的契约等级（1/2/3） |
| `capabilities` | []string | 否 | 数据面声明支持的能力列表 |
| `agentConfig` | object | 否 | BYO 模式下数据面自报的 agent 配置 |
| `port` | int32 | 否 | 服务端口，默认 8080 |

#### `GET /agentscope/health`

健康检查端点，控制面定期轮询以判断数据面是否健康。

**请求：**

```
GET /agentscope/health HTTP/1.1
Host: <agent-pod-ip>:8080
```

**响应：**

- `200 OK` -- 数据面健康
- 非 200 或连接失败 -- 数据面不健康

---

### Level 2 -- 会话观测

在 Level 1 基础上增加会话查询能力，使控制面能拉取活跃会话列表并查看会话详情。

| 端点 | 方法 | 说明 |
|------|------|------|
| `/agentscope/sessions` | GET | 返回活跃会话列表 |
| `/agentscope/sessions/{id}/state` | GET | 返回会话详细状态 |

#### `GET /agentscope/sessions`

返回数据面当前所有活跃会话的快照列表。控制面通过 `SessionPoller` 每 15 秒轮询该接口，将结果同步到 `AgentSession` CRD。

**请求：**

```
GET /agentscope/sessions HTTP/1.1
Host: <agent-pod-ip>:8080
```

**响应（200 OK）：**

```json
{
  "sessions": [
    {
      "id": "sess-abc123",
      "phase": "Active",
      "startedAt": "2026-06-26T10:00:00Z",
      "lastActiveAt": "2026-06-26T10:35:00Z",
      "messageCount": 42,
      "tokenUsage": {
        "promptTokens": 15000,
        "completionTokens": 8000
      },
      "contextPressure": 0.56,
      "taskSummary": {
        "total": 5,
        "pending": 1,
        "inProgress": 2,
        "completed": 2
      }
    }
  ]
}
```

**`SessionSnapshot` 字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 会话唯一标识 |
| `phase` | string | 会话阶段：`Active` / `Idle` / `Compressing` / `Terminated` |
| `startedAt` | string | 会话开始时间（RFC 3339） |
| `lastActiveAt` | string | 最近活跃时间（RFC 3339） |
| `messageCount` | int32 | 消息总数 |
| `tokenUsage` | object | Token 使用量 |
| `contextPressure` | float64 | 上下文压力比（0.0 ~ 1.0） |
| `taskSummary` | object | 任务统计 |

#### `GET /agentscope/sessions/{id}/state`

返回指定会话的详细状态快照，包括上下文压力、任务列表等。

**请求：**

```
GET /agentscope/sessions/sess-abc123/state HTTP/1.1
Host: <agent-pod-ip>:8080
```

**响应（200 OK）：**

```json
{
  "sessionId": "sess-abc123",
  "summary": "用户咨询了订单退款流程，已完成退款申请提交...",
  "currentIter": 3,
  "contextPressure": {
    "usedTokens": 18000,
    "maxTokens": 32000,
    "ratio": 0.5625
  },
  "tasks": [
    {"id": "task-1", "subject": "查询订单信息", "state": "completed"},
    {"id": "task-2", "subject": "提交退款申请", "state": "in_progress"}
  ]
}
```

**`SessionState` 字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `sessionId` | string | 会话 ID |
| `summary` | string | 会话摘要 |
| `currentIter` | int32 | 当前迭代次数 |
| `contextPressure` | object | 上下文压力详情 |
| `contextPressure.usedTokens` | int64 | 已使用 token 数 |
| `contextPressure.maxTokens` | int64 | 最大 token 数 |
| `contextPressure.ratio` | float64 | 使用比例 |
| `tasks` | []object | 任务列表 |

**响应（404 Not Found）：** 会话 ID 不存在时返回 404。

---

### Level 3 -- 全功能协调

在 Level 2 基础上增加主动控制指令，使控制面可以对会话下发压缩或终止操作。

| 端点 | 方法 | 说明 |
|------|------|------|
| `/agentscope/sessions/{id}/compress` | POST | 触发会话上下文压缩 |
| `/agentscope/sessions/{id}/terminate` | POST | 终止会话 |

#### `POST /agentscope/sessions/{id}/compress`

触发指定会话的上下文压缩。当 `contextPressure` 超过阈值时，控制面可主动下发该指令。

**请求：**

```
POST /agentscope/sessions/sess-abc123/compress HTTP/1.1
Host: <agent-pod-ip>:8080
```

**响应：**

- `200 OK` -- 压缩指令已接收
- `404 Not Found` -- 会话不存在

#### `POST /agentscope/sessions/{id}/terminate`

终止指定会话。

**请求：**

```
POST /agentscope/sessions/sess-abc123/terminate HTTP/1.1
Host: <agent-pod-ip>:8080
```

**响应：**

- `200 OK` -- 终止指令已接收
- `404 Not Found` -- 会话不存在

---

## 各等级下控制面行为

控制面根据数据面上报的 `contractLevel` 自动降级行为：

| 功能 | Level 1 | Level 2 | Level 3 |
|------|---------|---------|---------|
| 发现与纳管 | 支持 | 支持 | 支持 |
| 健康监测 | 支持 | 支持 | 支持 |
| 会话列表拉取 | 不支持 | 支持 | 支持 |
| 会话状态查看 | 不支持 | 支持 | 支持 |
| 会话压缩指令 | 不支持 | 不支持 | 支持 |
| 会话终止指令 | 不支持 | 不支持 | 支持 |

**降级逻辑实现（`SessionPollerReconciler`）：**

- `contractLevel < 2`：控制面跳过会话轮询，仅执行健康探测。Agent 的 `SessionPolling` condition 标记为 `SessionPollingUnsupported`。
- `contractLevel = 2`：控制面拉取会话列表和状态，同步到 `AgentSession` CRD，但不能下发 compress/terminate 指令。
- `contractLevel = 3`：完整功能，包括会话观测和 compress/terminate 指令下发。

对应代码位于：
- 轮询控制器：`internal/controller/session_poller.go`
- HTTP Prober：`internal/prober/http_prober.go`
- Prober 接口：`internal/prober/prober.go`

---

## Mock 数据面

项目提供了 Mock 数据面服务（`test/mock/dataplane.go`），用于 CI 测试和本地开发验证。Mock 实现了完整的 Level 1~3 契约 API。

### 使用方式

```go
import "github.com/agentscope/agentscope-go/control-plane/test/mock"

// 创建 Level 2 的 mock 数据面
dp := mock.NewMockDataPlane(2)
defer dp.Close()

// 预置会话数据
dp.AddSession(prober.SessionSnapshot{
    ID:           "sess-001",
    Phase:        "Active",
    MessageCount: 10,
})

// 设置会话状态
dp.SetSessionState("sess-001", &prober.SessionState{
    SessionID: "sess-001",
    Summary:   "处理用户咨询中",
    ContextPressure: &prober.ContextPressureInfo{
        UsedTokens: 8000,
        MaxTokens:  32000,
        Ratio:      0.25,
    },
})

// 获取 mock 服务端点
endpoint := dp.Endpoint() // http://127.0.0.1:<port>
```

### Mock 支持的操作

| 操作 | 方法 | 说明 |
|------|------|------|
| `NewMockDataPlane(level)` | 构造函数 | 创建指定 contractLevel 的 mock |
| `AddSession(snap)` | 数据注入 | 添加一个会话到列表 |
| `SetSessionState(id, state)` | 数据注入 | 设置会话详细状态 |
| `CompressCalledFor(id)` | 断言 | 检查 compress 是否被调用 |
| `TerminateCalledFor(id)` | 断言 | 检查 terminate 是否被调用 |
| `Endpoint()` | 查询 | 返回 mock 服务的 HTTP 地址 |
| `Close()` | 清理 | 关闭 mock 服务 |

---

## 一致性验证

使用 Mock 数据面验证控制面在各 contractLevel 下的行为：

### 场景 1：contractLevel = 1（最小可纳管）

```
输入：mock 数据面，contractLevel=1
预期：
  - 控制面成功探测 /agentscope/info，获取元数据
  - 控制面定期调用 /agentscope/health 进行健康检查
  - SessionPoller 跳过该 agent（contractLevel < 2）
  - Agent status 的 SessionPolling condition 为 SessionPollingUnsupported
```

### 场景 2：contractLevel = 2（会话观测）

```
输入：mock 数据面，contractLevel=2，预置 2 个 session
预期：
  - 控制面探测并纳管成功
  - SessionPoller 拉取到 2 个会话，创建对应的 AgentSession CRD
  - AgentSession CRD 的 status 反映会话的 phase、messageCount、tokenUsage 等
  - 如果 mock 移除一个 session，控制面将对应 CRD 标记为 Terminated
  - Agent status.activeSessions 反映活跃会话数
```

### 场景 3：contractLevel = 3（全功能协调）

```
输入：mock 数据面，contractLevel=3，预置 session，contextPressure 较高
预期：
  - 会话观测行为同 Level 2
  - 控制面可成功调用 compress 端点，mock.CompressCalledFor(id) 为 true
  - 控制面可成功调用 terminate 端点，mock.TerminateCalledFor(id) 为 true
```

### 运行一致性测试

```bash
cd control-plane
go test ./test/... -v -run TestContractLevel
```
