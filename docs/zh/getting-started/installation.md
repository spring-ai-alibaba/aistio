# 安装 Aistio

本页说明如何从当前仓库安装 Aistio `v0.2.0`。推荐使用安装脚本或
`helm upgrade --install`。不要使用 `aistioctl install`，该命令在
v0.2.0 中只打印进度信息，不执行实际安装。

## 前置条件

安装控制平面需要：

- 一个可访问的 Kubernetes 集群。
- 已配置当前集群上下文的 `kubectl`。
- Helm 3。
- 对目标命名空间和集群级 CRD、RBAC 资源的创建权限。

从源码构建时，还需要 Go 1.26.5+ 和 Docker Buildx。

## 获取源码

```bash
git clone https://github.com/spring-ai-alibaba/aistio.git
cd aistio
```

后续命令都从仓库根目录执行。

## 使用安装脚本

默认脚本把 release `aistio` 安装到 `aistio-system`：

```bash
./install/install.sh
```

脚本实际执行 Helm 安装，并等待 `aistio-controller` Deployment 就绪。
可以通过参数修改命名空间、release 或 profile：

```bash
./install/install.sh -n aistio-system -r aistio
./install/install.sh -p experimental
```

`experimental` 会同时启用 AgentTeam 和尚未实现供应功能的 SandboxClaim。
不要在生产工作负载中依赖这些功能。

## 直接使用 Helm

默认安装命令如下：

```bash
helm upgrade --install aistio ./helm/aistio \
  --namespace aistio-system \
  --create-namespace
```

可选 profile 位于 `helm/aistio/profiles`：

```bash
helm upgrade --install aistio ./helm/aistio \
  --namespace aistio-system \
  --create-namespace \
  -f ./helm/aistio/profiles/experimental.yaml
```

`ha.yaml` 默认使用 2 个控制面副本、leader election、Webhook 和
ServiceMonitor。该 profile 依赖 cert-manager 和 Prometheus Operator。
v0.2.0 的 cert-manager 证书 DNS 与 Webhook Service 名不一致，不能不经
修改直接使用。详见[部署与升级](../operations/deployment.md)。

## 验证安装

先检查 Deployment 和 CRD：

```bash
kubectl -n aistio-system rollout status deploy/aistio-controller
kubectl -n aistio-system get pods
kubectl get crds | grep agentscope.io
```

然后转发 REST API 端口：

```bash
kubectl -n aistio-system port-forward svc/aistio-controller 8080:8080
```

在另一个终端查询版本：

```bash
curl http://localhost:8080/api/v1/version
```

返回结果应包含 `version`、`apiVersion`、`component` 和
`experimental`。如已配置 `api.authToken`，除版本接口外的
`/api/v1` 请求需要 Bearer Token：

```bash
curl -H 'Authorization: Bearer <token>' \
  'http://localhost:8080/api/v1/agents?namespace=default'
```

`aistioctl verify-install` 目前只真实检查 REST API。CRD 和 Deployment
检查仍是空实现，因此不能替代上述 `kubectl` 命令。

## 检查 Chart

修改 Chart 后，可以先做本地静态验证：

```bash
helm lint ./helm/aistio
helm template aistio ./helm/aistio \
  --namespace aistio-system \
  --include-crds
```

这些命令只验证模板，不证明控制面和数据面已经端到端连通。

## 卸载

保留 CRD 和自定义资源时执行：

```bash
./install/uninstall.sh
```

Helm 按设计不会在卸载时删除 CRD。以下命令会删除所有
`agentscope.io` CRD 及其对象，必须先确认数据已不再需要：

```bash
./install/uninstall.sh --purge-crds
```

## 下一步

- [理解 Aistio 架构](../concepts/architecture.md)
- [管理声明式 Agent](../guides/agents.md)
- [部署与升级](../operations/deployment.md)
