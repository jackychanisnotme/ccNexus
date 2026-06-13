import { api } from '../api.js';
import { notifications } from '../utils/notifications.js';
import { formatNumber, formatTokens } from '../utils/formatters.js';
import { t } from '../utils/i18n.js';

class Stats {
    constructor() {
        this.container = document.getElementById('view-container');
        this.currentPeriod = 'daily';
        this.filters = {
            endpoint: '',
            clientIp: '',
            clientIpQuery: ''
        };
        this.filterOptions = {
            endpoints: [],
            clientIps: []
        };
        // 监听语言切换
        window.addEventListener('languageChanged', () => {
            if (state.get('currentView') === 'stats') {
                this.render();
            }
        });
    }

    async render() {
        await this.loadFilterOptions();
        this.container.innerHTML = `
            <div class="stats">
                <h1>${t('stats.title')}</h1>

                <div class="flex gap-2 mt-3 mb-3">
                    <button class="btn btn-sm btn-primary period-btn active" data-period="daily">${t('stats.daily')}</button>
                    <button class="btn btn-sm btn-secondary period-btn" data-period="weekly">${t('stats.weekly')}</button>
                    <button class="btn btn-sm btn-secondary period-btn" data-period="monthly">${t('stats.monthly')}</button>
                </div>

                <div class="stats-filter-row">
                    <select id="stats-endpoint-filter" class="form-select">
                        <option value="">${t('stats.allEndpoints')}</option>
                        ${this.filterOptions.endpoints.map(option => `
                            <option value="${this.escapeHtml(option.name)}" ${option.name === this.filters.endpoint ? 'selected' : ''}>
                                ${this.escapeHtml(option.name)}${option.deleted ? ` ${t('stats.deletedEndpointSuffix')}` : ''}
                            </option>
                        `).join('')}
                    </select>
                    <select id="stats-ip-filter" class="form-select">
                        <option value="">${t('stats.allIPs')}</option>
                        ${this.filterOptions.clientIps.map(ip => `
                            <option value="${this.escapeHtml(ip)}" ${ip === this.filters.clientIp ? 'selected' : ''}>${this.escapeHtml(ip)}</option>
                        `).join('')}
                    </select>
                    <input id="stats-ip-query-filter" class="form-input" type="text" placeholder="${t('stats.ipSearchPlaceholder')}" value="${this.escapeHtml(this.filters.clientIpQuery)}">
                    <button id="stats-clear-filters" class="btn btn-sm btn-secondary">${t('stats.clearFilters')}</button>
                </div>

                <div id="stats-content"></div>
            </div>
        `;

        document.querySelectorAll('.period-btn').forEach(btn => {
            btn.addEventListener('click', () => {
                document.querySelectorAll('.period-btn').forEach(b => {
                    b.classList.remove('btn-primary', 'active');
                    b.classList.add('btn-secondary');
                });
                btn.classList.remove('btn-secondary');
                btn.classList.add('btn-primary', 'active');
                this.loadStats(btn.dataset.period);
            });
        });
        this.bindFilterControls();

        await this.loadStats(this.currentPeriod);
    }

    async loadStats(period) {
        try {
            this.currentPeriod = period;
            let data;
            const params = this.requestFilters();
            switch (period) {
                case 'daily':
                    data = await api.getStatsDaily(params);
                    break;
                case 'weekly':
                    data = await api.getStatsWeekly(params);
                    break;
                case 'monthly':
                    data = await api.getStatsMonthly(params);
                    break;
            }

            this.renderStats(data);
        } catch (error) {
            notifications.error(`${t('stats.failedToLoad')}: ${error.message}`);
        }
    }

    async loadFilterOptions() {
        try {
            const data = await api.getStatsFilters();
            this.filterOptions = {
                endpoints: Array.isArray(data.endpoints) ? data.endpoints : [],
                clientIps: Array.isArray(data.clientIps) ? data.clientIps : []
            };
        } catch (error) {
            console.error('Failed to load stats filter options:', error);
            this.filterOptions = { endpoints: [], clientIps: [] };
        }
    }

