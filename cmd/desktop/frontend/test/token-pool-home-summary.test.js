import { after, describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const endpointsPath = resolve(__dirname, '../src/modules/endpoints.js');

const originalDocument = globalThis.document;
const originalWindow = globalThis.window;
const originalLocalStorage = globalThis.localStorage;
const originalSetTimeout = globalThis.setTimeout;
const storage = new Map();
function createFakeElement() {
    const children = new Map();
    return {
        dataset: {},
        style: {},
        className: '',
        textContent: '',
        innerHTML: '',
        isConnected: true,
        classList: {
            add() {},
            remove() {},
            toggle() {}
        },
        addEventListener() {},
        appendChild(child) {
            return child;
        },
        remove() {},
        closest() {
            return null;
        },
        querySelector(selector) {
            if (!String(selector || '').startsWith('#')) {
                return null;
            }
            if (!children.has(selector)) {
                children.set(selector, createFakeElement());
            }
            return children.get(selector);
        },
        querySelectorAll() {
            return [];
        }
    };
}
globalThis.document = {
    addEventListener() {},
    createElement() {
        return createFakeElement();
    },
    getElementById() {
        return null;
    },
    querySelectorAll() {
        return [];
    },
    body: createFakeElement()
};
globalThis.window = {
    addEventListener() {}
};
globalThis.localStorage = {
    getItem(key) {
        return storage.has(key) ? storage.get(key) : null;
    },
    setItem(key, value) {
        storage.set(key, String(value));
    },
    removeItem(key) {
        storage.delete(key);
    },
    clear() {
        storage.clear();
    }
};

const translations = {
    'tokenPool.homeTitle': 'Pool',
    'tokenPool.homeHealthy': 'healthy {active}/{total}',
    'tokenPool.homeProblems': 'problems {count}',
    'tokenPool.homeQuota': 'quota {primary} / {secondary}',
    'tokenPool.homeResetCredits': 'resets {count}',
    'tokenPool.homeResetCreditsShort': 'reset {count}',
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
    + '\nexport { renderEndpointPoolHomeSummary, renderCompactEndpointPoolHomeSummary, setEndpointPoolHomeSummariesForTest, scheduleEndpointPoolHomeAutoRefreshes, setEndpointPoolHomeRefreshStateForTest, waitForEndpointPoolHomeRefreshesForTest, scheduleEndpointPoolHomeResetCreditRefreshes, setEndpointPoolHomeResetCreditStateForTest, waitForEndpointPoolHomeResetCreditRefreshesForTest, confirmCodexResetCreditConsume };\n';
const endpointsModule = await import(`data:text/javascript;base64,${Buffer.from(dependencyStubs + endpointsSource).toString('base64')}`);

after(() => {
    globalThis.document = originalDocument;
    globalThis.window = originalWindow;
    globalThis.localStorage = originalLocalStorage;
    globalThis.setTimeout = originalSetTimeout;
});

describe('token pool homepage summary helpers', () => {
    after(() => {
        storage.clear();
    });

    it('renders Codex pool summary and rotates account previews from cache', () => {
        const {
            renderEndpointPoolHomeSummary,
            setEndpointPoolHomeSummariesForTest,
            setEndpointPoolHomeResetCreditStateForTest
        } = endpointsModule;

        setEndpointPoolHomeResetCreditStateForTest(Date.parse('2026-06-28T10:00:00Z'), {
            'Codex Pool::2': { date: '2026-06-28', count: 2, updatedAt: '2026-06-28T10:00:00Z' },
            'Codex Pool::3': { date: '2026-06-28', count: 1, updatedAt: '2026-06-28T10:00:00Z' }
        });
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
                    { id: 1, label: 'acct-a', status: 'active', enabled: true, primaryUsedPercent: 67, secondaryUsedPercent: 24 },
                    { id: 2, label: 'acct-b', status: 'invalid', enabled: true, hasError: true, errorText: 'unauthorized' },
                    { id: 3, label: 'acct-c', status: 'active', enabled: true }
                ]
            }
        }, 1);

        const html = renderEndpointPoolHomeSummary({ name: 'Codex Pool', authMode: 'codex_token_pool' });

        assert.match(html, /endpoint-pool-home/);
        assert.match(html, /healthy 1\/2/);
        assert.match(html, /quota 67% \/ 24%/);
        assert.match(html, /resets 3/);
        assert.match(html, /reset 2/);
        assert.match(html, /reset 1/);
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

    it('schedules a quiet background refresh when enabled accounts have no quota snapshot', async () => {
        const {
            scheduleEndpointPoolHomeAutoRefreshes,
            setEndpointPoolHomeRefreshStateForTest,
            waitForEndpointPoolHomeRefreshesForTest
        } = endpointsModule;
        const calls = [];
        let summaryReloads = 0;
        globalThis.window.go = { main: { App: {
            FetchCodexRateLimitsForEndpoint: async (endpointName) => {
                calls.push(endpointName);
                return JSON.stringify({ success: true, data: { updated: 1, failed: 0, skipped: 0 } });
            },
            GetCodexTokenPoolHomeSummaries: async () => {
                summaryReloads += 1;
                return JSON.stringify({ success: true, data: [] });
            }
        } } };
        setEndpointPoolHomeRefreshStateForTest(Date.parse('2026-06-28T10:00:00Z'));

        const scheduled = scheduleEndpointPoolHomeAutoRefreshes([{
            endpointName: 'Codex Pool',
            endpointIndex: 1,
            latestQuotaUpdatedAt: '2026-06-28T09:55:00Z',
            accounts: [
                { label: 'acct-a', enabled: true, status: 'active' }
            ]
        }]);

        assert.deepEqual(scheduled, ['Codex Pool']);
        await waitForEndpointPoolHomeRefreshesForTest();
        assert.deepEqual(calls, ['Codex Pool']);
        assert.equal(summaryReloads, 1);
    });

    it('does not refresh fresh quota snapshots', () => {
        const {
            scheduleEndpointPoolHomeAutoRefreshes,
            setEndpointPoolHomeRefreshStateForTest
        } = endpointsModule;
        const calls = [];
        globalThis.window.go = { main: { App: {
            FetchCodexRateLimitsForEndpoint: async (endpointName) => {
                calls.push(endpointName);
                return JSON.stringify({ success: true, data: {} });
            }
        } } };
        setEndpointPoolHomeRefreshStateForTest(Date.parse('2026-06-28T10:00:00Z'));

        const scheduled = scheduleEndpointPoolHomeAutoRefreshes([{
            endpointName: 'Codex Pool',
            endpointIndex: 1,
            latestQuotaUpdatedAt: '2026-06-28T09:50:01Z',
            accounts: [
                { label: 'acct-a', enabled: true, status: 'active', rateLimitStatus: 'ok' }
            ]
        }]);

        assert.deepEqual(scheduled, []);
        assert.deepEqual(calls, []);
    });

    it('throttles duplicate background refresh attempts while one is running or recently attempted', async () => {
        const {
            scheduleEndpointPoolHomeAutoRefreshes,
            setEndpointPoolHomeRefreshStateForTest,
            waitForEndpointPoolHomeRefreshesForTest
        } = endpointsModule;
        const calls = [];
        let releaseRefresh;
        globalThis.window.go = { main: { App: {
            FetchCodexRateLimitsForEndpoint: async (endpointName) => {
                calls.push(endpointName);
                await new Promise((resolve) => { releaseRefresh = resolve; });
                return JSON.stringify({ success: true, data: {} });
            },
            GetCodexTokenPoolHomeSummaries: async () => JSON.stringify({ success: true, data: [] })
        } } };
        const now = Date.parse('2026-06-28T10:00:00Z');
        const staleSummary = {
            endpointName: 'Codex Pool',
            endpointIndex: 1,
            latestQuotaUpdatedAt: '2026-06-28T09:00:00Z',
            accounts: [
                { label: 'acct-a', enabled: true, status: 'active', rateLimitStatus: 'ok' }
            ]
        };

        setEndpointPoolHomeRefreshStateForTest(now);
        assert.deepEqual(scheduleEndpointPoolHomeAutoRefreshes([staleSummary]), ['Codex Pool']);
        assert.deepEqual(scheduleEndpointPoolHomeAutoRefreshes([staleSummary]), []);
        assert.deepEqual(calls, ['Codex Pool']);
        releaseRefresh();
        await waitForEndpointPoolHomeRefreshesForTest();

        setEndpointPoolHomeRefreshStateForTest(now, { 'Codex Pool': now - 60_000 });
        assert.deepEqual(scheduleEndpointPoolHomeAutoRefreshes([staleSummary]), []);
    });

    it('refreshes reset credit counts at most once per day per enabled account', async () => {
        const {
            scheduleEndpointPoolHomeResetCreditRefreshes,
            setEndpointPoolHomeResetCreditStateForTest,
            waitForEndpointPoolHomeResetCreditRefreshesForTest
        } = endpointsModule;
        const calls = [];
        globalThis.window.go = { main: { App: {
            GetCodexResetCredits: async (endpointIndex, credentialID) => {
                calls.push([endpointIndex, credentialID]);
                return JSON.stringify({ success: true, data: { availableCount: credentialID === 11 ? 2 : 0 } });
            }
        } } };
        const summary = {
            endpointName: 'Codex Pool',
            endpointIndex: 4,
            accounts: [
                { id: 11, label: 'acct-a', enabled: true },
                { id: 12, label: 'acct-disabled', enabled: false }
            ]
        };

        setEndpointPoolHomeResetCreditStateForTest(Date.parse('2026-06-28T10:00:00Z'));
        assert.deepEqual(scheduleEndpointPoolHomeResetCreditRefreshes([summary]), ['Codex Pool::11']);
        await waitForEndpointPoolHomeResetCreditRefreshesForTest();
        assert.deepEqual(calls, [[4, 11]]);

        assert.deepEqual(scheduleEndpointPoolHomeResetCreditRefreshes([summary]), []);

        setEndpointPoolHomeResetCreditStateForTest(Date.parse('2026-06-29T10:00:00Z'));
        assert.deepEqual(scheduleEndpointPoolHomeResetCreditRefreshes([summary]), ['Codex Pool::11']);
    });

    it('retries reset credit refreshes after a same-day failure', async () => {
        const {
            scheduleEndpointPoolHomeResetCreditRefreshes,
            setEndpointPoolHomeResetCreditStateForTest,
            waitForEndpointPoolHomeResetCreditRefreshesForTest
        } = endpointsModule;
        const calls = [];
        globalThis.window.go = { main: { App: {
            GetCodexResetCredits: async (endpointIndex, credentialID) => {
                calls.push([endpointIndex, credentialID]);
                if (calls.length === 1) {
                    return JSON.stringify({ success: false, error: 'temporary network failure' });
                }
                return JSON.stringify({ success: true, data: { availableCount: 2 } });
            }
        } } };
        const summary = {
            endpointName: 'Codex Pool',
            endpointIndex: 4,
            accounts: [
                { id: 11, label: 'acct-a', enabled: true }
            ]
        };

        setEndpointPoolHomeResetCreditStateForTest(Date.parse('2026-06-28T10:00:00Z'));
        const originalConsoleWarn = console.warn;
        console.warn = () => {};
        try {
            assert.deepEqual(scheduleEndpointPoolHomeResetCreditRefreshes([summary]), ['Codex Pool::11']);
            await waitForEndpointPoolHomeResetCreditRefreshesForTest();
        } finally {
            console.warn = originalConsoleWarn;
        }

        assert.deepEqual(scheduleEndpointPoolHomeResetCreditRefreshes([summary]), ['Codex Pool::11']);
        await waitForEndpointPoolHomeResetCreditRefreshesForTest();
        assert.deepEqual(calls, [[4, 11], [4, 11]]);
        assert.deepEqual(scheduleEndpointPoolHomeResetCreditRefreshes([summary]), []);
    });

    it('invalidates cached reset credit counts after consuming a reset credit', async () => {
        const {
            confirmCodexResetCreditConsume,
            renderEndpointPoolHomeSummary,
            setEndpointPoolHomeResetCreditStateForTest,
            setEndpointPoolHomeSummariesForTest
        } = endpointsModule;
        const consumed = [];
        globalThis.setTimeout = (callback) => {
            callback();
            return 0;
        };
        globalThis.window.go = { main: { App: {
            ConsumeCodexResetCredit: async (endpointIndex, credentialID) => {
                consumed.push([endpointIndex, credentialID]);
                return JSON.stringify({ success: true, data: { consumed: true } });
            },
            FetchCodexRateLimitsForCredential: async () => JSON.stringify({ success: true, data: {} })
        } } };
        setEndpointPoolHomeResetCreditStateForTest(Date.parse('2026-06-28T10:00:00Z'), {
            'Codex Pool::11': { date: '2026-06-28', count: 2, updatedAt: '2026-06-28T10:00:00Z' }
        });
        setEndpointPoolHomeSummariesForTest({
            'Codex Pool': {
                endpointName: 'Codex Pool',
                endpointIndex: 4,
                totalAccounts: 1,
                activeAccounts: 1,
                accounts: [
                    { id: 11, label: 'acct-a', status: 'active', enabled: true }
                ]
            }
        });

        assert.match(renderEndpointPoolHomeSummary({ name: 'Codex Pool', authMode: 'codex_token_pool' }), /reset 2/);
        await confirmCodexResetCreditConsume(11, 'acct-a');

        assert.deepEqual(consumed, [[-1, 11]]);
        assert.doesNotMatch(renderEndpointPoolHomeSummary({ name: 'Codex Pool', authMode: 'codex_token_pool' }), /reset 2/);
    });
});
