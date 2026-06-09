// ═══ AINovel Web Terminal ═══
const $ = (s, p = document) => p.querySelector(s);
const $$ = (s, p = document) => [...p.querySelectorAll(s)];
const esc = s => { const d = document.createElement('div'); d.textContent = s; return d.innerHTML; };

// ── Persistence ──
const PERSIST_KEY = () => 'ainovel_terminal_' + (currentProject || '_default');
let currentProject = '';
let streamHTML = '';
let eventEntries = [];

function saveState() {
  try {
    localStorage.setItem(PERSIST_KEY(), JSON.stringify({
      stream: streamContent.innerHTML,
      events: eventEntries,
      streamBuf,
    }));
  } catch {}
}

function loadState() {
  try {
    const raw = localStorage.getItem(PERSIST_KEY());
    if (!raw) return false;
    const data = JSON.parse(raw);
    if (data.stream) {
      streamContent.innerHTML = data.stream;
      streamContent.classList.remove('empty-hint');
    }
    if (data.events) {
      data.events.forEach(e => addEvent(e.cat, e.summary, e.level, true));
    }
    if (data.streamBuf) streamBuf = data.streamBuf;
    return !!data.stream;
  } catch { return false; }
}

function clearState() {
  try { localStorage.removeItem(PERSIST_KEY()); } catch {}
}

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
    const panel = $('#panel-' + btn.dataset.tab);
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
let lastStatus = null;

// ── SSE ──
function connectSSE() {
  if (es) es.close();
  es = new EventSource('/api/stream');

  es.addEventListener('status', e => {
    try {
      lastStatus = JSON.parse(e.data);
      applyStatus(lastStatus);
    } catch {}
  });

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
  es.onerror = () => {};
}

function applyStatus(s) {
  const name = s.novelName || s.NovelName;
  if (name) {
    currentProject = name;
    topbarTitle.textContent = name;
    topbarSubtitle.textContent = s.bookTitle || s.BookTitle || '';
  }
  const ch = s.chapter || s.CurrentChapter;
  if (ch) {
    const prog = $('#status-progress');
    if (prog) prog.textContent = `第${ch}章·${s.wordCount||s.TotalWordCount||0}字`;
  }
  const running = s.running || s.IsRunning;
  const phase = s.phase || s.Phase;
  if (running) setPhase('writing');
  else if (phase === 'Review') setPhase('review');
  else setPhase('idle');
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
  debouncedSave();
}

