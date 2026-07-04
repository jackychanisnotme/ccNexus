# AINexus 端点通讯、切换逻辑与适配性只读审查报告

审查日期：2026-06-30  
审查范围：本地项目 AINexus 代码；重点覆盖端点通讯、端点配置、端点轮换/故障切换、Token Pool、Claude/Codex/OpenClaw/Hermes 适配、OpenAI Responses/Claude/Gemini 转换、流式处理，以及与 ccNexus 的逻辑和性能差异。  
审查方式：只读静态审查，未修改项目代码。

## 1. 总体结论

AINexus 相对 ccNexus 不是整体更弱。它在很多方向上是增强版，例如 Claude OAuth Token Pool、Codex reset credits / WebSocket、端点级代理、Hermes/OpenAI Responses 流式补偿、OpenClaw/Hermes 配置检查、请求观测和运行时状态等。

但增强能力也带来了更复杂的状态机、更大的回归面，以及部分热路径性能开销。当前最值得优先处理的问题集中在 Token Pool 重试边界、通用 token pool 被误判为 Codex pool、SSE 解析规范性、OpenAI Responses 与 Claude/Gemini 的工具调用和 reasoning 映射，以及 Claude/OAuth 默认行为。

## 2. 优先级摘要

### P0 / P1 建议优先修复

1. 指定 endpoint 时 Token Pool 最多只重试 3 次，池内好 token 可能用不到。
2. 通用 `token_pool` 容易被当成 Codex Pool，非 Codex Responses endpoint 被注入 Codex 专用头。
3. SSE 多行 `data:` 解析不符合规范，可能丢失长 JSON、tool arguments 或错误 payload。
4. Claude → OpenAI Responses 默认强制 `tool_choice=required`，可能破坏 Claude 默认工具语义。
5. Responses stream 如果只在 `response.completed` 里带 `function_call`，会丢工具调用。
6. 删除单个 credential 会误删同 endpoint 下所有 credential usage。

### P2 建议随后修复

1. OpenAI Responses → Claude reasoning 丢失。
2. Gemini stream 的 `thought:true` 被当成普通正文。
3. Claude OAuth Token Pool 默认模型为 `opus` 且会覆盖客户端模型。
4. OpenAI Responses → Claude 默认启用 Claude extended thinking。
5. Claude API key 模式同时发送 `x-api-key` 和 `Authorization: Bearer`。
6. Gemini 请求无条件追加 `alt=sse`。
7. Server Web UI/API 不能清空 endpoint 的 `model` / `thinking`，且省略 `enabled` 可能意外禁用 endpoint。

## 3. 确定问题

### 3.1 删除单个 credential 会误删同端点所有 credential usage

位置：`internal/storage/credentials.go:329-345`

`DeleteEndpointCredential` 中执行：

```go
DELETE FROM credential_usage WHERE credential_id=? OR endpoint_name=?
```

触发条件：在 Web UI / Desktop 删除某个 endpoint 下的单个 credential。

影响：同一 endpoint 下所有 credential 的 usage 都会被删除，而不只是当前 credential 的 usage。Token Pool 账号级统计、失败分析、使用量展示会被污染。

建议修复：单 credential 删除只按 `credential_id=?` 清理；按 `endpoint_name` 清理应只放在删除整个 endpoint 的路径。

### 3.2 指定 endpoint 时 Token Pool 最多只重试 3 次

位置：`internal/proxy/proxy.go:881-884`、`internal/proxy/proxy.go:1867-1907`

当前逻辑先用 `computeMaxRetries(requestEndpoints)` 按 token pool 大小计算重试数，但如果客户端通过 header、query 或 `@endpoint` 指定 endpoint，就强制：

```go
maxRetries = endpointSlowFailoverAttempts
```

触发条件：指定某个 Token Pool endpoint；该 endpoint 下前几个 credential 失效或被 401/403 拒绝，但后面仍有可用 credential。

影响：普通请求可能会按池大小尝试更多 credential，但指定 endpoint 时最多尝试 3 次。大 token 池里第 4 个及之后的可用 token 可能完全用不到，最终误报 “All endpoints failed”。

建议修复：指定 endpoint 时不要覆盖 token-pool extra retries；应按该 endpoint 的 usable credential 数计算重试上限，或按 credential 选择结果循环直到池内可用 credential 耗尽。

### 3.3 通用 `token_pool` 会被当成 Codex Pool 注入 Codex 头

