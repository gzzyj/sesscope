#!/usr/bin/env node
/**
 * claude 适配器 ↔ 官方 Agent SDK 的一致性校验。
 *
 * 用 SDK 的 listSessions()/getSessionMessages() 作为"官方语义"，和 sesscope 的
 * sessions/export 逐会话比对：id 集合、标题、目录、时间、assistant/人类 user 消息序列。
 *
 * 用法：
 *   node tools/claude-sdk-conformance.mjs
 *   CLAUDE_AGENT_SDK=/path/to/@anthropic-ai/claude-agent-sdk/sdk.mjs node tools/...
 *   SESSCOPE_BIN=./out/sesscope CONFORMANCE_LIMIT=10 node tools/...
 *
 * SDK 未安装时退出码 2 并给出安装命令。
 */
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.dirname(HERE);
const BIN = process.env.SESSCOPE_BIN || path.join(ROOT, 'out', 'sesscope');
const LIMIT = Number(process.env.CONFORMANCE_LIMIT || '5');
// 注入文本：CLI 回显/中断标记。slash 命令（<command-name>/exit</command-name>）算用户输入，
// 所以这里只排除 <local-command-*>，不排除 <command-*>。
const INJECTED = (text) => {
  const trimmed = String(text || '').trim();
  return trimmed.startsWith('<local-command-') || trimmed.startsWith('[Request interrupted by user');
};

async function loadSdk() {
  const candidates = [];
  if (process.env.CLAUDE_AGENT_SDK) {
    candidates.push(pathToFileURL(path.resolve(process.env.CLAUDE_AGENT_SDK)).href);
  }
  candidates.push('@anthropic-ai/claude-agent-sdk');
  let lastError = null;
  for (const spec of candidates) {
    try {
      return await import(spec);
    } catch (error) {
      lastError = error;
    }
  }
  console.error('找不到 @anthropic-ai/claude-agent-sdk：' + (lastError && lastError.message));
  console.error('安装：npm i --no-save @anthropic-ai/claude-agent-sdk');
  console.error('或指定入口：CLAUDE_AGENT_SDK=/path/to/sdk.mjs');
  process.exit(2);
}

function run(args) {
  // 导出摘要走 stderr；这里只要 stdout 的 JSON，别把摘要混进报告。
  return execFileSync(BIN, args, { encoding: 'utf8', maxBuffer: 64 << 20, stdio: ['ignore', 'pipe', 'pipe'] });
}

function fail(list, message) {
  list.push(message);
}

const sdk = await loadSdk();
const { listSessions, getSessionMessages } = sdk;

const sdkSessions = await listSessions();
const ours = JSON.parse(run(['sessions', '--source', 'claude', '-n', '0', '-j'])).result.Items;
const oursById = new Map(ours.map((item) => [item.ref.id, item]));
const sdkById = new Map(sdkSessions.map((item) => [item.sessionId, item]));

const errors = [];
let checked = 0;

if (sdkSessions.length !== ours.length) {
  fail(errors, '会话数不一致：SDK ' + sdkSessions.length + ' / sesscope ' + ours.length);
}
for (const id of sdkById.keys()) {
  if (!oursById.has(id)) fail(errors, 'SDK 有、sesscope 没有：' + id);
}
for (const id of oursById.keys()) {
  if (!sdkById.has(id)) fail(errors, 'sesscope 有、SDK 没有：' + id);
}

const ids = [...sdkById.keys()].filter((id) => oursById.has(id));
for (const id of ids) {
  const sdkItem = sdkById.get(id);
  const ourItem = oursById.get(id);
  const title = ourItem.title || '';
  if (title && sdkItem.summary && !sdkItem.summary.startsWith(title) && !title.startsWith(sdkItem.summary.slice(0, title.length))) {
    fail(errors, id + '：标题 " " 不一致（SDK summary=' + JSON.stringify(sdkItem.summary) + '，sesscope title=' + JSON.stringify(title) + '）');
  }
  if (sdkItem.cwd && ourItem.directory && sdkItem.cwd !== ourItem.directory) {
    fail(errors, id + '：目录不一致（SDK ' + sdkItem.cwd + ' / sesscope ' + ourItem.directory + '）');
  }
  const created = ourItem.created_ms;
  if (created && sdkItem.createdAt && Math.abs(created - sdkItem.createdAt) > 2000) {
    fail(errors, id + '：创建时间差 ' + Math.abs(created - sdkItem.createdAt) + 'ms');
  }
  const updated = ourItem.updated_ms;
  if (updated && sdkItem.lastModified && Math.abs(updated - sdkItem.lastModified) > 2000) {
    fail(errors, id + '：最后活动时间差 ' + Math.abs(updated - sdkItem.lastModified) + 'ms');
  }
}

const sample = ids.slice(0, LIMIT);
for (const id of sample) {
  checked += 1;
  const sdkMessages = (await getSessionMessages(id)).filter((message) => !message.parent_tool_use_id);
  const exported = JSON.parse(run(['export', '--session', 'claude:' + id, '--out', '-'])).session;

  const sdkAssistants = sdkMessages.filter((message) => message.type === 'assistant').map((message) => message.uuid);
  const ourAssistants = exported.messages.filter((message) => message.role === 'assistant').map((message) => message.id);
  if (JSON.stringify(sdkAssistants) !== JSON.stringify(ourAssistants)) {
    fail(errors, id + '：assistant 消息序列不一致（SDK ' + sdkAssistants.length + ' / sesscope ' + ourAssistants.length + '）');
  }

  const sdkHumans = sdkMessages
    .filter((message) => message.type === 'user')
    .filter((message) => {
      const content = message.message && message.message.content;
      const texts = typeof content === 'string' ? [content] : Array.isArray(content) ? content.filter((block) => block && block.type === 'text').map((block) => block.text) : [];
      return texts.some((text) => !INJECTED(text));
    })
    .map((message) => message.uuid);
  const ourHumans = exported.messages.filter((message) => message.role === 'user').map((message) => message.id);
  if (JSON.stringify(sdkHumans) !== JSON.stringify(ourHumans)) {
    fail(errors, id + '：人类输入序列不一致（SDK ' + sdkHumans.length + ' / sesscope ' + ourHumans.length + '）');
  }

  // SDK 默认把 isMeta 记录排除在消息之外；我们把本地命令/isMeta 都保留成 system。
  // 所以不要求数量相等，只要求“SDK 返回的每条注入记录，我们都能按 uuid 找到”。
  const sdkInjected = sdkMessages
    .filter((message) => message.type === 'user')
    .filter((message) => {
      const content = message.message && message.message.content;
      const texts = typeof content === 'string' ? [content] : Array.isArray(content) ? content.filter((block) => block && block.type === 'text').map((block) => block.text) : [];
      return texts.length > 0 && texts.every((text) => INJECTED(text));
    })
    .map((message) => message.uuid);
  const ourSystem = new Set(exported.messages.filter((message) => message.role === 'system').map((message) => message.id));
  const missing = sdkInjected.filter((uuid) => !ourSystem.has(uuid));
  if (missing.length > 0) {
    fail(errors, id + '：SDK 的注入记录没有落成 system 消息：' + missing.join(','));
  }
}

const total = ids.length;
if (errors.length > 0) {
  for (const message of errors) console.error('FAIL ' + message);
  console.error('conformance: ' + (total - errors.length) + '/' + total + ' 会话通过，' + errors.length + ' 处不一致（消息级抽查 ' + checked + ' 个）');
  process.exit(1);
}
console.log('conformance: ' + total + '/' + total + ' 会话通过（id/标题/目录/时间），消息级抽查 ' + checked + ' 个也一致');
