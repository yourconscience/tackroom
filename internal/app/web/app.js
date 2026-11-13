const VIEWS = ['overview', 'skills', 'roles', 'wiring', 'unmanaged', 'crons', 'config'];
const baseURL = new URL(window.location.pathname.endsWith('/') ? window.location.pathname : `${window.location.pathname}/`, window.location.origin);

// What each matrix dot means.
const CELLS = {
  ok: 'in place',
  drift: 'drifted',
  missing: 'missing',
  conflict: 'conflict',
  stale: 'stale',
  off: 'not targeted',
  disabled: 'turned off',
  unsupported: 'not supported',
  absent: 'not installed',
  error: 'config unreadable',
  unknown: 'unknown',
};
const ATTENTION = new Set(['drift', 'missing', 'conflict', 'error', 'unknown']);

const store = {
  view: 'overview',
  inv: null,
  invError: '',
  shared: null,
  local: null,
  busy: false,
  skillQuery: '',
  skillFilter: 'all',
  openRows: new Set(),
  configLayer: 'shared',
  configState: null,
  configLoading: false,
  drafts: {},
  configDiff: '',
  configNote: '',
  preview: null,
  crons: null,
  cronsError: '',
  cronsLoading: false,
  cronFilter: 'all',
  cronPatches: [],
};

