#!/usr/bin/env node
/**
 * 用 Chrome DevTools Protocol 真实驱动页面：悬停、点击、框选、滚轮缩放、搜索、深链，
 * 断言状态并截图，用来验证「总览时间轴能看到模型/工具耗时、能点到轮次里看 LLM 输出与工具结果」。
 *
 * 用法：node tools/cdp-check.mjs [--html out/sesscope.html] [--shots out/verify]
 * 退出码：0 全绿；1 有断言失败；2 环境问题。
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
const htmlPath = path.resolve(arg('html', path.join(HERE, 'out', 'sesscope.html')));
const shotsDir = path.resolve(arg('shots', path.join(HERE, 'out', 'verify')));
const PORT = 9333 + Math.floor(Math.random() * 200);

fs.mkdirSync(shotsDir, { recursive: true });
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'trace-cdp-'));

const chrome = spawn('google-chrome', [
  '--headless=new', '--disable-gpu', '--no-sandbox', '--hide-scrollbars',
  '--hide-crash-restore-bubble', '--window-size=1600,1000',
  '--user-data-dir=' + profile,
  '--remote-debugging-port=' + PORT,
  'file://' + htmlPath,
], { stdio: ['ignore', 'pipe', 'pipe'] });
let chromeStderr = '';
chrome.stderr.on('data', (chunk) => { chromeStderr += String(chunk); });

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitForTarget() {
  for (let i = 0; i < 120; i++) {
    try {
      const response = await fetch('http://127.0.0.1:' + PORT + '/json/list');
      const list = await response.json();
      const page = list.find((t) => t.type === 'page' && t.webSocketDebuggerUrl);
      if (page) return page;
    } catch { /* Chrome 还在启动 */ }
    await sleep(150);
  }
  throw new Error('连不上 Chrome DevTools：' + chromeStderr.slice(-400));
}

const page = await waitForTarget();
const ws = new WebSocket(page.webSocketDebuggerUrl);
let nextId = 1;
const pending = new Map();
const consoleErrors = [];
const exceptions = [];
ws.addEventListener('message', (event) => {
  const message = JSON.parse(event.data);
  if (message.id && pending.has(message.id)) {
    const settle = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) settle.reject(new Error(JSON.stringify(message.error)));
    else settle.resolve(message.result);
    return;
  }
  if (message.method === 'Runtime.consoleAPICalled' && message.params.type === 'error') {
    consoleErrors.push(message.params.args.map((a) => a.value ?? a.description ?? a.type).join(' '));
  }
  if (message.method === 'Runtime.exceptionThrown') {
    const details = message.params.exceptionDetails;
    exceptions.push(details.text + ' ' + (details.exception?.description ?? ''));
  }
});
await new Promise((resolve, reject) => {
  ws.addEventListener('open', resolve, { once: true });
  ws.addEventListener('error', reject, { once: true });
});
function send(method, params = {}) {
  const id = nextId++;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params }));
  });
}

await send('Runtime.enable');
await send('Page.enable');
await sleep(900);

/** 传函数：用 toString 注入页面执行，避免拼字符串。 */
async function evaluate(fn, arg) {
  const expression = '(' + fn.toString() + ')(' + (arg === undefined ? '' : JSON.stringify(arg)) + ')';
  const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text + ' :: ' + expression.slice(0, 200));
  return result.result.value;
}
async function shot(name) {
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  const file = path.join(shotsDir, name + '.png');
  fs.writeFileSync(file, Buffer.from(data, 'base64'));
  return file;
}
async function mouse(type, x, y, extra = {}) {
  await send('Input.dispatchMouseEvent', {
    type, x, y,
    button: extra.button ?? 'none',
    buttons: extra.buttons ?? 0,
    clickCount: extra.clickCount ?? 0,
    deltaX: 0,
    deltaY: extra.deltaY ?? 0,
  });
}
async function click(x, y) {
  await mouse('mouseMoved', x, y);
  await mouse('mousePressed', x, y, { button: 'left', buttons: 1, clickCount: 1 });
  await mouse('mouseReleased', x, y, { button: 'left', buttons: 0, clickCount: 1 });
  await sleep(140);
}
async function pressEscape() {
  await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 });
  await sleep(160);
}
async function goto(hash) {
  await send('Page.navigate', { url: 'file://' + htmlPath + (hash ? '#' + hash : '') });
  await sleep(1300);
}

