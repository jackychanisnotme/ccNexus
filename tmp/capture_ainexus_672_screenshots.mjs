import { chromium } from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';

const outDir = path.resolve('output/ainexus_672_manual_assets');
await fs.mkdir(outDir, { recursive: true });

const VERSION = '6.7.2';

const config = {
  port: 3000,
  listenMode: 'local',
  language: 'zh-CN',
  theme: 'light',
  themeAuto: false,
  proxyUrl: 'http://127.0.0.1:7890',
  closeWindowBehavior: 'ask',
  claudeNotificationType: 'system',
  failover: {
    recoveredEndpointPolicy: 'deprioritize',
    cooldowns: {
      quotaExhaustedSec: 3600,
      rateLimitedSec: 120,
      upstreamErrorSec: 60,
      networkErrorSec: 30,
      tokenUnavailableSec: 600,
      configErrorSec: 1800,
    },
    circuitBreaker: {
      consecutiveFailures: 3,
      windowSec: 60,
      failureRateThreshold: 0.6,
      minRequests: 5,
      cooldownSec: 600,
    },
  },
  webdav: {
    url: 'https://dav.jianguoyun.com/dav/',
    username: 'demo@example.com',
    password: 'demo-app-password',
  },
  backup: {
    provider: 'webdav',
    local: { dir: '/Users/demo/AINexusBackups' },
    s3: {
      endpoint: 's3.example.com',
      region: 'us-east-1',
      bucket: 'ainexus-backup',
      prefix: 'AINexus/',
      accessKey: 'AKIA...',
      secretKey: 'secret',
      sessionToken: '',
      useSSL: true,
      forcePathStyle: false,
    },
  },
  endpoints: [
    {
      name: 'Claude 官方',
      apiUrl: 'https://api.anthropic.com',
      apiKey: 'sk-ant-api03-demo-key',
      authMode: 'api_key',
      transformer: 'claude',
      model: 'claude-sonnet-4-5-20250929',
      enabled: true,
      thinking: 'medium',
      forceStream: false,
      codexFastMode: false,
      maxConcurrentRequests: 0,
      remark: '主力 Claude 端点',
    },
    {
      name: 'Codex Pool',
      apiUrl: 'https://chatgpt.com/backend-api/codex',
      apiKey: '',
      authMode: 'codex_token_pool',
      transformer: 'openai2',
      model: 'gpt-5-codex',
      enabled: true,
      thinking: 'off',
      forceStream: true,
      codexFastMode: true,
      maxConcurrentRequests: 3,
      remark: 'ChatGPT Codex 登录凭据轮换',
    },
    {
      name: 'Gemini 备用',
      apiUrl: 'https://generativelanguage.googleapis.com',
      apiKey: 'AIza-demo',
      authMode: 'api_key',
      transformer: 'gemini',
      model: 'gemini-2.5-pro',
      enabled: false,
      thinking: 'off',
      forceStream: false,
      codexFastMode: false,
      maxConcurrentRequests: 1,
      remark: '备用模型',
    },
  ],
};

const stats = {
  totals: {
    daily: { requests: 128, errors: 4, inputTokens: 842000, outputTokens: 216000 },
    yesterday: { requests: 96, errors: 3, inputTokens: 650000, outputTokens: 180000 },
    weekly: { requests: 725, errors: 19, inputTokens: 4200000, outputTokens: 1300000 },
    monthly: { requests: 3180, errors: 86, inputTokens: 18500000, outputTokens: 5400000 },
    history: { requests: 12400, errors: 242, inputTokens: 82400000, outputTokens: 21900000 },
  },
  endpoints: {
    'Claude 官方': { requests: 66, errors: 1, inputTokens: 410000, outputTokens: 120000 },
    'Codex Pool': { requests: 54, errors: 2, inputTokens: 386000, outputTokens: 80000 },
    'Gemini 备用': { requests: 8, errors: 1, inputTokens: 46000, outputTokens: 16000 },
  },
};

