import { api } from '../api.js';
import { state } from '../state.js';
import { notifications } from '../utils/notifications.js';
import { formatNumber, formatTokens } from '../utils/formatters.js';
import { t } from '../utils/i18n.js';

class Dashboard {
    constructor() {
        this.container = document.getElementById('view-container');
        state.subscribe('networkConnections', (connections) => {
            if (state.get('currentView') === 'dashboard') {
                this.updateNetworkConnections(connections);
            }
        });
        // 监听语言切换
        window.addEventListener('languageChanged', () => {
            if (state.get('currentView') === 'dashboard') {
                this.render();
            }
        });
    }

    async render() {
        this.container.innerHTML = `
            <div class="dashboard">
                <h1>${t('dashboard.title')}</h1>
                <div id="stats-cards" class="grid grid-cols-4 mt-3">
                    <div class="stat-card">
                        <div class="stat-label">${t('dashboard.totalRequests')}</div>
                        <div class="stat-value" id="stat-requests">-</div>
                    </div>
                    <div class="stat-card">
                        <div class="stat-label">${t('dashboard.successRate')}</div>
                        <div class="stat-value" id="stat-success">-</div>
                    </div>
                    <div class="stat-card">
                        <div class="stat-label">${t('dashboard.inputTokens')}</div>
                        <div class="stat-value" id="stat-input-tokens">-</div>
                    </div>
                    <div class="stat-card">
                        <div class="stat-label">${t('dashboard.outputTokens')}</div>
                        <div class="stat-value" id="stat-output-tokens">-</div>
                    </div>
                </div>

                <div class="card mt-4">
                    <div class="card-header">
                        <h3 class="card-title">${t('network.title')}</h3>
                    </div>
                    <div class="card-body">
                        <div id="network-status"></div>
                        <div class="network-connections-title mt-3">${t('network.activeConnections')}</div>
                        <div id="network-connections"></div>
                    </div>
                </div>

                <div class="card mt-4">
                    <div class="card-header agent-provider-card-header">
                        <h3 class="card-title">${t('agentProvider.title')}</h3>
                        <button class="btn btn-primary" id="agent-provider-open">${t('agentProvider.open')}</button>
                    </div>
                    <div class="card-body">
                        <div id="agent-provider-summary" class="agent-provider-inline"></div>
                    </div>
                </div>

                <div class="grid grid-cols-2 mt-4">
                    <div class="card">
                        <div class="card-header">
                            <h3 class="card-title">${t('dashboard.activeEndpoints')}</h3>
                        </div>
                        <div class="card-body">
                            <div id="endpoints-list"></div>
                        </div>
                    </div>

                    <div class="card">
                        <div class="card-header">
                            <h3 class="card-title">${t('dashboard.recentActivity')}</h3>
                        </div>
                        <div class="card-body">
                            <canvas id="activity-chart"></canvas>
                        </div>
                    </div>
                </div>
            </div>
        `;

        await this.loadData();
        document.getElementById('agent-provider-open')?.addEventListener('click', () => this.openAgentProviderModal());
    }

    async loadData() {
        try {
            // Load stats
            const stats = await api.getStatsSummary();
            this.updateStats(stats);

            // Load endpoints
            const endpointsData = await api.getEndpoints();
            this.updateEndpoints(endpointsData.endpoints);

            const network = await api.getNetwork();
            this.updateNetwork(network);

            const agentProvider = await api.getAgentProviderStatus();
            this.updateAgentProviderSummary(agentProvider);

            // Load daily stats for chart
            const dailyStats = await api.getStatsDaily();
            this.renderChart(dailyStats);
        } catch (error) {
            notifications.error('Failed to load dashboard data: ' + error.message);
        }
    }

