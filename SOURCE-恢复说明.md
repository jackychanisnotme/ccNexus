# AINexus native-compatible fallback 源码恢复包

这个目录是为此前迁移包中的 `AINexus.app` 补交的源码恢复包。

## 对应二进制

此前迁移包内的可执行文件 SHA256：

```text
4a7ee2dfc8c6884455bf9c423fd4c2357c83ea58176103e3aa59f71a10bcc9b1
```

恢复来源：

- 上游仓库：`https://github.com/lich0821/AINexus.git`
- 恢复基线提交：`8f503ba` (`Master lsf (#125)`)
- 本机会话日志中保留的补丁/文件片段

注意：这是从上游源码 + 会话补丁/片段重建出来的源码树，用于后续可靠修改和重新构建；它不是从 `.app` 二进制反编译得到的，也不包含用户配置数据库或 API key。

## 已包含的主要改动

- endpoint-level thinking/reasoning 配置路径（OpenAI / OpenAI2 Claude Code 转换路径）。
- request-level observability：`X-AINexus-Request-ID` / `X-AINexus-Endpoint` / `X-AINexus-Attempt` 与请求级日志字段。
- request-local fallback：普通请求失败不会修改全局 `currentIndex`。
- rate-limited backoff。
- `quota_exhausted` 识别、立即 request-local failover、endpoint cooldown。
- 相关回归测试文件。

## 补丁文件

同目录下有完整补丁：

```text
patches/AINexus-native-compatible-fallback-reconstructed-source.patch
```

可用于查看相对上游 `8f503ba` 的修改范围。

## 已验证

已在本机运行并通过：

```bash
go test -mod=mod ./internal/transformer/convert ./internal/proxy ./internal/config ./internal/storage ./internal/service -count=1
```

完整 `go test -mod=mod ./...` 在未生成前端 bundle 时会因以下预期原因失败：

```text
cmd/desktop/main.go:19:12: pattern all:frontend/dist: no matching files found
```

如需完整测试/构建桌面端，请先生成前端：

```bash
cd cmd/desktop/frontend
npm install
npm run build
cd ../../..
go test -mod=mod ./... -count=1
```

Wails 构建：

```bash
cd cmd/desktop
wails build -clean
```

## 不包含内容

不包含：

- `~/.AINexus/ainexus.db`
- API key / token pool 凭据
- 用户本机运行日志

