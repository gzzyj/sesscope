/* opencode 轨迹耗时视图（纯前端、零依赖）。
   数据：#trace-data 里由 build.mjs 归一化后的 opencode session 导出。
   视觉：对齐 @deepseek-ai/dsh-client-ui-trajectory（轨迹）+ dsh-client-ui-theme 的 token。 */
(() => {
  'use strict';

  const DATA = JSON.parse(document.getElementById('trace-data').textContent);
  const { meta, stats, warnings, turns } = DATA;

  // ---------------------------------------------------------------- 格式化
  const pad = (v, n = 2) => String(v).padStart(n, '0');
  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

  function fmtMs(ms) {
    if (ms === null || ms === undefined || !Number.isFinite(ms)) return '—';
    const sign = ms < 0 ? '-' : '';
    const v = Math.abs(ms);
    if (v < 1000) return sign + Math.round(v) + ' ms';
    if (v < 60000) return sign + (v / 1000).toFixed(v < 10000 ? 2 : 1) + ' s';
    if (v < 3600000) {
      const m = Math.floor(v / 60000);
      const s = (v % 60000) / 1000;
      return sign + m + ' m ' + (s >= 0.05 ? s.toFixed(1) + ' s' : '');
    }
    const h = Math.floor(v / 3600000);
    const m = Math.round((v % 3600000) / 60000);
    return sign + h + ' h ' + (m > 0 ? m + ' m' : '');
  }
  function fmtShort(ms) {
    if (ms >= 3600000) return Math.round(ms / 3600000) + 'h';
    if (ms >= 60000) return Math.round(ms / 60000) + 'm';
    if (ms >= 1000) return Math.round(ms / 1000) + 's';
    return Math.round(ms) + 'ms';
  }
  function fmtClock(ms) {
    if (ms === null || ms === undefined) return '—';
    const d = new Date(ms);
    return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds()) + '.' + pad(d.getMilliseconds(), 3);
  }
  function fmtHms(ms) {
    if (ms === null || ms === undefined) return '—';
    const d = new Date(ms);
    return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
  }
  function fmtDateTime(ms) {
    if (ms === null || ms === undefined) return '—';
    const d = new Date(ms);
    return pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
  }
  function fmtNum(n) {
    if (n === null || n === undefined) return '—';
    return String(Math.round(n)).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  }
  function pct(part, total) {
    if (!total) return '0%';
    const v = (part / total) * 100;
    return (v >= 10 ? v.toFixed(0) : v.toFixed(1)) + '%';
  }
  const esc = (s) => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  const oneLine = (s, max = 220) => {
    const t = String(s ?? '').replace(/\s+/g, ' ').trim();
    return t.length > max ? t.slice(0, max) + '…' : t;
  };
  function el(tag, cls, text) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  // ---------------------------------------------------------------- 记录
  const records = [];
  const recordByKey = new Map();
  function pushRecord(rec) {
    records.push(rec);
    recordByKey.set(rec.key, rec);
  }
  for (const turn of turns) {
    pushRecord({
      key: 't' + turn.index,
      kind: turn.kind === 'compaction' ? 'compaction' : turn.kind === 'continue' ? 'continue' : 'user',
      lane: turn.kind === 'compaction' ? 1 : 0,
      turn,
      step: null,
      tool: null,
      t0: turn.startMs,
      t1: turn.kind === 'compaction' ? turn.startMs + (turn.compaction?.durationMs ?? 0) : turn.startMs,
      status: 'completed',
      label: turn.kind === 'compaction' ? '上下文压缩' : turn.kind === 'continue' ? '自动续跑' : '用户输入',
    });
    for (const step of turn.steps) {
      if (step.modelStartMs !== null && step.modelEndMs !== null) {
        const think = step.reasoningMs + step.textMs > 0 ? step.reasoningMs / (step.reasoningMs + step.textMs) : 0;
        pushRecord({
          key: 't' + turn.index + 's' + step.index,
          kind: 'model', lane: 1, turn, step, tool: null,
          t0: step.modelStartMs, t1: step.modelEndMs, think,
          status: step.status,
          label: '模型调用',
        });
      }
      for (const tool of step.tools) {
        pushRecord({
          key: 't' + turn.index + 's' + step.index + 'c' + tool.callId,
          kind: 'tool', lane: 2, turn, step, tool,
          t0: tool.startMs, t1: tool.endMs,
          wait: tool.waitHuman === true,
          status: tool.status === 'error' ? 'error' : tool.status === 'running' ? 'running' : 'completed',
          label: tool.name,
        });
      }
    }
  }
  records.sort((a, b) => a.t0 - b.t0 || a.t1 - b.t1);

  // ---------------- 归因（本地规则算出来的事件链，页面只负责展示）----------------
  const EPISODES = Array.isArray(DATA.episodes) ? DATA.episodes : [];
  const CHAINS = Array.isArray(DATA.chains) ? DATA.chains : [];
  const episodeKeyOf = (turn, step) => 't' + turn + 's' + step;
  const episodesByStep = new Map();
  for (const episode of EPISODES) {
    const key = episodeKeyOf(episode.turn, episode.step);
    const list = episodesByStep.get(key) ?? [];
    list.push(episode);
    episodesByStep.set(key, list);
  }
  const episodeSummary = (() => {
    const byCode = new Map();
    for (const episode of EPISODES) {
      const entry = byCode.get(episode.code) ?? { code: episode.code, label: episode.label, count: 0, windowMs: 0, severity: episode.severity, confidence: episode.confidence };
      entry.count += 1;
      entry.windowMs += episode.windowMs;
      byCode.set(episode.code, entry);
    }
    return [...byCode.values()].sort((a, b) => b.windowMs - a.windowMs || b.count - a.count);
  })();
  const totalEpisodeMs = EPISODES.reduce((sum, episode) => sum + episode.windowMs, 0);

  const recDur = (rec) => rec.kind === 'tool'
    ? (rec.tool.durationMs ?? 0)
    : rec.kind === 'model'
      ? (rec.step.modelMs ?? 0)
      : rec.kind === 'compaction'
        ? (rec.turn.compaction?.durationMs ?? 0)
        : 0;

  // ---------------------------------------------------------------- 状态
  const state = {
    gapThreshold: 60000,
    view: null,        // { t0, t1 } 真实时间
    range: null,       // { t0, t1 } 真实时间（框选）
    selected: null,
    hovered: null,
    query: '',
    tab: null,
    collapsed: new Set(),
    theme: (() => {
      try {
        // 兼容旧版本写入的 opencode-trace-theme：改名不该把用户的主题选择弄丢。
        const saved = window.localStorage.getItem('atrace-theme') || window.localStorage.getItem('opencode-trace-theme');
        if (saved === 'light' || saved === 'dark') return saved;
      } catch { /* file:// 下 localStorage 可能不可用 */ }
      return window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
    })(),
  };

  // ---------------------------------------------------------------- DOM
  const dom = {
    sessionTitle: document.getElementById('sessionTitle'),
    appbarMeta: document.getElementById('appbarMeta'),
    overviewStrip: document.getElementById('overviewStrip'),
    track: document.getElementById('track'),
    lanes: document.getElementById('lanes'),
    boundaries: document.getElementById('boundaries'),
    selection: document.getElementById('selection'),
    hoverLine: document.getElementById('hoverLine'),
    axis: document.getElementById('axis'),
    tooltip: document.getElementById('timelineTooltip'),
    tbody: document.getElementById('tbody'),
    inspector: document.getElementById('inspector'),
    search: document.getElementById('search'),
  };

  dom.sessionTitle.textContent = meta.title ?? meta.sessionId ?? 'opencode 会话';
  dom.sessionTitle.title = meta.title ?? '';
  if (meta.model) {
    const chip = el('span', 'chip');
    chip.dataset.tone = 'model';
    chip.innerHTML = esc((meta.model.provider ?? '') + '/' + (meta.model.id ?? '')) + (meta.model.variant ? ' · ' + esc(meta.model.variant) : '');
    dom.appbarMeta.appendChild(chip);
  }
  if (meta.agent) {
    const chip = el('span', 'chip');
    chip.innerHTML = 'agent <b>' + esc(meta.agent) + '</b>';
    dom.appbarMeta.appendChild(chip);
  }
  {
    const chip = el('span', 'chip');
    chip.innerHTML = '跨时 <b>' + esc(fmtMs(meta.wallMs)) + '</b>';
    chip.title = fmtDateTime(meta.firstMs) + ' → ' + fmtDateTime(meta.lastMs);
    dom.appbarMeta.appendChild(chip);
  }
  {
    const chip = el('span', 'chip');
    chip.dataset.tone = 'error';
    chip.innerHTML = '报错 <b>' + stats.errors + '</b> · 未完成 <b>' + stats.running + '</b>';
    chip.title = '红色 = 报错/中止；琥珀 = 未完成（导出时会话仍在跑）';
    dom.appbarMeta.appendChild(chip);
  }

  function metric(label, value, tone, title) {
    const wrap = el('div', 'metric');
    if (tone) wrap.dataset.tone = tone;
    if (title) wrap.title = title;
    wrap.appendChild(el('span', 'metric-label', label));
    wrap.appendChild(el('span', 'metric-value', value));
    dom.overviewStrip.appendChild(wrap);
  }
  metric('活跃', fmtMs(stats.busyMs), 'muted', '所有模型片段 + 工具片段的并集');
  metric('模型调用', stats.steps + ' 次 · ' + fmtMs(stats.modelMs), 'model', '思考 + 生成的并集，不含工具执行');
  metric('其中思考', fmtMs(stats.reasoningMs), 'model');
  metric('工具调用', stats.toolCalls + ' 次 · ' + fmtMs(stats.toolMs), 'tool', '不含 question 的等待人工');
  metric('等待人工', fmtMs(stats.waitHumanMs), 'wait', 'question 工具：等用户回答');
  metric('空闲', fmtMs(stats.idleMs), 'muted', '相邻活动之间没有任何记录的时间');
  metric('轮次', stats.turns + '（用户 ' + stats.userTurns + '）', 'muted');
  metric('报错/未完成', stats.errors + ' / ' + stats.running, 'error');
  if (EPISODES.length > 0) {
    metric('归因', episodeSummary.length + ' 类 · ' + EPISODES.length + ' 次 · ' + fmtMs(totalEpisodeMs), 'warn',
      '本地规则算出来的事件链（不联网、不调用模型）；窗口耗时是代价上限');
  }

  {
    const legend = el('div', 'legend-inline');
    const items = [
      { kind: 'user', label: '输入' },
      { kind: 'model', label: '模型（浅=思考 深=生成）' },
      { kind: 'tool', label: '工具' },
      { kind: 'wait', label: '等待人工' },
      { kind: 'error', label: '报错' },
      { kind: 'running', label: '未完成' },
      { kind: 'compaction', label: '压缩/续跑' },
    ];
    for (const item of items) {
      const wrap = el('span', 'legend-inline-item');
      wrap.appendChild(el('span', 'legend-inline-swatch')).dataset.kind = item.kind;
      wrap.appendChild(el('span', '', item.label));
      legend.appendChild(wrap);
    }
    dom.overviewStrip.appendChild(legend);
  }

  // ---------------------------------------------------------------- 活动区间
  function mergeIntervals(list) {
    const sorted = list.slice().sort((a, b) => a[0] - b[0]);
    const merged = [];
    for (const [s, e] of sorted) {
      const last = merged[merged.length - 1];
      if (last && s <= last[1]) last[1] = Math.max(last[1], e);
      else merged.push([s, e]);
    }
    return merged;
  }
  const ACTIVITY = mergeIntervals(records.filter((r) => r.t1 > r.t0).map((r) => [r.t0, r.t1]));
  const DATA_START = ACTIVITY.length ? ACTIVITY[0][0] : (meta.firstMs ?? 0);
  const DATA_END = ACTIVITY.length ? ACTIVITY[ACTIVITY.length - 1][1] : (meta.lastMs ?? DATA_START + 1);
  const GAPS = [];
  for (let i = 1; i < ACTIVITY.length; i++) GAPS.push({ t0: ACTIVITY[i - 1][1], t1: ACTIVITY[i][0] });
  const GAP_PX = 20;

  function currentDomain() {
    if (state.view && state.view.t1 > state.view.t0) return state.view;
    return { t0: DATA_START, t1: DATA_END };
  }
  function isCollapsed(gap) {
    return state.gapThreshold > 0 && (gap.t1 - gap.t0) > state.gapThreshold;
  }

  /** 把真实时间投影到像素：活动按真实时长、折叠的空闲固定 GAP_PX 宽。每次渲染按当前视口重算。 */
  function buildProjection(width) {
    const domain = currentDomain();
    const nodes = [];
    for (const [a, b] of ACTIVITY) {
      const s = Math.max(a, domain.t0);
      const e = Math.min(b, domain.t1);
      if (e > s) nodes.push({ kind: 'act', t0: s, t1: e });
    }
    for (const g of GAPS) {
      if (g.t1 <= domain.t0 || g.t0 >= domain.t1) continue;
      const s = Math.max(g.t0, domain.t0);
      const e = Math.min(g.t1, domain.t1);
      if (e <= s) continue;
      nodes.push({ kind: 'gap', t0: s, t1: e, collapsed: isCollapsed(g), gapT0: g.t0, gapT1: g.t1 });
    }
    nodes.sort((x, y) => x.t0 - y.t0);
    if (nodes.length === 0) nodes.push({ kind: 'gap', t0: domain.t0, t1: domain.t1, collapsed: false, gapT0: domain.t0, gapT1: domain.t1 });
    const collapsedCount = nodes.filter((n) => n.collapsed).length;
    const realMs = nodes.reduce((acc, n) => acc + (n.collapsed ? 0 : (n.t1 - n.t0)), 0);
    const availPx = Math.max(8, width - collapsedCount * GAP_PX);
    const k = realMs > 0 ? availPx / realMs : 1;
    let cursor = 0;
    for (const n of nodes) {
      n.p0 = cursor;
      n.p1 = cursor + (n.collapsed ? GAP_PX : (n.t1 - n.t0) * k);
      cursor = n.p1;
    }
    const total = Math.max(1, cursor);
    const project = {
      domain,
      nodes,
      k,
      width,
      pxOf(t) {
        const clamped = Math.min(domain.t1, Math.max(domain.t0, t));
        for (const n of nodes) {
          if (clamped >= n.t0 && clamped <= n.t1) {
            const ratio = n.t1 > n.t0 ? (clamped - n.t0) / (n.t1 - n.t0) : 0;
            return n.p0 + ratio * (n.p1 - n.p0);
          }
        }
        return clamped <= nodes[0].t0 ? nodes[0].p0 : total;
      },
      tOf(px) {
        const clamped = Math.min(total, Math.max(0, px));
        for (const n of nodes) {
          if (clamped >= n.p0 && clamped <= n.p1) {
            const ratio = n.p1 > n.p0 ? (clamped - n.p0) / (n.p1 - n.p0) : 0;
            return n.t0 + ratio * (n.t1 - n.t0);
          }
        }
        return clamped <= 0 ? domain.t0 : domain.t1;
      },
      collapsedNodes() {
        return nodes.filter((n) => n.collapsed);
      },
    };
    return project;
  }

  let projection = null;

  // ---------------------------------------------------------------- 时间轴
  function matchesQuery(rec) {
    if (!state.query) return true;
    return recordText(rec).toLowerCase().includes(state.query.toLowerCase());
  }

  function renderTimeline() {
    const width = dom.track.clientWidth;
    if (width <= 0) return;
    projection = buildProjection(width);
    const p = projection;
    const xPct = (t) => (p.pxOf(t) / width) * 100;
    const domain = p.domain;
    const leftPct = xPct(domain.t0);

    // 补偿链底纹：整段区间铺一层浅色，一眼看出"这段时间都在补救"
    dom.lanes.textContent = '';
    const bandFrag = document.createDocumentFragment();
    for (const chain of CHAINS) {
      if (chain.endMs < domain.t0 || chain.startMs > domain.t1) continue;
      const left = xPct(Math.max(chain.startMs, domain.t0)) - leftPct;
      const right = xPct(Math.min(chain.endMs, domain.t1)) - leftPct;
      const band = document.createElement('span');
      band.className = 'chain-band';
      band.dataset.cause = chain.code;
      band.style.left = left.toFixed(4) + '%';
      band.style.width = Math.max(0.1, right - left).toFixed(4) + '%';
      band.title = chain.label + ' · 第 ' + chain.turn + ' 轮 ' + chain.startStep + '-' + chain.endStep + ' 步（' + chain.steps + ' 步）· ' + fmtMs(chain.windowMs) + '\n' + chain.outcome;
      bandFrag.appendChild(band);
    }
    dom.lanes.appendChild(bandFrag);

    // bars
    const frag = document.createDocumentFragment();
    for (const rec of records) {
      if (rec.t1 === null && rec.status !== 'running') continue;
      const t1 = rec.t1 === null ? rec.t0 : rec.t1;
      if (t1 < domain.t0 || rec.t0 > domain.t1) continue;
      const left = xPct(rec.t0) - leftPct;
      const right = xPct(t1) - leftPct;
      const bar = document.createElement('span');
      bar.className = 'bar';
      bar.dataset.kind = rec.kind;
      bar.dataset.key = rec.key;
      if (rec.status === 'error') bar.dataset.status = 'error';
      if (rec.status === 'running') bar.dataset.status = 'running';
      if (rec.kind === 'tool' && rec.wait) bar.dataset.wait = 'true';
      if (rec.kind === 'model' && rec.think > 0 && rec.think < 1) {
        bar.dataset.split = 'true';
        bar.style.setProperty('--think', (rec.think * 100).toFixed(2) + '%');
      }
      const isMarker = rec.kind === 'user' || rec.kind === 'continue';
      bar.style.setProperty('--lane', String(rec.lane));
      bar.style.setProperty('--l', left.toFixed(4));
      bar.style.setProperty('--w', isMarker ? '0' : Math.max(0, right - left).toFixed(4));
      if (isMarker) { bar.style.width = '8px'; bar.style.minWidth = '8px'; }
      if (state.selected === rec.key) bar.dataset.current = 'true';
      if (state.hovered === rec.key) bar.dataset.hover = 'true';
      const outsideRange = state.range !== null && !(rec.t0 <= state.range.t1 && t1 >= state.range.t0);
      if (outsideRange || !matchesQuery(rec)) bar.dataset.dim = 'true';
      frag.appendChild(bar);
    }
    // 归因触发点：在轨道底部打一个彩色小标，点击跳到该步骤
    for (const episode of EPISODES) {
      if (episode.startMs < domain.t0 || episode.startMs > domain.t1) continue;
      const mark = document.createElement('span');
      mark.className = 'cause-mark';
      mark.dataset.cause = episode.code;
      mark.dataset.key = episodeKeyOf(episode.turn, episode.step);
      mark.style.left = (xPct(episode.startMs) - leftPct).toFixed(4) + '%';
      mark.title = episode.label + ' · 第 ' + episode.turn + ' 轮第 ' + episode.step + ' 步 · ' + fmtMs(episode.windowMs) + ' · 置信度 ' + episode.confidence;
      frag.appendChild(mark);
    }
    // 折叠断口：跨泳道虚线 + ⇥
    for (const node of p.collapsedNodes()) {
      const x = (node.p0 + node.p1) / 2 / width * 100;
      const line = el('span', 'break-line');
      line.style.left = x.toFixed(4) + '%';
      line.title = '空闲 ' + fmtMs(node.gapT1 - node.gapT0) + ' 已折叠（' + fmtDateTime(node.gapT0) + ' → ' + fmtDateTime(node.gapT1) + '）';
      frag.appendChild(line);
      const mark = el('span', 'break-mark', '⇥');
      mark.style.left = x.toFixed(4) + '%';
      mark.title = line.title;
      frag.appendChild(mark);
    }
    dom.lanes.appendChild(frag);

    // 轮次分界
    dom.boundaries.textContent = '';
    const bfrag = document.createDocumentFragment();
    for (const turn of turns) {
      if (turn.index === 1) continue;
      if (turn.startMs <= domain.t0 || turn.startMs >= domain.t1) continue;
      const line = el('span', 'boundary');
      line.style.left = (xPct(turn.startMs) - leftPct).toFixed(4) + '%';
      bfrag.appendChild(line);
    }
    dom.boundaries.appendChild(bfrag);

    // 框选
    if (state.range !== null) {
      const l = clamp(xPct(state.range.t0) - leftPct, 0, 100);
      const r = clamp(xPct(state.range.t1) - leftPct, 0, 100);
      dom.selection.hidden = false;
      dom.selection.style.setProperty('--l', l.toFixed(4));
      dom.selection.style.setProperty('--w', Math.max(0.1, r - l).toFixed(4));
    } else {
      dom.selection.hidden = true;
    }

    // 轴：等像素刻度，标签取该位置对应的真实时刻
    dom.axis.textContent = '';
    const vw = Math.max(1, dom.axis.clientWidth || width);
    const tickCount = clamp(Math.round(vw / 150), 3, 9);
    const spanDays = domain.t1 - domain.t0 > 36 * 3600 * 1000;
    const collapsedNodes = p.collapsedNodes();
    const usedPx = [];
    for (let i = 0; i <= tickCount; i++) {
      let px = (width * i) / tickCount;
      // 刻度落在折叠断口里时，时间不是线性的：挪到断口右侧（活动恢复处）
      for (const node of collapsedNodes) {
        if (px >= node.p0 && px <= node.p1) px = Math.min(width, node.p1 + 1);
      }
      if (usedPx.some((other) => Math.abs(other - px) < 26)) continue;
      usedPx.push(px);
      const t = p.tOf(px);
      const tick = el('span', 'axis-tick', spanDays ? fmtDateTime(t).slice(0, 11) : fmtHms(t));
      tick.style.left = ((px / width) * 100).toFixed(4) + '%';
      if (i === 0) tick.style.transform = 'none';
      else if (i === tickCount) {
        tick.style.transform = 'translateX(-100%)';
        tick.style.paddingLeft = '0';
        tick.style.paddingRight = '3px';
        tick.style.borderLeft = '0';
        tick.style.borderRight = '0.5px solid var(--border-l2)';
      } else tick.style.transform = 'translateX(-50%)';
      dom.axis.appendChild(tick);
    }
    for (const node of p.collapsedNodes()) {
      const l = (node.p0 / width) * 100;
      const wPct = ((node.p1 - node.p0) / width) * 100;
      const marker = el('span', 'axis-gap', wPct > 1.6 ? fmtShort(node.gapT1 - node.gapT0) : '');
      marker.style.left = l.toFixed(4) + '%';
      marker.style.width = Math.max(0.15, wPct).toFixed(4) + '%';
      marker.title = '空闲 ' + fmtMs(node.gapT1 - node.gapT0) + ' 已折叠（' + fmtDateTime(node.gapT0) + ' → ' + fmtDateTime(node.gapT1) + '）';
      dom.axis.appendChild(marker);
    }
  }

  // ---------------------------------------------------------------- 台账
  function recordText(rec) {
    if (rec.kind === 'user' || rec.kind === 'continue' || rec.kind === 'compaction') {
      return [rec.label, rec.turn.input.text, rec.turn.input.syntheticText ?? '', (rec.turn.input.attachments ?? []).join(' ')].join(' ');
    }
    if (rec.kind === 'model') {
      const step = rec.step;
      return [step.messageId, ...step.texts.map((t) => t.text), ...step.reasoning.map((t) => t.text)].join(' ');
    }
    const tool = rec.tool;
    return [tool.name, tool.argsText, tool.outputText, tool.errorText ?? ''].join(' ');
  }

  function toolSummary(tool) {
    let args = null;
    try { args = JSON.parse(tool.argsText); } catch { /* 参数不是 JSON：退回原文 */ }
    if (args && typeof args === 'object' && !Array.isArray(args)) {
      if (typeof args.command === 'string') return oneLine(args.command, 200);
      if (typeof args.filePath === 'string') return oneLine(args.filePath, 200);
      if (typeof args.path === 'string') return oneLine(args.path + (args.pattern ? ' · ' + args.pattern : ''), 200);
      if (typeof args.pattern === 'string') return oneLine(args.pattern, 200);
      if (typeof args.query === 'string') return oneLine(args.query, 200);
      if (typeof args.description === 'string') return oneLine(args.description, 200);
      if (Array.isArray(args.questions) && args.questions[0]) {
        return oneLine(args.questions[0].question ?? args.questions[0].header ?? '', 200);
      }
      const firstKey = Object.keys(args)[0];
      if (firstKey) return oneLine(firstKey + '=' + JSON.stringify(args[firstKey]), 200);
    }
    return oneLine(tool.argsText, 200);
  }
  function modelPreview(step) {
    const text = step.texts.map((t) => t.text).join('\n').trim();
    if (text) return oneLine(text, 200);
    if (step.reasoning.length > 0) return '（仅思考）' + oneLine(step.reasoning.map((r) => r.text).join(' '), 180);
    if (step.tools.length > 0) return '（仅工具调用）';
    return '无内容';
  }
  function statusLabel(rec) {
    if (rec.kind === 'tool') return rec.status === 'error' ? '失败' : rec.status === 'running' ? '未结束' : '已完成';
    if (rec.kind === 'model') return rec.step.status === 'error' ? '已中止' : rec.step.status === 'running' ? '未完成' : '已完成';
    return '—';
  }
  function buildTag(rec) {
    let kind = rec.kind;
    let text = '输入';
    if (rec.kind === 'model') { kind = 'model'; text = '模型'; }
    else if (rec.kind === 'tool') { kind = rec.wait ? 'wait' : 'tool'; text = rec.wait ? '等待人工' : '工具'; }
    else if (rec.kind === 'continue') { kind = 'continue'; text = '自动续跑'; }
    else if (rec.kind === 'compaction') { kind = 'compaction'; text = '压缩'; }
    if (rec.status === 'error') { kind = 'error'; text = rec.kind === 'model' ? '已中止' : '失败'; }
    const tag = el('span', 'tag', text);
    tag.dataset.kind = kind;
    return tag;
  }
  function rowDuration(rec) {
    if (rec.kind === 'user' || rec.kind === 'continue') return '—';
    if (rec.kind === 'compaction') return fmtMs(rec.turn.compaction?.durationMs ?? null);
    if (rec.kind === 'model') return fmtMs(rec.step.modelMs);
    return rec.tool.durationMs === null ? '未结束' : fmtMs(rec.tool.durationMs);
  }

  function buildRecordRow(rec) {
    const tr = document.createElement('tr');
    tr.className = 'row-record';
    tr.dataset.key = rec.key;
    tr.dataset.kind = rec.kind;
    tr.dataset.error = rec.status === 'error' ? 'true' : 'false';
    tr.dataset.status = rec.status;
    if (state.selected === rec.key) tr.dataset.selected = 'true';
    const t1 = rec.t1 ?? rec.t0;
    if (state.range !== null && !(rec.t0 <= state.range.t1 && t1 >= state.range.t0)) tr.dataset.dim = 'true';
    if (state.query && !matchesQuery(rec)) tr.dataset.dim = 'true';

    const ev = document.createElement('td');
    ev.className = 'cell-event';
    ev.appendChild(buildTag(rec));
    tr.appendChild(ev);

    const content = document.createElement('td');
    content.className = 'cell-content';
    let title = '';
    let detail = '';
    if (rec.kind === 'model') {
      title = '模型 · ' + (rec.step.modelId ?? '') + ' · 第 ' + rec.turn.index + ' 轮第 ' + rec.step.index + ' 步';
      detail = modelPreview(rec.step);
    } else if (rec.kind === 'tool') {
      title = rec.tool.name;
      detail = toolSummary(rec.tool);
    } else if (rec.kind === 'user') {
      title = '用户输入';
      detail = oneLine(rec.turn.input.text || '（空输入）', 200);
    } else if (rec.kind === 'continue') {
      title = '自动续跑';
      detail = oneLine(rec.turn.input.syntheticText ?? '', 200);
    } else {
      title = '上下文压缩';
      detail = 'auto=' + (rec.turn.compaction?.auto === true) + ' · 耗时 ' + fmtMs(rec.turn.compaction?.durationMs ?? null);
    }
    content.appendChild(el('span', 'rec-title', title));
    content.appendChild(document.createTextNode('  '));
    const detailEl = el('span', rec.kind === 'tool' ? 'rec-detail rec-mono' : 'rec-detail', detail);
    if (!detail || detail === '无内容') detailEl.classList.add('is-empty');
    content.appendChild(detailEl);
    if (rec.status === 'error') {
      const reason = rec.kind === 'model' ? (rec.step.error?.name ?? '错误') : (rec.tool.errorText ? oneLine(rec.tool.errorText, 60) : '工具失败');
      content.appendChild(el('span', 'rec-badge-error', '✕ ' + reason));
    } else if (rec.status === 'running') {
      content.appendChild(el('span', 'rec-badge-error', '● 未完成'));
    }
    tr.appendChild(content);

    const time = document.createElement('td');
    time.className = 'cell-time';
    time.textContent = rowDuration(rec);
    time.title = '开始 ' + fmtClock(rec.t0) + (rec.t1 !== null ? ' → 结束 ' + fmtClock(rec.t1) : '（未结束）');
    tr.appendChild(time);

    tr.addEventListener('click', () => selectRecord(rec.key));
    return tr;
  }

  function buildTable() {
    dom.tbody.textContent = '';
    const frag = document.createDocumentFragment();
    for (const turn of turns) {
      const collapsed = state.collapsed.has(turn.index);
      const turnRecords = records.filter((r) => r.turn === turn);
      const hasError = turnRecords.some((r) => r.status === 'error');
      const turnTr = document.createElement('tr');
      turnTr.className = 'row-turn';
      turnTr.dataset.turn = String(turn.index);
      turnTr.dataset.error = hasError ? 'true' : 'false';
      const td = document.createElement('td');
      td.colSpan = 3;
      const inner = el('div', 'row-turn-inner');
      inner.appendChild(el('span', 'turn-caret', collapsed ? '▸' : '▾'));
      inner.appendChild(el('span', 'turn-label', '第 ' + turn.index + (turn.kind === 'compaction' ? ' 段' : ' 轮')));
      const inputText = turn.kind === 'user'
        ? (turn.input.text || '（空输入）')
        : turn.kind === 'compaction'
          ? '（自动上下文压缩，不是用户输入）'
          : turn.kind === 'continue'
            ? '（自动续跑提示，不是用户输入）'
            : '（空输入）';
      const input = el('span', 'turn-input', oneLine(inputText, 160));
      input.title = turn.input.text || inputText;
      inner.appendChild(input);
      if (turn.input.attachments && turn.input.attachments.length > 0) {
        const chip = el('span', 'chip', '附件 ×' + turn.input.attachments.length);
        chip.title = turn.input.attachments.join('\n');
        inner.appendChild(chip);
      }
      const turnMs = turn.endMs !== null && turn.startMs !== null ? turn.endMs - turn.startMs : null;
      const turnTools = turn.steps.reduce((acc, s) => acc + s.tools.length, 0);
      inner.appendChild(el('span', 'turn-stats', turn.steps.length + ' 步 · ' + turnTools + ' 工具 · ' + fmtMs(turnMs)));
      const focusBtn = el('button', 'turn-focus', '⤢');
      focusBtn.type = 'button';
      focusBtn.title = '把时间轴缩放到本轮';
      focusBtn.addEventListener('click', (event) => {
        event.stopPropagation();
        focusRange(turn.startMs, Math.max(turn.endMs, turn.startMs + 1));
      });
      inner.appendChild(focusBtn);
      td.appendChild(inner);
      turnTr.appendChild(td);
      frag.appendChild(turnTr);
      if (collapsed) continue;

      const turnRec = recordByKey.get('t' + turn.index);
      if (turnRec) frag.appendChild(buildRecordRow(turnRec));

      for (const step of turn.steps) {
        const stepTr = document.createElement('tr');
        stepTr.className = 'row-step';
        stepTr.dataset.error = step.status === 'error' ? 'true' : 'false';
        stepTr.dataset.key = 't' + turn.index + 's' + step.index;
        const std = document.createElement('td');
        std.colSpan = 3;
        const sin = el('div', 'step-inner');
        sin.appendChild(el('span', 'step-name', '第 ' + step.index + ' 步'));
        const noTextParts = step.reasoningMs === 0 && step.textMs === 0;
        const bits = noTextParts
          ? ['无 reasoning/text part']
          : ['思考 ' + fmtMs(step.reasoningMs), '生成 ' + fmtMs(step.textMs)];
        if (step.modelMs !== null) bits.push('模型 ' + fmtMs(step.modelMs) + (step.modelSpanSource === 'tool-gap' ? '（step 起点→首个工具）' : ''));
        if (step.tools.length > 0) bits.push('工具 ' + step.tools.length + ' 个 · ' + fmtMs(step.tools.reduce((a, t) => a + (t.durationMs ?? 0), 0)));
        bits.push('步骤 ' + fmtMs(step.stepMs));
        if (step.status === 'error') bits.push('已中止：' + (step.error?.name ?? ''));
        if (step.status === 'running') bits.push('未完成');
        sin.appendChild(el('span', '', bits.join(' · ')));
        const stepEpisodes = episodesByStep.get('t' + turn.index + 's' + step.index);
        if (stepEpisodes && stepEpisodes.length > 0) {
          const badge = el('span', 'cause-badge');
          badge.dataset.cause = stepEpisodes[0].code;
          badge.textContent = '归因 ' + stepEpisodes.length;
          badge.title = stepEpisodes.map((episode) => episode.label + '：' + episode.outcome).join('\n');
          sin.appendChild(badge);
        }
        if (step.finish) sin.appendChild(el('span', 'step-name', 'finish: ' + step.finish));
        if (step.tokens && step.tokens.total) sin.appendChild(el('span', 'step-name', 'tok ' + fmtNum(step.tokens.total)));
        std.appendChild(sin);
        stepTr.appendChild(std);
        frag.appendChild(stepTr);

        const modelRec = recordByKey.get('t' + turn.index + 's' + step.index);
        if (modelRec) frag.appendChild(buildRecordRow(modelRec));
        for (const tool of step.tools) {
          const rec = recordByKey.get('t' + turn.index + 's' + step.index + 'c' + tool.callId);
          if (rec) frag.appendChild(buildRecordRow(rec));
        }
      }
      if (turn.steps.length === 0) {
        const tr = document.createElement('tr');
        tr.className = 'row-record';
        const tdEmpty = document.createElement('td');
        tdEmpty.colSpan = 3;
        tdEmpty.className = 'empty-state';
        tdEmpty.textContent = '该段没有模型记录';
        tr.appendChild(tdEmpty);
        frag.appendChild(tr);
      }
    }
    dom.tbody.appendChild(frag);
  }

  function refreshTableStates() {
    for (const tr of dom.tbody.querySelectorAll('tr.row-record')) {
      const rec = recordByKey.get(tr.dataset.key);
      if (!rec) {
        // 占位行（该段没有模型记录）没有可匹配的内容：搜索时一并压暗。
        if (state.query) tr.dataset.dim = 'true';
        else delete tr.dataset.dim;
        continue;
      }
      if (state.selected === rec.key) tr.dataset.selected = 'true';
      else delete tr.dataset.selected;
      const t1 = rec.t1 ?? rec.t0;
      const outside = state.range !== null && !(rec.t0 <= state.range.t1 && t1 >= state.range.t0);
      if (outside || (state.query && !matchesQuery(rec))) tr.dataset.dim = 'true';
      else delete tr.dataset.dim;
    }
  }

  // ---------------------------------------------------------------- 检查器
  function mdToHtml(src) {
    const lines = String(src ?? '').split('\n');
    const out = [];
    let inCode = false;
    let code = [];
    let listType = null;
    const closeList = () => { if (listType) { out.push('</' + listType + '>'); listType = null; } };
    const inline = (s) => esc(s)
      .replace(/\x60([^\x60]+)\x60/g, (m, c) => '<code>' + c + '</code>')
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>');
    for (const raw of lines) {
      if (raw.trim().startsWith('\x60\x60\x60')) {
        if (inCode) { out.push('<pre><code>' + esc(code.join('\n')) + '</code></pre>'); code = []; inCode = false; }
        else { closeList(); inCode = true; }
        continue;
      }
      if (inCode) { code.push(raw); continue; }
      const line = raw.replace(/\s+$/, '');
      if (!line) { closeList(); continue; }
      const heading = /^(#{1,6})\s+(.*)$/.exec(line);
      if (heading) {
        closeList();
        out.push('<h' + heading[1].length + '>' + inline(heading[2]) + '</h' + heading[1].length + '>');
        continue;
      }
      if (/^\s*[-*+]\s+/.test(line)) {
        if (listType !== 'ul') { closeList(); out.push('<ul>'); listType = 'ul'; }
        out.push('<li>' + inline(line.replace(/^\s*[-*+]\s+/, '')) + '</li>');
        continue;
      }
      if (/^\s*\d+[.)]\s+/.test(line)) {
        if (listType !== 'ol') { closeList(); out.push('<ol>'); listType = 'ol'; }
        out.push('<li>' + inline(line.replace(/^\s*\d+[.)]\s+/, '')) + '</li>');
        continue;
      }
      if (/^>\s?/.test(line)) { closeList(); out.push('<blockquote>' + inline(line.replace(/^>\s?/, '')) + '</blockquote>'); continue; }
      if (/^(-{3,}|\*{3,})$/.test(line.trim())) { closeList(); out.push('<hr>'); continue; }
      closeList();
      out.push('<p>' + inline(line) + '</p>');
    }
    if (inCode) out.push('<pre><code>' + esc(code.join('\n')) + '</code></pre>');
    closeList();
    return out.join('\n');
  }

  function inspectorShell(title, tagRec, bodyBuilder) {
    dom.inspector.textContent = '';
    const head = el('div', 'insp-head');
    if (tagRec) head.appendChild(buildTag(tagRec));
    const titleEl = el('div', 'insp-title', title);
    titleEl.title = title;
    head.appendChild(titleEl);
    const close = el('button', 'insp-close', '✕');
    close.type = 'button';
    close.title = '关闭详情（Esc）';
    close.addEventListener('click', () => { state.selected = null; state.tab = null; refreshAll(); });
    head.appendChild(close);
    dom.inspector.appendChild(head);
    if (bodyBuilder) {
      const body = el('div', 'insp-body');
      bodyBuilder(body);
      dom.inspector.appendChild(body);
    }
  }

  function renderSummaryPanel() {
    inspectorShell('总览', null, (body) => {
      const section = el('section', 'insp-section');
      section.appendChild(el('h3', 'insp-section-title', '关键指标'));
      const grid = el('div', 'stat-grid');
      const stat = (label, value, tone, title) => {
        const box = el('div', 'stat');
        if (title) box.title = title;
        box.appendChild(el('span', 'stat-label', label));
        const v = el('span', 'stat-value', value);
        if (tone) v.dataset.tone = tone;
        box.appendChild(v);
        grid.appendChild(box);
      };
      stat('活跃（忙碌）', fmtMs(stats.busyMs), 'muted', '模型片段 ∪ 工具片段');
      stat('会话跨时', fmtMs(meta.wallMs), 'muted', fmtDateTime(meta.firstMs) + ' → ' + fmtDateTime(meta.lastMs));
      stat('模型调用', stats.steps + ' 次', 'model');
      stat('模型耗时', fmtMs(stats.modelMs), 'model', '思考 + 生成，不含工具');
      stat('工具调用', stats.toolCalls + ' 次', 'tool');
      stat('工具执行', fmtMs(stats.toolMs), 'tool', '不含 question 的等待人工');
      stat('等待人工', fmtMs(stats.waitHumanMs), 'wait', 'question 工具');
      stat('空闲', fmtMs(stats.idleMs), 'muted');
      stat('报错 / 未完成', stats.errors + ' / ' + stats.running, 'error', '红色 = 中止；琥珀 = 未完成');
      stat('tokens 输入/输出', fmtNum(stats.tokens.input) + ' / ' + fmtNum(stats.tokens.output), 'muted');
      section.appendChild(grid);
      body.appendChild(section);

      // ---- 补偿链 ----
      if (CHAINS.length > 0) {
        const chainSection = el('section', 'insp-section');
        chainSection.appendChild(el('h3', 'insp-section-title', '补偿链 · ' + CHAINS.length + ' 条'));
        chainSection.appendChild(el('p', 'hint', '一次故障引发的连续补救区间：步数比耗时更能说明代价。点击跳到该区间。'));
        for (const chain of CHAINS) {
          const row = el('button', 'cause-row chain-row');
          row.dataset.cause = chain.code;
          row.type = 'button';
          row.appendChild(el('span', 'cause-dot'));
          const main = el('div', 'cause-main');
          main.appendChild(el('span', 'cause-name', '第 ' + chain.turn + ' 轮 ' + chain.startStep + '–' + chain.endStep + ' 步 · ' + chain.steps + ' 步'));
          main.appendChild(el('span', 'cause-sub', chain.outcome));
          row.appendChild(main);
          row.appendChild(el('span', 'cause-value', fmtMs(chain.windowMs)));
          row.title = chain.label;
          row.addEventListener('click', () => {
            focusRange(chain.startMs, chain.endMs);
            selectRecord(episodeKeyOf(chain.turn, chain.startStep));
          });
          chainSection.appendChild(row);
        }
        body.appendChild(chainSection);
      }

      // ---- 归因 ----
      const attributionSection = el('section', 'insp-section');
      attributionSection.appendChild(el('h3', 'insp-section-title', '归因 · ' + episodeSummary.length + ' 类 / ' + EPISODES.length + ' 次'));
      if (EPISODES.length === 0) {
        attributionSection.appendChild(el('p', 'hint', '没有检测到异常事件链：工具都一次成功、没有报错、没有压缩。'));
      } else {
        attributionSection.appendChild(el('p', 'hint', '规则在本地算（不联网、不调用模型）：窗口耗时＝从触发到恢复到的时间窗，是代价上限。'));
        const list = el('div', 'cause-list');
        for (const entry of episodeSummary) {
          const row = el('button', 'cause-row');
          row.type = 'button';
          row.dataset.cause = entry.code;
          const dot = el('span', 'cause-dot');
          row.appendChild(dot);
          const main = el('div', 'cause-main');
          main.appendChild(el('span', 'cause-name', entry.label));
          main.appendChild(el('span', 'cause-sub', entry.count + ' 次 · 置信度 ' + entry.confidence));
          row.appendChild(main);
          row.appendChild(el('span', 'cause-value', fmtMs(entry.windowMs)));
          row.title = '点击跳到第一条：' + entry.label;
          row.addEventListener('click', () => {
            const first = EPISODES.find((episode) => episode.code === entry.code);
            if (first) selectRecord(episodeKeyOf(first.turn, first.step));
          });
          list.appendChild(row);
        }
        attributionSection.appendChild(list);
        attributionSection.appendChild(el('p', 'hint', '归因窗口合计 ' + fmtMs(totalEpisodeMs) + '；点时间轴底部的彩色小标可直接定位。'));
      }
      body.appendChild(attributionSection);

      const compo = el('section', 'insp-section');
      compo.appendChild(el('h3', 'insp-section-title', '时间构成（活跃时段内）'));
      const parts = [
        { kind: 'think', label: '模型 · 思考', value: stats.reasoningMs },
        { kind: 'decode', label: '模型 · 生成', value: stats.textMs },
        { kind: 'tool', label: '工具执行', value: stats.toolMs },
        { kind: 'wait', label: '等待人工（question）', value: stats.waitHumanMs },
        { kind: 'compaction', label: '上下文压缩', value: stats.compactionMs },
      ].filter((part) => part.value > 0);
      const total = parts.reduce((acc, part) => acc + part.value, 0) || 1;
      const bar = el('div', 'compo');
      for (const part of parts) {
        const seg = el('div', 'compo-seg');
        seg.dataset.kind = part.kind;
        seg.style.width = (part.value / total) * 100 + '%';
        seg.title = part.label + ' ' + fmtMs(part.value) + '（' + pct(part.value, total) + '）';
        bar.appendChild(seg);
      }
      compo.appendChild(bar);
      const legend = el('div', 'legend');
      for (const part of parts) {
        const row = el('div', 'legend-row');
        const dot = el('span', 'legend-dot');
        dot.style.background = 'var(--' + (part.kind === 'think' ? 'assistant-think' : part.kind === 'decode' ? 'assistant-decode' : part.kind === 'tool' ? 'state-warn-label' : part.kind === 'wait' ? 'wait-color' : 'label-tertiary') + ')';
        row.appendChild(dot);
        row.appendChild(el('span', '', part.label));
        row.appendChild(el('span', 'legend-value', fmtMs(part.value)));
        row.appendChild(el('span', 'legend-pct', pct(part.value, total)));
        legend.appendChild(row);
      }
      compo.appendChild(legend);
      compo.appendChild(el('p', 'hint', '区间全部取自 part 的原始时间戳；question 的等待人工与压缩单列，不混进模型/工具耗时。压缩段与同段模型调用在时间上重叠，构成图按各段自身耗时累加。'));
      body.appendChild(compo);

      const rank = el('section', 'insp-section');
      rank.appendChild(el('h3', 'insp-section-title', '耗时排行'));
      const tabsWrap = el('div');
      tabsWrap.style.display = 'flex';
      tabsWrap.style.gap = '2px';
      tabsWrap.style.marginBottom = '6px';
      const list = el('ol', 'rank-list');
      let active = 'tool';
      const renderList = () => {
        list.textContent = '';
        const pool = active === 'tool'
          ? records.filter((r) => r.kind === 'tool' && r.tool.durationMs !== null)
          : records.filter((r) => r.kind === 'model' && r.step.modelMs !== null);
        const sorted = pool.slice().sort((a, b) => recDur(b) - recDur(a)).slice(0, 8);
        const max = sorted.length ? recDur(sorted[0]) : 1;
        sorted.forEach((rec, index) => {
          const dur = recDur(rec);
          const row = el('button', 'rank-row');
          row.type = 'button';
          row.title = '第 ' + rec.turn.index + ' 轮' + (rec.step ? '第 ' + rec.step.index + ' 步' : '') + ' · ' + fmtClock(rec.t0);
          row.appendChild(el('span', 'rank-index', String(index + 1)));
          const main = el('div', 'rank-main');
          main.appendChild(el('span', 'rank-name', rec.kind === 'tool' ? (rec.wait ? 'question（等待人工）' : rec.tool.name) : '模型 · 第 ' + rec.turn.index + ' 轮第 ' + rec.step.index + ' 步'));
          main.appendChild(el('span', 'rank-sub', '第 ' + rec.turn.index + ' 轮 · ' + fmtHms(rec.t0)));
          row.appendChild(main);
          row.appendChild(el('span', 'rank-value', fmtMs(dur)));
          const barEl = el('span', 'rank-bar');
          barEl.dataset.kind = rec.status === 'error' ? 'error' : rec.wait ? 'wait' : rec.kind === 'tool' ? 'tool' : 'model';
          barEl.style.width = Math.max(2, (dur / max) * 100) + '%';
          row.appendChild(barEl);
          row.addEventListener('click', () => selectRecord(rec.key, { reveal: true }));
          list.appendChild(row);
        });
      };
      for (const def of [{ id: 'tool', label: '工具调用' }, { id: 'model', label: '模型调用' }]) {
        const btn = el('button', 'tab', def.label);
        btn.type = 'button';
        btn.style.height = '26px';
        btn.style.lineHeight = '26px';
        btn.setAttribute('aria-selected', def.id === active ? 'true' : 'false');
        btn.addEventListener('click', () => {
          active = def.id;
          for (const other of tabsWrap.querySelectorAll('.tab')) other.setAttribute('aria-selected', other === btn ? 'true' : 'false');
          renderList();
        });
        tabsWrap.appendChild(btn);
      }
      rank.appendChild(tabsWrap);
      rank.appendChild(list);
      renderList();
      body.appendChild(rank);

      if (warnings.length > 0) {
        const notes = el('section', 'insp-section');
        notes.appendChild(el('h3', 'insp-section-title', '数据提示 · ' + warnings.length));
        const ul = el('ul', 'notices');
        for (const warning of warnings) {
          const li = el('li', 'notice');
          li.dataset.level = warning.level;
          li.appendChild(el('span', 'notice-dot'));
          const text = el('div');
          text.appendChild(el('div', 'notice-title', warning.title));
          text.appendChild(el('div', 'notice-detail', warning.detail));
          li.appendChild(text);
          ul.appendChild(li);
        }
        notes.appendChild(ul);
        body.appendChild(notes);
      }

      const about = el('section', 'insp-section');
      about.appendChild(el('h3', 'insp-section-title', '数据来源'));
      const dl = el('dl', 'overview');
      const put = (key, value) => {
        dl.appendChild(el('dt', '', key));
        dl.appendChild(el('dd', '', value ?? '—'));
      };
      put('session', meta.sessionId);
      put('标题', meta.title);
      put('目录', meta.directory);
      put('opencode', meta.version);
      put('导出时间', meta.exportedAt);
      put('区间', fmtDateTime(meta.firstMs) + ' → ' + fmtDateTime(meta.lastMs));
      put('token 输入', fmtNum(stats.tokens.input));
      put('token 输出', fmtNum(stats.tokens.output));
      put('token 推理', fmtNum(stats.tokens.reasoning));
      put('缓存读取', fmtNum(stats.tokens.cacheRead));
      about.appendChild(dl);
      body.appendChild(about);
    });
  }

  function causePanel(episodes) {
    const wrap = el('div');
    for (const episode of episodes) {
      const card = el('div', 'cause-card');
      card.dataset.cause = episode.code;
      const head = el('div', 'cause-card-head');
      head.appendChild(el('span', 'cause-dot'));
      head.appendChild(el('span', 'cause-name', episode.label));
      head.appendChild(el('span', 'cause-conf', '置信度 ' + episode.confidence));
      head.appendChild(el('span', 'cause-value', fmtMs(episode.windowMs)));
      card.appendChild(head);
      for (const evidence of episode.evidence ?? []) {
        const line = el('div', 'cause-evidence');
        line.appendChild(el('span', 'cause-kind', evidence.kind));
        line.appendChild(el('span', 'cause-text', evidence.text));
        card.appendChild(line);
      }
      if ((episode.response ?? []).length > 0) {
        card.appendChild(el('div', 'cause-label', '补偿动作'));
        for (const response of episode.response) {
          card.appendChild(el('div', 'cause-response', response));
        }
      }
      card.appendChild(el('div', 'cause-outcome', '结论：' + episode.outcome));
      wrap.appendChild(card);
    }
    wrap.appendChild(el('p', 'hint', '以上由本地规则判定（A=结构化字段，B=结构化+序列模式），没有调用模型。'));
    return wrap;
  }

  function timingTable(rec) {
    const wrap = el('div');
    const rows = [];
    if (rec.kind === 'model') {
      for (const item of rec.step.reasoning) rows.push({ type: 'reasoning', t0: item.startMs, t1: item.endMs });
      for (const item of rec.step.texts) rows.push({ type: 'text', t0: item.startMs, t1: item.endMs });
      for (const tool of rec.step.tools) rows.push({ type: 'tool · ' + tool.name, t0: tool.startMs, t1: tool.endMs });
    } else if (rec.kind === 'tool') {
      rows.push({ type: 'tool · ' + rec.tool.name, t0: rec.tool.startMs, t1: rec.tool.endMs });
    } else if (rec.kind === 'compaction') {
      rows.push({ type: 'compaction', t0: rec.t0, t1: rec.t1 });
    }
    const table = el('table');
    table.style.width = '100%';
    table.style.borderCollapse = 'collapse';
    table.style.font = '11px/16px var(--font-code)';
    const head = document.createElement('tr');
    for (const label of ['part', '开始', '结束', '时长']) {
      const th = el('th', '', label);
      th.style.cssText = 'text-align:left;color:var(--label-tertiary);font-weight:500;padding:2px 6px 4px 0';
      head.appendChild(th);
    }
    table.appendChild(head);
    for (const row of rows) {
      const tr = document.createElement('tr');
      const cell = (text, color) => {
        const td = el('td', '', text);
        td.style.cssText = 'padding:2px 6px 2px 0;font-variant-numeric:tabular-nums;color:' + (color ?? 'var(--label-secondary)');
        return td;
      };
      tr.appendChild(cell(row.type));
      tr.appendChild(cell(fmtClock(row.t0)));
      tr.appendChild(cell(row.t1 === null ? '未结束' : fmtClock(row.t1)));
      tr.appendChild(cell(row.t1 === null ? '—' : fmtMs(row.t1 - row.t0)));
      table.appendChild(tr);
    }
    wrap.appendChild(table);
    return wrap;
  }

  function renderInspector() {
    if (state.selected === null) { renderSummaryPanel(); return; }
    const rec = recordByKey.get(state.selected);
    if (!rec) { renderSummaryPanel(); return; }
    const title = rec.kind === 'model'
      ? '第 ' + rec.turn.index + ' 轮第 ' + rec.step.index + ' 步 · 模型调用'
      : rec.kind === 'tool'
        ? rec.tool.name + ' · 第 ' + rec.turn.index + ' 轮第 ' + rec.step.index + ' 步'
        : rec.kind === 'user'
          ? '第 ' + rec.turn.index + ' 轮 · 用户输入'
          : rec.kind === 'continue'
            ? '第 ' + rec.turn.index + ' 轮 · 自动续跑'
            : '第 ' + rec.turn.index + ' 段 · 上下文压缩';

    inspectorShell(title, rec, (body) => {
      const dl = el('dl', 'overview');
      const put = (key, value, cls = '') => {
        dl.appendChild(el('dt', '', key));
        const dd = el('dd', cls, value ?? '—');
        dl.appendChild(dd);
        return dd;
      };
      put('状态', statusLabel(rec), rec.status === 'error' ? 'error' : rec.status === 'running' ? 'muted' : '');
      if (rec.kind === 'model') {
        put('轮次/步骤', '第 ' + rec.turn.index + ' 轮 · 第 ' + rec.step.index + ' 步');
        put('模型', (rec.step.provider ?? meta.model?.provider ?? '') + '/' + (rec.step.modelId ?? meta.model?.id ?? ''));
        put('开始', fmtClock(rec.t0));
        put('结束', fmtClock(rec.t1));
        put('模型耗时', fmtMs(rec.step.modelMs) + '（思考 ' + fmtMs(rec.step.reasoningMs) + ' + 生成 ' + fmtMs(rec.step.textMs) + '）', 'num');
        put('步骤总时长', fmtMs(rec.step.stepMs) + '（含工具）', 'num');
        if (rec.step.tokens) {
          put('tokens', '总 ' + fmtNum(rec.step.tokens.total) + ' · 输入 ' + fmtNum(rec.step.tokens.input) + '（缓存 ' + fmtNum(rec.step.tokens.cacheRead) + '）', 'num');
          put('输出/推理', fmtNum(rec.step.tokens.output) + ' / ' + fmtNum(rec.step.tokens.reasoning), 'num');
        }
        put('finish', rec.step.finish ?? '（未写）');
        if (rec.step.error) put('错误', rec.step.error.name + ' · ' + rec.step.error.message, 'error');
        put('message id', rec.step.messageId);
      } else if (rec.kind === 'tool') {
        put('轮次/步骤', '第 ' + rec.turn.index + ' 轮 · 第 ' + rec.step.index + ' 步');
        put('开始', fmtClock(rec.t0));
        put('结束', rec.t1 === null ? '未结束（导出时会话仍在跑）' : fmtClock(rec.t1), rec.t1 === null ? 'error' : '');
        put('工具耗时', rec.tool.durationMs === null ? '—（不虚构）' : fmtMs(rec.tool.durationMs), 'num');
        put('opencode 状态', rec.tool.status);
        if (rec.wait) put('说明', 'question 的耗时是等人工回答，不是机器执行');
        put('call id', rec.tool.callId);
      } else {
        put('时间', fmtClock(rec.t0));
        put('输入', rec.turn.input.text ? oneLine(rec.turn.input.text, 80) : '（空）', 'muted');
        if (rec.turn.input.attachments?.length) put('附件', rec.turn.input.attachments.join('、'));
        if (rec.kind === 'compaction') {
          put('压缩耗时', fmtMs(rec.turn.compaction?.durationMs ?? null), 'num');
          put('auto', String(rec.turn.compaction?.auto === true));
        }
        put('本段步骤', rec.turn.steps.length + ' 步');
      }
      body.appendChild(dl);

      const tabDefs = [];
      if (rec.kind === 'model') {
        tabDefs.push({ id: 'summary', label: '概述', build: () => {
          const wrap = el('div', 'md');
          wrap.innerHTML = mdToHtml(rec.step.texts.map((t) => t.text).join('\n\n') || '（无助手文本，仅思考/工具调用）');
          return wrap;
        } });
        tabDefs.push({ id: 'thinking', label: '思考', count: rec.step.reasoning.length, build: () => {
          const wrap = el('div');
          wrap.appendChild(el('pre', 'pre', rec.step.reasoning.map((r) => r.text).join('\n\n') || '（无思考内容）'));
          return wrap;
        } });
        tabDefs.push({ id: 'output', label: '原始输出', build: () => {
          const wrap = el('div');
          wrap.appendChild(el('pre', 'pre', rec.step.texts.map((t) => t.text).join('\n\n') || '（无输出文本）'));
          return wrap;
        } });
        tabDefs.push({ id: 'calls', label: '工具', count: rec.step.tools.length, build: () => {
          const wrap = el('div');
          if (rec.step.tools.length === 0) { wrap.appendChild(el('p', 'hint', '本步没有工具调用')); return wrap; }
          const list = el('div');
          list.style.cssText = 'display:grid;gap:4px';
          for (const tool of rec.step.tools) {
            const row = el('button', 'rank-row');
            row.type = 'button';
            const main = el('div', 'rank-main');
            main.appendChild(el('span', 'rank-name', tool.name));
            main.appendChild(el('span', 'rank-sub', oneLine(toolSummary(tool), 90)));
            row.appendChild(el('span', 'rank-index', ''));
            row.appendChild(main);
            row.appendChild(el('span', 'rank-value', tool.durationMs === null ? '未结束' : fmtMs(tool.durationMs)));
            row.addEventListener('click', () => selectRecord('t' + rec.turn.index + 's' + rec.step.index + 'c' + tool.callId));
            list.appendChild(row);
          }
          wrap.appendChild(list);
          return wrap;
        } });
        tabDefs.push({ id: 'parts', label: '计时', build: () => timingTable(rec) });
        const modelEpisodes = episodesByStep.get('t' + rec.turn.index + 's' + rec.step.index) ?? [];
        if (modelEpisodes.length > 0) {
          tabDefs.push({ id: 'cause', label: '归因', count: modelEpisodes.length, build: () => causePanel(modelEpisodes) });
        }
      } else if (rec.kind === 'tool') {
        tabDefs.push({ id: 'params', label: '参数', build: () => {
          const wrap = el('div');
          wrap.appendChild(el('pre', 'pre', rec.tool.argsText || '（无参数记录）'));
          return wrap;
        } });
        tabDefs.push({ id: 'result', label: '结果', build: () => {
          const wrap = el('div');
          const isError = rec.tool.status === 'error';
          const text = rec.tool.errorText ?? rec.tool.outputText;
          wrap.appendChild(el('pre', isError ? 'pre is-error' : 'pre',
            text || (rec.tool.status === 'running' ? '（仍在执行：导出时会话未结束）' : '（无输出）')));
          return wrap;
        } });
        tabDefs.push({ id: 'parts', label: '计时', build: () => timingTable(rec) });
      } else {
        tabDefs.push({ id: 'input', label: '输入', build: () => {
          const wrap = el('div');
          const text = rec.kind === 'continue' ? (rec.turn.input.syntheticText ?? '') : rec.turn.input.text;
          const md = el('div', 'md');
          md.innerHTML = mdToHtml(text || '（无文本）');
          wrap.appendChild(md);
          if (rec.turn.input.attachments?.length) {
            wrap.appendChild(el('h3', 'insp-section-title', '附件'));
            const ul = el('ul', 'notices');
            for (const name of rec.turn.input.attachments) {
              const li = el('li', 'notice');
              li.appendChild(el('span', 'notice-dot'));
              li.appendChild(el('div', 'notice-title', name));
              ul.appendChild(li);
            }
            wrap.appendChild(ul);
          }
          return wrap;
        } });
        if (rec.kind === 'compaction') tabDefs.push({ id: 'parts', label: '计时', build: () => timingTable(rec) });
      }

      const activeId = state.tab && tabDefs.some((def) => def.id === state.tab) ? state.tab : tabDefs[0].id;
      const tabs = el('div', 'tabs');
      const panelWrap = el('div');
      const panels = new Map();
      for (const def of tabDefs) {
        const btn = el('button', 'tab', def.label + (def.count ? ' ' + def.count : ''));
        btn.type = 'button';
        btn.setAttribute('aria-selected', def.id === activeId ? 'true' : 'false');
        btn.addEventListener('click', () => {
          state.tab = def.id;
          for (const [id, panel] of panels) panel.hidden = id !== def.id;
          for (const other of tabs.querySelectorAll('.tab')) other.setAttribute('aria-selected', other === btn ? 'true' : 'false');
        });
        tabs.appendChild(btn);
      }
      for (const def of tabDefs) {
        const panel = el('div', 'tab-panel');
        panel.hidden = def.id !== activeId;
        panel.appendChild(def.build());
        panels.set(def.id, panel);
        panelWrap.appendChild(panel);
      }
      body.appendChild(tabs);
      body.appendChild(panelWrap);
    });
  }

  // ---------------------------------------------------------------- 视口操作
  function refreshAll() {
    refreshTableStates();
    renderInspector();
    renderTimeline();
  }

  function selectRecord(key, opts = {}) {
    const rec = recordByKey.get(key);
    if (!rec) return;
    state.selected = key;
    state.tab = null;
    if (state.collapsed.has(rec.turn.index)) {
      state.collapsed.delete(rec.turn.index);
      buildTable();
    }
    refreshAll();
    if (opts.reveal !== false) {
      const row = dom.tbody.querySelector('tr.row-record[data-key="' + CSS.escape(key) + '"]');
      if (row) row.scrollIntoView({ block: 'center' });
    }
  }

  /** 把时间轴缩放到给定真实时间区间。 */
  function focusRange(t0, t1) {
    const span = Math.max(1, t1 - t0);
    const pad = Math.max(400, span * 0.06);
    const start = Math.max(DATA_START, t0 - pad);
    const end = Math.min(DATA_END, t1 + pad);
    const total = DATA_END - DATA_START;
    state.view = (end - start) >= total * 0.995 ? null : { t0: start, t1: end };
    state.range = null;
    refreshTableStates();
    renderTimeline();
  }
  function fitAll() {
    state.view = null;
    state.range = null;
    refreshAll();
  }

  // ---------------------------------------------------------------- 交互
  let drag = null;

  function trackRect() { return dom.track.getBoundingClientRect(); }
  function fractionOf(event) {
    const rect = trackRect();
    return clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
  }
  function timeAt(event) {
    if (!projection) return DATA_START;
    return projection.tOf(fractionOf(event) * trackRect().width);
  }
  function keyAt(event) {
    const target = event.target instanceof Element ? event.target.closest('[data-key]') : null;
    return target ? target.dataset.key : null;
  }
  function showTooltip(event, key) {
    if (!key) { dom.tooltip.hidden = true; return; }
    const rec = recordByKey.get(key);
    if (!rec) { dom.tooltip.hidden = true; return; }
    const lines = ['<b>' + esc(rec.label) + '</b>'];
    lines.push('第 ' + rec.turn.index + ' 轮' + (rec.step ? ' · 第 ' + rec.step.index + ' 步' : ''));
    if (rec.kind === 'tool') lines.push(esc(toolSummary(rec.tool)));
    if (rec.kind === 'model') lines.push('思考 ' + fmtMs(rec.step.reasoningMs) + ' · 生成 ' + fmtMs(rec.step.textMs));
    if (rec.t1 !== null && rec.t1 > rec.t0) {
      lines.push(fmtClock(rec.t0) + ' → ' + fmtClock(rec.t1));
      lines.push('总计 ' + fmtMs(rec.t1 - rec.t0));
    } else if (rec.kind === 'model' || rec.kind === 'tool') {
      lines.push('开始标记 ' + fmtClock(rec.t0) + '（无结束时间）');
    } else {
      lines.push('输入标记 ' + fmtClock(rec.t0));
    }
    if (rec.status === 'error') lines.push('状态：报错 / 中止');
    if (rec.status === 'running') lines.push('状态：未完成');
    dom.tooltip.innerHTML = lines.join('\n');
    dom.tooltip.hidden = false;
    const rect = dom.tooltip.getBoundingClientRect();
    const x = Math.min(window.innerWidth - rect.width - 8, Math.max(8, event.clientX + 12));
    const y = event.clientY + 16 + rect.height > window.innerHeight ? event.clientY - rect.height - 10 : event.clientY + 16;
    dom.tooltip.style.left = x + 'px';
    dom.tooltip.style.top = y + 'px';
  }
  function paintHover(key) {
    if (state.hovered === key) return;
    state.hovered = key;
    for (const bar of dom.lanes.children) {
      if (bar.dataset.key === key) bar.dataset.hover = 'true';
      else delete bar.dataset.hover;
    }
  }

  dom.track.addEventListener('pointerdown', (event) => {
    if (!projection) return;
    dom.track.setPointerCapture?.(event.pointerId);
    if (event.button === 2) {
      drag = { mode: 'pan', anchorPx: event.clientX, domain: currentDomain(), k: projection.k, moved: false };
      dom.track.dataset.panning = 'true';
      return;
    }
    if (event.button !== 0) return;
    drag = { mode: 'select', anchor: timeAt(event), anchorPx: event.clientX, moved: false, key: keyAt(event) };
  });

  dom.track.addEventListener('pointermove', (event) => {
    const fraction = fractionOf(event);
    dom.hoverLine.hidden = false;
    dom.hoverLine.style.setProperty('--x', (fraction * 100).toFixed(4));
    paintHover(keyAt(event));
    showTooltip(event, state.hovered);
    if (!drag) return;
    if (Math.abs(event.clientX - drag.anchorPx) > 3) drag.moved = true;
    if (drag.mode === 'pan') {
      if (!drag.moved) return;
      const deltaMs = (event.clientX - drag.anchorPx) / Math.max(1e-9, drag.k);
      const span = drag.domain.t1 - drag.domain.t0;
      let start = drag.domain.t0 - deltaMs;
      start = Math.max(DATA_START, Math.min(DATA_END - span, start));
      const next = { t0: start, t1: start + span };
      if (next.t1 - next.t0 >= (DATA_END - DATA_START) * 0.995) state.view = null;
      else state.view = next;
      renderTimeline();
      return;
    }
    const current = timeAt(event);
    state.range = { t0: Math.min(drag.anchor, current), t1: Math.max(drag.anchor, current) };
    refreshTableStates();
    renderTimeline();
  });

  dom.track.addEventListener('pointerup', (event) => {
    if (!drag) return;
    const { mode, moved, key } = drag;
    drag = null;
    delete dom.track.dataset.panning;
    if (mode === 'pan') { renderTimeline(); return; }
    if (!moved) {
      if (key) { selectRecord(key); return; }
      state.range = null;
      refreshTableStates();
      renderTimeline();
      const t = timeAt(event);
      const nearest = records.reduce((best, rec) => {
        const end = rec.t1 ?? rec.t0;
        const distance = t < rec.t0 ? rec.t0 - t : t > end ? t - end : 0;
        return best === null || distance < best.distance ? { rec, distance } : best;
      }, null);
      if (nearest) {
        const row = dom.tbody.querySelector('tr.row-record[data-key="' + CSS.escape(nearest.rec.key) + '"]');
        if (row) row.scrollIntoView({ block: 'center' });
      }
      return;
    }
    refreshTableStates();
    renderTimeline();
  });

  dom.track.addEventListener('pointercancel', () => { drag = null; delete dom.track.dataset.panning; });
  dom.track.addEventListener('pointerleave', () => {
    if (drag) return;
    dom.hoverLine.hidden = true;
    dom.tooltip.hidden = true;
    paintHover(null);
  });
  dom.track.addEventListener('contextmenu', (event) => event.preventDefault());
  dom.track.addEventListener('dblclick', (event) => { event.preventDefault(); fitAll(); });

  dom.track.addEventListener('wheel', (event) => {
    if (!projection) return;
    event.preventDefault();
    const rect = trackRect();
    const fraction = clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
    const anchor = projection.tOf(fraction * rect.width);
    const domain = currentDomain();
    const span = domain.t1 - domain.t0;
    const total = DATA_END - DATA_START;
    const factor = Math.exp(event.deltaY * 0.0015);
    const minSpan = Math.max(300, total / 5000);
    const nextSpan = clamp(span * factor, minSpan, total);
    let start = anchor - fraction * nextSpan;
    start = Math.max(DATA_START, Math.min(DATA_END - nextSpan, start));
    state.view = nextSpan >= total * 0.995 ? null : { t0: start, t1: start + nextSpan };
    renderTimeline();
  }, { passive: false });

  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      if (state.range !== null) { state.range = null; refreshAll(); }
      else if (state.selected !== null) { state.selected = null; state.tab = null; refreshAll(); }
      else if (state.view !== null) fitAll();
    }
    if (event.key === '/' && document.activeElement !== dom.search) {
      event.preventDefault();
      dom.search.focus();
    }
  });

  dom.tbody.addEventListener('click', (event) => {
    const tr = event.target instanceof Element ? event.target.closest('tr') : null;
    if (!tr) return;
    if (tr.classList.contains('row-turn')) {
      const turnIndex = Number(tr.dataset.turn);
      if (state.collapsed.has(turnIndex)) state.collapsed.delete(turnIndex);
      else state.collapsed.add(turnIndex);
      buildTable();
      refreshAll();
      return;
    }
    if (tr.classList.contains('row-step') && tr.dataset.key) selectRecord(tr.dataset.key);
  });

  dom.search.addEventListener('input', () => {
    state.query = dom.search.value.trim();
    buildTable();
    refreshAll();
  });

  for (const btn of document.querySelectorAll('.seg-btn')) {
    btn.addEventListener('click', () => {
      state.gapThreshold = Number(btn.dataset.gap);
      state.view = null;
      for (const other of document.querySelectorAll('.seg-btn')) other.setAttribute('aria-pressed', other === btn ? 'true' : 'false');
      renderTimeline();
    });
  }
  document.getElementById('expandAll').addEventListener('click', () => {
    state.collapsed.clear();
    buildTable();
    refreshAll();
  });
  document.getElementById('collapseAll').addEventListener('click', () => {
    for (const turn of turns) state.collapsed.add(turn.index);
    buildTable();
    refreshAll();
  });
  document.getElementById('fitAll').addEventListener('click', fitAll);

  const themeBtn = document.getElementById('themeToggle');
  function applyTheme() {
    document.documentElement.dataset.theme = state.theme;
    themeBtn.textContent = state.theme === 'dark' ? '☾' : '☀';
    themeBtn.title = state.theme === 'dark' ? '切换到浅色' : '切换到深色';
  }
  themeBtn.addEventListener('click', () => {
    state.theme = state.theme === 'dark' ? 'light' : 'dark';
    try { window.localStorage.setItem('atrace-theme', state.theme); } catch { /* 忽略不可写 */ }
    applyTheme();
  });

  /** 深链：#turn=3 聚焦某轮；#key=t3s4 选中某条记录；#theme=light 明暗。 */
  function applyHash() {
    const hash = window.location.hash.replace(/^#/, '');
    if (!hash) return;
    const params = new URLSearchParams(hash);
    const themeParam = params.get('theme');
    if (themeParam === 'light' || themeParam === 'dark') {
      state.theme = themeParam;
      applyTheme();
    }
    const keyParam = params.get('key');
    if (keyParam) { selectRecord(keyParam); return; }
    const turnParam = params.get('turn');
    if (!turnParam) return;
    const turn = turns.find((t) => String(t.index) === turnParam);
    if (!turn) return;
    state.collapsed.delete(turn.index);
    buildTable();
    focusRange(turn.startMs, Math.max(turn.endMs, turn.startMs + 1));
    refreshAll();
    const row = dom.tbody.querySelector('tr.row-turn[data-turn="' + turnParam + '"]');
    if (row) row.scrollIntoView({ block: 'center' });
  }

  // ---------------------------------------------------------------- 启动
  applyTheme();
  for (const btn of document.querySelectorAll('.seg-btn')) {
    btn.setAttribute('aria-pressed', Number(btn.dataset.gap) === state.gapThreshold ? 'true' : 'false');
  }
  buildTable();
  renderInspector();
  renderTimeline();
  window.addEventListener('resize', () => renderTimeline());
  window.addEventListener('hashchange', applyHash);
  applyHash();

  // 无头浏览器/调试用
  window.__trace = { records, stats, state, projection: () => projection, render: renderTimeline };

  // #debug=1：把投影节点与刻度粘到页面上，便于核对时间映射（不影响正常使用）
  if (new URLSearchParams(window.location.hash.replace(/^#/, '')).get('debug') === '1') {
    const p = projection;
    const box = document.createElement('pre');
    box.id = 'debug-output';
    box.style.cssText = 'position:fixed;z-index:99;inset:auto 0 0 0;max-height:40vh;overflow:auto;background:#000;color:#0f0;font:10px/12px monospace;padding:6px';
    box.textContent = JSON.stringify({
      domain: p.domain,
      k: p.k,
      width: p.width,
      nodes: p.nodes.map((n) => ({ kind: n.kind, t0: n.t0, t1: n.t1, p0: Math.round(n.p0), p1: Math.round(n.p1), collapsed: !!n.collapsed })),
      ticks: Array.prototype.map.call(document.querySelectorAll('.axis-tick'), (t) => t.style.left + ' ' + t.textContent),
    }, null, 1);
    document.body.appendChild(box);
  }
})();
