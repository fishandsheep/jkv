# 安全策略

## 支持范围

项目仅为最新 Beta 次版本提供安全修复。旧版本用户应先升级到最新版本。

## 私密报告

请使用 GitHub 仓库的 **Security → Report a vulnerability** 私密报告入口。不要为未修复漏洞创建公开 Issue。

报告请包含：

- 受影响版本与平台
- 最小复现步骤
- 可能影响
- 已知缓解方式

维护者会优先确认高风险报告。项目为社区项目，不承诺固定响应时限或 SLA。

## 安全边界

- `jkv` 自身 Release 资产提供 SHA-256、SBOM 与 GitHub artifact attestation。
- 第三方工具优先使用上游同源 checksum；无 checksum 时会在下载前警告。
- `--require-checksum` 或 `JKV_REQUIRE_CHECKSUM=1` 会拒绝无 checksum 的第三方制品。
- 项目不重新托管 JDK、Maven、Gradle 等第三方制品。
- 项目不提供跳过 TLS 校验的选项。
