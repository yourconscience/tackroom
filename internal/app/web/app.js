const VIEWS = ['overview', 'skills', 'roles', 'wiring', 'unmanaged', 'crons', 'config'];
// The six views in the sidebar and the phone menu. Unmanaged opens from Overview.
const TABS = [['overview', 'Overview', 'i-overview'], ['skills', 'Skills', 'i-skills'], ['roles', 'Roles', 'i-roles'], ['wiring', 'MCP & hooks', 'i-wiring'], ['crons', 'Crons', 'i-crons'], ['config', 'Config', 'i-config']];
const TITLES = {...Object.fromEntries(TABS.map(([id, label]) => [id, label])), unmanaged: 'Unmanaged'};
const baseURL = new URL(window.location.pathname.endsWith('/') ? window.location.pathname : `${window.location.pathname}/`, window.location.origin);

// What each per-agent state means.
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
const OK = ['i-ok', 'st-ok'];
const WARN = ['i-warn', 'st-warn'];
const FAIL = ['i-fail', 'st-fail'];
const IDLE = ['i-idle', 'st-off'];

const store = {
  view: 'overview',
  inv: null,
  invError: '',
  shared: null,
  local: null,
  updatedAt: 0,
  busy: false,
  skillQuery: '',
  skillFilter: 'all',
  openRows: new Set(),
  folds: {},
  configLayer: 'shared',
  configState: null,
  configLoading: false,
  drafts: {},
  configDiff: '',
  configNote: '',
  preview: null,
  previewAt: 0,
  planChanges: 0,
  crons: null,
  cronsError: '',
  cronsLoading: false,
  cronPatches: [],
  cronBusy: {},
  hosts: null,
  hostsLoading: true,
  hostsError: '',
  scheduleDrafts: {},
  sheet: null,
};

// ---------- helpers ----------
// Children may nest arrays at any depth; null, undefined, false and '' are skipped.
function nodes(children) {
  return children.flat(Infinity)
    .filter((child) => child !== undefined && child !== null && child !== false && child !== '')
    .map((child) => (child instanceof Node ? child : document.createTextNode(String(child))));
}
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
  node.append(...nodes(children));
  return node;
}
function mount(root, ...children) {
  root.replaceChildren(...nodes(children));
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
  const svg = svgNode('svg', {'aria-hidden': 'true', class: cls ? `ic ${cls}` : 'ic'});
  svg.append(svgNode('use', {href: `#${id}`}));
  return svg;
}
// Agent marks from the tackroom site; an agent without one gets its initial.
const AGENT_MARKS = {
  claude: 'a-claude-code', 'claude-code': 'a-claude-code', codex: 'a-codex', droid: 'a-droid', factory: 'a-droid',
  pi: 'a-pi', omp: 'a-omp', qwen: 'a-qwen-code', 'qwen-code': 'a-qwen-code', opencode: 'a-opencode', amp: 'a-amp',
  cursor: 'a-cursor', copilot: 'a-copilot', grok: 'a-grok', dsh: 'a-deepseek',
};
function agentMark(name) {
  const id = AGENT_MARKS[name];
  return id ? icon(id, 'mark') : h('span', {class: 'mark letter', 'aria-hidden': 'true', text: (name || '?')[0].toUpperCase()});
}
function agentByName(name) { return (store.inv?.agents || []).find((agent) => agent.name === name); }
function display(name) { return agentByName(name)?.display_name || name; }
function names(list) {
  if (list.length < 2) return list.join('');
  return `${list.slice(0, -1).join(', ')} and ${list[list.length - 1]}`;
}
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
// The toast is a popover, so it shows above an open sheet; browsers without popovers show it as a plain fixed box.
let toastTimer;
function toast(message, bad = false) {
  const node = document.getElementById('toast');
  node.textContent = message;
  node.className = bad ? 'toast bad' : 'toast';
  node.hidden = false;
  try {
    if (node.showPopover) {
      if (node.matches(':popover-open')) node.hidePopover();
      node.showPopover();
    }
  } catch { /* shown as a plain box */ }
  clearTimeout(toastTimer);
  toastTimer = setTimeout(hideToast, bad ? 6000 : 3200);
}
function hideToast() {
  const node = document.getElementById('toast');
  try { if (node.hidePopover && node.matches(':popover-open')) node.hidePopover(); } catch { /* plain box */ }
  node.hidden = true;
}
function plural(count, word, many = `${word}s`) { return `${count} ${count === 1 ? word : many}`; }
function tokens(count) { return count >= 1000 ? `~${(count / 1000).toFixed(1)}k` : `~${count}`; }
function columns() { return (store.inv?.agents || []).filter((agent) => agent.inspected).map((agent) => agent.name); }
function agentOrder(list) {
  const order = (store.inv?.agents || []).map((agent) => agent.name);
  return [...list].sort((a, b) => {
    const ia = order.indexOf(a); const ib = order.indexOf(b);
    return (ia < 0 ? 999 : ia) - (ib < 0 ? 999 : ib) || a.localeCompare(b);
  });
}
function liveAgents() { return (store.inv?.agents || []).filter((agent) => agent.enabled && agent.inspected && agent.detected && !agent.error); }
function hero([iconId, cls], title, text) {
  return h('div', {class: 'hero'}, icon(iconId, cls), h('h1', {text: title}), text ? h('p', {}, text) : null);
}
// A switch: a button with role=switch, its label and one line under it.
function switchEl(on, label, sub, onpick, disabled = false) {
  return h('button', {class: 'sw', type: 'button', role: 'switch', 'aria-checked': String(!!on), disabled: disabled || store.busy || undefined,
    onclick: (event) => { event.currentTarget.setAttribute('aria-checked', String(!on)); onpick(!on); }},
  h('span', {class: 'sw-text'}, h('span', {class: 'sw-label'}, label), sub ? h('span', {class: 'sw-sub', text: sub}) : null),
  h('span', {class: 'sw-track', 'aria-hidden': 'true'}, h('span', {class: 'sw-knob'})));
}
function copyButton(text) {
  return h('button', {class: 'copy', type: 'button', 'aria-label': `Copy ${text}`, title: `Copy: ${text}`, onclick: async () => {
    try { await navigator.clipboard.writeText(text); toast('Copied to the clipboard.'); }
    catch { toast(text); }
  }}, icon('i-copy'));
}
// A command to run in a terminal, with a copy button.
function command(text) { return h('div', {class: 'cmd'}, h('code', {text}), copyButton(text)); }
function agentChip(name, cls = '', word = '') {
  return h('span', {class: cls ? `chip ${cls}` : 'chip'}, agentMark(name), display(name), word ? h('span', {class: 'state', text: word}) : null);
}
function syncButton(label = 'Sync') {
  return h('button', {type: 'button', class: 'btn primary', onclick: openSync}, icon('i-sync'), label);
}
function linkButton(href, text) { return h('a', {class: 'btn', href, text}); }

// ---------- sheets ----------
// Dialogs that slide up on a phone and in from the right from 900px. showModal keeps focus inside and Escape closes.
const closeTimers = {};
function openDialog(dialog) {
  clearTimeout(closeTimers[dialog.id]);
  if (!dialog.open) dialog.showModal();
  document.body.classList.add('sheet-open');
  void dialog.offsetHeight; // start the slide from off screen
  dialog.classList.add('in');
}
function closeDialog(dialog, instant = false) {
  if (!dialog.open) return;
  dialog.classList.remove('in');
  clearTimeout(closeTimers[dialog.id]);
  if (instant || window.matchMedia('(prefers-reduced-motion: reduce)').matches) dialog.close();
  else closeTimers[dialog.id] = setTimeout(() => dialog.close(), 220);
}
function sheetOpen(id) { return document.getElementById(id).open; }

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
  document.querySelectorAll('.sw, button.chip').forEach((node) => { node.disabled = true; });
  // The plan on screen no longer matches the config: Apply stays off until the new plan arrives.
  if (sheetOpen('sync-panel')) {
    planSeq += 1;
    store.preview = null;
    store.planChanges = 0;
    setApply(false, 'Saving the change, then building the plan again…');
  }
  try {
    await api('/api/config', {method: 'PATCH', body: JSON.stringify({layer, expected_revision: state.revision, operations: [{op: 'set', path: `/${section}/${name}/${field}`, value}]})});
    toast(`${message} Saved to ${layer === 'local' ? 'tackroom.local.yaml' : 'tackroom.yaml'}; Sync to apply it.`);
  } catch (error) {
    toast(error.code === 'stale_revision' ? 'The config changed on disk. Reloaded the latest version; try again.' : error.message, true);
  } finally {
    store.busy = false;
    await refresh();
    // The plan in an open Sync sheet is stale after any config change.
    if (sheetOpen('sync-panel')) loadPlan();
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
  store.updatedAt = Date.now();
  // Drop the cached Config layer so it reloads with the new revision; an
  // unsaved draft keeps its base, so saving it reports the conflict instead.
  if (!store.drafts[store.configLayer]) store.configState = null;
  renderChrome();
  renderView();
  renderToggles();
}
async function loadCrons(refreshNow = false) {
  store.cronsLoading = true;
  if (store.view === 'crons') renderView();
  const started = Date.now();
  try {
    const data = await api(`/api/crons${refreshNow ? '?refresh=1' : ''}`);
    // A row changed after this collect began is newer than what it read.
    for (const item of store.cronPatches.filter((entry) => entry.at >= started)) data.jobs = replaceCronRow(data.jobs || [], item.matches, item.fresh);
    store.crons = data;
    store.cronsError = '';
  } catch (error) {
    store.cronsError = error.message;
  }
  store.cronsLoading = false;
  renderChrome();
  if (store.view === 'crons' || store.view === 'overview') renderView();
  renderSheet();
}
async function loadHosts(refreshNow = false) {
  try {
    store.hosts = await api(`/api/hosts${refreshNow ? '?refresh=1' : ''}`);
    store.hostsError = '';
  } catch (error) {
    // Overview shows the failure with Retry: it never reads "All clear" with the machines unchecked.
    store.hostsError = error.message;
  }
  store.hostsLoading = false;
  if (store.view === 'overview') renderView();
}

