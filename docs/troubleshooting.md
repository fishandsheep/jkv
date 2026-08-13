# 故障排查

先运行：

```sh
jkv doctor
jkv current
jkv mirror status
```

## 命令不存在

确认 `JKV_DIR/bin` 在 `PATH`，并重新加载 shell：

```sh
eval "$(jkv init bash)"       # Bash
eval "$(jkv init zsh)"        # Zsh
jkv init fish | source        # Fish
```

PowerShell 使用 `Invoke-Expression ((jkv init powershell) -join "`n")`。

## 下载或镜像失败

jkv 尊重系统 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY` 和系统 CA。检查代理是否允许目标域名。`jkv list <candidate> --refresh --verbose` 跳过本地 catalog 缓存。

失败不会静默切换 Java 生态工具到 GitHub。安装 jkv 本身时，国内制品传输失败才会使用 GitHub 后备；SHA-256 不匹配会立即终止。

`jkv self update` 只管理安装器放在 `$JKV_DIR/bin/jkv[.exe]` 的稳定版。若提示“当前二进制不由 JKV_DIR 管理”或“开发版不能自行更新”，请使用原安装方式升级。`v0.0.1` 需先运行一次 `v0.0.2` 安装器。

## 安装损坏

```sh
jkv repair <candidate> <version>
```

修复使用临时目录和原子替换；失败时保留原安装。并发操作由 `$JKV_DIR/locks` 中的跨进程锁串行化。

## 空间与缓存

```sh
jkv clean --dry-run
jkv clean downloads
jkv clean catalog
```

`--dry-run` 只列出将删除的路径。删除已安装版本使用 `jkv uninstall`。

在线 Catalog 下架的已安装版本仍由 `jkv list <candidate>` 显示，`AVAILABLE` 为 `-`、`SOURCE` 为 `local`。Catalog 和可信缓存都不可用时，`list` 仍报错，不会把本地目录伪装成可信 Catalog。

## 报告问题

普通缺陷使用 GitHub Issue，并附 `jkv version`、`jkv doctor --json`、系统/架构和可复现命令。安全问题按 [SECURITY.md](../SECURITY.md) 私密报告。
