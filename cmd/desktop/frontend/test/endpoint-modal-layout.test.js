import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(__dirname, '..');

const uiSource = readFileSync(resolve(frontendRoot, 'src/modules/ui.js'), 'utf8');
const modalSource = readFileSync(resolve(frontendRoot, 'src/modules/modal.js'), 'utf8');
const cssSource = readFileSync(resolve(frontendRoot, 'src/style.css'), 'utf8');

describe('endpoint modal option layout', () => {
    it('uses horizontal option labels for reasoning and force-stream checkboxes', () => {
        assert.match(
            uiSource,
            /<label class="endpoint-option-label">\s*<input type="checkbox" id="endpointThinkingEnabled"/,
        );
        assert.match(
            uiSource,
            /<label class="endpoint-option-label">\s*<input type="checkbox" id="endpointForceStream"/,
        );
    });

    it('keeps endpoint option checkboxes from taking the full form width', () => {
        assert.match(cssSource, /\.form-group\s+\.endpoint-option-label\s*{[^}]*display:\s*inline-flex;/s);
        assert.match(cssSource, /\.form-group\s+\.endpoint-option-label\s*{[^}]*align-items:\s*center;/s);
        assert.match(cssSource, /\.form-group\s+\.endpoint-option-label\s*{[^}]*word-break:\s*keep-all;/s);
        assert.match(
            cssSource,
            /\.form-group\s+\.endpoint-option-label\s+input\[type="checkbox"\]\s*{[^}]*width:\s*auto;/s,
        );
        assert.match(
            cssSource,
            /\.form-group\s+\.endpoint-option-label\s+input\[type="checkbox"\]\s*{[^}]*flex-shrink:\s*0;/s,
        );
    });

    it('keeps the edit endpoint modal open while opening token pool management', () => {
        const start = modalSource.indexOf('export async function openEndpointTokenPoolFromModal()');
        const end = modalSource.indexOf('export async function saveEndpoint()', start);
        assert.notEqual(start, -1);
        assert.notEqual(end, -1);

        const functionSource = modalSource.slice(start, end);
        assert.doesNotMatch(functionSource, /\bcloseModal\(\);/);
        assert.match(functionSource, /\bopenTokenPoolModal\(/);
    });
});