// ---------- chrome ----------
// owner/repo of a source URL in any form (https, ssh://, git@host:), as tackroom names a skill's origin.
function sourceRepo(row) {
  let rest = String(row.source || '').trim();
  if (rest.includes('://')) rest = rest.slice(rest.indexOf('://') + 3);
  rest = rest.replace(/^git@/, '');
  if (rest.includes(':')) rest = rest.slice(rest.indexOf(':') + 1);
  const parts = rest.replace(/\/$/, '').replace(/\.git$/, '').split('/');
  return parts.length >= 2 ? parts.slice(-2).join('/') : rest;
}
// The lock row of the source an outside skill comes from, when that source is blocked or in cooldown.
function sourceLock(skill) {
  if (skill.origin === 'local') return null;
  const repo = skill.origin.split('@')[0];
  return (store.inv?.lock || []).find((row) => (row.state === 'blocked' || row.state === 'cooldown') && sourceRepo(row) === repo) || null;
}
function blockedSource(skill) { const lock = sourceLock(skill); return lock?.state === 'blocked' ? lock : null; }
// The states that count against a skill. A skill of a source in cooldown stays with the rest, as on Overview
// and in the Herdr panel: sync waits for the cooldown on its own, so only a conflict or an unreadable config counts.
function skillStates(skill) {
  if (!sourceLock(skill)) return skill.states;
  return Object.fromEntries(Object.entries(skill.states).filter(([, state]) => state === 'conflict' || state === 'error'));
}
function skillNeedsAttention(skill) { return Boolean(blockedSource(skill)) || Object.values(skillStates(skill)).some((state) => ATTENTION.has(state)); }
// What needs attention on Skills: its skills, and blocked sources no skill here comes from yet.
// The headline, the filter chip and the sidebar badge all count this.
function skillAsks() {
  const inv = store.inv;
  if (!inv) return {skills: [], orphans: [], fail: 0};
  const skills = inv.skills.filter(skillNeedsAttention);
  const orphans = inv.lock.filter((row) => row.state === 'blocked' && !inv.skills.some((skill) => blockedSource(skill) === row));
  const fail = skills.filter((skill) => Object.values(skillStates(skill)).some((state) => state === 'conflict' || state === 'error')).length;
  return {skills, orphans, fail};
}
// Where a skill, role, MCP server or hook is wrong, as "is missing in 12 harnesses and has drifted in Pi".
const STATE_PHRASES = {missing: 'is missing in', drift: 'has drifted in', conflict: 'has a conflict in', error: 'cannot be checked in', unknown: 'is in an unknown state in'};
function whereWrong(states) {
  const visible = new Set(columns());
  const by = {};
  for (const [agent, state] of Object.entries(states)) if (visible.has(agent) && ATTENTION.has(state)) (by[state] = by[state] || []).push(agent);
  return Object.keys(STATE_PHRASES).filter((state) => by[state]).map((state) => {
    const list = agentOrder(by[state]);
    return `${STATE_PHRASES[state]} ${list.length <= 2 ? names(list.map(display)) : plural(list.length, 'harness', 'harnesses')}`;
  }).join(' and ') || 'needs attention';
}
// The rows that need attention, named in a view's lead: three at most, then how many more.
function attentionLead(rows) {
  return [...rows.slice(0, 3).map((row) => `${row.name} ${whereWrong(row.states)}.`), rows.length > 3 ? `${rows.length - 3} more need attention too.` : ''].filter(Boolean).join(' ');
}
function needsAttention(row) { return Object.values(row.states).some((state) => ATTENTION.has(state)); }
function syncCounts() {
  const agents = store.inv?.agents || [];
  return {
    pending: agents.reduce((sum, agent) => sum + (agent.inspected ? agent.pending : 0), 0),
    unsynced: agents.filter((agent) => agent.inspected && agent.detected && !agent.error && !agent.synced).length,
  };
}
// A badge counts what fails, in red, else what needs attention, in amber: the same things the view lists.
function setBadges(kind, count, title, warn) {
  document.querySelectorAll(`[data-badge="${kind}"]`).forEach((node) => {
    node.textContent = count ? String(count) : '';
    node.title = title;
    node.className = warn ? 'badge warn' : 'badge';
    node.hidden = !count;
  });
}
function configLinks() { return pick(store.shared?.effective_ui, 'Links', 'links') || []; }
function linkNodes() {
  return configLinks().map((link) => h('a', {href: pick(link, 'URL', 'url'), target: '_top'}, icon('i-out'), h('span', {class: 'label', text: pick(link, 'Name', 'name')})));
}
function renderChrome() {
  const inv = store.inv;
  const asks = skillAsks();
  const skills = asks.skills.length + asks.orphans.length;
  setBadges('skills', skills, `${skills} need${skills === 1 ? 's' : ''} attention`, !asks.fail);
  const data = store.crons;
  const cronsFail = data ? data.jobs.filter((job) => job.health === 'fail').length + downHosts(data).length : 0;
  const cronsWarn = data ? data.jobs.filter((job) => job.health === 'warn').length + unreadableSources(data).length + (store.cronsError ? 1 : 0) : 0;
  setBadges('crons', cronsFail || cronsWarn, cronsFail ? `${cronsFail} failing` : `${cronsWarn} need attention`, !cronsFail);
  const {pending, unsynced} = syncCounts();
  document.getElementById('sync-label').textContent = pending > 0 ? `Review sync · ${pending}` : unsynced > 0 ? 'Review sync' : 'Sync';
  const dockBadge = document.getElementById('dock-sync-badge');
  dockBadge.textContent = pending > 0 ? String(pending) : '';
  dockBadge.title = `${plural(pending, 'change')} waiting to sync`;
  dockBadge.hidden = !(pending > 0);
  const links = document.getElementById('links');
  mount(links, linkNodes());
  links.hidden = configLinks().length === 0;
  const stamp = document.getElementById('stamp');
  stamp.textContent = store.updatedAt ? `Updated ${clock(store.updatedAt)}` : '';
  stamp.title = [store.shared?.paths?.shared && `Config ${tilde(store.shared.paths.shared)}`, inv?.revision && `revision ${inv.revision.slice(0, 12)}`].filter(Boolean).join(', ');
}

function route() {
  const next = window.location.hash.replace('#', '');
  store.view = VIEWS.includes(next) ? next : 'overview';
  document.querySelectorAll('.view').forEach((node) => { node.hidden = node.dataset.view !== store.view; });
  document.querySelectorAll('.side-nav a[data-view]').forEach((node) => {
    if (node.dataset.view === store.view) node.setAttribute('aria-current', 'page');
    else node.removeAttribute('aria-current');
  });
  document.querySelectorAll('[data-dock]').forEach((node) => {
    const current = node.dataset.dock === store.view || (node.dataset.dock === 'menu' && !['overview', 'crons'].includes(store.view));
    if (current) node.setAttribute('aria-current', 'page');
    else node.removeAttribute('aria-current');
  });
  document.title = `${TITLES[store.view]} - tackroom`;
  if (store.view === 'crons' && !store.crons && !store.cronsLoading) loadCrons();
  if (store.sheet?.kind === 'menu') closeDialog(document.getElementById('sheet'), true);
  renderView();
}

function renderView() {
  const root = document.getElementById(`view-${store.view}`);
  if (store.view === 'config') { renderConfig(root); return; }
  if (store.view === 'crons') { renderCrons(root); return; }
  if (store.view === 'overview') { renderOverview(root); return; }
  if (!store.inv) {
    mount(root, store.invError
      ? [hero(FAIL, 'Could not inspect your agents', [store.invError, ' Fix the config in ', h('a', {href: '#config', text: 'Config'}), '.'])]
      : hero(IDLE, 'Inspecting your agents…'));
    return;
  }
  ({skills: renderSkills, roles: renderRoles, wiring: renderWiring, unmanaged: renderUnmanaged})[store.view](root);
}

// Each view's explanation, under its headline.
const HELP = {
  skills: 'Every canonical skill goes to every enabled agent. Sync fixes drifted and missing links.',
  roles: 'Agent roles from agents/*.md in your config root, rendered into each agent that supports subagents.',
  wiring: 'Turn an entry on or off, and select a harness to add or remove it. Each change saves to your config right away; Sync writes it to the agents.',
  unmanaged: "Things in your agents' folders that tackroom did not put there. tackroom never deletes them; copy the promote command to manage one.",
  crons: 'Scheduled jobs and services on every host in crons.hosts (tackroom.local.yaml). Add cron declares a job tackroom owns; Edit and Delete change only those. The CLI shows the same: tackroom cron collect --table.',
  config: 'Shared is tackroom.yaml in your config root; Local overrides it on this machine only; Effective is what sync uses.',
  sources: 'Skills and packages from outside your config root. Sync pins each source to a commit in tackroom.lock, audits it, and puts anything published in the last 72 hours in cooldown. Nothing in cooldown or blocked reaches your agents until the cooldown ends or is skipped, or the finding is accepted.',
};

