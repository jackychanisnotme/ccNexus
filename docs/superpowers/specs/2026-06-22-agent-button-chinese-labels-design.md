# Chinese Labels for Agent Buttons

## Goal

When the desktop interface language is Simplified Chinese, show Chinese labels for the two agent actions in the header.

## Scope

- Change `agent.button` in the Simplified Chinese translation from `AI Agent` to `AI 助手`.
- Change `agentProvider.button` in the Simplified Chinese translation from `Agent Provider` to `智能体配置`.
- Keep the English translations unchanged.
- Keep button layout, icons, click handlers, modal titles, and other Agent Provider text unchanged.

## Verification

Add a frontend source test that verifies the Simplified Chinese button labels and confirms the English button labels remain unchanged. Run the focused frontend tests and build the frontend distribution.