    bindFilterControls() {
        const endpointSelect = document.getElementById('stats-endpoint-filter');
        const ipSelect = document.getElementById('stats-ip-filter');
        const ipQuery = document.getElementById('stats-ip-query-filter');
        const clearButton = document.getElementById('stats-clear-filters');
        if (!endpointSelect || !ipSelect || !ipQuery || !clearButton) {
            return;
        }

        endpointSelect.addEventListener('change', () => {
            this.filters.endpoint = endpointSelect.value;
            this.loadStats(this.currentPeriod);
        });
        ipSelect.addEventListener('change', () => {
            this.filters.clientIp = ipSelect.value;
            if (this.filters.clientIp) {
                this.filters.clientIpQuery = '';
                ipQuery.value = '';
            }
            this.loadStats(this.currentPeriod);
        });
        ipQuery.addEventListener('input', this.debounce(() => {
            this.filters.clientIpQuery = ipQuery.value.trim();
            this.loadStats(this.currentPeriod);
        }, 300));
        clearButton.addEventListener('click', () => {
            this.filters = { endpoint: '', clientIp: '', clientIpQuery: '' };
            endpointSelect.value = '';
            ipSelect.value = '';
            ipQuery.value = '';
            this.loadStats(this.currentPeriod);
        });
    }

    requestFilters() {
        return {
            endpoint: this.filters.endpoint,
            clientIp: this.filters.clientIp,
            clientIpQuery: this.filters.clientIp ? '' : this.filters.clientIpQuery
        };
    }

    debounce(fn, delay) {
        let timer;
        return (...args) => {
            clearTimeout(timer);
            timer = setTimeout(() => fn(...args), delay);
        };
    }

    renderStats(data) {
        const stats = data.stats || {};
        const container = document.getElementById('stats-content');

        container.innerHTML = `
            <div class="grid grid-cols-4 mb-4">
                <div class="stat-card">
                    <div class="stat-label">${t('stats.totalRequests')}</div>
                    <div class="stat-value">${formatNumber(stats.totalRequests || 0)}</div>
                </div>
                <div class="stat-card">
                    <div class="stat-label">${t('stats.successful')}</div>
                    <div class="stat-value">${formatNumber(stats.totalSuccess || 0)}</div>
                </div>
                <div class="stat-card">
                    <div class="stat-label">${t('stats.errors')}</div>
                    <div class="stat-value">${formatNumber(stats.totalErrors || 0)}</div>
                </div>
                <div class="stat-card">
                    <div class="stat-label">${t('stats.totalTokens')}</div>
                    <div class="stat-value">${formatTokens((stats.totalInputTokens || 0) + (stats.totalOutputTokens || 0))}</div>
                </div>
            </div>

            <div class="card">
                <div class="card-header">
                    <h3 class="card-title">${t('stats.endpointBreakdown')}</h3>
                </div>
                <div class="card-body">
                    ${this.renderEndpointTable(stats.endpoints || {})}
                </div>
            </div>
        `;
    }

    renderEndpointTable(endpoints) {
        const endpointNames = Object.keys(endpoints);

        if (endpointNames.length === 0) {
            return `<div class="empty-state"><p>${t('stats.noDataAvailable')}</p></div>`;
        }

        return `
            <div class="table-container">
                <table class="table">
                    <thead>
                        <tr>
                            <th>${t('stats.endpoint')}</th>
                            <th>${t('stats.requests')}</th>
                            <th>${t('stats.errors')}</th>
                            <th>${t('stats.inputTokens')}</th>
                            <th>${t('stats.outputTokens')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${endpointNames.map(name => {
                            const ep = endpoints[name];
                            return `
                                <tr>
                                    <td><strong>${this.escapeHtml(name)}</strong></td>
                                    <td>${formatNumber(ep.requests || 0)}</td>
                                    <td>${formatNumber(ep.errors || 0)}</td>
                                    <td>${formatTokens(ep.inputTokens || 0)}</td>
                                    <td>${formatTokens(ep.outputTokens || 0)}</td>
                                </tr>
                            `;
                        }).join('')}
                    </tbody>
                </table>
            </div>
        `;
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

export const stats = new Stats();