function appendThink(text) {
  clearEmptyHint();
  const d = document.createElement('details');
  d.className = 'think-block';
  d.innerHTML = `<summary>💭 思考(${text.length}字)</summary><div class="think-content">${esc(text)}</div>`;
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
    saveState();
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

let _saveTimer = null;
function debouncedSave() {
  clearTimeout(_saveTimer);
  _saveTimer = setTimeout(saveState, 2000);
}

// ── Events ──
function addEvent(cat, summary, level, skipSave) {
  eventN++;
  eventCount.textContent = `(${eventN})`;
  const entry = document.createElement('details');
  entry.className = 'event-entry' + (level === 'error' ? ' error' : '');
  const t = new Date().toTimeString().slice(0, 5);
  entry.innerHTML = `<summary><span class="ev-cat">[${cat}]</span><span class="ev-time">${t}</span>${esc(summary)}</summary>`;
  eventLog.appendChild(entry);
  eventLog.scrollTop = eventLog.scrollHeight;
  if (!skipSave) {
    eventEntries.push({ cat, summary, level });
    debouncedSave();
  }
}

// ── Phase ──
function setPhase(phase) {
  const m = {
    idle:    ['待命',   'idle',   '● 待命'],
    writing: ['写作中…','writing','◉ 写作中'],
    review:  ['审查中', 'review', '◎ 审查中 · /feedback 或 /approve'],
    paused:  ['已暂停', 'idle',   '◌ 暂停'],
  };
  const [text, cls, badge] = m[phase] || m.idle;
  topbarStatus.textContent = badge;
  topbarStatus.className = 'badge ' + cls;
  $$('.qbtn').forEach(b => b.classList.toggle('review-mode', phase === 'review'));
}

async function updateTopbar() {
  try {
    const s = await api('GET', '/api/status');
    applyStatus(s);
    return s;
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
    if (raw === '/projects') { execCmd('/projects'); return; }
    execCmd(raw);
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
  { cmd: '/projects',args: '',       desc: '查看项目列表' },
  { cmd: '/new',     args: '<名称>', desc: '新建项目' },
  { cmd: '/switch',  args: '<名称>', desc: '切换项目' },
  { cmd: '/delete',  args: '<名称>', desc: '删除项目' },
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
        clearState();
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
        await api('POST', '/api/review/feedback', { feedback: args, project: currentProject });
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
      case '/projects':
        await showProjects();
        break;
      case '/new':
        if (!args) { addEvent('ERR', '用法: /new <项目名>', 'error'); return; }
        await api('POST', '/api/projects', { name: args });
        addEvent('OK', `项目「${args}」已创建`, 'info');
        await updateTopbar();
        break;
      case '/switch':
        if (!args) { addEvent('ERR', '用法: /switch <项目名>', 'error'); return; }
        await api('POST', '/api/projects/' + encodeURIComponent(args));
        clearState();
        addEvent('OK', `已切换到「${args}」`, 'info');
        streamContent.innerHTML = '<div class="empty-hint"><div class="welcome-icon">✍</div><div>已切换项目<br>/resume 恢复进度</div></div>';
        eventLog.innerHTML = '';
        eventEntries = [];
        eventN = 0;
        eventCount.textContent = '';
        await updateTopbar();
        break;
      case '/delete':
        if (!args) { addEvent('ERR', '用法: /delete <项目名>', 'error'); return; }
        await api('DELETE', '/api/projects/' + encodeURIComponent(args));
        addEvent('OK', `项目「${args}」已删除`, 'info');
        await updateTopbar();
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

async function showProjects() {
  try {
    const d = await api('GET', '/api/projects');
    const projects = d.projects || [];
    if (projects.length === 0) {
      addEvent('PROJECTS', '暂无项目，用 /new <名称> 创建', 'info');
      return;
    }
    const lines = projects.map(p => {
      const mark = p.active ? ' ◀' : '';
      return `  ${p.name}${mark}`;
    });
    addEvent('PROJECTS', '项目列表：\n' + lines.join('\n'));
    eventLogWrap.open = true;
  } catch (err) {
    addEvent('ERR', '获取项目列表失败: ' + err.message, 'error');
  }
}

function showHelp() {
  const html = COMMANDS.map(c => `<b>${c.cmd}</b> ${c.args} — ${c.desc}`).join('\n');
  addEvent('HELP', '命令列表');
  const div = document.createElement('div');
  div.className = 'help-block';
  div.innerHTML = html;
  eventLog.appendChild(div);
  eventLogWrap.open = true;
  eventLog.scrollTop = eventLog.scrollHeight;
}

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
  const c = $('#file-tree');
  c.innerHTML = '';
  c.appendChild(ul);
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
      `模型: ${s.provider||s.Provider||'-'}/${s.modelName||s.ModelName||'-'}`,
      `章节: ${s.chapter||s.CurrentChapter||0} / ${s.totalChapters||s.TotalChapters||'?'}`,
      `字数: ${s.wordCount||s.TotalWordCount||0}`,
      `项目: ${s.novelName||s.NovelName||'-'}`,
      `花费: $${(s.totalCostUSD||s.TotalCostUSD||0).toFixed(3)}`,
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

// Restore persisted state
connectSSE();
updateTopbar().then(() => {
  // After status loads, try to restore persisted stream
  loadState();
});
setInterval(updateTopbar, 15000);