    updateAgentProviderSummary(status) {
        const container = document.getElementById('agent-provider-summary');
        if (!container) return;
        const targets = Array.isArray(status?.targets) ? status.targets : [];
        const detected = targets.filter(target => target.detected).length;
        container.innerHTML = `
            <div>
                <div class="network-label">${t('agentProvider.targetUrl')}</div>
                <code class="network-code">${this.escapeHtml(status?.targetUrl || '')}</code>
            </div>
            <div>
                <div class="network-label">${t('agentProvider.detected')}</div>
                <div class="network-value">${detected} / ${targets.length}</div>
            </div>
            <div>
                <div class="network-label">${t('agentProvider.latestBackup')}</div>
                <code class="network-code">${this.escapeHtml(status?.latestBackup?.id || t('agentProvider.noBackup'))}</code>
            </div>
        `;
    }

    async openAgentProviderModal() {
        try {
            const status = await api.getAgentProviderStatus();
            const overlay = document.createElement('div');
            overlay.className = 'modal-overlay';
            overlay.id = 'agent-provider-modal';
            overlay.innerHTML = this.renderAgentProviderModal(status);
            document.body.appendChild(overlay);
            overlay.querySelectorAll('.modal-close').forEach(button => {
                button.addEventListener('click', () => overlay.remove());
            });
            overlay.querySelector('#agent-provider-select-all')?.addEventListener('click', () => this.setAgentProviderChecks(true));
            overlay.querySelector('#agent-provider-clear')?.addEventListener('click', () => this.setAgentProviderChecks(false));
            overlay.querySelector('#agent-provider-apply')?.addEventListener('click', () => this.applyAgentProvider(overlay));
            overlay.querySelector('#agent-provider-restore')?.addEventListener('click', () => this.restoreAgentProvider(overlay, status?.latestBackup?.id || ''));
        } catch (error) {
            notifications.error(`${t('agentProvider.loadFailed')}: ${error.message}`);
        }
    }

    renderAgentProviderModal(status) {
        const targets = Array.isArray(status?.targets) ? status.targets : [];
        const rows = targets.map(target => `
            <label class="agent-provider-row ${target.detected ? 'detected' : 'missing'}">
                <input type="checkbox" value="${this.escapeHtml(target.target)}" ${target.detected ? 'checked' : ''}>
                <span>
                    <strong>${this.escapeHtml(target.label)}</strong>
                    <small title="${this.escapeHtml(target.path)}">${this.escapeHtml(target.path)}</small>
                </span>
                <em>${target.detected ? t('agentProvider.detected') : t('agentProvider.missing')}</em>
            </label>
        `).join('');
        return `
            <div class="modal agent-provider-web-modal">
                <div class="modal-header">
                    <h3 class="modal-title">${t('agentProvider.title')}</h3>
                    <button class="modal-close">×</button>
                </div>
                <div class="modal-body">
                    <div class="agent-provider-inline">
                        <div>
                            <div class="network-label">${t('agentProvider.targetUrl')}</div>
                            <code class="network-code">${this.escapeHtml(status?.targetUrl || '')}</code>
                        </div>
                        <div>
                            <div class="network-label">${t('agentProvider.latestBackup')}</div>
                            <code class="network-code">${this.escapeHtml(status?.latestBackup?.id || t('agentProvider.noBackup'))}</code>
                        </div>
                    </div>
                    <div class="agent-provider-toolbar">
                        <button class="btn btn-secondary" id="agent-provider-select-all">${t('agentProvider.selectAll')}</button>
                        <button class="btn btn-secondary" id="agent-provider-clear">${t('agentProvider.clearAll')}</button>
                        <label><input type="checkbox" id="agent-provider-create-missing"> ${t('agentProvider.createMissing')}</label>
                    </div>
                    <div class="agent-provider-list">${rows}</div>
                    <div id="agent-provider-results"></div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary modal-close">${t('common.close')}</button>
                    <button class="btn btn-secondary" id="agent-provider-restore" ${status?.latestBackup?.id ? '' : 'disabled'}>${t('agentProvider.restore')}</button>
                    <button class="btn btn-primary" id="agent-provider-apply">${t('agentProvider.apply')}</button>
                </div>
            </div>
        `;
    }

