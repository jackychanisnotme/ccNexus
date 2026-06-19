# AINexus Release Helpers

这些脚本用于第一阶段仓库可交付发布验证，不会替你上传真实站点、创建付款链接或绕过签名要求。

## 环境约定

macOS 公证需要：

- `APPLE_TEAM_ID`
- `APPLE_ID`
- `APPLE_APP_SPECIFIC_PASSWORD`
- 本机钥匙串中已有 `Developer ID Application` 证书

Windows 签名需要：

- `WINDOWS_CERTIFICATE_PFX`
- `WINDOWS_CERTIFICATE_PASSWORD`
- Windows SDK `signtool.exe`

## 常用流程

生成校验和：

```bash
scripts/release/generate-checksums.sh dist
```

macOS 签名和公证：

```bash
scripts/release/macos-notarize.sh cmd/desktop/build/bin/AINexus.app AINexus-v6.1.4-darwin-arm64.zip
```

Windows 签名：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/release/windows-sign.ps1 `
  -FilePath cmd\desktop\build\bin\AINexus.exe
```

Docker 镜像：

```bash
scripts/release/docker-build.sh 6.1.4
```

## 回滚规则

- 保留上一稳定版本的 Release asset。
- 下载页只切换到通过 smoke test 的版本。
- 若 macOS Gatekeeper、Windows 启动、Docker `/health` 或核心教程跑通失败，先回滚下载页链接。
- 回滚后记录失败平台、版本、用户影响和修复动作。
