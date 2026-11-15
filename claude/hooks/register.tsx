import { atom, read, update } from 'claude-code'
import type {
  EngineInterface,
  Register,
  RenderChildren,
  RenderElement,
} from 'claude-code'

import type {
  Access,
  Harness,
  Job,
  Machine,
  Panel,
  Plan,
  Review,
  Surface,
  Wired,
} from '../types'

// tackroom inside Claude Code: `/tackroom` opens a pane with the sync status
// and the same `tackroom cron collect` JSON the web view and the Herdr panel
// draw. It acts through the same `tackroom cron run|pause|resume` verbs.
// With tackroom 2.6 or newer it also changes what the Herdr panel changes,
// through the same CLI:
//   Sync         the plan of `sync --dry-run --json`, applied with
//                `sync --plan-digest`, so only the plan shown applies
//   Turn on/off  a harness, MCP server or hook, and Add or Remove of one
//                agent on an MCP server or hook, with `config set`
//   Machines     `hosts --json`, read-only, with more than one host

const PANE = 'tackroom'
// A launchd job that is off offers Resume, as in the web view.
const OFF = ['disabled', 'not_loaded']
const ORDER = ['fail', 'warn', 'idle', 'ok', 'paused']
const HEALTH: Record<string, string> = {
  fail: 'Failing',
  warn: 'Last run failed',
  idle: 'No result',
  ok: 'Healthy',
  paused: 'Paused',
}
const TONE: Record<string, string> = {
  fail: 'error',
  warn: 'warning',
  ok: 'success',
  idle: 'inactive',
  paused: 'inactive',
}
const DONE: Record<string, string> = {
  run: 'Started',
  pause: 'Paused',
  resume: 'Resumed',
}
const MACHINE: Record<string, string> = {
  ok: 'healthy',
  warn: 'needs attention',
  fail: 'failing',
}
// What a cell of the inventory says, in the web view's words, and its tone.
const CELL: Record<string, [string, string]> = {
  ok: ['in place', 'ok'],
  drift: ['drifted', 'warn'],
  missing: ['missing', 'warn'],
  conflict: ['conflict', 'fail'],
  off: ['not targeted', 'idle'],
  disabled: ['turned off', 'idle'],
  unsupported: ['not supported', 'idle'],
  absent: ['not installed', 'idle'],
  error: ['config unreadable', 'fail'],
}
const RANK: Record<string, number> = { fail: 3, warn: 2, ok: 1, idle: 0 }

// Each loads on its own, so an edit reloads the plan without a crons collect.
const SOURCES = {
  plan: { args: ['sync', '--dry-run', '--json'], timeoutMs: 120_000 },
  inventory: { args: ['config', 'inventory'], timeoutMs: 120_000 },
  hosts: { args: ['hosts', '--json'], timeoutMs: 300_000 },
}
type Source = keyof typeof SOURCES
const ALL: Source[] = ['plan', 'inventory', 'hosts']

const EMPTY: Panel = {
  jobs: [],
  access: [],
  collectedAt: '',
  drifted: null,
  error: '',
  isLoading: false,
  busy: '',
  open: [],
  plan: null,
  inventory: null,
  machines: [],
  loading: [],
  failed: {},
  review: null,
}

// The tag changes with Panel's fields: a value an earlier version stored
// outlives a reload and is read as EMPTY.
const panel = atom({ plugin: 'tackroom', key: 'panel' } as const, EMPTY, {
  shape: 'panel-2',
})

const id = (job: Job) => `${job.host}/${job.source}/${job.id}`
const hostName = (host: string) => (host === 'local' ? 'This Mac' : host)
const rank = (job: Job) => ORDER.indexOf(job.health)
const plural = (n: number, one: string, many = `${one}s`) =>
  `${n} ${n === 1 ? one : many}`
const cell = (state: string): [string, string] =>
  CELL[state] ?? ['unknown', 'warn']
const worst = (tones: string[]) =>
  tones.reduce((a, b) => ((RANK[b] ?? 0) > (RANK[a] ?? 0) ? b : a), 'idle')