    setAgentProviderChecks(checked) {
        document.querySelectorAll('#agent-provider-modal .agent-provider-row input[type="checkbox"]').forEach(input => {
            input.checked = !!checked;
        });
    }

    selectedAgentProviderTargets(overlay) {
        return Array.from(overlay.querySelectorAll('.agent-provider-row input[type="checkbox"]:checked')).map(input => input.value);
    }

    async applyAgentProvider(overlay) {
        const targets = this.selectedAgentProviderTargets(overlay);
        if (!targets.length) {
            notifications.warning(t('agentProvider.noSelection'));
            return;
        }
        try {
            const result = await api.applyAgentProviderConfig({
                targets,
                createMissing: !!overlay.querySelector('#agent-provider-create-missing')?.checked,
            });
            this.renderAgentProviderResults(overlay, result.results);
            notifications.success(t('agentProvider.applyComplete'));
            this.updateAgentProviderSummary(await api.getAgentProviderStatus());
        } catch (error) {
            notifications.error(`${t('agentProvider.applyFailed')}: ${error.message}`);
        }
    }

    async restoreAgentProvider(overlay, backupId) {
        if (!backupId) {
            notifications.warning(t('agentProvider.noBackup'));
            return;
        }
        try {
            const result = await api.restoreAgentProviderBackup({
                backupId,
                targets: this.selectedAgentProviderTargets(overlay),
            });
            this.renderAgentProviderResults(overlay, result.results);
            notifications.success(t('agentProvider.restoreComplete'));
            this.updateAgentProviderSummary(await api.getAgentProviderStatus());
        } catch (error) {
            notifications.error(`${t('agentProvider.restoreFailed')}: ${error.message}`);
        }
    }

    renderAgentProviderResults(overlay, results = []) {
        const container = overlay.querySelector('#agent-provider-results');
        if (!container) return;
        container.innerHTML = `
            <div class="agent-provider-result-list">
                ${results.map(result => `
                    <div class="agent-provider-result ${this.escapeHtml(result.status)}">
                        <strong>${this.escapeHtml(result.label || result.target || '-')}</strong>
                        <span>${this.escapeHtml(t(`agentProvider.status.${result.status}`) || result.status)}</span>
                        <small>${this.escapeHtml(result.message || '')}</small>
                    </div>
                `).join('')}
            </div>
        `;
    }

    updateNetwork(network) {
        const container = document.getElementById('network-status');
        if (!container || !network) {
            return;
        }
        const lanURLs = Array.isArray(network.lanURLs) ? network.lanURLs : [];
        const modeLabel = network.listenMode === 'lan' ? t('network.lanAccess') : t('network.localOnly');
        container.innerHTML = `
            <div class="network-summary-grid">
                <div>
                    <div class="network-label">${t('network.listenMode')}</div>
                    <div class="network-value">${this.escapeHtml(modeLabel)}</div>
                </div>
                <div>
                    <div class="network-label">${t('network.localAddress')}</div>
                    <code class="network-code">${this.escapeHtml(network.localURL || '')}</code>
                </div>
                <div class="network-span">
                    <div class="network-label">${t('network.lanAddresses')}</div>
                    ${lanURLs.length > 0
                        ? lanURLs.map(url => `<code class="network-code">${this.escapeHtml(url)}</code>`).join('')
                        : `<div class="text-muted">${t('network.noLanAddresses')}</div>`}
                </div>
            </div>
            ${network.listenMode === 'lan' ? `<div class="network-warning mt-2">${t('network.riskWarning')}</div>` : ''}
        `;
        this.updateNetworkConnections(network.connections);
    }

