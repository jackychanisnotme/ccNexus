import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const componentRoot = resolve(__dirname);
const endpointsSource = readFileSync(resolve(componentRoot, 'endpoints.js'), 'utf8');
const zhSource = readFileSync(resolve(__dirname, '..', 'i18n', 'zh-CN.js'), 'utf8');
const enSource = readFileSync(resolve(__dirname, '..', 'i18n', 'en.js'), 'utf8');

describe('server endpoint Codex fast mode form', () => {
    it('renders and syncs Codex fast mode only for Codex Token Pool endpoints', () => {
        assert.match(endpointsSource, /id="endpoint-codex-fast-mode-group"/);
        assert.match(endpointsSource, /name="codexFastMode"/);
        assert.match(endpointsSource, /endpoint\?\.codexFastMode/);
        assert.match(endpointsSource, /codexFastModeGroup[\s\S]*mode === 'codex_token_pool'/);
        assert.match(endpointsSource, /codexFastModeInput[\s\S]*disabled = mode !== 'codex_token_pool'/);
    });

    it('saves and clones the Codex fast mode value', () => {
        assert.match(endpointsSource, /data\.codexFastMode = data\.authMode === 'codex_token_pool' && formData\.get\('codexFastMode'\) === 'on'/);
        assert.match(endpointsSource, /codexFastMode:\s*!!endpoint\.codexFastMode/);
        assert.match(zhSource, /codexFastMode:\s*'快速模式'/);
        assert.match(enSource, /codexFastMode:\s*'Fast mode'/);
        assert.doesNotMatch(zhSource, /1\.5\s*倍/);
        assert.doesNotMatch(enSource, /1\.5x/);
    });
});