const credentials = [
  {
    id: 1,
    accountId: 'acct_codex_primary_001',
    email: 'primary@example.com',
    status: 'active',
    enabled: true,
    expiresAt: '2026-08-08T10:00:00Z',
    lastUsedAt: '2026-07-08T10:30:00Z',
    rateLimits: {
      primary: { usedPercent: 43, resetAt: '2026-07-09T00:00:00Z' },
      secondary: { usedPercent: 18, resetAt: '2026-07-15T00:00:00Z' },
    },
    usage: { requests: 42, errors: 1, inputTokens: 320000, outputTokens: 64000 },
    lastError: '',
  },
  {
    id: 2,
    accountId: 'acct_codex_backup_002',
    email: 'backup@example.com',
    status: 'cooldown',
    enabled: true,
    expiresAt: '2026-08-01T10:00:00Z',
    lastUsedAt: '2026-07-08T09:20:00Z',
    rateLimits: {
      primary: { usedPercent: 82, resetAt: '2026-07-09T00:00:00Z' },
      secondary: { usedPercent: 55, resetAt: '2026-07-15T00:00:00Z' },
    },
    usage: { requests: 12, errors: 1, inputTokens: 66000, outputTokens: 16000 },
    lastError: 'rate_limited: upstream returned 429',
  },
];

const network = {
  listenMode: 'local',
  localURL: 'http://127.0.0.1:3000',
  lanURLs: ['http://192.168.1.8:3000'],
  restartRequired: false,
  connections: {
    byCategory: { proxy: 2, admin_ui: 1, api: 1, health: 0, events: 1 },
    connections: [
      { category: 'proxy', clientIp: '127.0.0.1', method: 'POST', path: '/v1/responses', durationMillis: 5200, userAgent: 'codex-cli' },
      { category: 'admin_ui', clientIp: '127.0.0.1', method: 'GET', path: '/ui/', durationMillis: 18000, userAgent: 'browser' },
    ],
  },
};

const agentProvider = {
  targetUrl: 'http://127.0.0.1:3000/v1',
  latestBackup: { id: 'backup-20260708-1030', createdAt: '2026-07-08T10:30:00Z' },
  backups: [
    { id: 'backup-20260708-1030', createdAt: '2026-07-08T10:30:00Z' },
    { id: 'backup-20260701-0900', createdAt: '2026-07-01T09:00:00Z' },
  ],
  targets: [
    { target: 'claude', label: 'Claude Code', detected: true, path: '~/.claude/settings.json' },
    { target: 'codex', label: 'Codex CLI', detected: true, path: '~/.codex/config.toml' },
    { target: 'openclaw', label: 'OpenClaw', detected: false, path: '~/.openclaw/config.json' },
    { target: 'hermes', label: 'Hermes Agent', detected: true, path: '~/.hermes/config.json' },
  ],
};

