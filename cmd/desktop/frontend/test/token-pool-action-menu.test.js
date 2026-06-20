import { after, describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const endpointsPath = resolve(__dirname, '../src/modules/endpoints.js');

class FakeClassList {
    constructor() {
        this.values = new Set();
    }

    add(...names) {
        names.forEach((name) => this.values.add(name));
    }

    remove(...names) {
        names.forEach((name) => this.values.delete(name));
    }

    contains(name) {
        return this.values.has(name);
    }
}

class FakeElement {
    constructor(rect = {}) {
        this.children = [];
        this.classList = new FakeClassList();
        this.listeners = new Map();
        this.parentElement = null;
        this.rect = rect;
        this.style = { left: '', top: '' };
    }

    get isConnected() {
        return this === document.body || Boolean(this.parentElement?.isConnected);
    }

    appendChild(child) {
        if (child.parentElement) {
            const index = child.parentElement.children.indexOf(child);
            if (index >= 0) {
                child.parentElement.children.splice(index, 1);
            }
        }
        child.parentElement = this;
        this.children.push(child);
        return child;
    }

    remove() {
        if (!this.parentElement) {
            return;
        }
        const index = this.parentElement.children.indexOf(this);
        if (index >= 0) {
            this.parentElement.children.splice(index, 1);
        }
        this.parentElement = null;
    }

    getBoundingClientRect() {
        return this.rect;
    }

    addEventListener(type, callback) {
        const callbacks = this.listeners.get(type) || [];
        callbacks.push(callback);
        this.listeners.set(type, callbacks);
    }

    dispatch(type, event) {
        (this.listeners.get(type) || []).forEach((callback) => callback(event));
    }

    closest(selector) {
        const className = selector.startsWith('.') ? selector.slice(1) : '';
        let element = this;
        while (element) {
            if (className && element.classList.contains(className)) {
                return element;
            }
            element = element.parentElement;
        }
        return null;
    }

    querySelector(selector) {
        const className = selector.startsWith('.') ? selector.slice(1) : '';
        for (const child of this.children) {
            if (className && child.classList.contains(className)) {
                return child;
            }
            const match = child.querySelector(selector);
            if (match) {
                return match;
            }
        }
        return null;
    }

    querySelectorAll() {
        return [];
    }
}

function createEventTarget(properties = {}) {
    const listeners = new Map();
    return {
        ...properties,
        listeners,
        addEventListener(type, callback, options) {
            const entries = listeners.get(type) || [];
            entries.push({ callback, options });
            listeners.set(type, entries);
        },
        dispatch(type) {
            (listeners.get(type) || []).forEach(({ callback }) => callback());
        }
    };
}

const originalDocument = globalThis.document;
const originalWindow = globalThis.window;
const body = new FakeElement();
globalThis.document = createEventTarget({ body });
globalThis.window = createEventTarget({ innerWidth: 320, innerHeight: 200 });

const dependencyStubs = `
const getLanguage = () => 'en';
const t = (key) => key;
const escapeHtml = (value) => String(value ?? '');
const formatTokens = (value) => String(value ?? '');
const maskApiKey = (value) => value;
const getEndpointStats = () => ({});
const toggleEndpoint = () => {};
const testAllEndpointsZeroCost = () => {};
const filterEndpoints = (value) => value;
const isFilterActive = () => false;
const updateFilterStats = () => {};
`;
const endpointsSource = readFileSync(endpointsPath, 'utf8').replace(/^import .*;\s*$/gm, '')
    + '\nexport { closeAllTokenPoolActionMenus, openTokenPoolActionMenu, bindTokenPoolMoreToggle, loadTokenPoolData };\n';
const endpointsModule = await import(`data:text/javascript;base64,${Buffer.from(dependencyStubs + endpointsSource).toString('base64')}`);

after(() => {
    globalThis.document = originalDocument;
    globalThis.window = originalWindow;
});

function createMenuFixture() {
    const wrap = new FakeElement();
    const button = new FakeElement({ top: 175, right: 315, bottom: 195 });
    const menu = new FakeElement({ width: 100, height: 60 });
    wrap.classList.add('token-pool-more-wrap');
    button.classList.add('token-pool-more-toggle');
    menu.classList.add('token-pool-more-menu');
    body.appendChild(wrap);
    wrap.appendChild(button);
    wrap.appendChild(menu);
    return { wrap, button, menu };
}

function installTokenPoolLoadFixture(getEndpointCredentials) {
    const hint = new FakeElement();
    const stats = new FakeElement();
    const tableBody = new FakeElement();
    const modal = new FakeElement();
    modal.dataset = { language: 'en' };
    modal.querySelector = (selector) => ({
        '#tokenPoolHint': hint,
        '#tokenPoolStats': stats,
        '#tokenPoolTableBody': tableBody
    })[selector] || null;
    document.getElementById = (id) => id === 'tokenPoolModal' ? modal : null;
    window.go = { main: { App: { GetEndpointCredentials: getEndpointCredentials } } };
    window.config = { endpoints: [] };
}

describe('token pool action menu portal lifecycle', () => {
    it('binds the production toggle click to portal and position its menu', () => {
        const { bindTokenPoolMoreToggle, closeAllTokenPoolActionMenus } = endpointsModule;
        const { wrap, button, menu } = createMenuFixture();
        let defaultPrevented = false;
        let propagationStopped = false;

        bindTokenPoolMoreToggle(button);
        button.dispatch('click', {
            preventDefault() {
                defaultPrevented = true;
            },
            stopPropagation() {
                propagationStopped = true;
            }
        });

        assert.equal(defaultPrevented, true);
        assert.equal(propagationStopped, true);
        assert.equal(menu.parentElement, body);
        assert.equal(menu.classList.contains('show'), true);
        assert.equal(menu.classList.contains('token-pool-more-menu-portal'), true);
        assert.equal(menu.style.left, '212px');
        assert.equal(menu.style.top, '111px');
        closeAllTokenPoolActionMenus();
        wrap.remove();
    });

    it('restores the menu through registered outside-click, scroll, and resize callbacks', () => {
        const { openTokenPoolActionMenu } = endpointsModule;
        const clickListener = document.listeners.get('click')?.[0];
        const scrollListener = window.listeners.get('scroll')?.[0];
        const resizeListener = window.listeners.get('resize')?.[0];
        assert.equal(typeof clickListener?.callback, 'function');
        assert.equal(scrollListener?.options, true);
        assert.equal(typeof resizeListener?.callback, 'function');

        for (const listener of [clickListener, scrollListener, resizeListener]) {
            const { wrap, button, menu } = createMenuFixture();
            openTokenPoolActionMenu(button, menu, wrap);
            listener.callback();
            assert.equal(menu.parentElement, wrap);
            assert.equal(menu.classList.contains('show'), false);
            assert.equal(menu.classList.contains('token-pool-more-menu-portal'), false);
            assert.equal(menu.style.left, '');
            assert.equal(menu.style.top, '');
            wrap.remove();
        }
    });

    it('removes a portal menu when its original wrap is disconnected', () => {
        const { openTokenPoolActionMenu, closeAllTokenPoolActionMenus } = endpointsModule;
        const { wrap, button, menu } = createMenuFixture();
        openTokenPoolActionMenu(button, menu, wrap);
        wrap.remove();

        closeAllTokenPoolActionMenus();

        assert.equal(menu.parentElement, null);
        assert.equal(menu.classList.contains('show'), false);
        assert.equal(menu.style.left, '');
        assert.equal(menu.style.top, '');
    });

    it('closes an open menu synchronously when token pool refresh starts', async () => {
        const { loadTokenPoolData, openTokenPoolActionMenu } = endpointsModule;
        const { wrap, button, menu } = createMenuFixture();
        let resolveCredentials;
        const credentialsPending = new Promise((resolve) => {
            resolveCredentials = resolve;
        });
        installTokenPoolLoadFixture(() => credentialsPending);
        openTokenPoolActionMenu(button, menu, wrap);

        const loadPending = loadTokenPoolData(0);

        try {
            assert.equal(menu.parentElement, wrap);
            assert.equal(menu.classList.contains('show'), false);
            assert.equal(menu.classList.contains('token-pool-more-menu-portal'), false);
        } finally {
            resolveCredentials({ success: true, data: { credentials: [], stats: {} } });
            await loadPending;
            wrap.remove();
        }
    });

    it('closes an open menu before a rejected credential request settles', async () => {
        const { loadTokenPoolData, openTokenPoolActionMenu } = endpointsModule;
        const { wrap, button, menu } = createMenuFixture();
        const requestError = new Error('credentials unavailable');
        installTokenPoolLoadFixture(() => Promise.reject(requestError));
        openTokenPoolActionMenu(button, menu, wrap);

        const loadPending = loadTokenPoolData(0);
        const rejection = assert.rejects(loadPending, requestError);

        try {
            assert.equal(menu.parentElement, wrap);
            assert.equal(menu.classList.contains('show'), false);
            assert.equal(menu.classList.contains('token-pool-more-menu-portal'), false);
        } finally {
            await rejection;
            wrap.remove();
        }
    });
});