位置：`internal/proxy/request.go:249-258`、`internal/proxy/request.go:325-328`

当前 `isCodexProviderType` 把空 provider type 也视为 Codex：

```go
return p == "" || p == storage.ProviderTypeCodex
```

而 `applyCodexCredentialHeaders` 只要 provider type 是 Codex、路径是 `/responses`，就会注入 Codex 专用头：

```text
Version
Session_id
User-Agent: codex_cli_rs/...
Connection: Keep-Alive
Originator: codex_cli_rs
Chatgpt-Account-Id
```

触发条件：创建普通 `authMode=token_pool` 的 OpenAI/OpenAI2 兼容 endpoint，导入 credential 时未显式传 provider type，随后请求 `/v1/responses`。

影响：非 ChatGPT Codex 后端会收到 Codex 专用头，可能导致认证失败、路由异常、WAF 拒绝或 provider 行为偏移。

建议修复：为通用 token pool 增加独立 provider type，例如 `generic` 或 `api_key`；Codex 专用头应同时要求 `authMode=codex_token_pool`，或目标 base URL 确认为 `https://chatgpt.com/backend-api/codex`。

### 3.4 Server Web UI/API 更新 endpoint 时无法清空 `model` / `thinking`

位置：`cmd/server/webui/api/endpoints.go:358-363`

触发条件：已有 endpoint 的 `model` 或 `thinking` 非空，用户/API 传空字符串希望恢复为“客户端模型”或 provider 默认。

影响：`if req.Model != ""`、`if req.Thinking != ""` 会跳过空值更新，旧值继续保留。代理层后续仍会通过 endpoint model 覆盖请求模型。

建议修复：PUT request struct 中把 `Model`、`Thinking` 改为 `*string`，区分“未传字段”和“明确传空”。明确传空时写入空字符串并走 normalization。

### 3.5 Server Web UI 的 endpoint 测试与真实代理路径不一致

位置：`cmd/server/webui/api/testing.go:83-95`、`cmd/server/webui/api/testing.go:112-130`、`cmd/server/webui/api/testing.go:199-218`

触发条件：测试 Claude endpoint 或 `transformer=openai2` endpoint。

影响：Claude 测试硬编码 `claude-3-5-sonnet-20241022`，忽略 endpoint 配置模型，容易出现“测试失败但真实请求可用”的假阴性。OpenAI Responses 测试发 `/v1/responses`，但解析结果时仍按 Chat Completions 的 `choices[0].message.content` 读取；正常 Responses 返回只能显示 raw JSON。

建议修复：Claude 测试优先使用 `endpoint.Model`，为空时使用统一默认模型；`openai2` 单独解析 `output_text` 或 `output[].content[].text`。

### 3.6 Server Web UI 拉取模型对 Token Pool / Codex / Claude OAuth 不可用

位置：`cmd/server/webui/ui/js/components/endpoints.js:470-492`、`cmd/server/webui/api/testing.go:294-311`

触发条件：在 `token_pool`、`codex_token_pool` 或 `claude_oauth_token_pool` endpoint 上点击 Fetch Models。

影响：前端要求 `apiKey` 非空；后端请求体也只有 `apiUrl/apiKey/transformer/proxyUrl`，无法按 endpoint name 选 token pool credential，也没有 Codex 模型 registry 逻辑。

建议修复：Fetch Models 请求带 `endpointName` 和 `authMode`；后端复用 proxy/desktop 中的 credential 解析与 Codex/Claude OAuth 特殊处理。

### 3.7 SSE 多行 `data:` 解析不符合规范

位置：`internal/transformer/convert/common.go:32-40`

当前 `parseSSE` 遍历每一行时，遇到 `data:` 就直接覆盖 `jsonData`。标准 SSE 允许一个 event 有多行 `data:`，应拼接所有 data 行。

触发条件：上游把一个 SSE event 拆成多行 `data:`，常见于长 JSON、长 tool arguments 或错误 payload。

影响：前面的 `data:` 行被覆盖，只解析最后一行，导致 JSON 截断、delta 丢失、工具参数损坏或错误 payload 无法识别。

建议修复：按 SSE 规范累积所有 `data:` 行，用 `\n` 拼接后再解析；保留 `[DONE]` 特殊处理。

### 3.8 Gemini 流式 `thought` 被转换成普通 Claude 正文

