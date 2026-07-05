import { getLanguage, t } from '../i18n/index.js';
import { escapeHtml, formatTokens, maskApiKey } from '../utils/format.js';
import { getEndpointStats } from './stats.js';
import { toggleEndpoint, testAllEndpointsZeroCost } from './config.js';
import { filterEndpoints, isFilterActive, updateFilterStats } from './filters.js';

// 提取基础名称，移除副本后缀
function extractBaseName(name) {
    // 移除类似 "(Copy)", "(副本)", "(Copy) 1", "(副本) 1" 等后缀
    // 使用固定的模式匹配，避免在函数内部调用 t()
    const copyPattern = /\(Copy\)(?:\s+\d+)?$/;
    const chineseCopyPattern = /\(副本\)(?:\s+\d+)?$/;
    
    let baseName = name.replace(copyPattern, '').trim();
    baseName = baseName.replace(chineseCopyPattern, '').trim();
    
    return baseName;
}

const ENDPOINT_TEST_STATUS_KEY = 'AINexus_endpointTestStatus';
const ENDPOINT_VIEW_MODE_KEY = 'AINexus_endpointViewMode';

// 获取端点测试状态
export function getEndpointTestStatus(endpointName) {
    try {
        const statusMap = JSON.parse(localStorage.getItem(ENDPOINT_TEST_STATUS_KEY) || '{}');
        return statusMap[endpointName]; // true=成功, false=失败, undefined=未测试
    } catch {
        return undefined;
    }
}

// 保存端点测试状态
export function saveEndpointTestStatus(endpointName, success) {
    try {
        const statusMap = JSON.parse(localStorage.getItem(ENDPOINT_TEST_STATUS_KEY) || '{}');
        statusMap[endpointName] = success;
        localStorage.setItem(ENDPOINT_TEST_STATUS_KEY, JSON.stringify(statusMap));
    } catch (error) {
        console.error('Failed to save endpoint test status:', error);
    }
}

// 获取端点视图模式
export function getEndpointViewMode() {
    try {
        return localStorage.getItem(ENDPOINT_VIEW_MODE_KEY) || 'detail';
    } catch {
        return 'detail';
    }
}

// 保存端点视图模式
export function saveEndpointViewMode(mode) {
    try {
        localStorage.setItem(ENDPOINT_VIEW_MODE_KEY, mode);
    } catch (error) {
        console.error('Failed to save endpoint view mode:', error);
    }
}

// 切换视图模式
export function switchEndpointViewMode(mode) {
    saveEndpointViewMode(mode);

    // 更新按钮状态
    const buttons = document.querySelectorAll('.view-mode-btn');
    buttons.forEach(btn => {
        btn.classList.toggle('active', btn.dataset.view === mode);
    });

    // 更新列表样式
    const container = document.getElementById('endpointList');
    if (mode === 'compact') {
        container.classList.add('compact-view');
    } else {
        container.classList.remove('compact-view');
    }

    // 重新渲染端点列表
    window.loadConfig();
}

// 初始化视图模式
export function initEndpointViewMode() {
    const mode = getEndpointViewMode();
    const buttons = document.querySelectorAll('.view-mode-btn');
    buttons.forEach(btn => {
        btn.classList.toggle('active', btn.dataset.view === mode);
    });
}

let currentTestButton = null;
let currentTestButtonOriginalText = '';
let currentTestIndex = -1;
let endpointPanelExpanded = true;
let tokenPoolCurrentIndex = -1;
let tokenPoolErrorCache = new Map();
let tokenPoolUsageCache = new Map();
let tokenPoolAuthPollTimer = null;
let tokenPoolAuthCountdownTimer = null;
let tokenPoolAuthLoginID = '';
let tokenPoolAuthPending = false;
let tokenPoolOpenActionMenu = null;
let currentEndpointName = '';
let endpointRuntimeStatuses = {};
let endpointActiveCounts = {};
let endpointPoolHomeSummaries = {};
let endpointPoolHomeLoadError = '';
let endpointPoolHomeRotation = 0;
let endpointPoolHomePollTimer = null;
let endpointPoolHomeRotateTimer = null;
const ENDPOINT_POOL_HOME_STALE_MS = 15 * 60 * 1000;
const ENDPOINT_POOL_HOME_RETRY_MS = 5 * 60 * 1000;
const ENDPOINT_POOL_HOME_RESET_CREDIT_KEY = 'AINexus_codexResetCreditHome';
let endpointPoolHomeRefreshNow = () => Date.now();
let endpointPoolHomeRefreshLastAttempt = new Map();
let endpointPoolHomeRefreshInFlight = new Map();
let endpointPoolHomeResetCreditNow = () => Date.now();
let endpointPoolHomeResetCreditInFlight = new Map();

function getLocalizedModal(id) {
    const modal = document.getElementById(id);
    if (!modal) {
        return null;
    }
    if (modal.dataset.language === getLanguage()) {
        return modal;
    }
    modal.remove();
    return null;
}

async function loadCurrentEndpointName() {
    try {
        currentEndpointName = await window.go.main.App.GetCurrentEndpoint();
    } catch (error) {
        console.error('Failed to get current endpoint:', error);
        currentEndpointName = '';
    }
    return currentEndpointName;
}

async function loadEndpointRuntimeStatuses() {
    try {
        if (!window.go?.main?.App?.GetEndpointRuntimeStatuses) {
            endpointRuntimeStatuses = {};
            return endpointRuntimeStatuses;
        }
        const raw = await window.go.main.App.GetEndpointRuntimeStatuses();
        endpointRuntimeStatuses = raw ? JSON.parse(raw) : {};
    } catch (error) {
        console.error('Failed to get endpoint runtime statuses:', error);
        endpointRuntimeStatuses = {};
    }
    return endpointRuntimeStatuses;
}

function formatRuntimeTime(value) {
    if (!value) {
        return '';
    }
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
        return '';
    }
    return date.toLocaleTimeString(undefined, {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false
    });
}

function formatFailureCode(status) {
    const reason = String(status.lastFailureReason || '').trim();
    const statusCode = Number(status.lastFailureStatusCode || 0);

    if (reason && statusCode > 0) {
        return `${reason}/${statusCode}`;
    }
    if (reason) {
        return reason;
    }
    if (statusCode > 0) {
        return `HTTP ${statusCode}`;
    }
    return '';
}

function renderDefaultEndpointControl(endpointName, enabled) {
    const safeName = escapeHtml(endpointName);
    const isDefaultEndpoint = endpointName === currentEndpointName;
    if (isDefaultEndpoint) {
        return '<span class="current-badge">' + t('endpoints.defaultEndpoint') + '</span>';
    }
    if (enabled) {
        return '<button class="btn btn-switch" data-action="switch" data-name="' + safeName + '">' + t('endpoints.switchTo') + '</button>';
    }
    return '';
}

function renderCompactDefaultEndpointControl(endpointName, enabled) {
    const safeName = escapeHtml(endpointName);
    const isDefaultEndpoint = endpointName === currentEndpointName;
    if (isDefaultEndpoint) {
        return '<span class="btn btn-primary compact-badge-btn">' + t('endpoints.defaultEndpoint') + '</span>';
    }
    if (enabled) {
        return '<button class="btn btn-primary compact-badge-btn" data-action="switch" data-name="' + safeName + '">' + t('endpoints.switchTo') + '</button>';
    }
    return '<span class="btn btn-primary compact-badge-btn compact-badge-disabled">' + t('endpoints.disabled') + '</span>';
}

function renderEndpointRuntimeBadges(endpointName, viewMode = 'detail') {
    const status = endpointRuntimeStatuses[endpointName] || {};
    const activeCount = endpointActiveCounts[endpointName] || 0;
    const isCompact = viewMode === 'compact';
    const badges = [];

    if (activeCount > 0) {
        const label = activeCount > 1
            ? `${t('endpoints.inUse')} x${activeCount}`
            : t('endpoints.inUse');
        badges.push(`<span class="runtime-badge runtime-badge-active" title="${t('endpoints.inUse')}">${label}</span>`);
    }

    const successTime = formatRuntimeTime(status.lastSuccessAt);
    if (successTime) {
        badges.push(`<span class="runtime-badge runtime-badge-success" title="${t('endpoints.recentSuccess')}">${t('endpoints.recentSuccess')} ${successTime}</span>`);
    }

    const failureTime = formatRuntimeTime(status.lastFailureAt);
    if (failureTime) {
        const failureCode = formatFailureCode(status);
        const title = failureCode ? `${t('endpoints.failureReason')}: ${failureCode}` : t('endpoints.recentFailure');
        const labelPrefix = isCompact ? t('endpoints.failureShort') : t('endpoints.recentFailure');
        const codeLabel = failureCode ? ` · ${escapeHtml(failureCode)}` : '';
        badges.push(`<span class="runtime-badge runtime-badge-failure" title="${escapeHtml(title)}">${labelPrefix} ${failureTime}${codeLabel}</span>`);
    }

    return badges.join('');
}

function isCodexTokenPoolHomeEndpoint(endpoint = {}) {
    return String(endpoint.authMode || '').trim().toLowerCase() === 'codex_token_pool';
}

function formatPoolHomePercent(value) {
    const number = Number(value || 0);
    if (!Number.isFinite(number) || number <= 0) {
        return '0%';
    }
    return `${Math.round(number)}%`;
}

function formatPoolHomeTime(value) {
    if (!value) {
        return '-';
    }
    return formatTokenPoolTime(value);
}

function endpointPoolHomeResetCreditDate() {
    const date = new Date(endpointPoolHomeResetCreditNow());
    if (Number.isNaN(date.getTime())) {
        return '';
    }
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
}

function readEndpointPoolHomeResetCreditCache() {
    try {
        const raw = localStorage.getItem(ENDPOINT_POOL_HOME_RESET_CREDIT_KEY);
        const data = raw ? JSON.parse(raw) : {};
        return data && typeof data === 'object' && !Array.isArray(data) ? data : {};
    } catch {
        return {};
    }
}

function writeEndpointPoolHomeResetCreditCache(cache = {}) {
    try {
        localStorage.setItem(ENDPOINT_POOL_HOME_RESET_CREDIT_KEY, JSON.stringify(cache));
    } catch (error) {
        console.warn('Failed to save Codex reset credit home cache:', error);
    }
}

function endpointPoolHomeResetCreditKey(summary = {}, account = {}) {
    const endpointName = String(summary.endpointName || '').trim();
    const credentialID = Number(account.id);
    if (!endpointName || !Number.isInteger(credentialID) || credentialID <= 0) {
        return '';
    }
    return `${endpointName}::${credentialID}`;
}

function getEndpointPoolHomeResetCreditEntry(summary = {}, account = {}) {
    const key = endpointPoolHomeResetCreditKey(summary, account);
    if (!key) {
        return null;
    }
    const entry = readEndpointPoolHomeResetCreditCache()[key];
    if (!entry || entry.date !== endpointPoolHomeResetCreditDate()) {
        return null;
    }
    const count = Number(entry.count);
    if (!Number.isFinite(count) || count < 0) {
        return null;
    }
    return {
        ...entry,
        count: Math.floor(count)
    };
}

function getEndpointPoolHomeResetCreditCount(summary = {}, account = {}) {
    const entry = getEndpointPoolHomeResetCreditEntry(summary, account);
    return entry ? entry.count : null;
}

function getEndpointPoolHomeResetCreditTotal(summary = {}) {
    const accounts = Array.isArray(summary.accounts) ? summary.accounts : [];
    let total = 0;
    let hasKnownCount = false;
    accounts.forEach((account) => {
        if (account?.enabled === false) {
            return;
        }
        const count = getEndpointPoolHomeResetCreditCount(summary, account);
        if (count === null) {
            return;
        }
        total += count;
        hasKnownCount = true;
    });
    return hasKnownCount ? total : null;
}

function renderPoolHomeAccount(account = {}, summary = {}) {
    const status = account.enabled === false ? t('tokenPool.statusLabels.disabled') : tokenPoolStatusLabel(account.status || 'active');
    const quota = [
        formatPoolHomePercent(account.primaryUsedPercent),
        formatPoolHomePercent(account.secondaryUsedPercent)
    ].join(' / ');
    const resetCreditCount = getEndpointPoolHomeResetCreditCount(summary, account);
    const resetCreditText = resetCreditCount === null ? '' : tt('tokenPool.homeResetCreditsShort', { count: resetCreditCount });
    const titleParts = [
        account.email || '',
        tt('tokenPool.homeStatus', { status }),
        tt('tokenPool.homeQuota', { primary: formatPoolHomePercent(account.primaryUsedPercent), secondary: formatPoolHomePercent(account.secondaryUsedPercent) })
    ];
    if (resetCreditText) {
        titleParts.push(resetCreditText);
    }
    if (account.resetAt) {
        titleParts.push(tt('tokenPool.homeReset', { time: formatPoolHomeTime(account.resetAt) }));
    }
    if (account.hasError) {
        titleParts.push(account.errorText || t('tokenPool.homeAccountError'));
    }
    return `
        <span class="endpoint-pool-home-account${account.hasError ? ' endpoint-pool-home-account-error' : ''}" title="${escapeHtml(titleParts.filter(Boolean).join(' · '))}">
            <strong>${escapeHtml(account.label || account.accountId || '-')}</strong>
            <span>${escapeHtml(status)}</span>
            <small>${escapeHtml(resetCreditText ? `${quota} · ${resetCreditText}` : quota)}</small>
        </span>
    `;
}

function getRotatedPoolHomeAccounts(accounts = [], limit = 2) {
    if (!Array.isArray(accounts) || accounts.length === 0) {
        return [];
    }
    const count = Math.min(limit, accounts.length);
    const start = endpointPoolHomeRotation % accounts.length;
    return Array.from({ length: count }, (_, offset) => accounts[(start + offset) % accounts.length]);
}

function renderEndpointPoolHomeSummary(endpoint = {}) {
    if (!isCodexTokenPoolHomeEndpoint(endpoint)) {
        return '';
    }
    const summary = endpointPoolHomeSummaries[endpoint.name];
    if (!summary) {
        if (!endpointPoolHomeLoadError) {
            return '';
        }
        return `
            <div class="endpoint-pool-home endpoint-pool-home-stale">
                <span>${t('tokenPool.homeTitle')}</span>
                <strong>${t('tokenPool.homeStale')}</strong>
            </div>
        `;
    }
    const accounts = getRotatedPoolHomeAccounts(summary.accounts, 2);
    const accountHTML = accounts.length
        ? accounts.map((account) => renderPoolHomeAccount(account, summary)).join('')
        : `<span class="endpoint-pool-home-empty">${t('tokenPool.homeNoAccounts')}</span>`;
    const resetCreditTotal = getEndpointPoolHomeResetCreditTotal(summary);
    const resetCreditHTML = resetCreditTotal === null
        ? ''
        : `<span class="endpoint-pool-home-badge">${tt('tokenPool.homeResetCredits', { count: resetCreditTotal })}</span>`;
    const problemHTML = (summary.problemAccounts || 0) > 0
        ? `<span class="endpoint-pool-home-badge endpoint-pool-home-badge-warn">${tt('tokenPool.homeProblems', { count: summary.problemAccounts || 0 })}</span>`
        : '';
    const staleHTML = endpointPoolHomeLoadError
        ? `<span class="endpoint-pool-home-badge endpoint-pool-home-badge-stale">${t('tokenPool.homeStale')}</span>`
        : '';
    return `
        <div class="endpoint-pool-home${endpointPoolHomeLoadError ? ' endpoint-pool-home-stale' : ''}" data-endpoint-name="${escapeHtml(endpoint.name || '')}">
            <div class="endpoint-pool-home-head">
                <span class="endpoint-pool-home-title">${t('tokenPool.homeTitle')}</span>
                <span class="endpoint-pool-home-badge">${tt('tokenPool.homeHealthy', {
                    active: summary.activeAccounts || 0,
                    total: summary.totalAccounts || 0
                })}</span>
                ${problemHTML}
                ${staleHTML}
                <span class="endpoint-pool-home-badge">${tt('tokenPool.homeQuota', {
                    primary: formatPoolHomePercent(summary.highestPrimaryUsedPercent),
                    secondary: formatPoolHomePercent(summary.highestSecondaryUsedPercent)
                })}</span>
                ${resetCreditHTML}
            </div>
            <div class="endpoint-pool-home-meta">
                <span>${tt('tokenPool.homeUpdated', { time: formatPoolHomeTime(summary.latestQuotaUpdatedAt) })}</span>
                <span>${tt('tokenPool.homeReset', { time: formatPoolHomeTime(summary.nextResetAt) })}</span>
            </div>
            <div class="endpoint-pool-home-accounts">${accountHTML}</div>
        </div>
    `;
}

function renderCompactEndpointPoolHomeSummary(endpoint = {}) {
    if (!isCodexTokenPoolHomeEndpoint(endpoint)) {
        return '';
    }
    const summary = endpointPoolHomeSummaries[endpoint.name];
    if (!summary) {
        return endpointPoolHomeLoadError
            ? `<span class="compact-pool-home compact-pool-home-stale">${t('tokenPool.homeStale')}</span>`
            : '';
    }
    const warningClass = (summary.problemAccounts || 0) > 0 ? ' compact-pool-home-warn' : '';
    const staleClass = endpointPoolHomeLoadError ? ' compact-pool-home-stale' : '';
    const account = getRotatedPoolHomeAccounts(summary.accounts, 1)[0];
    const accountLabel = account ? ` · ${account.label || account.accountId || '-'}` : '';
    const resetCreditTotal = getEndpointPoolHomeResetCreditTotal(summary);
    const resetCreditText = resetCreditTotal === null ? '' : ` · ${tt('tokenPool.homeResetCreditsShort', { count: resetCreditTotal })}`;
    return `<span class="compact-pool-home${warningClass}${staleClass}" title="${escapeHtml(tt('tokenPool.homeQuota', {
        primary: formatPoolHomePercent(summary.highestPrimaryUsedPercent),
        secondary: formatPoolHomePercent(summary.highestSecondaryUsedPercent)
    }))}${escapeHtml(resetCreditText)}">${tt('tokenPool.homeHealthy', {
        active: summary.activeAccounts || 0,
        total: summary.totalAccounts || 0
    })}${escapeHtml(resetCreditText)}${escapeHtml(accountLabel)}</span>`;
}