// ---------- overview: one answer, then what fails, what needs attention and everything else ----------
function harnessState(agent) {
  const c = agent.counts || {};
  if (!agent.enabled) return {st: 'off', word: 'turned off'};
  if (!agent.inspected) return {st: 'off', word: 'not inspected'};
  if (agent.error) return {st: 'fail', word: 'config unreadable'};
  if (!agent.detected) return {st: 'off', word: 'not installed'};
  if (c.conflicts) return {st: 'fail', word: plural(c.conflicts, 'conflict')};
  if (agent.pending > 0) return {st: 'warn', word: `${agent.pending} pending`};
  if (!agent.synced || c.drifted || c.missing || (agent.root_state && agent.root_state !== 'synced')) return {st: 'warn', word: 'needs sync'};
  return {st: 'ok', word: 'synced'};
}
function harnessDetail(agent) {
  const c = agent.counts || {};
  return [
    c.drifted && plural(c.drifted, 'drifted link'),
    c.missing && `${plural(c.missing, 'skill')} not linked`,
    agent.pending > 0 && `${plural(agent.pending, 'change')} waiting`,
    agent.root_state && agent.root_state !== 'synced' && `root instructions ${agent.root_state}`,
  ].filter(Boolean).join(', ');
}
// One row of Failing or Needs attention: a title, what is wrong, and its fix.
function issue(title, on, body, acts = []) {
  const buttons = nodes([acts]);
  return h('article', {class: 'item'},
    h('h3', {}, title, on ? h('span', {text: ` ${on}`}) : null),
    body,
    buttons.length ? h('div', {class: 'acts'}, buttons) : null);
}
function harnessItem(agent, state) {
  const where = agent.skill_root ? h('p', {class: 'meta', text: tilde(agent.skill_root)}) : null;
  if (agent.error) return issue(display(agent.name), state.word, [h('p', {text: tilde(agent.error)}), where], [linkButton('#config', 'Open Config')]);
  return issue(display(agent.name), state.word, [h('p', {text: 'A native file differs from what tackroom would write, so sync skips it.'}), where], [linkButton('#unmanaged', 'See conflicts')]);
}
function noAgentsItem() {
  return issue('No agents in your config', '', [
    h('p', {text: 'tackroom.yaml lists no agents, so sync has nothing to write to. Setup finds the agents installed on this machine, adds them to tackroom.yaml and syncs them.'}),
    command('tackroom setup'),
    h('p', {class: 'meta'}, 'Or add them by hand in ', h('a', {href: '#config', text: 'Config'}), '.')]);
}
// The machines of `tackroom hosts`, only with more than one host in crons.hosts.
function machines() {
  const hosts = store.hosts?.hosts || [];
  return hosts.length > 1 ? hosts : [];
}
const MACHINE_WORDS = {ok: 'healthy', warn: 'needs attention', fail: 'failing'};
// The command that fixes a machine: `tackroom hosts`'s own, or a look at the uncommitted changes it counts.
function machineFix(host) {
  if (host.fix) return host.fix;
  if (!(host.dirty > 0 && host.config_root)) return '';
  const root = host.host === 'local' ? tilde(host.config_root) : host.config_root;
  const cmd = `git -C ${shellWord(root)} status`;
  if (host.host === 'local') return cmd;
  // Quoted for the remote shell, so a space or a ~ in the path reaches it intact.
  return `ssh ${host.host} ${shellWord(root) === root && !root.startsWith('~') ? cmd : `'${cmd.replace(/'/g, "'\\''")}'`}`;
}
// One failing machine is a row of its own; the machines that need attention share one row,
// titled with their summary when they share it.
function machineItem(list) {
  const many = list.length > 1;
  const same = many && new Set(list.map((host) => host.summary)).size === 1;
  const title = !many ? hostName(list[0].host) : same ? sentence((list[0].summary || '').replace(/^1 /, 'One ')) : 'Machines need attention';
  const on = !many ? MACHINE_WORDS[list[0].health] || list[0].health : `on ${names(list.map((host) => hostName(host.host)))}`;
  return issue(title, on, [
    same ? null : list.map((host) => h('p', {text: `${many ? `${hostName(host.host)}: ` : ''}${sentence(host.summary || '')}${/\.$/.test(host.summary || '') ? '' : '.'}`})),
    list.filter(machineFix).map((host) => [many ? h('p', {class: 'meta', text: `${hostName(host.host)}:`}) : null, command(machineFix(host))]),
  ]);
}
// A pending sync is one row: the changes waiting, the harnesses that need it and the config root link.
function syncItem(outOfSync) {
  const inv = store.inv;
  const {pending} = syncCounts();
  const total = (key) => outOfSync.reduce((sum, agent) => sum + ((agent.counts || {})[key] || 0), 0);
  const what = [total('missing') && `${total('missing')} missing`, total('drifted') && `${total('drifted')} drifted`].filter(Boolean).join(', ');
  const link = inv.repo_state && inv.repo_state !== 'synced' ? `The config root link is ${inv.repo_state}` : '';
  const title = pending > 0 ? `${plural(pending, 'change')} waiting to sync` : outOfSync.length ? `${plural(outOfSync.length, 'harness', 'harnesses')} out of sync` : link;
  return issue(title, '', [
    outOfSync.length ? h('p', {text: `${names(outOfSync.map((agent) => display(agent.name)))} ${outOfSync.length === 1 ? 'needs' : 'need'} it${what ? `: ${what}` : ''}.`}) : null,
    new Set(outOfSync.map(harnessDetail)).size > 1 ? outOfSync.map((agent) => (harnessDetail(agent) ? h('p', {class: 'meta', text: `${display(agent.name)}: ${harnessDetail(agent)}`}) : null)) : null,
    link && outOfSync.length ? h('p', {class: 'meta', text: `${link}.`}) : null,
    h('p', {class: 'meta', text: 'Sync shows the plan before it changes anything.'}),
  ], [syncButton('Sync')]);
}
// A source the audit blocked, with the command that accepts the finding.
function blockedItem(row) {
  return issue(row.name, 'blocked', [
    h('p', {text: `${sentence(row.reason || 'blocked by the audit')}${/\.$/.test(row.reason || '') ? '' : '.'}`}),
    row.command ? [h('p', {class: 'meta', text: 'Read the finding with tackroom doctor audit; to sync it anyway, accept it:'}), command(row.command)] : null,
    row.source ? h('p', {class: 'meta', text: [row.source.replace(/^https:\/\//, ''), row.commit && !row.pinned ? `new ${row.commit}` : ''].filter(Boolean).join(', ')}) : null]);
}
// A cooldown as "owner/repo@4b4c947 · 37h left of 72h"; tackroom's own reason when there is no end time.
function cooldownLine(row) {
  const until = Date.parse(row.until);
  if (!until) return `${row.name}: ${row.reason || row.state}`;
  const ref = /^(git|npm|pypi):(\S+)@(\S+)/.exec(row.reason || '');
  const source = ref ? (ref[1] === 'git' ? (/([^/:]+\/[^/]+?)(?:\.git)?$/.exec(ref[2])?.[1] ?? ref[2]) : ref[2])
    : row.source ? row.source.replace(/^https:\/\/github\.com\//, '').replace(/\.git$/, '') : row.name;
  const version = ref ? (ref[1] === 'git' ? ref[3].slice(0, 7) : ref[3]) : (row.commit || '').slice(0, 7);
  const published = Date.parse(/ published (\S+)/.exec(row.reason || '')?.[1]);
  const left = Math.max(0, Math.ceil((until - Date.now()) / 3600000));
  const of = published && until > published ? ` of ${Math.round((until - published) / 3600000)}h` : '';
  return `${source}${version ? `@${version}` : ''} · ${left}h left${of}`;
}
function sourceWhy(row) {
  const until = Date.parse(row.until);
  return {
    cooldown: until ? 'Sync skips it until then and your agents keep what they have. To take it now:' : `In cooldown: ${row.reason}. To take it anyway:`,
    blocked: `Sync skips it: ${row.reason}. Read the finding with tackroom doctor audit; to sync it anyway, accept it:`,
  }[row.state] || `${sentence(row.reason || row.state)}. Fix it with:`;
}
function cronHosts(data) { return [...new Set([...(data.access || []).map((entry) => entry.host), ...(data.jobs || []).map((job) => job.host)])]; }
function hostAccess(host, data) { return (data.access || []).filter((entry) => entry.host === host); }
function unreachable(host, data) {
  const access = hostAccess(host, data);
  return access.length > 0 && access.every((entry) => entry.state === 'not_scanned') && access.some((entry) => (entry.detail || '').startsWith('probe failed'));
}
function downHosts(data) { return cronHosts(data).filter((host) => unreachable(host, data)); }
function unreadableSources(data) { return (data.access || []).filter((entry) => entry.state === 'unreadable'); }
function unreadableItem(entry) {
  return issue(`${entry.source} on ${hostName(entry.host)}`, 'unreadable', [
    h('p', {text: `${sentence(entry.detail || 'tackroom could not read it')}${/\.$/.test(entry.detail || '') ? '' : '.'}`}),
    h('p', {class: 'meta', text: 'Its jobs are not in this list.'})],
  [h('button', {type: 'button', class: 'btn', onclick: () => loadCrons(true), text: 'Collect again'})]);
}
// A collect that failed while older rows stay on screen.
function staleCronsItem() {
  const at = Date.parse(store.crons?.scanned_at);
  return loadErrorItem('Crons did not refresh', `${store.cronsError}${at ? ` The jobs shown are from ${clock(at)}.` : ''}`, () => loadCrons(true));
}
function unreachableItem(host, data) {
  const detail = hostAccess(host, data).find((entry) => (entry.detail || '').startsWith('probe failed'))?.detail || 'probe failed';
  return issue(`${hostName(host)} unreachable`, '', [h('p', {text: `${sentence(detail)}.`}), h('p', {class: 'meta', text: 'Its scheduled jobs are not in this list.'})],
    [h('button', {type: 'button', class: 'btn', onclick: () => loadCrons(true), text: 'Collect again'})]);
}
function loadErrorItem(title, message, retry, extra = null) {
  return issue(title, '', [h('p', {text: message})], [h('button', {type: 'button', class: 'btn', onclick: retry, text: 'Retry'}), extra]);
}
// "12 healthy, 2 paused, 1 not run yet" for the jobs that neither fail nor need attention.
function restCounts(jobs) {
  const count = (health) => jobs.filter((job) => job.health === health).length;
  return [count('ok') && `${count('ok')} healthy`, count('paused') && `${count('paused')} paused`, count('idle') && `${count('idle')} not run yet`].filter(Boolean).join(', ');
}
const FAIL_KINDS = {job: ['a cron', 'crons'], harness: ['a harness', 'harnesses'], machine: ['a machine', 'machines']};

// What the Overview answers, from every source; the headline and the rows come from here, so they cannot disagree.
// Failing: a failing cron, a harness with a conflict or an unreadable config, a failing machine, a host the
// crons collect could not reach. Needs attention: a cron whose last run failed, a sync waiting, the machines
// that need attention (one row for them all), a source the audit blocked, and a source that did not load.
// The same groups as the Herdr panel.
function overviewModel() {
  const inv = store.inv;
  const data = store.crons;
  const jobs = data?.jobs || [];
  const hosts = machines();
  const sick = hosts.filter((host) => host.health !== 'ok' && host.health !== 'fail');
  const agents = inv?.agents || [];
  const outOfSync = agents.filter((agent) => harnessState(agent).st === 'warn');
  const syncWaiting = Boolean(inv) && (outOfSync.length > 0 || Boolean(inv.repo_state && inv.repo_state !== 'synced'));
  const fail = [
    ...jobs.filter((job) => job.health === 'fail').sort(byLastRun).map((job) => ['job', jobItem(job)]),
    ...agents.filter((agent) => harnessState(agent).st === 'fail').map((agent) => ['harness', harnessItem(agent, harnessState(agent))]),
    ...hosts.filter((host) => host.health === 'fail').map((host) => ['machine', machineItem([host])]),
    ...(data ? downHosts(data) : []).filter((host) => !hosts.some((item) => item.host === host && item.health !== 'ok')).map((host) => ['machine', unreachableItem(host, data)]),
  ];
  const warn = [
    ...jobs.filter((job) => job.health === 'warn').sort(byLastRun).map((job) => ['job', jobItem(job)]),
    ...(inv && !agents.length ? [['harness', noAgentsItem()]] : []),
    ...(syncWaiting ? [['sync', syncItem(outOfSync)]] : []),
    ...(sick.length ? [['machine', machineItem(sick)]] : []),
    ...(inv?.lock || []).filter((row) => row.state === 'blocked').map((row) => ['source', blockedItem(row)]),
    ...(!inv && store.invError ? [['error', loadErrorItem('The inventory did not load', store.invError, refresh, linkButton('#config', 'Open Config'))]] : []),
    ...(data ? unreadableSources(data) : []).map((entry) => ['job', unreadableItem(entry)]),
    ...(!data && store.cronsError ? [['error', loadErrorItem('Crons did not load', store.cronsError, () => loadCrons(true))]] : []),
    ...(data && store.cronsError ? [['error', staleCronsItem()]] : []),
    ...(store.hostsError ? [['error', loadErrorItem(store.hosts ? 'Machines did not refresh' : 'Machines did not load', store.hostsError, () => loadHosts(true))]] : []),
  ];
  const pending = [!inv && !store.invError && 'harnesses', !data && !store.cronsError && 'crons', store.hostsLoading && 'machines'].filter(Boolean);
  return {fail, warn, pending, syncWaiting};
}

function renderOverview(root) {
  const inv = store.inv;
  const {fail, warn, pending, syncWaiting} = overviewModel();
  const title = [fail.length ? `${fail.length} failing` : '', warn.length ? `${warn.length} need${warn.length === 1 ? 's' : ''} attention` : ''].filter(Boolean).join(', ')
    || (pending.length ? 'Checking…' : 'All clear');
  // The line under it: whether the harnesses are in sync, what the failures are when they are all one kind,
  // and what is still loading.
  const live = (inv?.agents || []).filter((agent) => agent.enabled && agent.inspected && agent.detected);
  const kinds = [...new Set(fail.map(([kind]) => kind))];
  const [one, many] = kinds.length === 1 ? FAIL_KINDS[kinds[0]] : [];
  const said = [
    inv && !syncWaiting && live.length && live.every((agent) => harnessState(agent).st === 'ok') ? (live.length === 1 ? 'The installed harness is in sync.' : `All ${live.length} installed harnesses are in sync.`) : '',
    one ? (fail.length === 1 ? `The failure is ${one}.` : `${fail.length === 2 ? 'Both failures' : 'The failures'} are ${many}.`) : '',
    pending.length ? `Still checking ${names(pending)}.` : '',
  ].filter(Boolean).join(' ');
  mount(root,
    hero(fail.length ? FAIL : warn.length ? WARN : pending.length ? IDLE : OK, title, said),
    h('div', {class: 'cols'},
      fail.length || warn.length ? h('section', {},
        fail.length ? [h('h2', {class: 'gh st-fail'}, 'Failing'), h('div', {class: 'items'}, fail.map(([, node]) => node))] : null,
        warn.length ? [h('h2', {class: 'gh st-warn'}, 'Needs attention'), h('div', {class: 'items'}, warn.map(([, node]) => node))] : null) : null,
      h('section', {},
        h('h2', {class: 'gh'}, 'Everything else'),
        h('ul', {class: 'rest'}, everythingElse(syncWaiting)))));
}
// One line of Everything else: a mark, a title and the lines under it. It opens a view, opens Sync,
// or folds open in place.
function restLine(target, iconPair, title, subs, extra, off = false) {
  const inner = [icon(...iconPair), h('span', {}, h('b', {text: title}), [subs].flat().filter(Boolean).map((sub) => h('span', {class: 'sub', text: sub})), extra)];
  const node = typeof target === 'string' ? h('a', {href: target}, inner)
    : typeof target === 'function' ? h('button', {type: 'button', onclick: target}, inner)
      : target ? h('details', {}, h('summary', {}, inner), h('div', {class: 'rest-detail'}, target)) : h('div', {}, inner);
  return h('li', {class: off ? 'off' : null}, node);
}
// What is healthy or off, folded to one line per kind.
function everythingElse(syncWaiting) {
  const inv = store.inv;
  const lines = [];
  const ok = (inv?.agents || []).filter((agent) => harnessState(agent).st === 'ok');
  if (ok.length) {
    lines.push(restLine(openSync, OK, `${plural(ok.length, 'harness', 'harnesses')} in sync${syncWaiting ? '' : ', nothing to sync'}`, [
      [plural(inv.skills.length, 'skill'), plural(inv.roles.length, 'role'), plural(inv.mcp.length, 'MCP server'), plural(inv.hooks.length, 'hook')].join(', '),
      ok.map((agent) => display(agent.name)).join(', '),
    ], h('span', {class: 'sub link', text: 'Open Sync'})));
  }
  const jobs = store.crons?.jobs || [];
  const healthy = jobs.filter((job) => job.health === 'ok').length;
  const quiet = jobs.filter((job) => job.health === 'paused' || job.health === 'idle');
  if (healthy || quiet.length) lines.push(restLine('#crons', healthy ? OK : ['i-pause', 'st-off'], healthy ? `${plural(healthy, 'cron')} healthy` : plural(quiet.length, 'cron'), restCounts(quiet), null, !healthy));
  const hosts = machines();
  if (hosts.length) {
    const commits = new Set(hosts.map((host) => host.commit));
    const same = commits.size === 1 && hosts[0].commit;
    const versions = new Map();
    for (const host of hosts) if (host.version) versions.set(host.version, [...(versions.get(host.version) || []), hostName(host.host)]);
    lines.push(restLine(null, same ? OK : ['i-laptop', 'st-off'], same ? `${hosts.length === 2 ? 'Both' : `All ${hosts.length}`} machines on config ${hosts[0].commit.slice(0, 7)}` : plural(hosts.length, 'machine'),
      [...versions].map(([version, list]) => `tackroom ${version} on ${names(list)}`).join('; ')));
  }
  if (inv) {
    const pinned = inv.lock.filter((row) => row.state === 'pinned');
    if (pinned.length) {
      const findings = pinned.filter((row) => row.findings).map((row) => `${row.name} has ${plural(row.findings, 'audit finding')}, ${row.critical ? `${row.critical} critical` : 'none critical'}`);
      const unaudited = pinned.filter((row) => !row.audited).length;
      lines.push(restLine('#skills', OK, `${plural(pinned.length, 'outside source')} pinned`, [...findings, unaudited ? `${unaudited} not audited yet` : ''].filter(Boolean).join('; ') || 'No audit findings'));
    }
    const cool = inv.lock.filter((row) => row.state !== 'pinned' && row.state !== 'blocked');
    if (cool.length) {
      lines.push(restLine(cool.map((row) => h('div', {},
        h('b', {text: row.name}),
        Date.parse(row.until) ? h('p', {class: 'meta', text: `Cooldown ends ${weekdayClock(Date.parse(row.until))}.`}) : null,
        h('p', {class: 'meta', text: sourceWhy(row)}),
        row.command ? command(row.command) : null)), ['i-timer', 'st-off'], `${cool.length} in cooldown`, cool.map(cooldownLine), null, true));
    }
    const unmanaged = inv.unmanaged.filter((item) => item.kind !== 'conflict');
    if (unmanaged.length) lines.push(restLine('#unmanaged', ['i-unmanaged', 'st-off'], `${plural(unmanaged.length, 'item')} tackroom does not manage`, 'In agent folders; tackroom leaves them alone.', null, true));
    const absent = inv.agents.filter((agent) => agent.enabled && agent.inspected && !agent.detected);
    const off = inv.agents.filter((agent) => !agent.enabled);
    if (absent.length || off.length) {
      lines.push(restLine(openSync, ['i-minus', 'st-off'], [absent.length ? `${absent.length} not installed` : '', off.length ? `${off.length} turned off` : ''].filter(Boolean).join(', '),
        [...absent, ...off].map((agent) => display(agent.name)).join(', '), h('span', {class: 'sub link', text: 'Turn on or off under Sync'}), true));
    }
  }
  return lines;
}

// ---------- skills ----------
const STATE_LABELS = {drift: 'Drifted in', missing: 'Missing in', conflict: 'Conflict in', error: 'Config unreadable in', unknown: 'Unknown in'};
function renderSkills(root) {
  const inv = store.inv;
  const live = liveAgents();
  const skills = inv.skills;
  // A blocked source with no skill here yet still needs attention, as on Overview.
  const {skills: attention, orphans} = skillAsks();
  const blocked = inv.lock.filter((row) => row.state === 'blocked');
  const asks = attention.length + orphans.length;
  const yours = skills.filter((skill) => skill.origin === 'local');
  const outside = skills.filter((skill) => skill.origin !== 'local');
  const sources = new Set(outside.map((skill) => skill.origin.split('@')[0]));
  const allOk = skills.every((skill) => live.every((agent) => skill.states[agent.name] === 'ok'));
  const listing = live.find((agent) => agent.tokens)?.tokens;
  const title = asks ? `${[attention.length && plural(attention.length, 'skill'), orphans.length && plural(orphans.length, 'outside source')].filter(Boolean).join(' and ')} need${asks === 1 ? 's' : ''} attention`
    : !skills.length ? 'No skills yet'
      : allOk ? `${skills.length} skills, in place everywhere` : `${plural(skills.length, 'skill')}, none needs attention`;
  const fromBlocked = blocked.map((row) => {
    const from = skills.filter((skill) => blockedSource(skill) === row).map((skill) => skill.name);
    return from.length ? `${names(from)} come${from.length === 1 ? 's' : ''} from ${sourceRepo(row) || row.name}, which the audit blocked.` : `${sourceRepo(row) || row.name} is blocked by the audit.`;
  }).join(' ');
  const said = [
    fromBlocked,
    attentionLead(attention.filter((skill) => !blockedSource(skill)).map((skill) => ({name: skill.name, states: skillStates(skill)}))),
    !skills.length ? 'No canonical skills yet. Add one with tackroom skill new <name>.' : '',
    skills.length && allOk && live.length ? `Every skill is linked in all ${live.length} installed harnesses.` : '',
    skills.length ? `${yours.length} ${yours.length === 1 ? 'is' : 'are'} yours and ${outside.length} ${outside.length === 1 ? 'comes' : 'come'} from ${plural(sources.size, 'outside source')}.` : '',
    listing && skills.length ? `Their listings cost about ${tokens(listing).slice(1)} tokens in each harness.` : '',
    skills.length ? HELP.skills : '',
  ].filter(Boolean).join(' ');
  const list = h('div', {class: 'list'});
  const counts = {all: skills.length, attention: asks, local: yours.length, external: outside.length};
  const filters = h('div', {class: 'filters', role: 'group', 'aria-label': 'Filter skills'}, [['all', 'All'], ['attention', 'Needs attention'], ['local', 'Yours'], ['external', 'Outside']].map(([value, label]) =>
    h('button', {type: 'button', 'aria-pressed': String(value === store.skillFilter), onclick: (event) => {
      store.skillFilter = value;
      filters.querySelectorAll('button').forEach((node) => node.setAttribute('aria-pressed', String(node === event.currentTarget)));
      drawSkills(list);
    }}, label, h('span', {class: 'mono', text: String(counts[value])}))));
  mount(root,
    hero(asks ? WARN : skills.length ? OK : IDLE, title, said),
    skills.length ? h('div', {class: 'tools'},
      h('label', {class: 'search'}, icon('i-search'), h('input', {type: 'search', 'aria-label': 'Search skills', placeholder: `Search ${skills.length} skills`, value: store.skillQuery, oninput: (event) => { store.skillQuery = event.target.value; drawSkills(list); }})),
      filters) : null,
    list,
    h('div', {class: 'list'}, sourcesSection(inv.lock.filter((row) => row.state !== 'blocked'))));
  drawSkills(list);
}
function drawSkills(list) {
  const inv = store.inv;
  const query = store.skillQuery.trim().toLowerCase();
  const filter = store.skillFilter;
  const match = (skill) => (!query || `${skill.name} ${skill.description} ${skill.origin}`.toLowerCase().includes(query))
    && (filter === 'all' || (filter === 'attention' && skillNeedsAttention(skill)) || (filter === 'local' && skill.origin === 'local') || (filter === 'external' && skill.origin !== 'local'));
  // Needs attention first: each blocked source with the command that accepts it, then the skills to look at.
  const blocked = filter === 'local' ? [] : inv.lock.filter((row) => row.state === 'blocked' && (!query || `${row.name} ${row.source}`.toLowerCase().includes(query)));
  const asks = inv.skills.filter((item) => skillNeedsAttention(item) && match(item));
  const groups = new Map();
  for (const skill of inv.skills.filter((item) => item.origin !== 'local' && !skillNeedsAttention(item) && match(item))) {
    const repo = skill.origin.split('@')[0];
    if (!groups.has(repo)) groups.set(repo, []);
    groups.get(repo).push(skill);
  }
  const mine = inv.skills.filter((item) => item.origin === 'local' && !skillNeedsAttention(item) && match(item));
  const local = inv.skills.find((item) => item.origin === 'local');
  const root = local ? tilde(local.path.slice(0, local.path.lastIndexOf('/'))) : '';
  mount(list,
    blocked.length || asks.length ? [h('div', {class: 'src'}, h('b', {class: 'st-warn', text: 'Needs attention'})), blocked.map(blockedItem), asks.map(skillRow)] : null,
    [...groups].map(([repo, items]) => {
      const lock = inv.lock.find((row) => sourceRepo(row) === repo);
      const commit = items[0].origin.split('@')[1];
      return [h('div', {class: 'src'}, h('b', {text: repo}), h('code', {text: lock && lock.state !== 'pinned' ? lock.state : commit ? `pinned at ${commit}` : 'pinned'}),
        lock?.findings ? h('span', {class: lock.critical ? 'st-fail' : 'st-warn', text: plural(lock.findings, 'audit finding')}) : null), items.map(skillRow)];
    }),
    mine.length ? [h('div', {class: 'src'}, h('b', {text: 'Yours'}), root ? h('code', {text: root}) : null), mine.map(skillRow)] : null,
    !mine.length && !groups.size && !asks.length && !blocked.length && inv.skills.length ? h('p', {class: 'muted', text: filter === 'attention' && !query ? 'Nothing needs attention: every skill is in place.' : 'No skill matches.'}) : null);
}
function skillRow(skill) {
  const key = `skill:${skill.name}`;
  const open = store.openRows.has(key);
  const live = new Set(columns());
  const lock = sourceLock(skill);
  const byState = {};
  for (const [agent, state] of Object.entries(skill.states)) if (live.has(agent)) (byState[state] = byState[state] || []).push(agent);
  const chipsFor = (state, cls) => h('span', {class: 'chips'}, agentOrder(byState[state] || []).map((agent) => agentChip(agent, cls)));
  const detail = h('div', {class: 'open', hidden: !open || undefined},
    h('dl', {class: 'facts'},
      h('dt', {text: 'In place in'}), h('dd', {}, byState.ok ? chipsFor('ok') : 'No harness yet.'),
      Object.keys(STATE_LABELS).filter((state) => byState[state]).map((state) => [h('dt', {text: STATE_LABELS[state]}), h('dd', {}, chipsFor(state, state === 'conflict' || state === 'error' ? 'st-fail' : 'st-warn'))]),
      h('dt', {text: 'Path'}), h('dd', {class: 'mono', text: tilde(skill.path)}),
      h('dt', {text: 'Origin'}), h('dd', {text: skill.origin === 'local' ? 'yours' : skill.origin}),
      lock ? [h('dt', {text: 'Source'}), h('dd', {class: lock.state === 'blocked' ? 'st-warn' : '', text: lock.state === 'blocked' ? `blocked by the audit: ${lock.reason || 'see Needs attention'}`
        : `in cooldown${Date.parse(lock.until) ? ` until ${weekdayClock(Date.parse(lock.until))}` : ''}; sync takes it when the cooldown ends`})] : null,
      lock?.command ? [h('dt', {text: lock.state === 'blocked' ? 'Accept' : 'Take it now'}), h('dd', {}, command(lock.command))] : null,
      h('dt', {text: 'Listing'}), h('dd', {text: `${tokens(skill.tokens)} tokens in each harness (est.)`})));
  const row = h('button', {class: 'srow', type: 'button', 'aria-expanded': String(open), onclick: () => {
    const next = !store.openRows.has(key);
    if (next) store.openRows.add(key); else store.openRows.delete(key);
    row.setAttribute('aria-expanded', String(next));
    detail.hidden = !next;
  }},
  h('span', {class: 'n'}, skill.name, lock?.state === 'blocked' ? h('span', {class: 'tag st-warn', text: 'source blocked'})
    : lock ? h('span', {class: 'tag', text: 'in cooldown'}) : skillNeedsAttention(skill) ? h('span', {class: 'tag st-warn', text: 'needs attention'}) : null),
  h('span', {class: 'd', text: skill.description || ''}),
  h('span', {class: 't', title: 'Listing tokens (est.)', text: String(skill.tokens)}));
  return [row, detail];
}

// Outside sources: each pin, and what sync puts in cooldown or blocks with the command that lets it through.
// Drawn with the site's lock stamps: pinned solid, cooldown dashed, blocked a 2px fill, audit pass outlined.
const LOCK_STATES = {pinned: ['t-pin', '✓', 'i-pin'], cooldown: ['t-warn', '!', 'i-timer'], blocked: ['t-bad', '✗', 'i-audit']};
function lockTag(cls, glyph, word, code) {
  return h('span', {class: `lock-tag ${cls}`}, h('span', {class: 'g', 'aria-hidden': 'true', text: glyph}), h('span', {class: 'w'}, word, code ? h('code', {text: code}) : null));
}
function sourcesSection(rows) {
  return [
    h('h2', {class: 'sec-h', text: 'Outside sources'}),
    h('p', {class: 'sec-p', text: HELP.sources}),
    rows.length ? rows.map(lockItem)
      : h('p', {class: 'muted'}, 'None yet. ', h('code', {text: 'tackroom skill sources add owner/repo'}), ' adds one; sync pins it, audits it and puts anything published in the last 72 hours in cooldown.'),
  ];
}
function lockItem(row) {
  const [cls, glyph, iconId] = LOCK_STATES[row.state] || ['t-warn', '!', 'i-warn'];
  const until = Date.parse(row.until);
  const audit = !row.audited ? null
    : row.critical ? lockTag('t-bad', '✗', 'audit', `${row.critical} critical`)
      : row.findings ? lockTag('t-warn', '!', 'audit', plural(row.findings, 'finding')) : lockTag('t-ok', '✓', 'audit pass');
  return h('div', {class: 'lock'},
    h('div', {class: 'lock-head'}, icon(iconId, cls === 't-pin' ? 'st-off' : cls === 't-bad' ? 'st-fail' : 'st-warn'), h('span', {class: 'lock-name', text: row.name}),
      row.state === 'pinned' ? lockTag(cls, glyph, 'pinned', row.commit)
        : row.state === 'cooldown' && until ? lockTag(cls, glyph, 'cooldown until', weekdayClock(until)) : lockTag(cls, glyph, row.state),
      audit),
    row.source ? h('p', {class: 'meta', text: [row.source.replace(/^https:\/\//, ''), row.commit && !row.pinned ? `new ${row.commit}` : ''].filter(Boolean).join(', ')}) : null,
    row.state === 'pinned' ? null : [h('p', {class: 'note', text: sourceWhy(row)}), row.command ? command(row.command) : null]);
}

// ---------- roles ----------
function renderRoles(root) {
  const inv = store.inv;
  const live = liveAgents();
  const takes = live.filter((agent) => agent.supports_roles);
  const not = live.filter((agent) => !agent.supports_roles);
  const attention = inv.roles.filter(needsAttention);
  const roles = [...attention, ...inv.roles.filter((role) => !needsAttention(role))];
  const allOk = roles.every((role) => takes.every((agent) => role.states[agent.name] === 'ok'));
  const title = !roles.length ? 'No roles yet'
    : attention.length ? `${plural(attention.length, 'role')} need${attention.length === 1 ? 's' : ''} attention`
      : allOk && takes.length ? `${plural(roles.length, 'role')}, in place in all ${takes.length} harnesses that take roles` : plural(roles.length, 'role');
  const said = [attentionLead(attention), not.length ? `${names(not.map((agent) => display(agent.name)))} ${not.length === 1 ? 'does' : 'do'} not support roles.` : '', HELP.roles].filter(Boolean).join(' ');
  const visible = new Set(columns());
  mount(root,
    hero(attention.length ? WARN : roles.length ? OK : IDLE, title, said),
    h('div', {class: 'list'}, roles.length ? roles.map((role) => {
      const states = Object.entries(role.states).filter(([agent]) => visible.has(agent));
      const ok = agentOrder(states.filter(([, state]) => state === 'ok').map(([agent]) => agent));
      const order = agentOrder(states.map(([agent]) => agent));
      const bad = states.filter(([, state]) => ATTENTION.has(state)).sort(([a], [b]) => order.indexOf(a) - order.indexOf(b));
      return h('div', {class: 'entry'},
        h('h3', {}, role.name, needsAttention(role) ? [' ', h('span', {class: 'tag st-warn', text: 'needs attention'})] : null),
        h('span', {class: 'chips'}, ok.map((agent) => agentChip(agent)), bad.map(([agent, state]) => agentChip(agent, state === 'conflict' || state === 'error' ? 'st-fail' : 'st-warn', CELLS[state]))),
        h('p', {class: 'meta', text: `agents/${role.name}.md in your config root`}));
    }) : h('p', {class: 'muted', text: 'No agent roles are configured. Roles live in agents/*.md in your config root.'})));
}

// ---------- MCP & hooks ----------
function renderWiring(root) {
  const inv = store.inv;
  const rows = [...inv.mcp, ...inv.hooks];
  const attention = rows.filter(needsAttention);
  const first = (list) => [...list.filter(needsAttention), ...list.filter((row) => !needsAttention(row))];
  const off = rows.filter((row) => !row.enabled).length;
  const counts = `${plural(inv.mcp.length, 'MCP server')} and ${plural(inv.hooks.length, 'hook')}`;
  const title = attention.length ? `${plural(attention.length, 'entry', 'entries')} need${attention.length === 1 ? 's' : ''} attention`
    : !rows.length ? 'No MCP servers or hooks yet' : off ? `${counts}, ${off} turned off` : `${counts}, all in place`;
  mount(root,
    hero(attention.length ? WARN : rows.length ? OK : IDLE, title, [attentionLead(attention), HELP.wiring].filter(Boolean).join(' ')),
    h('div', {class: 'list'},
      h('h2', {class: 'sec-h', text: 'MCP servers'}),
      inv.mcp.length ? first(inv.mcp).map((row) => wiringEntry('mcp', row)) : h('p', {class: 'muted', text: 'No MCP servers yet. Add one with tackroom mcp add.'}),
      h('h2', {class: 'sec-h', text: 'Hooks'}),
      inv.hooks.length ? first(inv.hooks).map((row) => wiringEntry('hooks', row)) : h('p', {class: 'muted', text: 'No hooks yet. Add them under hooks: in tackroom.yaml.'})));
}
function wiringEntry(kind, row) {
  const section = kind === 'mcp' ? 'mcp_servers' : 'hooks';
  const supportKey = kind === 'mcp' ? 'supports_mcp' : 'supports_hooks';
  const local = definedLocally(section, row.name);
  const sub = [kind === 'mcp' ? 'MCP server' : row.event ? `${row.event} hook` : 'hook', row.explicit ? '' : 'all agents', local ? 'in tackroom.local.yaml' : ''].filter(Boolean).join(' · ');
  const agents = columns();
  const unsupported = agents.filter((agent) => row.states[agent] === 'unsupported');
  const chips = agents.filter((agent) => row.states[agent] !== 'unsupported').map((agent) => {
    const state = row.states[agent] || 'unknown';
    const targeted = !['off', 'unsupported', 'absent', 'error'].includes(state);
    const locked = ['absent', 'error'].includes(state) || store.busy;
    const word = CELLS[state] || CELLS.unknown;
    const shown = ['ok', 'off'].includes(state) ? '' : word;
    return h('button', {
      type: 'button', class: `chip${state === 'absent' ? ' dim' : ''}${ATTENTION.has(state) ? ' st-warn' : ''}`, disabled: locked || undefined,
      'aria-pressed': String(targeted), 'aria-label': `${row.name} on ${display(agent)}: ${word}`,
      title: locked ? `${display(agent)}: ${word}` : `${display(agent)}: ${word}. Select to ${targeted ? 'remove' : 'add'} this agent.`,
      onclick: () => toggleTarget(section, supportKey, row, agent, targeted),
    }, agentMark(agent), display(agent), shown ? h('span', {class: 'state', text: `(${shown})`}) : null);
  });
  return h('div', {class: 'entry'},
    switchEl(row.enabled, [row.name, needsAttention(row) ? h('span', {class: 'tag st-warn', text: 'needs attention'}) : null], sub, (next) => patch(section, row.name, 'enabled', next, `${row.name} ${next ? 'turned on' : 'turned off'}.`)),
    row.command ? h('p', {class: 'meta'}, h('code', {text: tilde(row.command)})) : null,
    chips.length ? [h('p', {class: 'muted', text: 'Targets. Select a harness to add or remove it:'}), h('div', {class: 'chips'}, chips)] : null,
    unsupported.length ? h('p', {class: 'muted', text: `Not supported in ${names(unsupported.map(display))}.`}) : null);
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
  const conflicts = items.filter((item) => item.kind === 'conflict').length;
  const groups = UNMANAGED_GROUPS.map(([kind, , title, help]) => {
    const list = items.filter((item) => item.kind === kind);
    if (!list.length) return null;
    return [h('h2', {class: kind === 'conflict' ? 'sec-h st-fail' : 'sec-h', text: `${title} (${list.length})`}), h('p', {class: 'sec-p', text: help}),
      list.map((item) => h('div', {class: 'entry'},
        h('div', {class: 'entry-head'}, icon(/symlink/.test(item.detail || '') ? 'i-link' : 'i-folder', 'st-off'), h('h3', {text: item.name}),
          h('span', {class: 'chips'}, item.agents.map((agent) => h('span', {title: display(agent), 'aria-label': display(agent)}, agentMark(agent))))),
        item.detail ? h('p', {class: 'meta', text: tilde(item.detail)}) : null,
        item.hint ? command(tilde(item.hint)) : null))];
  }).filter(Boolean);
  mount(root,
    hero(conflicts ? FAIL : ['i-unmanaged', 'st-off'], items.length ? `${plural(items.length, 'item')} tackroom does not manage` : 'Everything is managed', items.length ? HELP.unmanaged : "Everything in your agents' skill folders is managed by tackroom."),
    h('div', {class: 'list'}, groups));
}

// ---------- crons ----------
const DAY_NAMES = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const STRIP_HOURS = 24;
function ago(iso, short = false) {
  const time = Date.parse(iso);
  if (!time) return short ? '-' : '';
  const minutes = Math.round((Date.now() - time) / 60000);
  if (minutes < 2) return short ? 'now' : 'just now';
  if (minutes < 90) return short ? `${minutes}m` : `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 36) return short ? `${hours}h` : `${hours} h ago`;
  return short ? `${Math.round(hours / 24)}d` : `${Math.round(hours / 24)} d ago`;
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
function when(ms, now) { return ms - now < 86400000 ? clock(ms) : weekdayClock(ms); }
const PAUSED = ['disabled', 'paused', 'not_loaded'];
const SOURCE_ICONS = {launchd: 'i-launchd', crontab: 'i-crontab', systemd: 'i-gear', codex: 'a-codex', 'claude-desktop': 'a-claude-code', 'claude-cloud': 'a-claude-code', 'herdr-automations': 'i-alarm'};
const JOB_STATES = {fail: ['fail', 'failing'], warn: ['warn', 'last run failed'], idle: ['off', 'not run yet'], ok: ['ok', 'healthy'], paused: ['off', 'paused']};
function jobState(job) { return JOB_STATES[job.health] || ['warn', job.health || 'unknown']; }
function sourceIcon(job) { return icon(SOURCE_ICONS[job.source] || 'i-crons', 'src-icon'); }
function compact(n) { return n >= 10000 ? `${(n / 1000).toFixed(n >= 100000 ? 0 : 1)}k` : n.toLocaleString('en-US'); }
function cronKey(job) { return `cron:${job.host}/${job.source}/${job.id}`; }
function hostName(host) { return host === 'local' ? 'This Mac' : host || 'Unknown host'; }
function byLastRun(a, b) { return (Date.parse(b.last_run) || 0) - (Date.parse(a.last_run) || 0); }
function jobSchedule(job) { return job.schedule_text || job.schedule || ''; }
function jobNext(job, now = Date.now()) {
  if (PAUSED.includes(job.state) || job.health === 'paused' || job.state === 'service') return null;
  return cronFires(job, now)?.fires?.[0] ?? nextFire(job, now);
}
function jobReason(job) {
  const problems = (job.problems || []).map((problem) => `${sentence(grouped(job.host === 'local' ? tilde(problem) : problem))}.`.replace(/\.\.$/, '.'));
  if (problems.length) return problems;
  if (job.health === 'warn') return [job.last_exit != null ? `Last run exited ${job.last_exit}.` : job.last_status ? `Last run: ${job.last_status.replace('_', ' ')}.` : 'Last run failed.'];
  if (job.health === 'fail') return ['Failing.'];
  return [];
}
function lastRunText(job) {
  if (!job.last_run) return 'never run';
  return `last run ${ago(job.last_run)}${(job.inferred || []).includes('last_run') ? ' (from the log file)' : ''}`;
}
// Run, pause and resume a launchd job. While an action on this job runs, its
// buttons are off and the clicked one says what it does; other jobs stay live.
const CRON_DOING = {run: 'Starting…', pause: 'Pausing…', resume: 'Resuming…', schedule: 'Saving…'};
function runButtons(job) {
  if (!(job.source === 'launchd' && job.unit)) return null;
  const busy = store.cronBusy[cronKey(job)];
  const off = ['disabled', 'not_loaded'].includes(job.state);
  const button = (label, action, primary) => h('button', {type: 'button', class: primary ? 'btn primary' : 'btn', disabled: Boolean(busy) || undefined, onclick: () => cronAction(job, action), text: busy === action ? CRON_DOING[action] : label});
  return off ? [button('Resume', 'resume', true)] : [button('Run now', 'run', true), button('Pause', 'pause')];
}
// A failing or attention row: what is wrong, the schedule, and its actions.
function jobItem(job) {
  return h('article', {class: 'item'},
    h('h3', {}, job.short_name, h('span', {text: ` on ${hostName(job.host)}`})),
    jobReason(job).map((text) => h('p', {text})),
    h('p', {class: 'meta', text: [jobSchedule(job), lastRunText(job)].filter(Boolean).join(', ')}),
    h('div', {class: 'acts'},
      runButtons(job),
      job.log ? h('button', {class: 'btn', type: 'button', onclick: () => openLog(job), text: 'Log'}) : null,
      h('button', {class: 'btn quiet', type: 'button', onclick: () => openJob(job), text: 'Details'})));
}
// The soonest runs, with one job due on several machines at once said once.
function nextLine(jobs) {
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
  if (!queue.length) return 'Nothing scheduled this week.';
  const first = queue[0];
  const same = queue.filter((item) => item.next === first.next && item.job.short_name === first.job.short_name);
  return `Next: ${first.job.short_name} on ${names(same.map((item) => hostName(item.job.host)))} at ${when(first.next, now)}.`;
}

function renderCrons(root) {
  const data = store.crons;
  const tools = h('div', {class: 'tools tools-row'},
    h('button', {type: 'button', class: 'btn primary', onclick: () => openJobPanel('add')}, icon('i-plus'), 'Add cron'),
    h('button', {type: 'button', class: store.cronsLoading ? 'btn spinning' : 'btn', disabled: store.cronsLoading || undefined, onclick: () => loadCrons(true)}, icon('i-refresh'), store.cronsLoading ? 'Collecting…' : 'Collect again'),
    data ? h('span', {class: 'stamp', title: new Date(Date.parse(data.scanned_at)).toLocaleString(), text: `Collected ${clock(Date.parse(data.scanned_at))}`}) : null);
  if (!data) {
    mount(root, store.cronsError
      ? [hero(FAIL, 'Could not collect scheduled jobs', store.cronsError), tools]
      : [hero(IDLE, 'Collecting scheduled jobs…', HELP.crons), tools]);
    return;
  }
  const jobs = data.jobs || [];
  const hosts = cronHosts(data);
  const down = downHosts(data);
  const fails = jobs.filter((job) => job.health === 'fail').sort(byLastRun);
  const warns = jobs.filter((job) => job.health === 'warn').sort(byLastRun);
  // Healthy first, then not run yet and paused, by name.
  const order = {ok: 0, idle: 1, paused: 2};
  const rest = jobs.filter((job) => job.health !== 'fail' && job.health !== 'warn')
    .sort((a, b) => (order[a.health] ?? 3) - (order[b.health] ?? 3) || a.short_name.localeCompare(b.short_name));
  const unreadable = unreadableSources(data);
  const stale = Boolean(store.cronsError);
  const title = [jobs.length ? (fails.length ? `${fails.length} of ${jobs.length} jobs failing` : `${plural(jobs.length, 'job')}, none failing`) : '',
    ...down.map((host) => `${hostName(host)} unreachable`), unreadable.length ? `${plural(unreadable.length, 'scheduler')} unreadable` : ''].filter(Boolean).join(', ') || 'No scheduled jobs';
  const counts = restCounts(jobs);
  const said = [hosts.length ? `On ${names(hosts.map(hostName))}${counts ? `: ${counts}` : ''}.` : '', jobs.length ? nextLine(jobs) : HELP.crons].filter(Boolean).join(' ');
  const folds = hosts.map((host) => [host, rest.filter((job) => job.host === host)]).filter(([, list]) => list.length);
  const strip = fireStrip(jobs, hosts);
  mount(root,
    hero(fails.length || down.length ? FAIL : warns.length || unreadable.length || stale ? WARN : jobs.length ? OK : IDLE, title, said),
    tools,
    h('div', {class: 'cols'},
      fails.length || down.length || warns.length || unreadable.length || stale ? h('section', {},
        fails.length || down.length ? [h('h2', {class: 'gh st-fail'}, 'Failing'), h('div', {class: 'items'}, down.map((host) => unreachableItem(host, data)), fails.map(jobItem))] : null,
        warns.length || unreadable.length || stale ? [h('h2', {class: 'gh st-warn'}, 'Needs attention'),
          h('div', {class: 'items'}, stale ? staleCronsItem() : null, unreadable.map(unreadableItem), warns.map(jobItem))] : null) : null,
      folds.length || strip ? h('section', {},
        h('h2', {class: 'gh'}, 'Everything else'),
        h('div', {class: 'hosts'}, folds.map(([host, list]) => hostFold(host, data, list)), strip)) : null),
    jobs.length ? h('p', {class: 'foot-note', text: HELP.crons}) : null);
}
function fold(key, defaultOpen, ...children) {
  const open = key in store.folds ? store.folds[key] : defaultOpen;
  return h('details', {class: key === 'strip' ? 'strip-wrap' : 'host', open: open || undefined, ontoggle: (event) => { store.folds[key] = event.currentTarget.open; }}, children);
}
// Healthy and paused jobs, folded per machine; each row opens the job.
function hostFold(host, data, list) {
  const now = Date.now();
  const count = (health) => list.filter((job) => job.health === health).length;
  const summary = restCounts(list);
  // An empty herdr-automations source still says whether its scheduler runs.
  const sources = hostAccess(host, data).filter((entry) => entry.state === 'found' || entry.state === 'unreadable' || (entry.source === 'herdr-automations' && entry.state === 'empty' && entry.detail && entry.detail.startsWith('scheduler')));
  return fold(`host:${host}`, window.matchMedia('(min-width: 900px)').matches,
    h('summary', {}, h('b', {text: hostName(host)}), h('span', {text: summary}), icon('i-plus')),
    sources.length ? h('p', {class: 'sources'}, sources.map((entry) => h('span', {class: entry.state === 'unreadable' ? 'st-fail' : null, title: entry.detail || '', text: `${entry.source} ${entry.state === 'unreadable' ? 'unreadable' : entry.jobs}`}))) : null,
    list.map((job) => {
      const [st, word] = jobState(job);
      const next = st === 'off' ? null : jobNext(job, now);
      const sub = st === 'off' ? [word, jobSchedule(job)] : [jobSchedule(job), next ? `next ${when(next, now)}` : '', job.state === 'service' ? 'service' : ''];
      return h('button', {class: st === 'off' ? 'jrow off' : 'jrow', type: 'button', onclick: () => openJob(job)},
        h('span', {class: 'n'}, job.short_name),
        h('span', {class: 's', text: sub.filter(Boolean).join(', ')}),
        h('span', {class: 'l', title: job.last_run ? `Last run ${new Date(Date.parse(job.last_run)).toLocaleString()}` : 'Never run', text: job.last_run ? ago(job.last_run, true) : 'not run'}));
    }));
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
  if (!rows.length) return null;
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
    return h('div', {class: ['strip-row', job.health === 'fail' && 'trouble', many && 'many'].filter(Boolean).join(' ')},
      h('span', {class: 'strip-label', title: [job.name || job.id, hostName(job.host), job.schedule_text].filter(Boolean).join(' · ')}, sourceIcon(job), h('span', {class: 'strip-name', text: job.short_name}), hosts.length > 1 ? h('span', {class: 'strip-host', text: hostName(job.host)}) : null),
      track);
  });
  return fold('strip', false,
    h('summary', {}, h('b', {text: 'Next 24 hours'}), h('span', {text: plural(rows.length, 'job')}), icon('i-plus')),
    h('div', {class: 'strip'}, axis, body));
}

// The job sheet: what is wrong, every action this job takes, its facts and where its log is.
function currentJob() {
  const sheet = store.sheet;
  return store.crons?.jobs?.find((job) => cronKey(job) === sheet.key) || sheet.job;
}
function openJob(job) {
  store.sheet = {kind: 'job', key: cronKey(job), job};
  renderSheet();
  openDialog(document.getElementById('sheet'));
}
function openLog(job) {
  store.sheet = {kind: 'log', key: cronKey(job), job};
  renderSheet();
  openDialog(document.getElementById('sheet'));
}
function openMenu() {
  store.sheet = {kind: 'menu'};
  renderSheet();
  openDialog(document.getElementById('sheet'));
}
function renderSheet() {
  const sheet = store.sheet;
  if (!sheet) return;
  // A redraw keeps the caret in the schedule field the user is typing in.
  const active = document.activeElement;
  const caret = active?.closest?.('#sheet-body .cron-schedule') ? active.selectionStart : null;
  const refocus = () => {
    const input = caret === null ? null : document.querySelector('#sheet-body .cron-schedule input');
    if (input) { input.focus(); input.setSelectionRange(caret, caret); }
  };
  const title = document.getElementById('sheet-title');
  const sub = document.getElementById('sheet-sub');
  const body = document.getElementById('sheet-body');
  if (sheet.kind === 'menu') {
    title.textContent = 'tackroom';
    sub.textContent = store.updatedAt ? `Updated ${clock(store.updatedAt)}` : '';
    const badge = document.querySelector('[data-badge="crons"]');
    mount(body,
      h('nav', {class: 'menu-list', 'aria-label': 'Views'}, TABS.map(([id, label, iconId]) => h('a', {href: `#${id}`, 'aria-current': id === store.view ? 'page' : null, onclick: () => closeDialog(document.getElementById('sheet'), true)},
        icon(iconId), label, id === 'crons' && !badge.hidden ? h('span', {class: badge.className, title: badge.title, text: badge.textContent}) : null))),
      configLinks().length ? [h('h3', {class: 'sheet-h3', text: 'Links'}), h('nav', {class: 'menu-list', 'aria-label': 'Configured links'}, linkNodes())] : null);
    return;
  }
  const job = currentJob();
  const [st, word] = jobState(job);
  title.textContent = sheet.kind === 'log' ? `${job.short_name} log` : job.short_name;
  mount(sub, h('span', {class: `st-${st}`, text: word}), ` on ${hostName(job.host)}`);
  if (sheet.kind === 'log') {
    mount(body, logNode(job), h('h3', {class: 'sheet-h3', text: 'Actions'}), cronActions(job));
    refocus();
    return;
  }
  const reason = jobReason(job);
  mount(body,
    reason.length > 1 ? h('ul', {class: 'problems'}, reason.map((text) => h('li', {text}))) : reason.length ? h('p', {class: `job-reason st-${st}`, text: reason[0]}) : null,
    cronActions(job),
    jobFacts(job),
    h('h3', {class: 'sheet-h3', text: 'Log'}),
    logNode(job));
  refocus();
}
function jobFacts(job) {
  const now = Date.now();
  const next = jobNext(job, now);
  const result = job.last_exit != null ? `exit ${job.last_exit}` : job.last_status ? job.last_status.replace('_', ' ') : '';
  const rows = [
    ['Schedule', jobSchedule(job)], ['Next run', next ? `${when(next, now)} (${until(next - now)})` : ''],
    ['Last run', job.last_run ? `${ago(job.last_run)}, ${new Date(Date.parse(job.last_run)).toLocaleString()}${(job.inferred || []).includes('last_run') ? ' (from the log file)' : ''}` : 'never'],
    ['Result', result], ['Runs', job.runs != null ? compact(job.runs) : ''], ['Effect', job.effect], ['Command', job.command], ['ID', job.id],
    ['Scheduler', job.source], ['Cron', job.schedule !== jobSchedule(job) ? job.schedule : ''], ['Time zone', job.tz], ['Created by', job.created_by],
    ['Connectors', (job.connectors || []).join(', ')], ['Output', job.output_expanded || job.output], ['Inferred', (job.inferred || []).join(', ')],
  ].filter(([, value]) => value);
  return h('dl', {class: 'facts'}, rows.map(([label, value]) => [h('dt', {text: label}), h('dd', {class: ['Command', 'ID', 'Cron'].includes(label) ? 'mono' : '', text: value})]));
}
// The view does not read log files; it names the file and the command that shows its end.
// A leading ~/ stays outside the quotes, so the shell that runs the command expands it, there or over ssh.
function shellWord(path) {
  const home = path.startsWith('~/');
  const rest = home ? path.slice(2) : path;
  const quoted = /[\s'"$`\\!*?#&;|<>(){}]/.test(rest) ? `"${rest.replace(/(["\\$`])/g, '\\$1')}"` : rest;
  return home ? `~/${quoted}` : quoted;
}
// launchd gives a path; systemd gives StandardOutput, which is file:, append: or truncate: and a path,
// or where the output goes instead: journal, inherit, syslog, kmsg, tty, null.
function logNode(job) {
  const raw = String(job.log || '').trim();
  if (!raw) return h('p', {class: 'log-note', text: 'tackroom knows no log file for this job.'});
  const value = raw.replace(/^(file|append|truncate):/, '');
  const path = job.host === 'local' ? tilde(value) : value;
  if (!path.startsWith('/') && !path.startsWith('~/')) {
    return [
      h('p', {class: 'log-path'}, h('code', {text: raw})),
      h('p', {class: 'log-note', text: value === 'null' ? 'This job discards its output, so there is no log to read.' : 'This job logs to the systemd journal, not to a file.'}),
    ];
  }
  const tail = `tail -n 50 ${shellWord(path)}`;
  return [
    h('p', {class: 'log-path'}, h('code', {text: path})),
    h('p', {class: 'log-note', text: job.host === 'local' ? 'The view does not read log files. To see its last lines, run:' : `This log is on ${job.host}. To see its last lines, run:`}),
    command(job.host === 'local' ? tail : `ssh ${job.host} '${tail.replace(/'/g, "'\\''")}'`),
  ];
}
// Run, pause and resume a launchd job. A job tackroom owns here also takes a new
// schedule, Edit and Delete; for any other job the server says why not (edit_note).
function cronActions(job) {
  const launchd = job.source === 'launchd' && job.unit;
  if (!launchd && !job.editable && !job.edit_note) return h('p', {class: 'note', text: job.source === 'claude-desktop' ? 'Run and pause this task in Claude Desktop.' : 'No actions for this job.'});
  const busy = store.cronBusy[cronKey(job)];
  const input = launchd && job.owned && job.host === 'local' && job.state !== 'disabled'
    ? h('input', {type: 'text', value: store.scheduleDrafts[cronKey(job)] ?? job.declared_schedule ?? '', placeholder: '0 9 * * *', 'aria-label': 'Schedule (cron expression)', spellcheck: 'false', autocapitalize: 'off', autocomplete: 'off',
      oninput: (event) => { store.scheduleDrafts[cronKey(job)] = event.target.value; }})
    : null;
  return h('div', {},
    h('div', {class: 'acts sheet-acts'},
      runButtons(job),
      job.editable ? [
        h('button', {type: 'button', class: 'btn', disabled: Boolean(busy) || undefined, onclick: () => { closeDialog(document.getElementById('sheet'), true); openJobPanel('edit', job); }, text: 'Edit'}),
        h('button', {type: 'button', class: 'btn danger', disabled: Boolean(busy) || undefined, onclick: () => { closeDialog(document.getElementById('sheet'), true); openJobPanel('delete', job); }, text: 'Delete'}),
      ] : null),
    input ? h('form', {class: 'cron-schedule', onsubmit: (event) => { event.preventDefault(); cronAction(job, 'schedule', input.value); }},
      input, h('button', {type: 'submit', class: 'btn', disabled: Boolean(busy) || undefined, text: busy === 'schedule' ? CRON_DOING.schedule : 'Save schedule'})) : null,
    !job.editable && job.edit_note ? h('p', {class: 'note'}, h('b', {text: 'No edit or delete. '}), job.edit_note) : null);
}
async function cronAction(job, action, schedule) {
  store.cronBusy[cronKey(job)] = action;
  renderView();
  renderSheet();
  const done = {run: 'Started', pause: 'Paused', resume: 'Resumed', schedule: 'New schedule applied to'}[action];
  try {
    const result = await api('/api/crons/action', {method: 'POST', body: JSON.stringify({host: job.host, source: job.source, id: job.id, action, ...(action === 'schedule' ? {schedule} : {})})});
    toast(`${done} ${job.short_name}`);
    if (action === 'schedule') delete store.scheduleDrafts[cronKey(job)];
    // The answer carries the job as it now is: redraw that row now, and let the
    // full collect below catch up in the background.
    if (result.job) patchCronRow((row) => row.host === job.host && row.source === job.source && row.id === job.id, result.job);
  } catch (error) {
    toast(error.message, true);
  }
  delete store.cronBusy[cronKey(job)];
  renderView();
  renderSheet();
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
  store.cronPatches = store.cronPatches.filter((item) => Date.now() - item.at < 120000);
  store.cronPatches.push({at: Date.now(), matches, fresh});
  if (!store.crons) return;
  store.crons = {...store.crons, jobs: replaceCronRow(store.crons.jobs || [], matches, fresh)};
  renderChrome();
  renderView();
  renderSheet();
}

// ---------- cron jobs: add, edit, delete ----------
// One sheet for the three. POST /api/crons/jobs does the work (the same code as
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
  submit.className = mode === 'delete' ? 'btn danger' : 'btn primary';
  submit.disabled = false;
  setJobNote('');
  let first = document.getElementById('cancel-job');
  if (mode === 'delete') {
    mount(document.getElementById('job-body'),
      h('p', {}, 'This removes ', h('b', {text: job.short_name}), ' from your tackroom config and its unit from this machine. tackroom touches no other job.'),
      h('dl', {class: 'facts'}, [['Schedule', job.declared_schedule], ['Command', (job.declared_command || []).map(quoted).join(' ')], ['Effect', job.effect]]
        .filter(([, value]) => value).map(([label, value]) => [h('dt', {text: label}), h('dd', {class: 'mono', text: value})])));
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
  openDialog(document.getElementById('job-panel'));
  first.focus();
}
function closeJobPanel() {
  closeDialog(document.getElementById('job-panel'));
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
  const head = [
    hero(['i-config', 'st-off'], 'Config', HELP.config),
    h('div', {class: 'tools'}, h('div', {class: 'filters', role: 'group', 'aria-label': 'Config layer'}, [['shared', 'Shared'], ['local', 'Local'], ['effective', 'Effective']].map(([value, label]) =>
      h('button', {type: 'button', 'aria-pressed': String(value === store.configLayer), onclick: () => loadConfigLayer(value), text: label})))),
  ];
  const state = store.configState;
  if (!state || state.active_layer !== store.configLayer) {
    if (store.configNote) {
      mount(root, head, h('div', {class: 'configs'}, h('div', {class: 'error-box'}, store.configNote, ' ', h('button', {type: 'button', class: 'btn', onclick: () => loadConfigLayer(store.configLayer), text: 'Retry'}))));
      return;
    }
    mount(root, head, h('div', {class: 'configs'}, h('p', {class: 'loading', text: 'Loading config…'})));
    if (!store.configLoading) loadConfigLayer(store.configLayer);
    return;
  }
  const layer = store.configLayer;
  const readOnly = state.read_only;
  const original = state.raw_yaml || '';
  const draft = store.drafts[layer] ?? original;
  const editor = h('textarea', {class: 'editor', id: `editor-${layer}`, spellcheck: 'false', autocapitalize: 'off', 'aria-label': `${layer} YAML`, readonly: readOnly || undefined, oninput: (event) => {
    store.drafts[layer] = event.target.value;
    store.configDiff = '';
    buttons.querySelectorAll('[data-dirty]').forEach((node) => { node.disabled = event.target.value === original; });
  }});
  editor.value = draft;
  const dirty = draft !== original;
  const buttons = h('div', {class: 'editor-actions'},
    h('button', {type: 'button', class: 'btn', dataset: {dirty: '1'}, disabled: readOnly || !dirty || undefined, onclick: () => validateConfig(layer), text: 'Check changes'}),
    h('button', {type: 'button', class: 'btn primary', dataset: {dirty: '1'}, disabled: readOnly || !dirty || undefined, onclick: () => saveConfig(layer), text: 'Save'}),
    h('button', {type: 'button', class: 'btn quiet', dataset: {dirty: '1'}, disabled: readOnly || !dirty || undefined, onclick: () => { delete store.drafts[layer]; store.configDiff = ''; store.configNote = ''; renderView(); }, text: 'Discard'}),
    store.configNote ? h('span', {class: 'note', text: store.configNote}) : null);
  const path = layer === 'effective' ? 'merge of both files' : tilde(state.paths?.[layer] || '');
  mount(root, head,
    h('div', {class: 'configs'},
      h('div', {class: 'config-meta'}, h('span', {}, icon('i-folder'), path), state.revision ? h('span', {}, icon('i-commit'), state.revision.slice(0, 12)) : null, readOnly ? h('span', {text: 'read-only'}) : null),
      editor,
      readOnly ? null : buttons,
      store.configDiff ? renderDiff(store.configDiff) : null));
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
// The Sync sheet: the dry-run plan per harness, then the switches that turn agents, MCP servers and hooks on or off.
// Any switch saves to the config and builds the plan again, so Apply always sends the digest of the plan on screen.
const PLAN_GROUPS = [
  ['Adds', 'add', 'skill'], ['AddsAgent', 'add', 'role'], ['AddsMCP', 'add', 'MCP'], ['AddsHook', 'add', 'hook'],
  ['Updates', 'update', 'skill'], ['UpdatesAgent', 'update', 'role (overwrite)'], ['UpdatesMCP', 'update', 'MCP'], ['UpdatesHook', 'update', 'hook'], ['UpdatesPackage', 'update', 'package'],
  ['Removes', 'remove', 'skill'], ['RemovesAgent', 'remove', 'role'], ['RemovesPackage', 'remove', 'package'],
];
async function openSync() {
  openDialog(document.getElementById('sync-panel'));
  mount(document.getElementById('sync-body'), h('div', {id: 'sync-plan'}), h('div', {id: 'sync-toggles'}));
  renderToggles();
  await loadPlan();
}
let planSeq = 0;
async function loadPlan() {
  const seq = ++planSeq;
  store.preview = null;
  store.planChanges = 0;
  setApply(false, '');
  document.getElementById('sync-sub').textContent = 'Building the plan…';
  const box = document.getElementById('sync-plan');
  if (box) mount(box, h('p', {class: 'loading', text: 'Building the sync plan…'}));
  try {
    const preview = await api('/api/sync/preview', {method: 'POST', body: '{}'});
    if (seq !== planSeq) return;
    store.preview = preview;
    store.previewAt = Date.now();
    renderPlan();
  } catch (error) {
    if (seq !== planSeq) return;
    document.getElementById('sync-sub').textContent = 'No plan';
    if (box) mount(box, h('div', {class: 'error-box', text: error.message}));
  }
}
function renderPlan() {
  const box = document.getElementById('sync-plan');
  if (!box || !store.preview) return;
  const plan = store.preview.plan || {};
  const summary = Object.fromEntries((plan.summary || []).map((line) => {
    const at = line.indexOf(': ');
    return at < 0 ? [line, ''] : [line.slice(0, at), line.slice(at + 2)];
  }));
  let changes = 0;
  let errors = 0;
  const rows = [];
  const repoState = plan.repo?.State;
  if (repoState && repoState !== 'synced') {
    changes += 1;
    rows.push(h('div', {class: 'plan-row'}, icon('i-link', 'mark'), h('b', {text: '~/.agents'}), h('ul', {}, h('li', {}, h('span', {class: 'verb update', text: 'link'}), h('span', {text: `${repoState} → ${tilde(plan.repo.ExpectedTarget)}`})))));
  }
  const reported = new Set();
  for (const report of plan.reports || []) {
    reported.add(report.Name);
    const items = [];
    if (report.Error) { errors += 1; items.push(['note', `config unreadable, skipped: ${report.Error}`]); }
    else if (report.Detected) {
      for (const [key, verb, noun] of PLAN_GROUPS) for (const name of report[key] || []) items.push([verb, `${noun} ${name}`]);
      for (const conflict of report.Conflicts || []) items.push(['conflict', `${tilde(conflict)} (left alone)`]);
      if (report.RootState && report.RootState !== 'synced') items.push([report.RootState === 'missing' ? 'add' : 'update', `root instructions (${report.RootState})`]);
      // A harness out of sync with nothing listed still gets its native config written.
      if (!items.length && !report.Synced) { changes += 1; items.push(['note', 'refresh native config']); }
    }
    changes += items.filter(([verb]) => ['add', 'update', 'remove'].includes(verb)).length;
    const line = summary[report.Name] || (report.Error ? 'config unreadable' : !report.Detected ? 'not installed, skipped' : items.length ? plural(items.length, 'change') : 'up to date');
    const count = (key) => (report[key] || []).length;
    const counts = report.Detected && !report.Error ? [plural(count('Managed'), 'skill'), count('ManagedAgent') && plural(count('ManagedAgent'), 'role'), count('ManagedMCP') && `${count('ManagedMCP')} MCP`, count('ManagedHook') && plural(count('ManagedHook'), 'hook')].filter(Boolean).join(', ') : '';
    rows.push(h('div', {class: report.Detected && !report.Error ? 'plan-row' : 'plan-row off'}, agentMark(report.Name), h('b', {text: `${display(report.Name)}: ${line}`}),
      counts ? h('span', {text: counts}) : null,
      items.length ? h('ul', {}, items.map(([verb, text]) => h('li', {}, h('span', {class: `verb ${verb}`, text: verb}), h('span', {text})))) : null));
  }
  for (const agent of (store.inv?.agents || []).filter((item) => !reported.has(item.name) && !item.enabled)) {
    rows.push(h('div', {class: 'plan-row off'}, agentMark(agent.name), h('b', {text: `${display(agent.name)}: turned off, skipped`})));
  }
  const waiting = (store.inv?.lock || []).filter((row) => row.state === 'cooldown' || row.state === 'blocked');
  if (waiting.length) {
    rows.push(h('div', {class: 'plan-row'}, icon('i-pin', 'mark'), h('b', {text: 'Outside sources'}),
      h('ul', {}, waiting.map((row) => h('li', {}, h('span', {class: `verb ${row.state}`, text: row.state}), h('span', {text: `${row.name}, skipped. See Overview.`}))))));
  }
  const destructive = plan.destructive || [];
  changes = Math.max(changes, destructive.length);
  store.planChanges = changes;
  document.getElementById('sync-sub').textContent = changes ? `${plural(changes, 'change')} to apply` : 'Nothing to change';
  mount(box,
    destructive.length ? h('div', {class: 'destructive'},
      h('strong', {text: `${plural(destructive.length, 'change')} cannot be undone by sync. Tick each one to continue.`}),
      destructive.map((item) => h('label', {}, h('input', {type: 'checkbox', 'aria-label': `Confirm: ${item}`, dataset: {destructive: item}, onchange: updateApply}), h('span', {text: item})))) : null,
    h('p', {class: 'sheet-lead', text: `Plan from the dry run at ${clock(store.previewAt)}.${!changes && !errors ? ' Every installed harness is up to date.' : ''}`}),
    h('h3', {class: 'sheet-h3', text: 'Plan by harness'}),
    rows);
  updateApply();
}
function renderToggles() {
  const box = document.getElementById('sync-toggles');
  if (!box || !sheetOpen('sync-panel')) return;
  const inv = store.inv;
  if (!inv) { mount(box); return; }
  const onOff = (section, row, sub) => switchEl(row.enabled, row.name, sub, (next) => patch(section, row.name, 'enabled', next, `${row.name} ${next ? 'turned on' : 'turned off'}.`));
  mount(box,
    h('h3', {class: 'sheet-h3', text: 'Turn on or off'}),
    h('p', {class: 'sheet-lead', text: 'Each switch saves to your config right away, and the plan above is built again. Turning a harness off stops syncing it; nothing on disk is removed.'}),
    inv.agents.length ? [h('p', {class: 'toggle-group', text: 'Agents'}), inv.agents.map((agent) => {
      const local = definedLocally('agents', agent.name);
      return switchEl(agent.enabled, [agentMark(agent.name), display(agent.name)], [harnessState(agent).word, local ? 'in tackroom.local.yaml' : ''].filter(Boolean).join(' · '),
        (next) => patch('agents', agent.name, 'enabled', next, `${agent.name} ${next ? 'turned on' : 'turned off'}.`));
    })] : null,
    inv.mcp.length ? [h('p', {class: 'toggle-group', text: 'MCP servers'}), inv.mcp.map((row) => onOff('mcp_servers', row, tilde(row.command || '')))] : null,
    inv.hooks.length ? [h('p', {class: 'toggle-group', text: 'Hooks'}), inv.hooks.map((row) => onOff('hooks', row, row.event || tilde(row.command || '')))] : null);
}
function updateApply() {
  const boxes = [...document.querySelectorAll('[data-destructive]')];
  // Apply stays off while the plan changes nothing, and until every destructive change is ticked.
  if (!store.planChanges) { setApply(false, 'Nothing to change.'); return; }
  const ready = boxes.every((box) => box.checked);
  setApply(ready, ready ? (boxes.length ? 'All destructive changes confirmed.' : 'Sync writes only what tackroom manages.') : `${boxes.filter((box) => !box.checked).length} left to confirm.`);
}
function setApply(enabled, note, bad = false) {
  document.getElementById('apply-sync').disabled = !enabled;
  setNote(note, bad);
}
function setNote(note, bad = false) {
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
    closeDialog(document.getElementById('sync-panel'));
    toast('Synced to your agents.');
    await refresh();
    loadHosts(true);
  } catch (error) {
    if (error.code === 'sync_plan_changed' || error.code === 'stale_revision') {
      await loadPlan();
      setNote('Something changed since the preview. Review the new plan.', true);
      return;
    }
    setApply(true, error.message, true);
  }
}

// ---------- boot ----------
for (const dialog of document.querySelectorAll('dialog.sheet')) {
  // Escape and a tap on the scrim slide the sheet out instead of dropping it.
  dialog.addEventListener('cancel', (event) => { event.preventDefault(); closeDialog(dialog); });
  dialog.addEventListener('click', (event) => { if (event.target === dialog) closeDialog(dialog); });
  dialog.addEventListener('close', () => {
    dialog.classList.remove('in');
    if (dialog.id === 'sheet' && !dialog.open) store.sheet = null;
    if (!document.querySelector('dialog[open]')) document.body.classList.remove('sheet-open');
  });
}
document.getElementById('open-sync').addEventListener('click', openSync);
document.getElementById('dock-sync').addEventListener('click', openSync);
document.getElementById('close-sync').addEventListener('click', () => closeDialog(document.getElementById('sync-panel')));
document.getElementById('apply-sync').addEventListener('click', applySync);
document.getElementById('close-sheet').addEventListener('click', () => closeDialog(document.getElementById('sheet')));
document.getElementById('menu-btn').addEventListener('click', openMenu);
document.getElementById('open-menu').addEventListener('click', openMenu);
document.getElementById('job-form').addEventListener('submit', submitJob);
document.getElementById('close-job').addEventListener('click', closeJobPanel);
document.getElementById('cancel-job').addEventListener('click', closeJobPanel);
document.getElementById('toast').addEventListener('click', hideToast);
window.addEventListener('hashchange', () => { route(); window.scrollTo(0, 0); });
route();
refresh();
loadCrons();
loadHosts();
// A background view exits after ui.view.idle_timeout without requests. The page pings while it is
// visible, and while a Config draft is unsaved even when hidden, so the draft can still be saved.
// A failed ping says the view stopped instead of leaving the next action to fail.
function ping() {
  const stopped = document.getElementById('view-stopped');
  api('/api/ping').then(() => { stopped.hidden = true; }, () => { stopped.hidden = false; });
}
setInterval(() => { if (document.visibilityState === 'visible' || Object.keys(store.drafts).length) ping(); }, 60000);
document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') ping(); });
