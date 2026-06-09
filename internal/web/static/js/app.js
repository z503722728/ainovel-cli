// ═══ AINovel Web Terminal ═══
// 薄渲染层：SSE 事件流 + 斜杠命令转发。所有业务逻辑由 host.Host 承载。

const $ = (s, p = document) => p.querySelector(s);
const $$ = (s, p = document) => [...p.querySelectorAll(s)];

// ── API ──
async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (!res.ok) throw new Error((await res.text().catch(() => '')) || `${res.status}`);
  return res.json();
}

// ── Tab Switching ──
$$('.tab').forEach(btn => {
  btn.addEventListener('click', () => {
    $$('.tab').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    $$('.panel').forEach(p => p.classList.remove('active'));
    const id = 'panel-' + btn.dataset.tab;
    const panel = $(id);
    if (panel) panel.classList.add('active');
    if (btn.dataset.tab === 'files') loadFiles();
    if (btn.dataset.tab === 'settings') loadSettings();
  });
});

// ═══════════════════════════════════════════
//  WRITING TERMINAL
// ═══════════════════════════════════════════

const streamContent = $('#stream-content');
const eventLog = $('#event-log');
const eventLogWrap = $('#event-log-wrap');
const eventCount = $('#event-count');
const topbarStatus = $('#topbar-status');
const topbarTitle = $('#topbar-title');
const topbarSubtitle = $('#topbar-subtitle');

let es = null;
let streamBuf = '';
let eventN = 0;
let cmdHistory = [];
let cmdIdx = -1;

// ── SSE ──
function connectSSE() {
  if (es) es.close();
  es = new EventSource('/api/stream');

  es.addEventListener('event', e => {
    const d = JSON.parse(e.data);
    addEvent(d.category || 'INFO', d.summary || '', d.level);
  });

  es.addEventListener('delta', e => {
    let t;
    try { t = JSON.parse(e.data); } catch { t = e.data; }
    if (t.includes('\x02')) {
      t.split('\x02').forEach((p, i) => {
        if (i % 2 === 1) appendThink(p);
        else if (p) appendStream(p);
      });
    } else {
      appendStream(t);
    }
  });

  es.addEventListener('clear', endRound);
  es.addEventListener('done', () => { endRound(); setPhase('idle'); });
}

// ── Stream ──
function appendStream(text) {
  clearEmptyHint();
  const last = streamContent.lastElementChild;
  if (last && last.classList.contains('stream-p') && !last.dataset.ended) {
    last.textContent += text;
  } else {
    const p = document.createElement('div');
    p.className = 'stream-p';
    p.textContent = text;
    streamContent.appendChild(p);
  }
  streamBuf += text;
  scrollStream();
}

function appendThink(text) {
  clearEmptyHint();
  const d = document.createElement('details');
  d.className = 'think-block';
  d.innerHTML = `<summary>💭 思考 (${text.length}字)</summary><div class="think-content">${esc(text)}</div>`;
  streamContent.appendChild(d);
  scrollStream();
}

function endRound() {
  if (streamBuf) {
    const last = streamContent.lastElementChild;
    if (last) last.dataset.ended = '1';
    const sep = document.createElement('div');
    sep.className = 'round-sep';
    streamContent.appendChild(sep);
    streamBuf = '';
  }
}

function clearEmptyHint() {
  if (streamContent.classList.contains('empty-hint')) {
    streamContent.classList.remove('empty-hint');
    streamContent.innerHTML = '';
  }
}

function scrollStream() {
  requestAnimationFrame(() => {
    const c = $('#stream-output');
    if (c) c.scrollTop = c.scrollHeight;
  });
}

// ── Events ──
function addEvent(cat, summary, level) {
  eventN++;
  eventCount.textContent = `(${eventN})`;
  const entry = document.createElement('details');
  entry.className = 'event-entry' + (level === 'error' ? ' error' : '');
  const t = new Date().toTimeString().slice(0, 5);
  entry.innerHTML = `<summary><span class="ev-cat">[${cat}]</span><span class="ev-time">${t}</span>${esc(summary)}</summary>`;
  eventLog.appendChild(entry);
  eventLog.scrollTop = eventLog.scrollHeight;
}

