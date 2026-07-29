# 独立 catalog 路线

目标：让版本与新 JVM 工具定义独立于 jkv 客户端发布，同时保持客户端小、可审计、可离线回滚。

## v0.2：内置 provider

- provider 继续编译进 jkv。
- 固化 candidate、release、支持等级和缓存 schema。
- 不下载或执行远端代码。

## v0.3：签名数据 catalog

- 新建独立仓库，只存声明式数据和 JSON Schema。
- 条目包含 candidate、vendor、version、平台、制品 URL、校验和、支持等级、最小客户端版本。
- CI 校验 schema、URL 为 HTTPS、版本唯一、平台枚举、校验和格式和 provider live 状态。
- 发布不可变快照、SHA-256 与数字签名；jkv 保留最近已验证快照并支持固定版本、离线使用和回滚。
- 贡献者通过 PR 增加版本或工具，不获得客户端代码执行能力。

## v0.4：开放受审核的数据贡献

- 社区可通过 PR 增加受支持声明式类型的 provider、版本和工具。
- PR 必须通过 schema、来源域名、许可证、fixture、平台和 live 校验，并由维护者人工审核；不得自动合并或未经快照发布直接影响客户端。
- catalog 永久禁止 shell、JavaScript、Go 插件、安装后脚本、绝对路径和任意注册表修改。
- 超出有限模板能力的发现协议必须先在 jkv 客户端扩展并发布，不能向 catalog 引入万能 DSL。

独立仓库不是 jkv 安装脚本或 jkv 二进制的“分发镜像”。安装器镜像只负责 jkv 自身；catalog 只负责可扩展工具数据；工具制品仍来自公共官方国内镜像。
