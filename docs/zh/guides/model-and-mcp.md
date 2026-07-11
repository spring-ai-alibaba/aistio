# 配置模型和 MCP

Aistio 使用 `ModelConfig` 保存模型元数据和 Secret 引用，使用
`MCPServer` 注册工具服务。两者都是配置资源，不是模型或工具请求代理。

## 创建模型凭据

以下示例把 API Key 保存到 `production` 命名空间：

```bash
kubectl -n production create secret generic dashscope-credentials \
  --from-literal=api-key='<api-key>'
```

Secret 必须和 `ModelConfig` 位于同一命名空间。

## 创建 ModelConfig

```yaml
apiVersion: agentscope.io/v1alpha1
kind: ModelConfig
metadata:
  name: qwen-max-config
  namespace: production
spec:
  provider: DashScope
  model: qwen-max
  apiKeySecret: dashscope-credentials
  apiKeySecretKey: api-key
  options:
    temperature: "0.7"
    maxTokens: "4096"
  tls:
    disableVerify: false
```

```bash
kubectl apply -f modelconfig.yaml
kubectl -n production get modelconfig qwen-max-config -o yaml
```

控制器会确认 provider 和 model 非空，并检查 Secret key。成功后，
`Accepted=True`，`status.secretHash` 保存 Secret 内容的短哈希。

provider 必须使用 CRD 定义的大小写：`DashScope`、`OpenAI`、
`Anthropic`、`Gemini`、`Ollama`、`Moonshot` 或 `Custom`。

## ModelConfig 的边界

- Aistio 不向模型提供方发送请求。
- ASDP 只推送 ModelConfig spec，不推送 Secret 值。
- 自动创建的 Agent Pod 不会挂载 `apiKeySecret`。
- ModelConfig controller 没有为 Secret 注册 watch。只更新 Secret 不会立即
  重新计算 `secretHash`。

因此，数据面必须通过自己的 Kubernetes Secret 挂载或凭据系统获得 API Key。
轮换 Secret 后，可以更新 ModelConfig 的 annotation 触发重新协调，但这仍
不会把 Secret 值通过 ASDP 发送给数据面。

## 创建 Remote MCPServer

先创建认证 Header 所需的 Secret。Secret 值应包含服务端要求的完整 Header
内容：

```bash
kubectl -n production create secret generic mcp-auth \
  --from-literal=authorization='Bearer <token>'
```

然后注册 MCP 服务：

```yaml
apiVersion: agentscope.io/v1alpha1
kind: MCPServer
metadata:
  name: knowledge-base
  namespace: production
spec:
  description: 企业知识库
  type: Remote
  remote:
    protocol: STREAMABLE_HTTP
    url: https://mcp.example.com/knowledge
    headersFrom:
      - kind: Secret
        name: mcp-auth
        key: authorization
        header: Authorization
    timeout: 30s
  allowedNamespaces:
    from: Same
```

```bash
kubectl apply -f mcpserver.yaml
kubectl -n production get mcpserver knowledge-base -o yaml
```

控制面会执行 MCP `initialize`、发送
`notifications/initialized`，再调用 `tools/list`。它支持 JSON 和
`text/event-stream` 响应，并保存 `Mcp-Session-Id`。出于凭据和内部
地址保护考虑，客户端拒绝所有 HTTP 重定向。

工具发现每 5 分钟重新排队一次。失败时检查
`Discovered=False` 的 message 和控制面日志。

Schema 接受 `remote.protocol: SSE`，但发现客户端不会按 protocol 分支。
它始终向固定 URL 发送 JSON-RPC POST，只能解析 POST 返回的 SSE 数据。
传统 MCP SSE transport 的独立事件端点尚未实现。`remote.timeout` 当前也
未接入客户端，工具发现使用控制器固定的 15 秒超时。

## Stdio MCPServer 的边界

Stdio 配置可以存入 CRD：

```yaml
apiVersion: agentscope.io/v1alpha1
kind: MCPServer
metadata:
  name: local-tools
  namespace: production
spec:
  type: Stdio
  stdio:
    command: /usr/local/bin/tool-server
    args:
      - --stdio
```

控制面不会执行 command，也不会为 Stdio 服务发现工具。数据面需要自行解释
这份配置并管理子进程。

## 绑定到 Agent

声明式 Agent 通过名称引用同命名空间资源：

```yaml
spec:
  type: Declarative
  runtime: agentscope-java
  declarative:
    agentConfig:
      modelConfigRef: qwen-max-config
    tools:
      - type: McpServer
        mcpServer:
          name: knowledge-base
          toolNames:
            - search_docs
            - get_faq
          requireApproval:
            - delete_doc
```

`toolNames` 用于限制交给数据面的工具集合。`requireApproval` 也只作为
配置下发；Aistio 控制面没有工具调用审批状态机，最终执行规则由数据面保证。

`allowedNamespaces` 字段目前也只存在于 Schema，控制器没有执行跨命名空间
授权策略。v0.2.0 应只引用同命名空间资源，不要把该字段当作安全边界。

## 排查顺序

1. 检查 Secret、ModelConfig、MCPServer 和 Agent 是否位于同一命名空间。
2. 检查 `status.conditions` 的 type、reason 和 message。
3. 从 `aistio-controller` Pod 验证 MCP URL 的 DNS 和网络可达性。
4. 检查 ASDP 是否连接，以及数据面是否对配置返回 ACK。
5. 检查数据面自己的凭据挂载和工具审批实现。