function updateEndpointPoolHomeSlots() {
    document.querySelectorAll('.endpoint-pool-home-slot').forEach(slot => {
        const endpointName = slot.dataset.name || '';
        const authMode = slot.dataset.authMode || '';
        const compact = slot.dataset.view === 'compact';
        const endpoint = { name: endpointName, authMode };
        slot.innerHTML = compact
            ? renderCompactEndpointPoolHomeSummary(endpoint)
            : renderEndpointPoolHomeSummary(endpoint);
    });
}

function endpointPoolHomeSummaryNeedsRefresh(summary = {}) {
    const endpointName = String(summary.endpointName || '').trim();
    if (!endpointName) {
        return false;
    }
    const enabledAccounts = (Array.isArray(summary.accounts) ? summary.accounts : [])
        .filter((account) => account?.enabled !== false);
    if (enabledAccounts.length === 0) {
        return false;
    }
    if (enabledAccounts.some((account) => !String(account?.rateLimitStatus || '').trim())) {
        return true;
    }

    const updatedAt = Date.parse(summary.latestQuotaUpdatedAt || '');
    if (!Number.isFinite(updatedAt)) {
        return true;
    }
    return endpointPoolHomeRefreshNow() - updatedAt >= ENDPOINT_POOL_HOME_STALE_MS;
}

function scheduleEndpointPoolHomeAutoRefreshes(summaries = []) {
    const scheduled = [];
    (Array.isArray(summaries) ? summaries : []).forEach((summary) => {
        const endpointName = String(summary?.endpointName || '').trim();
        if (!endpointName || !endpointPoolHomeSummaryNeedsRefresh(summary)) {
            return;
        }
        if (endpointPoolHomeRefreshInFlight.has(endpointName)) {
            return;
        }
        const now = endpointPoolHomeRefreshNow();
        const lastAttempt = endpointPoolHomeRefreshLastAttempt.get(endpointName) || 0;
        if (lastAttempt > 0 && now - lastAttempt < ENDPOINT_POOL_HOME_RETRY_MS) {
            return;
        }

        endpointPoolHomeRefreshLastAttempt.set(endpointName, now);
        const promise = refreshEndpointPoolHomeRateLimits(summary)
            .catch((error) => {
                console.warn('Failed to refresh Codex token pool home quota:', error);
            })
            .finally(() => {
                endpointPoolHomeRefreshInFlight.delete(endpointName);
            });
        endpointPoolHomeRefreshInFlight.set(endpointName, promise);
        scheduled.push(endpointName);
    });
    return scheduled;
}

async function refreshEndpointPoolHomeRateLimits(summary = {}) {
    const app = window.go?.main?.App;
    if (!app) {
        return;
    }
    const endpointName = String(summary.endpointName || '').trim();
    let raw = '';
    if (app.FetchCodexRateLimitsForEndpoint && endpointName) {
        raw = await app.FetchCodexRateLimitsForEndpoint(endpointName);
    } else if (app.FetchCodexRateLimits && Number.isInteger(summary.endpointIndex) && summary.endpointIndex >= 0) {
        raw = await app.FetchCodexRateLimits(summary.endpointIndex);
    } else {
        return;
    }
    const result = parseAppJSON(raw);
    if (!result.success) {
        throw new Error(result.error || t('tokenPool.rateLimitsFetchFailedFallback'));
    }
    await refreshEndpointPoolHomeSummaries({ skipAutoRefresh: true });
}

async function refreshEndpointPoolHomeSummaries(options = {}) {
    if (!window.go?.main?.App?.GetCodexTokenPoolHomeSummaries) {
        return;
    }
    let summaries = [];
    try {
        const raw = await window.go.main.App.GetCodexTokenPoolHomeSummaries();
        const result = parseAppJSON(raw);
        if (!result.success) {
            throw new Error(result.error || t('tokenPool.homeLoadFailed'));
        }
        endpointPoolHomeSummaries = {};
        summaries = Array.isArray(result.data) ? result.data : [];
        summaries.forEach((summary) => {
            if (summary?.endpointName) {
                endpointPoolHomeSummaries[summary.endpointName] = summary;
            }
        });
        endpointPoolHomeLoadError = '';
    } catch (error) {
        console.error('Failed to load Codex token pool home summaries:', error);
        endpointPoolHomeLoadError = error?.message || String(error);
    }
    updateEndpointPoolHomeSlots();
    if (!options.skipAutoRefresh && !endpointPoolHomeLoadError) {
        scheduleEndpointPoolHomeAutoRefreshes(summaries);
        scheduleEndpointPoolHomeResetCreditRefreshes(summaries);
    }
}

function writeEndpointPoolHomeResetCreditEntry(key, entry = {}) {
    if (!key) {
        return;
    }
    const cache = readEndpointPoolHomeResetCreditCache();
    cache[key] = {
        ...entry,
        date: entry.date || endpointPoolHomeResetCreditDate()
    };
    writeEndpointPoolHomeResetCreditCache(cache);
}

function invalidateEndpointPoolHomeResetCreditCacheForCredential(credentialID) {
    const id = Number(credentialID);
    if (!Number.isInteger(id) || id <= 0) {
        return;
    }
    const suffix = `::${id}`;
    const cache = readEndpointPoolHomeResetCreditCache();
    let changed = false;
    Object.keys(cache).forEach((key) => {
        if (key.endsWith(suffix)) {
            delete cache[key];
            changed = true;
        }
    });
    if (changed) {
        writeEndpointPoolHomeResetCreditCache(cache);
    }
}

async function refreshEndpointPoolHomeResetCredit(summary = {}, account = {}) {
    const app = window.go?.main?.App;
    if (!app?.GetCodexResetCredits) {
        return;
    }
    const key = endpointPoolHomeResetCreditKey(summary, account);
    const endpointIndex = Number(summary.endpointIndex);
    const credentialID = Number(account.id);
    if (!key || !Number.isInteger(endpointIndex) || endpointIndex < 0 || !Number.isInteger(credentialID) || credentialID <= 0) {
        return;
    }
    const raw = await app.GetCodexResetCredits(endpointIndex, credentialID);
    const result = parseAppJSON(raw);
    if (!result.success) {
        throw new Error(result.error || t('tokenPool.resetCreditLoadFailedFallback'));
    }
    const count = Number(result.data?.availableCount ?? 0);
    writeEndpointPoolHomeResetCreditEntry(key, {
        count: Number.isFinite(count) && count > 0 ? Math.floor(count) : 0,
        updatedAt: new Date(endpointPoolHomeResetCreditNow()).toISOString()
    });
    updateEndpointPoolHomeSlots();
}

function scheduleEndpointPoolHomeResetCreditRefreshes(summaries = []) {
    const app = window.go?.main?.App;
    if (!app?.GetCodexResetCredits) {
        return [];
    }
    const scheduled = [];
    (Array.isArray(summaries) ? summaries : []).forEach((summary) => {
        const endpointIndex = Number(summary?.endpointIndex);
        if (!Number.isInteger(endpointIndex) || endpointIndex < 0) {
            return;
        }
        const accounts = Array.isArray(summary.accounts) ? summary.accounts : [];
        accounts.forEach((account) => {
            if (account?.enabled === false) {
                return;
            }
            const key = endpointPoolHomeResetCreditKey(summary, account);
            if (!key || endpointPoolHomeResetCreditInFlight.has(key)) {
                return;
            }
            const cacheEntry = readEndpointPoolHomeResetCreditCache()[key];
            if (cacheEntry?.date === endpointPoolHomeResetCreditDate()) {
                return;
            }

            const promise = refreshEndpointPoolHomeResetCredit(summary, account)
                .catch((error) => {
                    console.warn('Failed to refresh Codex reset credit home count:', error);
                })
                .finally(() => {
                    endpointPoolHomeResetCreditInFlight.delete(key);
                });
            endpointPoolHomeResetCreditInFlight.set(key, promise);
            scheduled.push(key);
        });
    });
    return scheduled;
}

function ensureEndpointPoolHomeTimers() {
    if (!endpointPoolHomePollTimer) {
        endpointPoolHomePollTimer = setInterval(() => {
            if (document.visibilityState === 'hidden') {
                return;
            }
            refreshEndpointPoolHomeSummaries();
        }, 30000);
    }
    if (!endpointPoolHomeRotateTimer) {
        endpointPoolHomeRotateTimer = setInterval(() => {
            endpointPoolHomeRotation += 1;
            updateEndpointPoolHomeSlots();
        }, 6000);
    }
}

function setEndpointPoolHomeSummariesForTest(summaries = {}, rotation = 0, error = '') {
    endpointPoolHomeSummaries = summaries;
    endpointPoolHomeRotation = rotation;
    endpointPoolHomeLoadError = error;
}

function setEndpointPoolHomeRefreshStateForTest(nowMs = Date.now(), lastAttempts = {}) {
    endpointPoolHomeRefreshNow = () => nowMs;
    endpointPoolHomeRefreshLastAttempt = new Map(Object.entries(lastAttempts));
    endpointPoolHomeRefreshInFlight = new Map();
}

async function waitForEndpointPoolHomeRefreshesForTest() {
    await Promise.all(Array.from(endpointPoolHomeRefreshInFlight.values()));
}

function setEndpointPoolHomeResetCreditStateForTest(nowMs = Date.now(), cache = {}) {
    endpointPoolHomeResetCreditNow = () => nowMs;
    endpointPoolHomeResetCreditInFlight = new Map();
    writeEndpointPoolHomeResetCreditCache(cache);
}

async function waitForEndpointPoolHomeResetCreditRefreshesForTest() {
    await Promise.all(Array.from(endpointPoolHomeResetCreditInFlight.values()));
}

function updateRuntimeStatusSlot(endpointName) {
    document.querySelectorAll('.endpoint-runtime-slot').forEach(slot => {
        if (slot.dataset.name === endpointName) {
            const viewMode = slot.classList.contains('compact-runtime-slot') ? 'compact' : 'detail';
            slot.innerHTML = renderEndpointRuntimeBadges(endpointName, viewMode);
        }
    });
}

function updateDefaultEndpointSlots() {
    document.querySelectorAll('.endpoint-default-slot').forEach(slot => {
        const endpointName = slot.dataset.name || '';
        const enabled = slot.dataset.enabled !== 'false';
        const compact = slot.dataset.view === 'compact';
        slot.innerHTML = compact
            ? renderCompactDefaultEndpointControl(endpointName, enabled)
            : renderDefaultEndpointControl(endpointName, enabled);
        bindEndpointSwitchButton(slot.querySelector('[data-action="switch"]'));
    });
}

function bindEndpointSwitchButton(switchBtn) {
    if (!switchBtn || switchBtn.dataset.bound === 'true') {
        return;
    }
    switchBtn.dataset.bound = 'true';
    switchBtn.addEventListener('click', async () => {
        const name = switchBtn.getAttribute('data-name');
        try {
            switchBtn.disabled = true;
            switchBtn.innerHTML = '...';
            await window.go.main.App.SwitchToEndpoint(name);
            currentEndpointName = name;
            updateDefaultEndpointSlots();
        } catch (error) {
            console.error('Failed to switch endpoint:', error);
            alert(t('endpoints.switchFailed') + ': ' + error);
            if (switchBtn.isConnected) {
                switchBtn.disabled = false;
                switchBtn.innerHTML = t('endpoints.switchTo');
            }
        }
    });
}

function showNotification(message, type = 'info') {
    const notification = document.createElement('div');
    notification.className = `notification notification-${type}`;
    notification.textContent = message;
    document.body.appendChild(notification);
    setTimeout(() => notification.classList.add('show'), 10);
    setTimeout(() => {
        notification.classList.remove('show');
        setTimeout(() => notification.remove(), 300);
    }, 3000);
}

function copyToClipboard(text, button) {
    navigator.clipboard.writeText(text).then(() => {
        const originalHTML = button.innerHTML;
        button.innerHTML = '<svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" width="1em" height="1em"><path d="M20 6L9 17l-5-5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
        setTimeout(() => { button.innerHTML = originalHTML; }, 1000);
    });
}

export function getTestState() {
    return { currentTestButton, currentTestIndex };
}

export function clearTestState() {
    if (currentTestButton) {
        currentTestButton.disabled = false;
        currentTestButton.innerHTML = currentTestButtonOriginalText;

        // 恢复简洁视图的 moreBtn
        const endpointItem = currentTestButton.closest('.endpoint-item-compact');
        if (endpointItem) {
            const moreBtn = endpointItem.querySelector('[data-action="more"]');
            if (moreBtn) {
                moreBtn.disabled = false;
                moreBtn.innerHTML = '⋯';
            }
        }

        currentTestButton = null;
        currentTestButtonOriginalText = '';
        currentTestIndex = -1;
    }
}

export function setTestState(button, index) {
    currentTestButton = button;
    currentTestButtonOriginalText = button.innerHTML;
    currentTestIndex = index;
}