const checks = [];
function check(name, condition, detail) {
  checks.push({ name, ok: !!condition, detail: detail === undefined ? null : String(detail) });
  console.log((condition ? '  ok   ' : '  FAIL ') + name + (detail === undefined ? '' : ' — ' + detail));
}
const center = (rect) => ({ x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2) });

// ---------------- 1. 基本渲染
const basics = await evaluate(() => {
  const trace = window.__trace;
  return {
    records: trace.records.length,
    steps: trace.stats.steps,
    toolCalls: trace.stats.toolCalls,
    models: trace.records.filter((r) => r.kind === 'model').length,
    tools: trace.records.filter((r) => r.kind === 'tool').length,
    rows: document.querySelectorAll('tr.row-record').length,
    bars: document.querySelectorAll('.bar').length,
    errors: trace.records.filter((r) => r.status === 'error').length,
    running: trace.records.filter((r) => r.status === 'running').length,
    ticks: document.querySelectorAll('.axis-tick').length,
  };
});
console.log('\n[1] 基本渲染', JSON.stringify(basics));
check('每个模型调用都有时间条', basics.models >= basics.steps, basics.models + '/' + basics.steps + ' 步');
check('每个工具调用都有时间条', basics.tools === basics.toolCalls, basics.tools + '/' + basics.toolCalls);
check('台账行数 = 记录数', basics.rows === basics.records, basics.rows + '/' + basics.records);
check('时间轴刻度 >= 4', basics.ticks >= 4, basics.ticks);
check('存在报错记录（红色）', basics.errors >= 1, basics.errors);
check('存在未完成记录', basics.running >= 1, basics.running);
await shot('01-full');

// ---------------- 2. 悬停 tooltip
const modelBar = await evaluate(() => {
  const bars = [...document.querySelectorAll('.bar[data-kind="model"]')];
  const bar = bars[Math.floor(bars.length * 0.3)];
  const r = bar.getBoundingClientRect();
  return { key: bar.dataset.key, x: r.x, y: r.y, width: r.width, height: r.height };
});
const modelPoint = center(modelBar);
await mouse('mouseMoved', modelPoint.x, modelPoint.y);
await sleep(300);
const tooltip = await evaluate(() => {
  const node = document.getElementById('timelineTooltip');
  return { hidden: node.hidden, text: node.textContent };
});
console.log('\n[2] 悬停模型条', JSON.stringify(tooltip).slice(0, 220));
check('悬停显示 tooltip', !tooltip.hidden && tooltip.text.length > 0, tooltip.text.replace(/\n/g, ' | ').slice(0, 120));
check('tooltip 含「总计」与「思考」', /总计/.test(tooltip.text) && /思考/.test(tooltip.text));
await shot('02-hover');

// ---------------- 3. 点击模型条 → 台账定位 + 检查器
await click(modelPoint.x, modelPoint.y);
await sleep(300);
const selected = await evaluate(() => {
  const state = window.__trace.state;
  const row = document.querySelector('tr.row-record[data-key="' + CSS.escape(state.selected) + '"]');
  const head = document.querySelector('.insp-head');
  return {
    selected: state.selected,
    rowSelected: row ? row.dataset.selected : null,
    tags: head ? [...head.querySelectorAll('.tag')].map((t) => t.textContent) : [],
    title: head ? head.querySelector('.insp-title').textContent : null,
    tabs: [...document.querySelectorAll('.tabs .tab')].map((t) => t.textContent),
    labels: [...document.querySelectorAll('.insp-body dl.overview dt')].map((d) => d.textContent),
  };
});
console.log('\n[3] 点击模型条', JSON.stringify(selected).slice(0, 320));
check('点击后选中该记录', selected.selected === modelBar.key, selected.selected);
check('台账对应行高亮', selected.rowSelected === 'true');
check('检查器标题含轮次/步骤', /第 \d+ 轮第 \d+ 步/.test(selected.title ?? ''), selected.title);
check('检查器有 概述/思考/原始输出/计时 页签', ['概述', '思考', '原始输出', '计时'].every((label) => selected.tabs.some((t) => t.startsWith(label))), selected.tabs.join('|'));
check('概览含模型耗时与 tokens', selected.labels.includes('模型耗时') && selected.labels.includes('tokens'));
await shot('03-model-inspector');

const thinkingText = await evaluate(() => {
  const tabs = [...document.querySelectorAll('.tabs .tab')];
  tabs.find((t) => t.textContent.startsWith('思考')).click();
  const panel = [...document.querySelectorAll('.tab-panel')].find((p) => !p.hidden);
  return panel.textContent.trim();
});
check('思考页签有正文', thinkingText.length > 5, thinkingText.slice(0, 60).replace(/\n/g, ' '));
await shot('04-thinking-tab');