// Plan lines read "agent: what"; agents with the same change share one line.
function grouped(lines: string[]) {
  const out = new Map<string, string[]>()
  for (const line of lines) {
    const at = line.indexOf(': ')
    const [agent, what] =
      at < 0 ? ['', line] : [line.slice(0, at), line.slice(at + 2)]
    if (what === 'up to date' || what === 'not installed, skipped') continue
    out.set(what, [...(out.get(what) ?? []), agent].filter(Boolean))
  }

  return [...out]
}

function harnessHealth(a: Harness): [string, string] {
  if (!a.detected) return ['idle', 'not installed']
  if (a.error) return ['fail', 'config unreadable']
  if (a.conflicts.length > 0)
    return ['fail', plural(a.conflicts.length, 'conflict')]

  return a.synced ? ['ok', 'synced'] : ['warn', 'needs sync']
}

function lastLine(ran: { stdout: string; stderr: string; exitCode: number }) {
  const text = (ran.stderr || ran.stdout).trim()

  return text.split('\n').pop() || `exit ${ran.exitCode}`
}

function ago(iso: string | undefined, now: number) {
  const at = iso ? Date.parse(iso) : NaN
  if (Number.isNaN(at)) return '-'
  const minutes = Math.round((now - at) / 60e3)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.round(minutes / 60)

  return hours < 48 ? `${hours} h ago` : `${Math.round(hours / 24)} d ago`
}

function clock(ms: number) {
  const at = new Date(ms)
  const two = (n: number) => String(n).padStart(2, '0')

  return `${two(at.getHours())}:${two(at.getMinutes())}`
}

// Set from the plugin's options when the module registers.
let bin = 'tackroom'
let config = ''

// args is [command, verb or flag, ...]; --config goes right after those two,
// because the cron verbs take their flags before the job id.
const tackroom = ($: EngineInterface, args: string[], timeoutMs: number) =>
  $.process.run(
    [
      bin,
      ...args.slice(0, 2),
      ...(config ? ['--config', config] : []),
      ...args.slice(2),
    ],
    { timeoutMs },
  )

const load = async ($: EngineInterface) => {
  if ((await read($, panel)).isLoading) return
  await update($, panel, p => ({ ...p, isLoading: true }))
  let next: Partial<Panel>
  try {
    // `status --json` exits 1 when a harness is out of sync, 2 on an error.
    const [status, crons] = await Promise.all([
      tackroom($, ['status', '--json'], 60_000),
      tackroom($, ['cron', 'collect'], 300_000),
    ])
    if (status.exitCode > 1) throw new Error(lastLine(status))
    if (crons.exitCode !== 0) throw new Error(lastLine(crons))
    const agents: { name: string; detected: boolean; synced: boolean }[] =
      JSON.parse(status.stdout).agents ?? []
    const collected: { jobs?: Job[]; access?: Access[] } = JSON.parse(
      crons.stdout,
    )
    next = {
      jobs: collected.jobs ?? [],
      access: collected.access ?? [],
      collectedAt: clock(await $.clock.now()),
      drifted: agents.filter(a => a.detected && !a.synced).map(a => a.name),
      error: '',
    }
  } catch (error) {
    next = { error: error instanceof Error ? error.message : String(error) }
  }
  await update($, panel, p => ({ ...p, ...next, isLoading: false }))
}

const act = async ($: EngineInterface, job: Job, verb: string) => {
  await update($, panel, p => ({ ...p, busy: id(job) }))
  try {
    const ran = await tackroom(
      $,
      ['cron', verb, '--host', job.host, '--source', job.source, job.id],
      120_000,
    )
    $.ui.toast(
      ran.exitCode === 0
        ? `${DONE[verb]} ${job.short_name}`
        : `Could not ${verb} ${job.short_name}: ${lastLine(ran)}`,
    )
  } catch (error) {
    $.ui.toast(`Could not ${verb} ${job.short_name}: ${String(error)}`)
  }
  await update($, panel, p => ({ ...p, busy: '' }))
  await load($)
}

const toggle = ($: EngineInterface, key: string) =>
  update($, panel, p => ({
    ...p,
    open: p.open.includes(key)
      ? p.open.filter(k => k !== key)
      : [...p.open, key],
  }))

