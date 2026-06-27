import { GetSessions, DeleteSession, RenameSession, GetSessionData, GetCodexSessions, GetAllCodexSessions, GetCodexSessionData, DeleteCodexSession, RenameCodexSession, RepairCodexSessionVisibility } from '../../wailsjs/go/main/App';
import { t } from '../i18n/index.js';
import { showNotification } from './modal.js';
import { parseMarkdown } from '../utils/markdown.js';
import { getCurrentCliType } from './terminal.js';

let currentProjectDir = '';
let sessions = [];
let selectedSessions = {}; // 按目录存储已确认选中的会话
let tempSelectedSession = null; // 临时选中的会话（未确认）
let codexVisibilityRepairMode = 'quick';
let codexVisibilityRepairSessionScope = 'all';
let codexVisibilityRepairRunning = false;
let codexVisibilityRepairResult = null;
let codexVisibilityRepairSelectedSessionIds = [];
let codexVisibilityRepairPickerSessions = [];
let codexVisibilityRepairPickerQuery = '';
let codexVisibilityRepairPickerLoading = false;
let codexVisibilityRepairPickerError = '';

export function initSession() {
    window.showSessionModal = showSessionModal;
    window.closeSessionModal = closeSessionModal;
    window.selectSession = selectSession;
    window.confirmSessionSelection = confirmSessionSelection;
    window.deleteSession = deleteSession;
    window.renameSession = renameSession;
    window.viewSessionDetail = viewSessionDetail;
    window.closeSessionDetailModal = closeSessionDetailModal;
    window.showCodexVisibilityRepairModal = showCodexVisibilityRepairModal;
    window.closeCodexVisibilityRepairModal = closeCodexVisibilityRepairModal;
    window.setCodexVisibilityRepairMode = setCodexVisibilityRepairMode;
    window.setCodexVisibilityRepairSessionScope = setCodexVisibilityRepairSessionScope;
    window.startCodexVisibilityRepair = startCodexVisibilityRepair;
    window.openCodexVisibilitySessionPicker = openCodexVisibilitySessionPicker;
    window.closeCodexVisibilitySessionPicker = closeCodexVisibilitySessionPicker;
    window.setCodexVisibilitySessionPickerQuery = setCodexVisibilitySessionPickerQuery;
    window.toggleCodexVisibilityPickerSession = toggleCodexVisibilityPickerSession;
    window.confirmCodexVisibilitySessionPicker = confirmCodexVisibilitySessionPicker;
}

// 获取选中的会话
export function getSelectedSession(dir) {
    return selectedSessions[dir] || null;
}

// 清除选中的会话
export function clearSelectedSession(dir) {
    if (dir) {
        delete selectedSessions[dir];
    } else if (currentProjectDir) {
        delete selectedSessions[currentProjectDir];
    }
}

// 清除所有已选会话
export function clearAllSelectedSessions() {
    for (const key in selectedSessions) {
        delete selectedSessions[key];
    }
}

export function showCodexVisibilityRepairModal() {
    codexVisibilityRepairMode = 'quick';
    codexVisibilityRepairSessionScope = 'all';
    codexVisibilityRepairRunning = false;
    codexVisibilityRepairResult = null;
    codexVisibilityRepairSelectedSessionIds = [];
    codexVisibilityRepairPickerSessions = [];
    codexVisibilityRepairPickerQuery = '';
    codexVisibilityRepairPickerError = '';
    renderCodexVisibilityRepairModal();
}

function closeCodexVisibilityRepairModal() {
    if (codexVisibilityRepairRunning) return;
    closeCodexVisibilitySessionPicker();
    const modal = document.getElementById('codexVisibilityRepairModal');
    if (modal) {
        modal.classList.remove('active');
        setTimeout(() => modal.remove(), 200);
    }
}

function setCodexVisibilityRepairMode(mode) {
    if (codexVisibilityRepairRunning) return;
    codexVisibilityRepairMode = mode === 'deep' ? 'deep' : 'quick';
    renderCodexVisibilityRepairModal();
}

