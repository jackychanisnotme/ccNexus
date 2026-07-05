import { t } from '../i18n/index.js';
import { escapeHtml } from '../utils/format.js';
import { addEndpoint, updateEndpoint, removeEndpoint, testEndpoint, testEndpointLight, updatePort, getNetworkStatus, updateListenMode, getLANDiscoveryStatus, refreshLANDiscovery as refreshLANDiscoveryConfig, addDiscoveredLANEndpoint as addDiscoveredLANEndpointConfig } from './config.js';
import { setTestState, clearTestState, saveEndpointTestStatus, openTokenPoolModal } from './endpoints.js';

let currentEditIndex = -1;
const AUTH_MODE_API_KEY = 'api_key';
const AUTH_MODE_TOKEN_POOL = 'token_pool';
const AUTH_MODE_CODEX_TOKEN_POOL = 'codex_token_pool';
const AUTH_MODE_CLAUDE_OAUTH_TOKEN_POOL = 'claude_oauth_token_pool';
const CODEX_FIXED_API_URL = 'https://chatgpt.com/backend-api/codex';
const CODEX_FIXED_TRANSFORMER = 'openai2';
const CLAUDE_OAUTH_FIXED_API_URL = 'https://api.anthropic.com';
const CLAUDE_OAUTH_FIXED_TRANSFORMER = 'claude';
const CLAUDE_OAUTH_DEFAULT_MODEL = 'opus';

function normalizeThinkingForTransformer(transformer, value) {
    const normalized = String(value ?? '').trim().toLowerCase();
    if (transformer === 'deepseek') {
        if (!normalized || normalized === 'default' || normalized === 'auto' || normalized === 'inherit') {
            return '';
        }
        if (normalized === 'off') {
            return 'off';
        }
        if (normalized === 'xhigh' || normalized === 'max') {
            return 'xhigh';
        }
        if (['low', 'medium', 'high'].includes(normalized)) {
            return 'high';
        }
        return '';
    }
    if (!normalized || normalized === 'off' || normalized === 'default' || normalized === 'auto' || normalized === 'inherit') {
        return 'off';
    }
    if (['low', 'medium', 'high', 'xhigh'].includes(normalized)) {
        return normalized;
    }
    return 'off';
}

function renderThinkingOptions(transformer, value) {
    const select = document.getElementById('endpointThinking');
    if (!select) {
        return;
    }

    const normalized = normalizeThinkingForTransformer(transformer, value);
    const selected = normalized === 'off' ? (transformer === 'deepseek' ? '' : 'medium') : normalized;
    const options = transformer === 'deepseek'
        ? [
            ['', t('modal.thinkingProviderDefault')],
            ['high', t('modal.thinkingHigh')],
            ['xhigh', t('modal.thinkingMax')]
        ]
        : [
            ['low', t('modal.thinkingLow')],
            ['medium', t('modal.thinkingMedium')],
            ['high', t('modal.thinkingHigh')],
            ['xhigh', t('modal.thinkingXHigh')]
        ];

    select.innerHTML = options.map(([optionValue, label]) =>
        `<option value="${optionValue}" ${optionValue === selected ? 'selected' : ''}>${label}</option>`
    ).join('');
}

function setThinkingControlValue(value) {
    const transformer = document.getElementById('endpointTransformer')?.value || 'claude';
    const enabled = document.getElementById('endpointThinkingEnabled');
    const select = document.getElementById('endpointThinking');
    const help = document.getElementById('thinkingHelpText');
    const normalized = normalizeThinkingForTransformer(transformer, value);

    renderThinkingOptions(transformer, normalized);
    if (enabled) {
        enabled.checked = normalized !== 'off';
    }
    if (select) {
        select.disabled = normalized === 'off';
    }
    if (help) {
        help.textContent = transformer === 'deepseek' ? t('modal.thinkingHelpDeepSeek') : t('modal.thinkingHelp');
    }
}

function getThinkingControlValue() {
    const transformer = document.getElementById('endpointTransformer')?.value || 'claude';
    const enabled = document.getElementById('endpointThinkingEnabled');
    if (!enabled || !enabled.checked) {
        return 'off';
    }
    return normalizeThinkingForTransformer(transformer, document.getElementById('endpointThinking')?.value ?? '');
}

export function handleThinkingControlChange() {
    const transformer = document.getElementById('endpointTransformer')?.value || 'claude';
    const enabled = document.getElementById('endpointThinkingEnabled');
    const select = document.getElementById('endpointThinking');
    if (!enabled || !select) {
        return;
    }
    renderThinkingOptions(transformer, enabled.checked ? select.value : 'off');
    select.disabled = !enabled.checked;
}

// Show error toast
function showError(message) {
    const toast = document.getElementById('errorToast');
    const messageEl = document.getElementById('errorToastMessage');

    messageEl.textContent = message;
    toast.classList.add('show');

    setTimeout(() => {
        toast.classList.remove('show');
    }, 3000);
}

// Show notification
export function showNotification(message, type = 'info') {
    // Create notification element
    const notification = document.createElement('div');
    notification.className = `notification notification-${type}`;
    notification.textContent = message;

    // Add to body
    document.body.appendChild(notification);

    // Show notification
    setTimeout(() => notification.classList.add('show'), 10);

    // Hide and remove after 3 seconds
    setTimeout(() => {
        notification.classList.remove('show');
        setTimeout(() => notification.remove(), 300);
    }, 3000);
}

// Confirm dialog
let confirmResolve = null;

export function showConfirm(message) {
    return new Promise((resolve) => {
        confirmResolve = resolve;
        document.getElementById('confirmMessage').textContent = message;
        document.getElementById('confirmDialog').classList.add('active');
    });
}

export function acceptConfirm() {
    document.getElementById('confirmDialog').classList.remove('active');
    if (confirmResolve) {
        confirmResolve(true);
        confirmResolve = null;
    }
}

export function cancelConfirm() {
    document.getElementById('confirmDialog').classList.remove('active');
    if (confirmResolve) {
        confirmResolve(false);
        confirmResolve = null;
    }
}

