import { formatTokens } from '../utils/format.js';
import { summarizeRequestStats } from '../utils/stats.js';
import { t } from '../i18n/index.js';

let endpointStats = {}; // Backward compatibility - stores current period only
let endpointStatsCache = {}; // { endpoint1: { daily: {...}, yesterday: {...}, weekly: {...}, monthly: {...} } }
let totalStatsCache = {}; // { daily: {...}, yesterday: {...}, weekly: {...}, monthly: {...} }
let currentPeriod = 'daily'; // 'daily', 'weekly', 'monthly'
let statsFilters = {
    endpointName: '',
    clientIp: '',
    clientIpQuery: ''
};

export function getEndpointStats() {
    // Dynamically build endpoint stats from 4-period cache for current period
    const period = currentPeriod;
    const result = {};

    for (const [name, periods] of Object.entries(endpointStatsCache)) {
        result[name] = periods[period] || { requests: 0, errors: 0, inputTokens: 0, outputTokens: 0 };
    }

    // Fallback: if cache is empty (during initialization), return old cache for backward compatibility
    return Object.keys(result).length > 0 ? result : endpointStats;
}

export function getCurrentPeriod() {
    return currentPeriod;
}

export function hasActiveStatsFilters() {
    return !!(statsFilters.endpointName || statsFilters.clientIp || statsFilters.clientIpQuery);
}

export function getStatsFilters() {
    return { ...statsFilters };
}

// Update 4-period cache for a single endpoint
export function updateEndpointStatsCache(endpointName, periodData) {
    if (!endpointStatsCache[endpointName]) {
        endpointStatsCache[endpointName] = {};
    }
    Object.assign(endpointStatsCache[endpointName], periodData);
}

// Update 4-period cache for totals
export function updateTotalStatsCache(periodData) {
    Object.assign(totalStatsCache, periodData);
}

// Get stats for a specific endpoint and period
export function getEndpointPeriodStats(endpointName, period) {
    return endpointStatsCache[endpointName]?.[period] ||
           { requests: 0, errors: 0, inputTokens: 0, outputTokens: 0 };
}

// Get total stats for a specific period
export function getTotalPeriodStats(period) {
    return totalStatsCache[period] ||
           { requests: 0, errors: 0, inputTokens: 0, outputTokens: 0 };
}

// Load statistics (legacy function for backward compatibility)
export async function loadStats() {
    try {
        const statsStr = await window.go.main.App.GetStats();
        const stats = JSON.parse(statsStr);

        let totalSuccess = 0;
        let totalFailed = 0;
        let totalInputTokens = 0;
        let totalOutputTokens = 0;

        for (const epStats of Object.values(stats.endpoints || {})) {
            totalSuccess += epStats.requests || 0;
            totalFailed += epStats.errors || 0;
            totalInputTokens += epStats.inputTokens || 0;
            totalOutputTokens += epStats.outputTokens || 0;
        }

        document.getElementById('totalRequests').textContent = totalSuccess + totalFailed;
        document.getElementById('successRequests').textContent = totalSuccess;
        document.getElementById('failedRequests').textContent = totalFailed;

        const totalTokens = totalInputTokens + totalOutputTokens;
        document.getElementById('totalTokens').textContent = formatTokens(totalTokens);
        document.getElementById('totalInputTokens').textContent = formatTokens(totalInputTokens);
        document.getElementById('totalOutputTokens').textContent = formatTokens(totalOutputTokens);

        endpointStats = stats.endpoints || {};

        return stats;
    } catch (error) {
        console.error('Failed to load stats:', error);
        return null;
    }
}

