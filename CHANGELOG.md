# Changelog

本项目遵循 [Semantic Versioning](https://semver.org/)。

## Unreleased

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

## v0.1.0

- 首个原生六平台 Release。