// Close action dialog
export function showCloseActionDialog() {
    document.getElementById('closeActionDialog').classList.add('active');
}

export function quitApplication() {
    document.getElementById('closeActionDialog').classList.remove('active');
    window.go.main.App.Quit();
}

export function minimizeToTray() {
    document.getElementById('closeActionDialog').classList.remove('active');
    window.go.main.App.HideWindow();
}

// Toggle password visibility
export function togglePasswordVisibility() {
    const input = document.getElementById('endpointKey');
    const icon = document.getElementById('eyeIcon');

    if (input.type === 'password') {
        input.type = 'text';
        icon.innerHTML = '<path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"></path><line x1="1" y1="1" x2="23" y2="23"></line>';
    } else {
        input.type = 'password';
        icon.innerHTML = '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path><circle cx="12" cy="12" r="3"></circle>';
    }
}

function getEndpointAuthMode() {
    const mode = document.getElementById('endpointAuthMode')?.value;
    if (mode === AUTH_MODE_CLAUDE_OAUTH_TOKEN_POOL) {
        return AUTH_MODE_CLAUDE_OAUTH_TOKEN_POOL;
    }
    if (mode === AUTH_MODE_CODEX_TOKEN_POOL) {
        return AUTH_MODE_CODEX_TOKEN_POOL;
    }
    if (mode === AUTH_MODE_TOKEN_POOL) {
        return AUTH_MODE_TOKEN_POOL;
    }
    return AUTH_MODE_API_KEY;
}

function isTokenPoolMode(mode) {
    return mode === AUTH_MODE_TOKEN_POOL || mode === AUTH_MODE_CODEX_TOKEN_POOL || mode === AUTH_MODE_CLAUDE_OAUTH_TOKEN_POOL;
}

function isCodexTokenPoolMode(mode) {
    return mode === AUTH_MODE_CODEX_TOKEN_POOL;
}

function isClaudeOAuthTokenPoolMode(mode) {
    return mode === AUTH_MODE_CLAUDE_OAUTH_TOKEN_POOL;
}

function readEndpointMaxConcurrentRequests() {
    const raw = document.getElementById('endpointMaxConcurrentRequests')?.value?.trim() || '';
    if (raw === '') {
        return 0;
    }
    const value = Number(raw);
    if (!Number.isInteger(value) || value < 0) {
        return null;
    }
    return value;
}

function readEndpointDraftFromModal() {
    const authMode = getEndpointAuthMode();
    let url = document.getElementById('endpointUrl')?.value?.trim() || '';
    let key = document.getElementById('endpointKey')?.value?.trim() || '';
    let transformer = document.getElementById('endpointTransformer')?.value || 'claude';
    const model = document.getElementById('endpointModel')?.value?.trim() || '';
    const maxConcurrentRequests = readEndpointMaxConcurrentRequests();
    const isCodexTokenPool = isCodexTokenPoolMode(authMode);
    const isClaudeOAuthTokenPool = isClaudeOAuthTokenPoolMode(authMode);

    if (isCodexTokenPool) {
        url = CODEX_FIXED_API_URL;
        transformer = CODEX_FIXED_TRANSFORMER;
        key = '';
    } else if (isClaudeOAuthTokenPool) {
        url = CLAUDE_OAUTH_FIXED_API_URL;
        transformer = CLAUDE_OAUTH_FIXED_TRANSFORMER;
        key = '';
    }

    return {
        name: document.getElementById('endpointName')?.value?.trim() || '',
        url,
        key,
        authMode,
        transformer,
        model: isClaudeOAuthTokenPool && !model ? CLAUDE_OAUTH_DEFAULT_MODEL : model,
        thinking: getThinkingControlValue(),
        proxyUrl: document.getElementById('endpointProxyUrl')?.value?.trim() || '',
        forceStream: !!document.getElementById('endpointForceStream')?.checked,
        codexFastMode: isCodexTokenPool && !!document.getElementById('endpointCodexFastMode')?.checked,
        maxConcurrentRequests,
        remark: document.getElementById('endpointRemark')?.value?.trim() || ''
    };
}

function draftValue(value) {
    return String(value ?? '').trim();
}

function getSavedEndpointForCurrentIndex() {
    const endpoints = window.config?.endpoints || [];
    if (currentEditIndex < 0 || currentEditIndex >= endpoints.length) {
        return null;
    }
    return endpoints[currentEditIndex] || null;
}

function endpointDraftHasUnsavedChanges(draft) {
    const saved = getSavedEndpointForCurrentIndex();
    if (!saved) {
        return currentEditIndex >= 0;
    }
    return (
        draftValue(saved.name) !== draft.name ||
        draftValue(saved.apiUrl) !== draft.url ||
        draftValue(saved.apiKey) !== draft.key ||
        draftValue(saved.authMode || AUTH_MODE_API_KEY) !== draft.authMode ||
        draftValue(saved.transformer || 'claude') !== draft.transformer ||
        draftValue(saved.model) !== draft.model ||
        draftValue(saved.thinking) !== draft.thinking ||
        draftValue(saved.proxyUrl) !== draft.proxyUrl ||
        !!saved.forceStream !== draft.forceStream ||
        !!saved.codexFastMode !== draft.codexFastMode ||
        (saved.maxConcurrentRequests || 0) !== (draft.maxConcurrentRequests || 0) ||
        draftValue(saved.remark) !== draft.remark
    );
}

async function loadFreshEndpointConfig() {
    const configStr = await window.go.main.App.GetConfig();
    const config = JSON.parse(configStr);
    window.config = config;
    return config;
}

function validateEndpointDraft(draft, config) {
    if (!draft.name || !draft.url) {
        showError(t('modal.requiredFields'));
        return false;
    }
    if (draft.authMode === AUTH_MODE_API_KEY && !draft.key) {
        showError(t('modal.requiredApiKey'));
        return false;
    }
    if (!Number.isInteger(draft.maxConcurrentRequests) || draft.maxConcurrentRequests < 0) {
        showError(t('modal.invalidMaxConcurrentRequests'));
        return false;
    }

    const endpoints = Array.isArray(config?.endpoints) ? config.endpoints : [];
    const existingEndpoint = endpoints.find((ep, idx) =>
        ep.name === draft.name && idx !== currentEditIndex
    );
    if (existingEndpoint) {
        showError(`Endpoint name "${draft.name}" already exists. Please use a different name.`);
        return false;
    }
    return true;
}

