import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(__dirname, '..');

const sources = [
    'src/main.js',
    'src/modules/ui.js',
    'src/modules/modal.js',
    'src/style.css',
    'src/i18n/en.js',
    'src/i18n/zh-CN.js',
    'src/themes/apple.css',
    'src/themes/aurora.css',
    'src/themes/cyberpunk.css',
    'src/themes/dark.css',
    'src/themes/green.css',
    'src/themes/holographic.css',
    'src/themes/mocha.css',
    'src/themes/ocean.css',
    'src/themes/quantum.css',
    'src/themes/sakura.css',
    'src/themes/starry.css',
    'src/themes/sunset.css',
];

describe('welcome modal removal', () => {
    for (const sourcePath of sources) {
        it(`does not keep welcome modal code in ${sourcePath}`, () => {
            const source = readFileSync(resolve(frontendRoot, sourcePath), 'utf8');

            assert.doesNotMatch(source, /welcomeModal/);
            assert.doesNotMatch(source, /showWelcomeModal/);
            assert.doesNotMatch(source, /closeWelcomeModal/);
            assert.doesNotMatch(source, /AINexus_welcomeShown/);
            assert.doesNotMatch(source, /\bwelcome\s*:/);
        });
    }
});
