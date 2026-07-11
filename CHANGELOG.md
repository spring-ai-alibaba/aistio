# 变更日志

## 未发布

### 文档

- 将文档站重构为 Aistio 中文单语站点，删除误引自其他仓库的 SDK 文档。
- 新增架构、安装、Agent、BYO、模型与 MCP、会话、ASDP、CLI、REST API、
  实验功能和运维说明，并明确 v0.2.0 的实现边界。

### 依赖

- 将最低 Go 版本从 1.26.0 提升到 1.26.5。
- 将 `google.golang.org/grpc` 从 v1.81.1 升级到 v1.82.0。
- 将 `github.com/quic-go/quic-go` 从 v0.59.0 升级到 v0.59.1。
- 将 `golang.org/x/crypto` 升级到 v0.54.0，并同步兼容的
  `golang.org/x/*` 间接依赖。
