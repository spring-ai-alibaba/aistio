# 纳管现有工作负载

Bring Your Own（BYO）模式用于观察现有 Agent Deployment。推荐使用
`workloadRef`，因为 Aistio 不会修改或拥有被引用的 Deployment。

## 数据面要求

现有工作负载至少应：

- 由同命名空间的 Kubernetes Deployment 管理。
- 有一个可被控制面访问的 Ready Pod。
- 在指定端口实现 `GET /agentscope/info` 和
  `GET /agentscope/health`。
- 如需会话观测，可通过 ASDP 主动上报会话；v0.2.0 的 HTTP SessionPoller
  不处理 `workloadRef` Agent。

当前 prober 固定访问 `/agentscope`。虽然 CRD 有 `contractPath` 字段，
v0.2.0 尚未使用自定义路径。

## 显式创建 BYO Agent

```yaml
apiVersion: agentscope.io/v1alpha1
kind: Agent
metadata:
  name: order-agent
  namespace: production
spec:
  type: BYO
  runtime: custom
  displayName: 订单 Agent
  byo:
    workloadRef:
      kind: Deployment
      name: order-agent-deployment
    agentPort: 8080
    contractPath: /agentscope
```

```bash
kubectl apply -f agent-byo.yaml
kubectl -n production get agent order-agent -o yaml
```

控制器按名称读取 `order-agent-deployment`，同步副本状态，并探测其中一个
Ready Pod。它不会修改 Deployment 的镜像、Pod template 或副本数。

## 通过标签自动发现

也可以给现有 Deployment 添加管理标签：

```bash
kubectl -n production annotate deployment order-agent-deployment \
  agentscope.io/runtime=custom \
  agentscope.io/agent-port=8080

kubectl -n production label deployment order-agent-deployment \
  agentscope.io/agent-name=order-agent \
  agentscope.io/managed=true
```

最后添加 `agentscope.io/managed=true`，确保首次发现时 runtime、端口和 Agent
名称已经就绪。Agent 创建后再补 annotation 不会重建已有 spec。

Discovery controller 会等待至少一个 Pod Ready，然后创建
`type: BYO`、`workloadRef` 指向该 Deployment 的 Agent。

以下元数据控制发现行为：

| 名称 | 类型 | 作用 |
| --- | --- | --- |
| `agentscope.io/managed=true` | label | 启用自动发现 |
| `agentscope.io/agent-name` | label | 指定 Agent 名称；缺省时使用 Deployment 名 |
| `agentscope.io/runtime` | annotation | 指定 runtime；缺省时尝试读取 `/info` |
| `agentscope.io/agent-port` | annotation | 指定探测端口；缺省为 `8080` |

即使初次 `/info` 探测失败，控制器仍可能使用最少信息创建 Agent，并将
runtime 回退为 `custom`。应继续查看状态条件和 Event，而不是只检查对象是否
存在。

## 检查纳管状态

```bash
kubectl -n production get agent order-agent
kubectl -n production get agent order-agent \
  -o jsonpath='{.status.managementMode}{"\n"}'
kubectl -n production describe agent order-agent
```

`status.managementMode` 应为 `Adopted`。当 Deployment 被删除时，
Agent 的 `Ready` 条件会变为 `False`，原因为 `WorkloadDeleted`。

## 解除纳管

删除 BYO Agent 不会删除外部 Deployment：

```bash
kubectl -n production delete agent order-agent
```

如果通过自动发现创建 Agent，还应移除 Deployment 上的管理标签，避免控制器
再次创建：

```bash
kubectl -n production label deployment order-agent-deployment \
  agentscope.io/managed-
```

## 不建议使用 BYO image

CRD 还允许在 `spec.byo.image` 中提供镜像，让 Aistio 创建 Deployment。
v0.2.0 的实现只为 Declarative Agent 创建 ConfigMap，但
`agentscope-java` 适配器会在 BYO image Pod 中无条件挂载
`<agent>-config`。该 ConfigMap 不存在时，Pod 无法正常启动。

在修复这一问题前，应使用 `workloadRef` 纳管自行创建的 Deployment。
`aistioctl agent adopt` 当前发送的字段名也与 REST API 不一致，建议直接
创建 Agent CR 或使用管理标签。

## 会话和多副本限制

BYO `workloadRef` 的状态探测会从所引用的 Deployment 中选择一个 Ready
Pod，但 HTTP SessionPoller 明确排除了这种 Agent。ASDP 仍可主动上报会话，
不过当前压缩和终止控制器会按 Agent 名查找 Deployment，而不会读取
`workloadRef.name`。当二者名称不同时，命令无法发送。

即使名称相同，多副本命令也只会发往任意一个 Ready Pod，不保证命中会话所属
实例。数据面应提供共享会话目录和路由；在这些限制修复前，不要依赖 Aistio
控制任意 BYO `workloadRef` 的会话。
