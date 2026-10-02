const $ = (selector) => document.querySelector(selector);
let layer = 'shared';
let state = null;
const baseURL = new URL(window.location.pathname.endsWith('/') ? window.location.pathname : `${window.location.pathname}/`, window.location.origin);

function csrf() {
  return document.cookie.split('; ').find((item) => item.startsWith('tackroom_csrf='))?.split('=')[1] || '';
}
function setStatus(message, kind = '') {
  const node = $('#status'); node.textContent = message; node.className = `status ${kind}`;
}
async function api(path, options = {}) {
  const headers = {'Accept':'application/json', ...(options.body ? {'Content-Type':'application/json'} : {}), ...(options.method && options.method !== 'GET' ? {'X-Tackroom-CSRF':csrf()} : {})};
  const response = await fetch(new URL(path.replace(/^\//, ''), baseURL), {...options, headers});
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(body.error?.message || `request failed (${response.status})`);
    error.code = body.error?.code || '';
    throw error;
  }
  return body;
}
function pick(object, ...keys) { for (const key of keys) if (object && object[key] !== undefined) return object[key]; return undefined; }
function labelFromPath(path) { return path.split('/')[2] || path; }
function renderLinks(ui) {
  const links = pick(ui, 'Links','links') || [];
  $('#links').replaceChildren(...links.map((link) => {
    const anchor = document.createElement('a');
    anchor.textContent = pick(link,'Name','name');
    anchor.href = pick(link,'URL','url');
    anchor.target = '_top';
    return anchor;
  }));
}
function toggle(path, value, labelText) {
  const label = document.createElement('label');
  label.className = 'toggle';
  const input = document.createElement('input');
  input.type = 'checkbox';
  input.checked = !!value;
  input.dataset.editPath = path;
  input.disabled = state.read_only;
  input.setAttribute('aria-label', labelText);
  const slider = document.createElement('span');
  slider.className = 'slider';
  slider.setAttribute('aria-hidden', 'true');
  label.append(input, slider);
  return label;
}
function row(keyLabel, description, controls) {
  const div = document.createElement('div');
  div.className = 'ledger-row';
  const summary = document.createElement('div');
  const key = document.createElement('strong');
  key.className = 'key';
  key.textContent = keyLabel;
  const detail = document.createElement('small');
  detail.textContent = description;
  summary.append(key, detail);
  const choices = document.createElement('div');
  choices.className = 'choices';
  choices.append(...controls);
  div.append(summary, choices);
  return div;
}
function section(label) {
  const heading = document.createElement('h2');
  heading.className = 'ledger-section';
  heading.textContent = label;
  return heading;
}
function renderStructured(config) {
  const agents = pick(config, 'Agents','agents') || [];
  const servers = pick(config, 'MCPServers','mcp_servers') || [];
  const hooks = pick(config, 'Hooks','hooks') || [];
  const links = pick(pick(config, 'UI','ui'), 'Links','links') || [];
  const rows = [];
  if (agents.length) rows.push(section('Agents'));
  for (const agent of agents) {
    const name = pick(agent,'Name','name');
    const roots = [pick(agent,'SkillRoot','skill_root'), pick(agent,'AgentRoot','agent_root')].filter(Boolean).join(' · ');
    rows.push(row(name, roots, [toggle(`/agents/${name}/enabled`, pick(agent,'Enabled','enabled'), `${name} enabled`)]));
  }
  if (servers.length) rows.push(section('MCP servers'));
  for (const server of servers) {
    const name = pick(server,'Name','name');
    const targets = pick(server,'Agents','agents') || [];
    rows.push(row(name, targets.length ? `Targets: ${targets.join(', ')}` : 'No targets', [toggle(`/mcp_servers/${name}/enabled`, pick(server,'Enabled','enabled'), `${name} enabled`)]));
  }
  if (hooks.length) rows.push(section('Hooks'));
  for (const hook of hooks) {
    const name = pick(hook,'Name','name');
    const event = pick(hook,'Event','event');
    rows.push(row(name, event ? `Event: ${event}` : 'No event', [toggle(`/hooks/${name}/enabled`, pick(hook,'Enabled','enabled'), `${name} enabled`)]));
  }
  if (links.length) rows.push(section('Navigation'));
  links.forEach((link) => rows.push(row(pick(link,'Name','name'), pick(link,'URL','url'), [])));
  if (!rows.length) {
    const empty = document.createElement('p');
    empty.className = 'empty';
    empty.textContent = 'No selectable settings in this layer.';
    rows.push(empty);
  }
  const ledger = $('#structured');
  ledger.replaceChildren(...rows);
  ledger.querySelectorAll('[data-edit-path]').forEach((input) => input.addEventListener('change', () => applyToggle(input)));
}
async function applyToggle(input) {
  const path = input.dataset.editPath;
  const value = input.checked;
  input.disabled = true;
  try {
    const result = await api('/api/config', {method:'PATCH', body:JSON.stringify({layer, expected_revision:state.revision, operations:[{op:'set', path, value}]})});
    state.revision = result.revision;
    $('#revision').textContent = result.revision ? result.revision.slice(0, 12) : '';
    setStatus(`${value ? 'Enabled' : 'Disabled'} ${labelFromPath(path)}. Sync to apply it to your agents.`, 'ok');
  } catch (error) {
    if (error.code === 'stale_revision') {
      await load(layer);
      setStatus('Config changed on disk; reloaded to the latest. Flip again.', 'error');
      return;
    }
    input.checked = !value;
    setStatus(error.message, 'error');
  } finally {
    input.disabled = state.read_only;
  }
}
function render() {
  const config = state.typed_config;
  $('#heading').textContent = layer[0].toUpperCase() + layer.slice(1) + (layer === 'effective' ? ' merge' : ' configuration');
  renderStructured(config);
  renderLinks(state.effective_ui);
  $('#source-meta').textContent = state.paths[layer === 'effective' ? 'shared' : layer] || '';
  $('#revision').textContent = state.revision ? state.revision.slice(0, 12) : '';
}
async function load(nextLayer = layer) {
  layer = nextLayer;
  document.querySelectorAll('.source').forEach((node) => node.classList.toggle('active', node.dataset.layer === layer));
  try { state = await api(`/api/state?layer=${encodeURIComponent(layer)}`); render(); setStatus(state.read_only ? 'Effective merge is read-only.' : 'Flip a toggle to apply it immediately.'); }
  catch (error) { setStatus(error.message, 'error'); }
}
// Report fields from the sync plan, in the order a reader cares about them.
const PLAN_CHANGES = [
  ['Adds','add skill'], ['AddsAgent','add role'], ['AddsMCP','add MCP'], ['AddsHook','add hook'],
  ['Updates','update skill'], ['UpdatesAgent','overwrite role'], ['UpdatesMCP','update MCP'], ['UpdatesHook','update hook'], ['UpdatesPackage','update package'],
  ['Removes','remove skill'], ['RemovesAgent','remove role'], ['RemovesPackage','remove package'],
];
function summarizePlan(plan) {
  const reports = (plan?.reports || []).filter((report) => report.Detected !== false);
  if (!reports.length) return 'No enabled agents to sync.';
  return reports.map((report) => {
    const changes = PLAN_CHANGES.flatMap(([key, label]) => (report[key] || []).map((item) => `  ${label} ${item}`));
    return changes.length ? `${report.Name}\n${changes.join('\n')}` : `${report.Name}: up to date`;
  }).join('\n');
}
async function syncNow() {
  setStatus('Previewing sync...');
  try {
    const preview = await api('/api/sync/preview', {method:'POST', body:'{}'});
    const destructive = preview.plan?.destructive || [];
    $('#plan').textContent = summarizePlan(preview.plan);
    if (destructive.length && !confirm(`Sync includes ${destructive.length} destructive change(s):\n\n${destructive.join('\n')}\n\nApply anyway?`)) {
      setStatus('Sync canceled.');
      return;
    }
    await api('/api/sync/apply', {method:'POST', body:JSON.stringify({expected_revision:preview.revision, plan_digest:preview.digest, confirmed_destructive:destructive})});
    await load(layer);
    setStatus('Synced to your agents.', 'ok');
  } catch (error) { setStatus(error.message, 'error'); }
}
document.querySelectorAll('.source').forEach((node) => node.addEventListener('click', () => load(node.dataset.layer)));
$('#sync').addEventListener('click', syncNow);
$('#msync').addEventListener('click', syncNow);
load();
