# Catalog 使用说明

## 用户需要知道什么

`jkv list` 和 `jkv install` 面向日常使用；无需手动下载或编辑 Catalog。工具包仍从条目声明的公共 HTTPS 源下载，Catalog 只提供经过审核的元数据。

v0.3 客户端会验证 `latest.json` 和不可变 Snapshot 的 Ed25519 签名，并保存最近可信 Snapshot。更新网络失败时，已有缓存继续可用；首次使用且无法取得可信 Catalog 时会明确失败，而不会回退执行远端 Provider。

```sh
jkv list java --refresh
jkv install java 21-tem --default
jkv doctor --json
```

`jkv doctor --json` 的 `catalog_trust` 显示已验证 Snapshot 的 sequence、年龄、端点、签名 key ID 与撤销数。

## 安全边界

- Catalog 可声明版本、平台、HTTPS 制品 URL、归档类型、可选 SHA-256 与撤销信息。
- Catalog 不能包含 shell、JavaScript、Go 插件、安装后脚本、绝对路径或任意环境变量。
- `jkv` 只接受 `zip`、`tar.gz`、`tgz`，沿用安全解压和不覆盖安装规则。
- 已发布 Snapshot 不重写；错误用更高 sequence 修正，危险制品用撤销记录处理。

## 参与维护

新版本或兼容的新工具请向 [jkv-catalog](https://github.com/fishandsheep/jkv-catalog) 提交 PR。Catalog 仓库 README 给出最短流程；协议细节见 [Catalog v1 规范](catalog-v1-spec.md)、[设计](catalog-v1-design.md) 与 [实施计划](catalog-v1-implementation-plan.md)。