// ── Phase ──
let curPhase = 'idle';
function setPhase(phase) {
  curPhase = phase;
  const m = {
    idle:    ['待命',     'idle',   '● 待命'],
    writing: ['写作中…',  'writing', '◉ 写作中'],
    review:  ['审查中',   'review',  '◎ 审查中'],
    paused:  ['已暂停',   'idle',    '◌ 暂停'],
  };
  const [text, cls, badge] = m[phase] || m.idle;
  topbarStatus.textContent = badge;
  topbarStatus.className = 'badge ' + cls;
  // highlight review quick-btn
  $$('.qbtn').forEach(b => b.classList.toggle('review-mode', phase === 'review'));
}

async function updateTopbar() {
  try {
    const s = await api('GET', '/api/status');
    topbarTitle.textContent = s.novelName || 'AINovel';
    topbarSubtitle.textContent = s.bookTitle || '';
    if (s.phase === 'Review') setPhase('review');
    else if (s.running) setPhase('writing');
    else setPhase('idle');
  } catch {}
}

// ═══════════════════════════════════════════
//  QUICK ACTIONS
// ═══════════════════════════════════════════

$$('.qbtn').forEach(btn => {
  btn.addEventListener('click', () => {
    const raw = btn.dataset.cmd;
    if (!raw) return;
    if (raw === '/help') { showHelp(); return; }
    if (raw === '/resume' || raw === '/approve' || raw === '/skip' || raw === '/pause') {
      execCmd(raw);
    }
  });
});

// ═══════════════════════════════════════════
//  COMMAND SYSTEM
// ═══════════════════════════════════════════

const cmdForm = $('#cmd-form');
const cmdInput = $('#cmd-input');
const cmdHints = $('#cmd-hints');

const COMMANDS = [
  { cmd: '/start',   args: '<创意>', desc: '开始创作' },
  { cmd: '/resume',  args: '',       desc: '恢复进度' },
  { cmd: '/pause',   args: '',       desc: '暂停' },
  { cmd: '/feedback',args: '<意见>', desc: '审查反馈' },
  { cmd: '/approve', args: '',       desc: '审查通过' },
  { cmd: '/skip',    args: '',       desc: '跳过审查' },
  { cmd: '/steer',   args: '<指令>', desc: '中途干预' },
  { cmd: '/status',  args: '',       desc: '刷新状态' },
  { cmd: '/help',    args: '',       desc: '帮助' },
];

cmdInput.addEventListener('input', () => {
  const v = cmdInput.value.trim();
  if (!v.startsWith('/')) { cmdHints.innerHTML = ''; return; }
  const root = v.split(/\s/)[0];
  const matches = COMMANDS.filter(c => c.cmd.startsWith(root));
  if (matches.length === 0 || (matches.length === 1 && matches[0].cmd === root)) {
    cmdHints.innerHTML = '';
    return;
  }
  cmdHints.innerHTML = matches.map(c =>
    `<div class="hint-item" data-cmd="${c.cmd}"><b>${c.cmd}</b>&nbsp;${c.args} — ${c.desc}</div>`
  ).join('');
  $$('.hint-item', cmdHints).forEach(el => {
    el.addEventListener('click', () => {
      cmdInput.value = el.dataset.cmd + ' ';
      cmdHints.innerHTML = '';
      cmdInput.focus();
    });
  });
});

cmdInput.addEventListener('keydown', e => {
  if (e.key === 'ArrowUp') {
    e.preventDefault();
    if (cmdHistory.length > 0 && cmdIdx < cmdHistory.length - 1) {
      cmdIdx++;
      cmdInput.value = cmdHistory[cmdIdx];
    }
  } else if (e.key === 'ArrowDown') {
    e.preventDefault();
    if (cmdIdx > 0) { cmdIdx--; cmdInput.value = cmdHistory[cmdIdx]; }
    else { cmdIdx = -1; cmdInput.value = ''; }
  }
});

cmdForm.addEventListener('submit', e => {
  e.preventDefault();
  const raw = cmdInput.value.trim();
  if (!raw) return;
  cmdHistory.unshift(raw);
  if (cmdHistory.length > 50) cmdHistory.pop();
  cmdIdx = -1;
  cmdInput.value = '';
  cmdHints.innerHTML = '';
  addEvent('CMD', raw);
  execCmd(raw);
});

