# 支持政策

v0.0.2 使用两级支持：

| 等级 | 范围 | 承诺 |
|---|---|---|
| core | Eclipse Temurin、Maven、Gradle | PR 必跑单元与集成测试；镜像健康由 jkv-catalog 仓库持续校验；阻断性回归优先修复 |
| beta | Dragonwell、BiSheng、Ant、Groovy、JMeter、Tomcat、Spring Boot CLI | PR 必跑离线解析测试；每日 live 检查；镜像变化可能短期导致发现失败 |

平台支持取决于 Catalog 条目声明的制品。jkv 客户端构建并测试 Linux、macOS、Windows 的 amd64/arm64；这不表示每个 provider 都覆盖六个目标。

下载源失败必须明确报错，不静默改用境外工具下载源。已安装版本不受在线目录下架影响，并继续出现在 `list` 的本地组。
