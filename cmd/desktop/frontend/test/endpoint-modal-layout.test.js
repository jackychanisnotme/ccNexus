import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(__dirname, '..');

const uiSource = readFileSync(resolve(frontendRoot, 'src/modules/ui.js'), 'utf8');
const modalSource = readFileSync(resolve(frontendRoot, 'src/modules/modal.js'), 'utf8');
const configSource = readFileSync(resolve(frontendRoot, 'src/modules/config.js'), 'utf8');
const endpointsSource = readFileSync(resolve(frontendRoot, 'src/modules/endpoints.js'), 'utf8');
const agentProviderSource = readFileSync(resolve(frontendRoot, 'src/modules/agentProvider.js'), 'utf8');
const cssSource = readFileSync(resolve(frontendRoot, 'src/style.css'), 'utf8');
const zhSource = readFileSync(resolve(frontendRoot, 'src/i18n/zh-CN.js'), 'utf8');
const enSource = readFileSync(resolve(frontendRoot, 'src/i18n/en.js'), 'utf8');

describe('endpoint modal option layout', () => {
    it('uses horizontal option labels for reasoning and force-stream checkboxes', () => {
        assert.match(
            uiSource,
            /<label class="endpoint-option-label">\s*<input type="checkbox" id="endpointThinkingEnabled"/,
        );
        assert.match(
            uiSource,
            /<label class="endpoint-option-label">\s*<input type="checkbox" id="endpointForceStream"/,
        );
    });

    it('keeps endpoint option checkboxes from taking the full form width', () => {
        assert.match(cssSource, /\.form-group\s+\.endpoint-option-label\s*{[^}]*display:\s*inline-flex;/s);
        assert.match(cssSource, /\.form-group\s+\.endpoint-option-label\s*{[^}]*align-items:\s*center;/s);
        assert.match(cssSource, /\.form-group\s+\.endpoint-option-label\s*{[^}]*word-break:\s*keep-all;/s);
        assert.match(
            cssSource,
            /\.form-group\s+\.endpoint-option-label\s+input\[type="checkbox"\]\s*{[^}]*width:\s*auto;/s,
        );
        assert.match(
            cssSource,
            /\.form-group\s+\.endpoint-option-label\s+input\[type="checkbox"\]\s*{[^}]*flex-shrink:\s*0;/s,
        );
    });

    it('keeps the edit endpoint modal open while opening token pool management', () => {
        const start = modalSource.indexOf('export async function openEndpointTokenPoolFromModal()');
        const end = modalSource.indexOf('export async function saveEndpoint()', start);
        assert.notEqual(start, -1);
        assert.notEqual(end, -1);

        const functionSource = modalSource.slice(start, end);
        assert.doesNotMatch(functionSource, /\bcloseModal\(\);/);
        assert.match(functionSource, /\bopenTokenPoolModal\(/);
    });

    it('shows token pool management immediately when a token-pool auth mode is selected', () => {
        const start = modalSource.indexOf('function updateManageTokenPoolButton()');
        const end = modalSource.indexOf('export function handleAuthModeChange()', start);
        assert.notEqual(start, -1);
        assert.notEqual(end, -1);

        const functionSource = modalSource.slice(start, end);
        assert.doesNotMatch(functionSource, /currentEditIndex\s*>=\s*0/);
        assert.match(functionSource, /isTokenPoolMode\(getEndpointAuthMode\(\)\)/);
        assert.match(functionSource, /manageTokenPoolNewEndpoint/);
        assert.match(functionSource, /manageTokenPoolApplyChanges/);
    });

    it('saves the current endpoint draft before opening token pool management', () => {
        const start = modalSource.indexOf('export async function openEndpointTokenPoolFromModal()');
        const end = modalSource.indexOf('export async function saveEndpoint()', start);
        assert.notEqual(start, -1);
        assert.notEqual(end, -1);

        const functionSource = modalSource.slice(start, end);
        assert.match(modalSource, /function readEndpointDraftFromModal\(\)/);
        assert.match(modalSource, /async function persistEndpointDraftForTokenPoolManagement\(/);
        assert.match(functionSource, /persistEndpointDraftForTokenPoolManagement\(/);
        assert.doesNotMatch(functionSource, /SetEndpointProxyURL/);
    });

    it('renders a beginner-friendly credential pool section in the endpoint modal', () => {
        assert.match(uiSource, /endpoint-token-pool-action/);
        assert.match(uiSource, /tokenPoolCredentialSectionTitle/);
        assert.match(uiSource, /tokenPoolCredentialHelp/);
        assert.match(uiSource, /tokenPoolCredentialModeHelp/);
    });

    it('refreshes the token pool management label when endpoint draft fields change', () => {
        assert.match(modalSource, /function bindEndpointDraftChangeHandlers\(\)/);
        assert.match(modalSource, /endpointName[\s\S]*endpointProxyUrl[\s\S]*endpointModel[\s\S]*endpointRemark/);
        assert.match(modalSource, /addEventListener\('input', updateManageTokenPoolButton\)/);
    });

    it('gives each token pool manager a distinct modal identity', () => {
        assert.match(endpointsSource, /tokenPoolModeBadge/);
        assert.match(endpointsSource, /tokenPoolModeDescription/);
        assert.match(endpointsSource, /apiTokenPoolTitle/);
        assert.match(endpointsSource, /codexTitle/);
        assert.match(endpointsSource, /claudeOAuthTitle/);
        assert.match(endpointsSource, /dataset\.tokenPoolMode/);
    });

    it('renders a Codex account overview for Codex token pool endpoints', () => {
        assert.match(endpointsSource, /GetCodexAccountOverview/);
        assert.match(endpointsSource, /tokenPoolOverview/);
        assert.match(endpointsSource, /renderCodexAccountOverview/);
        assert.match(endpointsSource, /loadCodexAccountOverview/);
        assert.match(endpointsSource, /codexAccountOverviewTitle/);
        assert.match(zhSource, /codexAccountOverviewTitle:\s*'Codex 账号总览'/);
        assert.match(enSource, /codexAccountOverviewTitle:\s*'Codex Account Overview'/);
    });

    it('adds manual Codex reset credit controls for Codex token pool credentials only', () => {
        assert.match(endpointsSource, /GetCodexResetCredits/);
        assert.match(endpointsSource, /ConsumeCodexResetCredit/);
        assert.match(endpointsSource, /token-pool-reset-credit/);
        assert.match(endpointsSource, /showCodexResetCreditDialog/);
        assert.match(endpointsSource, /confirmCodexResetCreditConsume/);
        assert.match(endpointsSource, /const refreshRaw = await window\.go\.main\.App\.FetchCodexRateLimitsForCredential\(tokenPoolCurrentIndex,\s*credentialID\)/);
        assert.match(endpointsSource, /const refreshResult = parseAppJSON\(refreshRaw\)/);
        assert.match(endpointsSource, /showCodexActions \? `<button type="button" class="token-pool-reset-credit"/);
        assert.match(endpointsSource, /resetCreditConfirmAction/);
        assert.match(endpointsSource, /availableCount\s*<=\s*0/);
        assert.match(cssSource, /\.token-pool-reset-credit-modal-content/);
        assert.match(zhSource, /resetCreditAction:\s*'重置额度'/);
        assert.match(enSource, /resetCreditAction:\s*'Reset usage'/);
    });

    it('keeps the token pool table inside the modal width', () => {
        assert.doesNotMatch(cssSource, /\.token-pool-table\s*{[^}]*min-width:\s*980px;/s);
        assert.match(cssSource, /\.token-pool-table\s*{[^}]*table-layout:\s*fixed;/s);
        assert.match(cssSource, /\.token-pool-cell-account[\s\S]*text-overflow:\s*ellipsis;/);
        assert.match(cssSource, /\.token-pool-cell-actions/);
        assert.match(endpointsSource, /token-pool-col-account/);
        assert.match(endpointsSource, /token-pool-cell-rate/);
    });

    it('portals token pool action menus outside the scrolling table', () => {
        const closeStart = endpointsSource.indexOf('function closeAllTokenPoolActionMenus()');
        const openStart = endpointsSource.indexOf('function openTokenPoolActionMenu(', closeStart);
        const openEnd = endpointsSource.indexOf('function setTokenPoolHint(', openStart);
        assert.notEqual(closeStart, -1);
        assert.notEqual(openStart, -1);
        assert.notEqual(openEnd, -1);

        const closeFunctionSource = endpointsSource.slice(closeStart, openStart);
        const openFunctionSource = endpointsSource.slice(openStart, openEnd);

        assert.match(endpointsSource, /let tokenPoolOpenActionMenu = null;/);
        assert.match(closeFunctionSource, /classList\.remove\([^)]*['"]show['"][^)]*\)/);
        assert.match(closeFunctionSource, /classList\.remove\([^)]*['"]token-pool-more-menu-portal['"][^)]*\)/);
        assert.match(closeFunctionSource, /menu\.style\.left\s*=\s*['"]['"]/);
        assert.match(closeFunctionSource, /menu\.style\.top\s*=\s*['"]['"]/);
        assert.match(closeFunctionSource, /wrap\.appendChild\(menu\)/);

        assert.match(openFunctionSource, /closeAllTokenPoolActionMenus\(\);/);
        assert.match(openFunctionSource, /document\.body\.appendChild\(menu\)/);
        assert.match(openFunctionSource, /button\.getBoundingClientRect\(\)/);
        assert.match(openFunctionSource, /menu\.getBoundingClientRect\(\)/);
        assert.match(openFunctionSource, /triggerRect\.bottom[\s\S]*window\.innerHeight/);
        assert.match(openFunctionSource, /triggerRect\.top[\s\S]*menuRect\.height/);
        assert.match(openFunctionSource, /Math\.min\([\s\S]*Math\.max\([\s\S]*window\.innerWidth/);

        assert.match(endpointsSource, /closeAllTokenPoolActionMenus\(\);/);
        assert.match(endpointsSource, /addEventListener\(['"]scroll['"],\s*closeAllTokenPoolActionMenus,\s*true\)/);
        assert.match(endpointsSource, /window\.addEventListener\(['"]resize['"],\s*closeAllTokenPoolActionMenus\)/);
        assert.match(
            cssSource,
            /\.token-pool-more-menu\.token-pool-more-menu-portal\s*{[^}]*position:\s*fixed;/s,
        );
        assert.match(
            cssSource,
            /\.token-pool-more-menu\.token-pool-more-menu-portal\s*{[^}]*width:\s*160px;/s,
        );
        assert.match(
            cssSource,
            /\.token-pool-more-menu\.token-pool-more-menu-portal\s*{[^}]*max-width:\s*calc\(100vw\s*-\s*16px\);/s,
        );
        assert.match(
            cssSource,
            /\.token-pool-more-menu\.token-pool-more-menu-portal\s*{[^}]*box-sizing:\s*border-box;/s,
        );
    });

    it('keeps AI Agent and Agent Provider beside the top-right port display', () => {
        assert.match(
            uiSource,
            /showAgentModal\(\)[\s\S]*showAgentProviderModal\(\)[\s\S]*class="port-display"/,
        );

        const toolbarStart = uiSource.indexOf('window.showTerminalModal()');
        const toolbarEnd = uiSource.indexOf('window.showAddEndpointModal()', toolbarStart);
        assert.notEqual(toolbarStart, -1);
        assert.notEqual(toolbarEnd, -1);

        const toolbarSource = uiSource.slice(toolbarStart, toolbarEnd);
        assert.doesNotMatch(toolbarSource, /showAgentModal/);
        assert.doesNotMatch(toolbarSource, /showAgentProviderModal/);
    });

    it('adds LAN discovery cue and one-click add controls to the port modal', () => {
        assert.match(uiSource, /id="lanDiscoveryBadge"/);
        assert.match(uiSource, /id="lanDiscoveryPanel"/);
        assert.match(uiSource, /id="lanDiscoveryList"/);
        assert.match(modalSource, /refreshLANDiscovery\(/);
        assert.match(modalSource, /renderLANDiscoveryList\(/);
        assert.match(modalSource, /addDiscoveredLANEndpoint\(/);
        assert.match(configSource, /AddDiscoveredLANEndpoint/);
        assert.match(cssSource, /\.port-discovery-badge/);
        assert.match(cssSource, /\.lan-discovery-panel/);
        assert.match(zhSource, /lanDiscoveryTitle:\s*'发现局域网 AINexus'/);
        assert.match(enSource, /lanDiscoveryTitle:\s*'Discovered LAN AINexus'/);
    });

    it('adds an Agent Provider home button and modal controls', () => {
        assert.match(uiSource, /showAgentProviderModal/);
        assert.match(uiSource, /agentProvider\.button/);
        assert.match(agentProviderSource, /GetAgentProviderStatus/);
        assert.match(agentProviderSource, /ApplyAgentProviderConfig/);
        assert.match(agentProviderSource, /RestoreAgentProviderBackup/);
        assert.match(agentProviderSource, /agentProviderTargets/);
        assert.match(cssSource, /\.agent-provider-modal/);
    });

    it('renders the agent provider backup history picker and restore target chooser', () => {
        assert.match(agentProviderSource, /status\.backups/);
        assert.match(agentProviderSource, /agentProvider\.backupHistory/);
        assert.match(agentProviderSource, /agentProvider\.selectBackup/);
        assert.match(agentProviderSource, /agentProvider\.restoreTargets/);
        assert.match(agentProviderSource, /agentProviderBackupSelect/);
        assert.match(agentProviderSource, /openAgentProviderRestorePicker/);
        assert.match(cssSource, /\.agent-provider-backup-select/);
        assert.match(cssSource, /\.agent-provider-restore-targets/);
    });

    it('localizes the agent header buttons in Simplified Chinese', () => {
        assert.match(zhSource, /agent:\s*{[\s\S]*?button:\s*'AI 助手'/);
        assert.match(zhSource, /agentProvider:\s*{[\s\S]*?button:\s*'智能体配置'/);
        assert.match(enSource, /agent:\s*{[\s\S]*?button:\s*'AI Agent'/);
        assert.match(enSource, /agentProvider:\s*{[\s\S]*?button:\s*'Agent Provider'/);
    });

});