// Load statistics by period (daily, yesterday, weekly, monthly)
export async function loadStatsByPeriod(period = 'daily') {
    try {
        currentPeriod = period;

        let statsStr;
        if (hasActiveStatsFilters() && window.go.main.App.GetStatsByPeriod) {
            statsStr = await window.go.main.App.GetStatsByPeriod(
                period,
                statsFilters.endpointName,
                statsFilters.clientIp,
                statsFilters.clientIp ? '' : statsFilters.clientIpQuery
            );
        } else {
            switch (period) {
                case 'daily':
                    statsStr = await window.go.main.App.GetStatsDaily();
                    break;
                case 'yesterday':
                    statsStr = await window.go.main.App.GetStatsYesterday();
                    break;
                case 'weekly':
                    statsStr = await window.go.main.App.GetStatsWeekly();
                    break;
                case 'monthly':
                    statsStr = await window.go.main.App.GetStatsMonthly();
                    break;
                default:
                    statsStr = await window.go.main.App.GetStatsDaily();
            }
        }

        const stats = JSON.parse(statsStr);
        const requestSummary = summarizeRequestStats(
            stats.successfulRequests ??
                stats.totalSuccess ??
                Math.max((stats.totalRequests || 0) - (stats.totalErrors || 0), 0),
            stats.totalErrors
        );

        // Update UI elements
        document.getElementById('periodTotalRequests').textContent = requestSummary.total;
        document.getElementById('periodSuccess').textContent = requestSummary.success;
        document.getElementById('periodFailed').textContent = requestSummary.failed;

        const totalTokens = (stats.totalInputTokens || 0) + (stats.totalOutputTokens || 0);
        document.getElementById('periodTotalTokens').textContent = formatTokens(totalTokens);
        document.getElementById('periodInputTokens').textContent = formatTokens(stats.totalInputTokens || 0);
        document.getElementById('periodOutputTokens').textContent = formatTokens(stats.totalOutputTokens || 0);

        // Update endpoint stats (active / total)
        const activeEndpoints = stats.activeEndpoints || 0;
        const totalEndpoints = stats.totalEndpoints || 0;
        document.getElementById('activeEndpointsDisplay').textContent = activeEndpoints;
        document.getElementById('totalEndpointsDisplay').textContent = totalEndpoints;

        // Load and display trend for current period
        await loadTrend(period);

        if (!hasActiveStatsFilters()) {
            // Store endpoint stats in 4-period cache structure
            for (const [name, periodStats] of Object.entries(stats.endpoints || {})) {
                if (!endpointStatsCache[name]) {
                    endpointStatsCache[name] = {};
                }
                endpointStatsCache[name][period] = periodStats;
            }

            // Store aggregated totals in 4-period cache
            totalStatsCache[period] = {
                requests: requestSummary.success,
                errors: requestSummary.failed,
                inputTokens: stats.totalInputTokens || 0,
                outputTokens: stats.totalOutputTokens || 0
            };
        }

        // Backward compatibility: update old single-period cache
        endpointStats = stats.endpoints || {};

        return stats;
    } catch (error) {
        console.error('Failed to load stats by period:', error);
        return null;
    }
}

// Load trend comparison data for specified period
async function loadTrend(period = 'daily') {
    try {
        const trendStr = hasActiveStatsFilters() && window.go.main.App.GetStatsTrendByPeriodFiltered
            ? await window.go.main.App.GetStatsTrendByPeriodFiltered(
                period,
                statsFilters.endpointName,
                statsFilters.clientIp,
                statsFilters.clientIp ? '' : statsFilters.clientIpQuery
            )
            : await window.go.main.App.GetStatsTrendByPeriod(period);
        const trend = JSON.parse(trendStr);

        const requestsTrend = formatTrend(trend.trend);
        const errorsTrend = formatTrend(trend.errorsTrend);
        const tokensTrend = formatTrend(trend.tokensTrend);

        const requestsEl = document.getElementById('requestsTrend');
        const errorsEl = document.getElementById('errorsTrend');
        const tokensEl = document.getElementById('tokensTrend');

        if (requestsEl) {
            requestsEl.textContent = requestsTrend.text;
            requestsEl.className = 'trend ' + requestsTrend.className;
        }

        if (errorsEl) {
            // For errors, negative trend is good
            errorsEl.textContent = errorsTrend.text;
            errorsEl.className = 'trend ' + (trend.errorsTrend < 0 ? 'trend-down' : trend.errorsTrend > 0 ? 'trend-up' : 'trend-flat');
        }

        if (tokensEl) {
            tokensEl.textContent = tokensTrend.text;
            tokensEl.className = 'trend ' + tokensTrend.className;
        }
    } catch (error) {
        console.error('Failed to load trend:', error);
    }
}

// Format trend value for display
function formatTrend(value) {
    const absValue = Math.abs(value);
    const formattedValue = absValue.toFixed(1);

    if (value > 0) {
        return {
            text: `↑ ${formattedValue}%`,
            className: 'trend-up'
        };
    } else if (value < 0) {
        return {
            text: `↓ ${formattedValue}%`,
            className: 'trend-down'
        };
    } else {
        return {
            text: '→ 0%',
            className: 'trend-flat'
        };
    }
}

// Switch statistics period
export async function switchStatsPeriod(period) {
    // Handle history modal separately
    if (period === 'history') {
        // Open history modal without changing active tab
        import('./history.js').then(module => {
            module.showHistoryModal();
        });
        return;
    }

    currentPeriod = period;

    // Update tab buttons
    const tabs = document.querySelectorAll('.stats-tab-btn');
    tabs.forEach(tab => {
        if (tab.dataset.period === period) {
            tab.classList.add('active');
        } else {
            tab.classList.remove('active');
        }
    });

    // Check if cache has data for this period
    const cachedTotals = totalStatsCache[period];
    const hasCache = !hasActiveStatsFilters() && cachedTotals && Object.keys(endpointStatsCache).length > 0;

    if (hasCache) {
        // Fast path: update DOM from cache
        updateDOMFromCache(period);
        await loadTrend(period); // Still load trend from backend
    } else {
        // Fallback path: load from backend if cache miss
        await loadStatsByPeriod(period);
    }

    // Reload endpoint list to update endpoint stats cards
    if (window.loadConfig) {
        window.loadConfig();
    }
}

export async function initStatsFilters() {
    await loadStatsFilterOptions();
    bindStatsFilterControls();
}

