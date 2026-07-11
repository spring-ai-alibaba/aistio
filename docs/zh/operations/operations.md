# 日常运维

Aistio 的运行状态来自 Kubernetes 资源、Event、控制面日志、健康端点和
metrics。排查时应先确认控制器，再检查目标资源和数据面。

## 检查控制面

```bash
kubectl -n aistio-system get deploy,pod,service
kubectl -n aistio-system rollout status deploy/aistio-controller
kubectl -n aistio-system describe deploy aistio-controller
```

Helm 默认配置两个健康面：

- controller-runtime 健康端口 `8082`，路径为 `/healthz` 和
  `/readyz`。Kubernetes liveness/readiness probe 使用该端口。
- REST 端口 `8080` 也提供同名路径，用于 API 进程检查。

本地检查 REST：

```bash
kubectl -n aistio-system port-forward svc/aistio-controller 8080:8080
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
curl http://localhost:8080/api/v1/version
```

## 查看日志

```bash
kubectl -n aistio-system logs deploy/aistio-controller --all-containers
kubectl -n aistio-system logs deploy/aistio-controller \
  --all-containers --previous
```

控制器日志默认使用 JSON；REST 访问日志仍由 Gin 以文本格式输出。源码本地
运行时可使用 `--log-format=console`，或设置 `AGENTSCOPE_DEV_LOG=true`。

建议按以下字段筛选：

- controller 名称。
- Agent、ModelConfig 或 MCPServer 名称。
- namespace。
- reconcile error reason。
- ASDP instance ID、config type、version 和 nonce。

## 检查资源条件和 Event

```bash
kubectl -n production get agents,modelconfigs,mcpservers,agentsessions
kubectl -n production describe agent customer-support-agent
kubectl -n production get events --sort-by=.lastTimestamp
```

常见条件：

| 条件或 reason | 含义 |
| --- | --- |
| `UnsupportedRuntime` | 没有注册对应数据面适配器 |
| `DeploymentNotReady` | 期望副本尚未 Ready |
| `DataPlaneConnected` 缺失 | HTTP 数据面信息探测尚未成功 |
| `SecretNotFound` | ModelConfig 的 Secret 或 key 不存在 |
| `DiscoveryFailed` | MCP Header 解析、连接或工具发现失败 |
| `ContractLevelInsufficient` | 数据面不支持请求的会话命令 |
| `NotImplemented` | Sandbox 供应未实现 |

`DataPlaneConnected=True` 和 `status.dataPlaneInfo.lastProbeAt` 也可能是旧值：当前
控制器在后续 `/info` 失败时不会主动写入 `False` 或清空已有状态。必须结合
`Ready`、Event、日志和直接探测结果判断实时连通性。

## 查看 metrics

默认 metrics 端口为 `8081`：

```bash
kubectl -n aistio-system port-forward svc/aistio-controller 8081:8081
curl http://localhost:8081/metrics
```

主要自定义指标包括：

- `agentscope_agent_replicas`
- `agentscope_dataplane_connected`
- `agentscope_sessions_active`
- `agentscope_session_operations_total`
- `agentscope_probe_duration_seconds`
- `agentscope_reconcile_errors_total`
- `agentscope_grpc_connections_active`
- `agentscope_grpc_config_push_total`
- `agentscope_grpc_config_nack_total`
- `agentscope_grpc_stream_errors_total`

Team 指标只对实验功能有意义。

Chart 可以创建 ServiceMonitor、PrometheusRule 和 Grafana dashboard
ConfigMap。这些开关依赖外部 CRD 或 dashboard sidecar。v0.2.0 的 dashboard
模板读取 `dashboards/aistio-overview.json`，但仓库实际文件名为
`agentscope-overview.json`，因此该 ConfigMap 可能得到空内容。

## OpenTelemetry

设置 `tracing.endpoint` 后，Chart 会向 `aistiod` 传入
`--otel-endpoint` 和 `--trace-sampling`。默认 endpoint 为空，Tracing
关闭。

开启前应确认 collector 地址、数据保留策略和采样成本。Tracing 初始化失败
只记录错误，不会阻止控制面启动。

## 排查数据面连接

HTTP 契约检查顺序：

```bash
kubectl -n production get pods -l agentscope.io/agent-name=customer-support-agent -o wide
kubectl -n production describe agent customer-support-agent
```

然后从集群内访问 Pod 的 `/agentscope/info` 和 `/agentscope/health`。
控制面直接访问 Pod IP，因此还要检查 NetworkPolicy 和跨节点网络。

ASDP 排查重点：

1. Service 是否暴露 `15010`。
2. 数据面使用的 DNS 名是否与 Helm release 和命名空间一致。
3. 控制面日志是否出现 handshake rejected。
4. mTLS 证书身份是否包含正确的 namespace 和 Agent。
5. `agentscope_grpc_config_nack_total` 是否增长。

## 备份和恢复

Aistio 没有独立数据库。备份应覆盖 Kubernetes 自定义资源及其引用的 Secret、
ConfigMap 和外部工作负载定义。

恢复顺序：

1. 安装匹配版本的 CRD。
2. 安装控制面。
3. 恢复 Secret 和 ConfigMap。
4. 恢复 ModelConfig、MCPServer 和 Agent。
5. 最后恢复实验性 Team 资源。

AgentSession 通常来自数据面重新上报。是否恢复历史会话，应由业务数据面的
持久化策略决定。

## 容量和高可用注意事项

- 控制器支持 leader election。
- ASDP 连接存在于单个 `aistiod` 进程内，不跨副本共享。
- 配置 watcher 在所有副本运行，但每个副本只推送给本地连接。
- HTTP 会话轮询每 15 秒访问一个控制面创建工作负载的 Pod；BYO
  `workloadRef` 不进入该轮询路径。
- MCP Remote 工具发现每 5 分钟执行一次。

扩容控制面前，应评估长连接重分布、API Server watch 数量和 MCP 外部请求
频率，而不是只观察 CPU。