位置：`internal/transformer/convert/claude_gemini.go:456-466`

触发条件：Gemini stream part 同时包含 `text` 且 `thought:true`。

影响：Gemini reasoning/thinking 会以 Claude `text_delta` 输出到正文，污染最终回答，也破坏 Claude Code / Claude Messages 对 thinking 与 text 的区分。

建议修复：流式转换中先判断 `part.Thought`，单独开启、输出、关闭 Claude `thinking` content block；非 thought 文本再走 text block。

### 3.9 Claude → OpenAI Responses 默认把工具选择强制成 `required`

位置：`internal/transformer/convert/claude_openai2.go:84-95`

触发条件：Claude 请求带 `tools`，但未显式传 `tool_choice`，且历史中没有 `tool_result`。

影响：Claude 默认语义应接近 `auto`，但转换后模型被强制调用工具。结果可能是本可直接回答的问题进入无效工具循环，或被不支持强制工具的 provider 拒绝。

建议修复：默认保持 `auto`；只有 Claude `tool_choice:any` 或指定工具时才映射为 Responses `required` / function choice。若某些 Codex/OpenClaw 端点确实需要强制工具，应做成 endpoint/provider 兼容开关。

### 3.10 OpenAI Responses → Claude 丢失 reasoning

位置：`internal/transformer/convert/claude_openai2.go:325-364`、`internal/transformer/convert/claude_openai2.go:643-764`

触发条件：Responses 非流式 `output` 中有 `type:"reasoning"`，或流式事件包含 `response.reasoning_text.delta` / `response.reasoning_summary_text.delta`。

影响：Claude 客户端收不到 thinking block；长 reasoning 场景下也缺少进度信号，可能表现为客户端长时间无可见输出。

建议修复：非流式处理 `item.Type=="reasoning"` 并转 Claude `thinking` block；流式 switch 增加 reasoning delta/done 事件映射为 Claude `thinking_delta` / block stop。

### 3.11 Responses stream 只在 `response.completed` 中带 `function_call` 时会丢工具调用

位置：`internal/transformer/convert/claude_openai2.go:721-736`、`internal/transformer/convert/openai_openai2.go:1082-1110`

触发条件：上游 stream 没有提前发送 `response.output_item.added/done`，而是在最终 `response.completed.response.output` 中才包含 `function_call` item。

影响：转 Claude 时不会发 `tool_use`，`stop_reason` 可能误判为 `end_turn`；转 Chat Completions 时不会发 `delta.tool_calls`，`finish_reason` 可能保持 `stop`。

建议修复：在 `response.completed` 分支遍历 `evt.Response.Output` 时同时补处理 `function_call`，发出缺失的 tool block/tool_calls，并把 stop/finish reason 改为 `tool_use` / `tool_calls`。

### 3.12 Responses → Gemini 工具结果可能丢失函数名

位置：`internal/transformer/convert/openai2_gemini.go:368-390`

触发条件：Responses `function_call` item 只有 `id`，没有 `call_id`，后续 `function_call_output` 用该 id 关联。

影响：`callIDToName` 不记录 id fallback，后续 Gemini `functionResponse.name` 为空，Gemini 上游可能拒绝或无法关联工具结果。

建议修复：建立映射时使用 `call_id`，为空再 fallback 到 `id`；与 `OpenAI2ReqToClaude` 的处理保持一致。

## 4. 潜在风险

### 4.1 Claude OAuth Token Pool 默认模型为 `opus`，且会覆盖客户端模型

位置：`internal/config/config.go:39-41`、`internal/config/config.go:118-123`、`internal/proxy/request.go` 的 endpoint model 覆盖路径

触发条件：创建 Claude OAuth Token Pool endpoint 但未手动填写模型。

影响：配置规则会写入 `model:"opus"`，代理层又会强制覆盖请求体模型。如果 Anthropic API 或兼容网关不接受该短别名，请求会失败；即便接受，也会把客户端选择的 Sonnet/Haiku 改成 Opus，带来成本和行为偏差。

建议修复：默认留空以尊重客户端模型，或使用明确的官方模型 ID；UI 明确提示“endpoint model 非空会覆盖客户端模型”。

### 4.2 Claude API key 模式同时发送 `x-api-key` 和 `Authorization: Bearer`

位置：`internal/proxy/request.go:220-228`

触发条件：普通 Claude API key endpoint 请求 Anthropic 或 Claude 兼容网关。