async function persistEndpointDraftFromModal() {
    const draft = readEndpointDraftFromModal();
    const config = await loadFreshEndpointConfig();
    if (!validateEndpointDraft(draft, config)) {
        return null;
    }

    try {
        if (currentEditIndex === -1) {
            await addEndpoint(draft.name, draft.url, draft.key, draft.authMode, draft.transformer, draft.model, draft.thinking, draft.proxyUrl, draft.forceStream, draft.codexFastMode, draft.maxConcurrentRequests, draft.remark);
        } else {
            await updateEndpoint(currentEditIndex, draft.name, draft.url, draft.key, draft.authMode, draft.transformer, draft.model, draft.thinking, draft.proxyUrl, draft.forceStream, draft.codexFastMode, draft.maxConcurrentRequests, draft.remark);
        }

        const freshConfig = await loadFreshEndpointConfig();
        const savedIndex = (freshConfig.endpoints || []).findIndex((ep) => ep.name === draft.name);
        if (savedIndex < 0) {
            showError(t('modal.saveFailed').replace('{error}', t('modal.savedEndpointNotFound')));
            return null;
        }
        currentEditIndex = savedIndex;
        return { index: savedIndex, name: draft.name, draft, config: freshConfig };
    } catch (error) {
        const message = error?.message || String(error);
        showError(t('modal.saveFailed').replace('{error}', message));
        return null;
    }
}

async function persistEndpointDraftForTokenPoolManagement() {
    const draft = readEndpointDraftFromModal();
    if (!isTokenPoolMode(draft.authMode)) {
        showNotification(t('modal.tokenPoolOnlyForTokenMode'), 'warning');
        return null;
    }
    return persistEndpointDraftFromModal();
}

function updateManageTokenPoolButton() {
    const btn = document.getElementById('manageTokenPoolBtn');
    const actionGroup = document.querySelector('.endpoint-token-pool-action');
    const help = document.getElementById('tokenPoolCredentialHelp');
    const modeHelp = document.getElementById('tokenPoolCredentialModeHelp');
    const isTokenPool = isTokenPoolMode(getEndpointAuthMode());

    if (actionGroup) {
        actionGroup.style.display = isTokenPool ? 'block' : 'none';
    }
    if (!btn) {
        return;
    }

    btn.style.display = isTokenPool ? 'inline-flex' : 'none';
    if (!isTokenPool) {
        return;
    }

    const authMode = getEndpointAuthMode();
    const draft = readEndpointDraftFromModal();
    const label = currentEditIndex < 0
        ? t('modal.manageTokenPoolNewEndpoint')
        : (endpointDraftHasUnsavedChanges(draft) ? t('modal.manageTokenPoolApplyChanges') : t('modal.manageTokenPool'));
    btn.textContent = `🪪 ${label}`;

    if (help) {
        help.textContent = t('modal.tokenPoolCredentialHelp');
    }
    if (modeHelp) {
        if (isCodexTokenPoolMode(authMode)) {
            modeHelp.textContent = t('modal.codexTokenPoolCredentialHelp');
        } else if (isClaudeOAuthTokenPoolMode(authMode)) {
            modeHelp.textContent = t('modal.claudeOAuthTokenPoolCredentialHelp');
        } else {
            modeHelp.textContent = t('modal.genericTokenPoolCredentialHelp');
        }
    }
}

function bindEndpointDraftChangeHandlers() {
    const fieldIds = [
        'endpointName',
        'endpointAuthMode',
        'endpointUrl',
        'endpointProxyUrl',
        'endpointKey',
        'endpointTransformer',
        'endpointModel',
        'endpointForceStream',
        'endpointCodexFastMode',
        'endpointMaxConcurrentRequests',
        'endpointRemark'
    ];

    fieldIds.forEach((id) => {
        const element = document.getElementById(id);
        if (!element || element.dataset.tokenPoolDraftBound === 'true') {
            return;
        }
        element.addEventListener('input', updateManageTokenPoolButton);
        element.addEventListener('change', updateManageTokenPoolButton);
        element.dataset.tokenPoolDraftBound = 'true';
    });
}