// ---------- helpers ----------
function h(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'class') node.className = value;
    else if (key === 'text') node.textContent = value;
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else if (key === 'dataset') Object.assign(node.dataset, value);
    // CSSOM, not a style attribute: the view's CSP blocks inline style attributes.
    else if (key === 'style') Object.assign(node.style, value);
    else if (value === true) node.setAttribute(key, '');
    else node.setAttribute(key, value);
  }
  for (const child of children.flat()) {
    if (child === undefined || child === null || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}
function mount(root, ...children) {
  root.replaceChildren(...children.flat().filter((child) => child !== null && child !== undefined && child !== false));
}
function tilde(path) {
  const home = store.inv?.home;
  if (!path || !home) return path || '';
  return path === home ? '~' : path.split(`${home}/`).join('~/');
}
const SVG_NS = 'http://www.w3.org/2000/svg';
function svgNode(tag, attrs = {}) {
  const node = document.createElementNS(SVG_NS, tag);
  for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, String(value));
  return node;
}
function icon(id, cls) {
  const svg = svgNode('svg', {'aria-hidden': 'true', ...(cls ? {class: cls} : {})});
  svg.append(svgNode('use', {href: `#${id}`}));
  return svg;
}
// A thin ring split into parts [{value, kind, label}]; CSS colours each kind. The centre holds a figure and a word.
function ring(parts, figure, word, size = 88) {
  const stroke = size >= 80 ? 7 : 6;
  const c = size / 2;
  const r = c - stroke / 2;
  const length = 2 * Math.PI * r;
  const total = parts.reduce((sum, part) => sum + part.value, 0);
  const seam = parts.filter((part) => part.value > 0).length > 1 ? 2 : 0;
  const svg = svgNode('svg', {viewBox: `0 0 ${size} ${size}`, class: 'ring-svg', 'aria-hidden': 'true'});
  svg.append(svgNode('circle', {cx: c, cy: c, r, class: 'ring-track', 'stroke-width': stroke}));
  let offset = 0;
  for (const part of parts) {
    if (!part.value || !total) continue;
    const span = (part.value / total) * length;
    svg.append(svgNode('circle', {cx: c, cy: c, r, class: `ring-part ${part.kind}`, 'stroke-width': stroke, 'stroke-dasharray': `${Math.max(span - seam, 1)} ${length}`, 'stroke-dashoffset': -offset, transform: `rotate(-90 ${c} ${c})`}));
    offset += span;
  }
  const label = total ? parts.filter((part) => part.value).map((part) => `${part.value} ${part.label || part.kind}`).join(', ') : word;
  return h('div', {class: total ? 'ring' : 'ring empty', role: 'img', 'aria-label': label, style: {width: `${size}px`, height: `${size}px`}}, svg,
    h('div', {class: 'ring-center'}, h('b', {text: String(figure)}), word ? h('span', {text: word}) : null));
}
// A 100% bar split into parts with 2px seams.
function bar(parts) {
  return h('div', {class: 'bar', 'aria-hidden': 'true'}, parts.filter((part) => part.value > 0).map((part) => h('span', {class: part.kind, style: {flexGrow: String(part.value)}})));
}
// Agent marks from the tackroom site; an agent without one gets its initial.
const AGENT_MARKS = {
  claude: 'a-claude-code', 'claude-code': 'a-claude-code', codex: 'a-codex', droid: 'a-droid', factory: 'a-droid',
  pi: 'a-pi', omp: 'a-omp', qwen: 'a-qwen-code', 'qwen-code': 'a-qwen-code', opencode: 'a-opencode', amp: 'a-amp',
  cursor: 'a-cursor', copilot: 'a-copilot', grok: 'a-grok', dsh: 'a-deepseek',
};
function agentMark(name, cls = 'ag') {
  const id = AGENT_MARKS[name];
  return id ? icon(id, cls) : h('span', {class: `${cls} ag-letter`, 'aria-hidden': 'true', text: (name || '?')[0].toUpperCase()});
}
// The view's own controls sit in the top bar, left of Sync.
function actions(...nodes) { mount(document.getElementById('view-actions'), ...nodes); }
function chip(kind, iconId, text, title) { return h('span', {class: `chip ${kind}`, title}, icon(iconId), text); }
function pick(object, ...keys) {
  for (const key of keys) if (object && object[key] !== undefined && object[key] !== null) return object[key];
  return undefined;
}
function csrf() {
  return document.cookie.split('; ').find((item) => item.startsWith('tackroom_csrf='))?.split('=')[1] || '';
}
async function api(path, options = {}) {
  const mutation = options.method && options.method !== 'GET';
  const headers = {Accept: 'application/json', ...(options.body ? {'Content-Type': 'application/json'} : {}), ...(mutation ? {'X-Tackroom-CSRF': csrf()} : {})};
  const response = await fetch(new URL(path.replace(/^\//, ''), baseURL), {...options, headers});
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(body.error?.message || `request failed (${response.status})`);
    error.code = body.error?.code || '';
    throw error;
  }
  return body;
}
let toastTimer;
function toast(message, bad = false) {
  const node = document.getElementById('toast');
  node.textContent = message;
  node.className = bad ? 'toast bad' : 'toast';
  node.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { node.hidden = true; }, bad ? 6000 : 3200);
}
function plural(count, word, many = `${word}s`) { return `${count} ${count === 1 ? word : many}`; }
function tokens(count) { return count >= 1000 ? `~${(count / 1000).toFixed(1)}k` : `~${count}`; }
function columns() { return (store.inv?.agents || []).filter((agent) => agent.inspected).map((agent) => agent.name); }
function agentOrder(names) {
  const order = (store.inv?.agents || []).map((agent) => agent.name);
  return [...names].sort((a, b) => {
    const ia = order.indexOf(a); const ib = order.indexOf(b);
    return (ia < 0 ? 999 : ia) - (ib < 0 ? 999 : ib) || a.localeCompare(b);
  });
}
function switchControl(checked, label, onchange, disabled = false) {
  const input = h('input', {type: 'checkbox', 'aria-label': label, disabled: disabled || store.busy || undefined, onchange});
  input.checked = !!checked;
  return h('label', {class: 'switch'}, input, h('span', {'aria-hidden': 'true'}));
}
function copyButton(text) {
  return h('button', {class: 'icon-button quiet', type: 'button', 'aria-label': `Copy ${text}`, title: `Copy: ${text}`, onclick: async () => {
    try { await navigator.clipboard.writeText(text); toast('Copied to the clipboard.'); }
    catch { toast(text); }
  }}, icon('i-copy'));
}

// ---------- config edits ----------
const SECTION_KEYS = {agents: ['Agents', 'agents'], mcp_servers: ['MCPServers', 'mcp_servers'], hooks: ['Hooks', 'hooks']};
function definedLocally(section, name) {
  const entries = pick(store.local?.typed_config, ...SECTION_KEYS[section]) || [];
  return entries.some((entry) => pick(entry, 'Name', 'name') === name);
}
async function patch(section, name, field, value, message) {
  const layer = definedLocally(section, name) ? 'local' : 'shared';
  const state = layer === 'local' ? store.local : store.shared;
  store.busy = true;
  try {
    await api('/api/config', {method: 'PATCH', body: JSON.stringify({layer, expected_revision: state.revision, operations: [{op: 'set', path: `/${section}/${name}/${field}`, value}]})});
    toast(`${message} Saved to ${layer === 'local' ? 'tackroom.local.yaml' : 'tackroom.yaml'}; Sync to apply it.`);
  } catch (error) {
    toast(error.code === 'stale_revision' ? 'The config changed on disk. Reloaded the latest version; try again.' : error.message, true);
  } finally {
    store.busy = false;
    await refresh();
  }
}

// ---------- data ----------
async function refresh() {
  const [inv, shared, local] = await Promise.allSettled([
    api('/api/inventory'),
    api('/api/state?layer=shared'),
    api('/api/state?layer=local'),
  ]);
  store.inv = inv.status === 'fulfilled' ? inv.value : null;
  store.invError = inv.status === 'rejected' ? inv.reason.message : '';
  store.shared = shared.status === 'fulfilled' ? shared.value : null;
  store.local = local.status === 'fulfilled' ? local.value : null;
  // Drop the cached Config layer so it reloads with the new revision; an
  // unsaved draft keeps its base, so saving it reports the conflict instead.
  if (!store.drafts[store.configLayer]) store.configState = null;
  renderChrome();
  renderView();
}

// ---------- chrome ----------
function renderChrome() {
  const inv = store.inv;
  const counts = {skills: '', roles: '', wiring: '', unmanaged: '', crons: ''};
  const alerts = {};
  if (inv) {
    const attention = inv.skills.filter((skill) => Object.values(skill.states).some((state) => ATTENTION.has(state))).length;
    counts.skills = attention || inv.skills.length;
    alerts.skills = attention > 0;
    counts.roles = inv.roles.length || '';
    counts.wiring = inv.mcp.length + inv.hooks.length || '';
    counts.unmanaged = inv.unmanaged.length || '';
  }
  if (store.crons) {
    const attention = store.crons.jobs.filter((job) => job.health === 'fail').length;
    counts.crons = attention || store.crons.jobs.length || '';
    alerts.crons = attention > 0;
  }
  document.querySelectorAll('[data-count]').forEach((node) => {
    node.textContent = counts[node.dataset.count] ?? '';
    node.classList.toggle('alert', !!alerts[node.dataset.count]);
  });
  const pending = (inv?.agents || []).reduce((sum, agent) => sum + (agent.inspected ? agent.pending : 0), 0);
  const unsynced = (inv?.agents || []).filter((agent) => agent.inspected && agent.detected && !agent.error && !agent.synced).length;
  const button = document.getElementById('open-sync');
  button.classList.toggle('pending', pending > 0 || unsynced > 0);
  document.getElementById('sync-label').textContent = pending > 0 ? `Review sync · ${pending}` : unsynced > 0 ? 'Review sync' : 'Sync';
  const links = pick(store.shared?.effective_ui, 'Links', 'links') || [];
  mount(document.getElementById('links'), ...links.map((link) => h('a', {href: pick(link, 'URL', 'url'), target: '_top', text: pick(link, 'Name', 'name')})));
  document.getElementById('links-menu').hidden = links.length === 0;
  document.getElementById('scope').textContent = store.shared?.paths?.shared ? tilde(store.shared.paths.shared) : '';
}

function route() {
  const next = window.location.hash.replace('#', '');
  store.view = VIEWS.includes(next) ? next : 'overview';
  document.querySelectorAll('.view').forEach((node) => { node.hidden = node.dataset.view !== store.view; });
  document.querySelectorAll('.tabs a').forEach((node) => {
    if (node.dataset.view === store.view) node.setAttribute('aria-current', 'page');
    else node.removeAttribute('aria-current');
  });
  if (store.view === 'crons' && !store.crons && !store.cronsLoading) loadCrons();
  document.getElementById('crumb').textContent = document.querySelector(`.tabs a[data-view="${store.view}"] .label`)?.textContent || '';
  const help = document.getElementById('help');
  help.dataset.tip = HELP[store.view];
  help.setAttribute('aria-label', HELP[store.view]);
  document.body.classList.remove('nav-open');
  document.getElementById('open-nav').setAttribute('aria-expanded', 'false');
  renderView();
}

function renderView() {
  const root = document.getElementById(`view-${store.view}`);
  actions();
  if (store.view === 'config') { renderConfig(root); return; }
  if (store.view === 'crons') { renderCrons(root); return; }
  if (!store.inv) {
    mount(root, store.invError
      ? h('div', {class: 'error-box'}, h('strong', {text: 'Could not inspect your agents. '}), store.invError, ' Fix the config in the ', h('a', {href: '#config', text: 'Config'}), ' view.')
      : h('p', {class: 'loading', text: 'Inspecting your agents…'}));
    return;
  }
  ({overview: renderOverview, skills: renderSkills, roles: renderRoles, wiring: renderWiring, unmanaged: renderUnmanaged})[store.view](root);
}

// Each view's explanation lives behind the info mark next to its title.
const HELP = {
  overview: 'What tackroom manages on each coding agent. The ring is linked skills; turn an agent off to stop syncing it, nothing on disk is removed.',
  skills: 'Every canonical skill goes to every enabled agent. Each dot shows what is on disk now; Sync fixes drifted and missing links. Select a row for details.',
  roles: 'Agent roles from agents/*.md, rendered into each agent that supports subagents.',
  wiring: 'Which agents get each MCP server and hook. Select a dot to add or remove that agent; changes save to your config right away and Sync writes them to the agents.',
  unmanaged: "Things in your agents' folders that tackroom did not put there. tackroom never deletes them; copy the promote command to manage one.",
  crons: 'Scheduled jobs and services on every host in crons.hosts (tackroom.local.yaml). Add cron declares a job tackroom owns; Edit and Delete change only those. The CLI shows the same: tackroom cron collect --table.',
  config: 'Shared is tackroom.yaml in your config root; Local overrides it on this machine only; Effective is what sync uses.',
};

// ---------- overview ----------
function agentStatus(agent) {
  if (!agent.enabled) return ['', 'i-pause', 'off'];
  if (!agent.inspected) return ['', 'i-idle', 'not inspected'];
  if (agent.error) return ['bad', 'i-fail', 'config unreadable'];
  if (!agent.detected) return ['', 'i-idle', 'not installed'];
  if (agent.pending > 0) return ['warn', 'i-warn', `${agent.pending} pending`];
  if (agent.synced) return ['ok', 'i-ok', 'synced'];
  return ['warn', 'i-warn', 'needs sync'];
}

function renderOverview(root) {
  const inv = store.inv;
  const enabled = inv.agents.filter((agent) => agent.enabled);
  const pending = inv.agents.reduce((sum, agent) => sum + (agent.inspected ? agent.pending : 0), 0);
  const unsynced = inv.agents.filter((agent) => agent.inspected && agent.detected && !agent.error && !agent.synced).length;
  actions(
    !inv.agents.length ? chip('warn', 'i-warn', 'No agents')
      : pending > 0 ? chip('warn', 'i-warn', `${plural(pending, 'change')} waiting`)
      : unsynced > 0 ? chip('warn', 'i-warn', `${plural(unsynced, 'agent')} out of sync`) : chip('ok', 'i-ok', 'All synced'),
    inv.revision ? chip('', 'i-commit', inv.revision.slice(0, 7), `Config revision ${inv.revision}`) : null);

  const cronJobs = store.crons?.jobs || [];
  const cronTrouble = cronJobs.filter((job) => job.health === 'fail').length;
  const tiles = h('div', {class: 'kpis tiles'},
    tile('i-robot', 'Agents on', `${enabled.length}/${inv.agents.length}`),
    tile('i-skills', 'Skills', inv.skills.length, '#skills'),
    tile('i-roles', 'Roles', inv.roles.length, '#roles'),
    tile('i-plug', 'MCP servers', inv.mcp.length, '#wiring'),
    tile('i-bolt', 'Hooks', inv.hooks.length, '#wiring'),
    cronTrouble ? tile('i-crons', 'Crons failing', `${cronTrouble}/${cronJobs.length}`, '#crons', true) : tile('i-crons', 'Crons', store.crons ? cronJobs.length : '-', '#crons'));

  const issues = [];
  for (const agent of inv.agents.filter((item) => item.inspected)) {
    if (agent.error) issues.push(['i-fail', `${agent.name}: config unreadable`, '#config']);
    if (agent.counts.drifted) issues.push(['i-drift', `${agent.name}: ${plural(agent.counts.drifted, 'drifted link')}`, '#skills']);
    if (agent.counts.missing) issues.push(['i-minus', `${agent.name}: ${plural(agent.counts.missing, 'skill')} not linked`, '#skills']);
    if (agent.counts.conflicts) issues.push(['i-warn', `${agent.name}: ${plural(agent.counts.conflicts, 'conflict')}`, '#unmanaged']);
    if (agent.root_state && agent.root_state !== 'synced') issues.push(['i-warn', `${agent.name}: root instructions ${agent.root_state}`, null]);
  }
  if (inv.repo_state && inv.repo_state !== 'synced') issues.unshift(['i-link', `~/.agents link is ${inv.repo_state}`, null]);
  const attention = issues.length
    ? h('ul', {class: 'issues'}, issues.map(([iconId, text, href]) => h('li', {}, icon(iconId), href ? h('a', {href, text}) : h('span', {text}))))
    : null;
  const crons = store.crons ? h('section', {class: 'block'},
    h('div', {class: 'block-title'}, icon('i-crons'), h('h2', {}, h('a', {href: '#crons', text: 'Crons'})), cronTrouble ? h('span', {class: 'count-chip bad', title: 'Jobs that need attention', text: String(cronTrouble)}) : null),
    h('div', {class: 'hero-grid'}, [...new Set(cronJobs.map((job) => job.host))].map((host) => hostCard(host, store.crons)), upNextCard(cronJobs))) : null;
  const agents = inv.agents.length ? h('div', {class: 'cards'}, inv.agents.map(agentCard)) : noAgents();
  mount(root, tiles, attention, agents, crons);
}

// An empty agents list is a config state, not a load in progress: say so and name the command that fills it.
function noAgents() {
  return h('section', {class: 'card'},
    h('h2', {text: 'No agents in your config'}),
    h('p', {class: 'muted'}, 'tackroom.yaml lists no agents, so sync has nothing to write to. Setup finds the agents installed on this machine, adds them to tackroom.yaml and syncs them.'),
    command('tackroom setup'),
    h('p', {class: 'muted'}, 'Or add them by hand in ', h('a', {href: '#config', text: 'Config'}), '.'));
}
// A command to run in a terminal, with a copy button.
function command(text) { return h('div', {class: 'command'}, h('code', {text}), copyButton(text)); }

function tile(iconId, label, value, href, bad = false) {
  return h(href ? 'a' : 'div', {class: 'kpi', href}, h('span', {class: 'kpi-label'}, icon(iconId), label), h('b', {class: bad ? 'kpi-value bad' : 'kpi-value', text: String(value)}));
}

function agentCard(agent) {
  const [kind, statusIcon, label] = agentStatus(agent);
  const local = definedLocally('agents', agent.name);
  const toggle = switchControl(agent.enabled, `${agent.name} enabled`, (event) => patch('agents', agent.name, 'enabled', event.target.checked, `${agent.name} ${event.target.checked ? 'turned on' : 'turned off'}.`));
  const live = agent.inspected && agent.detected && !agent.error;
  const c = agent.counts || {};
  const total = store.inv.skills.length;
  const visual = live
    ? ring([
      {value: c.managed, kind: 'ok', label: 'linked'},
      {value: c.drifted, kind: 'warn', label: 'drifted'},
      {value: c.missing, kind: 'warn', label: 'missing'},
      {value: Math.max(0, total - c.managed - c.drifted - c.missing), kind: 'none', label: 'not linked'},
    ], c.managed, 'skills', 76)
    : ring([], '-', agent.enabled ? '' : 'off', 76);
  const value = (n) => (live ? n || 0 : '-');
  const title = [agent.error, agent.skill_root && tilde(agent.skill_root), live && `${tokens(agent.tokens)} tokens of skill listings (est.)`].filter(Boolean).join(' · ');
  const idle = !agent.enabled || (agent.inspected && !agent.detected);
  return h('article', {class: idle ? 'card agent off' : 'card agent'},
    h('div', {class: 'agent-head'},
      agentMark(agent.name, 'ag ag-card'),
      h('div', {class: 'agent-name'},
        h('h3', {title}, agent.name, local ? h('span', {class: 'tag local', text: 'local'}) : null),
        h('span', {class: `status ${kind}`}, icon(statusIcon), label)),
      toggle),
    h('div', {class: 'agent-body'}, visual,
      h('ul', {class: 'facts'},
        fact('i-drift', 'Drifted', value(c.drifted), c.drifted ? 'warn' : ''),
        fact('i-minus', 'Missing', value(c.missing), c.missing ? 'warn' : ''),
        fact('i-unmanaged', 'Unmanaged', value(c.unmanaged)))));
}
function fact(iconId, label, value, kind = '') { return h('li', {class: kind}, icon(iconId), h('span', {text: label}), h('b', {text: String(value)})); }

// ---------- matrices ----------
// The agent's mark shows beside its dot only when the matrix stacks on a narrow screen.
function cellMark(agent) { return h('span', {class: 'cell-agent', 'aria-hidden': 'true'}, agentMark(agent)); }
function cell(state, agent) {
  const word = CELLS[state] || CELLS.unknown;
  return [cellMark(agent), h('span', {class: `cell ${state}`, role: 'img', title: `${agent}: ${word}`, 'aria-label': `${agent}: ${word}`})];
}
// How many agent cells are in each state, as a figure, a bar and a legend.
function coverage(items, states, noun) {
  const counts = {};
  for (const item of items) for (const agent of columns()) { const state = item.states[agent] || 'unknown'; counts[state] = (counts[state] || 0) + 1; }
  const n = (state) => counts[state] || 0;
  const untargeted = (state) => ['absent', 'unsupported', 'off', 'disabled'].includes(state);
  const targeted = Object.entries(counts).filter(([state]) => !untargeted(state)).reduce((sum, [, count]) => sum + count, 0);
  return h('div', {class: 'coverage'},
    h('div', {class: 'coverage-figure'}, h('b', {text: `${n('ok')}/${targeted}`}), h('span', {text: `${noun} in place`})),
    bar([...new Set([...states, ...Object.keys(counts)])].filter((state) => !untargeted(state)).map((state) => ({kind: state, value: n(state)}))),
    h('div', {class: 'legend'}, states.filter((state) => n(state) || ['ok', 'drift', 'missing'].includes(state)).map((state) => h('span', {}, h('span', {class: `cell ${state}`, 'aria-hidden': 'true'}), CELLS[state], h('b', {text: String(n(state))})))));
}
function matrixHead(lead) {
  return h('thead', {}, h('tr', {}, h('th', {class: 'lead', scope: 'col', text: lead}), columns().map((name) => h('th', {scope: 'col'}, h('span', {class: 'col-agent', title: name}, agentMark(name), h('span', {class: 'col-name', text: name}))))));
}

function renderSkills(root) {
  const inv = store.inv;
  const query = store.skillQuery.trim().toLowerCase();
  const rows = inv.skills.filter((skill) => {
    if (query && !`${skill.name} ${skill.description} ${skill.origin}`.toLowerCase().includes(query)) return false;
    if (store.skillFilter === 'attention') return Object.values(skill.states).some((state) => ATTENTION.has(state));
    if (store.skillFilter === 'local') return skill.origin === 'local';
    if (store.skillFilter === 'external') return skill.origin !== 'local';
    return true;
  });
  const search = h('input', {class: 'search', type: 'search', placeholder: 'Search skills', 'aria-label': 'Search skills', value: store.skillQuery, oninput: (event) => {
    store.skillQuery = event.target.value;
    const caret = event.target.selectionStart;
    renderView();
    const next = document.querySelector('#view-skills .search');
    next.focus(); next.setSelectionRange(caret, caret);
  }});
  const filters = segmented([['all', 'All'], ['attention', 'Needs attention'], ['local', 'Local'], ['external', 'External']], store.skillFilter, (value) => { store.skillFilter = value; renderView(); });
  const tbody = h('tbody', {});
  for (const skill of rows) {
    const open = store.openRows.has(`skill:${skill.name}`);
    const row = h('tr', {class: 'row', tabindex: '0', 'aria-expanded': String(open), onclick: () => toggleRow(`skill:${skill.name}`), onkeydown: (event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); toggleRow(`skill:${skill.name}`); } }},
      h('td', {class: 'lead'},
        h('div', {class: 'lead-title', title: skill.description || ''}, skill.name,
          skill.origin === 'local' ? null : h('span', {class: 'tag', title: skill.origin}, icon('i-github'), skill.origin.split('@')[0]))),
      columns().map((agent) => h('td', {}, cell(skill.states[agent] || 'unknown', agent))));
    tbody.append(row);
    if (open) {
      tbody.append(h('tr', {class: 'detail'}, h('td', {colspan: String(columns().length + 1)}, h('div', {class: 'detail-grid'},
        skill.description ? h('p', {text: skill.description}) : null,
        h('dl', {},
          h('dt', {text: 'origin'}), h('dd', {text: skill.origin}),
          h('dt', {text: 'path'}), h('dd', {class: 'mono', text: tilde(skill.path)}),
          h('dt', {text: 'listing'}), h('dd', {text: `${tokens(skill.tokens)} tokens (est.)`}))))));
    }
  }
  const table = rows.length
    ? h('div', {class: 'matrix-wrap'}, h('table', {class: 'matrix'}, matrixHead(`${plural(rows.length, 'skill')}`), tbody))
    : h('p', {class: 'empty', text: inv.skills.length ? 'No skills match this filter.' : 'No canonical skills yet. Add one with tackroom skill new <name>.'});
  mount(root,
    coverage(inv.skills, ['ok', 'drift', 'missing', 'conflict', 'absent'], 'links'),
    h('div', {class: 'toolbar'}, h('label', {class: 'search-wrap'}, icon('i-search'), search), filters),
    table);
}
function toggleRow(key) {
  if (store.openRows.has(key)) store.openRows.delete(key); else store.openRows.add(key);
  renderView();
}
function segmented(options, current, onpick) {
  return h('div', {class: 'segmented', role: 'group'}, options.map(([value, label]) => h('button', {type: 'button', 'aria-pressed': String(value === current), onclick: () => onpick(value), text: label})));
}

