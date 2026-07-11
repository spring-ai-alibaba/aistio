# 部署与升级

本页说明 Aistio `v0.2.0` 的 Helm 部署、镜像构建、升级和回滚。当前 Chart
位于 `helm/aistio`。

## 部署 profile

| profile | 行为 |
| --- | --- |
| 默认 values | 1 个副本、leader election、Webhook 和 metrics |
| `experimental.yaml` | 启用 AgentTeam、SandboxBroker 和 gRPC Service 端口 |
| `ha.yaml` | 2 个副本、cert-manager Webhook、ServiceMonitor 和较高资源配额 |

`ha` 是“偏生产”的参数集合，不代表项目已经通过生产验收。它依赖
cert-manager 和 Prometheus Operator CRD，而且 Webhook 证书模板在
v0.2.0 中存在 DNS 名不匹配问题。

## 关键 Helm 值

| 值 | 默认值 | 说明 |
| --- | --- | --- |
| `image.repository` | Aistio 公共镜像仓库 | 控制面镜像 |
| `image.tag` | `0.2.0` | 镜像标签 |
| `replicaCount` | `1` | 控制面副本数 |
| `leaderElection.enabled` | `true` | 控制器 leader election |
| `service.httpPort` | `8080` | REST API |
| `service.grpcPort` | `15010` | ASDP |
| `metrics.enabled` | `true` | metrics Service 端口 |
| `webhook.enabled` | `true` | Admission Webhook |
| `api.authToken` | 空 | REST 静态 Bearer Token |
| `networkPolicy.enabled` | `false` | 控制面 NetworkPolicy |
| `tracing.endpoint` | 空 | OpenTelemetry endpoint；空值禁用 |

## 构建自定义镜像

Dockerfile 使用 Go 1.26.5+ 构建 `aistiod` 和 `aistioctl`，最终运行
distroless nonroot 镜像：

```bash
docker build -t aistio:dev .
```

将镜像推送到集群可访问的仓库后安装：

```bash
helm upgrade --install aistio ./helm/aistio \
  --namespace aistio-system \
  --create-namespace \
  --set image.repository=registry.example.com/platform/aistio \
  --set image.tag=dev
```

本地集群也可以先把 `aistio:dev` 导入对应运行时，再把
`image.pullPolicy` 设为 `IfNotPresent`。

## 升级

Helm 不会在 `helm upgrade` 时升级 `crds/` 中的 CRD。先审查并显式应用
新 CRD，再升级控制面：

```bash
kubectl apply -f ./config/crd

helm upgrade aistio ./helm/aistio \
  --namespace aistio-system \
  --reuse-values

kubectl -n aistio-system rollout status deploy/aistio-controller
```

API 为 `v1alpha1`。升级前应导出自定义资源，比较 CRD Schema，并验证是否
存在不兼容字段变化。

## 回滚

先查看 release 历史：

```bash
helm -n aistio-system history aistio
helm -n aistio-system rollback aistio <revision>
```

Helm rollback 不会回滚已经单独应用的 CRD。若新旧控制面要求不同 Schema，
必须单独准备 CRD 和自定义资源迁移方案。

## Webhook

默认 Chart 生成自签名证书并创建 ValidatingWebhookConfiguration 和
MutatingWebhookConfiguration。每次升级重新生成证书可能影响正在进行的 API
请求。

Chart 提供 cert-manager 配置，但 `Certificate.spec.dnsNames` 当前使用
`<release>.<namespace>.svc`，Webhook 实际 Service 名为
`<release>-webhook`。此外，`issuerRef` 默认为空。因此，在修正模板、
配置有效 Issuer 并验证证书轮换前，不应直接启用该模式。

## 已知部署限制

### ASDP Service 暴露

`aistiod` 默认启用 ASDP，Deployment 始终监听 `15010`，但 Helm Service
只在 `experimental.enabled=true` 时暴露 gRPC 端口。默认安装无法通过
Service 建立 ASDP。

### 声明式 Agent 控制面地址

`agentscope-java` 适配器固定注入：

- `http://aistiod.aistio-system.svc:8080`
- `aistiod.aistio-system.svc:15010`

默认 Helm release 的 Service 名为 `aistio-controller`，而且命名空间可
配置。两者不一致。当前 Chart 也没有提供覆盖这些环境变量的 Agent 级配置。

### gRPC TLS

Helm values 定义了 `experimental.grpcTLS`，但 Deployment 模板没有挂载
证书，也没有传入 `--grpc-tls-*` 参数。仅设置 values 不会启用 TLS。

### 默认数据面镜像

声明式 Java 适配器使用 `runtime-java:latest`。Agent CRD 没有声明式镜像
覆盖字段。需要可重复部署时，应先在代码或 Chart 中补齐版本锁定能力。

## 卸载

```bash
helm uninstall aistio --namespace aistio-system
```

该命令保留 CRD 和自定义资源。只有确认要删除所有 Aistio 对象时，才执行：

```bash
./install/uninstall.sh --purge-crds
```