export async function renderEndpoints(endpoints) {
    const container = document.getElementById('endpointList');

    await loadCurrentEndpointName();
    await loadEndpointRuntimeStatuses();

    // 应用筛选
    const filteredEndpoints = filterEndpoints(endpoints);
    const isFiltered = isFilterActive();

    // 更新筛选统计
    updateFilterStats(endpoints.length, filteredEndpoints.length);

    // 空状态处理
    if (filteredEndpoints.length === 0) {
        container.innerHTML = `
            <div class="empty-state" style="text-align: center; padding: 60px 20px; color: #999;">
                <div style="font-size: 48px; margin-bottom: 15px;">🔍</div>
                <p style="font-size: 16px; margin-bottom: 20px;">
                    ${isFiltered ? t('endpoints.noMatchingEndpoints') : t('endpoints.noEndpoints')}
                </p>
                ${isFiltered ? `
                    <button class="btn btn-primary" onclick="window.clearAllFilters()">
                        🔄 ${t('endpoints.clearFilters')}
                    </button>
                ` : `
                    <button class="btn btn-primary" onclick="window.showAddEndpointModal()">
                        ➕ ${t('header.addEndpoint')}
                    </button>
                `}
            </div>
        `;
        return;
    }

    container.innerHTML = '';

    const endpointStats = getEndpointStats();
    // Display endpoints in config file order (no sorting by enabled status)
    // Keep original index from full endpoints array to avoid index mismatch after filtering
    const endpointIndexMap = new Map(endpoints.map((ep, idx) => [ep, idx]));
    const sortedEndpoints = filteredEndpoints.map((ep) => {
        const originalIndex = endpointIndexMap.has(ep)
            ? endpointIndexMap.get(ep)
            : endpoints.findIndex(item => item.name === ep.name);
        const stats = endpointStats[ep.name] || { requests: 0, errors: 0, inputTokens: 0, outputTokens: 0 };
        const enabled = ep.enabled !== undefined ? ep.enabled : true;
        return { endpoint: ep, originalIndex, stats, enabled };
    });

    // 检查视图模式
    const viewMode = getEndpointViewMode();
    if (viewMode === 'compact') {
        container.classList.add('compact-view');
        renderCompactView(sortedEndpoints, container, currentEndpointName, isFiltered);
        return;
    } else {
        container.classList.remove('compact-view');
    }

    sortedEndpoints.forEach(({ endpoint: ep, originalIndex: index, stats }) => {
        const totalTokens = stats.inputTokens + stats.outputTokens;
        const enabled = ep.enabled !== undefined ? ep.enabled : true;
        const transformer = ep.transformer || 'claude';
        const model = ep.model || '';
        const authMode = ep.authMode || 'api_key';

        const item = document.createElement('div');
        item.className = 'endpoint-item';
        item.dataset.name = ep.name;
        item.dataset.index = index;

        // 筛选激活时禁用拖拽
        if (isFiltered) {
            item.draggable = false;
            item.style.cursor = 'default';
            item.title = t('endpoints.dragDisabledDuringFilter');
        } else {
            item.draggable = true;
            setupDragAndDrop(item, container);
        }

        // 获取测试状态：true=成功显示✅，false=失败显示❌，undefined/unknown=未测试/未知显示⚠️
        const testStatus = getEndpointTestStatus(ep.name);
        let testStatusIcon = '⚠️';
        let testStatusTip = t('endpoints.testTipUnknown');
        if (testStatus === true) {
            testStatusIcon = '✅';
            testStatusTip = t('endpoints.testTipSuccess');
        } else if (testStatus === false) {
            testStatusIcon = '❌';
            testStatusTip = t('endpoints.testTipFailed');
        }

        item.innerHTML = `
            <div class="endpoint-info">
                <h3>
                    <span title="${testStatusTip}" style="cursor: help">${testStatusIcon}</span>
                    ${ep.name}
                    ${!enabled ? '<span class="disabled-badge">' + t('endpoints.disabled') + '</span>' : ''}
                    <span class="endpoint-default-slot" data-name="${escapeHtml(ep.name)}" data-enabled="${enabled ? 'true' : 'false'}" data-view="detail">${renderDefaultEndpointControl(ep.name, enabled)}</span>
                    <span class="endpoint-runtime-slot endpoint-status-badges" data-name="${escapeHtml(ep.name)}">${renderEndpointRuntimeBadges(ep.name, 'detail')}</span>
                </h3>
                <p style="display: flex; align-items: center; gap: 8px; min-width: 0;"><span style="white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">🌐 ${ep.apiUrl}</span> <button class="copy-btn" data-copy="${ep.apiUrl}" aria-label="${t('endpoints.copy')}" title="${t('endpoints.copy')}"><svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" width="1em" height="1em"><path d="M7 4c0-1.1.9-2 2-2h11a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2h-1V8c0-2-1-3-3-3H7V4Z" fill="currentColor"></path><path d="M5 7a2 2 0 0 0-2 2v10c0 1.1.9 2 2 2h10a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2H5Z" fill="currentColor"></path></svg></button></p>
                ${authMode === 'api_key'
                    ? `<p style="display: flex; align-items: center; gap: 8px; min-width: 0;"><span style="white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">🔑 ${maskApiKey(ep.apiKey)}</span> <button class="copy-btn" data-copy="${ep.apiKey}" aria-label="${t('endpoints.copy')}" title="${t('endpoints.copy')}"><svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" width="1em" height="1em"><path d="M7 4c0-1.1.9-2 2-2h11a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2h-1V8c0-2-1-3-3-3H7V4Z" fill="currentColor"></path><path d="M5 7a2 2 0 0 0-2 2v10c0 1.1.9 2 2 2h10a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2H5Z" fill="currentColor"></path></svg></button></p>`
                    : `<p style="color: #666; font-size: 14px; margin-top: 3px;">🪪 Using credential pool</p>`}
                <div class="endpoint-pool-home-slot" data-name="${escapeHtml(ep.name)}" data-auth-mode="${escapeHtml(authMode)}" data-view="detail">${renderEndpointPoolHomeSummary(ep)}</div>
                <p style="color: #666; font-size: 14px; margin-top: 5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">🔄 ${t('endpoints.transformer')}: ${transformer}${model ? ` (${model})` : ''}</p>
                <p style="color: #666; font-size: 14px; margin-top: 3px;">📊 ${t('endpoints.requests')}: ${stats.requests} | ${t('endpoints.errors')}: ${stats.errors}</p>
                <p style="color: #666; font-size: 14px; margin-top: 3px;">🎯 ${t('endpoints.tokens')}: ${formatTokens(totalTokens)} (${t('statistics.in')}: ${formatTokens(stats.inputTokens)}, ${t('statistics.out')}: ${formatTokens(stats.outputTokens)})</p>
                ${ep.remark ? `<p style="color: #888; font-size: 13px; margin-top: 5px; font-style: italic;" title="${ep.remark}">💬 ${ep.remark.length > 20 ? ep.remark.substring(0, 20) + '...' : ep.remark}</p>` : ''}
            </div>
            <div class="endpoint-actions">
                <label class="toggle-switch">
                    <input type="checkbox" data-index="${index}" ${enabled ? 'checked' : ''}>
                    <span class="toggle-slider"></span>
                </label>
                <button class="btn-card btn-secondary" data-action="test" data-index="${index}">${t('endpoints.test')}</button>
                <button class="btn-card btn-secondary" data-action="copy" data-index="${index}">${t('endpoints.copy')}</button>
                <button class="btn-card btn-secondary" data-action="edit" data-index="${index}">${t('endpoints.edit')}</button>
                <button class="btn-card btn-danger" data-action="delete" data-index="${index}">${t('endpoints.delete')}</button>
            </div>
        `;

        const testBtn = item.querySelector('[data-action="test"]');
        const editBtn = item.querySelector('[data-action="edit"]');
        const deleteBtn = item.querySelector('[data-action="delete"]');
        const toggleSwitch = item.querySelector('input[type="checkbox"]');
        const copyBtns = item.querySelectorAll('.copy-btn');

        if (currentTestIndex === index) {
            testBtn.disabled = true;
            testBtn.innerHTML = '⏳';
            currentTestButton = testBtn;
        }

        testBtn.addEventListener('click', () => {
            const idx = parseInt(testBtn.getAttribute('data-index'));
            window.testEndpoint(idx, testBtn);
        });
        const copyBtn = item.querySelector('[data-action="copy"]');
        copyBtn.addEventListener('click', () => {
            const idx = parseInt(copyBtn.getAttribute('data-index'));
            copyEndpointConfig(idx, copyBtn);
        });
        editBtn.addEventListener('click', () => {
            const idx = parseInt(editBtn.getAttribute('data-index'));
            window.editEndpoint(idx);
        });
        deleteBtn.addEventListener('click', () => {
            const idx = parseInt(deleteBtn.getAttribute('data-index'));
            window.deleteEndpoint(idx);
        });
        toggleSwitch.addEventListener('change', async (e) => {
            const idx = parseInt(e.target.getAttribute('data-index'));
            const newEnabled = e.target.checked;
            try {
                await toggleEndpoint(idx, newEnabled);
                window.loadConfig();
            } catch (error) {
                console.error('Failed to toggle endpoint:', error);
                alert('Failed to toggle endpoint: ' + error);
                e.target.checked = !newEnabled;
            }
        });
        copyBtns.forEach(btn => {
            btn.addEventListener('click', () => {
                copyToClipboard(btn.getAttribute('data-copy'), btn);
            });
        });

        // Add switch button event listener
        const switchBtn = item.querySelector('[data-action="switch"]');
        bindEndpointSwitchButton(switchBtn);

        container.appendChild(item);
    });

    ensureEndpointPoolHomeTimers();
    refreshEndpointPoolHomeSummaries();
}

function ensureTokenPoolModal() {
    let modal = getLocalizedModal('tokenPoolModal');
    if (modal) {
        return modal;
    }

    modal = document.createElement('div');
    modal.id = 'tokenPoolModal';
    modal.className = 'modal';
    modal.dataset.language = getLanguage();
    modal.innerHTML = `
        <div class="modal-content token-pool-modal-content">
            <div class="modal-header token-pool-modal-header">
                <div class="token-pool-title-block">
                    <h2 id="tokenPoolTitle">🪪 ${t('tokenPool.title')}</h2>
                    <div class="token-pool-mode-strip">
                        <span id="tokenPoolModeBadge" class="token-pool-mode-badge"></span>
                        <span id="tokenPoolEndpointName" class="token-pool-endpoint-name"></span>
                    </div>
                    <p id="tokenPoolModeDescription" class="token-pool-mode-description"></p>
                </div>
            </div>
            <div class="modal-body token-pool-modal-body">
                <div class="token-pool-toolbar">
                    <div id="tokenPoolHint" class="token-pool-hint" style="display: none;"></div>
                    <div id="tokenPoolStats" class="token-pool-stats"></div>
                    <div id="tokenPoolOverview" class="token-pool-overview" style="display: none;"></div>
                    <div class="token-pool-proxy-bar">
                        <label for="tokenPoolProxyUrl" class="token-pool-proxy-label">${t('settings.proxyUrl')}</label>
                        <input id="tokenPoolProxyUrl" class="form-input" type="text" placeholder="${t('settings.proxyUrlPlaceholder')}">
                        <button class="btn btn-secondary" id="tokenPoolProxySaveBtn">${t('tokenPool.save')}</button>
                        <button class="btn btn-secondary" id="tokenPoolProxyClearBtn">${t('tokenPool.clear')}</button>
                    </div>

                    <div class="form-group token-pool-import-group">
                        <div class="token-pool-import-header">
                            <label id="tokenPoolImportLabel">${t('tokenPool.batchImportJson')}</label>
                            <div class="token-pool-overwrite">
                                <label class="toggle-switch" style="margin: 0;">
                                    <input type="checkbox" id="tokenPoolOverwrite">
                                    <span class="toggle-slider"></span>
                                </label>
                                <label for="tokenPoolOverwrite" class="token-pool-overwrite-label">${t('tokenPool.overwriteExisting')}</label>
                            </div>
                        </div>
                        <textarea id="tokenPoolImportInput" class="token-pool-import-input" placeholder='${t('tokenPool.importPlaceholder')}'></textarea>
                        <div class="token-pool-command-row">
                            <button class="btn btn-primary" id="tokenPoolImportBtn">${t('tokenPool.import')}</button>
                            <button class="btn btn-secondary" id="tokenPoolImportFilesBtn">${t('tokenPool.importFiles')}</button>
                            <button class="btn btn-secondary" id="tokenPoolClaudeDiscoverBtn" style="display: none;">${t('tokenPool.discoverClaude')}</button>
                            <button class="btn btn-secondary" id="tokenPoolAuthBtn" style="display: none;">${t('tokenPool.auth')}</button>
                            <button class="btn btn-secondary" id="tokenPoolRefreshBtn">${t('tokenPool.refresh')}</button>
                            <button class="btn btn-secondary" id="tokenPoolRateRefreshBtn">${t('tokenPool.refreshLimits')}</button>
                        </div>
                    </div>
                </div>

                <div class="token-pool-table-wrap">
                    <table class="token-pool-table">
                        <colgroup>
                            <col class="token-pool-col-account">
                            <col class="token-pool-col-email">
                            <col class="token-pool-col-status">
                            <col class="token-pool-col-expires">
                            <col class="token-pool-col-rate token-pool-rate-header">
                            <col class="token-pool-col-error">
                            <col class="token-pool-col-actions">
                        </colgroup>
                        <thead>
                            <tr>
                                <th class="token-pool-col-account">${t('tokenPool.account')}</th>
                                <th class="token-pool-col-email">${t('tokenPool.email')}</th>
                                <th class="token-pool-col-status">${t('tokenPool.status')}</th>
                                <th class="token-pool-col-expires">${t('tokenPool.expiresAt')}</th>
                                <th class="token-pool-rate-header">${t('tokenPool.rateLimits')}</th>
                                <th class="token-pool-col-error">${t('tokenPool.lastError')}</th>
                                <th class="token-pool-col-actions">${t('tokenPool.actions')}</th>
                            </tr>
                        </thead>
                        <tbody id="tokenPoolTableBody"></tbody>
                    </table>
                </div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-secondary" id="tokenPoolCloseBtn">${t('tokenPool.close')}</button>
            </div>
        </div>
    `;

    document.body.appendChild(modal);

    const closeModal = () => {
        closeAllTokenPoolActionMenus();
        modal.classList.remove('active');
    };

    modal.addEventListener('click', (e) => {
        if (e.target === modal) {
            closeModal();
        }
    });
    modal.querySelector('.modal-body')?.addEventListener('click', () => {
        closeAllTokenPoolActionMenus();
    });
    modal.querySelector('#tokenPoolCloseBtn').addEventListener('click', closeModal);
    modal.querySelector('#tokenPoolImportBtn').addEventListener('click', handleTokenPoolImport);
    modal.querySelector('#tokenPoolImportFilesBtn').addEventListener('click', handleTokenPoolFileImport);
    modal.querySelector('#tokenPoolClaudeDiscoverBtn').addEventListener('click', handleClaudeOAuthDiscover);
    modal.querySelector('#tokenPoolAuthBtn').addEventListener('click', handleTokenPoolAuthStart);
    modal.querySelector('#tokenPoolRefreshBtn').addEventListener('click', async () => {
        await loadTokenPoolData(tokenPoolCurrentIndex);
    });
    modal.querySelector('#tokenPoolRateRefreshBtn').addEventListener('click', async () => {
        await refreshTokenPoolRateLimits(tokenPoolCurrentIndex);
    });
    modal.querySelector('#tokenPoolProxySaveBtn').addEventListener('click', async () => {
        await saveTokenPoolProxySetting();
    });
    modal.querySelector('#tokenPoolProxyClearBtn').addEventListener('click', async () => {
        await saveTokenPoolProxySetting(true);
    });

    return modal;
}

function isCodexTokenPoolEndpoint(index) {
    const allEndpoints = window.config?.endpoints || [];
    return index >= 0 && index < allEndpoints.length && allEndpoints[index]?.authMode === 'codex_token_pool';
}

function isClaudeOAuthTokenPoolEndpoint(index) {
    const allEndpoints = window.config?.endpoints || [];
    return index >= 0 && index < allEndpoints.length && allEndpoints[index]?.authMode === 'claude_oauth_token_pool';
}

function getTokenPoolManagerMode(index) {
    if (isCodexTokenPoolEndpoint(index)) {
        return 'codex';
    }
    if (isClaudeOAuthTokenPoolEndpoint(index)) {
        return 'claude';
    }
    return 'api';
}

function getTokenPoolManagerMeta(mode) {
    if (mode === 'codex') {
        return {
            title: t('tokenPool.codexTitle'),
            badge: t('tokenPool.codexBadge'),
            description: t('tokenPool.codexDescription'),
            importLabel: t('tokenPool.codexImportLabel'),
            placeholder: t('tokenPool.importPlaceholder')
        };
    }
    if (mode === 'claude') {
        return {
            title: t('tokenPool.claudeOAuthTitle'),
            badge: t('tokenPool.claudeOAuthBadge'),
            description: t('tokenPool.claudeOAuthDescription'),
            importLabel: t('tokenPool.claudeOAuthImportLabel'),
            placeholder: t('tokenPool.claudeImportPlaceholder')
        };
    }
    return {
        title: t('tokenPool.apiTokenPoolTitle'),
        badge: t('tokenPool.apiTokenPoolBadge'),
        description: t('tokenPool.apiTokenPoolDescription'),
        importLabel: t('tokenPool.apiTokenPoolImportLabel'),
        placeholder: t('tokenPool.importPlaceholder')
    };
}

function findEndpointIndexByName(endpointName) {
    const allEndpoints = window.config?.endpoints || [];
    if (!endpointName) {
        return -1;
    }
    return allEndpoints.findIndex((endpoint) => endpoint?.name === endpointName);
}

async function refreshTokenPoolEndpointConfig() {
    if (!window.go?.main?.App?.GetConfig) {
        return window.config || {};
    }
    try {
        const configStr = await window.go.main.App.GetConfig();
        const config = parseAppJSON(configStr);
        window.config = config;
        return config;
    } catch (error) {
        console.error('Failed to refresh token pool endpoint config:', error);
        return window.config || {};
    }
}

async function loadTokenPoolProxySetting() {
    const modal = ensureTokenPoolModal();
    const input = modal.querySelector('#tokenPoolProxyUrl');
    if (!input) {
        return;
    }

    try {
        const proxyUrl = tokenPoolCurrentIndex >= 0
            ? await window.go.main.App.GetEndpointProxyURL(tokenPoolCurrentIndex)
            : '';
        input.value = proxyUrl || '';
    } catch (error) {
        const message = error?.message || String(error);
        showNotification(tt('tokenPool.failedToLoadProxy', { error: message }), 'error');
    }
}

async function saveTokenPoolProxySetting(clear = false) {
    const modal = ensureTokenPoolModal();
    const input = modal.querySelector('#tokenPoolProxyUrl');
    if (!input) {
        return;
    }

    const proxyUrl = clear ? '' : input.value.trim();
    try {
        if (tokenPoolCurrentIndex < 0) {
            throw new Error('No endpoint selected');
        }
        await window.go.main.App.SetEndpointProxyURL(tokenPoolCurrentIndex, proxyUrl);
        if (Array.isArray(window.config?.endpoints) && window.config.endpoints[tokenPoolCurrentIndex]) {
            window.config.endpoints[tokenPoolCurrentIndex].proxyUrl = proxyUrl;
        }
        input.value = proxyUrl;
        showNotification(proxyUrl ? t('tokenPool.proxyUpdated') : t('tokenPool.proxyCleared'), 'success');
    } catch (error) {
        const message = error?.message || String(error);
        showNotification(tt('tokenPool.failedToSaveProxy', { error: message }), 'error');
    }
}

function parseAppJSON(value) {
    if (typeof value === 'string') {
        return JSON.parse(value);
    }
    return value;
}

function tt(key, replacements = {}) {
    let value = t(key);
    Object.entries(replacements).forEach(([name, replacement]) => {
        value = value.replaceAll(`{${name}}`, String(replacement));
    });
    return value;
}

function tokenPoolStatusLabel(status) {
    const normalized = (status || '').trim();
    if (!normalized) {
        return '';
    }
    const key = `tokenPool.statusLabels.${normalized}`;
    const translated = t(key);
    return translated === key ? normalized.replaceAll('_', ' ') : translated;
}

function tokenPoolSummaryDetail(data = {}) {
    return tt('tokenPool.summaryDetail', {
        updated: data.updated || 0,
        failed: data.failed || 0,
        skipped: data.skipped || 0
    });
}

function maskTokenPoolAccountID(accountId) {
    const raw = (accountId || '').trim();
    if (!raw) {
        return '-';
    }
    return `${raw.slice(0, 8)}*`;
}

function maskTokenPoolEmail(email) {
    const raw = (email || '').trim();
    if (!raw || !raw.includes('@')) {
        return raw || '-';
    }
    const [local, domain] = raw.split('@');
    if (!local || !domain) {
        return raw;
    }
    const localMasked = local.length <= 2
        ? `${local[0] || ''}*`
        : `${local[0]}*${local.slice(-2)}`;
    const domainParts = domain.split('.');
    const tld = domainParts.length > 1 ? domainParts[domainParts.length - 1] : '';
    const firstLabel = domainParts[0] || '';
    const domainMasked = firstLabel
        ? `${firstLabel[0]}*${tld ? tld : ''}`
        : `*${tld ? tld : ''}`;
    return `${localMasked}@${domainMasked}`;
}

function ensureTokenPoolErrorModal() {
    let modal = getLocalizedModal('tokenPoolErrorModal');
    if (modal) {
        return modal;
    }

    modal = document.createElement('div');
    modal.id = 'tokenPoolErrorModal';
    modal.className = 'modal';
    modal.dataset.language = getLanguage();
    modal.innerHTML = `
        <div class="modal-content token-pool-error-modal-content">
            <div class="modal-header">
                <h2>🧾 ${t('tokenPool.lastErrorTitle')}</h2>
            </div>
            <div class="modal-body">
                <pre id="tokenPoolErrorText" class="token-pool-error-pre"></pre>
            </div>
            <div class="modal-footer">
                <button class="btn btn-secondary" id="tokenPoolErrorCloseBtn">${t('tokenPool.close')}</button>
            </div>
        </div>
    `;

    document.body.appendChild(modal);
    const closeModal = () => modal.classList.remove('active');
    modal.addEventListener('click', (event) => {
        if (event.target === modal) {
            closeModal();
        }
    });
    modal.querySelector('#tokenPoolErrorCloseBtn')?.addEventListener('click', closeModal);
    return modal;
}

function showTokenPoolErrorDialog(errorText) {
    const modal = ensureTokenPoolErrorModal();
    const textEl = modal.querySelector('#tokenPoolErrorText');
    if (textEl) {
        textEl.textContent = (errorText || '').trim() || '-';
    }
    modal.classList.add('active');
}

function ensureTokenPoolUsageModal() {
    let modal = getLocalizedModal('tokenPoolUsageModal');
    if (modal) {
        return modal;
    }

    modal = document.createElement('div');
    modal.id = 'tokenPoolUsageModal';
    modal.className = 'modal';
    modal.dataset.language = getLanguage();
    modal.innerHTML = `
        <div class="modal-content token-pool-usage-modal-content">
            <div class="modal-header">
                <h2 id="tokenPoolUsageTitle">📊 ${t('tokenPool.usage')}</h2>
            </div>
            <div class="modal-body">
                <div id="tokenPoolUsageBody" class="token-pool-usage-body"></div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-secondary" id="tokenPoolUsageCloseBtn">${t('tokenPool.close')}</button>
            </div>
        </div>
    `;

    document.body.appendChild(modal);
    const closeModal = () => modal.classList.remove('active');
    modal.addEventListener('click', (event) => {
        if (event.target === modal) {
            closeModal();
        }
    });
    modal.querySelector('#tokenPoolUsageCloseBtn')?.addEventListener('click', closeModal);
    return modal;
}

function showTokenPoolUsageDialog(label, usage) {
    const modal = ensureTokenPoolUsageModal();
    const title = modal.querySelector('#tokenPoolUsageTitle');
    const body = modal.querySelector('#tokenPoolUsageBody');
    if (title) {
        title.textContent = `📊 ${t('tokenPool.usage')}${label ? `: ${label}` : ''}`;
    }

    if (!usage) {
        body.innerHTML = `<div class="token-pool-usage-empty">${t('tokenPool.noUsage')}</div>`;
    } else {
        const totalTokens = (usage.inputTokens || 0) + (usage.outputTokens || 0);
        const updatedAt = usage.updatedAt ? formatTokenPoolTime(usage.updatedAt) : '-';
        body.innerHTML = `
            <div class="token-pool-usage-grid">
                <div>${t('tokenPool.requests')}</div><div>${usage.requests || 0}</div>
                <div>${t('tokenPool.errors')}</div><div>${usage.errors || 0}</div>
                <div>${t('tokenPool.totalTokens')}</div><div>${formatTokens(totalTokens)}</div>
                <div>${t('tokenPool.inputTokens')}</div><div>${formatTokens(usage.inputTokens || 0)}</div>
                <div>${t('tokenPool.outputTokens')}</div><div>${formatTokens(usage.outputTokens || 0)}</div>
                <div>${t('tokenPool.updated')}</div><div>${escapeHtml(updatedAt)}</div>
            </div>
        `;
    }

    modal.classList.add('active');
}

function formatTokenPoolUnixTime(value) {
    const num = Number(value || 0);
    if (!Number.isFinite(num) || num <= 0) {
        return t('tokenPool.resetCreditTimeUnknown');
    }
    return formatTokenPoolTime(new Date(num * 1000).toISOString());
}

function resetCreditStatusLabel(credit = {}) {
    const status = String(credit.status || credit.rawStatus || '').trim().toLowerCase();
    if (status === 'available' || status === '') {
        return t('tokenPool.resetCreditStatusAvailable');
    }
    if (['redeemed', 'used', 'consumed'].includes(status)) {
        return t('tokenPool.resetCreditStatusRedeemed');
    }
    if (status === 'expired') {
        return t('tokenPool.resetCreditStatusExpired');
    }
    return `${t('tokenPool.resetCreditStatusUnknown')} (${escapeHtml(status)})`;
}

function resetCreditStatusClass(credit = {}) {
    const status = String(credit.status || credit.rawStatus || '').trim().toLowerCase();
    if (status === 'available' || status === '') {
        return 'is-available';
    }
    if (['redeemed', 'used', 'consumed'].includes(status)) {
        return 'is-redeemed';
    }
    if (status === 'expired') {
        return 'is-expired';
    }
    return 'is-unknown';
}

function ensureCodexResetCreditModal() {
    let modal = getLocalizedModal('codexResetCreditModal');
    if (modal) {
        return modal;
    }

    modal = document.createElement('div');
    modal.id = 'codexResetCreditModal';
    modal.className = 'modal';
    modal.dataset.language = getLanguage();
    modal.style.zIndex = '1002';
    modal.innerHTML = `
        <div class="modal-content token-pool-reset-credit-modal-content">
            <div class="modal-header">
                <h2>${t('tokenPool.resetCreditDialogTitle')}</h2>
            </div>
            <div class="modal-body">
                <div id="resetCreditBody" class="token-pool-reset-credit-body"></div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-secondary" id="resetCreditCancelAction">${t('tokenPool.cancel')}</button>
                <button class="btn btn-primary" id="resetCreditConfirmAction">${t('tokenPool.resetCreditDialogAction')}</button>
            </div>
        </div>
    `;
    document.body.appendChild(modal);
    return modal;
}

function showCodexResetCreditDialog(label, snapshot = {}) {
    return new Promise((resolve) => {
        const modal = ensureCodexResetCreditModal();
        const body = modal.querySelector('#resetCreditBody');
        const confirmButton = modal.querySelector('#resetCreditConfirmAction');
        const cancelButton = modal.querySelector('#resetCreditCancelAction');
        const availableCount = Number(snapshot.availableCount || 0);
        const credits = Array.isArray(snapshot.credits) ? snapshot.credits : [];
        const nextExpiry = snapshot.nextExpiresAt ? formatTokenPoolUnixTime(snapshot.nextExpiresAt) : '';
        const detailRows = credits.length
            ? credits.map((credit, index) => `
                <div class="token-pool-reset-credit-row">
                    <div>
                        <strong>${escapeHtml(credit.id || `#${index + 1}`)}</strong>
                        <span>${escapeHtml(credit.resetType || 'rate_limit')}</span>
                    </div>
                    <div class="token-pool-reset-credit-times">
                        <span>${t('tokenPool.resetCreditGrantedAt')}: ${escapeHtml(formatTokenPoolUnixTime(credit.grantedAt))}</span>
                        <span>${t('tokenPool.resetCreditExpiresAt')}: ${escapeHtml(formatTokenPoolUnixTime(credit.expiresAt))}</span>
                    </div>
                    <span class="token-pool-reset-credit-status ${resetCreditStatusClass(credit)}">${resetCreditStatusLabel(credit)}</span>
                </div>
            `).join('')
            : `<div class="token-pool-reset-credit-empty">${t('tokenPool.resetCreditNoRecords')}</div>`;

        body.innerHTML = `
            <p class="token-pool-reset-credit-desc">${tt('tokenPool.resetCreditDialogDesc', { count: availableCount })}</p>
            <div class="token-pool-reset-credit-account">${escapeHtml(label || '')}</div>
            ${nextExpiry ? `<div class="token-pool-reset-credit-expiry">${tt('tokenPool.resetCreditNextExpiry', { time: nextExpiry })}</div>` : ''}
            ${availableCount <= 0 ? `<div class="token-pool-reset-credit-warning">${t('tokenPool.resetCreditNoCredits')}</div>` : ''}
            <div class="token-pool-reset-credit-details">
                <strong>${t('tokenPool.resetCreditDetailsTitle')}</strong>
                ${detailRows}
            </div>
        `;
        confirmButton.disabled = availableCount <= 0;

        const closeModal = (confirmed) => {
            modal.classList.remove('active');
            confirmButton.onclick = null;
            cancelButton.onclick = null;
            modal.onclick = null;
            resolve(confirmed);
        };
        confirmButton.onclick = () => closeModal(true);
        cancelButton.onclick = () => closeModal(false);
        modal.onclick = (event) => {
            if (event.target === modal) {
                closeModal(false);
            }
        };
        modal.classList.add('active');
    });
}

async function confirmCodexResetCreditConsume(credentialID, label) {
    const modal = ensureTokenPoolModal();
    setTokenPoolHint(modal, tt('tokenPool.resetCreditConsuming', { label }));
    const raw = await window.go.main.App.ConsumeCodexResetCredit(tokenPoolCurrentIndex, credentialID);
    const result = parseAppJSON(raw);
    if (!result.success) {
        throw new Error(result.error || t('tokenPool.resetCreditFailedFallback'));
    }
    invalidateEndpointPoolHomeResetCreditCacheForCredential(credentialID);
    try {
        const refreshRaw = await window.go.main.App.FetchCodexRateLimitsForCredential(tokenPoolCurrentIndex, credentialID);
        const refreshResult = parseAppJSON(refreshRaw);
        if (!refreshResult.success) {
            throw new Error(refreshResult.error || t('tokenPool.rateLimitRefreshFailedFallback'));
        }
    } catch (error) {
        throw new Error(tt('tokenPool.resetCreditRefreshAfterConsumeFailed', {
            error: error?.message || String(error)
        }));
    }
    await loadTokenPoolData(tokenPoolCurrentIndex);
    const message = tt('tokenPool.resetCreditConsumed', { label });
    showNotification(message, 'success');
    setTokenPoolHint(modal, message);
}

function ensureTokenPoolAuthModal() {
    let modal = getLocalizedModal('tokenPoolAuthModal');
    if (modal) {
        return modal;
    }

    modal = document.createElement('div');
    modal.id = 'tokenPoolAuthModal';
    modal.className = 'modal';
    modal.dataset.language = getLanguage();
    modal.style.zIndex = '1002';
    modal.innerHTML = `
        <div class="modal-content" style="max-width: 560px;">
            <div class="modal-header">
                <h2>${t('tokenPool.authTitle')}</h2>
            </div>
            <div class="modal-body">
                <div id="tokenPoolAuthStatus" style="font-size: 13px; margin-bottom: 12px;">${t('tokenPool.preparing')}</div>
                <div style="display: grid; gap: 12px;">
                    <div>
                        <div style="font-size: 12px; color: #6b7280; margin-bottom: 4px;">${t('tokenPool.verificationUrl')}</div>
                        <a id="tokenPoolAuthUrl" href="#" target="_blank" rel="noreferrer" style="word-break: break-all;"></a>
                    </div>
                    <div>
                        <div style="font-size: 12px; color: #6b7280; margin-bottom: 4px;">${t('tokenPool.userCode')}</div>
                        <code id="tokenPoolAuthCode" style="display: inline-block; font-size: 22px; letter-spacing: 0; padding: 8px 10px; border-radius: 6px; background: rgba(148, 163, 184, 0.16);"></code>
                    </div>
                    <div id="tokenPoolAuthCountdown" style="font-size: 12px; color: #6b7280;"></div>
                </div>
                <div style="display: flex; gap: 8px; flex-wrap: wrap; margin-top: 16px;">
                    <button class="btn btn-primary" id="tokenPoolAuthOpenBtn">${t('tokenPool.open')}</button>
                    <button class="btn btn-secondary" id="tokenPoolAuthCopyUrlBtn">${t('tokenPool.copyUrl')}</button>
                    <button class="btn btn-secondary" id="tokenPoolAuthCopyCodeBtn">${t('tokenPool.copyCode')}</button>
                    <button class="btn btn-secondary" id="tokenPoolAuthCancelBtn">${t('tokenPool.cancelLogin')}</button>
                </div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-secondary" id="tokenPoolAuthCloseBtn">${t('tokenPool.close')}</button>
            </div>
        </div>
    `;
    document.body.appendChild(modal);

    const closeModal = () => modal.classList.remove('active');
    modal.addEventListener('click', (event) => {
        if (event.target === modal) {
            closeModal();
        }
    });
    modal.querySelector('#tokenPoolAuthCloseBtn')?.addEventListener('click', closeModal);
    modal.querySelector('#tokenPoolAuthOpenBtn')?.addEventListener('click', () => {
        const url = modal.querySelector('#tokenPoolAuthUrl')?.textContent || '';
        if (url && window.go?.main?.App?.OpenURL) {
            window.go.main.App.OpenURL(url);
        }
    });
    modal.querySelector('#tokenPoolAuthCopyUrlBtn')?.addEventListener('click', (event) => {
        const url = modal.querySelector('#tokenPoolAuthUrl')?.textContent || '';
        if (url) {
            copyToClipboard(url, event.currentTarget);
        }
    });
    modal.querySelector('#tokenPoolAuthCopyCodeBtn')?.addEventListener('click', (event) => {
        const code = modal.querySelector('#tokenPoolAuthCode')?.textContent || '';
        if (code) {
            copyToClipboard(code, event.currentTarget);
        }
    });
    modal.querySelector('#tokenPoolAuthCancelBtn')?.addEventListener('click', cancelTokenPoolAuthLogin);
    return modal;
}

function clearTokenPoolAuthTimers() {
    if (tokenPoolAuthPollTimer) {
        clearInterval(tokenPoolAuthPollTimer);
        tokenPoolAuthPollTimer = null;
    }
    if (tokenPoolAuthCountdownTimer) {
        clearInterval(tokenPoolAuthCountdownTimer);
        tokenPoolAuthCountdownTimer = null;
    }
}

function setTokenPoolAuthStatus(message, type = 'info') {
    const modal = ensureTokenPoolAuthModal();
    const statusEl = modal.querySelector('#tokenPoolAuthStatus');
    if (!statusEl) {
        return;
    }
    const colors = {
        info: '#374151',
        success: '#047857',
        warning: '#b45309',
        error: '#b91c1c'
    };
    statusEl.textContent = message;
    statusEl.style.color = colors[type] || colors.info;
}

function updateTokenPoolAuthCountdown(expiresAt) {
    const modal = ensureTokenPoolAuthModal();
    const countdownEl = modal.querySelector('#tokenPoolAuthCountdown');
    if (!countdownEl) {
        return;
    }
    const expires = Date.parse(expiresAt);
    if (Number.isNaN(expires)) {
        countdownEl.textContent = '';
        return;
    }
    const remaining = Math.max(0, Math.ceil((expires - Date.now()) / 1000));
    const minutes = Math.floor(remaining / 60);
    const seconds = String(remaining % 60).padStart(2, '0');
    countdownEl.textContent = tt('tokenPool.expiresIn', { time: `${minutes}:${seconds}` });
}

async function handleTokenPoolAuthStart() {
    if (tokenPoolCurrentIndex < 0) {
        return;
    }
    if (!isCodexTokenPoolEndpoint(tokenPoolCurrentIndex)) {
        showNotification(t('tokenPool.authOnlyCodex'), 'warning');
        return;
    }

    const modal = ensureTokenPoolAuthModal();
    clearTokenPoolAuthTimers();
    tokenPoolAuthLoginID = '';
    tokenPoolAuthPending = false;
    modal.classList.add('active');
    setTokenPoolAuthStatus(t('tokenPool.authRequesting'));

    try {
        const raw = await window.go.main.App.StartCodexCredentialAuth(tokenPoolCurrentIndex);
        const parsed = parseAppJSON(raw);
        if (!parsed.success) {
            throw new Error(parsed.error || t('tokenPool.authStartFailed'));
        }
        const data = parsed.data || {};
        tokenPoolAuthLoginID = data.loginId || '';
        tokenPoolAuthPending = true;

        const urlEl = modal.querySelector('#tokenPoolAuthUrl');
        const codeEl = modal.querySelector('#tokenPoolAuthCode');
        if (urlEl) {
            urlEl.textContent = data.verificationUrl || '';
            urlEl.href = data.verificationUrl || '#';
        }
        if (codeEl) {
            codeEl.textContent = data.userCode || '';
        }
        setTokenPoolAuthStatus(t('tokenPool.authWaiting'));
        updateTokenPoolAuthCountdown(data.expiresAt);
        tokenPoolAuthCountdownTimer = setInterval(() => updateTokenPoolAuthCountdown(data.expiresAt), 1000);

        const intervalMs = Math.max(2000, Number(data.pollIntervalSeconds || 2) * 1000);
        tokenPoolAuthPollTimer = setInterval(pollTokenPoolAuthStatus, intervalMs);
        await pollTokenPoolAuthStatus();
    } catch (error) {
        const message = error?.message || String(error);
        clearTokenPoolAuthTimers();
        tokenPoolAuthPending = false;
        setTokenPoolAuthStatus(tt('tokenPool.authFailed', { error: message }), 'error');
        showNotification(tt('tokenPool.authFailed', { error: message }), 'error');
    }
}

async function pollTokenPoolAuthStatus() {
    if (!tokenPoolAuthLoginID) {
        return;
    }
    try {
        const raw = await window.go.main.App.GetCodexCredentialAuthStatus(tokenPoolAuthLoginID);
        const parsed = parseAppJSON(raw);
        if (!parsed.success) {
            throw new Error(parsed.error || t('tokenPool.authStatusFailedFallback'));
        }
        const data = parsed.data || {};
        switch (data.status) {
            case 'complete':
                clearTokenPoolAuthTimers();
                tokenPoolAuthPending = false;
                setTokenPoolAuthStatus(`${t('tokenPool.authComplete')}${data.email ? `: ${data.email}` : ''}`, 'success');
                showNotification(t('tokenPool.authImported'), 'success');
                await loadTokenPoolData(tokenPoolCurrentIndex);
                if (window.loadConfig) {
                    window.loadConfig();
                }
                break;
            case 'failed':
            case 'expired':
                clearTokenPoolAuthTimers();
                tokenPoolAuthPending = false;
                setTokenPoolAuthStatus(tt('tokenPool.authStateFailed', {
                    status: tokenPoolStatusLabel(data.status),
                    error: data.error || t('tokenPool.unknownError')
                }), 'error');
                showNotification(tt('tokenPool.authStateNotification', {
                    status: tokenPoolStatusLabel(data.status)
                }), 'error');
                break;
            case 'canceled':
                clearTokenPoolAuthTimers();
                tokenPoolAuthPending = false;
                setTokenPoolAuthStatus(t('tokenPool.authCanceled'), 'warning');
                break;
            default:
                setTokenPoolAuthStatus(t('tokenPool.authWaiting'));
        }
    } catch (error) {
        const message = error?.message || String(error);
        clearTokenPoolAuthTimers();
        tokenPoolAuthPending = false;
        setTokenPoolAuthStatus(tt('tokenPool.authStatusFailed', { error: message }), 'error');
    }
}

async function cancelTokenPoolAuthLogin() {
    if (!tokenPoolAuthLoginID || !tokenPoolAuthPending) {
        return;
    }
    try {
        const raw = await window.go.main.App.CancelCodexCredentialAuth(tokenPoolAuthLoginID);
        const parsed = parseAppJSON(raw);
        if (!parsed.success) {
            throw new Error(parsed.error || t('tokenPool.cancelFailedFallback'));
        }
        clearTokenPoolAuthTimers();
        tokenPoolAuthPending = false;
        setTokenPoolAuthStatus(t('tokenPool.authCanceled'), 'warning');
    } catch (error) {
        const message = error?.message || String(error);
        showNotification(tt('tokenPool.cancelFailed', { error: message }), 'error');
    }
}

function showTokenPoolUpdateTokenDialog() {
    return new Promise((resolve) => {
        const modal = document.createElement('div');
        modal.id = 'tokenPoolUpdateTokenModal';
        modal.className = 'modal active';
        modal.style.zIndex = '1002';
        modal.innerHTML = `
            <div class="modal-content">
                <div class="modal-header">
                    <h2>🔑 ${t('tokenPool.updateTokenTitle')}</h2>
                </div>
                <div class="modal-body">
                    <div class="prompt-dialog">
                        <p><span class="required">*</span>${t('tokenPool.accessToken')}</p>
                        <div class="prompt-body">
                            <textarea id="tokenPoolUpdateAccess" class="form-input" rows="4" placeholder="${t('tokenPool.accessTokenPlaceholder')}"></textarea>
                        </div>
                        <p style="margin-top: 12px;">${t('tokenPool.expiresAtOptional')}</p>
                        <div class="prompt-body">
                            <input type="text" id="tokenPoolUpdateExpires" class="form-input" placeholder="2026-03-18T09:22:23Z" />
                        </div>
                        <div class="prompt-actions">
                            <button class="btn btn-primary" id="tokenPoolUpdateOk">${t('tokenPool.ok')}</button>
                            <button class="btn btn-secondary" id="tokenPoolUpdateCancel">${t('tokenPool.cancel')}</button>
                        </div>
                    </div>
                </div>
            </div>
        `;
        document.body.appendChild(modal);

        const accessEl = modal.querySelector('#tokenPoolUpdateAccess');
        const expiresEl = modal.querySelector('#tokenPoolUpdateExpires');
        setTimeout(() => accessEl?.focus(), 50);

        const closeModal = (value) => {
            modal.classList.remove('active');
            setTimeout(() => modal.remove(), 200);
            resolve(value);
        };

        const handleSubmit = () => {
            const token = (accessEl?.value || '').trim();
            if (!token) {
                showNotification(t('tokenPool.accessTokenRequired'), 'warning');
                accessEl?.focus();
                return;
            }
            const expiresAt = (expiresEl?.value || '').trim();
            closeModal({ token, expiresAt });
        };

        modal.querySelector('#tokenPoolUpdateOk')?.addEventListener('click', handleSubmit);
        modal.querySelector('#tokenPoolUpdateCancel')?.addEventListener('click', () => closeModal(null));
        modal.addEventListener('click', (event) => {
            if (event.target === modal) {
                closeModal(null);
            }
        });

        modal.addEventListener('keydown', (event) => {
            if (event.key === 'Escape') {
                closeModal(null);
            }
        });
    });
}

function formatTokenPoolTime(value) {
    if (!value) {
        return '-';
    }
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
        return value;
    }
    const pad = (num) => String(num).padStart(2, '0');
    const year = date.getFullYear();
    const month = pad(date.getMonth() + 1);
    const day = pad(date.getDate());
    const hours = pad(date.getHours());
    const minutes = pad(date.getMinutes());
    const seconds = pad(date.getSeconds());
    return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`;
}

function renderTokenPoolStatus(status, enabled = true, rateLimits = null) {
    if (!enabled) {
        return `<span style="display: inline-block; padding: 2px 8px; border-radius: 999px; background: #6b7280; color: #fff; font-size: 12px;">${t('tokenPool.statusLabels.disabled')}</span>`;
    }
    const rateStatus = (rateLimits?.status || '').trim();
    const normalized = rateStatus && rateStatus !== 'ok' ? rateStatus : (status || 'active');
    const colors = {
        active: '#10b981',
        expiring: '#f59e0b',
        need_refresh: '#f97316',
        expired: '#ef4444',
        invalid: '#ef4444',
        unauthorized: '#ef4444',
        blocked: '#ef4444',
        error: '#ef4444',
        network: '#f59e0b',
        upstream: '#f59e0b',
        parse_error: '#6366f1',
        empty: '#6366f1',
        missing_token: '#6b7280',
        invalid: '#ef4444',
        cooldown: '#6366f1'
    };
    const color = colors[normalized] || '#6b7280';
    return `<span style="display: inline-block; padding: 2px 8px; border-radius: 999px; background: ${color}; color: #fff; font-size: 12px;">${escapeHtml(tokenPoolStatusLabel(normalized))}</span>`;
}

function renderTokenPoolStats(stats = {}) {
    const items = [
        [t('tokenPool.statsTotal'), stats.total || 0, 'total'],
        [t('tokenPool.statsActive'), stats.active || 0, 'active'],
        [t('tokenPool.statsExpiring'), stats.expiring || 0, 'expiring'],
        [t('tokenPool.statsNeedRefresh'), stats.needRefresh || 0, 'needRefresh'],
        [t('tokenPool.statsExpired'), stats.expired || 0, 'expired'],
        [t('tokenPool.statsInvalid'), stats.invalid || 0, 'invalid'],
        [t('tokenPool.statsCooldown'), stats.cooldown || 0, 'cooldown'],
        [t('tokenPool.statsDisabled'), stats.disabled || 0, 'disabled']
    ];

    const parts = items.map(([label, value, key]) => {
        const highlight = value > 0 && key !== 'total' && key !== 'active' ? ' token-pool-stat-alert' : '';
        return `<span class="token-pool-stat${highlight}"><strong>${label}</strong> ${value}</span>`;
    });

    return `<div class="token-pool-stats-line">${parts.join('<span class="token-pool-stat-sep">·</span>')}</div>`;
}

function renderCodexAccountOverview(overview = {}) {
    const planCounts = overview.planCounts || {};
    const planSummary = Object.entries(planCounts)
        .filter(([, count]) => Number(count) > 0)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([plan, count]) => `${escapeHtml(plan)} x${Number(count) || 0}`)
        .join(' · ') || '-';
    const quotaSummary = [
        tt('tokenPool.codexAccountOverviewPrimary', { value: `${overview.highestPrimaryUsedPercent || 0}%` }),
        tt('tokenPool.codexAccountOverviewSecondary', { value: `${overview.highestSecondaryUsedPercent || 0}%` })
    ].join(' · ');
    const nextReset = overview.nextResetAt ? formatTokenPoolTime(overview.nextResetAt) : '-';
    const latestQuota = overview.latestQuotaUpdatedAt ? formatTokenPoolTime(overview.latestQuotaUpdatedAt) : '-';
    const problemClass = (overview.problemAccounts || 0) > 0 ? ' token-pool-overview-alert' : '';

    return `
        <div class="token-pool-overview-header">
            <strong>${t('tokenPool.codexAccountOverviewTitle')}</strong>
            <span>${tt('tokenPool.codexAccountOverviewUpdated', { time: latestQuota })}</span>
        </div>
        <div class="token-pool-overview-grid">
            <div class="token-pool-overview-card">
                <span>${t('tokenPool.codexAccountOverviewAccounts')}</span>
                <strong>${overview.totalAccounts || 0}</strong>
                <small>${tt('tokenPool.codexAccountOverviewAccountDetail', {
                    active: overview.activeAccounts || 0,
                    problem: overview.problemAccounts || 0
                })}</small>
            </div>
            <div class="token-pool-overview-card${problemClass}">
                <span>${t('tokenPool.codexAccountOverviewHealth')}</span>
                <strong>${overview.problemAccounts || 0}</strong>
                <small>${tt('tokenPool.codexAccountOverviewEnabledDetail', {
                    enabled: overview.enabledAccounts || 0,
                    disabled: overview.disabledAccounts || 0
                })}</small>
            </div>
            <div class="token-pool-overview-card">
                <span>${t('tokenPool.codexAccountOverviewQuota')}</span>
                <strong>${escapeHtml(quotaSummary)}</strong>
                <small>${tt('tokenPool.codexAccountOverviewReset', { time: nextReset })}</small>
            </div>
            <div class="token-pool-overview-card">
                <span>${t('tokenPool.codexAccountOverviewUsage')}</span>
                <strong>${formatTokens(overview.totalTokens || 0)}</strong>
                <small>${tt('tokenPool.codexAccountOverviewUsageDetail', {
                    requests: overview.requests || 0,
                    errors: overview.errors || 0
                })}</small>
            </div>
            <div class="token-pool-overview-card token-pool-overview-card-wide">
                <span>${t('tokenPool.codexAccountOverviewPlans')}</span>
                <strong>${planSummary}</strong>
                <small>${tt('tokenPool.codexAccountOverviewSnapshots', {
                    available: overview.quotaSnapshotAvailableCount || 0,
                    problem: overview.quotaSnapshotProblemCount || 0,
                    missing: overview.quotaSnapshotUnsupportedCount || 0
                })}</small>
            </div>
        </div>
    `;
}