function renderRoles(root) {
  const inv = store.inv;
  const body = inv.roles.length
    ? h('div', {class: 'matrix-wrap'}, h('table', {class: 'matrix'}, matrixHead(plural(inv.roles.length, 'role')),
      h('tbody', {}, inv.roles.map((role) => h('tr', {}, h('td', {class: 'lead'}, h('div', {class: 'lead-title', text: role.name})), columns().map((agent) => h('td', {}, cell(role.states[agent] || 'unknown', agent))))))))
    : h('p', {class: 'empty', text: 'No agent roles are configured. Roles live in agents/*.md in your config root.'});
  mount(root, inv.roles.length ? coverage(inv.roles, ['ok', 'drift', 'missing', 'unsupported'], 'roles') : null, body);
}

// ---------- wiring ----------
const EVENT_ICONS = {SessionStart: 'i-play', Stop: 'i-stop', SessionEnd: 'i-signout'};
function renderWiring(root) {
  const inv = store.inv;
  mount(root,
    h('div', {class: 'legend'}, ['ok', 'missing', 'drift', 'off', 'disabled', 'unsupported'].map((state) => h('span', {}, h('span', {class: `cell ${state}`, 'aria-hidden': 'true'}), CELLS[state]))),
    h('div', {class: 'block-title'}, icon('i-plug'), h('h2', {text: 'MCP servers'}), h('span', {class: 'count-chip', text: String(inv.mcp.length)})),
    wiringTable('mcp', inv.mcp),
    h('div', {class: 'block-title'}, icon('i-bolt'), h('h2', {text: 'Hooks'}), h('span', {class: 'count-chip', text: String(inv.hooks.length)})),
    wiringTable('hooks', inv.hooks));
}
function wiringTable(kind, rows) {
  if (!rows.length) return h('p', {class: 'empty', text: kind === 'mcp' ? 'No MCP servers yet. Add one with tackroom mcp add.' : 'No hooks yet. Add them under hooks: in tackroom.yaml.'});
  const section = kind === 'mcp' ? 'mcp_servers' : 'hooks';
  const supportKey = kind === 'mcp' ? 'supports_mcp' : 'supports_hooks';
  return h('div', {class: 'matrix-wrap'}, h('table', {class: 'matrix'}, matrixHead(kind === 'mcp' ? 'Server' : 'Hook'),
    h('tbody', {}, rows.map((row) => {
      const local = definedLocally(section, row.name);
      const lead = h('td', {class: 'lead'}, h('div', {class: 'lead-row'},
        switchControl(row.enabled, `${row.name} enabled`, (event) => patch(section, row.name, 'enabled', event.target.checked, `${row.name} ${event.target.checked ? 'turned on' : 'turned off'}.`)),
        h('div', {class: 'lead-title', title: tilde(row.command || '')}, row.name,
          row.event ? h('span', {class: 'tag'}, icon(EVENT_ICONS[row.event] || 'i-bolt'), row.event) : null,
          row.explicit ? null : h('span', {class: 'tag', text: 'all agents'}),
          local ? h('span', {class: 'tag local', text: 'local'}) : null)));
      const cells = columns().map((agent) => {
        const state = row.states[agent] || 'unknown';
        const targeted = !['off', 'unsupported', 'absent', 'error'].includes(state);
        const locked = ['unsupported', 'absent', 'error'].includes(state) || store.busy;
        const word = CELLS[state] || CELLS.unknown;
        return h('td', {}, h('button', {
          type: 'button', class: `cell ${state}`, disabled: locked || undefined,
          'aria-pressed': String(targeted), 'aria-label': `${row.name} on ${agent}: ${word}`,
          title: locked ? `${agent}: ${word}` : `${agent}: ${word}. Select to ${targeted ? 'remove' : 'add'} this agent.`,
          onclick: () => toggleTarget(section, supportKey, row, agent, targeted),
        }, cellMark(agent)));
      });
      return h('tr', {}, lead, cells);
    }))));
}
async function toggleTarget(section, supportKey, row, agent, targeted) {
  const universe = store.inv.agents.filter((item) => item[supportKey]).map((item) => item.name);
  const current = row.explicit ? [...row.targets] : universe;
  const next = targeted ? current.filter((name) => name !== agent) : [...new Set([...current, agent])];
  if (!next.length) {
    toast('At least one agent must stay targeted. Turn the entry off instead.', true);
    return;
  }
  await patch(section, row.name, 'agents', agentOrder(next), `${row.name} ${targeted ? 'removed from' : 'added to'} ${agent}.`);
}