function setCodexVisibilityRepairSessionScope(scope) {
    if (codexVisibilityRepairRunning) return;
    codexVisibilityRepairSessionScope = scope === 'selected' && codexVisibilityRepairSelectedSessionIds.length > 0 ? 'selected' : 'all';
    renderCodexVisibilityRepairModal();
}

function renderCodexVisibilityRepairModal() {
    let modal = document.getElementById('codexVisibilityRepairModal');
    if (!modal) {
        modal = document.createElement('div');
        modal.id = 'codexVisibilityRepairModal';
        modal.className = 'modal active';
        modal.style.zIndex = '1002';
        document.body.appendChild(modal);
    }

    const selectedCount = codexVisibilityRepairSelectedSessionIds.length;
    const selectedDesc = selectedCount > 0
        ? t('session.codexRepairSelectedCount').replace('{count}', String(selectedCount))
        : t('session.codexRepairChooseSessionsDesc');
    const result = codexVisibilityRepairResult;
    const resultHTML = result ? `
        <div class="codex-visibility-repair-result ${result.success ? 'success' : 'failed'}">
            <strong>${result.success ? t('session.codexRepairDone') : t('session.codexRepairFailed')}</strong>
            <div>${escapeHtml(result.message || '')}</div>
            ${result.targetProvider ? `<div>${t('session.codexRepairTargetProvider')}: <code>${escapeHtml(result.targetProvider)}</code></div>` : ''}
            ${Number.isFinite(result.updatedSqliteRowCount) ? `<div>${t('session.codexRepairSqliteRows')}: ${result.updatedSqliteRowCount}</div>` : ''}
            ${Number.isFinite(result.changedRolloutFileCount) ? `<div>${t('session.codexRepairRolloutFiles')}: ${result.changedRolloutFileCount}</div>` : ''}
            ${result.backupDir ? `<div>${t('session.codexRepairBackupDir')}: <code>${escapeHtml(result.backupDir)}</code></div>` : ''}
        </div>
    ` : '';

    modal.innerHTML = `
        <div class="modal-content codex-visibility-repair-modal">
            <div class="modal-header">
                <h2>${t('session.codexRepairTitle')}</h2>
                <button class="modal-close" onclick="window.closeCodexVisibilityRepairModal()">&times;</button>
            </div>
            <div class="modal-body codex-visibility-repair-body">
                <p class="codex-visibility-repair-copy">${t('session.codexRepairCopy')}</p>
                <div class="codex-visibility-repair-section">
                    <div class="codex-visibility-repair-label">${t('session.codexRepairModeTitle')}</div>
                    <div class="codex-visibility-repair-grid">
                        <button class="codex-visibility-repair-card ${codexVisibilityRepairMode === 'quick' ? 'selected' : ''}" onclick="window.setCodexVisibilityRepairMode('quick')" ${codexVisibilityRepairRunning ? 'disabled' : ''}>
                            <strong>${t('session.codexRepairQuick')}</strong>
                            <span>${t('session.codexRepairQuickDesc')}</span>
                        </button>
                        <button class="codex-visibility-repair-card ${codexVisibilityRepairMode === 'deep' ? 'selected' : ''}" onclick="window.setCodexVisibilityRepairMode('deep')" ${codexVisibilityRepairRunning ? 'disabled' : ''}>
                            <strong>${t('session.codexRepairDeep')}</strong>
                            <span>${t('session.codexRepairDeepDesc')}</span>
                        </button>
                    </div>
                </div>
                <div class="codex-visibility-repair-section">
                    <div class="codex-visibility-repair-label">${t('session.codexRepairTargetInstance')}</div>
                    <div class="codex-visibility-repair-instance">${t('session.codexRepairDefaultInstance')}</div>
                </div>
                <div class="codex-visibility-repair-section">
                    <div class="codex-visibility-repair-label">${t('session.codexRepairSessionScopeTitle')}</div>
                    <div class="codex-visibility-repair-grid">
                        <button class="codex-visibility-repair-card ${codexVisibilityRepairSessionScope === 'all' ? 'selected' : ''}" onclick="window.setCodexVisibilityRepairSessionScope('all')" ${codexVisibilityRepairRunning ? 'disabled' : ''}>
                            <strong>${t('session.codexRepairAllSessions')}</strong>
                            <span>${t('session.codexRepairAllSessionsDesc')}</span>
                        </button>
                        <button class="codex-visibility-repair-card ${codexVisibilityRepairSessionScope === 'selected' ? 'selected' : ''}" onclick="window.openCodexVisibilitySessionPicker()" ${codexVisibilityRepairRunning ? 'disabled' : ''}>
                            <strong>${t('session.codexRepairChooseSessions')}</strong>
                            <span>${selectedDesc}</span>
                        </button>
                    </div>
                </div>
                ${codexVisibilityRepairRunning ? `<div class="codex-visibility-repair-running">${t('session.codexRepairRunning')}</div>` : ''}
                ${resultHTML}
            </div>
            <div class="modal-footer codex-visibility-repair-footer">
                <button class="btn btn-secondary" onclick="window.closeCodexVisibilityRepairModal()" ${codexVisibilityRepairRunning ? 'disabled' : ''}>
                    ${t('common.close')}
                </button>
                <button class="btn btn-primary" onclick="window.startCodexVisibilityRepair()" ${codexVisibilityRepairRunning ? 'disabled' : ''}>
                    ↻ ${codexVisibilityRepairRunning ? t('session.codexRepairRunningShort') : t('session.codexRepairStart')}
                </button>
            </div>
        </div>
    `;
}

