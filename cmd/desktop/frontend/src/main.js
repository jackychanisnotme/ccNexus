import './style.css'
import './effects/festival-effects.css'
import './themes/apple.css'
import '../wailsjs/runtime/runtime.js'
import { setLanguage } from './i18n/index.js'
import { initUI, changeLanguage } from './modules/ui.js'
import { loadConfig } from './modules/config.js'
import { loadStats, switchStatsPeriod, loadStatsByPeriod, getCurrentPeriod, updateStatsIncremental, updateEndpointStatsCache, updateTotalStatsCache, initStatsFilters, hasActiveStatsFilters, refreshStatsForCurrentFilter } from './modules/stats.js'
import { renderEndpoints, toggleEndpointPanel, initEndpointSuccessListener, checkAllEndpointsOnStartup, switchEndpointViewMode, initEndpointViewMode, isDropdownOpen, updateEndpointStatsIncremental } from './modules/endpoints.js'
import { loadLogs, toggleLogPanel, changeLogLevel, copyLogs, clearLogs } from './modules/logs.js'
import { showDataSyncDialog } from './modules/webdav.js'
import { initTips } from './modules/tips.js'
import { initTerminal } from './modules/terminal.js'
import { initSession, showCodexVisibilityRepairModal } from './modules/session.js'
import { showSettingsModal, closeSettingsModal, saveSettings, applyTheme, initTheme, showAutoThemeConfigModal, closeAutoThemeConfigModal, saveAutoThemeConfig, refreshLicenseStatus, activateLicenseCard, showStartupLicenseGate, activateStartupLicenseCard } from './modules/settings.js'
import { checkUpdatesOnStartup, checkForUpdates, initUpdateSettings } from './modules/updater.js'
import { initBroadcast } from './modules/broadcast.js'
import { initFilterDropdowns, clearAllFilters } from './modules/filters.js'
import { showAgentProviderModal, closeAgentProviderModal, selectAllAgentProviders, applyAgentProviderConfig, restoreAgentProviderBackup, openAgentProviderRestorePicker, closeAgentProviderRestorePicker, selectAllAgentProviderRestoreTargets, confirmAgentProviderRestore } from './modules/agentProvider.js'
import { showAgentModal, closeAgentModal, runAgent, checkAgentConfigs, repairAgentConfigs, newAgentChat, selectAgentSession, deleteAgentChat, handleAgentComposerKeydown, handleAgentPromptInput } from './modules/agent.js'
import { formatTokens } from './utils/format.js'
import { summarizeRequestStats } from './utils/stats.js'
import {
    showAddEndpointModal,
    showAddEndpointModalWithPreset,
    editEndpoint,
    saveEndpoint,
    openEndpointTokenPoolFromModal,
    deleteEndpoint,
    closeModal,
    handleAuthModeChange,
    handleTransformerChange,
    handleThinkingControlChange,
    fetchModels,
    initModelInputEvents,
    toggleModelDropdown,
    showEditPortModal,
    savePort,
    closePortModal,
    updateNetworkStatus,
    updateLANDiscoveryStatus,
    loadLANDiscoveryStatus,
    refreshLANDiscovery,
    addDiscoveredLANEndpoint,
    showChangelogModal,
    closeChangelogModal,
    showChangelogIfNewVersion,
    testEndpointHandler,
    closeTestResultModal,
    togglePasswordVisibility,
    acceptConfirm,
    cancelConfirm,
    showCloseActionDialog,
    quitApplication,
    minimizeToTray
} from './modules/modal.js'

// Handle real-time stats update events from backend (4-period data)
function handleStatsUpdate(data) {
    if (!data || !data.endpointName) {
        return;
    }
    if (hasActiveStatsFilters()) {
        refreshStatsForCurrentFilter();
        return;
    }

    // Update all 4-period caches first (before DOM updates)
    if (data.endpoint) {
        updateEndpointStatsCache(data.endpointName, data.endpoint);
    }
    if (data.totals) {
        updateTotalStatsCache(data.totals);
    }

    const currentPeriod = getCurrentPeriod(); // Get current selected period

    // 1. Update header stats (top summary) using backend-provided aggregated data
    const totalStats = data.totals && data.totals[currentPeriod];
    if (totalStats) {
        const requestSummary = summarizeRequestStats(totalStats.requests, totalStats.errors);
        const totalRequestsEl = document.getElementById('periodTotalRequests');
        const successRequestsEl = document.getElementById('periodSuccess');
        const failedRequestsEl = document.getElementById('periodFailed');
        const totalTokensEl = document.getElementById('periodTotalTokens');
        const totalInputTokensEl = document.getElementById('periodInputTokens');
        const totalOutputTokensEl = document.getElementById('periodOutputTokens');

        if (totalRequestsEl) totalRequestsEl.textContent = requestSummary.total;
        if (successRequestsEl) successRequestsEl.textContent = requestSummary.success;
        if (failedRequestsEl) failedRequestsEl.textContent = requestSummary.failed;
        if (totalTokensEl) totalTokensEl.textContent = formatTokens(totalStats.inputTokens + totalStats.outputTokens);
        if (totalInputTokensEl) totalInputTokensEl.textContent = formatTokens(totalStats.inputTokens);
        if (totalOutputTokensEl) totalOutputTokensEl.textContent = formatTokens(totalStats.outputTokens);
    }

    // 2. Update endpoint card using single endpoint period data
    const endpointStats = data.endpoint && data.endpoint[currentPeriod];
    if (endpointStats) {
        updateEndpointStatsIncremental(data.endpointName, endpointStats);
    }
}