影响：部分网关可能优先解析 `Authorization`，把 API key 当 OAuth bearer token 处理并拒绝。

建议修复：Claude API key 模式只设置 `x-api-key`；仅 Claude OAuth credential 设置 `Authorization: Bearer`。

### 4.3 云备份策略只拦 Claude OAuth，不拦 Codex refresh token

位置：`internal/service/backup.go:167-187`、`internal/storage/sqlite.go:1283-1319`

触发条件：存在 Codex Token Pool credential，`IncludeLoginCredentials=false`，执行 WebDAV/S3 备份。

影响：策略只检查 `ProviderTypeClaudeOAuth`；`CreateBackupCopy` 只清理 `app_config`，不会清理 `endpoint_credentials`，Codex `access_token/refresh_token/id_token` 仍可能进入云备份。

建议修复：所有登录态 provider 至少包括 Codex 和 Claude OAuth 都应受 opt-in 控制；未 opt-in 时从备份副本删除或脱敏 `endpoint_credentials`、`credential_rate_limits`、`credential_usage`。

### 4.4 OpenAI/Claude → Responses 丢弃输出 token 上限

位置：`internal/transformer/convert/claude_openai2.go:68-70`、`internal/transformer/convert/openai_openai2.go:57-58`

触发条件：客户端设置 Claude `max_tokens` 或 OpenAI Chat `max_tokens/max_completion_tokens`，目标是 Responses API。

影响：转换后没有 `max_output_tokens`，上游可能按默认上限输出，导致成本增加、响应过长，或客户端期望的截断语义失效。

建议修复：标准 Responses 端点映射到 `max_output_tokens`；对不兼容 provider 用 endpoint/provider 兼容配置剥离，而不是通用转换层无条件丢弃。

### 4.5 Responses `failed/cancelled` 非流式结果会被包装成 Claude 成功消息

位置：`internal/transformer/convert/claude_openai2.go:325-364`、`internal/transformer/convert/common.go:671-684`

触发条件：上游 HTTP 200，但 Responses body 为 `status:"failed"` / `cancelled"`，且 `output` 为空或包含错误信息。

影响：转换器只遍历 `message/function_call`，最终 `mapOpenAI2ResponseStopReason` 落到 `end_turn`，下游收到空的成功 Claude message，真实错误丢失。

建议修复：扩展 `OpenAI2Response` 结构以解析 `error`；遇到 failed/cancelled 应返回转换错误或生成下游协议的错误响应。

### 4.6 OpenAI Responses → Claude 默认强制开启 Claude extended thinking

位置：`internal/transformer/convert/claude_openai2.go:156-175`、`internal/transformer/convert/claude_openai2.go:202-214`

触发条件：OpenAI Responses 客户端请求 Claude endpoint，且请求未显式传 `reasoning`。

影响：`claudeThinkingBudget` 默认返回 8192，导致所有此类请求都带 Claude `thinking`，并删除 `temperature`；会改变语义、延迟、成本，也可能让不支持 thinking 的模型或网关失败。

建议修复：只有客户端显式请求 reasoning 或 endpoint 配置启用时才注入 thinking；默认保持普通 Claude Messages 语义。

### 4.7 Gemini 请求无条件追加 `alt=sse`

位置：`internal/proxy/request.go:215-219`、`internal/proxy/request.go:155-162`

触发条件：Gemini transformer 的非流式 `generateContent` 请求。

影响：代码对 Gemini 所有请求都加 `alt=sse`，但非流式 path 是 `:generateContent`；部分 Gemini 兼容服务可能返回 SSE 或拒绝该 query，导致非流式代理走错处理路径。

建议修复：仅在目标 path 是 `:streamGenerateContent` 或 payload `stream=true` 时设置 `alt=sse`。

### 4.8 Server endpoint create/update API 省略 `enabled` 会把 endpoint 关闭

位置：`cmd/server/webui/api/endpoints.go:115-125`、`cmd/server/webui/api/endpoints.go:257-271`、`cmd/server/webui/api/endpoints.go:356`

触发条件：外部脚本调用 POST/PUT `/api/endpoints` 做部分更新，未传 `enabled`。

影响：Go bool 零值为 `false`，新建或更新 endpoint 会被意外禁用。当前 Web UI 全量提交能规避，但 API 层不安全。

建议修复：请求结构用 `*bool`；create 默认 true，update 仅字段存在时修改。

