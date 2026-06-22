import { api } from '../api.js';
import { state } from '../state.js';
import { notifications } from '../utils/notifications.js';
import { t } from '../utils/i18n.js';

const defaultFailover = {
    recoveredEndpointPolicy: 'deprioritize',
    cooldowns: {
        quotaExhaustedSec: 3600,
        rateLimitedSec: 120,
        upstreamErrorSec: 60,
        networkErrorSec: 30,
        tokenUnavailableSec: 600,
        configErrorSec: 1800
    },
    circuitBreaker: {
        consecutiveFailures: 3,
        windowSec: 60,
        failureRateThreshold: 0.60,
        minRequests: 5,
        cooldownSec: 600
    }
};

class Settings {
    constructor() {
        this.container = document.getElementById('view-container');
        this.currentFailover = defaultFailover;
        this.currentNetwork = null;
        state.subscribe('networkConnections', (connections) => {
            if (state.get('currentView') === 'settings' && this.currentNetwork) {
                this.currentNetwork.connections = connections;
                this.renderNetworkStatus(this.currentNetwork);
            }
        });
        window.addEventListener('languageChanged', () => {
            if (state.get('currentView') === 'settings') {
                this.render();
            }
        });
    }

    async render() {
        this.container.innerHTML = `
            <div class="settings">
                <h1>${t('settings.title')}</h1>
                <div class="card mt-3">
                    <div class="card-header">
                        <h3 class="card-title">${t('settings.licenseTitle')}</h3>
                    </div>
                    <div class="card-body">
                        <div class="license-summary-grid">
                            <div>
                                <div class="network-label">${t('settings.licenseStatus')}</div>
                                <div id="license-status" class="network-value">-</div>
                            </div>
                            <div>
                                <div class="network-label">${t('settings.licenseExpiresAt')}</div>
                                <div id="license-expires-at" class="network-value">-</div>
                            </div>
                            <div>
                                <div class="network-label">${t('settings.licenseRemainingDays')}</div>
                                <div id="license-remaining-days" class="network-value">-</div>
                            </div>
                            <div>
                                <div class="network-label">${t('settings.licensePlan')}</div>
                                <div id="license-plan" class="network-value">-</div>
                            </div>
                        </div>
                        <div class="form-group mt-3">
                            <label class="form-label">${t('settings.licenseCard')}</label>
                            <textarea class="form-input" id="license-card-key" rows="3" placeholder="${t('settings.licenseCardPlaceholder')}"></textarea>
                        </div>
                        <button type="button" class="btn btn-secondary" id="license-refresh-btn">${t('settings.licenseRefresh')}</button>
                        <button type="button" class="btn btn-primary" id="license-activate-btn">${t('settings.licenseActivate')}</button>
                    </div>
                </div>
                <div class="card mt-3">
                    <div class="card-header">
                        <h3 class="card-title">${t('network.title')}</h3>
                    </div>
                    <div class="card-body">
                        <form id="network-form">
                            <div class="form-group">
                                <label class="form-label">${t('network.listenMode')}</label>
                                <select class="form-select" name="listenMode">
                                    <option value="local">${t('network.localOnly')}</option>
                                    <option value="lan">${t('network.lanAccess')}</option>
                                </select>
                            </div>
                            <div id="network-risk-warning" class="network-warning" style="display: none;">${t('network.riskWarning')}</div>
                            <div id="network-status" class="mt-2"></div>
                            <button type="submit" class="btn btn-primary mt-3">${t('network.saveAccess')}</button>
                        </form>
                    </div>
                </div>
                <div class="card mt-3">
                    <div class="card-header">
                        <h3 class="card-title">${t('settings.failoverTitle')}</h3>
                    </div>
                    <div class="card-body">
                        <form id="settings-form">
                            <div class="form-group">
                                <label class="form-label">${t('settings.recoveredEndpointPolicy')}</label>
                                <select class="form-select" name="recoveredEndpointPolicy">
                                    <option value="deprioritize">${t('settings.policies.deprioritize')}</option>
                                    <option value="auto_return">${t('settings.policies.autoReturn')}</option>
                                </select>
                            </div>
                            <div class="grid grid-cols-2">
                                ${this.renderCooldownInput('quotaExhaustedSec', t('settings.cooldowns.quotaExhausted'))}
                                ${this.renderCooldownInput('rateLimitedSec', t('settings.cooldowns.rateLimited'))}
                                ${this.renderCooldownInput('upstreamErrorSec', t('settings.cooldowns.upstreamError'))}
                                ${this.renderCooldownInput('networkErrorSec', t('settings.cooldowns.networkError'))}
                                ${this.renderCooldownInput('tokenUnavailableSec', t('settings.cooldowns.tokenUnavailable'))}
                                ${this.renderCooldownInput('configErrorSec', t('settings.cooldowns.configError'))}
                            </div>
                            <button type="submit" class="btn btn-primary mt-3">${t('common.save')}</button>
                        </form>
                    </div>
                </div>
            </div>
        `;

        document.getElementById('settings-form').addEventListener('submit', (event) => this.save(event));
        document.getElementById('license-refresh-btn').addEventListener('click', () => this.loadLicense());
        document.getElementById('license-activate-btn').addEventListener('click', () => this.activateLicense());
        document.getElementById('network-form').addEventListener('submit', (event) => this.saveNetwork(event));
        document.querySelector('#network-form select[name="listenMode"]').addEventListener('change', (event) => {
            this.toggleNetworkWarning(event.currentTarget.value);
        });
        await this.load();
        await this.loadLicense();
        await this.loadNetwork();
    }

