import { t } from '../i18n/index.js';
import { showConfirm, showNotification } from './modal.js';
import {
    appendAgentMessage,
    appendAgentMessageToSession,
    appendAgentTaskToQueue,
    createAgentSession,
    deleteAgentSession,
    hasAgentRepairIntent,
    loadAgentSessions,
    parseAgentSlashCommand,
    saveAgentSessions,
    shiftNextAgentTask,
} from './agentConversation.js';

const DEFAULT_TARGETS = ['codex', 'openclaw', 'hermes'];

let agentSessions = [];
let activeSessionId = '';
let agentTaskQueue = [];
let agentDraftsBySessionId = {};
const pendingSessionIds = new Set();

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

function formatTime(value) {
    if (!value) return '';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    return date.toLocaleString();
}

function ensureSessionsLoaded() {
    if (agentSessions.length) {
        return;
    }
    agentSessions = loadAgentSessions();
    if (!agentSessions.length) {
        agentSessions = [createAgentSession()];
    }
    activeSessionId = agentSessions[0].id;
}

function persistSessions() {
    saveAgentSessions(agentSessions);
}

function getActiveSession() {
    ensureSessionsLoaded();
    let session = agentSessions.find(item => item.id === activeSessionId);
    if (!session) {
        session = agentSessions[0] || createAgentSession();
        activeSessionId = session.id;
        if (!agentSessions.length) {
            agentSessions = [session];
            persistSessions();
        }
    }
    return session;
}

function upsertActiveSession(session) {
    const existing = agentSessions.filter(item => item.id !== session.id);
    agentSessions = [session, ...existing]
        .sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime())
        .slice(0, 30);
    activeSessionId = session.id;
    persistSessions();
}

function appendToActiveSession(message) {
    const updated = appendAgentMessage(getActiveSession(), message);
    upsertActiveSession(updated);
    return updated;
}

function appendToSession(sessionId, message) {
    agentSessions = appendAgentMessageToSession(agentSessions, sessionId, message)
        .sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime())
        .slice(0, 30);
    persistSessions();
    return agentSessions.find(session => session.id === sessionId);
}

function setAgentDraft(sessionId, value) {
    const normalizedSessionId = String(sessionId || '').trim();
    if (!normalizedSessionId) {
        return;
    }
    const draft = String(value || '');
    if (draft) {
        agentDraftsBySessionId = {
            ...agentDraftsBySessionId,
            [normalizedSessionId]: draft,
        };
        return;
    }
    const { [normalizedSessionId]: _discarded, ...remaining } = agentDraftsBySessionId;
    agentDraftsBySessionId = remaining;
}

function captureVisibleComposerDraft() {
    const input = document.getElementById('agentPrompt');
    if (!input) {
        return;
    }
    setAgentDraft(input.dataset.sessionId, input.value);
}

function focusAgentPrompt() {
    const focus = () => {
        const input = document.getElementById('agentPrompt');
        if (input) {
            input.focus();
        }
    };
    if (typeof requestAnimationFrame === 'function') {
        requestAnimationFrame(focus);
        return;
    }
    setTimeout(focus, 0);
}

function startNewAgentChat(options = {}) {
    let session = createAgentSession();
    if (options.notice) {
        session = appendAgentMessage(session, {
            role: 'assistant',
            content: t('agent.contextResetNotice'),
        });
    }
    agentSessions = [session, ...agentSessions].slice(0, 30);
    activeSessionId = session.id;
    persistSessions();
    renderAgentModal();
    if (options.focusPrompt) {
        focusAgentPrompt();
    }
    return session;
}

function agentErrorLabel(error) {
    if (error === 'no_enabled_endpoints') {
        return t('agent.noEnabledEndpoints');
    }
    if (error === 'no_task') {
        return t('agent.noTask');
    }
    return error || t('agent.unknownError');
}