export function handleAuthModeChange() {
	const authMode = getEndpointAuthMode();
	const keyGroup = document.getElementById('endpointKeyGroup');
	const keyInput = document.getElementById('endpointKey');
	const fetchModelsBtn = document.getElementById('fetchModelsBtn');
	const urlHelp = document.getElementById('endpointUrlHelp');
	const urlInput = document.getElementById('endpointUrl');
	const transformerSelect = document.getElementById('endpointTransformer');
	const modelInput = document.getElementById('endpointModel');
	const codexFastModeGroup = document.getElementById('endpointCodexFastModeGroup');
	const codexFastModeInput = document.getElementById('endpointCodexFastMode');

	const isTokenPool = isTokenPoolMode(authMode);
	const isCodexTokenPool = isCodexTokenPoolMode(authMode);
	const isClaudeOAuthTokenPool = isClaudeOAuthTokenPoolMode(authMode);
	const isFixedTokenPool = isCodexTokenPool || isClaudeOAuthTokenPool;
    if (keyGroup) {
        keyGroup.style.display = isTokenPool ? 'none' : 'block';
    }
	if (keyInput) {
		keyInput.disabled = isTokenPool;
	}
	if (urlInput) {
		urlInput.disabled = isFixedTokenPool;
		urlInput.readOnly = isFixedTokenPool;
		urlInput.classList.toggle('field-locked', isFixedTokenPool);
		urlInput.title = isFixedTokenPool ? t('modal.tokenPoolLockedFieldTip') : '';
		if (isCodexTokenPool) {
			urlInput.value = CODEX_FIXED_API_URL;
		} else if (isClaudeOAuthTokenPool) {
			urlInput.value = CLAUDE_OAUTH_FIXED_API_URL;
		}
	}
	if (transformerSelect) {
		transformerSelect.disabled = isFixedTokenPool;
		transformerSelect.classList.toggle('field-locked', isFixedTokenPool);
		transformerSelect.title = isFixedTokenPool ? t('modal.tokenPoolLockedFieldTip') : '';
		if (isCodexTokenPool) {
			transformerSelect.value = CODEX_FIXED_TRANSFORMER;
		} else if (isClaudeOAuthTokenPool) {
			transformerSelect.value = CLAUDE_OAUTH_FIXED_TRANSFORMER;
		}
	}
	if (modelInput && isClaudeOAuthTokenPool && !modelInput.value.trim()) {
		modelInput.value = CLAUDE_OAUTH_DEFAULT_MODEL;
	}
	if (codexFastModeGroup) {
		codexFastModeGroup.style.display = isCodexTokenPool ? 'block' : 'none';
	}
	if (codexFastModeInput) {
		codexFastModeInput.disabled = !isCodexTokenPool;
		if (!isCodexTokenPool) {
			codexFastModeInput.checked = false;
		}
	}
	if (fetchModelsBtn) {
		fetchModelsBtn.disabled = false;
		fetchModelsBtn.title = isTokenPool ? t('modal.fetchModelsUsingTokenPool') : t('modal.fetchModels');
	}
	if (urlHelp) {
		if (isCodexTokenPool) {
			urlHelp.textContent = t('modal.codexTokenPoolApiUrlHelp');
		} else if (isClaudeOAuthTokenPool) {
			urlHelp.textContent = t('modal.claudeOAuthTokenPoolApiUrlHelp');
		} else {
			urlHelp.textContent = isTokenPool ? t('modal.tokenPoolApiUrlHelp') : t('modal.apiUrlHelp');
		}
	}
	if (isFixedTokenPool) {
		handleTransformerChange();
	}
	updateManageTokenPoolButton();
}

// Endpoint Modal
export function showAddEndpointModal() {
	currentEditIndex = -1;
	document.getElementById('modalTitle').textContent = '➕ ' + t('modal.addEndpoint');
    document.getElementById('endpointName').value = '';
    document.getElementById('endpointUrl').value = '';
    document.getElementById('endpointProxyUrl').value = '';
    document.getElementById('endpointKey').value = '';
    document.getElementById('endpointKey').type = 'password';
    document.getElementById('eyeIcon').innerHTML = '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path><circle cx="12" cy="12" r="3"></circle>';
    document.getElementById('endpointAuthMode').value = 'api_key';
    document.getElementById('endpointTransformer').value = 'claude';
    document.getElementById('endpointModel').value = '';
    document.getElementById('endpointForceStream').checked = false;
    document.getElementById('endpointCodexFastMode').checked = false;
    document.getElementById('endpointMaxConcurrentRequests').value = '0';
    document.getElementById('endpointRemark').value = '';
    handleAuthModeChange();
    updateManageTokenPoolButton();
    handleTransformerChange();
    setThinkingControlValue('');
    bindEndpointDraftChangeHandlers();
    updateManageTokenPoolButton();
    document.getElementById('endpointModal').classList.add('active');
}

// 使用预设数据打开添加端点模态框
export function showAddEndpointModalWithPreset(presetData) {
	currentEditIndex = -1;
	document.getElementById('modalTitle').textContent = '➕ ' + t('modal.addEndpoint');
	document.getElementById('endpointName').value = presetData.name || '';
	document.getElementById('endpointUrl').value = presetData.apiUrl || '';
	document.getElementById('endpointProxyUrl').value = presetData.proxyUrl || '';
	document.getElementById('endpointKey').value = presetData.apiKey || '';
	document.getElementById('endpointKey').type = 'password';
	document.getElementById('eyeIcon').innerHTML = '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path><circle cx="12" cy="12" r="3"></circle>';
	document.getElementById('endpointAuthMode').value = presetData.authMode || 'api_key';
	document.getElementById('endpointTransformer').value = presetData.transformer || 'claude';
	document.getElementById('endpointModel').value = presetData.model || '';
	document.getElementById('endpointForceStream').checked = !!presetData.forceStream;
	document.getElementById('endpointCodexFastMode').checked = !!presetData.codexFastMode;
	document.getElementById('endpointMaxConcurrentRequests').value = String(presetData.maxConcurrentRequests || 0);
	document.getElementById('endpointRemark').value = presetData.remark || '';
	handleAuthModeChange();
	updateManageTokenPoolButton();
	handleTransformerChange();
	setThinkingControlValue(presetData.thinking ?? '');
	bindEndpointDraftChangeHandlers();
	updateManageTokenPoolButton();
	document.getElementById('endpointModal').classList.add('active');
}

export async function editEndpoint(index) {
	currentEditIndex = index;
	const configStr = await window.go.main.App.GetConfig();
	const config = JSON.parse(configStr);
    window.config = config;
	const ep = config.endpoints[index];

	document.getElementById('modalTitle').textContent = '✏️ ' + t('modal.editEndpoint');
    document.getElementById('endpointName').value = ep.name;
    document.getElementById('endpointUrl').value = ep.apiUrl;
    document.getElementById('endpointProxyUrl').value = ep.proxyUrl || '';
    document.getElementById('endpointKey').value = ep.apiKey;
    document.getElementById('endpointKey').type = 'password';
    document.getElementById('eyeIcon').innerHTML = '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path><circle cx="12" cy="12" r="3"></circle>';
    document.getElementById('endpointAuthMode').value = ep.authMode || 'api_key';
    document.getElementById('endpointTransformer').value = ep.transformer || 'claude';
    document.getElementById('endpointModel').value = ep.model || '';
    document.getElementById('endpointForceStream').checked = !!ep.forceStream;
    document.getElementById('endpointCodexFastMode').checked = !!ep.codexFastMode;
    document.getElementById('endpointMaxConcurrentRequests').value = String(ep.maxConcurrentRequests || 0);
    document.getElementById('endpointRemark').value = ep.remark || '';

    handleAuthModeChange();
    updateManageTokenPoolButton();
    handleTransformerChange();
    setThinkingControlValue(ep.thinking ?? '');
    bindEndpointDraftChangeHandlers();
    updateManageTokenPoolButton();
    document.getElementById('endpointModal').classList.add('active');
}