    renderCooldownInput(name, label) {
        return `
            <div class="form-group">
                <label class="form-label">${label}</label>
                <input class="form-input" type="number" min="0" name="${name}">
            </div>
        `;
    }

    async load() {
        try {
            const config = await api.getConfig();
            const failover = this.normalizeFailover(config.failover);
            this.currentFailover = failover;
            const form = document.getElementById('settings-form');
            form.elements.recoveredEndpointPolicy.value = failover.recoveredEndpointPolicy;
            Object.entries(failover.cooldowns).forEach(([key, value]) => {
                if (form.elements[key]) {
                    form.elements[key].value = value;
                }
            });
        } catch (error) {
            notifications.error(`${t('settings.failedToLoad')}: ${error.message}`);
        }
    }

    async loadNetwork() {
        try {
            const network = await api.getNetwork();
            this.currentNetwork = network;
            const form = document.getElementById('network-form');
            if (form?.elements.listenMode) {
                form.elements.listenMode.value = network.listenMode || 'local';
            }
            this.toggleNetworkWarning(network.listenMode);
            this.renderNetworkStatus(network);
        } catch (error) {
            notifications.error(`${t('network.failedToLoad')}: ${error.message}`);
        }
    }

    async loadLicense() {
        try {
            const status = await api.getLicenseStatus();
            this.renderLicense(status);
        } catch (error) {
            notifications.error(`${t('settings.licenseLoadFailed')}: ${error.message}`);
        }
    }

    async activateLicense() {
        const input = document.getElementById('license-card-key');
        const cardKey = input?.value?.trim() || '';
        if (!cardKey) {
            notifications.error(t('settings.licenseCardRequired'));
            return;
        }
        try {
            const result = await api.activateLicense(cardKey);
            this.renderLicense(result);
            input.value = '';
            notifications.success(t('settings.licenseActivated'));
        } catch (error) {
            notifications.error(`${t('settings.licenseActivateFailed')}: ${error.message}`);
        }
    }

    renderLicense(status) {
        const setText = (id, value) => {
            const el = document.getElementById(id);
            if (el) el.textContent = value || '-';
        };
        setText('license-status', status?.licensed ? t('settings.licenseActive') : status?.expired ? t('settings.licenseExpired') : t('settings.licenseInactive'));
        setText('license-expires-at', this.formatDate(status?.expiresAt));
        setText('license-remaining-days', status?.licensed ? String(status.remainingDays ?? 0) : '-');
        setText('license-plan', this.planLabel(status?.lastPlan));
    }

    formatDate(value) {
        if (!value || value === '0001-01-01T00:00:00Z') {
            return '-';
        }
        const date = new Date(value);
        return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString();
    }

    planLabel(plan) {
        const labels = {
            monthly: t('settings.planMonthly'),
            quarterly: t('settings.planQuarterly'),
            half_year: t('settings.planHalfYear'),
            yearly: t('settings.planYearly'),
            custom: t('settings.planCustom')
        };
        return labels[plan] || plan || '-';
    }

    async saveNetwork(event) {
        event.preventDefault();
        const form = event.currentTarget;
        try {
            const network = await api.updateNetwork(form.elements.listenMode.value);
            this.currentNetwork = network;
            this.renderNetworkStatus(network);
            this.toggleNetworkWarning(network.listenMode);
            notifications.success(t('network.saved'));
        } catch (error) {
            notifications.error(`${t('network.failedToSave')}: ${error.message}`);
        }
    }