function desktopBackendStub() {
  const { VERSION, config, stats, credentials, network, agentProvider } = arguments[0];
  window.runtime = { EventsOn: () => {}, WindowHide: () => {} };
  const ok = (data) => JSON.stringify({ success: true, data });
  window.go = {
    main: {
      App: {
        GetLanguage: async () => 'zh-CN',
        SetLanguage: async () => null,
        GetThemeAuto: async () => false,
        GetTheme: async () => 'light',
        GetAutoLightTheme: async () => 'light',
        GetAutoDarkTheme: async () => 'dark',
        GetVersion: async () => VERSION,
        GetConfig: async () => JSON.stringify(config),
        GetLogLevel: async () => 1,
        SetLogLevel: async () => null,
        GetLogsByLevel: async () => [
          '[INFO] AINexus proxy started on 127.0.0.1:3000',
          '[INFO] current endpoint: Claude 官方',
          '[WARN] Codex Pool credential #2 entered cooldown after rate limit',
          '[INFO] endpoint error telemetry queued: rate_limited/429',
        ].join('\n'),
        ClearLogs: async () => null,
        GetStats: async () => JSON.stringify({
          activeEndpoints: 2,
          totalEndpoints: 3,
          totalRequests: 128,
          successRequests: 124,
          failedRequests: 4,
          totalTokens: 1058000,
          totalInputTokens: 842000,
          totalOutputTokens: 216000,
          endpoints: stats.endpoints,
        }),
        GetStatsByPeriod: async () => JSON.stringify({ totals: stats.totals, endpoints: stats.endpoints }),
        GetStatsDaily: async () => JSON.stringify({ totals: stats.totals, endpoints: stats.endpoints }),
        GetStatsYesterday: async () => JSON.stringify({ totals: stats.totals, endpoints: stats.endpoints }),
        GetStatsWeekly: async () => JSON.stringify({ totals: stats.totals, endpoints: stats.endpoints }),
        GetStatsMonthly: async () => JSON.stringify({ totals: stats.totals, endpoints: stats.endpoints }),
        GetStatsTrendByPeriod: async () => JSON.stringify({ requests: 12, tokens: 8 }),
        GetStatsTrendByPeriodFiltered: async () => JSON.stringify({ requests: 12, tokens: 8 }),
        GetStatsFilters: async () => JSON.stringify({
          endpoints: config.endpoints.map((ep) => ({ name: ep.name, deleted: false })),
          clientIps: ['127.0.0.1', '192.168.1.20'],
        }),
        GetCurrentEndpoint: async () => 'Claude 官方',
        SwitchToEndpoint: async () => null,
        GetEndpointRuntimeStatuses: async () => JSON.stringify({
          'Claude 官方': { lastSuccessAt: '2026-07-08T10:31:00Z' },
          'Codex Pool': { lastFailureAt: '2026-07-08T10:10:00Z', lastFailureReason: 'rate_limited', lastFailureStatusCode: 429 },
        }),
        TestAllEndpointsZeroCost: async () => JSON.stringify({}),
        TestEndpointLight: async () => JSON.stringify({ success: true, latency: 128 }),
        TestEndpoint: async () => JSON.stringify({ success: true, latency: 128, response: 'ok' }),
        AddEndpoint: async () => null,
        UpdateEndpoint: async () => null,
        RemoveEndpoint: async () => null,
        ToggleEndpoint: async () => null,
        ReorderEndpoints: async () => null,
        FetchModels: async () => JSON.stringify({ success: true, models: ['claude-sonnet-4-5-20250929', 'gpt-5-codex', 'gemini-2.5-pro'] }),
        UpdatePort: async () => null,
        UpdateListenMode: async () => network,
        GetNetworkStatus: async () => JSON.stringify(network),
        GetLANDiscoveryStatus: async () => JSON.stringify({ unadded: 1, candidates: [{ name: 'AINexus NAS', baseUrl: 'http://192.168.1.55:3000', added: false }] }),
        RefreshLANDiscovery: async () => JSON.stringify({ candidates: [] }),
        AddDiscoveredLANEndpoint: async () => JSON.stringify({ success: true }),
        GetEndpointCredentials: async () => JSON.stringify({ credentials, stats: { total: 2, active: 1, cooldown: 1, invalid: 0, expired: 0, expiring: 0, needRefresh: 0, disabled: 0 } }),
        GetEndpointProxyURL: async () => 'http://127.0.0.1:7890',
        SetEndpointProxyURL: async () => null,
        ImportEndpointCredentials: async () => JSON.stringify({ created: 1, updated: 0, skipped: 0, failed: 0 }),
        ImportEndpointCredentialsFromFiles: async () => JSON.stringify({ created: 1, updated: 0, skipped: 0, failed: 0 }),
        SetEndpointCredentialEnabled: async () => null,
        ActivateEndpointCredential: async () => null,
        DeleteEndpointCredential: async () => null,
        UpdateEndpointCredentialToken: async () => null,
        FetchCodexRateLimits: async () => JSON.stringify({ updated: 2, failed: 0, skipped: 0 }),
        FetchCodexRateLimitsForCredential: async () => JSON.stringify({ summary: 'primary 43%, secondary 18%', detail: 'ok' }),
        RefreshEndpointCredential: async () => null,
        StartCodexCredentialAuth: async () => JSON.stringify({ loginId: 'login-demo', verificationUrl: 'https://chatgpt.com/activate', userCode: 'ABCD-EFGH', expiresIn: 900 }),
        GetCodexCredentialAuthStatus: async () => JSON.stringify({ status: 'pending' }),
        CancelCodexCredentialAuth: async () => null,
        GetCodexAccountOverview: async () => JSON.stringify({
          updatedAt: '2026-07-08T10:30:00Z',
          totalAccounts: 2,
          enabledAccounts: 2,
          problemAccounts: 1,
          highestPrimaryUsedPercent: 82,
          highestSecondaryUsedPercent: 55,
          totalRequests: 54,
          totalErrors: 2,
          totalInputTokens: 386000,
          totalOutputTokens: 80000,
          planDistribution: { plus: 1, pro: 1 },
          snapshotAvailable: 2,
          snapshotProblem: 1,
          snapshotMissing: 0,
        }),
        GetCodexTokenPoolHomeSummaries: async () => JSON.stringify([
          {
            endpointName: 'Codex Pool',
            activeAccounts: 1,
            totalAccounts: 2,
            problemAccounts: 1,
            highestPrimaryUsedPercent: 82,
            highestSecondaryUsedPercent: 55,
            latestQuotaUpdatedAt: '2026-07-08T10:30:00Z',
            nextResetAt: '2026-07-09T00:00:00Z',
            accounts: [
              { id: 1, label: 'primary', accountId: 'acct_codex_primary_001', email: 'primary@example.com', status: 'active', primaryUsedPercent: 43, secondaryUsedPercent: 18, enabled: true },
              { id: 2, label: 'backup', accountId: 'acct_codex_backup_002', email: 'backup@example.com', status: 'cooldown', primaryUsedPercent: 82, secondaryUsedPercent: 55, enabled: true, hasError: true, errorText: '429' },
            ],
          },
        ]),
        GetCodexResetCredits: async () => JSON.stringify({ available: 2, credits: [{ status: 'available', grantedAt: '2026-07-01T00:00:00Z', expiresAt: '2026-08-01T00:00:00Z' }] }),
        ConsumeCodexResetCredit: async () => JSON.stringify({ ok: true }),
        DiscoverClaudeOAuthCredentials: async () => JSON.stringify({ items: [] }),
        ImportClaudeOAuthCredential: async () => JSON.stringify({ created: 1, updated: 0, skipped: 0, failed: 0 }),
        GetProxyURL: async () => 'http://127.0.0.1:7890',
        SaveSettings: async () => null,
        GetLicenseStatus: async () => ok({ licensed: true, expiresAt: '2027-07-08T00:00:00Z', remainingDays: 365, lastPlan: 'yearly', message: '授权有效' }),
        RefreshLicenseStatus: async () => ok({ licensed: true, expiresAt: '2027-07-08T00:00:00Z', remainingDays: 365, lastPlan: 'yearly', message: '授权有效' }),
        ActivateLicense: async () => ok({ licensed: true, expiresAt: '2027-07-08T00:00:00Z', remainingDays: 365, lastPlan: 'yearly' }),
        GetAgentProviderStatus: async () => JSON.stringify(agentProvider),
        ApplyAgentProviderConfig: async () => JSON.stringify({ results: [{ target: 'codex', label: 'Codex CLI', status: 'success', message: 'updated' }] }),
        RestoreAgentProviderBackup: async () => JSON.stringify({ results: [{ target: 'codex', label: 'Codex CLI', status: 'restored', message: 'restored' }] }),
        RunAgent: async () => JSON.stringify({ answer: '我可以帮助检查端点、修复 Agent Provider 配置并生成本地配置建议。', currentEndpoint: 'Claude 官方', endpointUrl: 'http://127.0.0.1:3000/v1', toolResults: [] }),
        UpdateWebDAVConfig: async () => null,
        TestWebDAVConnection: async () => JSON.stringify({ success: true }),
        UpdateBackupProvider: async () => null,
        UpdateLocalBackupDir: async () => null,
        UpdateS3BackupConfig: async () => null,
        TestS3Connection: async () => JSON.stringify({ success: true }),
        BackupToProvider: async () => JSON.stringify({ success: true, filename: 'ainexus-backup-20260708.db' }),
        ListBackups: async () => JSON.stringify([{ filename: 'ainexus-backup-20260708.db', size: 123456, modTime: '2026-07-08T10:00:00Z' }]),
        RestoreFromProvider: async () => null,
        DeleteBackups: async () => null,
        DetectBackupConflict: async () => JSON.stringify({ conflict: false }),
        SelectDirectory: async () => '/Users/demo/AINexusBackups',
        FetchBroadcast: async () => JSON.stringify(null),
        GetChangelog: async () => JSON.stringify([{ version: `v${VERSION}`, date: '2026-07-08', changes: ['在线授权与远程维护增强', 'Codex Token Pool 额度与凭证统计', '端点错误遥测'] }]),
        FetchImageAsBase64: async () => '',
        OpenURL: async () => null,
        HideWindow: async () => null,
        Quit: async () => null,
      },
    },
  };
}