async function loadCodexAccountOverview(index) {
    const modal = ensureTokenPoolModal();
    const overviewEl = modal.querySelector('#tokenPoolOverview');
    if (!overviewEl) {
        return;
    }
    if (getTokenPoolManagerMode(index) !== 'codex') {
        overviewEl.style.display = 'none';
        overviewEl.innerHTML = '';
        return;
    }

    overviewEl.style.display = 'block';
    overviewEl.innerHTML = `<div class="token-pool-overview-loading">${t('tokenPool.codexAccountOverviewLoading')}</div>`;
    try {
        const raw = await window.go.main.App.GetCodexAccountOverview(index);
        const result = parseAppJSON(raw);
        if (!result.success) {
            throw new Error(result.error || t('tokenPool.codexAccountOverviewFailed'));
        }
        overviewEl.innerHTML = renderCodexAccountOverview(result.data || {});
    } catch (error) {
        const message = error?.message || String(error);
        overviewEl.innerHTML = `<div class="token-pool-overview-error">${tt('tokenPool.codexAccountOverviewFailedWithError', { error: escapeHtml(message) })}</div>`;
    }
}

function renderTokenPoolRows(credentials = [], options = {}) {
    const mode = options.mode || (options.showCodexActions === false ? 'api' : 'codex');
    const showCodexActions = mode === 'codex';
    const columnCount = showCodexActions ? 7 : 6;
    if (!credentials.length) {
        return `<tr><td colspan="${columnCount}" style="padding: 16px; text-align: center; color: #6b7280;">${t('tokenPool.noCredentials')}</td></tr>`;
    }

    const latestUsed = credentials.reduce((max, cred) => {
        if (!cred.lastUsedAt) {
            return max;
        }
        const t = Date.parse(cred.lastUsedAt);
        if (Number.isNaN(t)) {
            return max;
        }
        return t > max ? t : max;
    }, 0);

    return credentials.map((cred) => `
        <tr class="${latestUsed && cred.lastUsedAt && Date.parse(cred.lastUsedAt) === latestUsed ? 'token-pool-row-active' : ''}" style="border-top: 1px solid rgba(148, 163, 184, 0.2);">
            <td class="token-pool-cell-account"><code title="${escapeHtml(cred.accountId || '')}">${escapeHtml(maskTokenPoolAccountID(cred.accountId))}</code></td>
            <td class="token-pool-cell-email"><span title="${escapeHtml(cred.email || '')}">${escapeHtml(maskTokenPoolEmail(cred.email))}</span></td>
            <td class="token-pool-cell-status">
                <div class="token-pool-status-cell">
                    ${renderTokenPoolStatus(cred.status, cred.enabled, cred.rateLimits)}
                </div>
            </td>
            <td class="token-pool-cell-expires" title="${escapeHtml(formatTokenPoolTime(cred.expiresAt))}">${escapeHtml(formatTokenPoolTime(cred.expiresAt))}</td>
            ${showCodexActions ? `<td class="token-pool-cell-rate token-pool-rate-header">${renderTokenPoolRateLimits(cred.rateLimits)}</td>` : ''}
            <td class="token-pool-cell-error">
                ${tokenPoolErrorCache.has(String(cred.id))
                    ? `<button type="button" class="btn btn-secondary token-pool-error-view" data-error-id="${cred.id}" style="padding: 4px 8px; font-size: 12px;">${t('tokenPool.view')}</button>`
                    : '-'}
            </td>
            <td class="token-pool-cell-actions">
                <div class="token-pool-actions">
                    <button type="button" class="btn btn-secondary token-pool-toggle-action" data-id="${cred.id}" data-enabled="${cred.enabled ? '1' : '0'}" style="padding: 4px 8px; font-size: 12px;">${cred.enabled ? t('tokenPool.disable') : t('tokenPool.enable')}</button>
                    <div class="token-pool-more-wrap">
                        <button type="button" class="btn btn-secondary token-pool-more-toggle" data-id="${cred.id}" style="padding: 4px 8px; font-size: 12px;">${t('tokenPool.more')}</button>
                        <div class="token-pool-more-menu">
                            <button type="button" class="token-pool-activate" data-id="${cred.id}">${t('tokenPool.activate')}</button>
                            ${showCodexActions ? `<button type="button" class="token-pool-rate-refresh" data-id="${cred.id}">${t('tokenPool.refreshLimitsAction')}</button>` : ''}
                            ${showCodexActions ? `<button type="button" class="token-pool-reset-credit" data-id="${cred.id}">${t('tokenPool.resetCreditAction')}</button>` : ''}
                            ${showCodexActions ? `<button type="button" class="token-pool-refresh-token" data-id="${cred.id}">${t('tokenPool.refreshToken')}</button>` : ''}
                            <button type="button" class="token-pool-usage" data-id="${cred.id}">${t('tokenPool.usage')}</button>
                            <button type="button" class="token-pool-update" data-id="${cred.id}">${t('tokenPool.updateToken')}</button>
                            <button type="button" class="token-pool-delete" data-id="${cred.id}">${t('tokenPool.delete')}</button>
                        </div>
                    </div>
                </div>
            </td>
        </tr>
    `).join('');
}