    updateNetworkConnections(connections) {
        const container = document.getElementById('network-connections');
        if (!container) {
            return;
        }
        const snapshot = connections || {};
        const active = Array.isArray(snapshot.connections) ? snapshot.connections : [];
        const byCategory = snapshot.byCategory || {};
        const categories = ['proxy', 'admin_ui', 'api', 'health', 'events'];
        const summary = categories.map(category => `
            <span class="network-count">${t(`network.categories.${category}`)} ${Number(byCategory[category] || 0)}</span>
        `).join('');

        if (active.length === 0) {
            container.innerHTML = `
                <div class="network-counts">${summary}</div>
                <div class="empty-state">${t('network.noActiveConnections')}</div>
            `;
            return;
        }

        container.innerHTML = `
            <div class="network-counts">${summary}</div>
            <div class="network-connection-table">
                ${active.map(conn => `
                    <div class="network-connection-row">
                        <span>${t(`network.categories.${conn.category}`)}</span>
                        <span>${this.escapeHtml(conn.clientIp || 'unknown')}</span>
                        <span title="${this.escapeHtml(conn.path || '')}">${this.escapeHtml(conn.method || '')} ${this.escapeHtml(conn.path || '')}</span>
                        <span>${this.formatDuration(conn.durationMillis)}</span>
                    </div>
                `).join('')}
            </div>
        `;
    }

    formatDuration(ms) {
        const seconds = Math.max(0, Math.floor((Number(ms) || 0) / 1000));
        if (seconds < 60) {
            return `${seconds}s`;
        }
        return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
    }

    updateStats(stats) {
        const totalRequests = stats.TotalRequests || 0;
        const totalErrors = stats.TotalErrors || 0;
        const successRate = totalRequests > 0
            ? ((totalRequests - totalErrors) / totalRequests * 100).toFixed(1)
            : 0;

        document.getElementById('stat-requests').textContent = formatNumber(totalRequests);
        document.getElementById('stat-success').textContent = successRate + '%';
        document.getElementById('stat-input-tokens').textContent = formatTokens(stats.TotalInputTokens || 0);
        document.getElementById('stat-output-tokens').textContent = formatTokens(stats.TotalOutputTokens || 0);
    }

    updateEndpoints(endpoints) {
        const container = document.getElementById('endpoints-list');

        if (!endpoints || endpoints.length === 0) {
            container.innerHTML = `<div class="empty-state"><p>${t('dashboard.noEndpoints')}</p></div>`;
            return;
        }

        const enabledEndpoints = endpoints.filter(ep => ep.enabled);

        if (enabledEndpoints.length === 0) {
            container.innerHTML = `<div class="empty-state"><p>${t('dashboard.noEnabledEndpoints')}</p></div>`;
            return;
        }

        container.innerHTML = `
            <div class="table-container">
                <table class="table">
                    <thead>
                        <tr>
                            <th>${t('common.name')}</th>
                            <th>${t('endpoints.transformer')}</th>
                            <th>${t('common.status')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${enabledEndpoints.map(ep => `
                            <tr>
                                <td>${this.escapeHtml(ep.name)}</td>
                                <td>${this.escapeHtml(ep.transformer)}</td>
                                <td>
                                    <span class="status-indicator online"></span>
                                    <span class="badge badge-success">${t('common.active')}</span>
                                </td>
                            </tr>
                        `).join('')}
                    </tbody>
                </table>
            </div>
        `;
    }

    renderChart(dailyStats) {
        const canvas = document.getElementById('activity-chart');
        const ctx = canvas.getContext('2d');

        // Simple bar chart showing requests
        const stats = dailyStats.stats || {};
        const endpoints = Object.keys(stats.endpoints || {});
        const requests = endpoints.map(ep => stats.endpoints[ep].requests || 0);

        new Chart(ctx, {
            type: 'bar',
            data: {
                labels: endpoints,
                datasets: [{
                    label: 'Requests',
                    data: requests,
                    backgroundColor: '#3b82f6',
                    borderColor: '#2563eb',
                    borderWidth: 1
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: true,
                plugins: {
                    legend: {
                        display: false
                    }
                },
                scales: {
                    y: {
                        beginAtZero: true
                    }
                }
            }
        });
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

export const dashboard = new Dashboard();