export async function refreshStatsForCurrentFilter() {
    await loadStatsByPeriod(currentPeriod);
    if (window.loadConfig) {
        window.loadConfig();
    }
}

async function loadStatsFilterOptions() {
    const endpointSelect = document.getElementById('statsEndpointFilter');
    const ipSelect = document.getElementById('statsIpFilter');
    if (!endpointSelect || !ipSelect || !window.go?.main?.App?.GetStatsFilters) {
        return;
    }

    try {
        const raw = await window.go.main.App.GetStatsFilters();
        const data = JSON.parse(raw || '{}');
        const endpoints = Array.isArray(data.endpoints) ? data.endpoints : [];
        const clientIps = Array.isArray(data.clientIps) ? data.clientIps : [];

        endpointSelect.innerHTML = [
            `<option value="">${t('statistics.allEndpoints')}</option>`,
            ...endpoints.map(option => {
                const name = option.name || '';
                const deleted = option.deleted ? ` ${t('statistics.deletedEndpointSuffix')}` : '';
                return `<option value="${escapeOption(name)}">${escapeHtml(name)}${deleted}</option>`;
            })
        ].join('');
        ipSelect.innerHTML = [
            `<option value="">${t('statistics.allIPs')}</option>`,
            ...clientIps.map(ip => `<option value="${escapeOption(ip)}">${escapeHtml(ip)}</option>`)
        ].join('');

        endpointSelect.value = statsFilters.endpointName;
        ipSelect.value = statsFilters.clientIp;
    } catch (error) {
        console.error('Failed to load stats filter options:', error);
    }
}

function bindStatsFilterControls() {
    const endpointSelect = document.getElementById('statsEndpointFilter');
    const ipSelect = document.getElementById('statsIpFilter');
    const ipQuery = document.getElementById('statsIpQueryFilter');
    const clearBtn = document.getElementById('statsClearFilters');
    if (!endpointSelect || !ipSelect || !ipQuery || !clearBtn) {
        return;
    }

    endpointSelect.onchange = async () => {
        clearStatsCaches();
        statsFilters.endpointName = endpointSelect.value;
        await refreshStatsForCurrentFilter();
    };
    ipSelect.onchange = async () => {
        clearStatsCaches();
        statsFilters.clientIp = ipSelect.value;
        if (statsFilters.clientIp) {
            statsFilters.clientIpQuery = '';
            ipQuery.value = '';
        }
        await refreshStatsForCurrentFilter();
    };
    ipQuery.oninput = debounce(async () => {
        clearStatsCaches();
        statsFilters.clientIpQuery = ipQuery.value.trim();
        await refreshStatsForCurrentFilter();
    }, 300);
    clearBtn.onclick = async () => {
        clearStatsCaches();
        statsFilters = { endpointName: '', clientIp: '', clientIpQuery: '' };
        endpointSelect.value = '';
        ipSelect.value = '';
        ipQuery.value = '';
        await refreshStatsForCurrentFilter();
    };
}

function clearStatsCaches() {
    endpointStatsCache = {};
    totalStatsCache = {};
}

function debounce(fn, delay) {
    let timer;
    return (...args) => {
        clearTimeout(timer);
        timer = setTimeout(() => fn(...args), delay);
    };
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text || '';
    return div.innerHTML;
}

function escapeOption(text) {
    return escapeHtml(text).replace(/"/g, '&quot;');
}

// Update DOM elements from cached data (zero-delay switching)
function updateDOMFromCache(period) {
    const totals = totalStatsCache[period];
    if (!totals) return;
    const requestSummary = summarizeRequestStats(totals.requests, totals.errors);

    document.getElementById('periodTotalRequests').textContent = requestSummary.total;
    document.getElementById('periodSuccess').textContent = requestSummary.success;
    document.getElementById('periodFailed').textContent = requestSummary.failed;

    const totalTokens = (totals.inputTokens || 0) + (totals.outputTokens || 0);
    document.getElementById('periodTotalTokens').textContent = formatTokens(totalTokens);
    document.getElementById('periodInputTokens').textContent = formatTokens(totals.inputTokens || 0);
    document.getElementById('periodOutputTokens').textContent = formatTokens(totals.outputTokens || 0);

    // Sync old endpointStats cache for backward compatibility
    // This ensures any code still directly accessing endpointStats gets current period data
    endpointStats = {};
    for (const [name, periods] of Object.entries(endpointStatsCache)) {
        endpointStats[name] = periods[period] || { requests: 0, errors: 0, inputTokens: 0, outputTokens: 0 };
    }
}

// DEPRECATED: Incremental stats update is no longer used
// Header stats are now updated directly in main.js handleStatsUpdate() using backend-provided totals
// This function is kept for backward compatibility only
export function updateStatsIncremental(endpointName, data) {
    console.warn('updateStatsIncremental is deprecated, header stats are now updated by handleStatsUpdate in main.js');
    // Function body retained for backward compatibility but not actively used
}
