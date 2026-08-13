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
| 3 | candidate、版本或默认值不存在 |
| 4 | 网络或镜像错误 |
| 5 | 完整性或归档安全错误 |
| 6 | 本地状态冲突、拒绝覆盖或锁错误 |

## 常用命令

```text
jkv list [candidate] [--refresh]
jkv install <candidate> [version] [--require-checksum]
jkv repair <candidate> <version>
jkv use <candidate> <version>
jkv default <candidate> <version>
jkv current [candidate]
jkv uninstall <candidate> <version>
jkv self update
jkv self uninstall [--purge] [--yes]
jkv home <candidate> [version]
jkv env [init|apply|clear]
jkv init <bash|zsh|fish|powershell>
jkv mirror <maven|gradle|status> [--apply]
jkv clean [downloads|catalog|partials] [candidate] [version] [--dry-run] [--older-than 720h]
jkv doctor
```

`JKV_REQUIRE_CHECKSUM=1` 等价于安装时使用 `--require-checksum`。上游未提供校验和时，默认明确警告；严格模式拒绝安装。

中断下载位于可识别的 `$JKV_DIR/partials/downloads`。只能用带年龄门槛的 `jkv clean partials --older-than 24h` 清理；清理会等待对应安装锁，避免删除仍在使用的断点文件。

`self` 可简写为 `s`，`update` 可简写为 `up`，自身 `uninstall` 可简写为 `rm`，并可混用。普通自身卸载只删除受管二进制和安装器拥有的 shell block，保留 candidates、默认值、配置和缓存。`--purge` 删除整个 `JKV_DIR`；交互终端需确认，非交互环境必须同时传 `--yes`。root、HOME 或 HOME 祖先目录始终拒绝。

`list --json` 的每个版本包含 `support_tier`、`integrity_level`、`installed`、`default`、`current`、`in_catalog`。Catalog 中版本的 `in_catalog` 为 `true`。仅本地版本为 `in_catalog=false`、`installed=true`、`availability_known=false`、`availability_status="installed-only"`。`integrity_level` 通常为 `checksum` 或 `https-only`；元数据损坏的仅本地版本为 `unknown`。