export async function openEndpointTokenPoolFromModal() {
    const savedEndpoint = await persistEndpointDraftForTokenPoolManagement();
    if (!savedEndpoint) {
        return;
    }
    await openTokenPoolModal(savedEndpoint.index, savedEndpoint.name);
}

export async function saveEndpoint() {
    const savedEndpoint = await persistEndpointDraftFromModal();
    if (!savedEndpoint) {
        return;
    }

    closeModal();
    if (window.loadConfig) {
        await window.loadConfig();
    }
}

export async function deleteEndpoint(index) {
    try {
        const config = await window.go.main.App.GetConfig();
        const endpoints = JSON.parse(config).endpoints;
        const endpointName = endpoints[index].name;

        const confirmed = await showConfirm(t('modal.confirmDelete').replace('{name}', endpointName));
        if (!confirmed) {
            return;
        }

        await removeEndpoint(index);
        window.loadConfig();
    } catch (error) {
        console.error('Delete failed:', error);
        showError(t('modal.deleteFailed').replace('{error}', error));
    }
}

export function closeModal() {
    document.getElementById('endpointModal').classList.remove('active');
}

export function handleTransformerChange() {
    const transformer = document.getElementById('endpointTransformer').value;
    const modelRequired = document.getElementById('modelRequired');
    const modelInput = document.getElementById('endpointModel');
    const modelHelpText = document.getElementById('modelHelpText');

    // Clear fetched models when transformer changes
    clearFetchedModels();

    modelRequired.style.display = 'none';
    if (transformer === 'claude') {
        modelInput.placeholder = 'e.g., claude-3-5-sonnet-20241022';
        modelHelpText.textContent = t('modal.modelHelpClaude');
    } else if (transformer === 'openai') {
        modelInput.placeholder = 'e.g., gpt-4-turbo';
        modelHelpText.textContent = t('modal.modelHelpOpenAI');
    } else if (transformer === 'openai2') {
        modelInput.placeholder = 'e.g., gpt-4.1';
        modelHelpText.textContent = t('modal.modelHelpOpenAI2');
    } else if (transformer === 'gemini') {
        modelInput.placeholder = 'e.g., gemini-pro';
        modelHelpText.textContent = t('modal.modelHelpGemini');
    } else if (transformer === 'deepseek') {
        modelInput.placeholder = 'e.g., deepseek-v4-pro';
        modelHelpText.textContent = t('modal.modelHelpDeepSeek');
    } else if (transformer === 'kimi') {
        modelInput.placeholder = 'e.g., kimi-k2.6';
        modelHelpText.textContent = t('modal.modelHelpKimi');
    } else if (transformer === 'poe') {
        modelInput.placeholder = 'e.g., claude-fable-5';
        modelHelpText.textContent = t('modal.modelHelpPoe');
    }

    const currentThinking = getThinkingControlValue();
    setThinkingControlValue(transformer === 'deepseek' && currentThinking === 'off' ? '' : currentThinking);
}

// Store fetched models for filtering
let fetchedModels = [];

// Fetch models from API
export async function fetchModels() {
    const authMode = getEndpointAuthMode();
    const isCodexTokenPool = isCodexTokenPoolMode(authMode);
    const apiUrl = isCodexTokenPool ? CODEX_FIXED_API_URL : document.getElementById('endpointUrl').value.trim();
    const proxyUrl = document.getElementById('endpointProxyUrl')?.value.trim() || '';
    const apiKey = document.getElementById('endpointKey').value.trim();
    const transformer = isCodexTokenPool ? CODEX_FIXED_TRANSFORMER : document.getElementById('endpointTransformer').value;
    const fetchBtn = document.getElementById('fetchModelsBtn');
    const fetchIcon = document.getElementById('fetchModelsIcon');
    const modelInput = document.getElementById('endpointModel');
    const dropdown = document.getElementById('modelDropdown');

    // Validate inputs
    if (!apiUrl) {
        showNotification(t('modal.fetchModelsNoUrl'), 'error');
        return;
    }
    if (!isTokenPoolMode(authMode) && !apiKey) {
        showNotification(t('modal.fetchModelsNoKey'), 'error');
        return;
    }

    // Show loading state
    fetchBtn.disabled = true;
    fetchIcon.textContent = '⏳';

    try {
        const resultStr = await window.go.main.App.FetchModels(apiUrl, apiKey, transformer, proxyUrl);
        const result = JSON.parse(resultStr);

        if (result.success && result.models && result.models.length > 0) {
            fetchedModels = result.models;
            renderModelDropdown(fetchedModels, dropdown, modelInput);
            dropdown.classList.add('show');

            showNotification(t('modal.fetchModelsSuccess').replace('{count}', result.models.length), 'success');
        } else {
            const msg = result.message?.includes('no_models_found') ? t('modal.fetchModelsEmpty') : t('modal.fetchModelsFailed');
            showNotification(msg, 'error');
        }
    } catch (error) {
        console.error('Failed to fetch models:', error);
        showNotification(t('modal.fetchModelsFailed') + ': ' + error, 'error');
    } finally {
        fetchBtn.disabled = false;
        fetchIcon.textContent = t('modal.fetchModelsBtn');
    }
}

// Render model dropdown
function renderModelDropdown(models, dropdown, input) {
    dropdown.innerHTML = '';
    models.forEach(model => {
        const item = document.createElement('div');
        item.className = 'model-dropdown-item';
        item.textContent = model;
        item.onclick = () => {
            input.value = model;
            dropdown.classList.remove('show');
        };
        dropdown.appendChild(item);
    });

}


