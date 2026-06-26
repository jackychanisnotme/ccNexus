import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(__dirname, '..');
const uiSource = readFileSync(resolve(frontendRoot, 'src/modules/ui.js'), 'utf8');
const sessionSource = readFileSync(resolve(frontendRoot, 'src/modules/session.js'), 'utf8');
const mainSource = readFileSync(resolve(frontendRoot, 'src/main.js'), 'utf8');
const wailsSource = readFileSync(resolve(frontendRoot, 'wailsjs/go/main/App.js'), 'utf8');

describe('Codex session visibility repair UI', () => {
    it('places the repair button before the AI assistant button in the header', () => {
        const repairIndex = uiSource.indexOf('showCodexVisibilityRepairModal');
        const agentIndex = uiSource.indexOf('showAgentModal');

        assert.notEqual(repairIndex, -1);
        assert.notEqual(agentIndex, -1);
        assert.ok(repairIndex < agentIndex);
    });

    it('calls the Wails repair binding and refreshes sessions after success', () => {
        assert.match(sessionSource, /RepairCodexSessionVisibility/);
        assert.match(sessionSource, /window\.showCodexVisibilityRepairModal\s*=\s*showCodexVisibilityRepairModal/);
        assert.match(sessionSource, /sessionScope:\s*codexVisibilityRepairSessionScope/);
        assert.match(sessionSource, /await\s+RepairCodexSessionVisibility\(JSON\.stringify\(request\)\)/);
        assert.match(sessionSource, /await\s+loadSessions\(\)/);
        assert.match(mainSource, /showCodexVisibilityRepairModal/);
        assert.match(wailsSource, /RepairCodexSessionVisibility/);
    });
});