function formatTokenPoolWindowLabel(windowMinutes) {
    if (!windowMinutes || windowMinutes <= 0) {
        return '';
    }
    if (windowMinutes % (60 * 24) === 0) {
        return `${windowMinutes / (60 * 24)}d`;
    }
    if (windowMinutes % 60 === 0) {
        return `${windowMinutes / 60}h`;
    }
    return `${windowMinutes}m`;
}

function renderTokenPoolRateLimits(rateLimits) {
    if (!rateLimits) {
        return '-';
    }
    const status = (rateLimits.status || '').trim();
    const updatedAt = rateLimits.updatedAt ? formatTokenPoolTime(rateLimits.updatedAt) : '-';
    const data = rateLimits.data || {};
    const snapshot = data.snapshot || {};
    const primary = snapshot.primary || {};
    const secondary = snapshot.secondary || {};
    const usedPercent = typeof primary.usedPercent === 'number' ? Math.round(primary.usedPercent) : null;
    const primaryWindowMinutes = primary.windowMinutes;
    const secondaryUsedPercent = typeof secondary.usedPercent === 'number' ? Math.round(secondary.usedPercent) : null;
    const secondaryWindowMinutes = secondary.windowMinutes;
    const primaryLabel = formatTokenPoolWindowLabel(primaryWindowMinutes);
    const secondaryLabel = formatTokenPoolWindowLabel(secondaryWindowMinutes);
    const parts = [];
    if (primaryLabel || usedPercent !== null) {
        const pct = usedPercent !== null ? `${usedPercent}%` : '';
        parts.push(`${pct}@${primaryLabel || t('tokenPool.window')}`.replace(/^@/, '').trim());
    }
    if (secondaryLabel || secondaryUsedPercent !== null) {
        const pct = secondaryUsedPercent !== null ? `${secondaryUsedPercent}%` : '';
        parts.push(`${pct}@${secondaryLabel || t('tokenPool.shortWindow')}`.replace(/^@/, '').trim());
    }
    const summary = parts.join(' · ');
    const credits = snapshot.credits || {};
    const creditText = credits.unlimited
        ? t('tokenPool.unlimitedCredits')
        : credits.balance
            ? tt('tokenPool.creditBalance', { balance: credits.balance })
            : credits.hasCredits
                ? t('tokenPool.hasCredits')
                : '';
    const primaryReset = primary.resetsAt ? formatTokenPoolTime(new Date(primary.resetsAt * 1000).toISOString()) : '';
    const secondaryReset = secondary.resetsAt ? formatTokenPoolTime(new Date(secondary.resetsAt * 1000).toISOString()) : '';
    const metaParts = [];
    if (creditText) {
        metaParts.push(creditText);
    }
    if (!summary && status === 'ok') {
        metaParts.push(t('tokenPool.statusLabels.ok'));
    }

    const detailLines = [];
    if (summary) {
        detailLines.push(summary);
    }
    if (primaryReset) {
        detailLines.push(tt('tokenPool.resetAt', {
            label: primaryLabel || t('tokenPool.primaryWindow'),
            time: primaryReset
        }).trim());
    }
    if (secondaryReset) {
        detailLines.push(tt('tokenPool.resetAt', {
            label: secondaryLabel || t('tokenPool.secondaryWindow'),
            time: secondaryReset
        }).trim());
    }
    if (creditText) {
        detailLines.push(tt('tokenPool.creditsDetail', { credits: creditText }));
    }
    if (updatedAt) {
        detailLines.push(tt('tokenPool.updatedAt', { time: updatedAt }));
    }
    const detailTitle = detailLines.join(' · ');

    const errorText = (rateLimits.error || '').trim();
    if (status && status !== 'ok') {
        return '-';
    }

    if (!summary && !metaParts.length) {
        return '-';
    }

    const mainLine = escapeHtml(summary || metaParts.shift() || '');
    const metaLine = metaParts.length ? escapeHtml(metaParts.join(' · ')) : '';
    return `
        <div class="token-pool-rate-cell" title="${escapeHtml(detailTitle)}">
            <div class="token-pool-rate-main">${mainLine}</div>
            ${metaLine ? `<div class="token-pool-rate-meta">${metaLine}</div>` : ''}
        </div>
    `;
}

