# AINexus 节点订阅交付模板

## 适合谁

- 需要持续更新端点配置源的个人用户、小团队或教程作者。
- 希望获得推荐 transformer、模型、fallback 顺序和节点状态说明的用户。
- 已接受第一阶段为私有链接、配置文件或社群通知的半自动交付方式。

## 交付内容

- 私有配置文件或私有订阅链接。
- 端点名称、API URL、认证模式、transformer、模型、reasoning、force stream。
- 推荐 fallback 顺序和禁用策略。
- 节点状态说明：可用、观察中、维护中、下线。
- 每周更新记录：新增、修改、下线和风险提示。
- 客户端接入示例：Codex CLI、Claude Code、OpenAI SDK。

## 私有配置文件格式

```json
{
  "version": "2026-06-phase1",
  "updatedAt": "2026-06-15",
  "notes": "第一阶段半自动节点源，导入前请替换自己的合法 API key。",
  "endpoints": [
    {
      "name": "Primary Responses",
      "apiUrl": "https://api.example.com",
      "authMode": "api_key",
      "transformer": "openai2",
      "model": "gpt-5-codex",
      "thinking": "medium",
      "forceStream": false,
      "status": "available"
    }
  ]
}
```

## 价格

- 基础节点源：49-99 元/月，少量稳定端点配置和更新说明。
- 高级节点源：99-299 元/月，更多模型、更细 fallback、更快更新。
- 团队节点源：399 元/月起，团队配置模板、私有更新和部署建议。

## 交付时间

- 首次开通：付款后 24 小时内发放私有链接或配置文件。
- 常规更新：每周至少一次更新记录。
- 紧急更新：核心节点不可用时在支持渠道同步替代方案。

## 限制

- 第一阶段不承诺完整自动订阅协议和客户端自动拉取。
- 节点源不包含非法账号池、不可解释来源或无限额度承诺。
- 用户需要自行准备合法 API key，或使用明确授权的上游额度。
- 因第三方上游策略、地区网络或额度变化导致的问题，按更新说明和支持规则处理。

## 验收方式

- 用户能获取私有链接或配置文件。
- 至少一个节点可按说明导入或手动创建到 AINexus。
- AINexus WebUI 或桌面端中端点测试通过。
- 用户在 7 天内至少一次成功请求，或确认需要退款/换源。
