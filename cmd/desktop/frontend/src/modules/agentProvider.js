import { t } from '../i18n/index.js';
import { showNotification } from './modal.js';

const TARGET_ORDER = ['claude', 'claude_desktop', 'codex', 'gemini', 'opencode', 'openclaw', 'hermes'];
let currentStatus = null;

function tt(key, params = {}) {
    let value = t(`agentProvider.${key}`);
    Object.entries(params).forEach(([name, replacement]) => {
        value = String(value).replace(`{${name}}`, replacement);
    });
    return value;
}

function escapeHtml(value) {
    return String(value ?? '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#039;');
}

function getSelectedTargets() {
    return Array.from(document.querySelectorAll('#agentProviderTargets input[type="checkbox"]:checked'))
        .map(input => input.value);
}

function renderTargetRows(status) {
    const targets = Array.isArray(status?.targets) ? status.targets : [];
    const sorted = [...targets].sort((a, b) => TARGET_ORDER.indexOf(a.target) - TARGET_ORDER.indexOf(b.target));
    return sorted.map(target => `
        <label class="agent-provider-target ${target.detected ? 'detected' : 'missing'}">
            <input type="checkbox" value="${escapeHtml(target.target)}" ${target.detected ? 'checked' : ''}>
            <span class="agent-provider-main">
                <strong>${escapeHtml(target.label)}</strong>
                <small title="${escapeHtml(target.path)}">${escapeHtml(target.path)}</small>
            </span>
            <span class="agent-provider-status ${target.detected ? 'ok' : 'muted'}">
                ${target.detected ? tt('detected') : tt('missing')}
            </span>
        </label>
    `).join('');
}

function renderResults(results = []) {
    const el = document.getElementById('agentProviderResults');
    if (!el) return;
    if (!results.length) {
        el.innerHTML = '';
        return;
    }
    el.innerHTML = `
        <div class="agent-provider-results">
            ${results.map(result => `
                <div class="agent-provider-result ${escapeHtml(result.status)}">
                    <strong>${escapeHtml(result.label || result.target || '-')}</strong>
                    <span>${escapeHtml(tt(`status.${result.status}`) || result.status)}</span>
                    ${result.message ? `<small>${escapeHtml(result.message)}</small>` : ''}
                </div>
            `).join('')}
        </div>
    `;
}

function renderModal(status) {
    currentStatus = status;
    const latestBackup = status?.latestBackup;
    return `
        <div id="agentProviderModal" class="modal active">
            <div class="modal-content agent-provider-modal">
                <div class="modal-header">
                    <h2>🔀 ${t('agentProvider.title')}</h2>
                    <button class="modal-close" onclick="window.closeAgentProviderModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="agent-provider-summary">
                        <span>${t('agentProvider.targetUrl')}</span>
                        <code>${escapeHtml(status?.targetUrl || '')}</code>
                    </div>
                    <div class="agent-provider-actions-row">
                        <button class="btn btn-secondary btn-sm" onclick="window.selectAllAgentProviders(true)">${t('agentProvider.selectAll')}</button>
                        <button class="btn btn-secondary btn-sm" onclick="window.selectAllAgentProviders(false)">${t('agentProvider.clearAll')}</button>
                        <label class="agent-provider-create">
                            <input type="checkbox" id="agentProviderCreateMissing">
                            ${t('agentProvider.createMissing')}
                        </label>
                    </div>
                    <div id="agentProviderTargets" class="agent-provider-targets">
                        ${renderTargetRows(status)}
                    </div>
                    <div class="agent-provider-backup">
                        <span>${t('agentProvider.latestBackup')}</span>
                        <code>${latestBackup ? escapeHtml(latestBackup.id) : t('agentProvider.noBackup')}</code>
                    </div>
                    <div id="agentProviderResults"></div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="window.closeAgentProviderModal()">${t('common.close')}</button>
                    <button class="btn btn-secondary" ${latestBackup ? '' : 'disabled'} onclick="window.restoreAgentProviderBackup()">${t('agentProvider.restore')}</button>
                    <button class="btn btn-primary" onclick="window.applyAgentProviderConfig()">${t('agentProvider.apply')}</button>
                </div>
            </div>
        </div>
    `;
}

export async function showAgentProviderModal() {
    try {
        const raw = await window.go.main.App.GetAgentProviderStatus();
        const status = JSON.parse(raw);
        let container = document.getElementById('agentProviderModalHost');
        if (!container) {
            container = document.createElement('div');
            container.id = 'agentProviderModalHost';
            document.body.appendChild(container);
        }
        container.innerHTML = renderModal(status);
    } catch (error) {
        console.error('Failed to load agent provider status:', error);
        showNotification(tt('loadFailed', { error: error.message }), 'error');
    }
}

export function closeAgentProviderModal() {
    const host = document.getElementById('agentProviderModalHost');
    if (host) host.innerHTML = '';
}

export function selectAllAgentProviders(checked) {
    document.querySelectorAll('#agentProviderTargets input[type="checkbox"]').forEach(input => {
        input.checked = !!checked;
    });
}

export async function applyAgentProviderConfig() {
    const targets = getSelectedTargets();
    if (!targets.length) {
        showNotification(t('agentProvider.noSelection'), 'warning');
        return;
    }
    try {
        const createMissing = !!document.getElementById('agentProviderCreateMissing')?.checked;
        const raw = await window.go.main.App.ApplyAgentProviderConfig(JSON.stringify({ targets, createMissing }));
        const result = JSON.parse(raw);
        renderResults(result.results);
        showNotification(t('agentProvider.applyComplete'), 'success');
        await refreshAgentProviderModal();
    } catch (error) {
        showNotification(tt('applyFailed', { error: error.message }), 'error');
    }
}

export async function restoreAgentProviderBackup() {
    const backupID = currentStatus?.latestBackup?.id || '';
    if (!backupID) {
        showNotification(t('agentProvider.noBackup'), 'warning');
        return;
    }
    const targets = getSelectedTargets();
    try {
        const raw = await window.go.main.App.RestoreAgentProviderBackup(backupID, JSON.stringify({ targets }));
        const result = JSON.parse(raw);
        renderResults(result.results);
        showNotification(t('agentProvider.restoreComplete'), 'success');
        await refreshAgentProviderModal();
    } catch (error) {
        showNotification(tt('restoreFailed', { error: error.message }), 'error');
    }
}

async function refreshAgentProviderModal() {
    const raw = await window.go.main.App.GetAgentProviderStatus();
    currentStatus = JSON.parse(raw);
    const host = document.getElementById('agentProviderModalHost');
    if (host) {
        const previousResults = document.getElementById('agentProviderResults')?.innerHTML || '';
        host.innerHTML = renderModal(currentStatus);
        const resultEl = document.getElementById('agentProviderResults');
        if (resultEl) resultEl.innerHTML = previousResults;
    }
}