// Load data on startup
window.addEventListener('DOMContentLoaded', async () => {
    // Wait for Wails runtime to be ready
    while (!window.go) {
        await new Promise(resolve => setTimeout(resolve, 100));
    }

    // Initialize language
    const lang = await window.go.main.App.GetLanguage();
    setLanguage(lang);

    // Initialize theme (supports auto mode)
    await initTheme();

    // Initialize UI
    initUI();

    // Listen for close dialog event from backend before any startup gate can block interaction
    if (window.runtime) {
        window.runtime.EventsOn('show-close-dialog', () => {
            showCloseActionDialog();
        });
    }

    // Initialize endpoint view mode
    initEndpointViewMode();

    // Render local endpoint config before any network-backed startup work.
    await loadConfigAndRender();

    await showStartupLicenseGate();

    // Initialize filter dropdowns
    initFilterDropdowns();
    await initStatsFilters();

    // Initialize terminal module
    initTerminal();

    // Initialize session module
    initSession();

    // Initialize model input events
    initModelInputEvents();

    // Load and display version
    try {
        const version = await window.go.main.App.GetVersion();
        document.getElementById('appVersion').textContent = version;
    } catch (error) {
        console.error('Failed to get version:', error);
    }

    // Load initial stats after endpoints are visible, then refresh endpoint cards with stats.
    await loadStatsByPeriod('daily'); // Load today's stats by default (ensure initialization completes before events)
    await loadConfigAndRender();

    // Restore log level from config
    try {
        const logLevel = await window.go.main.App.GetLogLevel();
        document.getElementById('logLevel').value = logLevel;
    } catch (error) {
        console.error('Failed to get log level:', error);
    }

    loadLogs();

    // Initialize tips
    initTips();

    // Initialize endpoint success listener
    initEndpointSuccessListener();

    // Check all endpoints on startup (zero-cost methods only)
    checkAllEndpointsOnStartup();

    // Listen for real-time stats updates from backend
    if (window.runtime && window.runtime.EventsOn) {
        window.runtime.EventsOn('stats:updated', (data) => {
            handleStatsUpdate(data);
        });
        window.runtime.EventsOn('network:updated', (data) => {
            updateNetworkStatus(data);
        });
        window.runtime.EventsOn('lan-discovery:updated', (data) => {
            updateLANDiscoveryStatus(data);
        });
        window.runtime.EventsOn('remote:config-updated', async () => {
            await loadConfigAndRender();
        });
    }

    // Fallback: If event-based updates fail, uncomment the following to restore polling
    // setInterval(async () => {
    //     await loadStats(); // Refresh cumulative stats for endpoint cards
    //     const currentPeriod = getCurrentPeriod(); // Get current selected period
    //     await loadStatsByPeriod(currentPeriod); // Refresh period stats (daily/weekly/monthly)
    //     // 如果下拉菜单打开，跳过渲染避免菜单消失
    //     if (isDropdownOpen()) {
    //         return;
    //     }
    //     const config = await window.go.main.App.GetConfig();
    //     if (config) {
    //         renderEndpoints(JSON.parse(config).endpoints);
    //     }
    // }, 30000); // 降低频率到 30 秒

    // Refresh logs every 2 seconds
    setInterval(loadLogs, 2000);

    // showChangelogIfNewVersion(); // 暂时禁用自动弹窗

    // Check for updates on startup
    checkUpdatesOnStartup();

    // Initialize broadcast banner
    initBroadcast();
    loadLANDiscoveryStatus();


    // Initialize update settings
    initUpdateSettings();

    // Handle Cmd/Ctrl+W to hide window
    window.addEventListener('keydown', (e) => {
        if ((e.metaKey || e.ctrlKey) && e.key === 'w') {
            e.preventDefault();
            window.runtime.WindowHide();
        }
    });
});