// A load asked for while one of the same source runs runs again after it, so
// the pane never keeps a plan from before an edit. Module state: a reload
// drops the runs, and session.start clears `loading` with them.
const running = new Set<Source>()
const again = new Set<Source>()

const loadSource = async ($: EngineInterface, name: Source) => {
  if (running.has(name)) {
    again.add(name)

    return
  }
  running.add(name)
  again.delete(name)
  await update($, panel, p => ({ ...p, loading: [...p.loading, name] }))
  let next: Partial<Panel> = {}
  let error = ''
  try {
    const ran = await tackroom($, SOURCES[name].args, SOURCES[name].timeoutMs)
    if (ran.exitCode !== 0) throw new Error(lastLine(ran))
    const data = JSON.parse(ran.stdout)
    next =
      name === 'plan'
        ? { plan: data }
        : name === 'inventory'
          ? { inventory: data }
          : { machines: data.hosts ?? [] }
  } catch (caught) {
    error = caught instanceof Error ? caught.message : String(caught)
  }
  running.delete(name)
  await update($, panel, p => ({
    ...p,
    ...next,
    loading: p.loading.filter(n => n !== name),
    failed: { ...p.failed, [name]: error },
  }))
  if (again.has(name)) await loadSource($, name)
}

const loadSetup = ($: EngineInterface, names: Source[] = ALL) =>
  Promise.all(names.map(name => loadSource($, name)))

// One field of tackroom.yaml (or tackroom.local.yaml, where the entry is
// defined there), as the web view's switches change it. The plan and the
// inventory load again, so Sync shows what is waiting.
const configSet = async (
  $: EngineInterface,
  path: string,
  value: boolean | string[],
  done: string,
) => {
  // A plan on show is no longer the plan once the config changes.
  await update($, panel, p => ({ ...p, busy: `cfg:${path}`, review: null }))
  try {
    const ran = await tackroom(
      $,
      ['config', 'set', path, JSON.stringify(value)],
      60_000,
    )
    $.ui.toast(
      ran.exitCode === 0
        ? `${done}. Saved. Sync applies it.`
        : `Could not change ${path}: ${lastLine(ran)}`,
    )
  } catch (error) {
    $.ui.toast(`Could not change ${path}: ${String(error)}`)
  }
  await update($, panel, p => ({ ...p, busy: '' }))
  await loadSetup($, ['plan', 'inventory'])
}

// Adds or removes one agent from an MCP server's or hook's targets, as a cell
// of the web view's matrix does. An entry without an agents list targets every
// agent that supports it, so the list starts from those.
const toggleTarget = async (
  $: EngineInterface,
  kind: 'mcp' | 'hook',
  name: string,
  agent: string,
  isTargeted: boolean,
) => {
  const inv = (await read($, panel)).inventory
  const entry = (kind === 'mcp' ? inv?.mcp : inv?.hooks)?.find(
    w => w.name === name,
  )
  if (!inv || !entry) return
  const order = inv.agents.map(a => a.name)
  const current = entry.explicit
    ? entry.targets
    : inv.agents
        .filter(a => (kind === 'mcp' ? a.supports_mcp : a.supports_hooks))
        .map(a => a.name)
  const next = (
    isTargeted
      ? current.filter(n => n !== agent)
      : [...new Set([...current, agent])]
  ).sort((a, b) => order.indexOf(a) - order.indexOf(b))
  if (next.length === 0) {
    $.ui.toast(`${name} needs at least one agent. Turn it off instead.`)

    return
  }
  await configSet(
    $,
    `${kind === 'mcp' ? 'mcp_servers' : 'hooks'}/${name}/agents`,
    next,
    `${name} ${isTargeted ? 'removed from' : 'added to'} ${agent}`,
  )
}

// Shows the plan as it loaded last; Sync then applies that plan alone.
const openReview = ($: EngineInterface) =>
  update($, panel, p => {
    const plan = p.plan
    if (!plan?.plan_digest) return p

    return {
      ...p,
      review: {
        lines: plan.plan,
        destructive: plan.destructive,
        digest: plan.plan_digest,
        error: '',
        isStale: false,
      },
    }
  })

