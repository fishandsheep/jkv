# 发布流程

## 前置检查

1. 更新 `CHANGELOG.md`，确认 core provider live 检查通过。
2. 使用工作流固定的 Go 1.26.5（或更新安全补丁版）运行 `gofmt`、`go vet`、`staticcheck`、`govulncheck`、完整测试与六目标交叉编译。`go.mod` 的 1.24 是源码兼容基线，不是发布工具链版本。
3. 创建公开 CNB 仓库后，配置 GitHub Variables：`CNB_JKV_REPOSITORY=<CNB 组织>/<CNB 仓库>` 与 `JKV_CN_DOWNLOAD_BASE=https://cnb.cool/<CNB 组织>/<CNB 仓库>/-/releases/download`。后者必须严格等于该仓库的 CNB Release download 根；不含 tag。
4. 配置 GitHub Secret `CNB_JKV_TOKEN`。它必须是具备该 CNB 仓库 Release 创建和附件上传权限的访问令牌；不要使用只读部署令牌。

## 发布

当前发布基线为 `v0.0.1`；后续推送 `v*` tag。工作流构建六个平台二进制，为每个制品生成 SHA-256、SPDX JSON SBOM 和 GitHub artifact attestation；随后创建 GitHub 与 CNB 同名草稿 Release，上传全部附件并校验每个文件的名称、大小、SHA-256，最后发布两端并从公开 URL 再逐文件比对。含 `-` 的 tag 标记为 prerelease。

CNB URL 固定为 `https://cnb.cool/<CNB 组织>/<CNB 仓库>/-/releases/download/<tag>/<asset>`。安装器会写入该 tag 的 CNB 下载基址；国内传输失败才回退同一 tag 的 GitHub 固定地址，不使用可能漂移的 `latest`。已发布 CNB Release 不允许覆盖；失败会保留草稿供排查。

## 验证

- 从 CNB Release 脚本地址在至少 Linux amd64、macOS arm64、Windows amd64 执行干净安装。
- 断开 GitHub 可达性，确认国内安装成功。
- 让国内地址返回传输错误，确认 GitHub 后备成功。
- 返回错误 SHA-256，确认安装终止且旧二进制未被替换。
- 检查 `jkv version`、SBOM、attestation 和校验文件。

## 回滚

不覆盖已发布 tag 或 CNB Release。标记有问题的 Release，重新发布递增版本。若安装脚本本身有问题，发布新 tag，不修改历史附件。已泄露的 CNB 访问令牌立即吊销并轮换。