async function shot(page, name, selector = null, options = {}) {
  await page.waitForTimeout(options.delay ?? 450);
  const file = path.join(outDir, `${name}.png`);
  if (selector) {
    const loc = page.locator(selector).first();
    await loc.waitFor({ state: 'visible', timeout: 5000 });
    await loc.screenshot({ path: file });
  } else {
    await page.screenshot({ path: file, fullPage: options.fullPage ?? false });
  }
  return file;
}

function remoteAdminHtml() {
  return `<!doctype html><html><head><meta charset="utf-8"><style>
    body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Arial,"Microsoft YaHei",sans-serif;margin:0;background:#f6f8fb;color:#1f2937}
    .wrap{padding:26px}.top{display:flex;justify-content:space-between;align-items:center;margin-bottom:18px}.brand h1{margin:0;font-size:26px}.brand p{margin:6px 0 0;color:#64748b}
    .tabs{display:flex;gap:10px;margin-bottom:18px}.tab{padding:10px 14px;border-radius:8px;background:white;border:1px solid #dbe3ef}.tab.active{background:#2563eb;color:white}
    .grid{display:grid;grid-template-columns:1.1fr .9fr;gap:18px}.card{background:white;border:1px solid #dbe3ef;border-radius:10px;padding:18px;box-shadow:0 8px 24px rgba(15,23,42,.06)}
    h2{font-size:18px;margin:0 0 6px}.muted{color:#64748b;font-size:13px}.actions{display:flex;gap:8px;flex-wrap:wrap}.btn{border:0;border-radius:7px;padding:8px 10px;background:#e5e7eb}.btn.primary{background:#2563eb;color:white}.btn.danger{background:#ef4444;color:white}.btn.secondary{background:#f1f5f9}
    table{border-collapse:collapse;width:100%;margin-top:12px;font-size:13px}th,td{border:1px solid #e2e8f0;padding:8px;text-align:left;vertical-align:middle}th{background:#eef4fb}.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}.pill{display:inline-block;padding:3px 7px;border-radius:999px;background:#dcfce7;color:#166534}.warn{background:#fef3c7;color:#92400e}.bad{background:#fee2e2;color:#991b1b}
    .detail{margin-top:16px}.toolbar{display:flex;justify-content:space-between;align-items:center;margin:10px 0}.small{font-size:12px}
  </style></head><body><div class="wrap">
    <div class="top"><div class="brand"><h1>AINexus 授权服务器后台</h1><p>卡密、设备、远程端点维护与端点错误遥测</p></div><div class="actions"><button class="btn secondary">退出</button><button class="btn primary">刷新</button></div></div>
    <div class="tabs"><div class="tab active">设备</div><div class="tab">卡密</div><div class="tab">账号</div><div class="tab">审计历史</div></div>
    <div class="grid"><div class="card"><h2>设备列表</h2><p class="muted">按客户、设备 ID、状态和到期时间管理授权设备。</p><table><thead><tr><th>客户</th><th>设备 ID/IP</th><th>状态</th><th>版本</th><th>操作</th></tr></thead><tbody><tr><td>客户 A</td><td class="mono">dev_**** <button class="btn small">显示</button><br>207.*.*.12</td><td><span class="pill">active</span></td><td>${VERSION}</td><td><div class="actions"><button class="btn">明细</button><button class="btn">备注</button><button class="btn">修改到期</button><button class="btn danger">禁用当前</button></div></td></tr></tbody></table>
      <div class="detail"><h2>远程端点维护</h2><p class="muted">状态：已启用 · 心跳：2026-07-08 20:30 · 仅上传聚合统计与脱敏凭据。</p><div class="toolbar"><button class="btn primary">新增端点</button><span class="muted">命令下发后等待客户端拉取并回传结果</span></div><table><thead><tr><th>端点</th><th>状态</th><th>Base URL</th><th>API Key</th><th>快速</th><th>并发</th><th>用量</th><th>操作</th></tr></thead><tbody><tr><td>Codex Pool</td><td>启用</td><td class="mono">https://chatgpt.com/backend-api/codex</td><td class="mono">sk-****</td><td>开启</td><td>3</td><td>请求 54 / Token 466k / 错误 2</td><td><div class="actions"><button class="btn">上移</button><button class="btn">改URL</button><button class="btn">改Key</button><button class="btn">改并发</button><button class="btn">关闭快速</button><button class="btn danger">删除</button></div></td></tr></tbody></table></div></div>
    <div class="card"><h2>端点错误遥测</h2><p class="muted">按设备聚合展示近 24 小时与近 7 天错误分类，不上传 prompt/response。</p><table><thead><tr><th>窗口</th><th>端点</th><th>原因</th><th>次数</th><th>最近状态码</th></tr></thead><tbody><tr><td>近24小时</td><td>Codex Pool</td><td><span class="warn">rate_limited</span></td><td>8</td><td>429</td></tr><tr><td>近7天</td><td>Gemini 备用</td><td><span class="bad">upstream_error</span></td><td>3</td><td>500</td></tr></tbody></table>
      <h2 style="margin-top:18px">Codex Token Pool</h2><table><thead><tr><th>ID</th><th>账号</th><th>状态</th><th>额度</th><th>操作</th></tr></thead><tbody><tr><td>1</td><td class="mono">acct_****</td><td>active</td><td>43% / 18%</td><td><div class="actions"><button class="btn">停用</button><button class="btn">改Token</button><button class="btn danger">删除</button><button class="btn">查看Token</button></div></td></tr></tbody></table></div></div>
  </div></body></html>`;
}

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1 });

