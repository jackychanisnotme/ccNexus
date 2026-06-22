import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const uiSource = readFileSync(resolve(__dirname, '../src/modules/ui.js'), 'utf8');
const cssSource = readFileSync(resolve(__dirname, '../src/style.css'), 'utf8');

test('renders the AINexus brand icon in the header', () => {
    assert.match(uiSource, /class="app-brand-icon"/);
    assert.match(uiSource, /src="\/ainexus-icon\.png"/);
    assert.doesNotMatch(uiSource, /<h1>🚀/);
});

test('sizes the AINexus header brand icon', () => {
    assert.match(cssSource, /\.app-brand-icon\s*{/);
    assert.match(cssSource, /\.app-brand-icon\s*{[^}]*width:\s*44px/s);
    assert.match(cssSource, /\.app-brand-icon\s*{[^}]*height:\s*44px/s);
});