### 4.9 Gemini → Responses 流式工具调用 output_index 可能从 1 开始

位置：`internal/transformer/convert/openai2_gemini.go:213-232`

触发条件：Gemini stream 第一个输出就是 `functionCall`，前面没有文本 message item。

影响：先生成 `call_0`，随后自增并用 `ctx.ToolCallCounter` 作为 `output_index`，第一个工具 item 可能是 `output_index:1`，没有 0；严格 Responses 客户端可能认为 output item 序列不连续。

建议修复：单独维护 Responses output index；无文本时第一个 tool item 应使用 0，有文本 message item 时 tool 从 1 开始。

## 5. Codex / Claude / OpenClaw / Hermes 适配重点

### 5.1 Codex

Codex 方向最需要优先处理两点。第一，通用 `token_pool` 被误判为 Codex pool 并注入 Codex headers，这会影响非 Codex Responses provider。第二，指定 endpoint 时 Token Pool 最多只试 3 个 credential，这会显著降低大 token 池可用性。

AINexus 相对 ccNexus 在 Codex 能力上更强，包含 Codex client version/user-agent 常量化、Codex auth manager、reset credits、WebSocket、credential refresh singleflight、rate limit/credit 状态等。但这些也依赖 ChatGPT 后端私有协议，维护风险高于 ccNexus 较简单的 HTTP 转发。

### 5.2 Claude

Claude 方向主要风险是 API key 与 OAuth header 语义混用、Claude OAuth 默认模型 `opus`、Responses → Claude 默认开启 thinking。这些问题可能造成兼容性失败、成本增加或模型行为偏差。

建议把 Claude API key 与 Claude OAuth 明确拆开：API key 只走 `x-api-key`，OAuth credential 只走 `Authorization: Bearer`。Claude OAuth endpoint 默认模型建议留空，除非用户显式设置 endpoint model。

### 5.3 OpenClaw / Hermes

OpenClaw/Hermes 的主要风险集中在 OpenAI Responses 兼容层：默认强制 `tool_choice=required`、reasoning 丢失、completed-only function_call 丢失、SSE 多行 data 解析不完整。这些问题会在工具调用密集、流式长 reasoning、SDK 严格校验 Responses event 顺序的场景里放大。

AINexus 已经比 ccNexus 公开实现多了 OpenClaw/Hermes 本地配置检查、Responses 首事件补偿和 duplicate `response.created` 过滤。但这类补偿不能替代协议语义完整性，仍建议补齐 reasoning、function_call 和 SSE 规范处理。

## 6. 与 ccNexus 对比下的逻辑与性能劣势

### 6.1 入站连接监控增加每请求热路径开销

ccNexus 的 proxy server 路径更直接。AINexus 在 `internal/proxy/proxy.go:185-196` 包了一层 inbound connection tracking，并在 `internal/proxy/inbound_connections.go` 维护 map、mutex、快照和事件。

这对桌面监控有价值，但每个请求进入/退出都会产生锁、map 写入、可能的事件触发。高并发或低延迟代理场景下，尾延迟可能不如 ccNexus 简单路径。

建议：提供配置项允许关闭实时入站连接监控；或改为采样/无锁计数/分片 map，降低热路径成本。

### 6.2 Listener 热重绑逻辑更复杂

ccNexus 主要是简单 start/stop listener。AINexus 增加 LAN/local rebind、`listenerMu`、`serverStopCh`、`serverFatalCh` 等状态。

这是功能增强，但故障面更大，尤其是旧 listener 关闭、新 listener 失败恢复、并发 rebind 这类生命周期路径。

建议：为 rebind 增加并发和失败恢复测试，覆盖新端口绑定失败、旧 listener 关闭后恢复、重复 rebind、server shutdown 竞态等场景。

### 6.3 剥离 `endpoint` / `ep` query 参数降低透明代理兼容性

ccNexus 构造上游 URL 时更透明地保留原 query。AINexus 为了本地指定 endpoint，会删除 `endpoint` 和 `ep` 参数。

这通常更安全，能避免本地路由参数泄漏到上游。但如果上游 API 本身合法使用 `endpoint` 或 `ep` 参数，AINexus 会破坏请求透明性。

建议：把本地指定 endpoint 的 query 参数改为更不易冲突的命名，例如 `ainexus_endpoint`，或仅在明确启用本地路由参数时剥离。