// ---------- unmanaged ----------
const UNMANAGED_GROUPS = [
  ['skill', 'i-unmanaged', 'Skills in agent folders', 'tackroom found these next to its own links and leaves them alone. Copy the promote command to manage one from your config root.'],
  ['conflict', 'i-warn', 'Conflicts', 'A native file differs from what tackroom would write, so sync skips it.'],
  ['stale', 'i-link', 'Stale links', 'Links into the tackroom store for skills that are no longer canonical. Sync removes them.'],
];
function renderUnmanaged(root) {
  const items = store.inv.unmanaged;
  const groups = UNMANAGED_GROUPS.map(([kind, iconId, title, help]) => {
    const list = items.filter((item) => item.kind === kind);
    if (!list.length) return null;
    return h('section', {class: 'block'},
      h('div', {class: 'block-title'}, icon(iconId, kind === 'conflict' ? 'bad' : ''), h('h2', {text: title}), h('span', {class: kind === 'conflict' ? 'count-chip bad' : 'count-chip', text: String(list.length)}),
        h('span', {class: 'help', tabindex: '0', role: 'note', 'aria-label': help, dataset: {tip: help}}, icon('i-info'))),
      h('div', {class: 'items'}, list.map((item) => {
        const link = /symlink/.test(item.detail || '');
        return h('div', {class: 'item'},
          h('span', {class: 'item-icon', title: tilde(item.detail || '')}, icon(link ? 'i-link' : 'i-folder')),
          h('span', {class: 'item-name', title: tilde(item.detail || ''), text: item.name}),
          h('span', {class: 'chips'}, item.agents.map((agent) => h('span', {class: 'tag', title: agent, 'aria-label': agent}, agentMark(agent)))),
          item.hint ? copyButton(tilde(item.hint)) : h('span'));
      })));
  }).filter(Boolean);
  mount(root, ...(groups.length ? groups : [h('p', {class: 'empty', text: 'Everything in your agents\' skill folders is managed by tackroom.'})]));
}

const DAY_NAMES = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const STRIP_HOURS = 24;