async function startCodexVisibilityRepair() {
    if (codexVisibilityRepairRunning) return;
    if (codexVisibilityRepairSessionScope === 'selected' && codexVisibilityRepairSelectedSessionIds.length === 0) {
        showNotification(t('session.codexRepairSelectAtLeastOne'), 'warning');
        await openCodexVisibilitySessionPicker();
        return;
    }
    const request = {
        mode: codexVisibilityRepairMode,
        sessionScope: codexVisibilityRepairSessionScope,
        sessionIds: codexVisibilityRepairSessionScope === 'selected' ? codexVisibilityRepairSelectedSessionIds : []
    };
    codexVisibilityRepairRunning = true;
    codexVisibilityRepairResult = null;
    renderCodexVisibilityRepairModal();
    try {
        const raw = await RepairCodexSessionVisibility(JSON.stringify(request));
        const response = JSON.parse(raw);
        if (!response.success) {
            throw new Error(response.error || t('session.codexRepairFailed'));
        }
        const data = response.data || {};
        codexVisibilityRepairResult = {
            success: true,
            message: data.message || t('session.codexRepairDone'),
            targetProvider: data.targetProvider,
            updatedSqliteRowCount: data.updatedSqliteRowCount,
            changedRolloutFileCount: data.changedRolloutFileCount,
            backupDir: data.backupDir
        };
        showNotification(t('session.codexRepairDone'), 'success');
        await loadSessions();
    } catch (err) {
        console.error('Failed to repair Codex session visibility:', err);
        codexVisibilityRepairResult = {
            success: false,
            message: err?.message || t('session.codexRepairFailed')
        };
        showNotification(t('session.codexRepairFailed'), 'error');
    } finally {
        codexVisibilityRepairRunning = false;
        renderCodexVisibilityRepairModal();
    }
}

async function openCodexVisibilitySessionPicker() {
    if (codexVisibilityRepairRunning) return;
    codexVisibilityRepairPickerQuery = '';
    codexVisibilityRepairPickerError = '';
    codexVisibilityRepairPickerLoading = true;
    renderCodexVisibilitySessionPicker();
    try {
        const raw = await GetAllCodexSessions();
        const response = JSON.parse(raw);
        if (!response.success) {
            throw new Error(response.message || t('session.codexRepairLoadSessionsFailed'));
        }
        codexVisibilityRepairPickerSessions = response.sessions || [];
    } catch (err) {
        console.error('Failed to load Codex sessions for repair:', err);
        codexVisibilityRepairPickerError = err?.message || t('session.codexRepairLoadSessionsFailed');
        codexVisibilityRepairPickerSessions = [];
    } finally {
        codexVisibilityRepairPickerLoading = false;
        renderCodexVisibilitySessionPicker();
    }
}