function closeAllTokenPoolActionMenus() {
    if (!tokenPoolOpenActionMenu) {
        return;
    }

    const { menu, wrap } = tokenPoolOpenActionMenu;
    menu.classList.remove('show');
    menu.classList.remove('token-pool-more-menu-portal');
    menu.style.left = '';
    menu.style.top = '';
    if (wrap.isConnected) {
        wrap.appendChild(menu);
    } else {
        menu.remove();
    }
    tokenPoolOpenActionMenu = null;
}

function openTokenPoolActionMenu(button, menu, wrap) {
    closeAllTokenPoolActionMenus();
    document.body.appendChild(menu);
    menu.classList.add('show', 'token-pool-more-menu-portal');

    const triggerRect = button.getBoundingClientRect();
    const menuRect = menu.getBoundingClientRect();
    const viewportMargin = 8;
    const left = Math.max(
        viewportMargin,
        Math.min(
            Math.max(viewportMargin, triggerRect.right - menuRect.width),
            window.innerWidth - menuRect.width - viewportMargin
        )
    );
    let top = triggerRect.bottom + 4;
    if (top + menuRect.height > window.innerHeight - viewportMargin) {
        top = triggerRect.top - menuRect.height - 4;
    }

    menu.style.left = `${left}px`;
    menu.style.top = `${Math.max(viewportMargin, top)}px`;
    tokenPoolOpenActionMenu = { menu, wrap };
}

function bindTokenPoolMoreToggle(button) {
    const wrap = button.closest('.token-pool-more-wrap');
    const menu = wrap?.querySelector('.token-pool-more-menu');
    button.addEventListener('click', (event) => {
        event.preventDefault();
        event.stopPropagation();

        if (!menu || !wrap) {
            return;
        }

        if (tokenPoolOpenActionMenu?.menu === menu) {
            closeAllTokenPoolActionMenus();
        } else {
            openTokenPoolActionMenu(button, menu, wrap);
        }
    });
}

document.addEventListener('click', closeAllTokenPoolActionMenus);
window.addEventListener('scroll', closeAllTokenPoolActionMenus, true);
window.addEventListener('resize', closeAllTokenPoolActionMenus);

function setTokenPoolHint(modal, text) {
    const hintEl = modal.querySelector('#tokenPoolHint');
    if (!hintEl) {
        return;
    }
    const message = (text || '').trim();
    hintEl.textContent = message;
    hintEl.style.display = message ? 'block' : 'none';
}

async function loadTokenPoolData(index) {
    if (index < 0) {
        return;
    }

    closeAllTokenPoolActionMenus();
    const modal = ensureTokenPoolModal();
    setTokenPoolHint(modal, t('tokenPool.loading'));
    const raw = await window.go.main.App.GetEndpointCredentials(index);
    const parsed = parseAppJSON(raw);
    if (!parsed.success) {
        const error = parsed.error || t('tokenPool.unknownError');
        setTokenPoolHint(modal, tt('tokenPool.loadFailed', { error }));
        throw new Error(parsed.error || t('tokenPool.failedToLoadFallback'));
    }

    const payload = parsed.data || {};
    const credentials = payload.credentials || [];
    const stats = payload.stats || {};

    tokenPoolErrorCache = new Map();
    tokenPoolUsageCache = new Map();
    credentials.forEach((cred) => {
        const primaryError = (cred.lastError || '').trim();
        const rateErr = (cred.rateLimits && cred.rateLimits.status && cred.rateLimits.status !== 'ok')
            ? (cred.rateLimits.error || '').trim()
            : '';
        const displayError = primaryError || rateErr;
        if (displayError) {
            tokenPoolErrorCache.set(String(cred.id), displayError);
        }
        if (cred.usage) {
            tokenPoolUsageCache.set(String(cred.id), cred.usage);
        }
    });

    const statsEl = modal.querySelector('#tokenPoolStats');
    const bodyEl = modal.querySelector('#tokenPoolTableBody');
    const mode = getTokenPoolManagerMode(index);
    statsEl.innerHTML = renderTokenPoolStats(stats);
    bodyEl.innerHTML = renderTokenPoolRows(credentials, { mode });
    await loadCodexAccountOverview(index);
    await refreshEndpointPoolHomeSummaries();
    setTokenPoolHint(modal, '');

    bodyEl.querySelectorAll('.token-pool-toggle-action').forEach((button) => {
        button.addEventListener('click', async () => {
            const credentialID = Number(button.dataset.id);
            const currentlyEnabled = button.dataset.enabled === '1';
            const targetEnabled = !currentlyEnabled;
            try {
                await window.go.main.App.SetEndpointCredentialEnabled(tokenPoolCurrentIndex, credentialID, targetEnabled);
                showNotification(targetEnabled ? t('tokenPool.credentialEnabled') : t('tokenPool.credentialDisabled'), 'success');
                await loadTokenPoolData(tokenPoolCurrentIndex);
                if (window.loadConfig) {
                    window.loadConfig();
                }
            } catch (error) {
                const message = error?.message || String(error);
                showNotification(tt('tokenPool.failed', { error: message }), 'error');
                await loadTokenPoolData(tokenPoolCurrentIndex);
            }
            closeAllTokenPoolActionMenus();
        });
    });

    bodyEl.querySelectorAll('.token-pool-activate').forEach((button) => {
        button.addEventListener('click', async () => {
            try {
                await window.go.main.App.ActivateEndpointCredential(tokenPoolCurrentIndex, Number(button.dataset.id));
                showNotification(t('tokenPool.credentialActivated'), 'success');
                await loadTokenPoolData(tokenPoolCurrentIndex);
                if (window.loadConfig) {
                    window.loadConfig();
                }
            } catch (error) {
                const message = error?.message || String(error);
                showNotification(tt('tokenPool.failed', { error: message }), 'error');
            }
        });
    });

    bodyEl.querySelectorAll('.token-pool-more-toggle').forEach((button) => {
        bindTokenPoolMoreToggle(button);
    });

    bodyEl.querySelectorAll('.token-pool-more-menu').forEach((menu) => {
        menu.addEventListener('click', (event) => {
            event.stopPropagation();
            closeAllTokenPoolActionMenus();
        });
    });

    bodyEl.querySelectorAll('.token-pool-error-view').forEach((button) => {
        button.addEventListener('click', (event) => {
            event.preventDefault();
            event.stopPropagation();
            const errorId = button.dataset.errorId;
            const errorText = errorId ? tokenPoolErrorCache.get(String(errorId)) || '' : '';
            showTokenPoolErrorDialog(errorText);
        });
    });

    bodyEl.querySelectorAll('.token-pool-rate-error-view').forEach((button) => {
        button.addEventListener('click', (event) => {
            event.preventDefault();
            event.stopPropagation();
            showTokenPoolErrorDialog(button.dataset.error || '');
        });
    });

    bodyEl.querySelectorAll('.token-pool-usage').forEach((button) => {
        button.addEventListener('click', (event) => {
            event.preventDefault();
            event.stopPropagation();
            const credentialID = String(button.dataset.id || '');
            const row = tokenPoolOpenActionMenu?.wrap.closest('tr') || button.closest('tr');
            const accountText = row?.querySelector('td code')?.textContent?.trim() || '';
            const label = accountText || '';
            const usage = tokenPoolUsageCache.get(credentialID) || null;
            showTokenPoolUsageDialog(label, usage);
            closeAllTokenPoolActionMenus();
        });
    });

    bodyEl.querySelectorAll('.token-pool-update').forEach((button) => {
        button.addEventListener('click', async () => {
            const result = await showTokenPoolUpdateTokenDialog();
            if (!result) {
                return;
            }
            try {
                await window.go.main.App.UpdateEndpointCredentialToken(
                    tokenPoolCurrentIndex,
                    Number(button.dataset.id),
                    result.token,
                    result.expiresAt
                );
                showNotification(t('tokenPool.credentialUpdated'), 'success');
                await loadTokenPoolData(tokenPoolCurrentIndex);
                if (window.loadConfig) {
                    window.loadConfig();
                }
            } catch (error) {
                const message = error?.message || String(error);
                showNotification(tt('tokenPool.failed', { error: message }), 'error');
            }
            closeAllTokenPoolActionMenus();
        });
    });

    bodyEl.querySelectorAll('.token-pool-rate-refresh').forEach((button) => {
        button.addEventListener('click', async () => {
            const credentialID = Number(button.dataset.id);
            if (!Number.isFinite(credentialID) || credentialID <= 0) {
                showNotification(t('tokenPool.invalidCredentialId'), 'error');
                return;
            }
            const row = tokenPoolOpenActionMenu?.wrap.closest('tr') || button.closest('tr');
            const accountText = row?.querySelector('td code')?.textContent?.trim() || '';
            const label = accountText ? `${accountText} (#${credentialID})` : `#${credentialID}`;
            const modal = ensureTokenPoolModal();
            try {
                setTokenPoolHint(modal, tt('tokenPool.refreshingRateLimitsFor', { label }));
                const raw = await window.go.main.App.FetchCodexRateLimitsForCredential(tokenPoolCurrentIndex, credentialID);
                const result = parseAppJSON(raw);
                if (!result.success) {
                    throw new Error(result.error || t('tokenPool.rateLimitsRefreshFailedFallback'));
                }
                const data = result.data || {};
                const detail = tokenPoolSummaryDetail(data);
                await loadTokenPoolData(tokenPoolCurrentIndex);
                const refreshedRow = modal.querySelector(`.token-pool-rate-refresh[data-id="${credentialID}"]`)?.closest('tr');
                const rateMain = refreshedRow?.querySelector('.token-pool-rate-main')?.textContent?.trim();
                const rateStatus = refreshedRow?.querySelector('.token-pool-rate-status')?.textContent?.trim();
                const rateSummary = rateMain || rateStatus || '-';
                const message = tt('tokenPool.rateLimitsRefreshedFor', { label, summary: rateSummary, detail });
                showNotification(message, 'success');
                setTokenPoolHint(modal, message);
            } catch (error) {
                const message = error?.message || String(error);
                const localized = tt('tokenPool.rateLimitsRefreshFailed', { error: message });
                showNotification(localized, 'error');
                setTokenPoolHint(modal, localized);
            } finally {
                closeAllTokenPoolActionMenus();
            }
        });
    });

    bodyEl.querySelectorAll('.token-pool-reset-credit').forEach((button) => {
        button.addEventListener('click', async () => {
            const credentialID = Number(button.dataset.id);
            if (!Number.isFinite(credentialID) || credentialID <= 0) {
                showNotification(t('tokenPool.invalidCredentialId'), 'error');
                return;
            }
            const row = tokenPoolOpenActionMenu?.wrap.closest('tr') || button.closest('tr');
            const accountText = row?.querySelector('td code')?.textContent?.trim() || '';
            const label = accountText ? `${accountText} (#${credentialID})` : `#${credentialID}`;
            const modal = ensureTokenPoolModal();
            try {
                setTokenPoolHint(modal, tt('tokenPool.resetCreditLoading', { label }));
                const raw = await window.go.main.App.GetCodexResetCredits(tokenPoolCurrentIndex, credentialID);
                const result = parseAppJSON(raw);
                if (!result.success) {
                    throw new Error(result.error || t('tokenPool.resetCreditLoadFailedFallback'));
                }
                const confirmed = await showCodexResetCreditDialog(label, result.data || {});
                if (!confirmed) {
                    setTokenPoolHint(modal, '');
                    return;
                }
                await confirmCodexResetCreditConsume(credentialID, label);
            } catch (error) {
                const message = error?.message || String(error);
                const localized = tt('tokenPool.resetCreditFailed', { error: message });
                showNotification(localized, 'error');
                setTokenPoolHint(modal, localized);
            } finally {
                closeAllTokenPoolActionMenus();
            }
        });
    });

    bodyEl.querySelectorAll('.token-pool-refresh-token').forEach((button) => {
        button.addEventListener('click', async () => {
            const credentialID = Number(button.dataset.id);
            if (!Number.isFinite(credentialID) || credentialID <= 0) {
                showNotification(t('tokenPool.invalidCredentialId'), 'error');
                return;
            }
            const row = tokenPoolOpenActionMenu?.wrap.closest('tr') || button.closest('tr');
            const accountText = row?.querySelector('td code')?.textContent?.trim() || '';
            const label = accountText ? `${accountText} (#${credentialID})` : `#${credentialID}`;
            const modal = ensureTokenPoolModal();
            try {
                setTokenPoolHint(modal, tt('tokenPool.refreshingTokenFor', { label }));
                const raw = await window.go.main.App.RefreshEndpointCredential(tokenPoolCurrentIndex, credentialID);
                const result = parseAppJSON(raw);
                if (!result.success) {
                    throw new Error(result.error || t('tokenPool.tokenRefreshFailedFallback'));
                }
                showNotification(tt('tokenPool.tokenRefreshedFor', { label }), 'success');
                await loadTokenPoolData(tokenPoolCurrentIndex);
                setTokenPoolHint(modal, tt('tokenPool.tokenRefreshedFor', { label }));
            } catch (error) {
                const message = error?.message || String(error);
                const localized = tt('tokenPool.tokenRefreshFailed', { error: message });
                showNotification(localized, 'error');
                setTokenPoolHint(modal, localized);
                await loadTokenPoolData(tokenPoolCurrentIndex);
            } finally {
                closeAllTokenPoolActionMenus();
            }
        });
    });

    bodyEl.querySelectorAll('.token-pool-delete').forEach((button) => {
        button.addEventListener('click', async () => {
            try {
                const credentialID = Number(button.dataset.id);
                if (!Number.isFinite(credentialID) || credentialID <= 0) {
                    throw new Error(`invalid credential id: ${button.dataset.id}`);
                }

                console.info('[TokenPool] delete clicked', {
                    endpointIndex: tokenPoolCurrentIndex,
                    credentialID
                });

                showNotification(tt('tokenPool.deletingCredential', { id: credentialID }), 'info');
                await window.go.main.App.DeleteEndpointCredential(tokenPoolCurrentIndex, credentialID);
                showNotification(t('tokenPool.credentialDeleted'), 'success');
                await loadTokenPoolData(tokenPoolCurrentIndex);
                if (window.loadConfig) {
                    window.loadConfig();
                }
            } catch (error) {
                const message = error?.message || String(error);
                console.error('[TokenPool] delete failed', {
                    endpointIndex: tokenPoolCurrentIndex,
                    credentialID: button.dataset.id,
                    error
                });
                showNotification(tt('tokenPool.failed', { error: message }), 'error');
            }
            closeAllTokenPoolActionMenus();
        });
    });
}

async function refreshTokenPoolRateLimits(index) {
    if (index < 0) {
        return;
    }

    const modal = ensureTokenPoolModal();
    const button = modal.querySelector('#tokenPoolRateRefreshBtn');
    try {
        if (button) {
            button.disabled = true;
            button.textContent = t('tokenPool.refreshing');
        }
        setTokenPoolHint(modal, t('tokenPool.fetchingRateLimits'));

        const raw = await window.go.main.App.FetchCodexRateLimits(index);
        const result = parseAppJSON(raw);
        if (!result.success) {
            throw new Error(result.error || t('tokenPool.rateLimitsFetchFailedFallback'));
        }

        const data = result.data || {};
        showNotification(tt('tokenPool.rateLimitsRefreshed', {
            updated: data.updated || 0,
            failed: data.failed || 0,
            skipped: data.skipped || 0
        }), 'success');
        await loadTokenPoolData(index);
    } catch (error) {
        const message = error?.message || String(error);
        const localized = tt('tokenPool.rateLimitsRefreshFailed', { error: message });
        showNotification(localized, 'error');
        setTokenPoolHint(modal, localized);
    } finally {
        if (button) {
            button.disabled = false;
            button.textContent = t('tokenPool.refreshLimits');
        }
    }
}