const desktop = await context.newPage();
await desktop.addInitScript(desktopBackendStub, { VERSION, config, stats, credentials, network, agentProvider });
await desktop.goto('http://127.0.0.1:5173/', { waitUntil: 'networkidle' });
await desktop.waitForSelector('.header', { timeout: 10000 });
await shot(desktop, 'desktop-home', null, { fullPage: true });
await desktop.evaluate(() => window.showAddEndpointModal());
await shot(desktop, 'desktop-endpoint-modal-api-key', '#endpointModal .modal-content');
await desktop.selectOption('#endpointAuthMode', 'codex_token_pool');
await desktop.evaluate(() => window.handleAuthModeChange());
await shot(desktop, 'desktop-endpoint-modal-codex-pool', '#endpointModal .modal-content');
await desktop.evaluate(() => window.closeModal());
await desktop.evaluate(async () => { const mod = await import('/src/modules/endpoints.js'); await mod.openTokenPoolModal(1, 'Codex Pool'); });
await shot(desktop, 'desktop-token-pool', '#tokenPoolModal .modal-content');
await desktop.evaluate(() => document.getElementById('tokenPoolModal')?.classList.remove('active'));
await desktop.evaluate(() => window.showSettingsModal());
await shot(desktop, 'desktop-settings', '#settingsModal .modal-content');
await desktop.evaluate(() => window.closeSettingsModal());
await desktop.evaluate(() => window.showEditPortModal());
await shot(desktop, 'desktop-access-settings', '#portModal .modal-content');
await desktop.evaluate(() => window.closePortModal());
await desktop.evaluate(() => window.showDataSyncDialog('webdav'));
await shot(desktop, 'desktop-data-sync-webdav', '#genericModal .modal-content');
await desktop.evaluate(() => window.switchDataSyncTab('s3'));
await shot(desktop, 'desktop-data-sync-s3', '#genericModal .modal-content');
await desktop.evaluate(() => window.closeDataSyncDialog?.());
await desktop.evaluate(() => window.showAgentModal());
await shot(desktop, 'desktop-ai-agent', '#agentModal .modal-content');
await desktop.evaluate(() => window.closeAgentModal());
await desktop.evaluate((status) => {
  window.showAgentProviderModal?.();
  if (document.querySelector('#agentProviderModal')) return;
  const host = document.createElement('div');
  host.id = 'agentProviderModalHost';
  host.innerHTML = `<div id="agentProviderModal" class="modal active"><div class="modal-content agent-provider-modal"><div class="modal-header"><h2>🔀 Agent Provider</h2><button class="modal-close">&times;</button></div><div class="modal-body"><div class="agent-provider-summary"><span>目标地址</span><code>${status.targetUrl}</code></div><div class="agent-provider-actions-row"><button class="btn btn-secondary btn-sm">全选</button><button class="btn btn-secondary btn-sm">清空</button><label><input type="checkbox"> 创建缺失配置</label></div><div class="agent-provider-targets">${status.targets.map((target) => `<label class="agent-provider-target ${target.detected ? 'detected' : 'missing'}"><input type="checkbox" ${target.detected ? 'checked' : ''}><span class="agent-provider-main"><strong>${target.label}</strong><small>${target.path}</small></span><span class="agent-provider-status ${target.detected ? 'ok' : 'muted'}">${target.detected ? '已检测' : '未初始化'}</span></label>`).join('')}</div><div class="agent-provider-backup"><span>历史备份</span><code>${status.latestBackup.id}</code></div></div><div class="modal-footer"><button class="btn btn-secondary">关闭</button><button class="btn btn-secondary">还原备份</button><button class="btn btn-primary">覆盖为 AINexus</button></div></div></div>`;
  document.body.appendChild(host);
}, agentProvider);
await shot(desktop, 'desktop-agent-provider', '#agentProviderModal .modal-content');
await desktop.evaluate(() => window.closeAgentProviderModal?.());
await desktop.evaluate(() => { window.showTerminalModal?.(); document.querySelector('#terminalModal')?.classList.add('active'); });
await shot(desktop, 'desktop-launcher', '#terminalModal .modal-content');
await desktop.evaluate(() => window.closeTerminalModal?.());
await desktop.evaluate(() => document.getElementById('startupLicenseModal')?.classList.add('active'));
await shot(desktop, 'desktop-license-gate', '#startupLicenseModal .modal-content');