function closeCodexVisibilitySessionPicker() {
    const modal = document.getElementById('codexVisibilitySessionPickerModal');
    if (modal) {
        modal.classList.remove('active');
        setTimeout(() => modal.remove(), 160);
    }
}

function setCodexVisibilitySessionPickerQuery(value) {
    codexVisibilityRepairPickerQuery = value || '';
    renderCodexVisibilitySessionPicker();
}

function toggleCodexVisibilityPickerSession(sessionId) {
    if (!sessionId) return;
    const selected = new Set(codexVisibilityRepairSelectedSessionIds);
    if (selected.has(sessionId)) {
        selected.delete(sessionId);
    } else {
        selected.add(sessionId);
    }
    codexVisibilityRepairSelectedSessionIds = Array.from(selected);
    renderCodexVisibilitySessionPicker();
}

function confirmCodexVisibilitySessionPicker() {
    if (codexVisibilityRepairSelectedSessionIds.length === 0) {
        showNotification(t('session.codexRepairSelectAtLeastOne'), 'warning');
        return;
    }
    codexVisibilityRepairSessionScope = 'selected';
    closeCodexVisibilitySessionPicker();
    renderCodexVisibilityRepairModal();
}

function renderCodexVisibilitySessionPicker() {
    let modal = document.getElementById('codexVisibilitySessionPickerModal');
    if (!modal) {
        modal = document.createElement('div');
        modal.id = 'codexVisibilitySessionPickerModal';
        modal.className = 'modal active';
        modal.style.zIndex = '1003';
        modal.style.background = 'transparent';
        document.body.appendChild(modal);
    }

    const query = codexVisibilityRepairPickerQuery.trim().toLowerCase();
    const filteredSessions = codexVisibilityRepairPickerSessions.filter(session => {
        if (!query) return true;
        return [session.alias, session.summary, session.sessionId, session.projectDir]
            .filter(Boolean)
            .some(value => String(value).toLowerCase().includes(query));
    });
    const selected = new Set(codexVisibilityRepairSelectedSessionIds);
    const selectedCount = selected.size;
    let bodyHTML = '';

    if (codexVisibilityRepairPickerLoading) {
        bodyHTML = `<div class="codex-visibility-picker-empty">${t('session.loading')}</div>`;
    } else if (codexVisibilityRepairPickerError) {
        bodyHTML = `<div class="codex-visibility-picker-empty">${escapeHtml(codexVisibilityRepairPickerError)}</div>`;
    } else if (filteredSessions.length === 0) {
        bodyHTML = `<div class="codex-visibility-picker-empty">${t('session.codexRepairNoSessions')}</div>`;
    } else {
        bodyHTML = filteredSessions.map(session => {
            const title = escapeHtml(session.alias || session.summary || session.sessionId || t('session.noSummary'));
            const summary = escapeHtml(session.summary || t('session.noSummary'));
            const projectDir = escapeHtml(session.projectDir || t('session.codexRepairUnknownProject'));
            const time = formatTime(session.modTime || 0);
            const size = formatSize(session.size || 0);
            const checked = selected.has(session.sessionId);
            return `
                <label class="codex-visibility-picker-item ${checked ? 'selected' : ''}">
                    <input type="checkbox" ${checked ? 'checked' : ''} onchange="window.toggleCodexVisibilityPickerSession('${escapeJsString(session.sessionId)}')" />
                    <span class="codex-visibility-picker-main">
                        <strong>${title}</strong>
                        <span>${summary}</span>
                        <code title="${projectDir}">${projectDir}</code>
                    </span>
                    <span class="codex-visibility-picker-meta">
                        <span>${time}</span>
                        <span>${size}</span>
                    </span>
                </label>
            `;
        }).join('');
    }

    modal.innerHTML = `
        <div class="modal-content codex-visibility-picker-modal">
            <div class="modal-header">
                <h2>${t('session.codexRepairPickerTitle')}</h2>
                <button class="modal-close" onclick="window.closeCodexVisibilitySessionPicker()">&times;</button>
            </div>
            <div class="modal-body codex-visibility-picker-body">
                <div class="codex-visibility-picker-toolbar">
                    <input class="form-input codex-visibility-picker-search"
                           value="${escapeHtml(codexVisibilityRepairPickerQuery)}"
                           placeholder="${t('session.codexRepairSearchSessions')}"
                           oninput="window.setCodexVisibilitySessionPickerQuery(this.value)" />
                    <div class="codex-visibility-picker-count">
                        ${t('session.codexRepairSelectedCount').replace('{count}', String(selectedCount))}
                    </div>
                </div>
                <div class="codex-visibility-picker-list">
                    ${bodyHTML}
                </div>
            </div>
            <div class="modal-footer codex-visibility-repair-footer">
                <button class="btn btn-secondary" onclick="window.closeCodexVisibilitySessionPicker()">
                    ${t('common.cancel')}
                </button>
                <button class="btn btn-primary" onclick="window.confirmCodexVisibilitySessionPicker()" ${selectedCount === 0 ? 'disabled' : ''}>
                    ${t('common.ok')}
                </button>
            </div>
        </div>
    `;
}

