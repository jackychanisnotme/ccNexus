import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const componentRoot = resolve(__dirname);
const dashboardSource = readFileSync(resolve(componentRoot, 'dashboard.js'), 'utf8');
const cssSource = readFileSync(resolve(__dirname, '..', '..', 'css', 'components.css'), 'utf8');
const zhSource = readFileSync(resolve(__dirname, '..', 'i18n', 'zh-CN.js'), 'utf8');
const enSource = readFileSync(resolve(__dirname, '..', 'i18n', 'en.js'), 'utf8');

describe('agent provider dashboard backup history', () => {
    it('renders a backup history selector instead of a single latest backup label', () => {
        assert.match(dashboardSource, /status\.backups/);
        assert.match(dashboardSource, /agentProvider\.backupHistory/);
        assert.match(dashboardSource, /agent-provider-backup-select/);
        assert.match(dashboardSource, /agent-provider-restore-targets/);
        assert.match(dashboardSource, /openAgentProviderRestorePicker/);
        assert.match(cssSource, /\.agent-provider-backup-select/);
        assert.match(cssSource, /\.agent-provider-restore-targets/);
    });

    it('localizes the backup history picker copy', () => {
        assert.match(zhSource, /backupHistory:\s*'历史备份'/);
        assert.match(zhSource, /selectBackup:\s*'选择备份'/);
        assert.match(zhSource, /restoreTargets:\s*'选择恢复目标'/);
        assert.match(zhSource, /noBackup:\s*'暂无备份'/);

        assert.match(enSource, /backupHistory:\s*'Backup history'/);
        assert.match(enSource, /selectBackup:\s*'Select backup'/);
        assert.match(enSource, /restoreTargets:\s*'Choose restore targets'/);
        assert.match(enSource, /noBackup:\s*'No backup'/);
    });
});
