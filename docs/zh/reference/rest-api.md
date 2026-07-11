# REST API 参考

`aistiod` 默认在 `:8080` 提供 REST API。资源接口以 `/api/v1` 为
前缀。该 REST 版本不等同于 Kubernetes CRD 的
`agentscope.io/v1alpha1`。

## 系统接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz` | HTTP 服务健康状态 |
| GET | `/readyz` | HTTP 服务就绪状态 |
| GET | `/api/v1/version` | 版本、组件和实验功能状态 |

这 3 个接口不经过 API Bearer Token 中间件。

## 认证和授权

Helm 的 `api.authToken` 配置静态 Bearer Token：

```text
Authorization: Bearer <token>
```

静态 Token 模式只做认证，不执行 Kubernetes SubjectAccessReview。

`aistiod --enable-kube-auth=true` 会改用 Kubernetes TokenReview，并根据
HTTP 方法、资源和 `namespace` 查询参数执行 SubjectAccessReview。Helm
模板当前没有暴露该开关。

## 通用查询参数

- `namespace`：目标命名空间。单对象接口通常默认 `default`。
- `limit`：列表上限，默认通常为 `100`。
- `continue`：Kubernetes 列表分页 Token。

不同列表接口对空 namespace 的处理不完全一致。客户端应始终显式传入
`namespace`。

## Agent 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/agents` | 列出 Agent 摘要 |
| GET | `/api/v1/agents/{name}` | 获取完整 Agent |
| POST | `/api/v1/agents/{name}/push` | 创建或更新 Agent 及关联配置 |
| PATCH | `/api/v1/agents/{name}` | 只更新 `displayName` 和 `description` |
| DELETE | `/api/v1/agents/{name}` | 删除 Agent |
| GET | `/api/v1/agents/{name}/health` | 返回 Agent 健康摘要 |
| GET | `/api/v1/agents/{name}/revisions` | 列出 revision |
| GET | `/api/v1/agents/{name}/revisions/{rev}` | 读取 revision 快照 |
| POST | `/api/v1/agents/{name}/rollback` | 回滚并创建新 revision |
| POST | `/api/v1/agents/{name}/adopt` | 纳管 Deployment |

adopt 请求体使用 `deploymentName`：

```json
{
  "deploymentName": "order-agent-deployment",
  "namespace": "production",
  "agentName": "order-agent",
  "runtime": "custom"
}
```

push 是 Aistio 自定义配置接口，不接受完整 Kubernetes Agent 对象。请求字段
包括 runtime、systemPrompt、model、tools、skills、subagents、
teamTemplates 和 deployment。

## 会话接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/agents/{name}/sessions` | 列出 AgentSession 摘要 |
| POST | `/api/v1/agents/{name}/sessions` | 只创建 AgentSession CR |
| GET | `/api/v1/agents/{name}/sessions/{id}` | 获取 AgentSession |
| GET | `/api/v1/agents/{name}/sessions/{id}/state` | 读取 CR 中的状态快照 |
| POST | `/api/v1/agents/{name}/sessions/{id}/compress` | 写入 compress 命令 |
| POST | `/api/v1/agents/{name}/sessions/{id}/terminate` | 写入 terminate 命令 |
| DELETE | `/api/v1/agents/{name}/sessions/{id}` | 删除 AgentSession CR |

POST create 只创建控制面记录，不向数据面发送“启动会话”命令。compress 和
terminate 返回 `initiated` 也只表示 CR 更新成功。

## ModelConfig 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/v1/modelconfigs?name={name}` | 从请求体 spec 创建资源 |
| GET | `/api/v1/modelconfigs` | 列表 |
| GET | `/api/v1/modelconfigs/{name}` | 详情 |
| PATCH | `/api/v1/modelconfigs/{name}` | 更新 provider、model 或 options |
| DELETE | `/api/v1/modelconfigs/{name}` | 删除 |

PATCH 当前不会更新 API Key Secret 或 TLS 字段。完整修改可使用 Kubernetes
API。

## MCPServer 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/v1/mcpservers` | 创建 MCPServer |
| GET | `/api/v1/mcpservers` | 列表 |
| GET | `/api/v1/mcpservers/{name}` | 详情 |
| PATCH | `/api/v1/mcpservers/{name}` | 只更新 `description` 和 `remote` |
| DELETE | `/api/v1/mcpservers/{name}` | 删除 |
| GET | `/api/v1/mcpservers/{name}/tools` | 返回已发现工具 |

创建 MCPServer 的请求体是包含顶层 `name` 和 `spec` 的对象，不是完整
Kubernetes 资源。具体 spec 结构以
`api/v1alpha1/mcpserver_types.go` 为准。

## 实验接口

启用 `--enable-experimental` 后，额外注册：

- `/api/v1/teams`
- `/api/v1/teams/{team}/members`
- `/api/v1/teams/{team}/tasks`
- `/api/v1/teams/{team}/messages`
- `/api/v1/sandboxes`

这些接口未启用时返回 `404`。Sandbox create 只创建保持 Pending 的
SandboxClaim，不供应实际环境。完整路由和边界见
[实验功能](../experimental/teams-and-sandbox.md)。

## 错误响应

常见错误格式为：

```json
{
  "error": "resource not found",
  "message": "optional detail"
}
```

不同 handler 的状态码和 message 仍不完全统一。客户端应先判断 HTTP 状态码，
再解析 `error` 和 `message`。