export async function showSessionModal(projectDir) {
    currentProjectDir = projectDir;
    // 初始化临时选择为当前已确认的选择
    tempSelectedSession = selectedSessions[projectDir] ? { ...selectedSessions[projectDir] } : null;
    const modal = document.getElementById('sessionModal');
    modal.style.display = 'flex';
    await loadSessions();
}

export function closeSessionModal() {
    document.getElementById('sessionModal').style.display = 'none';
    // 关闭时清空临时选择（不保存）
    tempSelectedSession = null;
    sessions = [];
}

async function loadSessions() {
    const listContainer = document.getElementById('sessionList');
    if (!listContainer) {
        return;
    }
    listContainer.innerHTML = `<div class="session-loading">${t('session.loading')}</div>`;

    try {
        const cliType = getCurrentCliType();
        let result;

        if (cliType === 'codex') {
            result = JSON.parse(await GetCodexSessions(currentProjectDir));
        } else {
            result = JSON.parse(await GetSessions(currentProjectDir));
        }

        if (!result.success) {
            listContainer.innerHTML = `<div class="session-empty">${t('session.loadError')}</div>`;
            return;
        }

        sessions = result.sessions || [];
        renderSessionList();
    } catch (err) {
        console.error('Failed to load sessions:', err);
        listContainer.innerHTML = `<div class="session-empty">${t('session.loadError')}</div>`;
    }
}

