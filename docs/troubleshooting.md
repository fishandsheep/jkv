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

## 报告问题

普通缺陷使用 GitHub Issue，并附 `jkv version`、`jkv doctor --json`、系统/架构和可复现命令。安全问题按 [SECURITY.md](../SECURITY.md) 私密报告。
