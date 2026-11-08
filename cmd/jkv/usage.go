package main

import "fmt"

func usage() {
	fmt.Print(`jkv (Java Kit Version) - 中国网络友好、跨平台 Java 生态及开发者工具版本管理器

用法:
  jkv list|ls [工具] [--refresh]      列出工具或在线版本
  jkv install|i <工具> [版本]         安装版本；支持 21-tem 等别名
  jkv repair <工具> <版本>            原子修复已安装版本
  jkv use|u <工具> <版本>             当前终端切换（需 shell hook）
  jkv default|d <工具> <版本>         设置默认版本
  jkv current|c [工具]                显示当前生效版本
  jkv uninstall|rm <工具> <版本>      卸载版本
  jkv self|s update|up                更新 jkv 自身
  jkv self|s uninstall|rm [--purge]   卸载 jkv 自身
  jkv home|h <工具> [版本]            输出安装目录
  jkv env|e [init|apply|clear]        项目 .jkvrc 环境
  jkv init|in <bash|zsh|fish|powershell> 输出 shell hook 和补全
  jkv mirror|m <maven|gradle|status>  配置国内依赖镜像
  jkv clean|cl                        清理本地缓存
  jkv doctor                          检查本机配置与运行环境
  jkv version|v                       显示版本
  jkv help                            显示帮助

任一命令可用 -h 或 --help 查看单项用法，例如 jkv install --help。

例:
  jkv list java
  jkv install java 21-tem
  jkv install java 17-dragonwell
  jkv install maven
  jkv mirror maven --apply
`)
}

// commandHelp holds the per-command usage text printed by `<command> -h`.
var commandHelp = map[string]string{
	"list": `用法: jkv list|ls [工具] [--refresh]

不带参数列出全部可安装工具；带工具名列出版本，标注已安装、默认与当前版本。
--refresh 忽略本地缓存，重新拉取签名 catalog。
`,
	"install": `用法: jkv install|i <工具> [版本] [--default] [--require-checksum]

安装指定版本；省略版本安装最新稳定版并自动设为默认。
版本支持前缀和发行商别名，如 21、21-tem、17-dragonwell、21-bisheng。
--default           安装后设为默认版本
--require-checksum  上游无 SHA-256 时拒绝安装
`,
	"repair": `用法: jkv repair <工具> <版本>

按安装元数据原子修复已损坏的安装目录，失败时保留旧版本。
`,
	"use": `用法: jkv use|u <工具> <版本> [--shell bash|zsh|fish|powershell]

切换当前终端的版本；需要 shell hook（jkv init），hook 会自动补 --shell。
`,
	"default": `用法: jkv default|d <工具> <版本> [--shell ...]

设置该工具的默认版本；新终端自动使用默认版本。
`,
	"current": `用法: jkv current|c [工具]

显示当前生效版本；带工具名只显示该工具，否则列出全部。
`,
	"uninstall": `用法: jkv uninstall|rm <工具> <版本>

卸载指定版本；版本支持前缀匹配，如 21 匹配 21.0.1-tem。
`,
	"self": `用法: jkv self|s update|up        更新 jkv 自身
      jkv self|s uninstall|rm [--purge] [--yes]

卸载保留已安装工具；--purge 删除整个 JKV_DIR，交互终端需确认，
非交互必须加 --yes。
`,
	"home": `用法: jkv home|h <工具> [版本]

输出某版本的安装目录；省略版本用默认版本。
`,
	"env": `用法: jkv env|e [init|apply|clear] [--shell ...]

不带参数输出默认版本的环境变量；init 在当前目录创建 .jkvrc；
apply 应用 .jkvrc；clear 输出撤销环境变量的命令。
`,
	"init": `用法: jkv init|in <bash|zsh|fish|powershell|pwsh>

输出 shell hook 与补全脚本，按提示写入 shell 配置后 jkv use 生效。
`,
	"mirror": `用法: jkv mirror|m <maven|gradle> [--apply]
      jkv mirror|m status

生成国内依赖镜像配置。不带 --apply 只预览；--apply 写入配置文件，
已有同名文件且内容不同时拒绝覆盖。
`,
	"clean": `用法: jkv clean|cl [--dry-run]
      jkv clean|cl downloads [工具] [版本] [--dry-run] [--older-than 24h]
      jkv clean|cl partials --older-than 24h

不带参数清理已下载的安装包缓存。downloads 只清安装包；
partials 清理中断下载（必须带 --older-than 防止删掉进行中的下载）。
可信 catalog 状态不会被清理。
`,
	"doctor": `用法: jkv doctor

检查目录权限、可信 catalog 状态与国内镜像可达性。
`,
	"version": `用法: jkv version|v

显示 jkv 版本。
`,
	"help": `用法: jkv help

显示总览帮助；任一命令加 -h 或 --help 查看单项用法。
`,
}

// printCommandHelp prints the usage text for a resolved command name.
func printCommandHelp(command string) bool {
	help, ok := commandHelp[command]
	if !ok {
		return false
	}
	fmt.Print(help)
	return true
}