// HTML 转义函数
function escapeHtml(str) {
    if (!str) return '';
    return String(str).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function escapeJsString(str) {
    return String(str || '').replace(/\\/g, '\\\\').replace(/'/g, "\\'").replace(/\n/g, '\\n').replace(/\r/g, '\\r');
}

function renderSessionList() {
    const listContainer = document.getElementById('sessionList');

    if (sessions.length === 0) {
        listContainer.innerHTML = `<div class="session-empty">${t('session.noSessions')}</div>`;
        return;
    }

    // 使用临时选择状态来显示高亮
    const currentSelected = tempSelectedSession;

    listContainer.innerHTML = sessions.map((s, index) => {
        const serialNumber = index + 1; // 序号从1开始
        const time = formatTime(s.modTime);
        const fullTime = formatFullTime(s.modTime);
        const size = formatSize(s.size);
        const summary = escapeHtml(s.summary) || t('session.noSummary');
        const displaySummary = escapeHtml(s.alias) || summary;
        const tooltipTitle = escapeHtml(s.alias
            ? `${s.alias}\n${t('session.modTime')}: ${fullTime}\n${t('session.size')}: ${size}\n${t('session.summary')}: ${s.summary || ''}`
            : `${t('session.session')} ${serialNumber}\n${t('session.modTime')}: ${fullTime}\n${t('session.size')}: ${size}\n${t('session.summary')}: ${s.summary || ''}`);

        return `
        <div class="session-item ${currentSelected && currentSelected.sessionId === s.sessionId ? 'selected' : ''}"
             data-index="${index}"
             data-session-id="${s.sessionId}"
             title="${tooltipTitle}">
            <div class="session-info">
                <span class="session-serial">${serialNumber}</span>
                <span class="session-summary" title="${displaySummary}">${displaySummary.length > 50 ? displaySummary.substring(0, 47) + '...' : displaySummary}</span>
                <span class="session-time">${time}</span>
                <span class="session-size">${size}</span>
            </div>
            <div class="session-actions">
                <button class="session-btn session-btn-view" title="${t('session.view')}">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>
                        <circle cx="12" cy="12" r="3"/>
                    </svg>
                </button>
                <button class="session-btn session-btn-rename" title="${t('session.rename')}">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                        <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                    </svg>
                </button>
                <button class="session-btn session-btn-delete" title="${t('session.delete')}">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <polyline points="3 6 5 6 21 6"/>
                        <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                    </svg>
                </button>
            </div>
        </div>
    `}).join('');

    // 绑定事件
    listContainer.querySelectorAll('.session-item').forEach(item => {
        const index = parseInt(item.dataset.index);
        const session = sessions[index];

        // 点击会话信息区域选择会话
        item.querySelector('.session-info').onclick = () => window.selectSession(session.sessionId);

        // 查看按钮
        item.querySelector('.session-btn-view').onclick = (e) => {
            e.stopPropagation();
            window.viewSessionDetail(session.sessionId);
        };

        // 重命名按钮
        item.querySelector('.session-btn-rename').onclick = (e) => {
            e.stopPropagation();
            window.renameSession(session.sessionId);
        };

        // 删除按钮
        item.querySelector('.session-btn-delete').onclick = (e) => {
            e.stopPropagation();
            window.deleteSession(session.sessionId);
        };
    });
}

function formatTime(timestamp) {
    const date = new Date(timestamp * 1000);
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    const hours = String(date.getHours()).padStart(2, '0');
    const minutes = String(date.getMinutes()).padStart(2, '0');
    return `${month}/${day} ${hours}:${minutes}`;
}

function formatFullTime(timestamp) {
    const date = new Date(timestamp * 1000);
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    const hours = String(date.getHours()).padStart(2, '0');
    const minutes = String(date.getMinutes()).padStart(2, '0');
    return `${year}-${month}-${day} ${hours}:${minutes}`;
}

function formatSize(bytes) {
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
}

// 选择会话（临时选中，未确认）
function selectSession(sessionId) {
    const session = sessions.find(s => s.sessionId === sessionId);
    if (!session) return;

    const sessionIndex = sessions.findIndex(s => s.sessionId === sessionId);
    const serialNumber = sessionIndex + 1;

    // 临时存储选中的会话（点击确认后才真正保存）
    tempSelectedSession = {
        sessionId: sessionId,
        info: {
            alias: session.alias,
            summary: session.summary,
            serialNumber: serialNumber
        }
    };

    // 仅更新UI显示选中状态，不关闭窗口，不弹toast
    renderSessionList();
}

// 确认选择会话
function confirmSessionSelection() {
    // 将临时选择保存为正式选择
    if (tempSelectedSession) {
        selectedSessions[currentProjectDir] = { ...tempSelectedSession };
    } else {
        // 如果没有临时选择，清除该目录的选择
        delete selectedSessions[currentProjectDir];
    }

    // 关闭会话模态框
    document.getElementById('sessionModal').style.display = 'none';
    tempSelectedSession = null;
    sessions = [];

    // 触发启动器界面更新（通过自定义事件）
    window.dispatchEvent(new CustomEvent('sessionSelected'));
}

async function deleteSession(sessionId) {
    const confirmed = await showConfirmDialog(t('session.confirmDelete'));
    if (!confirmed) return;

    // 保存滚动位置
    const listContainer = document.getElementById('sessionList');
    const scrollTop = listContainer.scrollTop;

    try {
        const cliType = getCurrentCliType();
        if (cliType === 'codex') {
            await DeleteCodexSession(sessionId);
        } else {
            await DeleteSession(currentProjectDir, sessionId);
        }
        showNotification(t('session.deleted'), 'success');
        await loadSessions();
        // 恢复滚动位置
        listContainer.scrollTop = scrollTop;
    } catch (err) {
        console.error('Failed to delete session:', err);
        showNotification(t('session.deleteFailed'), 'error');
    }
}

async function renameSession(sessionId) {
    const session = sessions.find(s => s.sessionId === sessionId);
    const currentName = session?.alias || '';

    const newName = await showPromptDialog(t('session.renamePrompt'), currentName);
    if (newName === null) return;

    // 保存滚动位置
    const listContainer = document.getElementById('sessionList');
    const scrollTop = listContainer.scrollTop;

    try {
        const cliType = getCurrentCliType();
        if (cliType === 'codex') {
            await RenameCodexSession(sessionId, newName);
        } else {
            await RenameSession(currentProjectDir, sessionId, newName);
        }
        showNotification(t('session.renamed'), 'success');
        await loadSessions();
        // 恢复滚动位置
        listContainer.scrollTop = scrollTop;
    } catch (err) {
        console.error('Failed to rename session:', err);
        showNotification(t('session.renameFailed'), 'error');
    }
}

function showConfirmDialog(message) {
    return new Promise(resolve => {
        const modal = document.createElement('div');
        modal.id = 'sessionConfirmModal';
        modal.className = 'modal active';
        modal.style.zIndex = '1002';
        modal.innerHTML = `
            <div class="confirm-dialog-content">
                <div class="confirm-body">
                    <div class="confirm-icon">
                        <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
                            <path d="M12 9v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                        </svg>
                    </div>
                    <div class="confirm-content">
                        <h4 class="confirm-title">${t('common.confirmDeleteTitle')}</h4>
                        <p class="confirm-message">${message}</p>
                    </div>
                </div>
                <div class="confirm-divider"></div>
                <div class="confirm-footer">
                    <button class="btn-confirm-delete" id="confirmYes">${t('common.delete')}</button>
                    <button class="btn-confirm-cancel" id="confirmNo">${t('common.cancel')}</button>
                </div>
            </div>
        `;
        document.body.appendChild(modal);

        modal.querySelector('#confirmYes').onclick = () => { modal.remove(); resolve(true); };
        modal.querySelector('#confirmNo').onclick = () => { modal.remove(); resolve(false); };
        modal.onclick = (e) => { if (e.target === modal) { modal.remove(); resolve(false); } };
    });
}

function showPromptDialog(message, defaultValue = '') {
    return new Promise(resolve => {
        const modal = document.createElement('div');
        modal.id = 'sessionPromptModal';
        modal.className = 'modal active';
        modal.style.zIndex = '1002';
        modal.innerHTML = `
            <div class="modal-content">
                <div class="modal-header">
                    <h2>📝 ${t('session.rename')}</h2>
                    <button class="modal-close" id="promptClose">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="prompt-dialog">
                        <p><span class="required">*</span>${message}</p>
                        <div class="prompt-body">
                            <input type="text" id="promptInput" class="form-input" value="${defaultValue}" />
                        </div>
                        <div class="prompt-actions">
                            <button class="btn btn-primary" id="promptOk">${t('common.ok')}</button>
                            <button class="btn btn-secondary" id="promptCancel">${t('common.cancel')}</button>
                        </div>
                    </div>
                </div>
            </div>
        `;
        document.body.appendChild(modal);

        const input = modal.querySelector('#promptInput');
        setTimeout(() => {
            input.focus();
            input.select();
        }, 100);

        const closeModal = () => {
            modal.classList.remove('active');
            setTimeout(() => modal.remove(), 300);
        };

        const handleSubmit = () => {
            const value = input.value.trim();
            if (!value) {
                showNotification(t('session.aliasRequired'), 'warning');
                input.focus();
                return;
            }
            closeModal();
            resolve(value);
        };

        modal.querySelector('#promptOk').onclick = handleSubmit;
        modal.querySelector('#promptCancel').onclick = () => {
            closeModal();
            resolve(null);
        };
        modal.querySelector('#promptClose').onclick = () => {
            closeModal();
            resolve(null);
        };
        input.onkeydown = (e) => {
            if (e.key === 'Enter') {
                handleSubmit();
            }
        };
    });
}

// 查看会话详情
async function viewSessionDetail(sessionId) {
    const session = sessions.find(s => s.sessionId === sessionId);
    if (!session) return;

    const modal = document.createElement('div');
    modal.id = 'sessionDetailModal';
    modal.className = 'modal active';
    modal.style.zIndex = '1002';
    modal.style.background = 'transparent'; // 子弹窗不需要重复的背景遮罩

    const displayName = session.alias || session.summary || t('session.noSummary');

    modal.innerHTML = `
        <div class="modal-content session-detail-content">
            <div class="modal-header">
                <h2>💬 ${t('session.detail')}</h2>
                <button class="modal-close" onclick="closeSessionDetailModal()">&times;</button>
            </div>
            <div class="modal-body">
                <div class="session-detail-messages" id="sessionDetailMessages">
                    <div class="session-loading">${t('session.loading')}</div>
                </div>
            </div>
        </div>
    `;

    document.body.appendChild(modal);

    // 加载会话数据
    try {
        const cliType = getCurrentCliType();
        let result;

        if (cliType === 'codex') {
            result = JSON.parse(await GetCodexSessionData(sessionId));
        } else {
            result = JSON.parse(await GetSessionData(currentProjectDir, sessionId));
        }

        if (!result.success) {
            document.getElementById('sessionDetailMessages').innerHTML =
                `<div class="session-empty">${t('session.loadDetailError')}</div>`;
            return;
        }

        const messages = result.data || [];
        renderMessages(messages);
    } catch (err) {
        console.error('Failed to load session data:', err);
        document.getElementById('sessionDetailMessages').innerHTML =
            `<div class="session-empty">${t('session.loadDetailError')}</div>`;
    }
}

// 渲染消息列表
function renderMessages(messages) {
    const container = document.getElementById('sessionDetailMessages');

    if (messages.length === 0) {
        container.innerHTML = `<div class="session-empty">${t('session.noMessages')}</div>`;
        return;
    }

    container.innerHTML = messages.map(msg => {
        const isUser = msg.type === 'user';
        const label = isUser ? t('session.user') : t('session.assistant');
        // 使用 markdown 解析器处理内容
        const content = parseMarkdown(msg.content.trim());
        // 格式化时间
        const timeStr = msg.timestamp ? formatMessageTime(msg.timestamp) : '';

        return `
            <div class="message-row ${isUser ? 'message-row-user' : 'message-row-assistant'}">
                <div class="message-label">${label}${timeStr ? `<span class="message-time">${timeStr}</span>` : ''}</div>
                <div class="message-bubble ${isUser ? 'bubble-user' : 'bubble-assistant'}">
                    <div class="message-content markdown-body">${content}</div>
                </div>
            </div>
        `;
    }).join('');
}

// 格式化消息时间
function formatMessageTime(timestamp) {
    const date = new Date(timestamp);
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    const hours = String(date.getHours()).padStart(2, '0');
    const minutes = String(date.getMinutes()).padStart(2, '0');
    const seconds = String(date.getSeconds()).padStart(2, '0');
    return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`;
}

// 关闭会话详情模态窗口
function closeSessionDetailModal() {
    const modal = document.getElementById('sessionDetailModal');
    if (modal) {
        modal.classList.remove('active');
        setTimeout(() => modal.remove(), 300);
    }
}