async function loadCrons(refresh = false) {
  store.cronsLoading = true;
  if (store.view === 'crons') renderView();
  const started = Date.now();
  try {
    const data = await api(`/api/crons${refresh ? '?refresh=1' : ''}`);
    // A row changed after this collect began is newer than what it read.
    for (const patch of store.cronPatches.filter((item) => item.at >= started)) data.jobs = replaceCronRow(data.jobs || [], patch.matches, patch.fresh);
    store.crons = data;
    store.cronsError = '';
  } catch (error) {
    store.cronsError = error.message;
  }
  store.cronsLoading = false;
  renderChrome();
  if (store.view === 'crons' || store.view === 'overview') renderView();
}
function ago(iso) {
  const time = Date.parse(iso);
  if (!time) return '';
  const minutes = Math.round((Date.now() - time) / 60000);
  if (minutes < 2) return 'just now';
  if (minutes < 90) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 36) return `${hours} h ago`;
  return `${Math.round(hours / 24)} d ago`;
}
function until(ms) {
  const minutes = Math.max(1, Math.round(ms / 60000));
  if (minutes < 60) return `in ${minutes} min`;
  const hours = Math.floor(minutes / 60);
  return minutes % 60 && hours < 10 ? `in ${hours} h ${minutes % 60} min` : `in ${Math.round(minutes / 60)} h`;
}
function clock(ms) { return new Date(ms).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit', hourCycle: 'h23'}); }
function grouped(text) { return String(text).replace(/\d{4,}/g, (digits) => Number(digits).toLocaleString('en-US')); }
function sentence(text) { return text ? text[0].toUpperCase() + text.slice(1) : ''; }

// Cron fields: minute hour day-of-month month day-of-week, with *, lists, ranges and steps.
function cronField(text, min, max) {
  const values = new Set();
  for (const part of text.split(',')) {
    const [range, stepText] = part.split('/');
    const step = Number(stepText || 1);
    let [lo, hi] = range === '*' ? [min, max] : range.split('-').map(Number);
    if (hi === undefined) hi = stepText ? max : lo;
    if (![lo, hi, step].every(Number.isFinite) || step < 1 || lo < min || hi > max) return null;
    for (let value = lo; value <= hi; value += step) values.add(max === 7 && value === 7 ? 0 : value);
  }
  return values;
}
function parseCron(expr) {
  const fields = expr.trim().split(/\s+/);
  if (fields.length !== 5) return null;
  const spec = {m: cronField(fields[0], 0, 59), h: cronField(fields[1], 0, 23), dom: cronField(fields[2], 1, 31), mon: cronField(fields[3], 1, 12), dow: cronField(fields[4], 0, 7)};
  if (Object.values(spec).some((value) => value === null)) return null;
  return {...spec, domAny: fields[2] === '*', dowAny: fields[4] === '*'};
}
// `wall` carries the job's local time in its UTC fields.
function cronMatches(spec, wall) {
  if (!spec.m.has(wall.getUTCMinutes()) || !spec.h.has(wall.getUTCHours()) || !spec.mon.has(wall.getUTCMonth() + 1)) return false;
  const dom = spec.dom.has(wall.getUTCDate());
  const dow = spec.dow.has(wall.getUTCDay());
  return spec.domAny || spec.dowAny ? dom && dow : dom || dow;
}
// Minutes east of UTC for an IANA zone or a +HH:MM offset.
function zoneOffset(tz, at) {
  const fixed = /^([+-])(\d\d):(\d\d)$/.exec(tz || '');
  if (fixed) return (fixed[1] === '-' ? -1 : 1) * (Number(fixed[2]) * 60 + Number(fixed[3]));
  try {
    const parts = Object.fromEntries(new Intl.DateTimeFormat('en-US', {timeZone: tz, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'})
      .formatToParts(at).map((part) => [part.type, part.value]));
    return Math.round((Date.UTC(+parts.year, parts.month - 1, +parts.day, +parts.hour, +parts.minute) - Math.floor(at / 60000) * 60000) / 60000);
  } catch {
    return -at.getTimezoneOffset();
  }
}
// The zone offset in ms at a time, looked up once per hour: offsets change on the hour or half hour.
function hourlyOffset(tz) {
  let hour = null;
  let offset = 0;
  return (t) => {
    const at = Math.floor(t / 1800000);
    if (at !== hour) { hour = at; offset = zoneOffset(tz, new Date(t)) * 60000; }
    return offset;
  };
}
// Fire times in the next STRIP_HOURS for cron and interval schedules; null for services and unknown forms.
function cronFires(job, from) {
  if (job.state === 'service') return null;
  const schedule = job.schedule || '';
  const end = from + STRIP_HOURS * 3600000;
  const every = /^every (\d+)s$/.exec(schedule);
  if (every) {
    const step = Number(every[1]) * 1000;
    const base = Date.parse(job.last_run) || from;
    let next = base + step * Math.ceil(Math.max(0, from - base) / step);
    const fires = [];
    for (; next < end && fires.length < 2000; next += step) fires.push(next);
    return {fires, interval: Number(every[1])};
  }
  const specs = schedule.split(';').map(parseCron);
  if (!specs.length || specs.some((spec) => !spec)) return null;
  const start = Math.ceil(from / 60000) * 60000;
  const offset = hourlyOffset(job.tz);
  const fires = [];
  for (let t = start; t < end; t += 60000) {
    const wall = new Date(t + offset(t));
    if (specs.some((spec) => cronMatches(spec, wall))) fires.push(t);
  }
  return {fires};
}
// Runs more often than every 30 minutes read as a band, not as single runs.
function dense({fires, interval}) { return interval ? interval < 1800 : fires.length > 48; }
// The first fire within a week, for jobs that do not run in the next STRIP_HOURS.
function nextFire(job, from) {
  const specs = (job.schedule || '').split(';').map(parseCron);
  if (!specs.length || specs.some((spec) => !spec)) return null;
  const start = Math.ceil(from / 60000) * 60000;
  const offset = hourlyOffset(job.tz);
  for (let t = start; t < start + 8 * 86400000; t += 60000) {
    if (specs.some((spec) => cronMatches(spec, new Date(t + offset(t))))) return t;
  }
  return null;
}
function weekdayClock(ms) { return `${DAY_NAMES[new Date(ms).getDay()]} ${clock(ms)}`; }
const PAUSED = ['disabled', 'paused', 'not_loaded'];
const SOURCE_ICONS = {launchd: 'i-launchd', crontab: 'i-crontab', systemd: 'i-gear', codex: 'a-codex', 'claude-desktop': 'a-claude-code', 'claude-cloud': 'a-claude-code', 'herdr-automations': 'i-alarm'};
const HEALTH = {ok: ['i-ok', 'Healthy'], idle: ['i-idle', 'No result'], warn: ['i-warn', 'Last run failed'], fail: ['i-fail', 'Failing'], paused: ['i-pause', 'Paused']};
const HEALTH_ORDER = ['ok', 'idle', 'warn', 'fail', 'paused'];
function sourceIcon(job) { return icon(SOURCE_ICONS[job.source] || 'i-crons', 'src-icon'); }
function compact(n) { return n >= 10000 ? `${(n / 1000).toFixed(n >= 100000 ? 0 : 1)}k` : n.toLocaleString('en-US'); }
// The kind of trouble as [icon, label, short detail].
function problemKind(job) {
  const problems = job.problems || [];
  const first = problems[0] || '';
  let match = /crash loop: KeepAlive restarted it (\d+) times, last exit (-?\d+)/.exec(first);
  if (match) return ['i-loop', 'Crash loop', `${compact(Number(match[1]))} restarts · exit ${match[2]}`];
  const missing = problems.filter((problem) => problem.startsWith('path does not exist: '));
  if (missing.length) return ['i-file-x', 'Missing file', missing.map((problem) => problem.split('/').pop()).join(', ')];
  if (first.includes('has not run')) return ['i-alarm', 'Missed run', job.last_run ? `last ran ${ago(job.last_run)}` : 'never ran'];
  if (first.startsWith('last error: ')) {
    const text = first.slice(12).replace(/^\[[^\]]*\]\s*/, '');
    const auth = /credential|auth/i.test(text);
    return [auth ? 'i-key' : 'i-fail', auth ? 'Auth error' : 'Run failed', text.split(/[:.]/)[0].trim()];
  }
  match = /^last exit (-?\d+)/.exec(first);
  if (match) return ['i-fail', 'Exited', `exit ${match[1]}`];
  return ['i-warn', sentence(job.state), sentence(first)];
}
function cronKey(job) { return `cron:${job.host}/${job.source}/${job.id}`; }
function hostName(host) { return host === 'local' ? 'This Mac' : host || 'Unknown host'; }
function openCron(job) {
  store.cronFilter = 'all';
  store.openRows.add(cronKey(job));
  renderView();
  document.getElementById(cronKey(job))?.scrollIntoView({block: 'center'});
}

function renderCrons(root) {
  const data = store.crons;
  actions(
    h('button', {type: 'button', class: 'primary', onclick: () => openJobPanel('add')}, icon('i-plus'), 'Add cron'),
    data ? h('span', {class: 'stamp', title: `Collected ${new Date(Date.parse(data.scanned_at)).toLocaleString()}`}, icon('i-crontab'), clock(Date.parse(data.scanned_at))) : null,
    h('button', {type: 'button', class: store.cronsLoading ? 'icon-button spinning' : 'icon-button', disabled: store.cronsLoading || undefined, 'aria-label': 'Collect again', title: store.cronsLoading ? 'Collecting…' : 'Collect again', onclick: () => loadCrons(true)}, icon('i-refresh')));
  if (!data) {
    if (store.cronsError) {
      mount(root, h('div', {class: 'error-box'}, h('strong', {text: 'Could not collect scheduled jobs. '}), store.cronsError, ' ', h('button', {type: 'button', class: 'quiet', onclick: () => loadCrons(true), text: 'Retry'})));
      return;
    }
    // The final layout with its figures still empty, so nothing moves when the data lands.
    const waiting = (iconId, title) => h('section', {class: 'hero-card'}, h('div', {class: 'hero-head'}, icon(iconId), h('h2', {text: title})), h('div', {class: 'host-body'}, ring([], '-', 'jobs'), h('p', {class: 'muted', text: 'Collecting…'})));
    mount(root, h('div', {class: 'hero-grid'}, waiting('i-laptop', 'This Mac'), waiting('i-laptop', 'Hosts'), waiting('i-timer', 'Up next')));
    return;
  }
  const jobs = data.jobs || [];
  const hosts = [...new Set([...(data.access || []).map((entry) => entry.host), ...jobs.map((job) => job.host)])];
  const attention = jobs.filter((job) => job.health === 'fail').sort((a, b) => (Date.parse(b.last_run) || 0) - (Date.parse(a.last_run) || 0));
  mount(root,
    h('div', {class: 'hero-grid'}, hosts.map((host) => hostCard(host, data)), upNextCard(jobs)),
    attention.length ? h('section', {class: 'block'},
      h('div', {class: 'block-title'}, icon('i-warn', 'bad'), h('h2', {text: 'Needs attention'}), h('span', {class: 'count-chip bad', text: String(attention.length)})),
      h('div', {class: 'alert-grid'}, attention.map(alertCard))) : null,
    fireStrip(jobs, hosts),
    jobsBlock(jobs, hosts));
}
function hostCard(host, data) {
  const access = (data.access || []).filter((entry) => entry.host === host);
  const unreachable = access.length > 0 && access.every((entry) => entry.state === 'not_scanned') && access.some((entry) => (entry.detail || '').startsWith('probe failed'));
  const jobs = data.jobs.filter((job) => job.host === host);
  const counts = Object.fromEntries(HEALTH_ORDER.map((kind) => [kind, 0]));
  jobs.forEach((job) => { counts[job.health] += 1; });
  // An empty herdr-automations source still says whether its scheduler runs.
  const sources = access.filter((entry) => entry.state === 'found' || entry.state === 'unreadable' || (entry.source === 'herdr-automations' && entry.state === 'empty' && entry.detail && entry.detail.startsWith('scheduler')));
  return h('section', {class: 'hero-card'},
    h('div', {class: 'hero-head'}, icon('i-laptop'), h('h2', {text: hostName(host)}), unreachable ? h('span', {class: 'pill bad', title: access[0].detail, text: 'unreachable'}) : null),
    h('div', {class: 'host-body'},
      ring(HEALTH_ORDER.map((kind) => ({kind, value: counts[kind], label: HEALTH[kind][1].toLowerCase()})), jobs.length, 'jobs'),
      h('ul', {class: 'health-list'}, HEALTH_ORDER.filter((kind) => counts[kind]).map((kind) => h('li', {class: kind}, icon(HEALTH[kind][0]), h('span', {text: HEALTH[kind][1]}), h('b', {text: String(counts[kind])}))))),
    h('div', {class: 'source-row'}, sources.map((entry) => h('span', {class: entry.state === 'unreadable' ? 'src bad' : 'src', title: `${entry.source}: ${entry.state === 'found' ? plural(entry.jobs, 'job') + (entry.detail ? `, ${entry.detail}` : '') : entry.detail || entry.state}`},
      icon(SOURCE_ICONS[entry.source] || 'i-crons'), entry.source, h('b', {text: entry.state === 'unreadable' ? '!' : String(entry.jobs)})))));
}
function upNextCard(jobs) {
  const now = Date.now();
  const queue = [];
  for (const job of jobs) {
    if (PAUSED.includes(job.state) || job.state === 'service') continue;
    const fires = cronFires(job, now);
    if (fires && dense(fires)) continue;
    const next = fires?.fires?.[0] ?? nextFire(job, now);
    if (next) queue.push({job, next});
  }
  queue.sort((a, b) => a.next - b.next);
  const [first, ...rest] = queue;
  const when = (t) => (t - now < 86400000 ? clock(t) : weekdayClock(t));
  return h('section', {class: 'hero-card'},
    h('div', {class: 'hero-head'}, icon('i-timer'), h('h2', {text: 'Up next'})),
    first
      ? h('div', {class: 'upnext-lead', title: `${first.job.name || first.job.id} · ${hostName(first.job.host)} · ${when(first.next)}`},
        h('b', {class: 'upnext-in', text: until(first.next - now).replace(/^in /, '')}),
        h('span', {class: 'upnext-name'}, sourceIcon(first.job), first.job.short_name, h('span', {class: 'faint', text: hostName(first.job.host)})))
      : h('p', {class: 'muted', text: 'Nothing scheduled this week.'}),
    rest.length ? h('ul', {class: 'upnext-list'}, rest.slice(0, 4).map(({job, next}) => h('li', {title: `${job.name || job.id} · ${hostName(job.host)}`},
      h('span', {class: 'fig', text: when(next)}), sourceIcon(job), h('span', {class: 'upnext-job', text: job.short_name}), h('span', {class: 'faint', text: hostName(job.host)})))) : null);
}
function alertCard(job) {
  const [iconId, label, detail] = problemKind(job);
  const stale = iconId === 'i-alarm' && Date.now() - (Date.parse(job.last_run) || 0) > 7 * 86400000;
  return h('button', {type: 'button', class: stale ? 'alert-card stale' : 'alert-card', title: `${job.name || job.id}\n${(job.problems || []).map((problem) => sentence(grouped(problem))).join('\n')}`, onclick: () => openCron(job)},
    h('span', {class: 'alert-top'}, icon(iconId, 'alert-icon'), h('span', {text: label}), h('span', {class: 'alert-ago', text: job.last_run ? ago(job.last_run) : ''})),
    h('span', {class: 'alert-name', text: job.short_name}),
    h('span', {class: 'alert-meta'}, icon('i-laptop'), h('span', {text: hostName(job.host)}), detail ? h('span', {class: 'alert-detail', text: detail}) : null));
}
function fireStrip(jobs, hosts) {
  const now = Date.now();
  const rows = [];
  for (const host of hosts) {
    for (const job of jobs.filter((item) => item.host === host && !PAUSED.includes(item.state))) {
      const fires = cronFires(job, now);
      if (fires) rows.push({job, ...fires});
    }
  }
  rows.sort((a, b) => (a.job.host === b.job.host ? (a.fires[0] ?? Infinity) - (b.fires[0] ?? Infinity) : hosts.indexOf(a.job.host) - hosts.indexOf(b.job.host)));
  const span = STRIP_HOURS * 3600000;
  const at = (t) => `${(((t - now) / span) * 100).toFixed(3)}%`;
  const firstHour = Math.ceil(now / 3600000) * 3600000;
  const ticks = [];
  for (let t = firstHour; t < now + span; t += 3600000) if (new Date(t).getHours() % 3 === 0) ticks.push(t);
  const axis = h('div', {class: 'strip-row strip-axis'}, h('span', {class: 'strip-label'}),
    h('div', {class: 'strip-track'}, h('span', {class: 'strip-now', style: {left: '0'}, text: `Now ${clock(now)}`}), ticks.filter((t) => t - now > 2 * 3600000).map((t) => h('span', {class: t - now < 7 * 3600000 || new Date(t).getHours() % 6 ? 'strip-hour minor' : 'strip-hour', style: {left: at(t)}, text: clock(t)}))));
  const body = rows.map(({job, fires, interval}) => {
    const band = dense({fires, interval});
    const many = !band && fires.length > 24;
    const later = fires.length ? null : nextFire(job, now);
    const track = h('div', {class: 'strip-track', title: `${job.schedule_text}${fires[0] ? `, next ${clock(fires[0])}` : ''}`},
      ticks.map((t) => h('span', {class: 'strip-grid', style: {left: at(t)}})),
      band
        ? h('span', {class: 'strip-band', style: {left: at(fires[0] ?? now), right: '0'}})
        : fires.map((t) => h('span', {class: 'strip-fire', style: {left: at(t)}})),
      many ? h('span', {class: 'strip-band alt', style: {left: at(fires[0]), right: '0'}}) : null,
      fires.length ? null : h('span', {class: 'strip-later', text: later ? `→ ${weekdayClock(later)}` : 'not this week'}));
    return h('div', {class: ['strip-row', (job.health === 'fail') && 'trouble', many && 'many'].filter(Boolean).join(' ')},
      h('span', {class: 'strip-label', title: [job.name || job.id, hostName(job.host), job.schedule_text].filter(Boolean).join(' · ')}, sourceIcon(job), h('span', {class: 'strip-name', text: job.short_name}), hosts.length > 1 ? h('span', {class: 'strip-host', text: hostName(job.host)}) : null),
      track);
  });
  return h('section', {class: 'block'},
    h('div', {class: 'block-title'}, icon('i-crons'), h('h2', {text: 'Next 24 hours'}), h('span', {class: 'count-chip', text: String(rows.length)})),
    h('div', {class: 'panel-block strip'}, h('div', {class: 'block-body'}, axis, ...body)));
}
function jobsBlock(jobs, hosts) {
  const healthCount = (kind) => jobs.filter((job) => job.health === kind).length;
  const filters = [
    ['all', null, 'All', jobs.length],
    ...(hosts.length > 1 ? hosts.map((host) => [`host:${host}`, 'i-laptop', hostName(host), jobs.filter((job) => job.host === host).length]) : []),
    ['health:fail', 'i-fail', 'Failing', healthCount('fail')],
    ['health:paused', 'i-pause', 'Paused', healthCount('paused')],
  ].filter(([value, , , count]) => value === 'all' || count > 0);
  const current = filters.some(([value]) => value === store.cronFilter) ? store.cronFilter : 'all';
  const [kind, value] = current.split(':');
  const rows = jobs.filter((job) => (kind === 'host' ? job.host === value : kind === 'health' ? job.health === value : true));
  return h('section', {class: 'block'},
    h('div', {class: 'block-title'}, icon('i-launchd'), h('h2', {text: 'Jobs'}),
      h('div', {class: 'filter-chips', role: 'group', 'aria-label': 'Filter jobs'}, filters.map(([option, iconId, label, count]) => h('button', {type: 'button', class: 'chip-button', 'aria-pressed': String(option === current), onclick: () => { store.cronFilter = option; renderView(); }},
        iconId ? icon(iconId) : null, label, h('span', {class: 'chip-count', text: String(count)}))))),
    h('div', {class: 'panel-block'}, cronTable(rows, hosts.length > 1)));
}
function cronTable(rows, showHost) {
  const now = Date.now();
  const tbody = h('tbody', {});
  const width = showHost ? 7 : 6;
  for (const job of rows) {
    const key = cronKey(job);
    const open = store.openRows.has(key);
    const toggle = () => toggleRow(key);
    const next = cronFires(job, now)?.fires?.[0];
    const health = job.health;
    const paused = health === 'paused';
    const schedule = job.schedule_text || job.schedule || '-';
    const later = !paused && !next && job.state !== 'service' ? nextFire(job, now) : null;
    const nextText = paused ? 'Paused' : next ? until(next - now) : later ? weekdayClock(later) : '-';
    const lastText = job.last_run ? ago(job.last_run) : '-';
    const result = job.last_exit != null ? `exit ${job.last_exit}` : job.last_status ? job.last_status.replace('_', ' ') : '';
    tbody.append(h('tr', {id: key, class: `row ${health}`, tabindex: '0', 'aria-expanded': String(open), onclick: toggle, onkeydown: (event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); toggle(); } }},
      h('td', {class: 'status-cell', title: [HEALTH[health][1], result].filter(Boolean).join(' · ')}, icon(HEALTH[health][0], `health ${health}`), h('span', {class: 'sr', text: HEALTH[health][1]})),
      h('td', {class: 'ident'},
        h('span', {class: 'ident-name', title: job.name || job.id}, sourceIcon(job), h('span', {text: job.short_name})),
        h('span', {class: 'ident-phone', text: [schedule !== '-' ? schedule : job.state === 'missing' ? 'Missing' : HEALTH[health][1], nextText !== '-' ? nextText : job.last_run ? `ran ${lastText}` : ''].filter(Boolean).join(' · ')})),
      showHost ? h('td', {class: 'muted nowrap', text: hostName(job.host)}) : null,
      h('td', {class: 'nowrap', title: job.schedule || '', text: schedule}),
      h('td', {class: paused ? 'faint nowrap fig' : 'nowrap fig', title: next ? new Date(next).toLocaleString() : '', text: nextText}),
      h('td', {class: 'muted nowrap fig', title: job.last_run || '', text: lastText}),
      h('td', {class: 'num', text: job.runs != null ? compact(job.runs) : '-'})));
    if (open) {
      const fields = [['Result', result], ['ID', job.id], ['Command', job.command], ['Log', job.log && tilde(job.log)], ['Schedule', job.schedule], ['Time zone', job.tz], ['Last run', job.last_run], ['Created by', job.created_by], ['Connectors', (job.connectors || []).join(', ')], ['Effect', job.effect], ['Inferred', (job.inferred || []).join(', ')]]
        .filter(([, field]) => field);
      tbody.append(h('tr', {class: 'detail'}, h('td', {colspan: String(width)}, h('div', {class: 'detail-grid'},
        (job.problems || []).length ? h('ul', {class: 'problems'}, job.problems.map((problem) => h('li', {text: sentence(grouped(problem))}))) : null,
        h('dl', {}, fields.flatMap(([label, field]) => [h('dt', {text: label}), h('dd', {class: ['ID', 'Command', 'Log', 'Schedule'].includes(label) ? 'code' : '', text: field})])),
        cronActions(job)))));
    }
  }
  if (!rows.length) tbody.append(h('tr', {}, h('td', {colspan: String(width), class: 'muted', text: 'No jobs match.'})));
  return h('div', {class: 'block-body'}, h('table', {class: 'grid-table jobs'},
    h('thead', {}, h('tr', {}, [['', 'status-cell'], ['Job', ''], showHost ? ['Host', ''] : null, ['Schedule', ''], ['Next run', ''], ['Last run', ''], ['Runs', 'num']].filter(Boolean).map(([label, cls]) => h('th', {scope: 'col', class: cls, text: label, 'aria-label': label || 'Status'})))),
    tbody));
}

