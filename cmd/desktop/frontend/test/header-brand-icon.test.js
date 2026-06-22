import { test } from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const testDir = dirname(fileURLToPath(import.meta.url));
const uiSource = readFileSync(resolve(testDir, '../src/modules/ui.js'), 'utf8');
const cssSource = readFileSync(resolve(testDir, '../src/style.css'), 'utf8');
const headerTemplate = uiSource.match(
    /<div class="header">[\s\S]*?<\/div>\s*<div class="container">/,
)?.[0] ?? '';

test('renders the AINexus brand icon in the header', () => {
    assert.match(
        headerTemplate,
        /<img class="app-brand-icon" src="\/ainexus-icon\.png" alt="" aria-hidden="true">/,
    );
    assert.doesNotMatch(uiSource, /<h1>🚀/);
});

test('ships the AINexus header brand icon asset', () => {
    assert.equal(existsSync(resolve(testDir, '../public/ainexus-icon.png')), true);
});

test('sizes the AINexus header brand icon', () => {
    assert.match(cssSource, /\.app-brand-icon\s*{/);
    assert.match(cssSource, /\.app-brand-icon\s*{[^}]*width:\s*44px/s);
    assert.match(cssSource, /\.app-brand-icon\s*{[^}]*height:\s*44px/s);
});