async function handleTokenPoolImport() {
    if (tokenPoolCurrentIndex < 0) {
        return;
    }

    const modal = ensureTokenPoolModal();
    const input = modal.querySelector('#tokenPoolImportInput');
    const overwrite = modal.querySelector('#tokenPoolOverwrite')?.checked === true;
    const raw = (input?.value || '').trim();

    if (!raw) {
        showNotification(t('tokenPool.pasteJsonFirst'), 'warning');
        return;
    }

    try {
        const isClaudeOAuth = isClaudeOAuthTokenPoolEndpoint(tokenPoolCurrentIndex);
        let resultRaw = '';
        try {
            JSON.parse(raw);
            resultRaw = await window.go.main.App.ImportEndpointCredentials(tokenPoolCurrentIndex, raw, overwrite);
        } catch (parseError) {
            if (!isClaudeOAuth) {
                showNotification(t('tokenPool.invalidJson'), 'error');
                return;
            }
            resultRaw = await window.go.main.App.ImportClaudeOAuthCredential(tokenPoolCurrentIndex, raw, false, '', overwrite);
        }
        const result = parseAppJSON(resultRaw);
        if (!result.success) {
            throw new Error(result.error || t('tokenPool.importFailedFallback'));
        }

        const data = result.data || {};
        showNotification(
            tt('tokenPool.importSummary', {
                created: data.created || 0,
                updated: data.updated || 0,
                skipped: data.skipped || 0,
                failed: data.failed || 0
            }),
            'success'
        );
        input.value = '';
        await loadTokenPoolData(tokenPoolCurrentIndex);
        if (window.loadConfig) {
            window.loadConfig();
        }
    } catch (error) {
        const message = error?.message || String(error);
        showNotification(tt('tokenPool.importFailed', { error: message }), 'error');
    }
}

async function handleClaudeOAuthDiscover() {
    if (!isClaudeOAuthTokenPoolEndpoint(tokenPoolCurrentIndex)) {
        showNotification(t('tokenPool.claudeOnly'), 'warning');
        return;
    }
    const modal = ensureTokenPoolModal();
    const overwrite = modal.querySelector('#tokenPoolOverwrite')?.checked === true;
    try {
        setTokenPoolHint(modal, t('tokenPool.discoveringClaude'));
        const raw = await window.go.main.App.DiscoverClaudeOAuthCredentials(tokenPoolCurrentIndex);
        const result = parseAppJSON(raw);
        if (!result.success) {
            throw new Error(result.error || t('tokenPool.discoveryFailedFallback'));
        }
        const credentials = result.data?.credentials || [];
        if (!credentials.length) {
            setTokenPoolHint(modal, t('tokenPool.noClaudeCredentialsFound'));
            showNotification(t('tokenPool.noClaudeCredentialsFound'), 'warning');
            return;
        }
        const choice = chooseClaudeOAuthPreview(credentials);
        if (!choice) {
            setTokenPoolHint(modal, '');
            return;
        }
        const importedRaw = await window.go.main.App.ImportClaudeOAuthCredential(tokenPoolCurrentIndex, choice.id, true, '', overwrite);
        const imported = parseAppJSON(importedRaw);
        if (!imported.success) {
            throw new Error(imported.error || t('tokenPool.importFailedFallback'));
        }
        const data = imported.data || {};
        const message = tt('tokenPool.importSummary', {
            created: data.created || 0,
            updated: data.updated || 0,
            skipped: data.skipped || 0,
            failed: data.failed || 0
        });
        showNotification(message, 'success');
        setTokenPoolHint(modal, message);
        await loadTokenPoolData(tokenPoolCurrentIndex);
    } catch (error) {
        const message = error?.message || String(error);
        const localized = tt('tokenPool.discoveryFailed', { error: message });
        showNotification(localized, 'error');
        setTokenPoolHint(modal, localized);
    }
}

function chooseClaudeOAuthPreview(credentials) {
    if (credentials.length === 1) {
        const item = credentials[0];
        if (window.confirm(tt('tokenPool.importClaudeDiscoveredConfirm', {
            label: item.label || item.source || item.id,
            token: item.maskedToken || ''
        }))) {
            return item;
        }
        return null;
    }
    const options = credentials.map((item, index) =>
        `${index + 1}. ${(item.label || item.source || item.id)} ${item.maskedToken || ''}`
    ).join('\n');
    const answer = window.prompt(`${t('tokenPool.importClaudeDiscoveredPrompt')}\n${options}`, '1');
    const selected = Number(answer);
    if (!Number.isInteger(selected) || selected < 1 || selected > credentials.length) {
        return null;
    }
    return credentials[selected - 1];
}

async function handleTokenPoolFileImport() {
    if (tokenPoolCurrentIndex < 0) {
        return;
    }

    const modal = ensureTokenPoolModal();
    const overwrite = modal.querySelector('#tokenPoolOverwrite')?.checked === true;

    try {
        setTokenPoolHint(modal, t('tokenPool.openingFilePicker'));
        const resultRaw = await window.go.main.App.ImportEndpointCredentialsFromFiles(tokenPoolCurrentIndex, overwrite);
        const result = parseAppJSON(resultRaw);
        if (!result.success) {
            throw new Error(result.error || t('tokenPool.importFailedFallback'));
        }

        const data = result.data || {};
        if ((data.processed || 0) === 0 && (data.failed || 0) === 0) {
            showNotification(t('tokenPool.noFilesSelected'), 'warning');
            setTokenPoolHint(modal, t('tokenPool.noFilesSelected'));
            return;
        }

        showNotification(
            tt('tokenPool.importFilesSummary', {
                created: data.created || 0,
                updated: data.updated || 0,
                skipped: data.skipped || 0,
                failed: data.failed || 0
            }),
            (data.failed || 0) > 0 ? 'warning' : 'success'
        );
        await loadTokenPoolData(tokenPoolCurrentIndex);
        if (window.loadConfig) {
            window.loadConfig();
        }
    } catch (error) {
        const message = error?.message || String(error);
        if (message.toLowerCase().includes('no files selected')) {
            showNotification(t('tokenPool.noFilesSelected'), 'warning');
            setTokenPoolHint(modal, t('tokenPool.noFilesSelected'));
            return;
        }
        const localized = tt('tokenPool.importFilesFailed', { error: message });
        showNotification(localized, 'error');
        setTokenPoolHint(modal, localized);
    }
}

export async function openTokenPoolModal(index, endpointName = '') {
    const config = await refreshTokenPoolEndpointConfig();
    const endpoints = config.endpoints || [];
    const namedIndex = findEndpointIndexByName(endpointName);
    const resolvedIndex = namedIndex >= 0 ? namedIndex : index;
    if (resolvedIndex < 0 || resolvedIndex >= endpoints.length) {
        showNotification(tt('tokenPool.failed', { error: `Invalid endpoint index ${resolvedIndex}` }), 'error');
        return;
    }

    tokenPoolCurrentIndex = resolvedIndex;
    const resolvedEndpointName = endpointName || endpoints[resolvedIndex]?.name || '';
    const mode = getTokenPoolManagerMode(resolvedIndex);
    const meta = getTokenPoolManagerMeta(mode);
    const modal = ensureTokenPoolModal();
    modal.dataset.tokenPoolMode = mode;
    const title = modal.querySelector('#tokenPoolTitle');
    const modeBadge = modal.querySelector('#tokenPoolModeBadge');
    const endpointNameEl = modal.querySelector('#tokenPoolEndpointName');
    const modeDescription = modal.querySelector('#tokenPoolModeDescription');
    const importLabel = modal.querySelector('#tokenPoolImportLabel');
    const authBtn = modal.querySelector('#tokenPoolAuthBtn');
    const claudeDiscoverBtn = modal.querySelector('#tokenPoolClaudeDiscoverBtn');
    const rateRefreshBtn = modal.querySelector('#tokenPoolRateRefreshBtn');
    const importInput = modal.querySelector('#tokenPoolImportInput');
    const rateHeaders = modal.querySelectorAll('.token-pool-rate-header');
    const isCodex = mode === 'codex';
    const isClaudeOAuth = mode === 'claude';
    title.textContent = `🪪 ${meta.title}`;
    if (modeBadge) {
        modeBadge.textContent = meta.badge;
        modeBadge.className = `token-pool-mode-badge token-pool-mode-${mode}`;
    }
    if (endpointNameEl) {
        endpointNameEl.textContent = resolvedEndpointName ? tt('tokenPool.endpointLabel', { name: resolvedEndpointName }) : '';
    }
    if (modeDescription) {
        modeDescription.textContent = meta.description;
    }
    if (importLabel) {
        importLabel.textContent = meta.importLabel;
    }
    if (authBtn) {
        authBtn.style.display = isCodex ? '' : 'none';
    }
    if (claudeDiscoverBtn) {
        claudeDiscoverBtn.style.display = isClaudeOAuth ? '' : 'none';
    }
    if (rateRefreshBtn) {
        rateRefreshBtn.style.display = isCodex ? '' : 'none';
    }
    if (importInput) {
        importInput.placeholder = meta.placeholder;
    }
    rateHeaders.forEach((header) => {
        header.style.display = isCodex ? '' : 'none';
    });
    modal.classList.add('active');
    await loadTokenPoolProxySetting();

    try {
        await loadTokenPoolData(resolvedIndex);
    } catch (error) {
        const message = error?.message || String(error);
        showNotification(tt('tokenPool.failedToLoad', { error: message }), 'error');
    }
}

// 克隆端点配置（创建副本）
function copyEndpointConfig(index, button) {
    const allEndpoints = window.config?.endpoints || [];
    
    if (index < 0 || index >= allEndpoints.length) {
        const errorMsg = `Invalid index ${index} for cloning endpoint. Total endpoints: ${allEndpoints.length} at ${new Date().toISOString()}`;
        console.error(errorMsg);
        if (typeof window.logError === 'function') {
            window.logError(errorMsg);
        }
        showNotification(t('endpoints.cloneFailed') + ': ' + (t('endpoints.invalidIndex') || `Invalid index ${index}`), 'error');
        return;
    }
    
    const endpoint = allEndpoints[index];
    
    if (endpoint) {
        const clonedEndpoint = { ...endpoint };
        
        const baseName = extractBaseName(endpoint.name);
        const copySuffix = '(Copy)';
        
        let newName = `${baseName}${copySuffix}`;
        let counter = 1;
        while (allEndpoints.some(ep => ep.name === newName)) {
            newName = `${baseName}${copySuffix} ${counter}`;
            counter++;
        }
        clonedEndpoint.name = newName;
        
        if (clonedEndpoint.authMode === "token_pool" || clonedEndpoint.authMode === "codex_token_pool" || clonedEndpoint.authMode === "claude_oauth_token_pool") {
            delete clonedEndpoint.apiKey;
        }
        
        const originalHTML = button.innerHTML;
        button.innerHTML = '<svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" width="1em" height="1em"><path d="M20 6L9 17l-5-5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
        setTimeout(() => { button.innerHTML = originalHTML; }, 1000);
        
        showNotification(t('endpoints.cloned') || 'Endpoint cloned successfully', 'success');
        
        window.clonedEndpointData = clonedEndpoint;
        
        if (typeof window.showAddEndpointModalWithPreset === 'function') {
            try {
                window.showAddEndpointModalWithPreset(clonedEndpoint);
            } catch (error) {
                const errorMsg = `Error calling showAddEndpointModalWithPreset at ${new Date().toISOString()}: ${error.message}\nStack: ${error.stack}`;
                console.error(errorMsg);
                try {
                    if (typeof window.logError === 'function') {
                        window.logError(errorMsg);
                    }
                } catch (logErr) {
                    console.error('Failed to call logError:', logErr);
                }
                showNotification(t('endpoints.cloneFailed') + ': ' + error.message || `Failed to clone endpoint: ${error.message}`, 'error');
            }
        } else {
            const errorMsg = `showAddEndpointModalWithPreset function is not available at ${new Date().toISOString()}`;
            console.error(errorMsg);
            if (typeof window.logError === 'function') {
                window.logError(errorMsg);
            }
            showNotification(t('endpoints.cloneFailed') + ': ' + t('endpoints.functionUnavailable') || 'Failed to clone endpoint: Function not available', 'error');
        }
    } else {
        const errorMsg = `Failed to clone endpoint: endpoint data not found at index ${index} at ${new Date().toISOString()}`;
        console.error(errorMsg);
        if (typeof window.logError === 'function') {
            window.logError(errorMsg);
        }
        showNotification(t('endpoints.cloneFailed') + ': ' + (t('endpoints.noEndpointAtIdxWithIndex') || `No endpoint found at index ${index}`), 'error');
    }
}

export function toggleEndpointPanel() {
    const panel = document.getElementById('endpointPanel');
    const icon = document.getElementById('endpointToggleIcon');
    const text = document.getElementById('endpointToggleText');

    endpointPanelExpanded = !endpointPanelExpanded;

    if (endpointPanelExpanded) {
        panel.style.display = 'block';
        icon.textContent = '🔼';
        text.textContent = t('endpoints.collapse');
    } else {
        panel.style.display = 'none';
        icon.textContent = '🔽';
        text.textContent = t('endpoints.expand');
    }
}

// Drag and drop state
let draggedElement = null;
let draggedOverElement = null;
let draggedOriginalName = null;
let autoScrollInterval = null;

// Auto scroll when dragging near edges
function autoScroll(e) {
    const scrollContainer = document.querySelector('.container');
    const scrollThreshold = 80;
    const scrollSpeed = 10;

    const rect = scrollContainer.getBoundingClientRect();
    const distanceFromTop = e.clientY - rect.top;
    const distanceFromBottom = rect.bottom - e.clientY;

    if (distanceFromTop < scrollThreshold) {
        scrollContainer.scrollTop -= scrollSpeed;
    } else if (distanceFromBottom < scrollThreshold) {
        scrollContainer.scrollTop += scrollSpeed;
    }
}

// Setup drag and drop for an endpoint item
function setupDragAndDrop(item, container) {
    item.addEventListener('dragstart', (e) => {
        draggedElement = item;
        draggedOriginalName = item.dataset.name;
        item.classList.add('dragging');
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/html', item.innerHTML);

        // Start auto-scroll interval
        autoScrollInterval = setInterval(() => {
            if (window.lastDragEvent) {
                autoScroll(window.lastDragEvent);
            }
        }, 50);
    });

    item.addEventListener('dragend', (e) => {
        item.classList.remove('dragging');
        const allItems = container.querySelectorAll('.endpoint-item');
        allItems.forEach(i => i.classList.remove('drag-over'));
        draggedElement = null;
        draggedOverElement = null;
        draggedOriginalName = null;

        // Clear auto-scroll
        if (autoScrollInterval) {
            clearInterval(autoScrollInterval);
            autoScrollInterval = null;
        }
        window.lastDragEvent = null;
    });

    item.addEventListener('dragover', (e) => {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        window.lastDragEvent = e; // Store for auto-scroll

        if (draggedElement && draggedElement !== item) {
            if (draggedOverElement && draggedOverElement !== item) {
                draggedOverElement.classList.remove('drag-over');
            }
            item.classList.add('drag-over');
            draggedOverElement = item;
        }
    });

    item.addEventListener('dragleave', (e) => {
        // Only remove if we're actually leaving the element
        if (!item.contains(e.relatedTarget)) {
            item.classList.remove('drag-over');
            if (draggedOverElement === item) {
                draggedOverElement = null;
            }
        }
    });

    item.addEventListener('drop', async (e) => {
        e.preventDefault();
        e.stopPropagation();

        if (draggedElement && draggedElement !== item) {
            // Use dataset.name to identify positions, not DOM order
            const draggedName = draggedElement.dataset.name;
            const targetName = item.dataset.name;

            // Get all items and build current order by name
            const allItems = Array.from(container.querySelectorAll('.endpoint-item'));
            const currentOrder = allItems.map(el => el.dataset.name);

            // Find positions by name (stable, not affected by scrolling)
            const fromIndex = currentOrder.indexOf(draggedName);
            const toIndex = currentOrder.indexOf(targetName);

            // Calculate new order
            const newOrder = [...currentOrder];
            newOrder.splice(fromIndex, 1);
            newOrder.splice(toIndex, 0, draggedName);

            // Compare arrays: if order hasn't changed, don't do anything
            const orderChanged = !currentOrder.every((name, idx) => name === newOrder[idx]);

            if (!orderChanged) {
                item.classList.remove('drag-over');
                return;
            }

            // Save to backend
            try {
                await window.go.main.App.ReorderEndpoints(newOrder);
                window.loadConfig();
            } catch (error) {
                console.error('Failed to reorder endpoints:', error);
                alert(t('endpoints.reorderFailed') + ': ' + error);
                window.loadConfig();
            }
        }

        item.classList.remove('drag-over');
    });
}

// 初始化端点成功事件监听
export function initEndpointSuccessListener() {
    if (window.runtime && window.runtime.EventsOn) {
        window.runtime.EventsOn('endpoint:success', (endpointName) => {
            saveEndpointTestStatus(endpointName, true);
        });

        window.runtime.EventsOn('endpoint:current', (event) => {
            currentEndpointName = event?.name || '';
            updateDefaultEndpointSlots();
        });

        window.runtime.EventsOn('endpoint:runtime', (event) => {
            const endpointName = event?.endpointName;
            if (!endpointName) {
                return;
            }
            endpointActiveCounts[endpointName] = Number(event.activeCount || 0);
            const hasFailureStatusCode = Object.prototype.hasOwnProperty.call(event, 'lastFailureStatusCode');
            const currentFailureStatusCode = Number(endpointRuntimeStatuses[endpointName]?.lastFailureStatusCode || 0);
            endpointRuntimeStatuses[endpointName] = {
                ...(endpointRuntimeStatuses[endpointName] || {}),
                endpointName,
                lastSuccessAt: event.lastSuccessAt || endpointRuntimeStatuses[endpointName]?.lastSuccessAt,
                lastFailureAt: event.lastFailureAt || endpointRuntimeStatuses[endpointName]?.lastFailureAt,
                lastFailureReason: event.lastFailureReason || endpointRuntimeStatuses[endpointName]?.lastFailureReason,
                lastFailureStatusCode: hasFailureStatusCode
                    ? Number(event.lastFailureStatusCode || 0)
                    : (event.event === 'failure' ? 0 : currentFailureStatusCode),
                lastAttemptAt: event.lastAttemptAt || endpointRuntimeStatuses[endpointName]?.lastAttemptAt
            };
            if (endpointActiveCounts[endpointName] <= 0) {
                delete endpointActiveCounts[endpointName];
            }
            updateRuntimeStatusSlot(endpointName);
        });
    }
}