// ---------------- 4. 工具调用：参数 + 结果
await evaluate(() => { window.__trace.state.selected = null; window.__trace.state.tab = null; });
const toolBar = await evaluate(() => {
  const bars = [...document.querySelectorAll('.bar[data-kind="tool"]')].filter((b) => b.dataset.wait !== 'true');
  const bar = bars[Math.floor(bars.length / 2)];
  const r = bar.getBoundingClientRect();
  return { key: bar.dataset.key, x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) };
});
await click(toolBar.x, toolBar.y);
await sleep(300);
const toolState = await evaluate(() => {
  const head = document.querySelector('.insp-head');
  const tabs = [...document.querySelectorAll('.tabs .tab')];
  const label = (t) => t.textContent.replace(/\s+\d+$/, '');
  const readTab = (name) => {
    const tab = tabs.find((t) => label(t).startsWith(name));
    tab.click();
    return [...document.querySelectorAll('.tab-panel')].find((p) => !p.hidden).textContent.trim();
  };
  const result = readTab('结果');
  const params = readTab('参数');
  return {
    title: head.querySelector('.insp-title').textContent,
    tags: [...head.querySelectorAll('.tag')].map((t) => t.textContent),
    tabs: tabs.map((t) => t.textContent),
    result, params,
  };
});
console.log('\n[4] 点击工具条', JSON.stringify({ title: toolState.title, tabs: toolState.tabs, resultHead: toolState.result.slice(0, 80) }));
check('工具检查器标题含轮次/步骤', /第 \d+ 轮第 \d+ 步/.test(toolState.title), toolState.title);
check('工具有 参数/结果/计时 页签', ['参数', '结果', '计时'].every((name) => toolState.tabs.some((t) => t.startsWith(name))), toolState.tabs.join('|'));
check('工具结果有内容', toolState.result.length > 5, toolState.result.slice(0, 60).replace(/\n/g, ' '));
check('工具参数有内容', toolState.params.length > 2, toolState.params.slice(0, 60).replace(/\n/g, ' '));
await shot('05-tool-inspector');

// ---------------- 5. 框选
const trackRect = await evaluate(() => {
  const r = document.getElementById('track').getBoundingClientRect();
  return { x: r.x, y: r.y, width: r.width, height: r.height };
});
const midY = Math.round(trackRect.y + trackRect.height / 2);
const fromX = Math.round(trackRect.x + trackRect.width * 0.2);
const toX = Math.round(trackRect.x + trackRect.width * 0.45);
await mouse('mouseMoved', fromX, midY);
await mouse('mousePressed', fromX, midY, { button: 'left', buttons: 1, clickCount: 1 });
for (let i = 1; i <= 6; i++) await mouse('mouseMoved', fromX + ((toX - fromX) * i) / 6, midY, { button: 'left', buttons: 1 });
await mouse('mouseReleased', toX, midY, { button: 'left', buttons: 0, clickCount: 1 });
await sleep(300);
const rangeState = await evaluate(() => {
  const state = window.__trace.state;
  const rows = [...document.querySelectorAll('tr.row-record')];
  return {
    range: state.range,
    selectionHidden: document.getElementById('selection').hidden,
    dimRows: rows.filter((r) => r.dataset.dim === 'true').length,
    totalRows: rows.length,
  };
});
console.log('\n[5] 框选', JSON.stringify(rangeState));
check('框选产生时间区间', rangeState.range !== null && rangeState.range.t1 > rangeState.range.t0);
check('选择区间可见', rangeState.selectionHidden === false);
check('区间外的行被压暗', rangeState.dimRows > 0 && rangeState.dimRows < rangeState.totalRows, rangeState.dimRows + '/' + rangeState.totalRows);
await shot('06-range-selection');
await pressEscape();
const afterEscape = await evaluate(() => ({ range: window.__trace.state.range, selectionHidden: document.getElementById('selection').hidden }));
check('Esc 清除框选', afterEscape.range === null && afterEscape.selectionHidden === true);

