# Codex Token Pool `max_output_tokens` 兼容设计

## 背景与根因

AI Agent 通过 AINexus 的 `/v1/responses` 调用 Codex Token Pool 时，会发送标准 OpenAI Responses 参数 `max_output_tokens`。当前 `openai2` Responses 直通转换保留该字段，随后请求被发送到 `chatgpt.com/backend-api/codex/responses`。该 Codex 后端拒绝此参数并返回 HTTP 400，导致 Agent 在 preflight 阶段失败。

## 方案比较

1. **仅在 Codex 后端请求边界移除字段（采用）**：在构建 Codex Responses 请求时删除 `max_output_tokens`。范围最小，不改变普通 OpenAI Responses 或第三方端点行为。
2. 在所有 `openai2` 转换中移除字段：实现简单，但会破坏支持该标准参数的其他 Responses 端点。
3. 收到上游 400 后重试：会增加延迟和一次无效请求，并依赖错误文本，稳定性较差。

## 设计

扩展现有 `ensureCodexResponsesPayload` 边界规范化逻辑。仅当目标基础 URL 为 Codex 后端且目标路径为 Responses 时，该逻辑才会运行；它在继续设置 `store=false`、`stream=true` 和缺省 `instructions` 的同时删除顶层 `max_output_tokens`。

此变更不修改客户端请求、不修改端点配置或数据库，也不改变其他认证模式、转换器及上游端点的请求体。Codex 后端继续使用其自身默认输出限制。

## 错误处理与兼容性

无效 JSON、空请求体和数组请求体维持当前原样返回行为。普通 API Key OpenAI Responses 端点仍完整接收 `max_output_tokens`。旧客户端无需升级配置，旧数据库无需迁移。

## 测试

- 回归测试：Codex payload 中的 `max_output_tokens` 被移除，其余字段保留。
- 边界测试：非 Codex Responses 请求不经过该规范化逻辑，标准字段保持不变。
- 运行 `internal/proxy` 测试及全量 Go 测试，确认无回归。
