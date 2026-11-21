#!/usr/bin/env node
/**
 * 与具体会话无关的冒烟检查：验证任意一份导出 JSON 渲染出的页面还能正常用。
 * （cdp-check.mjs 是钉死某个会话的完整回归；这个是"换个会话也能开"的门槛）
 *
 * 用法：node tools/cdp-smoke.mjs [--html out/demo/ses_small.html]
 */
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const argv = process.argv.slice(2);
const arg = (name, fallback) => {
  const i = argv.indexOf('--' + name);
  return i === -1 ? fallback : argv[i + 1];
};
const htmlPath = path.resolve(arg('html', path.join(HERE, 'out', 'demo', 'ses_small.html')));
if (!fs.existsSync(htmlPath)) {
  console.error('找不到 HTML：' + htmlPath);
  process.exit(2);
}
const PORT = 9500 + Math.floor(Math.random() * 200);
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'trace-smoke-'));
const chrome = spawn('google-chrome', [
  '--headless=new', '--disable-gpu', '--no-sandbox', '--hide-scrollbars',
  '--user-data-dir=' + profile, '--remote-debugging-port=' + PORT, '--window-size=1600,1000',
  'file://' + htmlPath,
], { stdio: ['ignore', 'ignore', 'pipe'] });
let chromeStderr = '';
chrome.stderr.on('data', (c) => { chromeStderr += String(c); });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function waitForTarget() {
  for (let i = 0; i < 120; i++) {
    try {
      const list = await (await fetch('http://127.0.0.1:' + PORT + '/json/list')).json();
      const page = list.find((t) => t.type === 'page' && t.webSocketDebuggerUrl);
      if (page) return page;
    } catch { /* 还在启动 */ }
    await sleep(150);
  }
  throw new Error('连不上 Chrome：' + chromeStderr.slice(-300));
}

const page = await waitForTarget();
const ws = new WebSocket(page.webSocketDebuggerUrl);
let nextId = 1;
const pending = new Map();
const exceptions = [];
const consoleErrors = [];
ws.addEventListener('message', (event) => {
  const message = JSON.parse(event.data);
  if (message.id && pending.has(message.id)) {
    const settle = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) settle.reject(new Error(JSON.stringify(message.error)));
    else settle.resolve(message.result);
    return;
  }
  if (message.method === 'Runtime.exceptionThrown') exceptions.push(message.params.exceptionDetails.text);
  if (message.method === 'Runtime.consoleAPICalled' && message.params.type === 'error') {
    consoleErrors.push(message.params.args.map((a) => a.value ?? a.description).join(' '));
  }
});
await new Promise((resolve, reject) => {
  ws.addEventListener('open', resolve, { once: true });
  ws.addEventListener('error', reject, { once: true });
});
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = nextId++;
  pending.set(id, { resolve, reject });
  ws.send(JSON.stringify({ id, method, params }));
});
await send('Runtime.enable');
await sleep(900);

async function evaluate(fn) {
  const expression = '(' + fn.toString() + ')()';
  const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text);
  return result.result.value;
}
async function click(x, y) {
  for (const [type, buttons] of [['mousePressed', 1], ['mouseReleased', 0]]) {
    await send('Input.dispatchMouseEvent', { type, x, y, button: 'left', buttons, clickCount: 1 });
  }
  await sleep(150);
}

const checks = [];
const check = (name, ok, detail) => {
  checks.push({ name, ok: !!ok });
  console.log((ok ? '  ok   ' : '  FAIL ') + name + (detail === undefined ? '' : ' — ' + detail));
};

const basics = await evaluate(() => {
  const trace = window.__trace;
  return {
    title: document.getElementById('sessionTitle').textContent,
    records: trace.records.length,
    models: trace.records.filter((r) => r.kind === 'model').length,
    tools: trace.records.filter((r) => r.kind === 'tool').length,
    bars: document.querySelectorAll('.bar').length,
    rows: document.querySelectorAll('tr.row-record').length,
    turns: document.querySelectorAll('tr.row-turn').length,
    errors: trace.records.filter((r) => r.status === 'error').length,
  };
});
console.log('会话：' + basics.title);
console.log(JSON.stringify(basics));
check('页面有记录', basics.records > 0, basics.records);
check('时间轴条与台账行都渲染出来', basics.bars > 0 && basics.rows > 0, basics.bars + '/' + basics.rows);
check('轮次表头存在', basics.turns > 0, basics.turns);

const modelBar = await evaluate(() => {
  const bar = document.querySelector('.bar[data-kind="model"]');
  if (!bar) return null;
  const r = bar.getBoundingClientRect();
  return { x: Math.round(r.x + Math.min(4, r.width / 2)), y: Math.round(r.y + r.height / 2) };
});
if (modelBar) {
  await click(modelBar.x, modelBar.y);
  const inspector = await evaluate(() => {
    const head = document.querySelector('.insp-head');
    return {
      title: head ? head.querySelector('.insp-title').textContent : null,
      tabs: [...document.querySelectorAll('.tabs .tab')].map((t) => t.textContent),
    };
  });
  check('点击模型条能打开检查器', /模型/.test(inspector.title ?? ''), inspector.title);
  check('检查器有思考/计时页签', inspector.tabs.some((t) => t.startsWith('思考')) && inspector.tabs.some((t) => t.startsWith('计时')), inspector.tabs.join('|'));
}