// Helper function to load config and render endpoints
async function loadConfigAndRender() {
    const config = await loadConfig();
    if (config) {
        await renderEndpoints(config.endpoints);
    }
}

// Expose functions to window for onclick handlers
window.loadConfig = loadConfigAndRender;
window.showAddEndpointModal = showAddEndpointModal;
window.showAddEndpointModalWithPreset = showAddEndpointModalWithPreset;
window.editEndpoint = editEndpoint;
window.saveEndpoint = saveEndpoint;
window.openEndpointTokenPoolFromModal = openEndpointTokenPoolFromModal;
window.deleteEndpoint = deleteEndpoint;
window.closeModal = closeModal;
window.handleAuthModeChange = handleAuthModeChange;
window.handleTransformerChange = handleTransformerChange;
window.handleThinkingControlChange = handleThinkingControlChange;
window.fetchModels = fetchModels;
window.toggleModelDropdown = toggleModelDropdown;
window.showEditPortModal = showEditPortModal;
window.savePort = savePort;
window.closePortModal = closePortModal;
window.refreshLANDiscovery = refreshLANDiscovery;
window.addDiscoveredLANEndpoint = addDiscoveredLANEndpoint;
window.showChangelogModal = showChangelogModal;
window.closeChangelogModal = closeChangelogModal;
window.testEndpoint = testEndpointHandler;
window.closeTestResultModal = closeTestResultModal;
window.toggleLogPanel = toggleLogPanel;
window.changeLogLevel = changeLogLevel;
window.copyLogs = copyLogs;
window.clearLogs = clearLogs;
window.changeLanguage = changeLanguage;
window.togglePasswordVisibility = togglePasswordVisibility;
window.acceptConfirm = acceptConfirm;
window.checkForUpdates = checkForUpdates;
window.cancelConfirm = cancelConfirm;
window.showCloseActionDialog = showCloseActionDialog;
window.quitApplication = quitApplication;
window.minimizeToTray = minimizeToTray;
window.showDataSyncDialog = showDataSyncDialog;
window.showCodexVisibilityRepairModal = showCodexVisibilityRepairModal;
window.showAgentModal = showAgentModal;
window.closeAgentModal = closeAgentModal;
window.runAgent = runAgent;
window.checkAgentConfigs = checkAgentConfigs;
window.repairAgentConfigs = repairAgentConfigs;
window.newAgentChat = newAgentChat;
window.selectAgentSession = selectAgentSession;
window.deleteAgentChat = deleteAgentChat;
window.handleAgentComposerKeydown = handleAgentComposerKeydown;
window.handleAgentPromptInput = handleAgentPromptInput;
window.showAgentProviderModal = showAgentProviderModal;
window.closeAgentProviderModal = closeAgentProviderModal;
window.selectAllAgentProviders = selectAllAgentProviders;
window.applyAgentProviderConfig = applyAgentProviderConfig;
window.restoreAgentProviderBackup = restoreAgentProviderBackup;
window.openAgentProviderRestorePicker = openAgentProviderRestorePicker;
window.closeAgentProviderRestorePicker = closeAgentProviderRestorePicker;
window.selectAllAgentProviderRestoreTargets = selectAllAgentProviderRestoreTargets;
window.confirmAgentProviderRestore = confirmAgentProviderRestore;
window.switchStatsPeriod = switchStatsPeriod;
window.toggleEndpointPanel = toggleEndpointPanel;
window.switchEndpointViewMode = switchEndpointViewMode;
window.clearAllFilters = clearAllFilters;
window.showSettingsModal = showSettingsModal;
window.closeSettingsModal = closeSettingsModal;
window.saveSettings = saveSettings;
window.showAutoThemeConfigModal = showAutoThemeConfigModal;
window.closeAutoThemeConfigModal = closeAutoThemeConfigModal;
window.saveAutoThemeConfig = saveAutoThemeConfig;
window.refreshLicenseStatus = refreshLicenseStatus;
window.activateLicenseCard = activateLicenseCard;
window.showStartupLicenseGate = showStartupLicenseGate;
window.activateStartupLicenseCard = activateStartupLicenseCard;

// History modal functions
window.closeHistoryModal = async () => {
    const { closeHistoryModal } = await import('./modules/history.js');
    closeHistoryModal();
};

window.deleteHistoryArchive = async () => {
    const { deleteHistoryArchive } = await import('./modules/history.js');
    deleteHistoryArchive();
};