// Initialize model input events
export function initModelInputEvents() {
    const modelInput = document.getElementById('endpointModel');
    const dropdown = document.getElementById('modelDropdown');
    if (!modelInput || !dropdown) return;

    // Show dropdown on focus if has models
    modelInput.addEventListener('focus', () => {
        if (fetchedModels.length > 0) {
            renderModelDropdown(fetchedModels, dropdown, modelInput);
            dropdown.classList.add('show');
        }
    });

    // Hide dropdown on click outside
    document.addEventListener('click', (e) => {
        if (!e.target.closest('.model-select-container')) {
            dropdown.classList.remove('show');
        }
    });

}

// Toggle model dropdown
export function toggleModelDropdown() {
    const dropdown = document.getElementById('modelDropdown');
    const modelInput = document.getElementById('endpointModel');
    if (!dropdown || fetchedModels.length === 0) return;

    if (dropdown.classList.contains('show')) {
        dropdown.classList.remove('show');
    } else {
        renderModelDropdown(fetchedModels, dropdown, modelInput);
        dropdown.classList.add('show');
    }
}

// Clear fetched models (call when transformer changes)
export function clearFetchedModels() {
    fetchedModels = [];
    const dropdown = document.getElementById('modelDropdown');
    if (dropdown) {
        dropdown.innerHTML = '';
        dropdown.classList.remove('show');
    }
}

// Port Modal
const networkCategoryKeys = {
    proxy: 'connectionCategoryProxy',
    admin_ui: 'connectionCategoryAdminUi',
    api: 'connectionCategoryApi',
    health: 'connectionCategoryHealth',
    events: 'connectionCategoryEvents'
};

function getNetworkCategoryLabel(category) {
    return t(`modal.${networkCategoryKeys[category] || 'connectionCategoryProxy'}`);
}

function formatConnectionDuration(ms) {
    const seconds = Math.max(0, Math.floor((Number(ms) || 0) / 1000));
    if (seconds < 60) {
        return `${seconds}s`;
    }
    const minutes = Math.floor(seconds / 60);
    const rest = seconds % 60;
    return `${minutes}m ${rest}s`;
}

function renderNetworkAddressList(status) {
    const localURL = status?.localURL || '';
    const lanURLs = Array.isArray(status?.lanURLs) ? status.lanURLs : [];
    return `
        <div class="network-address-group">
            <div class="network-section-title">${t('modal.localAddress')}</div>
            <code class="network-address">${escapeHtml(localURL)}</code>
        </div>
        <div class="network-address-group">
            <div class="network-section-title">${t('modal.lanAddresses')}</div>
            ${lanURLs.length > 0
                ? lanURLs.map(url => `<code class="network-address">${escapeHtml(url)}</code>`).join('')
                : `<div class="network-empty">${t('modal.noLanAddresses')}</div>`}
        </div>
        ${status?.restartRequired ? `<div class="network-restart">${t('modal.restartRequired')}</div>` : ''}
    `;
}

function renderNetworkConnections(status) {
    const connections = status?.connections || {};
    const byCategory = connections.byCategory || {};
    const activeConnections = Array.isArray(connections.connections) ? connections.connections : [];
    const categoryOrder = ['proxy', 'admin_ui', 'api', 'health', 'events'];
    const categorySummary = categoryOrder.map(category => `
        <span class="network-count-pill">${getNetworkCategoryLabel(category)} ${Number(byCategory[category] || 0)}</span>
    `).join('');

    if (activeConnections.length === 0) {
        return `
            <div class="network-category-summary">${categorySummary}</div>
            <div class="network-empty">${t('modal.noActiveConnections')}</div>
        `;
    }

    return `
        <div class="network-category-summary">${categorySummary}</div>
        <div class="network-connection-list">
            ${activeConnections.map(conn => `
                <div class="network-connection-row">
                    <span class="network-connection-category">${getNetworkCategoryLabel(conn.category)}</span>
                    <span title="${escapeHtml(conn.userAgent || '')}">${escapeHtml(conn.clientIp || 'unknown')}</span>
                    <span title="${escapeHtml(conn.path || '')}">${escapeHtml(conn.method || '')} ${escapeHtml(conn.path || '')}</span>
                    <span>${formatConnectionDuration(conn.durationMillis)}</span>
                </div>
            `).join('')}
        </div>
    `;
}

function updateLANDiscoveryBadge(status) {
    const badge = document.getElementById('lanDiscoveryBadge');
    if (!badge) {
        return;
    }
    const unadded = Number(status?.unadded || 0);
    badge.classList.toggle('hidden', unadded <= 0);
    badge.textContent = '!';
}

function renderLANDiscoveryList(status) {
    const panel = document.getElementById('lanDiscoveryPanel');
    const list = document.getElementById('lanDiscoveryList');
    if (!panel || !list) {
        updateLANDiscoveryBadge(status);
        return;
    }
    const candidates = Array.isArray(status?.candidates) ? status.candidates : [];
    window.__lanDiscoveryCandidates = candidates;
    updateLANDiscoveryBadge(status);
    panel.style.display = candidates.length > 0 ? 'block' : 'none';
    if (candidates.length === 0) {
        list.innerHTML = `<div class="network-empty">${t('modal.lanDiscoveryEmpty')}</div>`;
        return;
    }
    list.innerHTML = candidates.map((candidate, index) => {
        const disabled = candidate.added || candidate.requiresPairing || candidate?.pairing?.enabled;
        const buttonLabel = candidate.added
            ? t('modal.lanDiscoveryAdded')
            : (candidate.requiresPairing || candidate?.pairing?.enabled)
                ? t('modal.lanDiscoveryPairingReserved')
                : t('modal.lanDiscoveryAdd');
        return `
            <div class="lan-discovery-row">
                <div class="lan-discovery-main">
                    <strong>${escapeHtml(candidate.name || candidate.host || 'AINexus')}</strong>
                    <code>${escapeHtml(candidate.baseUrl || '')}</code>
                </div>
                <button class="btn btn-primary lan-discovery-add-btn" ${disabled ? 'disabled' : ''} onclick="window.addDiscoveredLANEndpoint(${index})">
                    ${buttonLabel}
                </button>
            </div>
        `;
    }).join('');
}

export function updateLANDiscoveryStatus(status) {
    renderLANDiscoveryList(status);
}