// 清除所有端点测试状态
export function clearAllEndpointTestStatus() {
    try {
        localStorage.removeItem(ENDPOINT_TEST_STATUS_KEY);
    } catch (error) {
        console.error('Failed to clear endpoint test status:', error);
    }
}

// 启动时零消耗检测所有端点
export async function checkAllEndpointsOnStartup() {
    try {
        // 先清除所有状态
        clearAllEndpointTestStatus();

        const results = await testAllEndpointsZeroCost();
        for (const [name, status] of Object.entries(results)) {
            if (status === 'ok') {
                saveEndpointTestStatus(name, true);
            } else if (status === 'invalid_key') {
                saveEndpointTestStatus(name, false);
            }
            // 'unknown' 保持未设置状态，显示 ⚠️
        }
        // 刷新端点列表显示
        if (window.loadConfig) {
            window.loadConfig();
        }
    } catch (error) {
        console.error('Failed to check endpoints on startup:', error);
    }
}

// 渲染简洁视图
function renderCompactView(sortedEndpoints, container, currentEndpointName, isFiltered) {
    sortedEndpoints.forEach(({ endpoint: ep, originalIndex: index, stats }) => {
        const enabled = ep.enabled !== undefined ? ep.enabled : true;
        const transformer = ep.transformer || 'claude';
        const model = ep.model || '';
        const authMode = ep.authMode || 'api_key';

        // 获取测试状态
        const testStatus = getEndpointTestStatus(ep.name);
        let testStatusIcon = '⚠️';
        let testStatusTip = t('endpoints.testTipUnknown');
        if (testStatus === true) {
            testStatusIcon = '✅';
            testStatusTip = t('endpoints.testTipSuccess');
        } else if (testStatus === false) {
            testStatusIcon = '❌';
            testStatusTip = t('endpoints.testTipFailed');
        }

        const item = document.createElement('div');
        item.className = 'endpoint-item-compact';
        item.dataset.name = ep.name;
        item.dataset.index = index;

        // 筛选激活时禁用拖拽
        if (isFiltered) {
            item.draggable = false;
            item.style.cursor = 'default';
            item.title = t('endpoints.dragDisabledDuringFilter');
        } else {
            item.draggable = true;
            setupCompactDragAndDrop(item, container);
        }

        // 截断 URL 显示
        const displayUrl = ep.apiUrl.length > 40 ? ep.apiUrl.substring(0, 40) + '...' : ep.apiUrl;

        // 构建统计详情提示
        const totalTokens = stats.inputTokens + stats.outputTokens;
        let statsTooltip = `${t('endpoints.requests')}: ${stats.requests} | ${t('endpoints.errors')}: ${stats.errors}\n${t('statistics.in')}: ${formatTokens(stats.inputTokens)} | ${t('statistics.out')}: ${formatTokens(stats.outputTokens)}`;
        if (model) {
            statsTooltip += `\n${t('modal.model')}: ${model}`;
        }
        if (ep.remark) {
            statsTooltip += `\n${t('modal.remark')}: ${ep.remark}`;
        }

        item.innerHTML = `
            <div class="drag-handle" title="${t('endpoints.dragToReorder')}">
                <div class="drag-handle-dots"><span></span><span></span></div>
                <div class="drag-handle-dots"><span></span><span></span></div>
                <div class="drag-handle-dots"><span></span><span></span></div>
            </div>
            <span class="compact-status" title="${testStatusTip}" style="cursor: help">${testStatusIcon}</span>
            <span class="compact-name" title="${ep.name}">${ep.name}</span>
            <span class="endpoint-default-slot compact-default-slot" data-name="${escapeHtml(ep.name)}" data-enabled="${enabled ? 'true' : 'false'}" data-view="compact">${renderCompactDefaultEndpointControl(ep.name, enabled)}</span>
            <span class="endpoint-runtime-slot compact-runtime-slot" data-name="${escapeHtml(ep.name)}">${renderEndpointRuntimeBadges(ep.name, 'compact')}</span>
            <span class="compact-url" title="${ep.apiUrl}"><span class="compact-url-icon">🌐</span>${displayUrl}</span>
            <span class="compact-transformer">🔄 ${transformer}</span>
            <span class="endpoint-pool-home-slot compact-pool-home-slot" data-name="${escapeHtml(ep.name)}" data-auth-mode="${escapeHtml(authMode)}" data-view="compact">${renderCompactEndpointPoolHomeSummary(ep)}</span>
            <span class="compact-stats" title="${statsTooltip}">📊 ${stats.requests} | 🎯 ${formatTokens(stats.inputTokens + stats.outputTokens)}</span>
            <div class="compact-actions">
                <label class="toggle-switch">
                    <input type="checkbox" data-index="${index}" ${enabled ? 'checked' : ''}>
                    <span class="toggle-slider"></span>
                </label>
                <div class="compact-more-dropdown">
                    <button class="compact-btn" data-action="more" title="${t('endpoints.moreActions')}">⋯</button>
                    <div class="compact-more-menu">
                        <button data-action="test" data-index="${index}">🧪 ${t('endpoints.test')}</button>
                        <button data-action="edit" data-index="${index}">✏️ ${t('endpoints.edit')}</button>
                        <button data-action="copy" data-index="${index}">📋 ${t('endpoints.copy')}</button>
                        <button data-action="delete" data-index="${index}" class="danger">🗑️ ${t('endpoints.delete')}</button>
                    </div>
                </div>
            </div>
        `;

        // 绑定事件
        bindCompactItemEvents(item, index, enabled);

        container.appendChild(item);
    });

    ensureEndpointPoolHomeTimers();
    refreshEndpointPoolHomeSummaries();

    // 点击其他地方关闭下拉菜单（先移除旧监听器，避免重复绑定）
    document.removeEventListener('click', closeAllDropdowns);
    document.addEventListener('click', closeAllDropdowns);
}

// 绑定简洁视图项目事件
function bindCompactItemEvents(item, index, enabled) {
    const toggleSwitch = item.querySelector('input[type="checkbox"]');
    const switchBtn = item.querySelector('[data-action="switch"]');
    const moreBtn = item.querySelector('[data-action="more"]');
    const moreMenu = item.querySelector('.compact-more-menu');
    const testBtn = item.querySelector('[data-action="test"]');
    const editBtn = item.querySelector('[data-action="edit"]');
    const deleteBtn = item.querySelector('[data-action="delete"]');

    // 如果当前正在测试这个端点，显示加载状态
    if (currentTestIndex === index) {
        moreBtn.innerHTML = '⏳';
        moreBtn.disabled = true;
        currentTestButton = testBtn;
    }

    // 启用/禁用开关
    toggleSwitch.addEventListener('change', async (e) => {
        const idx = parseInt(e.target.getAttribute('data-index'));
        const newEnabled = e.target.checked;
        try {
            await toggleEndpoint(idx, newEnabled);
            window.loadConfig();
        } catch (error) {
            console.error('Failed to toggle endpoint:', error);
            alert('Failed to toggle endpoint: ' + error);
            e.target.checked = !newEnabled;
        }
    });

    // 切换按钮
    bindEndpointSwitchButton(switchBtn);

    // 更多操作按钮
    moreBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        const isOpen = moreMenu.classList.contains('show');
        closeAllDropdowns();
        if (!isOpen) {
            moreMenu.classList.add('show');
        }
    });

    // 测试按钮
    testBtn.addEventListener('click', () => {
        closeAllDropdowns();
        const idx = parseInt(testBtn.getAttribute('data-index'));
        window.testEndpoint(idx, testBtn);
    });

    // 复制按钮
    const copyBtn = item.querySelector('[data-action="copy"]');
    copyBtn.addEventListener('click', () => {
        closeAllDropdowns();
        const idx = parseInt(copyBtn.getAttribute('data-index'));
        copyEndpointConfig(idx, copyBtn);
});

    // 编辑按钮
    editBtn.addEventListener('click', () => {
        closeAllDropdowns();
        const idx = parseInt(editBtn.getAttribute('data-index'));
        window.editEndpoint(idx);
    });

    // 删除按钮
    deleteBtn.addEventListener('click', () => {
        closeAllDropdowns();
        const idx = parseInt(deleteBtn.getAttribute('data-index'));
        window.deleteEndpoint(idx);
    });
}

// 关闭所有下拉菜单
function closeAllDropdowns() {
    document.querySelectorAll('.compact-more-menu.show').forEach(menu => {
        menu.classList.remove('show');
    });
}

// 检查是否有下拉菜单正在显示
export function isDropdownOpen() {
    return document.querySelectorAll('.compact-more-menu.show').length > 0;
}

// 拖拽占位符元素
let dragPlaceholder = null;
let draggedItemHeight = 0;

// 创建占位符（指示线）
function createPlaceholder() {
    const placeholder = document.createElement('div');
    placeholder.className = 'drag-placeholder';
    return placeholder;
}

// 更新其他元素的位置
function updateItemPositions(container, draggedElement, placeholder) {
    const allItems = Array.from(container.querySelectorAll('.endpoint-item-compact'));
    const draggedIndex = allItems.indexOf(draggedElement);

    // 计算占位符在端点元素中的目标索引
    let targetIndex = 0;
    let currentNode = placeholder.previousSibling;
    while (currentNode) {
        if (currentNode.classList && currentNode.classList.contains('endpoint-item-compact')) {
            targetIndex++;
        }
        currentNode = currentNode.previousSibling;
    }

    allItems.forEach((item, index) => {
        let offset = 0;

        if (item === draggedElement) {
            // 被拖拽元素视觉上移动到占位符位置
            offset = (targetIndex - draggedIndex) * (draggedItemHeight + 8);
        } else if (draggedIndex < targetIndex) {
            // 向下拖拽：draggedIndex 和 targetIndex 之间的元素向上移
            if (index > draggedIndex && index < targetIndex) {
                offset = -(draggedItemHeight + 8);
            }
        } else if (draggedIndex > targetIndex) {
            // 向上拖拽：targetIndex 和 draggedIndex 之间的元素向下移
            if (index >= targetIndex && index < draggedIndex) {
                offset = draggedItemHeight + 8;
            }
        }

        item.style.transform = offset !== 0 ? `translateY(${offset}px)` : '';
    });
}

// 根据鼠标位置移动占位符
function movePlaceholderByMousePosition(e, container, draggedElement, dragPlaceholder) {
    if (!draggedElement || !dragPlaceholder) return;

    const allItems = Array.from(container.querySelectorAll('.endpoint-item-compact'));
    const mouseY = e.clientY;

    // 找到最接近鼠标位置的元素
    let closestItem = null;
    let closestDistance = Infinity;
    let insertBefore = true;

    allItems.forEach(item => {
        if (item === draggedElement) return;

        const rect = item.getBoundingClientRect();
        const itemMiddle = rect.top + rect.height / 2;
        const distance = Math.abs(mouseY - itemMiddle);

        if (distance < closestDistance) {
            closestDistance = distance;
            closestItem = item;
            insertBefore = mouseY < itemMiddle;
        }
    });

    // 移动占位符
    if (closestItem) {
        const targetPosition = insertBefore ? closestItem : closestItem.nextSibling;
        if (targetPosition !== dragPlaceholder && targetPosition !== dragPlaceholder.nextSibling) {
            container.insertBefore(dragPlaceholder, targetPosition);
            updateItemPositions(container, draggedElement, dragPlaceholder);
        }
    } else if (allItems.length === 1) {
        // 只有一个元素（被拖拽的元素）
        if (dragPlaceholder.parentNode !== container) {
            container.appendChild(dragPlaceholder);
        }
    }
}

// 简洁视图的拖拽设置
function setupCompactDragAndDrop(item, container) {
    item.addEventListener('dragstart', (e) => {
        draggedElement = item;
        draggedOriginalName = item.dataset.name;
        draggedItemHeight = item.offsetHeight;
        item.classList.add('dragging');
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/html', item.innerHTML);

        // 创建并插入占位符（指示线）
        dragPlaceholder = createPlaceholder();
        item.parentNode.insertBefore(dragPlaceholder, item.nextSibling);

        // 在容器上添加事件监听
        container.addEventListener('dragover', handleContainerDragOver);
        container.addEventListener('drop', handleContainerDrop);

        autoScrollInterval = setInterval(() => {
            if (window.lastDragEvent) {
                autoScroll(window.lastDragEvent);
            }
        }, 50);
    });

    item.addEventListener('dragend', () => {
        item.classList.remove('dragging');
        const allItems = container.querySelectorAll('.endpoint-item-compact');
        allItems.forEach(i => {
            i.classList.remove('drag-over');
            i.style.transform = '';
        });

        // 清理容器的 cursor 样式
        container.style.cursor = '';

        // 移除容器的事件监听
        container.removeEventListener('dragover', handleContainerDragOver);
        container.removeEventListener('drop', handleContainerDrop);

        // 移除占位符
        if (dragPlaceholder && dragPlaceholder.parentNode) {
            dragPlaceholder.parentNode.removeChild(dragPlaceholder);
            dragPlaceholder = null;
        }

        draggedElement = null;
        draggedOverElement = null;
        draggedOriginalName = null;
        draggedItemHeight = 0;

        if (autoScrollInterval) {
            clearInterval(autoScrollInterval);
            autoScrollInterval = null;
        }
        window.lastDragEvent = null;
    });

    // 在端点元素上禁止 drop（但允许事件冒泡到容器，让占位符能正常移动）
    item.addEventListener('dragover', (e) => {
        e.preventDefault();
        // 移除 stopPropagation()，让事件冒泡到容器
        e.dataTransfer.dropEffect = 'none';
    });
}

// 容器的 dragover 处理函数
function handleContainerDragOver(e) {
    e.preventDefault();
    window.lastDragEvent = e;

    const container = e.currentTarget;

    // 检查鼠标是否在端点元素上
    const isOverEndpointItem = e.target.closest('.endpoint-item-compact');

    if (isOverEndpointItem) {
        // 在端点元素上：显示禁止图标，但仍然移动占位符
        e.dataTransfer.dropEffect = 'none';
        container.style.cursor = 'no-drop';
    } else {
        // 在空白区域或占位符上：显示允许图标
        e.dataTransfer.dropEffect = 'move';
        container.style.cursor = 'grabbing';
    }

    // 始终更新占位符位置，让其他元素自动移开
    movePlaceholderByMousePosition(e, container, draggedElement, dragPlaceholder);
}

// 容器的 drop 处理函数
async function handleContainerDrop(e) {
    if (e.target.closest('.endpoint-item-compact')) {
        return;
    }
    e.preventDefault();
    e.stopPropagation();

    const container = e.currentTarget;
    if (draggedElement && dragPlaceholder) {
        const draggedName = draggedElement.dataset.name;
        const allItems = Array.from(container.querySelectorAll('.endpoint-item-compact'));
        const currentOrder = allItems.map(el => el.dataset.name);
        const allChildren = Array.from(container.children);
        const placeholderIndex = allChildren.indexOf(dragPlaceholder);

        let targetIndex = 0;
        for (let i = 0; i < placeholderIndex; i++) {
            if (allChildren[i].classList.contains('endpoint-item-compact')) {
                targetIndex++;
            }
        }

        const draggedIndex = currentOrder.indexOf(draggedName);
        if (draggedIndex < targetIndex) {
            targetIndex--;
        }

        const newOrder = [...currentOrder];
        newOrder.splice(draggedIndex, 1);
        newOrder.splice(targetIndex, 0, draggedName);

        const orderChanged = !currentOrder.every((name, idx) => name === newOrder[idx]);
        if (!orderChanged) return;

        try {
            await window.go.main.App.ReorderEndpoints(newOrder);
            window.loadConfig();
        } catch (error) {
            console.error('Failed to reorder endpoints:', error);
            alert(t('endpoints.reorderFailed') + ': ' + error);
            window.loadConfig();
        }
    }
}

// Incremental endpoint stats update - updates only the numbers in the endpoint card without re-rendering
export function updateEndpointStatsIncremental(endpointName, data) {
    // Find endpoint card by name (works for both detail and compact views)
    const endpointCard = document.querySelector(`[data-name="${endpointName}"]`);
    if (!endpointCard) {
        return; // Endpoint not found or filtered out
    }

    const totalTokens = (data.inputTokens || 0) + (data.outputTokens || 0);

    // Update stats in detail view
    const paragraphs = endpointCard.querySelectorAll('p');
    for (const p of paragraphs) {
        const text = p.textContent;

        // Update requests/errors line
        if (text.includes('📊') && text.includes(t('endpoints.requests'))) {
            p.innerHTML = `📊 ${t('endpoints.requests')}: ${data.requests} | ${t('endpoints.errors')}: ${data.errors}`;
        }

        // Update tokens line
        if (text.includes('🎯') && text.includes(t('endpoints.tokens'))) {
            p.innerHTML = `🎯 ${t('endpoints.tokens')}: ${formatTokens(totalTokens)} (${t('statistics.in')}: ${formatTokens(data.inputTokens)}, ${t('statistics.out')}: ${formatTokens(data.outputTokens)})`;
        }
    }

    // Update stats in compact view
    const compactStats = endpointCard.querySelector('.compact-stats');
    if (compactStats) {
        compactStats.textContent = `📊 ${data.requests} | 🎯 ${formatTokens(totalTokens)}`;

        // Update tooltip
        const tooltip = `${t('endpoints.requests')}: ${data.requests} | ${t('endpoints.errors')}: ${data.errors}\n${t('statistics.in')}: ${formatTokens(data.inputTokens)} | ${t('statistics.out')}: ${formatTokens(data.outputTokens)}`;
        compactStats.title = tooltip;
    }
}
