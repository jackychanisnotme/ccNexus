# AINexus 配置包交付模板

## 适合谁

- 想快速把 Codex CLI、Claude Code、OpenAI SDK 指向 AINexus 的个人用户。
- 已经有合法上游 API key 或自有 Token Pool，但不想研究 transformer、模型和 fallback 的用户。
- 需要一份可复制、可核对、可排障的初始配置，而不是远程代部署。

## 交付内容

- AINexus 端点配置表：名称、API URL、认证模式、transformer、模型、reasoning、force stream、fallback 顺序。
- Codex CLI `config.toml` 示例。
- Claude Code `settings.json` 示例。
- OpenAI SDK `base_url` 示例。
- 常见 transformer 映射：`claude`、`openai`、`openai2`、`gemini`、`deepseek`、`kimi`。
- 5 个常见错误排查：401/403、模型不存在、端口占用、SSE 中断、上游只接受流式。

## 示例端点配置

```json
[
  {
    "name": "Codex Responses",
    "apiUrl": "https://api.openai.com",
    "apiKey": "sk-your-key",
    "authMode": "api_key",
    "enabled": true,
    "transformer": "openai2",
    "model": "gpt-5-codex",
    "thinking": "medium",
    "forceStream": false,
    "remark": "Codex CLI 推荐 Responses API"
  },
  {
    "name": "Claude Compatible",
    "apiUrl": "https://api.anthropic.com",
    "apiKey": "sk-ant-your-key",
    "authMode": "api_key",
    "enabled": true,
    "transformer": "claude",
    "model": "",
    "thinking": "",
    "forceStream": false,
    "remark": "Claude Code 兼容入口"
  }
]
```

## 客户端配置片段

Codex CLI:

```toml
model_provider = "AINexus"
model = "gpt-5-codex"
preferred_auth_method = "apikey"

[model_providers.AINexus]
name = "AINexus"
base_url = "http://127.0.0.1:3000/v1"
wire_api = "responses"
```

Claude Code:

```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "placeholder",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3000"
  }
}
```

OpenAI SDK:

```ts
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "placeholder",
  baseURL: "http://127.0.0.1:3000/v1"
});
```

## 价格

- 入门配置包：29-99 元，一次性交付。
- 高级配置包：99-299 元，一次性交付，包含多客户端、多上游和 fallback 建议。
- 更新包：29-99 元/月或季度，持续更新端点模板和模型映射。

## 交付时间

- 标准交付：付款后 24 小时内。
- 高级配置包：付款后 48 小时内。
- 需要用户提供客户端、操作系统、目标上游和已有 API key 类型。

## 限制

- 不包含任何上游账号、API 额度或账号池。
- 不承诺第三方上游永久可用。
- 不包含服务器部署、HTTPS、反向代理和长期维护。
- 用户应自行确认上游服务条款和额度来源合法。

## 验收方式

- AINexus 中至少一个端点测试通过。
- Codex CLI 或 Claude Code 至少跑通一次请求。
- 用户能说明当前使用的 base URL、transformer 和模型。
- 交付后 7 天内因配置包错误导致无法跑通，经排查确认后可退款或免费修正。