export async function refreshLANDiscovery() {
    try {
        const status = await refreshLANDiscoveryConfig();
        renderLANDiscoveryList(status);
        return status;
    } catch (error) {
        showNotification(t('modal.lanDiscoveryRefreshFailed').replace('{error}', error), 'error');
        return null;
    }
}

export async function loadLANDiscoveryStatus() {
    try {
        const status = await getLANDiscoveryStatus();
        renderLANDiscoveryList(status);
        return status;
    } catch (error) {
        console.error('Failed to load LAN discovery:', error);
        return null;
    }
}

export async function addDiscoveredLANEndpoint(index) {
    const candidates = Array.isArray(window.__lanDiscoveryCandidates) ? window.__lanDiscoveryCandidates : [];
    const candidate = candidates[index];
    if (!candidate) {
        showNotification(t('modal.lanDiscoveryAddFailed').replace('{error}', 'candidate not found'), 'error');
        return;
    }
    try {
        await addDiscoveredLANEndpointConfig(candidate);
        await refreshLANDiscovery();
        if (typeof window.loadConfig === 'function') {
            await window.loadConfig();
        }
        showNotification(t('modal.lanDiscoveryAddSuccess'), 'success');
    } catch (error) {
        showNotification(t('modal.lanDiscoveryAddFailed').replace('{error}', error), 'error');
    }
}

export function updateNetworkStatus(status) {
    const portInput = document.getElementById('portInput');
    const listenModeInput = document.getElementById('listenModeInput');
    const statusPanel = document.getElementById('networkStatusPanel');
    const connectionsPanel = document.getElementById('networkConnectionsPanel');
    const warning = document.getElementById('networkRiskWarning');

    if (!statusPanel || !connectionsPanel) {
        return;
    }
    if (portInput && status?.port) {
        portInput.value = status.port;
    }
    if (listenModeInput && status?.listenMode) {
        listenModeInput.value = status.listenMode;
    }
    const mode = listenModeInput?.value || status?.listenMode || 'local';
    if (warning) {
        warning.style.display = mode === 'lan' ? 'block' : 'none';
    }
    statusPanel.innerHTML = renderNetworkAddressList(status);
    connectionsPanel.innerHTML = renderNetworkConnections(status);
}

export async function showEditPortModal() {
    document.getElementById('portModal').classList.add('active');
    try {
        const status = await getNetworkStatus();
        updateNetworkStatus(status);
        const listenModeInput = document.getElementById('listenModeInput');
        if (listenModeInput) {
            listenModeInput.onchange = () => {
                const warning = document.getElementById('networkRiskWarning');
                if (warning) {
                    warning.style.display = listenModeInput.value === 'lan' ? 'block' : 'none';
                }
            };
        }
        await refreshLANDiscovery();
    } catch (error) {
        showNotification(t('modal.portUpdateFailed').replace('{error}', error), 'error');
    }
}

export async function savePort() {
    const port = parseInt(document.getElementById('portInput').value);
    const listenMode = document.getElementById('listenModeInput')?.value || 'local';

    if (!port || port < 1 || port > 65535) {
        showNotification(t('modal.portInvalid'), 'error');
        return;
    }

    try {
        await updatePort(port);
        await updateListenMode(listenMode);
        closePortModal();
        window.loadConfig();
        showNotification(t('modal.portUpdateSuccess'), 'success');
    } catch (error) {
        showNotification(t('modal.portUpdateFailed').replace('{error}', error), 'error');
    }
}

export function closePortModal() {
    document.getElementById('portModal').classList.remove('active');
}


// ========== 加群二维码URL配置 ==========
// 上传到图床后填写URL，过期时直接替换图床文件即可自动更新
const CHAT_QRCODE_URL = 'https://gitee.com/hea7en/images/raw/master/group/chat.png';

// 添加时间戳破除缓存
function getQRCodeUrlWithTimestamp(url) {
    const ts = new Date().getTime();
    return url + (url.includes('?') ? '&' : '?') + 't=' + ts;
}
// ===================================

// Welcome Modal
export async function showWelcomeModal() {
    document.getElementById('welcomeModal').classList.add('active');

    try {
        const version = await window.go.main.App.GetVersion();
        document.querySelector('#welcomeModal .modal-header h2').textContent = t('welcome.titleWithVersion').replace('{version}', version);
    } catch (error) {
        console.error('Failed to load version:', error);
    }

    // 通过 Go 后端获取加群二维码图片（绕过 CORS 限制，添加时间戳破除缓存）
    try {
        const urlWithTimestamp = getQRCodeUrlWithTimestamp(CHAT_QRCODE_URL);
        const base64Data = await window.go.main.App.FetchImageAsBase64(urlWithTimestamp);
        if (base64Data) {
            const img = document.getElementById('chatQRCodeImg');
            const tip = document.getElementById('chatQRCodeTip');
            if (img) {
                img.src = base64Data;
            }
            if (tip) {
                tip.textContent = t('welcome.chatGroupTip');
            }
        }
    } catch (error) {
        console.error('Failed to load chat QR code:', error);
    }
}

export function closeWelcomeModal() {
    const dontShowAgain = document.getElementById('dontShowAgain').checked;
    if (dontShowAgain) {
        localStorage.setItem('AINexus_welcomeShown', 'true');
    }
    document.getElementById('welcomeModal').classList.remove('active');
}

// Changelog Modal
export async function showChangelogModal() {
    const modal = document.getElementById('changelogModal');
    const content = document.getElementById('changelogContent');
    if (!modal || !content) return;

    content.innerHTML = `<p>${t('changelog.loading')}</p>`;
    modal.classList.add('active');

    try {
        const lang = await window.go.main.App.GetLanguage();
        const changelogJson = await window.go.main.App.GetChangelog(lang);
        const changelog = JSON.parse(changelogJson);

        let html = '<div class="changelog-timeline">';
        changelog.forEach((item, index) => {
            const position = index % 2 === 0 ? 'left' : 'right';
            html += `
                <div class="timeline-item ${position}">
                    <div class="timeline-dot"></div>
                    <div class="timeline-content">
                        <div class="timeline-header">
                            <span class="timeline-version">${item.version}</span>
                            <span class="timeline-date">${item.date}</span>
                        </div>
                        <ul class="timeline-changes">
                            ${item.changes.map(c => `<li>${c}</li>`).join('')}
                        </ul>
                    </div>
                </div>
            `;
        });
        html += '</div>';
        content.innerHTML = html;
    } catch (error) {
        console.error('Failed to load changelog:', error);
        content.innerHTML = `<p style="color: #e74c3c;">${t('changelog.error')}</p>`;
    }
}