// ---------------- 6. 滚轮缩放 / 双击复位
const beforeZoom = await evaluate(() => window.__trace.state.view);
const wheelX = Math.round(trackRect.x + trackRect.width * 0.5);
await mouse('mouseMoved', wheelX, midY);
for (let i = 0; i < 4; i++) {
  await send('Input.dispatchMouseEvent', { type: 'mouseWheel', x: wheelX, y: midY, deltaX: 0, deltaY: -160, button: 'none', buttons: 0 });
  await sleep(70);
}
await sleep(250);
const afterZoom = await evaluate(() => window.__trace.state.view);
console.log('\n[6] 滚轮缩放', JSON.stringify({ before: beforeZoom, after: afterZoom }));
check('滚轮缩窄时间窗口', afterZoom !== null && (beforeZoom === null || (afterZoom.t1 - afterZoom.t0) < (beforeZoom.t1 - beforeZoom.t0)), afterZoom ? Math.round(afterZoom.t1 - afterZoom.t0) : null);
await shot('07-zoom');
await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: wheelX, y: midY, button: 'left', buttons: 1, clickCount: 2 });
await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: wheelX, y: midY, button: 'left', buttons: 0, clickCount: 2 });
await sleep(300);
check('双击复位到全量', (await evaluate(() => window.__trace.state.view)) === null);

// ---------------- 7. 收起/展开轮次
const rowsBefore = await evaluate(() => document.querySelectorAll('tr.row-record').length);
await evaluate(() => document.getElementById('collapseAll').click());
await sleep(250);
const rowsCollapsed = await evaluate(() => document.querySelectorAll('tr.row-record').length);
await evaluate(() => document.getElementById('expandAll').click());
await sleep(250);
const rowsExpanded = await evaluate(() => document.querySelectorAll('tr.row-record').length);
console.log('\n[7] 收起/展开', JSON.stringify({ rowsBefore, rowsCollapsed, rowsExpanded }));
check('收起轮次后行数减少', rowsCollapsed < rowsBefore, rowsCollapsed + ' < ' + rowsBefore);
check('展开后行数恢复', rowsExpanded === rowsBefore, rowsExpanded + ' = ' + rowsBefore);

// ---------------- 8. 搜索
await evaluate(() => {
  const input = document.getElementById('search');
  input.value = 'fastdata';
  input.dispatchEvent(new Event('input', { bubbles: true }));
});
await sleep(300);
const searchState = await evaluate(() => {
  const rows = [...document.querySelectorAll('tr.row-record')];
  const bars = [...document.querySelectorAll('.bar')];
  return {
    dimRows: rows.filter((r) => r.dataset.dim === 'true').length,
    totalRows: rows.length,
    dimBars: bars.filter((b) => b.dataset.dim === 'true').length,
  };
});
console.log('\n[8] 搜索 fastdata', JSON.stringify(searchState));
check('搜索压暗不匹配行', searchState.dimRows > 0 && searchState.dimRows < searchState.totalRows, searchState.dimRows + '/' + searchState.totalRows);
check('搜索同时压暗时间轴', searchState.dimBars > 0, searchState.dimBars);
await shot('08-search');
await evaluate(() => {
  const input = document.getElementById('search');
  input.value = '';
  input.dispatchEvent(new Event('input', { bubbles: true }));
});

// ---------------- 9. 深链 #turn=4
await goto('turn=4');
const deepLink = await evaluate(() => {
  const state = window.__trace.state;
  return {
    view: state.view,
    ticks: [...document.querySelectorAll('.axis-tick')].map((t) => t.textContent),
    hasTurn4: !!document.querySelector('tr.row-turn[data-turn="4"]'),
    viewSpan: state.view ? state.view.t1 - state.view.t0 : null,
  };
});
console.log('\n[9] 深链 #turn=4', JSON.stringify(deepLink));
check('深链把视口缩到第 4 轮', deepLink.viewSpan !== null && deepLink.viewSpan < 60000, deepLink.viewSpan && Math.round(deepLink.viewSpan));
check('刻度落在第 4 轮时间范围', deepLink.ticks.some((t) => t.startsWith('16:01:2') || t.startsWith('16:01:3')), deepLink.ticks.join(','));
await shot('09-deeplink-turn4');