// tackroom runs the dry run again and syncs only while its digest is the one
// shown and the config is unchanged; otherwise it refuses and changes nothing.
const sync = async ($: EngineInterface) => {
  const review = (await read($, panel)).review
  if (!review || review.isStale) return
  await update($, panel, p => ({ ...p, busy: 'sync' }))
  let error = ''
  try {
    // --plan-digest=... stays one argument: --config goes after it.
    const ran = await tackroom(
      $,
      ['sync', `--plan-digest=${review.digest}`],
      300_000,
    )
    if (ran.exitCode !== 0)
      error = (ran.stderr || ran.stdout || `exit ${ran.exitCode}`)
        .trim()
        .slice(0, 500)
  } catch (caught) {
    error = caught instanceof Error ? caught.message : String(caught)
  }
  // A retry would send the digest of a plan that is no longer the plan.
  await update($, panel, p => ({
    ...p,
    busy: '',
    review: error && p.review ? { ...p.review, error, isStale: true } : null,
  }))
  if (!error) $.ui.toast('Synced')
  // Either way: a failed apply can leave the config or the agents changed.
  await loadSetup($)
}

export const register: Register = (on, options) => {
  bin = String(options.tackroom || 'tackroom')
  config = String(options.config || '')

  on('session.start', async ($, e, next) => {
    await $.command.register({
      name: 'tackroom',
      description:
        'Sync tackroom and manage its setup and scheduled jobs in a pane',
    })
    // A reload can land mid-collect or mid-action and leave these set.
    await update($, panel, p => ({
      ...p,
      isLoading: false,
      busy: '',
      loading: [],
    }))

    return next(e)
  })

  on('command.run', { command: 'tackroom' }, async $ => {
    await $.ui.open({ id: PANE, title: 'tackroom' })
    void load($)
    void loadSetup($)

    return { text: 'tackroom pane opened.' }
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Button, Text } = $.ui.resolve(e)
    const p = await read($, panel)
    const now = await $.clock.now()
    const hosts = [
      ...new Set([...p.access.map(a => a.host), ...p.jobs.map(j => j.host)]),
    ]
    const isBusy = p.busy !== '' || p.isLoading

    const dot = (health: string) => <Text color={TONE[health]}>● </Text>

    const hostRow = (host: string) => {
      const access = p.access.filter(a => a.host === host)
      const isDown =
        access.length > 0 &&
        access.every(a => a.state === 'not_scanned') &&
        access.some(a => (a.detail ?? '').startsWith('probe failed'))
      const counts = ORDER.map(health => ({
        health,
        n: p.jobs.filter(j => j.host === host && j.health === health).length,
      })).filter(c => c.n > 0)

      return (
        <Box flexDirection="row" flexWrap="wrap" columnGap={2}>
          <Text bold>{hostName(host)}</Text>
          {isDown && (
            <Text color="error">unreachable: {access[0]?.detail}</Text>
          )}
          {!isDown &&
            counts.map(c => (
              <Text>
                {dot(c.health)}
                <Text dimColor>
                  {c.n} {(HEALTH[c.health] ?? c.health).toLowerCase()}
                </Text>
              </Text>
            ))}
        </Box>
      )
    }

    const detail = (job: Job) => {
      const result =
        job.last_exit != null ? `exit ${job.last_exit}` : job.last_status
      const fields = [
        ['Health', HEALTH[job.health] ?? job.health],
        ['ID', job.id],
        ['Command', job.command],
        ['Log', job.log],
        ['Schedule', job.declared_schedule || job.schedule],
        ['Last run', job.last_run],
        ['Result', result],
      ].filter(field => field[1])
      const verbs =
        job.source === 'launchd' && job.unit
          ? OFF.includes(job.state ?? '')
            ? [['resume', 'Resume']]
            : [
                ['run', 'Run now'],
                ['pause', 'Pause'],
              ]
          : []

      return (
        <Box flexDirection="column" marginLeft={2} marginBottom={1}>
          {(job.problems ?? []).map(problem => (
            <Text color="error">{problem}</Text>
          ))}
          {fields.map(field => (
            <Text dimColor>
              {field[0]}: {field[1]}
            </Text>
          ))}
          {verbs.length > 0 && (
            <Box flexDirection="row" columnGap={1}>
              {verbs.map((verb, i) => (
                <Button
                  key={`${id(job)}:${verb[0]}`}
                  label={p.busy === id(job) ? '…' : String(verb[1])}
                  variant={i === 0 ? 'primary' : 'secondary'}
                  dimColor={isBusy}
                  onPress={() => {
                    if (!isBusy) void act($, job, String(verb[0]))
                  }}
                />
              ))}
            </Box>
          )}
        </Box>
      )
    }

    // `where` keeps a job's row under Needs attention apart from its row
    // under its host.
    const jobRow = (job: Job, where: string) => {
      const key = `${where}:${id(job)}`
      const note = where === 'a' ? hostName(job.host) : job.schedule_text
      const when =
        job.health === 'paused' ? 'paused' : ago(job.last_run, now)

      return (
        <Box flexDirection="column">
          <Button key={key} plain onPress={() => toggle($, key)}>
            {dot(job.health)}
            {job.short_name}{' '}
            <Text dimColor>
              {note} · {when}
            </Text>
          </Button>
          {p.open.includes(key) && detail(job)}
        </Box>
      )
    }

    const section = (title: string, jobs: Job[], where: string) => (
      <Box flexDirection="column">
        <Text dimColor bold>
          {title.toUpperCase()} {jobs.length}
        </Text>
        {jobs.map(job => jobRow(job, where))}
      </Box>
    )

    const failing = p.jobs.filter(job => job.health === 'fail')

    // Edits need a tackroom whose dry run prints plan_digest (2.6 or newer).
    const canEdit = Boolean(p.plan?.plan_digest)
    const isEditBusy = p.busy !== ''
    const isSetupLoading = p.loading.length > 0

    const failure = (name: Source, hasData: boolean) => {
      const error = p.failed[name] ?? ''
      if (error === '') return null

      return (
        <Text color="error">
          {hasData ? `Last load failed: ${error}` : error}
        </Text>
      )
    }

    // A row that opens into its details when pressed, as a job's row does.
    const fold = (
      key: string,
      head: RenderChildren,
      body: () => RenderElement,
    ) => (
      <Box flexDirection="column">
        <Button key={key} plain onPress={() => toggle($, key)}>
          {head}
        </Button>
        {p.open.includes(key) && body()}
      </Box>
    )

    // The setup sections start closed, so the crons stay in view.
    const group = (
      key: string,
      title: string,
      count: number,
      level: string,
      body: () => RenderChildren,
    ) => {
      const isOpen = p.open.includes(key)

      return (
        <Box flexDirection="column">
          <Button key={key} plain onPress={() => toggle($, key)}>
            <Text dimColor bold>
              {isOpen ? '▾' : '▸'} {title.toUpperCase()} {count}{' '}
            </Text>
            {(level === 'fail' || level === 'warn') && dot(level)}
          </Button>
          {isOpen && body()}
        </Box>
      )
    }

    const headline = (plan: Plan) => {
      const waiting = plan.agents.filter(a => a.detected && !a.synced).length
      const text = plan.synced
        ? 'All synced'
        : plan.changes > 0
          ? `${plural(plan.changes, 'change')} waiting`
          : `${plural(waiting, 'harness', 'harnesses')} out of sync`
      const isSyncBlocked = isEditBusy || p.loading.includes('plan')
      const offersSync = !plan.synced && p.review === null

      return (
        <Box flexDirection="column">
          <Box flexDirection="row" flexWrap="wrap" columnGap={2}>
            <Text color={plan.synced ? 'success' : 'warning'}>{text}</Text>
            {isSetupLoading && <Text dimColor>loading…</Text>}
            {offersSync && (
              <Button
                key="sync"
                label="Sync"
                variant="primary"
                dimColor={isSyncBlocked}
                onPress={() => {
                  if (!isSyncBlocked) void openReview($)
                }}
              />
            )}
            <Button
              key="refresh"
              label="Refresh"
              dimColor={isSetupLoading}
              onPress={() => {
                void loadSetup($)
              }}
            />
          </Box>
          {offersSync && (
            <Text dimColor>
              Sync shows the plan before it changes anything.
            </Text>
          )}
        </Box>
      )
    }

    const change = ([what, agents]: [string, string[]], color?: string) => (
      <Box flexDirection="column">
        <Text color={color}>{what}</Text>
        {agents.length > 0 && (
          <Box marginLeft={2}>
            <Text dimColor>{agents.join(', ')}</Text>
          </Box>
        )}
      </Box>
    )

    // The plan as the Herdr panel's Sync dialog shows it, in the pane itself.
    const reviewBox = (r: Review) => {
      const changes = grouped(r.lines)
      const removes = grouped(r.destructive)
      const isSyncing = p.busy === 'sync'

      return (
        <Box flexDirection="column" rowGap={1} borderStyle="round" paddingX={1}>
          <Text bold>Sync</Text>
          {changes.length === 0 && removes.length === 0 && (
            <Text dimColor>Nothing to change.</Text>
          )}
          {changes.length > 0 && (
            <Box flexDirection="column">{changes.map(g => change(g))}</Box>
          )}
          {removes.length > 0 && (
            <Box flexDirection="column">
              <Text color="error" bold>
                REMOVES OR OVERWRITES
              </Text>
              {removes.map(g => change(g, 'error'))}
            </Box>
          )}
          <Text dimColor>
            Sync applies this plan from the config it was made from. If the
            plan or the config changed since it loaded, it stops and the pane
            loads the new plan. Edits inside skill and role files are not
            checked.
          </Text>
          {r.error !== '' && <Text color="error">{r.error}</Text>}
          {r.isStale && (
            <Text dimColor>
              Nothing more is applied from this plan. Cancel, then press Sync
              for the current one.
            </Text>
          )}
          <Box flexDirection="row" columnGap={1}>
            <Button
              key="review-cancel"
              label="Cancel"
              dimColor={isSyncing}
              onPress={() => {
                if (!isSyncing)
                  void update($, panel, q => ({ ...q, review: null }))
              }}
            />
            {!r.isStale && (
              <Button
                key="review-sync"
                label={
                  isSyncing
                    ? 'Working…'
                    : r.destructive.length > 0
                      ? 'Sync and remove'
                      : 'Sync'
                }
                variant="primary"
                dimColor={isEditBusy}
                onPress={() => {
                  if (!isEditBusy) void sync($)
                }}
              />
            )}
          </Box>
        </Box>
      )
    }

    // One machine of `tackroom hosts`: its health, summary and the command
    // that fixes it.
    const machineRow = (m: Machine) => (
      <Box flexDirection="column">
        <Box flexDirection="row" flexWrap="wrap" columnGap={2}>
          <Text bold>{hostName(m.host)}</Text>
          <Text>
            {dot(m.health)}
            <Text color={TONE[m.health]}>{MACHINE[m.health] ?? m.health}</Text>
          </Text>
        </Box>
        <Box flexDirection="column" marginLeft={2}>
          <Text dimColor>{m.summary}</Text>
          {m.fix ? <Text dimColor>Fix: {m.fix}</Text> : null}
        </Box>
      </Box>
    )

    // Turn on or Turn off for a harness, MCP server or hook.
    const switchButton = (section: string, name: string, isOn: boolean) => {
      const path = `${section}/${name}/enabled`

      return (
        <Box flexDirection="row">
          <Button
            key={`on:${path}`}
            label={
              p.busy === `cfg:${path}` ? '…' : isOn ? 'Turn off' : 'Turn on'
            }
            dimColor={isEditBusy}
            onPress={() => {
              if (!isEditBusy)
                void configSet(
                  $,
                  path,
                  !isOn,
                  `${name} turned ${isOn ? 'off' : 'on'}`,
                )
            }}
          />
        </Box>
      )
    }

    const surfaces = (a: Harness): [string, Surface][] => [
      ['Skills', a.skills],
      ['Roles', a.roles],
      ['MCP', a.mcp],
      ['Hooks', a.hooks],
    ]

    const harnessRow = (a: Harness) => {
      const [health, word] = harnessHealth(a)
      const count = (kind: 'missing' | 'drifted') =>
        surfaces(a).reduce((n, [, s]) => n + s[kind].length, 0)
      // What needs attention first, then how much the harness has.
      const summary = [
        ...(count('missing') ? [`${count('missing')} missing`] : []),
        ...(count('drifted') ? [`${count('drifted')} drifted`] : []),
        plural(a.skills.managed.length, 'skill'),
        plural(a.roles.managed.length, 'role'),
        `${a.mcp.managed.length} MCP`,
        plural(a.hooks.managed.length, 'hook'),
      ].join(' · ')

      return fold(
        `h:${a.name}`,
        [dot(health), a.name, ' ', <Text color={TONE[health]}>{word}</Text>],
        () => (
          <Box flexDirection="column" marginLeft={2} marginBottom={1}>
            {a.detected && <Text dimColor>{summary}</Text>}
            {a.error ? <Text color="error">{a.error}</Text> : null}
            {a.conflicts.map(conflict => (
              <Text color="error">{conflict}</Text>
            ))}
            {a.root_instructions && a.root_instructions.state !== 'synced' && (
              <Text dimColor>Root file: {a.root_instructions.state}</Text>
            )}
            {surfaces(a).flatMap(([label, s]) =>
              (['missing', 'drifted'] as const)
                .filter(kind => s[kind].length > 0)
                .map(kind => (
                  <Text dimColor>
                    {label}: {kind}: {s[kind].join(', ')}
                  </Text>
                )),
            )}
            {switchButton('agents', a.name, true)}
          </Box>
        ),
      )
    }

    // A harness turned off in the config is not in the plan; the inventory
    // lists it.
    const offRow = (name: string) =>
      fold(
        `h:${name}`,
        [dot('idle'), name, ' ', <Text dimColor>turned off</Text>],
        () => (
          <Box flexDirection="column" marginLeft={2} marginBottom={1}>
            {switchButton('agents', name, false)}
          </Box>
        ),
      )

    const agentLabel = (agent: string) =>
      p.inventory?.agents.find(a => a.name === agent)?.display_name || agent

    const levelOf = (w: Wired) =>
      worst(Object.values(w.states).map(state => cell(state)[1]))

    // An MCP server's or hook's row: its state in each harness, with Add or
    // Remove where the harness supports it, as the web view's cells do.
    const wiredRow = (kind: 'mcp' | 'hook', w: Wired) => {
      const agents = Object.keys(w.states)
      const stateOf = (agent: string) => w.states[agent] ?? 'unknown'
      const targeted = agents.filter(
        a => !['off', 'disabled', 'unsupported', 'absent'].includes(stateOf(a)),
      )
      const inPlace = targeted.filter(a => stateOf(a) === 'ok').length
      const tag = w.event || (w.enabled ? '' : 'turned off')
      const note = [
        tag,
        targeted.length > 0 ? `${inPlace}/${targeted.length}` : '-',
      ]
        .filter(Boolean)
        .join(' · ')

      return fold(
        `${kind}:${w.name}`,
        [dot(levelOf(w)), w.name, ' ', <Text dimColor>{note}</Text>],
        () => (
          <Box flexDirection="column" marginLeft={2} marginBottom={1}>
            {w.command ? <Text dimColor>Command: {w.command}</Text> : null}
            <Text dimColor>
              Targets: {w.explicit ? w.targets.join(', ') : 'all agents'}
            </Text>
            {agents.map(agent => {
              const state = stateOf(agent)
              const [word, level] = cell(state)
              const isLocked = ['unsupported', 'absent', 'error'].includes(
                state,
              )
              const isTargeted = !isLocked && state !== 'off'

              return (
                <Box flexDirection="row" flexWrap="wrap" columnGap={1}>
                  <Text>
                    {dot(level)}
                    {agentLabel(agent)} <Text dimColor>{word}</Text>
                  </Text>
                  {!isLocked && (
                    <Button
                      key={`t:${kind}:${w.name}:${agent}`}
                      label={isTargeted ? 'Remove' : 'Add'}
                      dimColor={isEditBusy}
                      onPress={() => {
                        if (!isEditBusy)
                          void toggleTarget($, kind, w.name, agent, isTargeted)
                      }}
                    />
                  )}
                </Box>
              )
            })}
            {switchButton(
              kind === 'mcp' ? 'mcp_servers' : 'hooks',
              w.name,
              w.enabled,
            )}
          </Box>
        ),
      )
    }

    // Rows that need attention first, then by name.
    const byLevel = (list: Wired[]) =>
      [...list].sort(
        (a, b) =>
          (RANK[levelOf(b)] ?? 0) - (RANK[levelOf(a)] ?? 0) ||
          a.name.localeCompare(b.name),
      )

    const wiredGroup = (kind: 'mcp' | 'hook', list: Wired[]) =>
      group(
        `s:${kind}`,
        kind === 'mcp' ? 'MCP servers' : 'Hooks',
        list.length,
        worst(list.map(levelOf)),
        () =>
          list.length === 0 ? (
            <Text dimColor>
              {kind === 'mcp'
                ? 'No MCP servers yet. Add one with tackroom mcp add.'
                : 'No hooks yet. Add them under hooks: in tackroom.yaml.'}
            </Text>
          ) : (
            byLevel(list).map(w => wiredRow(kind, w))
          ),
      )

    const setup = (plan: Plan) => {
      const off = (p.inventory?.agents ?? []).filter(a => !a.enabled)

      return (
        <Box flexDirection="column">
          {group(
            's:harnesses',
            'Harnesses',
            plan.agents.length + off.length,
            worst(plan.agents.map(a => harnessHealth(a)[0])),
            () => [
              ...plan.agents.map(harnessRow),
              ...off.map(a => offRow(a.name)),
            ],
          )}
          {p.inventory && wiredGroup('mcp', p.inventory.mcp)}
          {p.inventory && wiredGroup('hook', p.inventory.hooks)}
          {failure('inventory', p.inventory !== null)}
        </Box>
      )
    }

    return (
      <Box flexDirection="column" rowGap={1}>
        {canEdit && p.plan ? (
          headline(p.plan)
        ) : (
          <Box flexDirection="column">
            <Box flexDirection="row" columnGap={2}>
              {p.drifted === null && <Text dimColor>Sync status unknown</Text>}
              {p.drifted?.length === 0 && <Text color="success">In sync</Text>}
              {p.drifted !== null && p.drifted.length > 0 && (
                <Text color="warning">
                  Out of sync: {p.drifted.join(', ')}. Run tackroom sync.
                </Text>
              )}
            </Box>
            {(p.plan !== null || (p.failed.plan ?? '') !== '') && (
              <Text dimColor>
                Update tackroom to sync and edit from this pane.
              </Text>
            )}
          </Box>
        )}
        {failure('plan', p.plan !== null)}
        {p.review !== null && reviewBox(p.review)}
        {p.machines.length > 1 && (
          <Box flexDirection="column">
            <Text dimColor bold>
              MACHINES {p.machines.length}
            </Text>
            {p.machines.map(machineRow)}
          </Box>
        )}
        {canEdit && p.plan && setup(p.plan)}
        <Box flexDirection="row" columnGap={2}>
          <Text bold>Crons</Text>
          <Text dimColor>{p.isLoading ? 'collecting…' : p.collectedAt}</Text>
          <Button
            key="collect"
            label="Collect"
            dimColor={p.isLoading}
            onPress={() => {
              void load($)
            }}
          />
        </Box>
        {p.error !== '' && (
          <Text color="error">
            {p.collectedAt ? `Last collect failed: ${p.error}` : p.error}
          </Text>
        )}
        {hosts.length > 0 && (
          <Box flexDirection="column">{hosts.map(hostRow)}</Box>
        )}
        {failing.length > 0 && section('Needs attention', failing, 'a')}
        {hosts.map(host => {
          const jobs = p.jobs
            .filter(job => job.host === host)
            .sort((a, b) => rank(a) - rank(b))

          return jobs.length > 0 ? section(hostName(host), jobs, 'j') : null
        })}
      </Box>
    )
  })
}
