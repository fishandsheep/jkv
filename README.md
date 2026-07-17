# jkv

[![CI](https://github.com/fishandsheep/jkv/actions/workflows/ci.yml/badge.svg)](https://github.com/fishandsheep/jkv/actions/workflows/ci.yml)

面向国内网络的 JVM 工具版本管理器。思路接近 SDKMAN：在线列版本、下载解压、保存多版本、按终端或项目切换；区别是只接入可脚本化的国内稳定源，并原生支持 Linux、macOS、Windows。

## 当前支持

| Candidate | 国内下载源 | Linux | macOS | Windows |
|---|---|---:|---:|---:|
| Java / Eclipse Temurin | 清华 TUNA Adoptium | x64/arm64 | x64/arm64 | x64；arm64 视镜像文件 |
| Java / Alibaba Dragonwell | 阿里云 OSS 官方源 | x64/arm64 | — | x64 |
| Java / Huawei BiSheng | 华为云鲲鹏归档 | x64/arm64 | — | — |
| Gradle | 腾讯云 Gradle 镜像 | ✓ | ✓ | ✓ |
| Maven、Ant、Groovy、JMeter、Tomcat | 阿里云 Apache 镜像 | ✓ | ✓ | ✓ |
| Spring Boot CLI | 阿里云 Maven 公共仓库 | ✓ | ✓ | ✓ |

版本从镜像目录或官方元数据实时发现，不在客户端写死。Dragonwell 官方元数据偶发不可用时，客户端回退到内置的最近已知官方 OSS 坐标。

## 快速开始（无需 Go）

Linux / macOS：

```sh
curl -fsSL https://raw.githubusercontent.com/fishandsheep/jkv/main/install.sh | sh
```

Windows PowerShell：

```powershell
irm https://raw.githubusercontent.com/fishandsheep/jkv/main/install.ps1 | iex
```

安装器自动识别 Linux、macOS、Windows 及 amd64、arm64，从 GitHub Releases 下载原生二进制并校验 SHA-256。默认安装到 `~/.jkv`，无需 Go 或管理员权限。重新打开终端后验证：

```sh
jkv version
jkv list
```

可用 `JKV_DIR` 修改安装目录，`JKV_REPO` 改用其他 GitHub fork，或用 `JKV_DOWNLOAD_BASE` 指向包含二进制及 `.sha256` 文件的镜像目录。Tag `v*` 会由 GitHub Actions 构建六个平台产物。

不运行安装器时，手工加载环境：

```sh
export JKV_DIR="$HOME/.jkv"
export PATH="$JKV_DIR/bin:$PATH"
eval "$(jkv init bash)" # zsh 用户改为 zsh
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

### 命令简写与补全

| 命令 | 简写 | 命令 | 简写 |
|---|---|---|---|
| `list` | `ls` | `install` | `i` |
| `use` | `u` | `default` | `d` |
| `current` | `c` | `uninstall` | `rm` |
| `home` | `h` | `env` | `e` |
| `init` | `in` | `mirror` | `m` |
| `clean` | `cl` | `version` | `v` |

`jkv init bash`、`jkv init zsh` 和 `jkv init powershell` 会同时注册 shell hook 与动态补全。命令和简写共享补全规则：

| 输入位置 | 补全内容 |
|---|---|
| `list/ls`、`install/i`、`use/u`、`default/d`、`current/c`、`uninstall/rm`、`home/h` | 全部 candidate |
| `install/i <candidate>` | 在线版本、下载缓存版本、`latest` |
| `use/u`、`default/d`、`uninstall/rm`、`home/h <candidate>` | 已安装版本 |
| `clean/cl` | 缓存类型、全部 candidate、下载缓存版本 |
| `env/e`、`init/in`、`mirror/m` | 动作、shell、选项 |

版本补全适用于 Java、Maven、Gradle、Ant、Groovy、JMeter、Tomcat 和 Spring Boot CLI，不写死具体包名：

```sh
jkv install java <Tab>      # JDK 在线及已缓存版本
jkv i maven <Tab>           # Maven 在线及已缓存版本
jkv default gradle <Tab>    # 已安装 Gradle 版本
jkv rm tomcat <Tab>         # 已安装 Tomcat 版本
```

首次补全在线版本时会读取镜像元数据；后续使用本地缓存。补全不会探测下载地址。

### 本地缓存

版本目录和下载可用性缓存位于 `$JKV_DIR/cache/catalog/`，有效期 6 小时。缓存过期时自动刷新；网络失败会回退到旧缓存。使用 `jkv list <candidate> --refresh` 强制刷新。

安装压缩包保存在 `$JKV_DIR/cache/downloads/`。`uninstall` 只删除解压后的安装目录；再次安装同一精确版本时会校验并复用下载包。

```sh
jkv clean                              # 清理全部缓存
jkv clean downloads                    # 只清下载包
jkv clean downloads java 21.0.11+10-tem
jkv clean catalog java                 # 只清 Java 版本缓存
```

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

## 选源原则

详见 [docs/sources.md](docs/sources.md)。核心标准：国内主体长期维护、HTTPS、无需登录、目录或元数据可机器读取、当前仍同步稳定版。只有 GitHub Release、网盘、博客转存、需人工点击或版本长期滞后的生态暂不支持。

## 开发

每次 push 和 pull request 会执行：Linux、macOS、Windows × amd64、arm64 六目标交叉编译；六种对应原生 GitHub runner 上执行单测、`go vet`、二进制冒烟测试，以及 Unix/PowerShell 安装器集成测试。Linux/Windows ARM64 runner 目前是 GitHub public preview。

```sh
go test ./...
go run ./cmd/jkv list java
go build ./cmd/jkv
```

从源码目录安装（需要 Go）：

```sh
./install.sh
```

Windows PowerShell：

```powershell
.\install.ps1
```
