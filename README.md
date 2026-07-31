# jkv

[![CI](https://github.com/fishandsheep/jkv/actions/workflows/ci.yml/badge.svg)](https://github.com/fishandsheep/jkv/actions/workflows/ci.yml)
[![Mirror health](https://github.com/fishandsheep/jkv/actions/workflows/mirror-health.yml/badge.svg)](https://github.com/fishandsheep/jkv/actions/workflows/mirror-health.yml)

面向国内网络的 JVM 工具版本管理器。v0.3 起，版本清单来自独立、人工审核、Ed25519 签名的 Catalog；工具归档仍从各自公共国内源下载。jkv 不下载或执行远端 Provider、插件、脚本。

> 当前公开安装包是 `v0.2.0-beta.2`。`v0.3` 发布包会内置 Catalog 公钥，默认使用 CNB Catalog、网络故障时回退 GitHub Catalog；验签、哈希、schema 或防回滚失败绝不回退到未签名数据。参见[支持政策](docs/support.md)。

[English](README.en.md) · [命令参考](docs/commands.md) · [故障排查](docs/troubleshooting.md) · [安全政策](SECURITY.md)

## 当前支持

| Candidate | 等级 | 国内下载源 | Linux | macOS | Windows |
|---|---|---|---:|---:|---:|
| Java / Eclipse Temurin | core | 清华 TUNA Adoptium | x64/arm64 | x64/arm64 | x64；arm64 视镜像文件 |
| Maven | core | 阿里云 Apache 镜像 | ✓ | ✓ | ✓ |
| Gradle | core | 腾讯云 Gradle 镜像 | ✓ | ✓ | ✓ |
| Java / Dragonwell、BiSheng | beta | 阿里 OSS / 华为云 | 见版本 | — | 见版本 |
| Ant、Groovy、JMeter、Tomcat、Spring Boot CLI | beta | 阿里云镜像 | ✓ | ✓ | ✓ |

v0.3 的版本、URL、平台和支持等级由签名 Catalog 决定；`jkv list/install` 不再实时解析镜像目录。旧实时 Provider 仅在迁移期开启 `JKV_LEGACY_PROVIDER=true` 时使用。

## 快速开始

Linux / macOS，当前公开 beta：

```sh
curl -fsSL https://github.com/fishandsheep/jkv/releases/download/v0.2.0-beta.2/install.sh | sh
```

Windows PowerShell，当前公开 beta：

```powershell
irm https://github.com/fishandsheep/jkv/releases/download/v0.2.0-beta.2/install.ps1 | iex
```

国内对象存储配置完成后，Linux/macOS 使用 `https://<国内域名>/beta/install.sh`，PowerShell 使用同路径 `install.ps1`；URL 只替换域名部分。安装器优先下载国内 jkv 二进制，传输失败时回退 GitHub。JDK、Maven、Gradle 等工具介质始终来自 Catalog 指定的公共国内源。每个 jkv 二进制都校验 SHA-256；校验不匹配不会切换来源。

固定版本入口为 `https://github.com/fishandsheep/jkv/releases/download/<tag>/install.sh`；PowerShell 将文件名换成 `install.ps1`。`beta/` 是国内便捷指针。未配置国内地址时，发布仍正常进行，安装脚本直接使用同一 Tag 的 GitHub 资产。

默认安装到 `~/.jkv`，无需 Go 或管理员权限。重新打开终端后验证：

```sh
jkv version
jkv list
```

已有 Go 工具链也可使用固定 module 版本：

```sh
go install github.com/fishandsheep/jkv/cmd/jkv@v0.2.0-beta.1
```

可用 `JKV_DIR` 修改安装目录，`JKV_DOWNLOAD_BASE` 指定首选 jkv 制品目录，`JKV_FALLBACK_BASE` 指定后备目录。`--no-modify-profile` 不修改 shell 配置；`--uninstall` 只移除 jkv 与托管配置，保留已安装工具；`--purge --yes` 才彻底删除数据。

不运行安装器时，手工加载环境：

```sh
export JKV_DIR="$HOME/.jkv"
export PATH="$JKV_DIR/bin:$PATH"
eval "$(jkv init bash)" # zsh 用户改为 zsh
```

Fish：

```fish
set -gx JKV_DIR "$HOME/.jkv"
fish_add_path "$JKV_DIR/bin"
jkv init fish | source
```

```powershell
$env:JKV_DIR = Join-Path $HOME '.jkv'
$env:Path = (Join-Path $env:JKV_DIR 'bin') + [IO.Path]::PathSeparator + $env:Path
Invoke-Expression ((jkv init powershell) -join [Environment]::NewLine)
```

## 使用

```sh
jkv list                         # 简写: jkv ls
jkv list java                    # 按 vendor 分组，显示下载可用性 √/×
jkv list java --refresh          # 忽略 6 小时缓存并刷新
jkv install java 21-tem          # 简写: jkv i java 21-tem
jkv install java 17-dragonwell
jkv install java 21-bisheng
jkv install maven
jkv install gradle 8.14.3

jkv use java 17-dragonwell       # 当前终端；简写: jkv u
jkv default java 21-tem          # 新终端默认；简写: jkv d
jkv current                      # 简写: jkv c
jkv home java
jkv uninstall java 17-dragonwell
jkv repair java 21.0.11+10-tem
jkv doctor
```

无版本参数时安装最新稳定版。Java 默认优先 Temurin；可用 `21-tem`、`17-dragonwell`、`21-bisheng` 选择某个大版本最新构建。

项目级版本：

```sh
jkv env init          # 根据当前默认版本生成 .jkvrc
jkv env apply         # 在当前终端应用
jkv env clear         # 恢复默认版本
```

`.jkvrc` 是简单、可提交的文本：

```properties
java=21.0.11+10-tem
maven=3.9.16
```

## v0.3 Catalog 使用与验证

v0.3 发布二进制内置 Catalog 公钥和两个固定端点：CNB 为首选，GitHub Release 为网络故障后备。客户端先验证签名 `latest.json`，再按其 SHA-256 和签名验证不可变 Snapshot；已接受过更高 `sequence` 后，自动更新会拒绝旧 Snapshot。

正常用户无需设置 Catalog 环境变量：

```sh
jkv version
jkv list java --refresh
jkv install java 21-tem --default
jkv doctor --json
```

`jkv doctor --json` 的 `catalog_trust` 会显示当前 Snapshot sequence、年龄、端点、验证 key ID 和累计撤销数。首次访问无可信缓存且 Catalog 不可达会明确失败；已有可信缓存时离线仍可 `list` 和安装已列出的版本。

开发、预发布或自建镜像可覆盖公开参数：

```sh
export JKV_EXPERIMENTAL_CATALOG=true
export JKV_CATALOG_KEY_ID='catalog-2026-a'
export JKV_CATALOG_PUBLIC_KEY='<base64-ed25519-public-key>'
export JKV_CATALOG_ENDPOINT='https://catalog.example.cn/releases/download'
export JKV_CATALOG_FALLBACK_ENDPOINT='https://github.com/fishandsheep/jkv-catalog/releases/download'
jkv list java --refresh
```

`JKV_CATALOG_ENDPOINT` 是 Release download 根，不是 `latest.json` 文件 URL。客户端固定请求：

```text
<root>/catalog-latest/latest.json
<root>/catalog-v1-000001/catalog-v1.json
```

仅迁移排障时设置 `JKV_LEGACY_PROVIDER=true`；它放弃 Catalog 的审核、撤销和防回滚保护，不应作为日常配置。

## 新版本与新工具如何进入 jkv

用户不能通过本机命令向 Catalog 注入 URL 或脚本。新增版本和工具走 [jkv-catalog](https://github.com/fishandsheep/jkv-catalog) 的受审 PR：Provider 发现候选数据，维护者审核 `data/catalog-input.json`，合并后手动发布新签名 Snapshot。

新 Candidate 仅支持通用 ZIP、`tar.gz` 或 `tgz` 归档，并要求解压后存在单一根目录和 `bin/`。Catalog 条目必须声明稳定 `artifact_id`、HTTPS 直链、平台、archive type、selector、support tier；不能包含 shell、JavaScript、安装后脚本、绝对路径或任意环境变量。需要特殊安装布局时，先在 jkv 客户端增加受限能力，再发布 Catalog 数据。

## Maven / Gradle 依赖镜像

安装工具本体和下载项目依赖是两件事。以下命令生成或启用阿里云公共仓库配置：

```sh
jkv mirror maven                # 生成 ~/.m2/settings-jkv.xml
jkv mirror maven --apply        # 仅当 ~/.m2/settings.xml 不存在时启用
jkv mirror gradle               # 预览 init script
jkv mirror gradle --apply       # 写 ~/.gradle/init.d/jkv-mirrors.gradle
jkv mirror status
```

已有 Maven/Gradle 配置绝不覆盖。企业项目若依赖私服或 `repositoriesMode`，应手工合并，不建议全局强制镜像。

## 隐私与网络

jkv 不含遥测、账号、广告或后台常驻进程。网络请求仅用于用户触发的版本发现、可用性检查和下载；定时 provider 健康检查只在项目 CI 内运行。客户端尊重系统代理和系统 CA，不提供关闭 TLS 校验的开关。

## 选源原则

详见 [docs/sources.md](docs/sources.md)。核心标准：国内主体长期维护、HTTPS、无需登录、目录或元数据可机器读取、当前仍同步稳定版。只有 GitHub Release、网盘、博客转存、需人工点击或版本长期滞后的生态暂不支持。

## 与其他工具的边界

| 工具 | jkv 的差异 |
|---|---|
| SDKMAN | jkv 原生支持 Windows，使用 Go 单二进制，工具发现不依赖境外 broker；candidate 数量更少 |
| asdf | jkv 不运行第三方插件脚本，内置 provider 只访问审核过的公共官方国内源；扩展性更保守 |

jkv 选择“窄而可靠”，不追求 candidate 数量、企业私服管理或任意插件。Beta 真实测试任务和退出条件见 [docs/beta-testing.md](docs/beta-testing.md)，维护与归档政策见 [GOVERNANCE.md](GOVERNANCE.md)。

## 开发

每次 push 和 pull request 会执行：Linux、macOS、Windows × amd64、arm64 六目标交叉编译；六种对应原生 GitHub runner 上执行单测、`go vet`、二进制冒烟测试，以及 Unix/PowerShell 安装器集成测试。Linux/Windows ARM64 runner 目前是 GitHub public preview。

```sh
go test ./...
go run ./cmd/jkv list java
go build ./cmd/jkv
```

正式发布流程、供应链产物和回滚步骤见 [docs/release.md](docs/release.md)。独立模板/catalog 仓库的分阶段方案见 [docs/catalog-roadmap.md](docs/catalog-roadmap.md)；v0.2 不从远端 catalog 执行任何代码。

从源码目录安装（需要 Go）：

```sh
./install.sh
```

Windows PowerShell：

```powershell
.\install.ps1
```
