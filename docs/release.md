# 发布流程

## 前置检查

1. 更新 `CHANGELOG.md`，确认 core provider live 检查通过。
2. 使用工作流固定的 Go 1.26.5（或更新安全补丁版）运行 `gofmt`、`go vet`、`staticcheck`、`govulncheck`、完整测试与六目标交叉编译。`go.mod` 的 1.24 是源码兼容基线，不是发布工具链版本。
3. 推荐让仓库变量 `JKV_CN_DOWNLOAD_BASE` 指向国内 HTTPS 公开目录。未配置时安装脚本直接使用同一 Tag 的 GitHub Release，不阻塞发布。
4. 若发布工作流负责国内上传，配置 `JKV_CN_S3_URI`、endpoint/region 和最小权限凭证；未配置时跳过国内上传。

## 发布

推送 `v*` tag。工作流构建六个平台二进制，为每个制品生成 SHA-256、SPDX JSON SBOM 和 GitHub artifact attestation；随后先上传国内对象存储，再创建 GitHub 后备 Release。含 `-` 的 tag 标记为 prerelease。

配置国内源时，目录必须按 tag 隔离且不可覆盖，安装脚本会写入该 tag 的国内下载基址。未配置时脚本使用同一 tag 的 GitHub 固定地址，不使用可能漂移的 `latest`。对象存储公开读取、关闭目录写入、启用版本控制或保留策略。

## 验证

- 从国内脚本地址在至少 Linux amd64、macOS arm64、Windows amd64 执行干净安装。
- 断开 GitHub 可达性，确认国内安装成功。
- 让国内地址返回传输错误，确认 GitHub 后备成功。
- 返回错误 SHA-256，确认安装终止且旧二进制未被替换。
- 检查 `jkv version`、SBOM、attestation 和校验文件。

## 回滚

不覆盖已发布 tag。撤回有问题的国内 tag 目录并在 Release 标记问题；重新发布递增版本。若安装脚本本身有问题，恢复上一版脚本对象并保留其校验文件。已泄露的对象存储凭证立即吊销并轮换。
