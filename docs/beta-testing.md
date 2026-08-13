# Beta 种子测试

`v0.2.0-beta.1` 目标招募 10–20 名非作者用户。测试者请选择真实设备，不用 CI 容器代替。

## 固定任务

1. 记录系统、架构、shell 和网络环境；从国内固定版本脚本安装。
2. 运行 `jkv version`、`jkv doctor --json`、`jkv list`。
3. 安装并实际运行 Temurin、Maven、Gradle；记录版本发现和下载耗时。
4. 测试 `use`、`default`、`.jkvrc` 的 `env init/apply/clear`。
5. 重复安装验证幂等；损坏一个测试安装后执行 `repair`。
6. 运行 `jkv s up`，确认最新版 no-op；安装一个 Catalog 已不含的旧版本，确认 `list` 仍显示并可切换。
7. 执行 `jkv s rm`，确认 candidates、默认值和缓存保留；重新安装后确认继续可用。
8. 可选：阻断国内 jkv 制品地址，验证 GitHub 后备；不要对第三方工具期待 GitHub 后备。

## 反馈模板

- jkv 版本：
- OS/架构/shell：
- 网络与代理：
- 完成的任务：
- 预期与实际：
- 复现命令：
- 脱敏后的 `jkv doctor --json`：
- 是否阻断继续使用：

安全问题不要写入公开反馈，按 `SECURITY.md` 私密报告。

## Beta 退出条件

- Beta 运行至少 8 周。
- 六客户端平台无未解决 P0/P1。
- core provider 连续 30 天稳定。
- 至少 10 名非作者完成真实安装任务。
- CLI、配置与 catalog schema 完成兼容性审查。
- 安全、卸载、repair 和回滚路径完成真实验证。
