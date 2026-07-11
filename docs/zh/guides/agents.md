# 管理声明式 Agent

声明式 Agent 由 Aistio 创建 ConfigMap、Deployment 和 Service。Aistio
`v0.2.0` 只注册了 `agentscope-java` 数据面适配器，因此本页不使用
其他 runtime。

## 前置条件

开始前应完成以下操作：

- 安装 Aistio，并确认 `aistio-controller` 已就绪。
- 准备一个目标命名空间。
- 如需引用模型或 MCP 服务，先创建同命名空间的 `ModelConfig` 和
  `MCPServer`。
- 确认数据面镜像实现了 Aistio HTTP 数据面契约。

创建示例命名空间：

```bash
kubectl create namespace production
```

## 创建 Agent

以下示例使用当前唯一的声明式 runtime：

```yaml
apiVersion: agentscope.io/v1alpha1
kind: Agent
metadata:
  name: customer-support-agent
  namespace: production
spec:
  type: Declarative
  runtime: agentscope-java
  displayName: 客服助手
  description: 处理客户咨询
  declarative:
    agentConfig:
      systemMessage: 你是一个专业的客服助手。
      modelConfigRef: qwen-max-config
      stream: true
      maxTurns: 50
      sessionAffinity: none
    tools:
      - type: McpServer
        mcpServer:
          name: knowledge-base
          toolNames:
            - search_docs
          requireApproval:
            - delete_doc
    replicas: 1
    resources:
      requests:
        cpu: 200m
        memory: 256Mi
      limits:
        cpu: "1"
        memory: 1Gi
    env:
      - name: LOG_LEVEL
        value: info
```

保存为 `agent.yaml`，然后应用：

```bash
kubectl apply -f agent.yaml
kubectl -n production get agents
kubectl -n production get deploy,service,configmap \
  -l agentscope.io/agent-name=customer-support-agent
```

控制器会创建：

- `customer-support-agent-config` ConfigMap。
- `customer-support-agent` Deployment。
- `customer-support-agent` Service。

## 检查状态

```bash
kubectl -n production get agent customer-support-agent -o yaml
kubectl -n production describe agent customer-support-agent
kubectl -n production get events \
  --field-selector involvedObject.name=customer-support-agent
```

重点检查：

- `status.managementMode` 应为 `CP-Managed`。
- `status.replicas` 应显示期望、Ready 和 Available 副本数。
- `Accepted=True` 表示适配器接受了 runtime。
- `Ready=True` 表示 Deployment 副本就绪。
- `DataPlaneConnected=True` 表示 `/agentscope/info` 探测成功。

数据面未实现契约时，Deployment 可能就绪，但 Aistio 无法填充完整的
`dataPlaneInfo`。

## 更新配置和副本

直接修改 Agent：

```bash
kubectl -n production patch agent customer-support-agent \
  --type merge \
  -p '{"spec":{"declarative":{"replicas":2}}}'
```

控制器会更新 Deployment 的 Pod template 和副本数。Aistio 当前不创建
HorizontalPodAutoscaler，自动扩缩容需要由使用方另行管理。

Agent 配置会写入启动 ConfigMap。ASDP 连通时，控制面还会推送 Agent、
Model、Tool 和 Skill 配置。数据面必须处理配置并返回 ACK，热更新才算完成。

## 使用 aistioctl 部署项目配置

`aistioctl agent deploy` 会读取 `agentscope.yaml`。当配置没有
`systemPrompt` 时，它还会把同目录的 `AGENTS.md` 作为 system prompt。

```bash
aistioctl init support-agent
cd support-agent
aistioctl --api-endpoint http://localhost:8080 \
  --namespace production \
  agent deploy support-agent --dry-run
```

确认生成的 JSON 后再移除 `--dry-run`。如果 REST API 开启静态 Token，
通过 `--api-token` 或 `AGENTSCOPE_API_TOKEN` 提供。

注意：`aistioctl init` 在 v0.2.0 生成的小写 `dashscope` 与 CRD 要求的
`DashScope` 不一致。部署前应手动改为正确大小写。

通过 REST push 创建的 Agent 会维护 revision 快照。直接使用
`kubectl apply` 不会自动生成相同的 revision 历史。

## 删除 Agent

```bash
kubectl -n production delete agent customer-support-agent
```

控制器会清理关联的 AgentSession。Deployment、Service 和 ConfigMap 通过
owner reference 由 Kubernetes 级联删除。

## v0.2.0 限制

- 仅 `agentscope-java` 支持声明式协调。
- 适配器使用未锁定版本的默认 Java runtime 镜像。生产环境应先验证并固定
  可重复的镜像版本；当前 Agent Schema 没有声明式 image 覆盖字段。
- 适配器把控制面地址固定为
  `aistiod.aistio-system.svc`，与默认 Helm Service
  `aistio-controller` 不一致。
- 默认 Helm Service 不暴露 ASDP gRPC 端口。
- ModelConfig 的 Secret 值不会自动挂载到数据面 Pod。
- Schema 中的上下文压缩阈值只作为数据面配置，不会让控制面自动触发压缩。

在修复上述部署链路前，声明式 Agent 适合源码验证和受控 PoC，不应直接作为
生产可用性承诺。
