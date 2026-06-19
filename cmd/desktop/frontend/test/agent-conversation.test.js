import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import {
    AGENT_SESSIONS_STORAGE_KEY,
    appendAgentMessage,
    appendAgentMessageToSession,
    appendAgentTaskToQueue,
    createAgentSession,
    deleteAgentSession,
    deserializeAgentSessions,
    hasAgentRepairIntent,
    parseAgentSlashCommand,
    shiftNextAgentTask,
    serializeAgentSessions,
} from '../src/modules/agentConversation.js';

const __dirname = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(__dirname, '..');
const agentSource = readFileSync(resolve(frontendRoot, 'src/modules/agent.js'), 'utf8');
const mainSource = readFileSync(resolve(frontendRoot, 'src/main.js'), 'utf8');

describe('agent conversation state', () => {
    it('creates a local chat session and derives a beginner-friendly title from the first user message', () => {
        const now = '2026-06-16T08:00:00.000Z';
        const session = createAgentSession({ now });
        const updated = appendAgentMessage(session, {
            role: 'user',
            content: '帮我检查 Codex 是否能用，如果配置坏了就修复',
            now,
            id: 'msg-1',
        });

        assert.equal(session.messages.length, 0);
        assert.equal(updated.messages.length, 1);
        assert.equal(updated.title, '帮我检查 Codex 是否能用，如果配置坏了就修复');
        assert.equal(updated.updatedAt, now);
    });

    it('round-trips saved sessions while keeping only valid messages', () => {
        const session = appendAgentMessage(createAgentSession({
            id: 'session-1',
            now: '2026-06-16T08:00:00.000Z',
        }), {
            id: 'message-1',
            role: 'assistant',
            content: 'AINexus 已经帮你完成检查。',
            now: '2026-06-16T08:00:02.000Z',
            result: { success: true, toolResults: [] },
        });

        const restored = deserializeAgentSessions(serializeAgentSessions([session]));

        assert.equal(AGENT_SESSIONS_STORAGE_KEY, 'ainexus.agent.sessions.v1');
        assert.equal(restored.length, 1);
        assert.equal(restored[0].id, 'session-1');
        assert.equal(restored[0].messages.length, 1);
        assert.deepEqual(restored[0].messages[0].result, { success: true, toolResults: [] });
    });

    it('detects repair intent from natural language instead of visible target switches', () => {
        assert.equal(hasAgentRepairIntent('请修复本机 agent 配置'), true);
        assert.equal(hasAgentRepairIntent('check whether the local agent config works'), false);
    });

    it('recognizes standalone new-context slash commands only', () => {
        assert.equal(parseAgentSlashCommand('/new'), 'new_chat');
        assert.equal(parseAgentSlashCommand(' /new '), 'new_chat');
        assert.equal(parseAgentSlashCommand('/clear'), 'new_chat');
        assert.equal(parseAgentSlashCommand('/new now'), '');
        assert.equal(parseAgentSlashCommand('请 /new 一下'), '');
        assert.equal(parseAgentSlashCommand('//new'), '');
    });

    it('appends an async assistant reply to the session that started the request', () => {
        const first = appendAgentMessage(createAgentSession({
            id: 'first-chat',
            now: '2026-06-16T08:00:00.000Z',
        }), {
            id: 'first-user',
            role: 'user',
            content: '第一个问题',
            now: '2026-06-16T08:00:01.000Z',
        });
        const fresh = createAgentSession({
            id: 'new-chat',
            now: '2026-06-16T08:00:02.000Z',
        });

        const sessions = appendAgentMessageToSession([fresh, first], 'first-chat', {
            id: 'first-assistant',
            role: 'assistant',
            content: '第一个回复',
            now: '2026-06-16T08:00:03.000Z',
        });

        assert.deepEqual(
            sessions.find(session => session.id === 'first-chat').messages.map(message => message.content),
            ['第一个问题', '第一个回复'],
        );
        assert.equal(sessions.find(session => session.id === 'new-chat').messages.length, 0);
    });

    it('queues multiple tasks for the same conversation in submission order', () => {
        let queue = appendAgentTaskToQueue([], 'chat-1', { task: '第一条' });
        queue = appendAgentTaskToQueue(queue, 'chat-1', { task: '第二条' });
        queue = appendAgentTaskToQueue(queue, 'chat-2', { task: '其他对话' });

        const first = shiftNextAgentTask(queue, 'chat-1');
        const second = shiftNextAgentTask(first.queue, 'chat-1');
        const empty = shiftNextAgentTask(second.queue, 'chat-1');

        assert.equal(first.task.task, '第一条');
        assert.equal(second.task.task, '第二条');
        assert.equal(empty.task, null);
        assert.deepEqual(empty.queue, [{ sessionId: 'chat-2', task: '其他对话', options: {} }]);
    });

    it('deletes one saved conversation without touching the others', () => {
        const first = createAgentSession({ id: 'chat-1', now: '2026-06-16T08:00:00.000Z' });
        const second = createAgentSession({ id: 'chat-2', now: '2026-06-16T08:00:01.000Z' });
        const third = createAgentSession({ id: 'chat-3', now: '2026-06-16T08:00:02.000Z' });

        const sessions = deleteAgentSession([first, second, third], 'chat-2');

        assert.deepEqual(sessions.map(session => session.id), ['chat-1', 'chat-3']);
    });
});

