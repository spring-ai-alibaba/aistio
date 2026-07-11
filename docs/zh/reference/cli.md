# aistioctl 命令参考

`aistioctl` 是 Aistio `v0.2.0` 的 REST API 客户端和项目配置工具。
它不是完整的安装或运维工具。可靠的集群操作仍应使用 Helm 和 `kubectl`。

## 全局参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--api-endpoint` | `http://localhost:8080` | 控制面 REST 地址 |
| `--api-token` | `AGENTSCOPE_API_TOKEN` | Bearer Token |
| `--namespace`、`-n` | `default` | 目标命名空间 |

## 可用命令

| 命令 | 当前行为 |
| --- | --- |
| `aistioctl version` | 打印 CLI 版本 `0.2.0` |
| `aistioctl init NAME` | 创建 `agentscope.yaml`、`AGENTS.md`、`skills/` 和 `tools/` |
| `aistioctl agent list` | 列出 Agent 摘要，可用 `--type` 过滤 |
| `aistioctl agent status NAME` | 输出完整 Agent JSON |
| `aistioctl agent deploy [NAME]` | 解析项目 YAML，并调用 push API |
| `aistioctl agent push NAME` | 把指定文件作为原始 JSON 请求体发送 |
| `aistioctl agent revisions NAME [REV]` | 列出 revision，或显示快照 |
| `aistioctl agent rollback NAME REV` | 从保存的快照生成新 revision |

## 初始化和部署

```bash
aistioctl init support-agent
cd support-agent
aistioctl --api-endpoint http://localhost:8080 \
  --namespace production \
  agent deploy support-agent --dry-run
```

`agent deploy` 支持：

- `--config`、`-c`：配置文件名，默认 `agentscope.yaml`。
- `--dir`、`-d`：项目目录，默认当前目录。
- `--dry-run`：只输出 push JSON。
- `--api-key`：覆盖模型 API Key。

没有显式 `systemPrompt` 时，命令会读取同目录 `AGENTS.md`。API Key 的
读取顺序为 `--api-key`、配置文件、`AGENTSCOPE_MODEL_API_KEY`。

不要把 `agent push` 和 `agent deploy` 混用：

- `deploy` 解析 YAML 并构造 JSON。
- `push` 不解析 YAML，直接把文件内容以
  `Content-Type: application/json` 发送。其默认文件名虽为
  `agentscope.yaml`，实际内容必须是合法 JSON。

## 查询和回滚

```bash
aistioctl -n production agent list
aistioctl -n production agent status support-agent
aistioctl -n production agent revisions support-agent
aistioctl -n production agent revisions support-agent v2
aistioctl -n production agent rollback support-agent v2
```

revision 只由 REST push/rollback 路径维护。没有保存
`specSnapshot` 的历史项不能用于回滚。

## 已知不完整命令

| 命令 | v0.2.0 问题 | 替代方式 |
| --- | --- | --- |
| `install` | 只打印成功信息，不执行 Helm 或 Kubernetes 操作 | `./install/install.sh` 或 Helm |
| `verify-install` | CRD 和 Deployment 检查固定返回成功 | `kubectl get crds` 和 `kubectl rollout status` |
| `agent adopt` | 请求字段为 `deployment`，API 要求 `deploymentName` | 创建 BYO Agent CR 或加发现标签 |
| `proxy-status` | 按完整 Agent 解析摘要列表，字段可能为空 | 查看 `Agent.status` |
| `team *` | 部分响应结构与 REST API 不一致，且 Team 本身为实验功能 | 使用 `kubectl` 或直接调用 REST |

`aistioctl init` 生成的 provider 为小写 `dashscope`。CRD 要求
`DashScope`，部署前需要修正。

## 构建 CLI

Dockerfile 会同时构建 `aistiod` 和 `aistioctl`，但最终镜像入口是
`/aistiod`。本地单独构建 CLI：

```bash
go build -o bin/aistioctl ./cmd/aistioctl
./bin/aistioctl version
```