// ---------------- 10. 报错记录（红色）
const errorInfo = await evaluate(() => {
  const rec = window.__trace.records.find((r) => r.status === 'error');
  return { key: rec ? rec.key : null };
});
await goto('key=' + errorInfo.key);
const errorState = await evaluate(() => {
  const bar = document.querySelector('.bar[data-status="error"]');
  const row = document.querySelector('tr.row-record[data-error="true"]');
  const head = document.querySelector('.insp-head');
  const body = document.querySelector('.insp-body');
  return {
    barExists: !!bar,
    barColor: bar ? getComputedStyle(bar).backgroundColor : null,
    rowExists: !!row,
    tags: head ? [...head.querySelectorAll('.tag')].map((t) => t.textContent) : [],
    bodyText: body ? body.textContent : '',
    rowTimeColor: row ? getComputedStyle(row.querySelector('.cell-time')).color : null,
  };
});
console.log('\n[10] 报错记录', JSON.stringify({ key: errorInfo.key, barColor: errorState.barColor, tags: errorState.tags, rowTimeColor: errorState.rowTimeColor }));
check('报错条在时间轴上变红', errorState.barExists && /242, 90, 90|236, 19, 19/.test(errorState.barColor ?? ''), errorState.barColor);
check('报错台账行带 data-error', errorState.rowExists);
check('报错行时长文字为红色', /242, 90, 90|236, 19, 19/.test(errorState.rowTimeColor ?? ''), errorState.rowTimeColor);
check('检查器显示已中止 + MessageAbortedError', errorState.tags.includes('已中止') && /MessageAbortedError/.test(errorState.bodyText), errorState.tags.join('|'));
await shot('10-error-record');

// ---------------- 11. 控制台
console.log('\n[11] 控制台', JSON.stringify({ consoleErrors, exceptions }));
check('没有 JS 异常', exceptions.length === 0, exceptions.slice(0, 2).join(' | '));
check('没有 console.error', consoleErrors.length === 0, consoleErrors.slice(0, 2).join(' | '));

// ---------------- 12. 工具报错渲染（fixture）
const fixturePath = path.join(path.dirname(htmlPath), 'fixture-tool-error.html');
if (fs.existsSync(fixturePath)) {
  await send('Page.navigate', { url: 'file://' + fixturePath + '#key=t1s1ccall_cc0c49b9358f4837990b1806' });
  await sleep(1300);
  const fixture = await evaluate(() => {
    const toolBars = [...document.querySelectorAll('.bar[data-kind="tool"]')];
    const errorToolBar = toolBars.find((b) => b.dataset.status === 'error');
    const head = document.querySelector('.insp-head');
    const tabs = [...document.querySelectorAll('.tabs .tab')];
    const resultTab = tabs.find((t) => t.textContent.startsWith('结果'));
    resultTab.click();
    const panel = [...document.querySelectorAll('.tab-panel')].find((p) => !p.hidden);
    const pre = panel.querySelector('pre');
    return {
      toolBarRed: errorToolBar ? getComputedStyle(errorToolBar).backgroundColor : null,
      tags: head ? [...head.querySelectorAll('.tag')].map((t) => t.textContent) : [],
      statusText: document.querySelector('.insp-body dl.overview dd').textContent,
      resultIsError: pre ? pre.classList.contains('is-error') : false,
      resultColor: pre ? getComputedStyle(pre).color : null,
      resultText: panel.textContent.trim().slice(0, 60),
    };
  });
  console.log('\n[12] 工具报错样本', JSON.stringify(fixture));
  check('工具报错条为红色', /242, 90, 90|236, 19, 19/.test(fixture.toolBarRed ?? ''), fixture.toolBarRed);
  check('工具报错行标签为「失败」', fixture.tags.includes('失败'), fixture.tags.join('|'));
  check('检查器状态显示失败', fixture.statusText === '失败', fixture.statusText);
  check('结果面板以红色错误样式呈现', fixture.resultIsError && /242, 90, 90|236, 19, 19/.test(fixture.resultColor ?? ''), fixture.resultColor);
  await shot('12-tool-error-fixture');
} else {
  console.log('\n[12] 跳过：没有 ' + fixturePath + '（先跑 node build.mjs --fixture-tool-error）');
}

// ---------------- 汇总
const failed = checks.filter((c) => !c.ok);
fs.writeFileSync(path.join(shotsDir, 'report.json'), JSON.stringify({ total: checks.length, failed: failed.length, checks, consoleErrors, exceptions }, null, 2));
console.log('\n' + (failed.length === 0 ? 'ALL GREEN' : 'FAILED') + ' ' + (checks.length - failed.length) + '/' + checks.length + ' 项通过');
console.log('截图目录：' + shotsDir);
if (failed.length > 0) console.log(JSON.stringify(failed.map((f) => f.name + ' — ' + f.detail), null, 2));
ws.close();
chrome.kill('SIGKILL');
process.exit(failed.length === 0 ? 0 : 1);
