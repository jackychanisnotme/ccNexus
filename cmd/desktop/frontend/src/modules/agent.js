import { t } from '../i18n/index.js';
import { showNotification } from './modal.js';

let lastResult = null;

const DEFAULT_TARGETS = ['codex', 'openclaw', 'hermes'];

function tt(key, params = {}) {
    let value = t(`agent.${key}`);
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

function selectedTargets() {
    const values = Array.from(document.querySelectorAll('#agentTargets input[type="checkbox"]:checked'))
        .map(input => input.value);
    return values.length ? values : DEFAULT_TARGETS;
}

function renderTargets() {
    return DEFAULT_TARGETS.map(target => `
        <label class="agent-target-choice">
            <input type="checkbox" value="${target}" checked>
            <span>${target}</span>
        </label>
    `).join('');
}

function renderEvents(events = []) {
    if (!events.length) return '';
    return `
        <div class="agent-section">
            <h3>${t('agent.events')}</h3>
            <div class="agent-events">
                ${events.map(event => `
                    <div class="agent-event-row">
                        <strong>${escapeHtml(event.type || '-')}</strong>
                        <span>${escapeHtml(event.message || '')}</span>
                    </div>
                `).join('')}
            </div>
        </div>
    `;
}

function renderConfigTargets(targets = []) {
    if (!targets.length) return '';
    return `
        <div class="agent-config-grid">
            ${targets.map(target => {
                const state = target.healthy ? 'healthy' : (target.status || 'unhealthy');
                const label = target.healthy ? t('agent.healthy') : target.status === 'missing' ? t('agent.missing') : t('agent.unhealthy');
                return `
                    <div class="agent-target-row ${escapeHtml(state)}">
                        <div class="agent-target-row-head">
                            <strong>${escapeHtml(target.label || target.target)}</strong>
                            <span>${escapeHtml(label)}</span>
                        </div>
                        <code title="${escapeHtml(target.path || '')}">${escapeHtml(target.path || '')}</code>
                        ${Array.isArray(target.problems) && target.problems.length ? `
                            <ul>
                                ${target.problems.map(problem => `<li>${escapeHtml(problem)}</li>`).join('')}
                            </ul>
                        ` : ''}
                    </div>
                `;
            }).join('')}
        </div>
    `;
}

function renderToolResult(tool) {
    const data = tool?.data || {};
    const targets = Array.isArray(data.targets) ? data.targets : [];
    const backupId = data.backupId || '';
    return `
        <div class="agent-result-card ${escapeHtml(tool?.status || '')}">
            <div class="agent-result-card-head">
                <strong>${escapeHtml(tool?.tool || t('agent.tools'))}</strong>
                <span>${escapeHtml(tool?.status || '')}</span>
            </div>
            <p>${escapeHtml(tool?.summary || '')}</p>
            ${backupId ? `<div class="agent-backup-line"><span>${t('agent.backupId')}</span><code>${escapeHtml(backupId)}</code></div>` : ''}
            ${renderConfigTargets(targets)}
        </div>
    `;
}

function renderAgentResult(result) {
    const el = document.getElementById('agentResults');
    if (!el) return;
    lastResult = result;
    const tools = Array.isArray(result?.toolResults) ? result.toolResults : [];
    el.innerHTML = `
        <div class="agent-summary">
            <div>
                <span>${t('agent.endpoint')}</span>
                <code>${escapeHtml(result?.endpointUrl || '-')}</code>
            </div>
            <div>
                <span>${t('agent.currentEndpoint')}</span>
                <code>${escapeHtml(result?.currentEndpoint || '-')}</code>
            </div>
        </div>
        ${result?.answer ? `
            <div class="agent-section">
                <h3>${t('agent.answer')}</h3>
                <div class="agent-answer">${escapeHtml(result.answer)}</div>
            </div>
        ` : ''}
        ${result?.error ? `
            <div class="agent-error">${escapeHtml(result.error)}</div>
        ` : ''}
        ${tools.length ? `
            <div class="agent-section">
                <h3>${t('agent.tools')}</h3>
                <div class="agent-tool-results">
                    ${tools.map(renderToolResult).join('')}
                </div>
            </div>
        ` : ''}
        ${renderEvents(result?.events || [])}
    `;
}

function renderInspectStatus(status) {
    const el = document.getElementById('agentResults');
    if (!el) return;
    el.innerHTML = `
        <div class="agent-summary">
            <div>
                <span>${t('agent.endpoint')}</span>
                <code>${escapeHtml(status?.targetUrl || '-')}</code>
            </div>
        </div>
        <div class="agent-section">
            <h3>${t('agent.checkConfigs')}</h3>
            ${renderConfigTargets(status?.targets || [])}
        </div>
    `;
}

function renderRepairResult(result) {
    const el = document.getElementById('agentResults');
    if (!el) return;
    const backupId = result?.backupId || '';
    el.innerHTML = `
        <div class="agent-result-card success">
            <div class="agent-result-card-head">
                <strong>${t('agent.repairComplete')}</strong>
                <span>${escapeHtml(result?.targetUrl || '')}</span>
            </div>
            ${backupId ? `<div class="agent-backup-line"><span>${t('agent.backupId')}</span><code>${escapeHtml(backupId)}</code></div>` : ''}
            <div class="agent-provider-results">
                ${(result?.results || []).map(item => `
                    <div class="agent-provider-result ${escapeHtml(item.status)}">
                        <strong>${escapeHtml(item.label || item.target || '-')}</strong>
                        <span>${escapeHtml(item.status || '')}</span>
                        <small>${escapeHtml(item.message || item.path || '')}</small>
                    </div>
                `).join('')}
            </div>
        </div>
    `;
}

function setBusy(isBusy) {
    ['agentRunButton', 'agentCheckButton', 'agentRepairButton'].forEach(id => {
        const btn = document.getElementById(id);
        if (btn) btn.disabled = isBusy;
    });
}

export function showAgentModal() {
    let container = document.getElementById('agentModalHost');
    if (!container) {
        container = document.createElement('div');
        container.id = 'agentModalHost';
        document.body.appendChild(container);
    }
    container.innerHTML = `
        <div id="agentModal" class="modal active">
            <div class="modal-content agent-modal">
                <div class="modal-header">
                    <h2>${t('agent.title')}</h2>
                    <button class="modal-close" onclick="window.closeAgentModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <textarea id="agentTask" class="agent-task-input" placeholder="${escapeHtml(t('agent.taskPlaceholder'))}"></textarea>
                    <div class="agent-targets" id="agentTargets">
                        ${renderTargets()}
                    </div>
                    <div class="agent-actions">
                        <button id="agentCheckButton" class="btn btn-secondary" onclick="window.checkAgentConfigs()">${t('agent.checkConfigs')}</button>
                        <button id="agentRepairButton" class="btn btn-secondary" onclick="window.repairAgentConfigs()">${t('agent.repairConfigs')}</button>
                        <button id="agentRunButton" class="btn btn-primary" onclick="window.runAgent()">${t('agent.run')}</button>
                    </div>
                    <div id="agentResults" class="agent-results">
                        ${lastResult ? '' : `<div class="empty-state">${escapeHtml(t('agent.taskPlaceholder'))}</div>`}
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="window.closeAgentModal()">${t('common.close')}</button>
                </div>
            </div>
        </div>
    `;
    if (lastResult) renderAgentResult(lastResult);
}

export function closeAgentModal() {
    const host = document.getElementById('agentModalHost');
    if (host) host.innerHTML = '';
}

export async function runAgent() {
    const task = document.getElementById('agentTask')?.value?.trim() || '';
    if (!task) {
        showNotification(t('agent.noTask'), 'warning');
        return;
    }
    setBusy(true);
    try {
        const payload = { task };
        if (/repair|fix|修复|修理|修正/i.test(task)) {
            payload.repairTargets = selectedTargets();
        }
        const raw = await window.go.main.App.RunAgent(JSON.stringify(payload));
        const result = JSON.parse(raw);
        renderAgentResult(result);
        if (!result.success) {
            showNotification(tt('runFailed', { error: result.error || 'unknown' }), 'error');
        }
    } catch (error) {
        showNotification(tt('runFailed', { error: error.message }), 'error');
    } finally {
        setBusy(false);
    }
}

export async function checkAgentConfigs() {
    setBusy(true);
    try {
        const raw = await window.go.main.App.CheckAgentConfigs(JSON.stringify({ targets: selectedTargets() }));
        renderInspectStatus(JSON.parse(raw));
    } catch (error) {
        showNotification(tt('runFailed', { error: error.message }), 'error');
    } finally {
        setBusy(false);
    }
}

export async function repairAgentConfigs() {
    setBusy(true);
    try {
        const raw = await window.go.main.App.RepairAgentConfigs(JSON.stringify({
            targets: selectedTargets(),
            createMissing: true
        }));
        const result = JSON.parse(raw);
        renderRepairResult(result);
        showNotification(t('agent.repairComplete'), 'success');
    } catch (error) {
        showNotification(tt('runFailed', { error: error.message }), 'error');
    } finally {
        setBusy(false);
    }
}