describe('agent chat UI source', () => {
    it('uses a conversation transcript with local history instead of visible config controls', () => {
        assert.match(agentSource, /agentTranscript/);
        assert.match(agentSource, /agentHistory/);
        assert.match(agentSource, /loadAgentSessions/);
        assert.doesNotMatch(agentSource, /id="agentTargets"/);
        assert.doesNotMatch(agentSource, /id="agentCheckButton"/);
        assert.doesNotMatch(agentSource, /id="agentRepairButton"/);
        assert.doesNotMatch(agentSource, /onclick="window\.checkAgentConfigs\(\)"/);
        assert.doesNotMatch(agentSource, /onclick="window\.repairAgentConfigs\(\)"/);
    });

    it('keeps async replies attached to the request session instead of the active chat', () => {
        assert.match(agentSource, /const requestSessionId = requestSession\.id;/);
        assert.match(agentSource, /runAgentTaskForSession\(sessionId,\s*task,\s*options/);
        assert.match(agentSource, /appendToSession\(sessionId,\s*\{\s*role:\s*'assistant'/s);
        assert.doesNotMatch(agentSource, /appendToActiveSession\(\{\s*role:\s*'assistant'/s);
    });

    it('handles /new before calling the model endpoint', () => {
        const start = agentSource.indexOf('export async function runAgent()');
        const end = agentSource.indexOf('export async function checkAgentConfigs()', start);
        assert.notEqual(start, -1);
        assert.notEqual(end, -1);

        const runAgentSource = agentSource.slice(start, end);
        assert.match(runAgentSource, /parseAgentSlashCommand\(task\)/);
        assert.match(runAgentSource, /startNewAgentChat\(\{\s*notice:\s*true,\s*focusPrompt:\s*true\s*\}\)/s);
        assert.ok(runAgentSource.indexOf('parseAgentSlashCommand(task)') < runAgentSource.indexOf('runAgentTask(task)'));
    });

    it('keeps the composer enabled while the current conversation has queued work', () => {
        assert.match(agentSource, /agentTaskQueue/);
        assert.match(agentSource, /processAgentQueueForSession\(requestSessionId\)/);
        assert.match(agentSource, /data-session-id="\$\{escapeHtml\(session\.id\)\}"/);
        assert.match(agentSource, /oninput="window\.handleAgentPromptInput\(this\.dataset\.sessionId,\s*this\.value\)"/);
        assert.match(agentSource, /captureVisibleComposerDraft\(\)/);
        assert.match(mainSource, /handleAgentPromptInput/);
        assert.match(mainSource, /window\.handleAgentPromptInput = handleAgentPromptInput/);
        assert.doesNotMatch(agentSource, /id="agentPrompt"[\s\S]*disabled/);
        assert.doesNotMatch(agentSource, /id="agentRunButton"[\s\S]*disabled/);
    });

    it('adds a separate delete button for local chat history items', () => {
        assert.match(agentSource, /agent-history-row/);
        assert.match(agentSource, /agent-history-delete/);
        assert.match(agentSource, /event\.stopPropagation\(\)/);
        assert.match(agentSource, /window\.deleteAgentChat\(this\.dataset\.sessionId\)/);
        assert.match(mainSource, /deleteAgentChat/);
        assert.match(mainSource, /window\.deleteAgentChat = deleteAgentChat/);
    });

    it('focuses the composer after starting a new chat', () => {
        assert.match(agentSource, /function focusAgentPrompt\(\)/);
        assert.match(agentSource, /document\.getElementById\('agentPrompt'\)/);
        assert.match(agentSource, /\.focus\(\)/);
        assert.match(agentSource, /if \(options\.focusPrompt\) \{\s*focusAgentPrompt\(\);\s*\}/s);
        assert.match(agentSource, /startNewAgentChat\(\{\s*focusPrompt:\s*true\s*\}\)/s);
        assert.match(agentSource, /startNewAgentChat\(\{\s*notice:\s*true,\s*focusPrompt:\s*true\s*\}\)/s);
    });
});