### 6.4 启用 proxy 时仍是每请求新建 transport

ccNexus 也有类似问题，但 AINexus 支持端点级 proxy，实际触发场景更多。命中 proxy 后每请求创建 transport/client，会削弱连接池复用，HTTPS/proxy 握手成本更高。

建议：按 proxy URL、TLS 配置、超时等 key 缓存 transport，并在配置变更时清理缓存。这样能提升高并发和长连接场景下的性能。

### 6.5 适配分支更多，维护和回归风险更高

AINexus 增加了 Claude OAuth、Poe、Codex WebSocket/reset credits、Hermes Responses 首事件补偿、OpenClaw/Hermes inspector 等能力。这不是功能劣势，但确实扩大了回归面。

建议：把协议转换、流式状态机、Token Pool、Codex 私有协议、OpenClaw/Hermes 客户端兼容性拆成更系统的测试矩阵。

## 7. 建议测试清单

### 7.1 基础回归测试

建议运行：

```bash
go test ./internal/proxy ./internal/transformer/... ./cmd/server/webui/api -count=1
```

验证重点：端点选择/故障切换、流式聚合、Responses/Chat/Claude/Gemini 转换现有用例是否仍通过。

### 7.2 建议新增边界单测

1. 多行 `data:` SSE event 应正确拼接。
2. 指定 endpoint + Token Pool 超过 3 个 credential，前 3 个失败、第 4 个成功时应成功。
3. 普通 `token_pool + openai2` endpoint 不应注入 Codex headers。
4. 删除一个 credential 后，其他 credential usage 不应丢失。
5. Claude tools 未指定 `tool_choice` 时应保持 `auto`。
6. Gemini stream `thought:true` 不应进入普通正文。
7. `response.completed.response.output` 只含 `function_call` 时也应生成 Claude `tool_use`。
8. Responses `reasoning` 非流式和流式都应映射到 Claude thinking。
9. Responses → Gemini 的 `function_call` 只有 `id` 没有 `call_id` 时，后续 function output 应能找到函数名。

### 7.3 真实客户端兼容性验证

建议用真实 OpenClaw / Hermes / OpenAI Python SDK / OpenAI JS SDK 验证以下场景：

1. 上游缺少 `response.completed`。
2. 上游先发 heartbeat/comment。
3. 工具调用流中断。
4. `response.completed` 才包含 function call。
5. 长 reasoning 流式输出。
6. synthetic `response.completed` 对 OpenClaw/Hermes 是否兼容。

### 7.4 Codex / ChatGPT 后端验证

建议验证：

1. `codex_token_pool` HTTP streaming。
2. Codex WebSocket 可用/不可用 fallback。
3. Refresh token 过期、复用、刷新成功三种路径。
4. 非 Codex `token_pool + openai2` endpoint 是否因 Codex headers 被拒。
5. 指定 endpoint 下大 token pool 的完整轮换。

## 8. 建议修复顺序

第一批：修复会直接造成失败或错误语义的问题，包括指定 endpoint 的 Token Pool 重试上限、通用 token pool 误注入 Codex headers、SSE 多行 data 解析、Claude → Responses 默认 `tool_choice=required`、Responses completed-only function_call 丢失、删除 credential 误删 usage。

第二批：修复 Claude/OpenAI Responses/Gemini 语义问题，包括 Responses reasoning 映射、Gemini thought 映射、OpenAI Responses → Claude 默认 thinking、Claude OAuth 默认模型、Claude API key header。

第三批：修复性能和 API 体验问题，包括 proxy transport 缓存、inbound tracking 可配置或降开销、endpoint update 支持清空 model/thinking、enabled 使用指针字段、Fetch Models 支持 token pool / Codex / Claude OAuth。

## 9. 参考文件

- `internal/proxy/proxy.go`
- `internal/proxy/request.go`
- `internal/proxy/streaming.go`
- `internal/storage/credentials.go`
- `internal/config/config.go`
- `internal/transformer/convert/common.go`
- `internal/transformer/convert/claude_openai2.go`
- `internal/transformer/convert/claude_gemini.go`
- `internal/transformer/convert/openai2_gemini.go`
- `cmd/server/webui/api/endpoints.go`
- `cmd/server/webui/api/testing.go`
- `cmd/server/webui/ui/js/components/endpoints.js`
- ccNexus：`https://github.com/lich0821/ccNexus`