    renderNetworkStatus(network) {
        const container = document.getElementById('network-status');
        if (!container || !network) {
            return;
        }
        const lanURLs = Array.isArray(network.lanURLs) ? network.lanURLs : [];
        container.innerHTML = `
            <div class="network-summary-grid">
                <div>
                    <div class="network-label">${t('network.localAddress')}</div>
                    <code class="network-code">${this.escapeHtml(network.localURL || '')}</code>
                </div>
                <div>
                    <div class="network-label">${t('network.lanAddresses')}</div>
                    ${lanURLs.length > 0
                        ? lanURLs.map(url => `<code class="network-code">${this.escapeHtml(url)}</code>`).join('')
                        : `<div class="text-muted">${t('network.noLanAddresses')}</div>`}
                </div>
            </div>
            ${network.restartRequired ? `<div class="network-restart mt-2">${t('network.restartRequired')}</div>` : ''}
        `;
    }

    toggleNetworkWarning(mode) {
        const warning = document.getElementById('network-risk-warning');
        if (warning) {
            warning.style.display = mode === 'lan' ? 'block' : 'none';
        }
    }

    async save(event) {
        event.preventDefault();
        const form = event.currentTarget;
        const readSeconds = (name) => {
            const value = Number.parseInt(form.elements[name]?.value || '0', 10);
            return Number.isFinite(value) && value > 0 ? value : 0;
        };

        try {
            await api.updateConfig({
                failover: {
                    recoveredEndpointPolicy: form.elements.recoveredEndpointPolicy.value,
                    cooldowns: {
                        quotaExhaustedSec: readSeconds('quotaExhaustedSec'),
                        rateLimitedSec: readSeconds('rateLimitedSec'),
                        upstreamErrorSec: readSeconds('upstreamErrorSec'),
                        networkErrorSec: readSeconds('networkErrorSec'),
                        tokenUnavailableSec: readSeconds('tokenUnavailableSec'),
                        configErrorSec: readSeconds('configErrorSec')
                    },
                    circuitBreaker: this.currentFailover?.circuitBreaker || defaultFailover.circuitBreaker
                }
            });
            notifications.success(t('settings.saved'));
            await this.load();
        } catch (error) {
            notifications.error(`${t('settings.failedToSave')}: ${error.message}`);
        }
    }

    normalizeFailover(failover) {
        const cooldowns = failover?.cooldowns || {};
        const circuitBreaker = failover?.circuitBreaker || {};
        return {
            recoveredEndpointPolicy: failover?.recoveredEndpointPolicy || defaultFailover.recoveredEndpointPolicy,
            cooldowns: {
                quotaExhaustedSec: Number.isFinite(Number(cooldowns.quotaExhaustedSec)) ? Number(cooldowns.quotaExhaustedSec) : defaultFailover.cooldowns.quotaExhaustedSec,
                rateLimitedSec: Number.isFinite(Number(cooldowns.rateLimitedSec)) ? Number(cooldowns.rateLimitedSec) : defaultFailover.cooldowns.rateLimitedSec,
                upstreamErrorSec: Number.isFinite(Number(cooldowns.upstreamErrorSec)) ? Number(cooldowns.upstreamErrorSec) : defaultFailover.cooldowns.upstreamErrorSec,
                networkErrorSec: Number.isFinite(Number(cooldowns.networkErrorSec)) ? Number(cooldowns.networkErrorSec) : defaultFailover.cooldowns.networkErrorSec,
                tokenUnavailableSec: Number.isFinite(Number(cooldowns.tokenUnavailableSec)) ? Number(cooldowns.tokenUnavailableSec) : defaultFailover.cooldowns.tokenUnavailableSec,
                configErrorSec: Number.isFinite(Number(cooldowns.configErrorSec)) ? Number(cooldowns.configErrorSec) : defaultFailover.cooldowns.configErrorSec
            },
            circuitBreaker: {
                consecutiveFailures: Number.isFinite(Number(circuitBreaker.consecutiveFailures)) ? Number(circuitBreaker.consecutiveFailures) : defaultFailover.circuitBreaker.consecutiveFailures,
                windowSec: Number.isFinite(Number(circuitBreaker.windowSec)) ? Number(circuitBreaker.windowSec) : defaultFailover.circuitBreaker.windowSec,
                failureRateThreshold: Number.isFinite(Number(circuitBreaker.failureRateThreshold)) ? Number(circuitBreaker.failureRateThreshold) : defaultFailover.circuitBreaker.failureRateThreshold,
                minRequests: Number.isFinite(Number(circuitBreaker.minRequests)) ? Number(circuitBreaker.minRequests) : defaultFailover.circuitBreaker.minRequests,
                cooldownSec: Number.isFinite(Number(circuitBreaker.cooldownSec)) ? Number(circuitBreaker.cooldownSec) : defaultFailover.circuitBreaker.cooldownSec
            }
        };
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text || '';
        return div.innerHTML;
    }
}

export const settings = new Settings();