function assistantMessageFromResult(result) {
    const answer = String(result?.answer || '').trim();
    if (answer) {
        return answer;
    }

    const summaries = Array.isArray(result?.toolResults)
        ? result.toolResults.map(tool => String(tool.summary || '').trim()).filter(Boolean)
        : [];
    if (summaries.length && result?.error) {
        return `${summaries.join('\n')}\n\n${agentErrorLabel(result.error)}`;
    }
    if (summaries.length) {
        return summaries.join('\n');
    }
    if (result?.error) {
        return agentErrorLabel(result.error);
    }
    return t('agent.toolOnlyAnswer');
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
                            <strong>${escapeHtml(target.label || target.target || '-')}</strong>
                            <span>${escapeHtml(label)}</span>
                        </div>
                        ${target.path ? `<code title="${escapeHtml(target.path)}">${escapeHtml(target.path)}</code>` : ''}
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
                <strong>${escapeHtml(tool?.summary || tool?.tool || t('agent.tools'))}</strong>
                <span>${escapeHtml(tool?.status || '')}</span>
            </div>
            ${backupId ? `<div class="agent-backup-line"><span>${t('agent.backupId')}</span><code>${escapeHtml(backupId)}</code></div>` : ''}
            ${renderConfigTargets(targets)}
        </div>
    `;
}

function renderEvents(events = []) {
    if (!events.length) return '';
    return `
        <div class="agent-events">
            ${events.map(event => `
                <div class="agent-event-row">
                    <strong>${escapeHtml(event.type || '-')}</strong>
                    <span>${escapeHtml(event.message || '')}</span>
                </div>
            `).join('')}
        </div>
    `;
}

function renderMessageDetails(message) {
    const result = message?.result;
    if (!result) {
        return '';
    }
    const tools = Array.isArray(result.toolResults) ? result.toolResults : [];
    const events = Array.isArray(result.events) ? result.events : [];
    return `
        <details class="agent-message-details">
            <summary>${t('agent.details')}</summary>
            <div class="agent-summary">
                <div>
                    <span>${t('agent.endpoint')}</span>
                    <code>${escapeHtml(result.endpointUrl || '-')}</code>
                </div>
                <div>
                    <span>${t('agent.currentEndpoint')}</span>
                    <code>${escapeHtml(result.currentEndpoint || '-')}</code>
                </div>
            </div>
            ${tools.length ? `<div class="agent-tool-results">${tools.map(renderToolResult).join('')}</div>` : ''}
            ${events.length ? renderEvents(events) : ''}
        </details>
    `;
}

function renderMessages(session) {
    if (!session.messages.length) {
        return `
            <div class="agent-empty-chat">
                <div class="agent-message assistant">
                    <div class="agent-message-content">${escapeHtml(t('agent.emptyChat'))}</div>
                </div>
            </div>
        `;
    }
    return session.messages.map(message => `
        <article class="agent-message ${escapeHtml(message.role)}">
            <div class="agent-message-meta">
                <span>${message.role === 'user' ? t('agent.you') : t('agent.assistant')}</span>
                <time>${escapeHtml(formatTime(message.createdAt))}</time>
            </div>
            <div class="agent-message-content">${escapeHtml(message.content)}</div>
            ${renderMessageDetails(message)}
        </article>
    `).join('');
}

function renderHistory() {
    ensureSessionsLoaded();
    return agentSessions.map(session => `
        <div class="agent-history-row ${session.id === activeSessionId ? 'active' : ''}">
            <button class="agent-history-item" type="button" data-session-id="${escapeHtml(session.id)}" onclick="window.selectAgentSession(this.dataset.sessionId)">
                <span>${escapeHtml(session.title)}</span>
                <small>${escapeHtml(formatTime(session.updatedAt))}</small>
            </button>
            <button class="agent-history-delete" type="button" data-session-id="${escapeHtml(session.id)}" title="${escapeHtml(t('agent.deleteChat'))}" aria-label="${escapeHtml(t('agent.deleteChat'))}" onclick="event.stopPropagation(); window.deleteAgentChat(this.dataset.sessionId)">
                <span aria-hidden="true">&times;</span>
            </button>
        </div>
    `).join('');
}

function renderAgentModal() {
    const host = document.getElementById('agentModalHost');
    if (!host) return;
    captureVisibleComposerDraft();
    const session = getActiveSession();
    const isCurrentSessionPending = pendingSessionIds.has(session.id);
    const draft = agentDraftsBySessionId[session.id] || '';
    host.innerHTML = `
        <div id="agentModal" class="modal active">
            <div class="modal-content agent-modal">
                <div class="modal-header">
                    <h2>${t('agent.title')}</h2>
                    <div class="agent-header-actions">
                        <button class="btn btn-secondary" type="button" onclick="window.newAgentChat()">${t('agent.newChat')}</button>
                        <button class="modal-close" onclick="window.closeAgentModal()">&times;</button>
                    </div>
                </div>
                <div class="modal-body agent-body">
                    <aside class="agent-sidebar">
                        <div class="agent-sidebar-title">${t('agent.history')}</div>
                        <div id="agentHistory" class="agent-history">
                            ${renderHistory()}
                        </div>
                        <div class="agent-local-note">${t('agent.localSaved')}</div>
                    </aside>
                    <section class="agent-chat">
                        <div id="agentTranscript" class="agent-transcript">
                            ${renderMessages(session)}
                            ${isCurrentSessionPending ? `
                                <article class="agent-message assistant pending">
                                    <div class="agent-message-content">${escapeHtml(t('agent.thinking'))}</div>
                                </article>
                            ` : ''}
                        </div>
                        <div class="agent-composer">
                            <textarea id="agentPrompt" class="agent-prompt-input" data-session-id="${escapeHtml(session.id)}" placeholder="${escapeHtml(t('agent.taskPlaceholder'))}" oninput="window.handleAgentPromptInput(this.dataset.sessionId, this.value)" onkeydown="window.handleAgentComposerKeydown(event)">${escapeHtml(draft)}</textarea>
                            <button id="agentRunButton" class="btn btn-primary" type="button" onclick="window.runAgent()">${t('agent.send')}</button>
                        </div>
                    </section>
                </div>
            </div>
        </div>
    `;
    scrollTranscriptToBottom();
}

function scrollTranscriptToBottom() {
    const transcript = document.getElementById('agentTranscript');
    if (transcript) {
        transcript.scrollTop = transcript.scrollHeight;
    }
}

async function runAgentTaskForSession(sessionId, task, options = {}) {
    try {
        const payload = { task };
        if (options.repairTargets || hasAgentRepairIntent(task)) {
            payload.repairTargets = options.repairTargets || DEFAULT_TARGETS;
        }
        const raw = await window.go.main.App.RunAgent(JSON.stringify(payload));
        const result = JSON.parse(raw);
        appendToSession(sessionId, {
            role: 'assistant',
            content: assistantMessageFromResult(result),
            result,
        });
        if (!result.success && !(Array.isArray(result.toolResults) && result.toolResults.length)) {
            showNotification(tt('runFailed', { error: result.error || 'unknown' }), 'error');
        }
    } catch (error) {
        const result = { success: false, error: error.message, toolResults: [], events: [] };
        appendToSession(sessionId, {
            role: 'assistant',
            content: tt('runFailed', { error: error.message }),
            result,
        });
        showNotification(tt('runFailed', { error: error.message }), 'error');
    }
}

async function processAgentQueueForSession(sessionId) {
    if (pendingSessionIds.has(sessionId)) {
        return;
    }
    pendingSessionIds.add(sessionId);
    renderAgentModal();
    try {
        while (true) {
            const next = shiftNextAgentTask(agentTaskQueue, sessionId);
            agentTaskQueue = next.queue;
            if (!next.task) {
                break;
            }
            await runAgentTaskForSession(sessionId, next.task.task, next.task.options);
            renderAgentModal();
        }
    } finally {
        pendingSessionIds.delete(sessionId);
        renderAgentModal();
    }
}

async function runAgentTask(task, options = {}) {
    const requestSession = appendToActiveSession({ role: 'user', content: task });
    const requestSessionId = requestSession.id;
    setAgentDraft(requestSessionId, '');
    agentTaskQueue = appendAgentTaskToQueue(agentTaskQueue, requestSessionId, { task, options });
    renderAgentModal();
    processAgentQueueForSession(requestSessionId);
}

export function showAgentModal() {
    ensureSessionsLoaded();
    let container = document.getElementById('agentModalHost');
    if (!container) {
        container = document.createElement('div');
        container.id = 'agentModalHost';
        document.body.appendChild(container);
    }
    renderAgentModal();
}

export function closeAgentModal() {
    const host = document.getElementById('agentModalHost');
    if (host) host.innerHTML = '';
}

export function newAgentChat() {
    startNewAgentChat({ focusPrompt: true });
}

export function selectAgentSession(sessionId) {
    ensureSessionsLoaded();
    if (agentSessions.some(session => session.id === sessionId)) {
        activeSessionId = sessionId;
        renderAgentModal();
    }
}

export async function deleteAgentChat(sessionId) {
    ensureSessionsLoaded();
    const targetSessionId = String(sessionId || '').trim();
    if (!targetSessionId || !agentSessions.some(session => session.id === targetSessionId)) {
        return;
    }
    const confirmed = await showConfirm(t('agent.confirmDeleteChat'));
    if (!confirmed) {
        return;
    }

    agentSessions = deleteAgentSession(agentSessions, targetSessionId);
    setAgentDraft(targetSessionId, '');
    agentTaskQueue = agentTaskQueue.filter(item => item?.sessionId !== targetSessionId);
    pendingSessionIds.delete(targetSessionId);

    if (!agentSessions.length) {
        agentSessions = [createAgentSession()];
    }
    if (activeSessionId === targetSessionId || !agentSessions.some(session => session.id === activeSessionId)) {
        activeSessionId = agentSessions[0].id;
    }
    persistSessions();
    renderAgentModal();
}

export function handleAgentComposerKeydown(event) {
    if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault();
        runAgent();
    }
}

export function handleAgentPromptInput(sessionId, value) {
    setAgentDraft(sessionId, value);
}

export async function runAgent() {
    const input = document.getElementById('agentPrompt');
    const task = input?.value?.trim() || '';
    if (!task) {
        showNotification(t('agent.noTask'), 'warning');
        return;
    }
    if (input) {
        setAgentDraft(input.dataset.sessionId, '');
        input.value = '';
    }
    const command = parseAgentSlashCommand(task);
    if (command === 'new_chat') {
        startNewAgentChat({ notice: true, focusPrompt: true });
        return;
    }
    await runAgentTask(task);
}

export async function checkAgentConfigs() {
    await runAgentTask(t('agent.defaultCheckTask'));
}

export async function repairAgentConfigs() {
    await runAgentTask(t('agent.defaultRepairTask'), { repairTargets: DEFAULT_TARGETS });
}
