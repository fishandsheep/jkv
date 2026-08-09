# Changelog

本项目遵循 [Semantic Versioning](https://semver.org/)。

## Unreleased

后续版本变更将在此记录。

## v0.0.1

重置 jkv 发布基线，发布首个 `jkv (Java Kit Version)` 版本。

### Added

- 将 jkv 定位为 Java 生态及开发者工具版本管理器，覆盖 Java、Maven、Gradle、Ant、Groovy、JMeter、Tomcat 和 Spring Boot CLI。
- GitHub 与 CNB 使用同一套六平台二进制、安装器、校验文件、SPDX SBOM 和 provenance 资产。
- 正式发布版本内置 Catalog 信任根，并消费 `catalog-v1-000004` 签名 Snapshot。

### Representative output

```text
$ jkv version
jkv v0.0.1
```

## v0.2.0-beta.1（历史）

### Added

- Apache-2.0 许可证与正式项目治理文档。
- Fish shell 支持。
- JSON、quiet、verbose 基础选项与 `doctor`、`repair` 命令。
- 严格 checksum 模式、缓存 dry-run、下载续传和重试。
- 国内优先、GitHub fallback 的 `jkv` 自身安装链路。

### Changed

- Go module 使用完整 GitHub 路径。
- 安装器使用可升级 managed profile block，并写入实际安装目录。
- 安装和本地状态变更增加并发保护。
- Release 增加 SPDX SBOM、artifact attestation 和固定 Action commit。

### Security

- 加强 ZIP/TAR 路径、链接、特殊文件与展开资源限制。
- `.jkvrc` 拒绝路径型版本值。
- checksum 失败不再尝试其他来源。

### Known issues

- Beta 仍需 10 名以上非作者完成真实六平台种子测试；CI 结果不能替代用户验证。
- Dragonwell、BiSheng 与其他 beta provider 的制品平台覆盖不完整，镜像目录变化可能短期影响发现。
- 多数第三方镜像未提供同源 checksum；需要强完整性保证时使用严格模式。

### Upgrade

`v0.1.0` 用户可直接覆盖安装 jkv 二进制。安装器会把旧的单行 `# jkv init` 配置迁移为受管 block；candidates、下载缓存和默认版本保留。卸载默认也保留这些数据。

### Representative output

```text
$ jkv version
jkv v0.2.0-beta.1

$ jkv list
CANDIDATE    说明                                 国内源                 平台
java         JDK：Temurin、Alibaba Dragonwell…   清华 / 阿里 OSS / 华为云 按发行商
```

## v0.1.0

- 首个原生六平台 Release。
