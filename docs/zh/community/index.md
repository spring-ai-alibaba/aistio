# 参与 Aistio

Aistio 的代码、问题和变更记录位于
[spring-ai-alibaba/aistio](https://github.com/spring-ai-alibaba/aistio)。
本页只列出当前仓库可以验证的参与入口，不提供未确认的交流群或邀请链接。

## 提交问题

在 [GitHub Issues](https://github.com/spring-ai-alibaba/aistio/issues) 提交
问题时，请包含：

- Aistio 版本、Git commit 和安装方式。
- Kubernetes、Helm 和 Go 版本。
- 使用的 CR YAML，删除其中的 Secret 值。
- 资源 `status.conditions` 和相关 Event。
- 最小化后的 `aistiod` 日志。
- 可重复的操作步骤、预期结果和实际结果。

不要上传 API Key、Bearer Token、证书私钥或业务敏感数据。

## 提交变更

开始前阅读仓库根目录的
[中文贡献指南](https://github.com/spring-ai-alibaba/aistio/blob/main/CONTRIBUTING_zh.md)
和[行为准则](https://github.com/spring-ai-alibaba/aistio/blob/main/CODE_OF_CONDUCT_zh.md)。

本地基础验证：

```bash
go fmt ./...
go vet ./...
go test ./...
helm lint ./helm/aistio
helm template aistio ./helm/aistio --namespace aistio-system
```

修改 API 类型时，还需要重新生成 CRD、RBAC 和 deepcopy 文件，并执行
`make verify`。生成文件必须和源码保持一致，不应手工只修改 Helm CRD
副本。

## 文档贡献

文档变更应遵守：

- 中文页面沿用 `docs/_toc.yml` 中的目录结构。
- 以当前源码、CRD 和 Chart 为事实来源。
- 明确版本、实验状态和未实现边界。
- 命令使用 `aistioctl`、`helm/aistio` 和实际 release 名称。
- 不把 Schema 字段直接描述为已完成的运行能力。
- 不复制其他仓库的 SDK 教程、包路径或品牌内容。

新增中文文档使用 UTF-8 无 BOM，并保持标题连续、术语一致和短段落。

## 安全问题

安全问题的处理方式见
[SECURITY_zh.md](https://github.com/spring-ai-alibaba/aistio/blob/main/SECURITY_zh.md)。
不要在公开 Issue 中披露仍可利用的漏洞、凭据或内部地址。

## 贡献者

仓库贡献记录以
[GitHub Contributors](https://github.com/spring-ai-alibaba/aistio/graphs/contributors)
为准。维护者信息以仓库 CODEOWNERS、提交记录和项目设置为准，不在文档中
维护可能过期的个人名单。

## 许可证

Aistio 使用 Apache License 2.0。提交代码和文档前，请阅读仓库
[LICENSE](https://github.com/spring-ai-alibaba/aistio/blob/main/LICENSE)。