// Run, pause and resume a launchd job. A job tackroom owns here also takes a new
// schedule, Edit and Delete; for any other job the server says why not (edit_note).
function cronActions(job) {
  const launchd = job.source === 'launchd' && job.unit;
  if (!launchd && !job.editable && !job.edit_note) return null;
  const busy = Boolean(store.cronBusy);
  const off = ['disabled', 'not_loaded'].includes(job.state);
  const button = (label, action, cls) => h('button', {type: 'button', class: cls, disabled: busy || undefined, onclick: () => cronAction(job, action), text: label});
  const input = launchd && job.owned && job.host === 'local' && job.state !== 'disabled'
    ? h('input', {type: 'text', value: job.declared_schedule || '', placeholder: '0 9 * * *', 'aria-label': 'Schedule (cron expression)', spellcheck: 'false', autocapitalize: 'off', autocomplete: 'off'})
    : null;
  return h('div', {class: 'cron-actions'},
    launchd ? (off ? button('Resume', 'resume', 'primary') : [button('Run now', 'run', 'primary'), button('Pause', 'pause')]) : null,
    job.editable ? [
      h('button', {type: 'button', disabled: busy || undefined, onclick: () => openJobPanel('edit', job), text: 'Edit'}),
      h('button', {type: 'button', class: 'danger', disabled: busy || undefined, onclick: () => openJobPanel('delete', job), text: 'Delete'}),
    ] : null,
    input ? h('form', {class: 'cron-schedule', onsubmit: (event) => { event.preventDefault(); cronAction(job, 'schedule', input.value); }},
      input, h('button', {type: 'submit', disabled: busy || undefined, text: 'Save schedule'})) : null,
    store.cronBusy === cronKey(job) ? h('span', {class: 'muted', text: 'Working…'}) : null,
    !job.editable && job.edit_note ? h('p', {class: 'note'}, h('b', {text: 'No edit or delete. '}), job.edit_note) : null);
}
async function cronAction(job, action, schedule) {
  store.cronBusy = cronKey(job);
  renderView();
  const done = {run: 'Started', pause: 'Paused', resume: 'Resumed', schedule: 'New schedule applied to'}[action];
  try {
    const result = await api('/api/crons/action', {method: 'POST', body: JSON.stringify({host: job.host, source: job.source, id: job.id, action, ...(action === 'schedule' ? {schedule} : {})})});
    toast(`${done} ${job.short_name}`);
    // The answer carries the job as it now is: redraw that row now, and let the
    // full collect below catch up in the background.
    if (result.job) patchCronRow((row) => row.host === job.host && row.source === job.source && row.id === job.id, result.job);
  } catch (error) {
    toast(error.message, true);
  }
  store.cronBusy = '';
  renderView();
  loadCrons(true);
}

