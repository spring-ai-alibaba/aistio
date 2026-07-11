# AgentTeam 和 Sandbox

AgentTeam、TeamTask、TeamMessage 和 SandboxClaim 在 Aistio `v0.2.0` 中属于
实验功能。默认安装不会注册相关控制器或 REST 路由。

实验功能不等于生产可用。AgentTeam 只有部分控制面闭环，Sandbox 供应则明确
未实现。

## 启用实验功能

使用安装脚本：

```bash
./install/install.sh -p experimental
```

或使用 Helm profile：

```bash
helm upgrade --install aistio ./helm/aistio \
  --namespace aistio-system \
  --create-namespace \
  -f ./helm/aistio/profiles/experimental.yaml
```

这会向 `aistiod` 传入 `--enable-experimental=true`，同时启用 Team 和
SandboxBroker。不能只通过当前 Chart 单独启用其中一项。

## AgentTeam 资源

AgentTeam 描述目标、负责人、静态成员、动态成员策略、恢复策略和生命周期：

```yaml
apiVersion: agentscope.io/v1alpha1
kind: AgentTeam
metadata:
  name: pr-review-team
  namespace: production
spec:
  objective: 检查变更的正确性、性能和测试覆盖
  lead:
    agentRef:
      name: senior-reviewer
    prompt: 负责拆分任务和汇总结论。
  members:
    - name: security-reviewer
      agentRef:
        name: security-agent
      prompt: 检查认证、注入和数据暴露。
  dynamicMembers:
    enabled: true
    maxTotal: 4
  recovery:
    reschedulePolicy: Auto
    maxRestarts: 3
  lifecycle:
    maxDuration: 2h
    ttlAfterCompleted: 1h
  config:
    taskClaimStrategy: self-claim
    shutdownPolicy: all-complete
```

```bash
kubectl apply -f agentteam.yaml
kubectl -n production get agentteams,agentsessions,teamtasks,teammessages
```

## AgentTeam 已实现部分

实验控制器可以：

- 为 lead 和静态成员创建 AgentSession CR。
- 把 Team 上下文保存到 AgentSession annotation。
- 使用 TeamTask 保存任务、依赖、claim、complete 和结果。
- 使用 TeamMessage 作为消息 outbox。
- 通过 ASDP 传递部分 Team 事件。
- 记录成员阶段，并执行超时、TTL 和有限恢复状态机。

Task claim 使用 Kubernetes `resourceVersion` 处理并发冲突。

## AgentTeam 未闭环部分

创建 AgentSession CR 不会启动真实数据面的推理会话。当前
SessionSpawner 直接把新 CR 状态标记为 `Active`，但没有向 Agent 发送
“创建会话并执行 prompt”的生产命令。

因此，以下行为不能由控制面状态单独证明：

- lead 或成员已经运行。
- objective 和 prompt 已进入模型上下文。
- TeamTask 被真实 Agent 消费。
- 消息广播已经到达所有成员。
- 恢复会话已经恢复业务执行。

只有自定义数据面实现相应 ASDP Team 事件和会话启动逻辑时，才能继续做端到端
验证。

## SandboxClaim

SandboxClaim Schema 可以描述 Pod template、生命周期和网络域名：

```yaml
apiVersion: agentscope.io/v1alpha1
kind: SandboxClaim
metadata:
  name: support-agent-sandbox
  namespace: production
spec:
  agentRef:
    name: customer-support-agent
  sandboxTemplate:
    podTemplate:
      containers:
        - name: worker
          image: python:3.13
    lifecycle:
      shutdownPolicy: Delete
      idleTimeout: 30m
```

创建后，SandboxBroker 只会：

- 把 `status.phase` 设为 `Pending`。
- 设置 `Accepted=True`。
- 设置 `Provisioned=False`。
- 把 reason 设为 `NotImplemented`。

它不会创建 Pod、Service、网络策略或 agent-sandbox 资源。

## 验证边界

```bash
kubectl -n production get sandboxclaim support-agent-sandbox -o yaml
```

预期结果是永久 Pending，而不是 Bound。不要把 SandboxClaim 用于隔离不可信
代码，也不要依赖其中的网络或生命周期字段提供安全边界。

## 关闭实验功能

把 `experimental.enabled` 改为 `false` 后，相关 CRD 仍留在集群中，
但控制器和 REST 路由停止工作。关闭前应先清理或导出实验资源，避免留下无人
协调的对象。
