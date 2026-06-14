import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(__dirname, '..');

const uiSource = readFileSync(resolve(frontendRoot, 'src/modules/ui.js'), 'utf8');
const modalSource = readFileSync(resolve(frontendRoot, 'src/modules/modal.js'), 'utf8');
const endpointsSource = readFileSync(resolve(frontendRoot, 'src/modules/endpoints.js'), 'utf8');
const agentProviderSource = readFileSync(resolve(frontendRoot, 'src/modules/agentProvider.js'), 'utf8');
const cssSource = readFileSync(resolve(frontendRoot, 'src/style.css'), 'utf8');

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

    it('keeps the token pool table inside the modal width', () => {
        assert.doesNotMatch(cssSource, /\.token-pool-table\s*{[^}]*min-width:\s*980px;/s);
        assert.match(cssSource, /\.token-pool-table\s*{[^}]*table-layout:\s*fixed;/s);
        assert.match(cssSource, /\.token-pool-cell-account[\s\S]*text-overflow:\s*ellipsis;/);
        assert.match(cssSource, /\.token-pool-cell-actions/);
        assert.match(endpointsSource, /token-pool-col-account/);
        assert.match(endpointsSource, /token-pool-cell-rate/);
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

});