const toolBar = await evaluate(() => {
  const bars = [...document.querySelectorAll('.bar[data-kind="tool"]')];
  const bar = bars[0];
  if (!bar) return null;
  const r = bar.getBoundingClientRect();
  return { x: Math.round(r.x + Math.min(4, r.width / 2)), y: Math.round(r.y + r.height / 2) };
});
if (toolBar) {
  await click(toolBar.x, toolBar.y);
  const toolTabs = await evaluate(() => [...document.querySelectorAll('.tabs .tab')].map((t) => t.textContent));
  check('点击工具条能看到参数/结果', toolTabs.some((t) => t.startsWith('参数')) && toolTabs.some((t) => t.startsWith('结果')), toolTabs.join('|'));
}

await evaluate(() => {
  const input = document.getElementById('search');
  // 用随机串：固定串可能恰好出现在会话内容里（比如本工具源码被当成会话内容）。
  input.value = 'no-match-' + Math.random().toString(36).slice(2, 10) + '-token';
  input.dispatchEvent(new Event('input', { bubbles: true }));
});
await sleep(200);
const searchState = await evaluate(() => {
  const rows = [...document.querySelectorAll('tr.row-record')];
  return { total: rows.length, dim: rows.filter((r) => r.dataset.dim === 'true').length };
});
check('搜索能压暗不匹配的行', searchState.dim === searchState.total && searchState.total > 0, searchState.dim + '/' + searchState.total);

await evaluate(() => {
  const input = document.getElementById('search');
  input.value = '';
  input.dispatchEvent(new Event('input', { bubbles: true }));
});
const collapse = await evaluate(() => {
  const before = document.querySelectorAll('tr.row-record').length;
  document.getElementById('collapseAll').click();
  const after = document.querySelectorAll('tr.row-record').length;
  document.getElementById('expandAll').click();
  return { before, after, restored: document.querySelectorAll('tr.row-record').length };
});
check('收起/展开轮次正常', collapse.after < collapse.before && collapse.restored === collapse.before, JSON.stringify(collapse));


// ---------------- 归因（有 episodes 时才测） ----------------
// 先回到总览面板，才能看到归因聚合行
const closed = await evaluate(() => {
  const button = document.querySelector('.insp-close');
  if (button) { button.click(); return true; }
  return false;
});
await sleep(200);
const attribution = await evaluate(() => {
  const episodes = (window.__trace && window.__trace.state) ? (JSON.parse(document.getElementById('trace-data').textContent).episodes ?? []) : [];
  const marks = [...document.querySelectorAll('.cause-mark')];
  const badges = [...document.querySelectorAll('.cause-badge')];
  const summaryRows = [...document.querySelectorAll('.cause-row')];
  return { episodes: episodes.length, marks: marks.length, badges: badges.length, summaryRows: summaryRows.length };
});
void closed;
if (attribution.episodes > 0) {
  console.log('归因：' + JSON.stringify(attribution));
  check('时间轴有归因触发点', attribution.marks > 0, attribution.marks);
  check('总览有归因聚合行', attribution.summaryRows > 0, attribution.summaryRows);
  check('台账有归因徽标', attribution.badges > 0, attribution.badges);

  const mark = await evaluate(() => {
    const first = document.querySelector('.cause-mark');
    if (!first) return null;
    const r = first.getBoundingClientRect();
    return { key: first.dataset.key, x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) };
  });
  await click(mark.x, mark.y);
  await sleep(200);
  const afterClick = await evaluate(() => {
    const head = document.querySelector('.insp-head');
    return {
      selected: window.__trace.state.selected,
      title: head ? head.querySelector('.insp-title').textContent : null,
      tabs: [...document.querySelectorAll('.tabs .tab')].map((t) => t.textContent),
    };
  });
  check('点归因标记能选中对应步骤', afterClick.selected === mark.key, afterClick.selected + ' vs ' + mark.key);
  check('检查器出现归因页签', afterClick.tabs.some((t) => t.startsWith('归因')), afterClick.tabs.join('|'));

  const card = await evaluate(() => {
    const tabs = [...document.querySelectorAll('.tabs .tab')];
    const target = tabs.find((t) => t.textContent.startsWith('归因'));
    if (!target) return null;
    target.click();
    const panel = [...document.querySelectorAll('.tab-panel')].find((p) => !p.hidden);
    return panel ? panel.textContent : null;
  });
  check('归因页签有证据与结论', /证据|结论|补偿动作/.test(card ?? ''), (card ?? '').replace(/\s+/g, ' ').slice(0, 90));
}


// 回到总览再数补偿链行
const backToSummary = await evaluate(() => {
  const button = document.querySelector('.insp-close');
  if (button) { button.click(); return true; }
  return false;
});
await sleep(200);
void backToSummary;
const chains = await evaluate(() => {
  const data = JSON.parse(document.getElementById('trace-data').textContent);
  return {
    chains: (data.chains ?? []).length,
    bands: document.querySelectorAll('.chain-band').length,
    rows: document.querySelectorAll('.chain-row').length,
  };
});
if (chains.chains > 0) {
  console.log('补偿链：' + JSON.stringify(chains));
  check('时间轴有补偿链底纹', chains.bands > 0, chains.bands);
  check('总览有补偿链行', chains.rows > 0, chains.rows);
}

check('没有 JS 异常', exceptions.length === 0, exceptions[0]);
check('没有 console.error', consoleErrors.length === 0, consoleErrors[0]);

const failed = checks.filter((c) => !c.ok).length;
console.log('\n' + (failed === 0 ? 'SMOKE PASS' : 'SMOKE FAIL') + ' ' + (checks.length - failed) + '/' + checks.length);
ws.close();
chrome.kill('SIGKILL');
process.exit(failed === 0 ? 0 : 1);
