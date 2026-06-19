export const AGENT_SESSIONS_STORAGE_KEY = 'ainexus.agent.sessions.v1';

const DEFAULT_SESSION_TITLE = 'New chat';
const MAX_TITLE_LENGTH = 60;
const MAX_SESSIONS = 30;
const MAX_MESSAGES_PER_SESSION = 200;
const VALID_ROLES = new Set(['user', 'assistant', 'system']);
const REPAIR_INTENT_PATTERN = /repair|fix|修复|修理|修正/i;
const NEW_CONTEXT_COMMANDS = new Set(['/new', '/clear']);

function nowIso() {
    return new Date().toISOString();
}

function generateId(prefix) {
    const random = globalThis.crypto?.randomUUID?.() || Math.random().toString(36).slice(2, 12);
    return `${prefix}-${random}`;
}

function normalizeText(value) {
    return String(value ?? '').trim();
}

function titleFromMessage(content) {
    const normalized = normalizeText(content).replace(/\s+/g, ' ');
    if (!normalized) {
        return DEFAULT_SESSION_TITLE;
    }
    if (normalized.length <= MAX_TITLE_LENGTH) {
        return normalized;
    }
    return `${normalized.slice(0, MAX_TITLE_LENGTH - 1)}…`;
}

function isPlainObject(value) {
    return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function normalizeMessage(message) {
    if (!isPlainObject(message) || !VALID_ROLES.has(message.role)) {
        return null;
    }
    const content = normalizeText(message.content);
    if (!content) {
        return null;
    }
    return {
        id: normalizeText(message.id) || generateId('message'),
        role: message.role,
        content,
        createdAt: normalizeText(message.createdAt) || nowIso(),
        ...(message.result !== undefined ? { result: message.result } : {}),
    };
}

function normalizeSession(session) {
    if (!isPlainObject(session)) {
        return null;
    }
    const createdAt = normalizeText(session.createdAt) || nowIso();
    const messages = Array.isArray(session.messages)
        ? session.messages.map(normalizeMessage).filter(Boolean).slice(-MAX_MESSAGES_PER_SESSION)
        : [];
    const title = normalizeText(session.title)
        || messages.find(message => message.role === 'user')?.content
        || DEFAULT_SESSION_TITLE;

    return {
        id: normalizeText(session.id) || generateId('session'),
        title: titleFromMessage(title),
        createdAt,
        updatedAt: normalizeText(session.updatedAt) || createdAt,
        messages,
    };
}

export function createAgentSession(options = {}) {
    const timestamp = options.now || nowIso();
    return {
        id: options.id || generateId('session'),
        title: options.title || DEFAULT_SESSION_TITLE,
        createdAt: timestamp,
        updatedAt: timestamp,
        messages: [],
    };
}

export function appendAgentMessage(session, message) {
    const normalizedSession = normalizeSession(session) || createAgentSession();
    const timestamp = message?.now || nowIso();
    const normalizedMessage = normalizeMessage({
        ...message,
        createdAt: message?.createdAt || timestamp,
    });
    if (!normalizedMessage) {
        return normalizedSession;
    }

    const messages = [...normalizedSession.messages, normalizedMessage].slice(-MAX_MESSAGES_PER_SESSION);
    const shouldUseMessageTitle = normalizedSession.title === DEFAULT_SESSION_TITLE
        && normalizedMessage.role === 'user'
        && normalizedSession.messages.every(existing => existing.role !== 'user');

    return {
        ...normalizedSession,
        title: shouldUseMessageTitle ? titleFromMessage(normalizedMessage.content) : normalizedSession.title,
        updatedAt: timestamp,
        messages,
    };
}

export function appendAgentMessageToSession(sessions, sessionId, message) {
    let found = false;
    const updatedSessions = (Array.isArray(sessions) ? sessions : []).map(session => {
        const normalizedSession = normalizeSession(session);
        if (!normalizedSession || normalizedSession.id !== sessionId) {
            return normalizedSession;
        }
        found = true;
        return appendAgentMessage(normalizedSession, message);
    }).filter(Boolean);

    if (found) {
        return updatedSessions;
    }
    return updatedSessions;
}

export function deleteAgentSession(sessions, sessionId) {
    const normalizedSessionId = normalizeText(sessionId);
    if (!normalizedSessionId) {
        return Array.isArray(sessions) ? sessions.map(normalizeSession).filter(Boolean) : [];
    }
    return (Array.isArray(sessions) ? sessions : [])
        .map(normalizeSession)
        .filter(session => session && session.id !== normalizedSessionId);
}

export function appendAgentTaskToQueue(queue, sessionId, task) {
    const normalizedSessionId = normalizeText(sessionId);
    const normalizedTask = normalizeText(task?.task);
    if (!normalizedSessionId || !normalizedTask) {
        return Array.isArray(queue) ? [...queue] : [];
    }
    return [
        ...(Array.isArray(queue) ? queue : []),
        {
            sessionId: normalizedSessionId,
            task: normalizedTask,
            options: isPlainObject(task?.options) ? task.options : {},
        },
    ];
}

export function shiftNextAgentTask(queue, sessionId) {
    const normalizedSessionId = normalizeText(sessionId);
    const source = Array.isArray(queue) ? queue : [];
    const index = source.findIndex(item => item?.sessionId === normalizedSessionId);
    if (index < 0) {
        return { task: null, queue: [...source] };
    }
    return {
        task: source[index],
        queue: [...source.slice(0, index), ...source.slice(index + 1)],
    };
}

export function serializeAgentSessions(sessions) {
    const normalized = Array.isArray(sessions)
        ? sessions.map(normalizeSession).filter(Boolean).slice(0, MAX_SESSIONS)
        : [];
    return JSON.stringify(normalized);
}

export function deserializeAgentSessions(raw) {
    if (!raw) {
        return [];
    }
    try {
        const parsed = JSON.parse(raw);
        if (!Array.isArray(parsed)) {
            return [];
        }
        return parsed.map(normalizeSession).filter(Boolean).slice(0, MAX_SESSIONS);
    } catch {
        return [];
    }
}

export function loadAgentSessions(storage = globalThis.localStorage) {
    try {
        return deserializeAgentSessions(storage?.getItem?.(AGENT_SESSIONS_STORAGE_KEY));
    } catch {
        return [];
    }
}

export function saveAgentSessions(sessions, storage = globalThis.localStorage) {
    try {
        storage?.setItem?.(AGENT_SESSIONS_STORAGE_KEY, serializeAgentSessions(sessions));
        return true;
    } catch {
        return false;
    }
}

export function hasAgentRepairIntent(task) {
    return REPAIR_INTENT_PATTERN.test(String(task ?? ''));
}

export function parseAgentSlashCommand(input) {
    const command = normalizeText(input).toLowerCase();
    if (NEW_CONTEXT_COMMANDS.has(command)) {
        return 'new_chat';
    }
    return '';
}
