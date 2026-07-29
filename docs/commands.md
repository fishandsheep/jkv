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
jkv home <candidate> [version]
jkv env [init|apply|clear]
jkv init <bash|zsh|fish|powershell>
jkv mirror <maven|gradle|status> [--apply]
jkv clean [downloads|catalog] [candidate] [version] [--dry-run]
jkv doctor
```

`JKV_REQUIRE_CHECKSUM=1` 等价于安装时使用 `--require-checksum`。上游未提供校验和时，默认明确警告；严格模式拒绝安装。
