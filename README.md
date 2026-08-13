# jkv

<p align="center">
  <img src="img/logo.svg" alt="jkv logo" width="180">
</p>

[![CI](https://github.com/fishandsheep/jkv/actions/workflows/ci.yml/badge.svg)](https://github.com/fishandsheep/jkv/actions/workflows/ci.yml)

jkv (Java Kit Version) 是中国网络友好、跨平台的 Java 生态及开发者工具版本管理器。用一个命令安装、切换和固定 Java、Maven、Gradle、Ant、Groovy、JMeter、Tomcat、Spring Boot CLI 等工具版本。

[English](README.en.md) · [完整命令](docs/commands.md) · [故障排查](docs/troubleshooting.md) · [安全政策](SECURITY.md)

## 概览

- 国内公共镜像下载；支持 Linux、macOS、Windows。
- 同一工具可并存多个版本；按当前终端、默认环境或项目 `.jkvrc` 选择版本。
- 下载时校验 SHA-256；安全解压且不覆盖已有安装。
- Maven、Gradle 可生成国内依赖镜像配置，不覆盖已有用户配置。
- 可自行更新或卸载；在线 Catalog 下架的本地旧版本仍可列出、切换和卸载。
- 支持工具：Java、Maven、Gradle、Ant、Groovy、JMeter、Tomcat、Spring Boot CLI。

当前发布版本为 [`v0.0.2`](https://github.com/fishandsheep/jkv/releases/tag/v0.0.2)。Java（Temurin）、Maven、Gradle 为 core 支持；Dragonwell、BiSheng、Ant、Groovy、JMeter、Tomcat、Spring Boot CLI 为 beta，实际可用版本以 `jkv list` 为准。

## 快速开始

Linux / macOS：

```sh
curl -fsSL https://cnb.cool/fishandsheep/jkv/-/releases/download/v0.0.2/install.sh | sh
```

Windows PowerShell：

```powershell
irm https://cnb.cool/fishandsheep/jkv/-/releases/download/v0.0.2/install.ps1 | iex
```

安装器优先从 CNB 下载同一版本二进制，传输失败才回退 GitHub；校验和不匹配会直接失败。工具包仍从 Catalog 审核过的公共源直连下载。

也可下载 [Release](https://github.com/fishandsheep/jkv/releases/latest) 对应系统与架构的二进制，或运行 `go install github.com/fishandsheep/jkv/cmd/jkv@v0.0.2`。工具默认安装到 `~/.jkv`；用 `JKV_DIR` 可改位置。`go install` 和手工放置的二进制不归 `self` 命令管理。

`v0.0.1` 用户需先重新运行上方 `v0.0.2` 安装器一次；安装器会写入所有权收据。之后可直接使用 `jkv self update`。

首次切换版本前加载 shell hook（将 `zsh` 换成你的 shell）：

```sh
eval "$(jkv init zsh)"
jkv install java 21-tem
jkv use java 21-tem
java -version
```

支持 Bash、Zsh、Fish、PowerShell。更多安装方式见 [发布与安装](docs/release.md)。

## 基本命令

```sh
jkv list java                    # 简写：jkv ls java
jkv install java 21-tem          # 简写：jkv i java 21-tem
jkv install maven                # 安装最新稳定版
jkv use java 21-tem              # 简写：jkv u；仅当前终端
jkv default java 21-tem          # 简写：jkv d；新终端默认
jkv current                      # 简写：jkv c
jkv home java                    # 输出当前安装目录
jkv doctor                       # 检查环境、缓存和镜像
jkv self update                  # 简写：jkv s up
jkv self uninstall               # 简写：jkv s rm；保留 candidates
jkv self uninstall --purge       # 删除整个 JKV_DIR；非交互需 --yes
```

`jkv list <candidate>` 合并在线 Catalog 与 `$JKV_DIR/candidates`。例如 Catalog 只保留 Spring Boot 3.x 时，已安装的 2.x 仍显示为 `AVAILABLE -`、`SOURCE local`，可继续 `use`、`default` 或 `uninstall`。

项目需要固定版本时：

```sh
jkv env init
jkv env apply
```

`.jkvrc` 可提交到仓库，例如：

```properties
java=21.0.11+10-tem
maven=3.9.16
```

Maven / Gradle 依赖镜像与工具安装分开配置：

```sh
jkv mirror maven --apply
jkv mirror gradle --apply
jkv mirror status
```

完整选项、JSON 输出、退出码与清理命令见 [命令参考](docs/commands.md)。

## Catalog 如何联动

`jkv` 负责本机下载、校验、解压和切换；[jkv-catalog](https://github.com/fishandsheep/jkv-catalog) 负责审核版本、平台、下载地址和校验信息。

v0.0.2 消费已签名 Catalog Snapshot：正常使用 `jkv list`、`jkv install` 即可，网络临时故障时继续使用本机可信缓存。Catalog 不执行 Provider、插件或安装脚本；工具归档仍从审核过的公共源直连下载。协议、安全边界和迁移说明见 [Catalog 使用说明](docs/catalog.md)。

想补充版本或工具？到 [jkv-catalog](https://github.com/fishandsheep/jkv-catalog) 提交数据 PR；步骤见其 README。

## 未来计划

代码已具备签名 Catalog 的核心链路：客户端验签、CNB/GitHub 双端点、可信缓存与防回滚，以及 Catalog 构建和发布流水线。接下来保持小而可验证：

1. 完成真实 Catalog 的端到端验收与正式发布反馈闭环。
2. 完成 Catalog 通用 Candidate 的 shell 激活（传递 `home_env`）与动态补全，真正做到新增兼容工具无需发新版 jkv。
3. 持续扩展审核过的版本与平台覆盖，完善 checksum、撤销和镜像健康检查。

## 开发与贡献

```sh
gofmt -w cmd internal
go test -short ./...
go test ./...
go vet ./...
go build ./cmd/jkv
```

修复、测试、文档可直接提交 PR；新命令、客户端能力或破坏性变更请先开 Issue。贡献约定见 [CONTRIBUTING.md](CONTRIBUTING.md)，发布流程见 [docs/release.md](docs/release.md)，来源选择见 [docs/sources.md](docs/sources.md)。
