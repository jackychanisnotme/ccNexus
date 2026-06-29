import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(__dirname, '..');

const uiSource = readFileSync(resolve(frontendRoot, 'src/modules/ui.js'), 'utf8');
const modalSource = readFileSync(resolve(frontendRoot, 'src/modules/modal.js'), 'utf8');
const cssSource = readFileSync(resolve(frontendRoot, 'src/style.css'), 'utf8');
const mainSource = readFileSync(resolve(frontendRoot, 'src/main.js'), 'utf8');
const settingsSource = readFileSync(resolve(frontendRoot, 'src/modules/settings.js'), 'utf8');
const zhSource = readFileSync(resolve(frontendRoot, 'src/i18n/zh-CN.js'), 'utf8');
const enSource = readFileSync(resolve(frontendRoot, 'src/i18n/en.js'), 'utf8');

describe('startup license gate', () => {
    it('uses AINexus branding in startup license copy', () => {
        assert.match(zhSource, /startupTitle:\s*'AINexus Pro 授权'/);
        assert.match(zhSource, /cardPlaceholder:\s*'请输入 AINexus Pro 在线卡密'/);
        assert.match(enSource, /startupTitle:\s*'AINexus Pro License'/);
        assert.match(enSource, /cardPlaceholder:\s*'Enter AINexus Pro online license card'/);
    });

    it('keeps the startup license modal below the close confirmation dialog', () => {
        assert.match(uiSource, /<div id="startupLicenseModal" class="modal license-gate-modal">/);
        assert.match(cssSource, /\.license-gate-modal\s*{[^}]*z-index:\s*1400;/s);
        assert.match(cssSource, /#confirmDialog\s*{[^}]*z-index:\s*1500;/s);
    });

    it('shows the close confirmation dialog on startup close requests', () => {
        const start = modalSource.indexOf('export function showCloseActionDialog()');
        const end = modalSource.indexOf('export function quitApplication()', start);
        assert.notEqual(start, -1);
        assert.notEqual(end, -1);

        const functionSource = modalSource.slice(start, end);
        assert.match(functionSource, /closeActionDialog/);
        assert.doesNotMatch(functionSource, /window\.go\.main\.App\.Quit\(\)/);
    });

    it('uses the server refresh binding for manual license refreshes', () => {
        assert.match(settingsSource, /getLicenseStatusData\(force = false\)/);
        assert.match(settingsSource, /force\s*\?\s*window\.go\.main\.App\.RefreshLicenseStatus\(\)\s*:\s*window\.go\.main\.App\.GetLicenseStatus\(\)/);
        assert.match(settingsSource, /refreshLicenseStatus\(prefix = 'license', force = true\)/);
    });

    it('uses cached local license status for the automatic startup gate', () => {
        const start = settingsSource.indexOf('export async function showStartupLicenseGate()');
        const end = settingsSource.indexOf('export async function activateStartupLicenseCard()', start);
        assert.notEqual(start, -1);
        assert.notEqual(end, -1);

        const functionSource = settingsSource.slice(start, end);
        assert.match(functionSource, /refreshLicenseStatus\('startupLicense',\s*false\)/);
        assert.doesNotMatch(functionSource, /RefreshLicenseStatus\(\)/);
    });

    it('renders endpoint config before startup stats are loaded', () => {
        const start = mainSource.indexOf("window.addEventListener('DOMContentLoaded'");
        const end = mainSource.indexOf('// Helper function to load config and render endpoints', start);
        assert.notEqual(start, -1);
        assert.notEqual(end, -1);

        const startupSource = mainSource.slice(start, end);
        const endpointViewIndex = startupSource.indexOf('initEndpointViewMode();');
        const configIndex = startupSource.indexOf('await loadConfigAndRender();');
        const statsIndex = startupSource.indexOf("await loadStatsByPeriod('daily')");

        assert.notEqual(endpointViewIndex, -1);
        assert.notEqual(configIndex, -1);
        assert.notEqual(statsIndex, -1);
        assert.ok(endpointViewIndex < configIndex, 'endpoint view mode should be ready before rendering endpoints');
        assert.ok(configIndex < statsIndex, 'endpoint config should render before startup stats load');
    });
});
