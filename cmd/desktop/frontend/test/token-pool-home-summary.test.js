import { after, describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const endpointsPath = resolve(__dirname, '../src/modules/endpoints.js');

const originalDocument = globalThis.document;
const originalWindow = globalThis.window;
globalThis.document = {
    addEventListener() {},
    querySelectorAll() {
        return [];
    }
};
globalThis.window = {
    addEventListener() {}
};

const translations = {
    'tokenPool.homeTitle': 'Pool',
    'tokenPool.homeHealthy': 'healthy {active}/{total}',
    'tokenPool.homeProblems': 'problems {count}',
    'tokenPool.homeQuota': 'quota {primary} / {secondary}',
    'tokenPool.homeUpdated': 'updated {time}',
    'tokenPool.homeReset': 'reset {time}',
    'tokenPool.homeNoAccounts': 'no accounts',
    'tokenPool.homeAccountError': 'error',
    'tokenPool.homeStatus': 'status {status}',
    'tokenPool.homeStale': 'stale',
    'tokenPool.statusLabels.active': 'active',
    'tokenPool.statusLabels.invalid': 'invalid'
};

const dependencyStubs = `
const getLanguage = () => 'en';
const t = (key) => (${JSON.stringify(translations)})[key] || key;
const escapeHtml = (value) => String(value ?? '').replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
const formatTokens = (value) => String(value ?? '');
const maskApiKey = (value) => value;
const getEndpointStats = () => ({});
const toggleEndpoint = () => {};
const testAllEndpointsZeroCost = () => {};
const filterEndpoints = (value) => value;
const isFilterActive = () => false;
const updateFilterStats = () => {};
`;
const endpointsSource = readFileSync(endpointsPath, 'utf8').replace(/^import .*;\s*$/gm, '')
    + '\nexport { renderEndpointPoolHomeSummary, renderCompactEndpointPoolHomeSummary, setEndpointPoolHomeSummariesForTest };\n';
const endpointsModule = await import(`data:text/javascript;base64,${Buffer.from(dependencyStubs + endpointsSource).toString('base64')}`);

after(() => {
    globalThis.document = originalDocument;
    globalThis.window = originalWindow;
});

describe('token pool homepage summary helpers', () => {
    it('renders Codex pool summary and rotates account previews from cache', () => {
        const {
            renderEndpointPoolHomeSummary,
            setEndpointPoolHomeSummariesForTest
        } = endpointsModule;

        setEndpointPoolHomeSummariesForTest({
            'Codex Pool': {
                endpointName: 'Codex Pool',
                totalAccounts: 2,
                activeAccounts: 1,
                problemAccounts: 1,
                highestPrimaryUsedPercent: 67,
                highestSecondaryUsedPercent: 24,
                latestQuotaUpdatedAt: '2026-06-28T09:45:00Z',
                nextResetAt: '2026-06-28T12:00:00Z',
                accounts: [
                    { label: 'acct-a', status: 'active', enabled: true, primaryUsedPercent: 67, secondaryUsedPercent: 24 },
                    { label: 'acct-b', status: 'invalid', enabled: true, hasError: true, errorText: 'unauthorized' },
                    { label: 'acct-c', status: 'active', enabled: true }
                ]
            }
        }, 1);

        const html = renderEndpointPoolHomeSummary({ name: 'Codex Pool', authMode: 'codex_token_pool' });

        assert.match(html, /endpoint-pool-home/);
        assert.match(html, /healthy 1\/2/);
        assert.match(html, /quota 67% \/ 24%/);
        assert.match(html, /problems 1/);
        assert.match(html, /acct-b/);
        assert.match(html, /acct-c/);
        assert.doesNotMatch(html, /acct-a/);
        assert.match(html, /endpoint-pool-home-account-error/);
    });

    it('does not render for non-Codex endpoints and keeps stale failures quiet', () => {
        const {
            renderEndpointPoolHomeSummary,
            renderCompactEndpointPoolHomeSummary,
            setEndpointPoolHomeSummariesForTest
        } = endpointsModule;

        setEndpointPoolHomeSummariesForTest({}, 0, 'load failed');

        assert.equal(renderEndpointPoolHomeSummary({ name: 'OpenAI', authMode: 'api_key' }), '');
        assert.equal(renderCompactEndpointPoolHomeSummary({ name: 'OpenAI', authMode: 'api_key' }), '');

        const html = renderEndpointPoolHomeSummary({ name: 'Codex Pool', authMode: 'codex_token_pool' });
        assert.match(html, /endpoint-pool-home/);
        assert.match(html, /stale/);
        assert.doesNotMatch(html, /load failed/);
    });
});
