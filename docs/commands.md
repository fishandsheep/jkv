# 命令与机器接口

## 全局选项

- `--json`：输出单个 JSON 文档；适用于 `list`、`current`、`home`、`mirror status`、`clean`、`doctor`、`version`。
- `--quiet`：抑制普通进度信息；安全警告仍输出到 stderr。
- `--verbose`：输出诊断信息。

正常数据写 stdout，错误和警告写 stderr。JSON 字段使用 `snake_case`；新增字段向后兼容，既有字段在同一主版本内不改义。

## 退出码

| 退出码 | 含义 |
|---:|---|
| 0 | 成功 |
| 1 | 未分类错误 |
| 2 | 用法或不支持的输入 |
| 3 | 工具、版本或默认值不存在 |
| 4 | 网络或镜像错误 |
| 5 | 完整性或归档安全错误 |
| 6 | 本地状态冲突、拒绝覆盖或锁错误 |

## 常用命令

```text
jkv list [工具] [--refresh]
jkv install <工具> [version] [--require-checksum]
jkv repair <工具> <version>
jkv use <工具> <version>
jkv default <工具> <version>
jkv current [工具]
jkv uninstall <工具> <version>
jkv self update
jkv self uninstall [--purge] [--yes]
jkv home <工具> [version]
jkv env [init|apply|clear]
jkv init <bash|zsh|fish|powershell>
jkv mirror <maven|gradle|status> [--apply]
jkv clean [--dry-run]
jkv clean downloads [工具] [version] [--older-than 720h]
jkv clean partials --older-than 24h
jkv doctor
```

任一命令支持 `-h` / `--help` 输出单项用法。`install` 省略版本时安装最新稳定版并设为默认；版本前缀在多个发行商命中同一主版本时优先 Temurin（`21-tem` 等后缀可显式指定发行商）。`list <工具>` 默认显示 `VERSION/STATUS/AVAILABLE` 三列，`--verbose` 追加 `TIER/INTEGRITY/SOURCE`。`mirror` 不带 `--apply` 仅预览；`env clear` 输出撤销 jkv 环境变量的命令（`JKV_DIR` 保留）。

`jkv clean` 不带参数清理下载缓存；`clean downloads` 限定安装包；可信 catalog 状态（`cache/trusted-catalog.json`）不会被清理。

`JKV_REQUIRE_CHECKSUM=1` 等价于安装时使用 `--require-checksum`。上游未提供校验和时，默认明确警告；严格模式拒绝安装。

中断下载位于可识别的 `$JKV_DIR/partials/downloads`。只能用带年龄门槛的 `jkv clean partials --older-than 24h` 清理；清理会等待对应安装锁，避免删除仍在使用的断点文件。

`self` 可简写为 `s`，`update` 可简写为 `up`，自身 `uninstall` 可简写为 `rm`，并可混用。普通自身卸载只删除受管二进制和安装器拥有的 shell block，保留已安装版本（`$JKV_DIR/candidates`）、默认值、配置和缓存。`--purge` 删除整个 `JKV_DIR`；交互终端需确认，非交互环境必须同时传 `--yes`。root、HOME 或 HOME 祖先目录始终拒绝。

版本列表只来自签名 Catalog；`--verbose` 不影响 JSON 输出。`list --json` 的每个版本包含 `support_tier`、`integrity_level`、`installed`、`default`、`current`、`in_catalog`。Catalog 中版本的 `in_catalog` 为 `true`。仅本地版本为 `in_catalog=false`、`installed=true`、`availability_known=false`、`availability_status="installed-only"`。`integrity_level` 通常为 `checksum` 或 `https-only`；元数据损坏的仅本地版本为 `unknown`。
