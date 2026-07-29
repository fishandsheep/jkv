# 贡献指南

感谢改进 jkv。

## 开始前

- 小型缺陷、测试、文档修正可直接提交 PR。
- 新命令、新 candidate、新 provider 或破坏性变更先创建 Issue 对齐方案。
- 安全漏洞按 `SECURITY.md` 私密报告。

## 本地检查

```sh
gofmt -w cmd internal
go test -short ./...
go test ./...
go vet ./...
go build ./cmd/jkv
```

网络测试必须支持 `testing.Short()`。文件系统测试使用 `t.TempDir()`。用户可见行为变化需补代表性 CLI 输出。

## 设计原则

- 保持中国网络友好、六平台可移植。
- provider 发现属于 catalog；下载、校验、解压和安装状态属于 store。
- 不覆盖用户现有 Maven、Gradle 或 shell 配置。
- 新来源必须为 HTTPS、无需登录、可脚本发现、长期维护的公共官方国内来源。
- 测试通过最高可观察 seam 验证行为，不绑定内部实现。

## PR

使用 Conventional Commit 风格标题。PR 说明动机、行为变化、测试命令、跨平台与安全影响。提交贡献即表示该贡献按 Apache-2.0 许可。