async function execCmd(raw) {
  const [cmd, ...rest] = raw.split(/\s+/);
  const args = rest.join(' ');
  try {
    switch (cmd) {
      case '/start':
        if (!args) { addEvent('ERR', '用法: /start <创意描述>', 'error'); return; }
        setPhase('writing');
        await api('POST', '/api/start', { prompt: args });
        break;
      case '/resume':
        setPhase('writing');
        await api('POST', '/api/resume');
        break;
      case '/pause':
        await api('POST', '/api/pause');
        setPhase('paused');
        break;
      case '/feedback':
        if (!args) { addEvent('ERR', '用法: /feedback <修改意见>', 'error'); return; }
        setPhase('writing');
        await api('POST', '/api/review/feedback', { feedback: args, project: '' });
        break;
      case '/approve':
        await api('POST', '/api/review/approve');
        setPhase('writing');
        break;
      case '/skip':
        await api('POST', '/api/review/skip');
        setPhase('writing');
        break;
      case '/steer':
        if (!args) { addEvent('ERR', '用法: /steer <指令>', 'error'); return; }
        await api('POST', '/api/steer', { text: args });
        break;
      case '/status':
        await updateTopbar();
        break;
      case '/help':
        showHelp();
        break;
      default:
        await api('POST', '/api/steer', { text: raw });
        break;
    }
  } catch (err) {
    addEvent('ERR', err.message, 'error');
  }
}

function showHelp() {
  const html = COMMANDS.map(c => `<b>${c.cmd}</b> ${c.args} — ${c.desc}`).join('\n');
  addEvent('HELP', '命令列表');
  const div = document.createElement('div');
  div.className = 'help-block';
  div.innerHTML = html;
  eventLog.appendChild(div);
  // auto-open event log to show help
  eventLogWrap.open = true;
  eventLog.scrollTop = eventLog.scrollHeight;
}

function esc(s) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML; }

// ═══════════════════════════════════════════
//  FILES TAB
// ═══════════════════════════════════════════

async function loadFiles() {
  try {
    const d = await api('GET', '/api/files');
    renderTree(d.tree || d.children || []);
  } catch (e) {
    $('#file-tree').innerHTML = `<div class="empty-hint">${esc(e.message)}</div>`;
  }
}

function renderTree(nodes, depth = 0) {
  const ul = document.createElement('ul');
  ul.className = 'tree-list';
  for (const n of nodes) {
    const li = document.createElement('li');
    li.style.paddingLeft = (depth * 14) + 'px';
    if (n.type === 'dir') {
      li.innerHTML = `<span class="tree-dir">📁 ${n.name}</span>`;
      if (n.children) li.appendChild(renderTree(n.children, depth + 1));
    } else {
      const sp = document.createElement('span');
      sp.className = 'tree-file';
      sp.textContent = '📄 ' + n.name;
      sp.addEventListener('click', () => viewFile(n));
      li.appendChild(sp);
    }
    ul.appendChild(li);
  }
  const container = $('#file-tree');
  container.innerHTML = '';
  container.appendChild(ul);
}

async function viewFile(node) {
  const path = node.path || node.name;
  try {
    const d = await api('GET', '/api/files/' + encodeURIComponent(path));
    $('#file-viewer').innerHTML = `<pre class="file-content">${esc(d.content || '')}</pre>`;
  } catch (e) {
    $('#file-viewer').innerHTML = `<div class="empty-hint">${esc(e.message)}</div>`;
  }
}

// ═══════════════════════════════════════════
//  SETTINGS TAB
// ═══════════════════════════════════════════

async function loadSettings() {
  try {
    const d = await api('GET', '/api/styles');
    const styles = d.styles || [];
    $('#style-select').innerHTML = styles.map(s =>
      `<option value="${s.name}" ${s.active ? 'selected' : ''}>${s.label || s.name}</option>`
    ).join('');
  } catch {}
  try {
    const s = await api('GET', '/api/status');
    $('#review-toggle').checked = s.reviewEnabled || false;
    $('#model-info').textContent = [
      `模型: ${s.provider || '-'}/${s.modelName || '-'}`,
      `章节: ${s.chapter || 0} / ${s.totalChapters || '?'}`,
      `字数: ${s.wordCount || 0}`,
      `项目: ${s.novelName || '-'}`,
      `花费: $${(s.totalCostUSD || 0).toFixed(3)}`,
    ].join('\n');
  } catch {}
}

$('#style-select')?.addEventListener('change', async () => {
  try { await api('POST', '/api/style/current', { style: $('#style-select').value }); } catch {}
});

$('#review-toggle')?.addEventListener('change', async () => {
  try { await api('POST', '/api/review/toggle', { enabled: $('#review-toggle').checked }); }
  catch { $('#review-toggle').checked = !$('#review-toggle').checked; }
});

// ═══════════════════════════════════════════
//  INIT
// ═══════════════════════════════════════════

connectSSE();
updateTopbar();
setInterval(updateTopbar, 15000);
