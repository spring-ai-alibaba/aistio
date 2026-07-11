# 观测与控制会话

Aistio 将数据面报告的会话同步为 `AgentSession`。HTTP 轮询和命令能力取决于
`Agent.status.dataPlaneInfo.contractLevel`；ASDP 主动上报不检查该字段。

## 能力要求

| 操作 | 最低契约级别 |
| --- | --- |
| 发现 Agent 和健康检查 | Level 1 |
| 列出会话、读取会话状态 | Level 2 |
| 压缩上下文、终止会话 | Level 3 |

对于控制面创建且 Deployment 与 Agent 同名的工作负载，HTTP SessionPoller
每 15 秒选择一个 Ready Pod，并读取 `GET /agentscope/sessions`。BYO
`workloadRef` Agent 被该轮询器明确排除。ASDP 数据面仍可以主动上报会话快照。

## 查看 Agent 契约级别

```bash
kubectl -n production get agent customer-support-agent \
  -o jsonpath='{.status.dataPlaneInfo.contractLevel}{"\n"}'
```

如果值为空或小于 2，HTTP SessionPoller 不会同步会话列表；已建立的 ASDP
连接仍可主动上报会话。

## 列出会话

```bash
kubectl -n production get agentsessions
kubectl -n production get agentsession <session-name> -o yaml
```

也可以通过 REST API：

```bash
curl 'http://localhost:8080/api/v1/agents/customer-support-agent/sessions?namespace=production'
```

`AgentSession.status` 可以包含：

- `phase`
- `messageCount`
- `tokenUsage`
- `state.summary`
- `state.currentIter`
- `state.contextPressure`
- `state.tasks`

数据面 session ID 不满足 Kubernetes DNS 名规则时，控制面会生成稳定的哈希
对象名，并把原始 ID 保存到 annotation。v0.2.0 的命令控制器却会把哈希后的
对象名当作数据面 session ID 发送，因此不能可靠地压缩或终止这类会话。

## 读取详细状态

```bash
curl \
  'http://localhost:8080/api/v1/agents/customer-support-agent/sessions/<session-name>/state?namespace=production'
```

该 REST 接口只读取 `AgentSession.status.state`，不会访问数据面。
v0.2.0 虽然实现了
`GET /agentscope/sessions/{id}/state` 的 prober 客户端，但生产控制器没有
调用它。HTTP 和 ASDP 会话快照目前最多同步上下文压力比例，不会填充完整的
summary、currentIter 和 tasks；尚未同步时，REST 返回的 state 为 `null`。

## 触发上下文压缩

Level 3 数据面可通过 CR 命令触发压缩。以下方式只适用于 AgentSession 对象名
与数据面原始 session ID 相同的会话：

```bash
kubectl -n production patch agentsession <session-name> \
  --type merge \
  -p '{"spec":{"commands":{"compress":true}}}'
```

也可以使用 REST：

```bash
curl -X POST \
  'http://localhost:8080/api/v1/agents/customer-support-agent/sessions/<session-name>/compress?namespace=production'
```

命令成功发送后，控制器把阶段设为 `Compressing`。Aistio 不会根据
`contextStrategy.triggerRatio` 自动写入该命令；自动压缩必须由数据面实现
或由外部控制器触发。

## 终止会话

以下方式同样要求 AgentSession 对象名与数据面原始 session ID 相同：

```bash
kubectl -n production patch agentsession <session-name> \
  --type merge \
  -p '{"spec":{"commands":{"terminate":true}}}'
```

REST 方式如下：

```bash
curl -X POST \
  'http://localhost:8080/api/v1/agents/customer-support-agent/sessions/<session-name>/terminate?namespace=production'
```

命令发送成功后，控制器把阶段设为 `Terminated`。这表示控制面已派发命令，
不等于已经验证数据面释放了所有业务资源。

## 删除会话记录

```bash
kubectl -n production delete agentsession <session-name>
```

删除 CR 只删除控制面记录。需要先终止真实数据面会话时，应先发送 terminate。

## 多副本限制

v0.2.0 的 HTTP 会话实现存在以下限制：

- SessionPoller 只读取一个 Ready Pod，不聚合其他实例。
- 未被所选 Pod 报告的历史会话可能被标记为 `Terminated`。
- `instanceRef` 和 `instanceIP` 不能作为可靠的完整路由信息。
- 压缩和终止命令发送到任意一个 Ready Pod，不保证命中会话所属实例。

在多副本场景中，应由数据面提供共享会话目录或粘性路由。在该问题修复前，
不要把 Aistio 的会话命令作为唯一的生产控制通道。

BYO `workloadRef` 还有额外限制：压缩和终止控制器按 Agent 名查找
Deployment，不读取 `workloadRef.name`。名称不同时命令会失败；即使名称相同，
上述单 Pod 路由限制仍然存在。