export function closeChangelogModal() {
    document.getElementById('changelogModal').classList.remove('active');
}

export async function showChangelogIfNewVersion() {
    const currentVersion = await window.go.main.App.GetVersion();
    const lastVersion = localStorage.getItem('AINexus_lastVersion');

    if (lastVersion && lastVersion !== currentVersion) {
        setTimeout(() => showChangelogModal(), 600);
    }
    localStorage.setItem('AINexus_lastVersion', currentVersion);
}

// 判断是否为"不支持测试"的情况
function isTestNotSupported(statusCode, message) {
    // 可能不支持测试的 HTTP 状态码
    const notSupportedCodes = [404, 405, 501];
    // 认证错误关键词（如果包含这些，说明是真正的错误，不是不支持测试）
    const authErrorKeywords = ['unauthorized', 'invalid key', 'invalid_api_key', 'authentication', 'api key', 'api_key', 'forbidden', 'permission', 'access denied'];

    if (notSupportedCodes.includes(statusCode)) {
        const lowerMsg = (message || '').toLowerCase();
        // 排除明显的认证错误
        const isAuthError = authErrorKeywords.some(kw => lowerMsg.includes(kw));
        return !isAuthError;
    }
    return false;
}

// Test Result Modal
export async function testEndpointHandler(index, buttonElement) {
    setTestState(buttonElement, index);

    // 获取端点名称用于保存测试状态（兼容详情视图和简洁视图）
    const endpointItem = buttonElement.closest('.endpoint-item') || buttonElement.closest('.endpoint-item-compact');
    const endpointName = endpointItem ? endpointItem.dataset.name : null;

    // 简洁视图：同时更新 moreBtn
    const moreBtn = endpointItem ? endpointItem.querySelector('[data-action="more"]') : null;
    if (moreBtn) {
        moreBtn.disabled = true;
        moreBtn.innerHTML = '⏳';
    }

    try {
        buttonElement.disabled = true;
        buttonElement.innerHTML = '⏳';

        // 使用轻量级测试（优先零消耗方法）
        const result = await testEndpointLight(index);

        const resultContent = document.getElementById('testResultContent');
        const resultTitle = document.getElementById('testResultTitle');

        if (result.success) {
            resultTitle.innerHTML = t('test.successTitle');
            resultContent.innerHTML = `
                <div style="padding: 15px; background: #d4edda; border: 1px solid #c3e6cb; border-radius: 5px; margin-bottom: 15px;">
                    <strong style="color: #155724;">${t('test.connectionSuccess')}</strong>
                </div>
                <div style="padding: 15px; background: #f8f9fa; border-radius: 5px; font-family: monospace; white-space: pre-line; word-break: break-all;">${escapeHtml(result.message)} (${result.method})</div>
            `;
            // 保存测试成功状态
            if (endpointName) {
                saveEndpointTestStatus(endpointName, true);
            }
        } else if (result.status === 'unknown') {
            // 无法确定状态（如三方站限制测试）
            showNotification(t('test.notSupportedMessage'), 'warning');
            // 保存为未知状态
            if (endpointName) {
                saveEndpointTestStatus(endpointName, 'unknown');
            }
            // 清除测试状态，恢复按钮
            clearTestState();
            // 刷新端点列表以更新图标
            if (window.loadConfig) {
                window.loadConfig();
            }
            return; // 不显示测试结果弹窗
        } else {
            resultTitle.innerHTML = t('test.failedTitle');
            resultContent.innerHTML = `
                <div style="padding: 15px; background: #f8d7da; border: 1px solid #f5c6cb; border-radius: 5px; margin-bottom: 15px;">
                    <strong style="color: #721c24;">${t('test.connectionFailed')}</strong>
                </div>
                <div style="padding: 15px; background: #f8f9fa; border-radius: 5px; font-family: monospace; white-space: pre-line; word-break: break-all;"><strong>Error:</strong><br>${escapeHtml(result.message)}</div>
            `;
            // 保存测试失败状态
            if (endpointName) {
                saveEndpointTestStatus(endpointName, false);
            }
        }

        document.getElementById('testResultModal').classList.add('active');
        // 刷新端点列表以更新图标
        if (window.loadConfig) {
            window.loadConfig();
        }

    } catch (error) {
        console.error('Test failed:', error);

        const resultContent = document.getElementById('testResultContent');
        const resultTitle = document.getElementById('testResultTitle');

        resultTitle.innerHTML = t('test.failedTitle');
        resultContent.innerHTML = `
            <div style="padding: 15px; background: #f8d7da; border: 1px solid #f5c6cb; border-radius: 5px; margin-bottom: 15px;">
                <strong style="color: #721c24;">${t('test.testError')}</strong>
            </div>
            <div style="padding: 15px; background: #f8f9fa; border-radius: 5px; font-family: monospace; white-space: pre-line;">${escapeHtml(error.toString())}</div>
        `;

        // 保存测试失败状态（异常情况）
        if (endpointName) {
            saveEndpointTestStatus(endpointName, false);
        }

        document.getElementById('testResultModal').classList.add('active');
        // 刷新端点列表以更新图标
        if (window.loadConfig) {
            window.loadConfig();
        }
    }
}

export function closeTestResultModal() {
    document.getElementById('testResultModal').classList.remove('active');
    clearTestState();
}

export function openArticle() {
    if (window.go?.main?.App) {
        window.go.main.App.OpenURL('https://mp.weixin.qq.com/s/ohtkyIMd5YC7So1q-gE0og');
    }
}