// A row changed by an action or an edit is redrawn from the server's answer at
// once. A collect that began before that answer holds older data for the row,
// so loadCrons applies the same patch to its result.
function replaceCronRow(jobs, matches, fresh) {
  const out = [];
  let placed = !fresh;
  for (const job of jobs) {
    if (!matches(job)) out.push(job);
    else if (!placed) { out.push(fresh); placed = true; }
  }
  if (!placed) out.push(fresh);
  return out;
}
function patchCronRow(matches, fresh) {
  store.cronPatches = store.cronPatches.filter((patch) => Date.now() - patch.at < 120000);
  store.cronPatches.push({at: Date.now(), matches, fresh});
  if (!store.crons) return;
  store.crons = {...store.crons, jobs: replaceCronRow(store.crons.jobs || [], matches, fresh)};
  renderChrome();
  renderView();
}

// ---------- cron jobs: add, edit, delete ----------
// One drawer for the three. POST /api/crons/jobs does the work (the same code as
// `tackroom cron add|edit|remove`) and answers with the job's fresh record. A
// refusal shows in the form, where it can be fixed, not only in a toast.
const jobDialog = {mode: '', job: null, busy: false};
const quoted = (arg) => (/[\s"']/.test(arg) ? JSON.stringify(arg) : arg);
function jobField(label, control, hint) {
  return h('label', {class: 'field'}, h('span', {text: label}), control, hint ? h('small', {text: hint}) : null);
}
function setJobNote(message) {
  const note = document.getElementById('job-note');
  note.textContent = message;
  note.hidden = !message;
}
function openJobPanel(mode, job) {
  Object.assign(jobDialog, {mode, job: job || null, busy: false});
  const [executable = '', ...args] = job?.declared_command || [];
  const text = (name, value, extra = {}) => h('input', {type: 'text', name, value, autocomplete: 'off', autocapitalize: 'off', spellcheck: 'false', ...extra});
  document.getElementById('job-title').textContent = {add: 'Add cron', edit: `Edit ${job?.short_name}`, delete: `Delete ${job?.short_name}?`}[mode];
  const submit = document.getElementById('submit-job');
  submit.textContent = {add: 'Add cron', edit: 'Save changes', delete: 'Delete'}[mode];
  submit.className = mode === 'delete' ? 'danger' : 'primary';
  submit.disabled = false;
  setJobNote('');
  let first = document.getElementById('cancel-job');
  if (mode === 'delete') {
    mount(document.getElementById('job-body'),
      h('p', {}, 'This removes ', h('b', {text: job.short_name}), ' from your tackroom config and its unit from this machine. tackroom touches no other job.'),
      h('div', {class: 'detail-grid'}, h('dl', {}, [['Schedule', job.declared_schedule], ['Command', (job.declared_command || []).map(quoted).join(' ')], ['Effect', job.effect]]
        .filter(([, value]) => value).flatMap(([label, value]) => [h('dt', {text: label}), h('dd', {class: 'code', text: value})]))));
  } else {
    mount(document.getElementById('job-body'),
      mode === 'add'
        ? jobField('Name', text('id', '', {placeholder: 'nightly-backup', maxlength: '63'}), 'Lowercase letters, digits and dashes. The unit is dev.tackroom.<name>.')
        : jobField('Name', text('id', job.id, {readonly: true})),
      jobField('Schedule', text('schedule', job?.declared_schedule || '', {placeholder: '0 9 * * 1-5'}), 'Five fields: minute hour day-of-month month weekday. Not both a day of month and a weekday.'),
      jobField('Command', text('command', executable, {placeholder: '/usr/bin/git'}), 'The program: a name on PATH or an absolute path. ~ and $HOME expand when the unit is written.'),
      jobField('Arguments', h('textarea', {name: 'args', rows: '3', placeholder: '-C\n/srv/notes\npull', autocomplete: 'off', autocapitalize: 'off', spellcheck: 'false'}, args.join('\n')), 'One argument per line, kept as typed.'),
      jobField('Effect (optional)', text('effect', job?.effect || ''), 'What it changes, shown in the Crons tab.'));
    first = document.querySelector('#job-body input:not([readonly])');
  }
  const panel = document.getElementById('job-panel');
  if (!panel.open) panel.showModal();
  first.focus();
}
function closeJobPanel() {
  const panel = document.getElementById('job-panel');
  if (panel.open) panel.close();
}
async function submitJob(event) {
  event.preventDefault();
  if (jobDialog.busy) return;
  const {mode, job} = jobDialog;
  const form = new FormData(event.currentTarget);
  const command = form.get('command')?.trim() ? [form.get('command').trim(), ...String(form.get('args') || '').split('\n').map((line) => line.replace(/\r$/, '')).filter(Boolean)] : [];
  const schedule = String(form.get('schedule') || '').trim();
  const effect = String(form.get('effect') || '').trim();
  let body;
  if (mode === 'delete') {
    body = {action: 'remove', id: job.id};
  } else if (mode === 'add') {
    body = {action: 'add', id: String(form.get('id') || '').trim(), schedule, command, ...(effect ? {effect} : {})};
  } else {
    // Only what changed goes in the request, so the rest of the entry stays as written.
    body = {action: 'edit', id: job.id};
    if (schedule !== (job.declared_schedule || '')) body.schedule = schedule;
    if (command.join('\n') !== (job.declared_command || []).join('\n')) body.command = command;
    if (effect !== (job.effect || '')) body.effect = effect;
    if (Object.keys(body).length === 2) { closeJobPanel(); toast('Nothing to change.'); return; }
  }
  jobDialog.busy = true;
  const submit = document.getElementById('submit-job');
  const label = submit.textContent;
  submit.disabled = true;
  submit.textContent = 'Working…';
  setJobNote('');
  try {
    const result = await api('/api/crons/jobs', {method: 'POST', body: JSON.stringify(body)});
    closeJobPanel();
    patchCronRow((row) => row.id === body.id && (row.host === 'local' || row.state === 'missing'), result.job);
    toast({add: `Added ${body.id}`, edit: `Saved ${job?.short_name}`, delete: `Deleted ${job?.short_name}`}[mode]);
    loadCrons(true);
  } catch (error) {
    setJobNote(error.message);
    submit.disabled = false;
    submit.textContent = label;
  }
  jobDialog.busy = false;
}

// ---------- config ----------
async function loadConfigLayer(layer) {
  store.configLayer = layer;
  store.configDiff = '';
  store.configNote = '';
  store.configLoading = true;
  try { store.configState = await api(`/api/state?layer=${encodeURIComponent(layer)}`); }
  catch (error) { store.configState = null; store.configNote = error.message; }
  store.configLoading = false;
  renderView();
}
function renderConfig(root) {
  actions(segmented([['shared', 'Shared'], ['local', 'Local'], ['effective', 'Effective']], store.configLayer, (value) => loadConfigLayer(value)));
  const state = store.configState;
  if (!state || state.active_layer !== store.configLayer) {
    if (store.configNote) {
      mount(root, h('div', {class: 'error-box'}, store.configNote, ' ', h('button', {type: 'button', onclick: () => loadConfigLayer(store.configLayer), text: 'Retry'})));
      return;
    }
    mount(root, h('p', {class: 'loading', text: 'Loading config…'}));
    if (!store.configLoading) loadConfigLayer(store.configLayer);
    return;
  }
  const layer = store.configLayer;
  const readOnly = state.read_only;
  const original = state.raw_yaml || '';
  const draft = store.drafts[layer] ?? original;
  const editor = h('textarea', {class: 'editor', id: `editor-${layer}`, spellcheck: 'false', 'aria-label': `${layer} YAML`, readonly: readOnly || undefined, oninput: (event) => {
    store.drafts[layer] = event.target.value;
    store.configDiff = '';
    buttons.querySelectorAll('[data-dirty]').forEach((node) => { node.disabled = event.target.value === original; });
  }});
  editor.value = draft;
  const dirty = draft !== original;
  const buttons = h('div', {class: 'editor-actions'},
    h('button', {type: 'button', dataset: {dirty: '1'}, disabled: readOnly || !dirty || undefined, onclick: () => validateConfig(layer), text: 'Check changes'}),
    h('button', {type: 'button', class: 'primary', dataset: {dirty: '1'}, disabled: readOnly || !dirty || undefined, onclick: () => saveConfig(layer), text: 'Save'}),
    h('button', {type: 'button', class: 'quiet', dataset: {dirty: '1'}, disabled: readOnly || !dirty || undefined, onclick: () => { delete store.drafts[layer]; store.configDiff = ''; store.configNote = ''; renderView(); }, text: 'Discard'}),
    store.configNote ? h('span', {class: 'note', text: store.configNote}) : null);
  const path = layer === 'effective' ? 'merge of both files' : tilde(state.paths?.[layer] || '');
  mount(root,
    h('div', {class: 'config-meta'}, h('span', {}, icon('i-folder'), path), state.revision ? h('span', {}, icon('i-commit'), state.revision.slice(0, 12)) : null, readOnly ? h('span', {text: 'read-only'}) : null),
    editor,
    readOnly ? null : buttons,
    store.configDiff ? renderDiff(store.configDiff) : null);
}
function renderDiff(text) {
  return h('pre', {class: 'diff', 'aria-label': 'Changes'}, text.split('\n').map((line) => {
    const kind = line.startsWith('@@') ? 'hunk' : line.startsWith('+') && !line.startsWith('+++') ? 'add' : line.startsWith('-') && !line.startsWith('---') ? 'del' : '';
    return h('span', {class: kind}, `${tilde(line)}\n`);
  }));
}
async function validateConfig(layer) {
  try {
    const result = await api('/api/config/validate', {method: 'POST', body: JSON.stringify({layer, raw_yaml: store.drafts[layer]})});
    store.configDiff = result.diff || '';
    store.configNote = result.diff ? 'Valid. Review the changes, then save.' : 'Valid, and identical to the file on disk.';
  } catch (error) { store.configDiff = ''; store.configNote = error.message; }
  renderView();
}
async function saveConfig(layer) {
  try {
    await api('/api/config/raw', {method: 'PUT', body: JSON.stringify({layer, expected_revision: store.configState.revision, raw_yaml: store.drafts[layer]})});
    delete store.drafts[layer];
    toast(`Saved ${layer === 'local' ? 'tackroom.local.yaml' : 'tackroom.yaml'}. Sync to apply it.`);
    await loadConfigLayer(layer);
    await refresh();
  } catch (error) {
    store.configNote = error.code === 'stale_revision' ? 'The file changed on disk since you opened it. Discard your edit or copy it before reloading.' : error.message;
    renderView();
  }
}

// ---------- sync ----------
const PLAN_GROUPS = [
  ['Adds', 'add', 'skill'], ['AddsAgent', 'add', 'role'], ['AddsMCP', 'add', 'MCP'], ['AddsHook', 'add', 'hook'],
  ['Updates', 'update', 'skill'], ['UpdatesAgent', 'update', 'role (overwrite)'], ['UpdatesMCP', 'update', 'MCP'], ['UpdatesHook', 'update', 'hook'], ['UpdatesPackage', 'update', 'package'],
  ['Removes', 'remove', 'skill'], ['RemovesAgent', 'remove', 'role'], ['RemovesPackage', 'remove', 'package'],
];
async function openSync() {
  const panel = document.getElementById('sync-panel');
  if (!panel.open) panel.showModal();
  store.preview = null;
  setApply(false, '');
  mount(document.getElementById('sync-body'), h('p', {class: 'loading', text: 'Building the sync plan…'}));
  try {
    store.preview = await api('/api/sync/preview', {method: 'POST', body: '{}'});
    renderPlan();
  } catch (error) {
    mount(document.getElementById('sync-body'), h('div', {class: 'error-box', text: error.message}));
  }
}
function renderPlan() {
  const plan = store.preview.plan || {};
  const body = [];
  const repoState = plan.repo?.State;
  if (repoState && repoState !== 'synced') body.push(h('div', {class: 'plan-agent'}, h('h3', {text: '~/.agents'}), h('ul', {}, h('li', {}, h('span', {class: 'verb update', text: 'link'}), h('span', {text: `${repoState} → ${tilde(plan.repo.ExpectedTarget)}`})))));
  for (const report of plan.reports || []) {
    const items = [];
    if (report.Error) items.push(['note', `config unreadable, skipped: ${report.Error}`]);
    else if (!report.Detected) items.push(['note', 'not installed, skipped']);
    else {
      for (const [key, verb, noun] of PLAN_GROUPS) for (const name of report[key] || []) items.push([verb, `${noun} ${name}`]);
      for (const conflict of report.Conflicts || []) items.push(['conflict', `${tilde(conflict)} (left alone)`]);
      if (report.RootState && report.RootState !== 'synced') items.push([report.RootState === 'missing' ? 'add' : 'update', `root instructions (${report.RootState})`]);
      if (!items.length) items.push(['note', report.Synced ? 'up to date' : 'refresh native config']);
    }
    body.push(h('div', {class: 'plan-agent'}, h('h3', {}, agentMark(report.Name), report.Name), h('ul', {}, items.map(([verb, text]) => h('li', {}, h('span', {class: `verb ${verb}`, text: verb}), h('span', {text}))))));
  }
  const destructive = plan.destructive || [];
  if (destructive.length) {
    body.unshift(h('div', {class: 'destructive'},
      h('strong', {text: `${plural(destructive.length, 'change')} cannot be undone by sync. Tick each one to continue.`}),
      destructive.map((item) => h('label', {}, h('input', {type: 'checkbox', 'aria-label': `Confirm: ${item}`, dataset: {destructive: item}, onchange: updateApply}), h('span', {text: item})))));
  }
  mount(document.getElementById('sync-body'), ...body);
  updateApply();
}
function updateApply() {
  const boxes = [...document.querySelectorAll('[data-destructive]')];
  const ready = boxes.every((box) => box.checked);
  setApply(ready, ready ? (boxes.length ? 'All destructive changes confirmed.' : 'Sync writes only what tackroom manages.') : `${boxes.filter((box) => !box.checked).length} left to confirm.`);
}
function setApply(enabled, note, bad = false) {
  document.getElementById('apply-sync').disabled = !enabled;
  const node = document.getElementById('sync-note');
  node.textContent = note;
  node.className = bad ? 'note bad' : 'note';
}
async function applySync() {
  const preview = store.preview;
  if (!preview) return;
  setApply(false, 'Syncing…');
  try {
    await api('/api/sync/apply', {method: 'POST', body: JSON.stringify({expected_revision: preview.revision, plan_digest: preview.digest, confirmed_destructive: preview.plan?.destructive || []})});
    document.getElementById('sync-panel').close();
    toast('Synced to your agents.');
    await refresh();
  } catch (error) {
    if (error.code === 'sync_plan_changed' || error.code === 'stale_revision') {
      await openSync();
      setApply(false, 'Something changed since the preview. Review the new plan.', true);
      return;
    }
    setApply(true, error.message, true);
  }
}

// ---------- boot ----------
document.getElementById('open-sync').addEventListener('click', openSync);
document.getElementById('close-sync').addEventListener('click', () => document.getElementById('sync-panel').close());
document.getElementById('cancel-sync').addEventListener('click', () => document.getElementById('sync-panel').close());
document.getElementById('apply-sync').addEventListener('click', applySync);
document.getElementById('sync-panel').addEventListener('click', (event) => { if (event.target === event.currentTarget) event.currentTarget.close(); });
document.getElementById('job-form').addEventListener('submit', submitJob);
document.getElementById('close-job').addEventListener('click', closeJobPanel);
document.getElementById('cancel-job').addEventListener('click', closeJobPanel);
document.addEventListener('click', (event) => {
  const menu = document.getElementById('links-menu');
  if (menu.open && !menu.contains(event.target)) menu.open = false;
});
document.getElementById('open-nav').addEventListener('click', (event) => {
  const open = document.body.classList.toggle('nav-open');
  event.currentTarget.setAttribute('aria-expanded', String(open));
});
window.addEventListener('hashchange', route);
route();
refresh();
loadCrons();