const server = await context.newPage();
await server.route('**/api/**', async (route) => {
  const url = new URL(route.request().url());
  const p = url.pathname.replace('/api', '');
  let data = { ok: true };
  if (p === '/events') return route.fulfill({ status: 200, headers: { 'content-type': 'text/event-stream' }, body: '' });
  if (p === '/endpoints') data = { endpoints: config.endpoints, tokenPools: { 'Codex Pool': { total: 2, active: 1, cooldown: 1 } } };
  else if (p === '/endpoints/current') data = { name: 'Claude 官方' };
  else if (p.endsWith('/credentials')) data = { credentials, stats: { total: 2, active: 1, cooldown: 1 } };
  else if (p === '/stats/summary') data = { totalRequests: 128, successRate: 96.9, inputTokens: 842000, outputTokens: 216000 };
  else if (p === '/stats/daily' || p === '/stats/weekly' || p === '/stats/monthly') data = { stats: { totalRequests: 128, totalSuccess: 124, totalErrors: 4, totalInputTokens: 842000, totalOutputTokens: 216000, endpoints: stats.endpoints } };
  else if (p === '/stats/filters') data = { endpoints: config.endpoints.map((ep) => ({ name: ep.name, deleted: false })), clientIps: ['127.0.0.1', '192.168.1.20'] };
  else if (p === '/network') data = network;
  else if (p === '/agent-providers/status') data = agentProvider;
  else if (p === '/config') data = config;
  else if (p === '/license/status') data = { licensed: true, expiresAt: '2027-07-08T00:00:00Z', remainingDays: 365, lastPlan: 'yearly' };
  await route.fulfill({ status: 200, headers: { 'content-type': 'application/json' }, body: JSON.stringify({ success: true, data }) });
});
await server.goto('http://127.0.0.1:5180/ui/', { waitUntil: 'networkidle' });
await server.waitForSelector('#sidebar', { timeout: 10000 });
await shot(server, 'server-dashboard', null, { fullPage: true });
await server.click('[data-view="endpoints"]');
await shot(server, 'server-endpoints', null, { fullPage: true });
await server.click('#add-endpoint-btn');
await shot(server, 'server-endpoint-modal', '.modal');
await server.click('.modal-close');
await server.click('[data-view="stats"]');
await shot(server, 'server-stats', null, { fullPage: true });
await server.click('[data-view="testing"]');
await shot(server, 'server-testing', null, { fullPage: true });
await server.click('[data-view="settings"]');
await shot(server, 'server-settings', null, { fullPage: true });
await server.click('[data-view="dashboard"]');
await server.click('#agent-provider-open');
await shot(server, 'server-agent-provider-modal', '#agent-provider-modal .modal');

const admin = await context.newPage();
await admin.setContent(remoteAdminHtml(), { waitUntil: 'load' });
await shot(admin, 'license-admin-remote', null, { fullPage: true });

await browser.close();
console.log(`screenshots written to ${outDir}`);
