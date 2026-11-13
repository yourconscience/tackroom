import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Access, Job, Panel } from '../types'

// tackroom inside Claude Code: `/tackroom` opens a pane with the sync status
// and the same `tackroom cron collect` JSON the web view and the Herdr panel
// draw. It acts through the same `tackroom cron run|pause|resume` verbs.

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

const EMPTY: Panel = {
  jobs: [],
  access: [],
  collectedAt: '',
  drifted: null,
  error: '',
  isLoading: false,
  busy: '',
  open: [],
}

const panel = atom({ plugin: 'tackroom', key: 'panel' } as const, EMPTY, {
  shape: 'panel-1',
})

const id = (job: Job) => `${job.host}/${job.source}/${job.id}`
const hostName = (host: string) => (host === 'local' ? 'This Mac' : host)
const rank = (job: Job) => ORDER.indexOf(job.health)

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

export const register: Register = (on, options) => {
  bin = String(options.tackroom || 'tackroom')
  config = String(options.config || '')

  on('session.start', async ($, e, next) => {
    await $.command.register({
      name: 'tackroom',
      description: 'Show tackroom sync status and scheduled jobs in a pane',
    })
    // A reload can land mid-collect or mid-action and leave these set.
    await update($, panel, p => ({ ...p, isLoading: false, busy: '' }))

    return next(e)
  })

  on('command.run', { command: 'tackroom' }, async $ => {
    await $.ui.open({ id: PANE, title: 'tackroom' })
    void load($)

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

    return (
      <Box flexDirection="column" rowGap={1}>
        <Box flexDirection="row" columnGap={2}>
          {p.drifted === null && <Text dimColor>Sync status unknown</Text>}
          {p.drifted?.length === 0 && <Text color="success">In sync</Text>}
          {p.drifted !== null && p.drifted.length > 0 && (
            <Text color="warning">
              Out of sync: {p.drifted.join(', ')}. Run tackroom sync.
            </Text>
          )}
        </Box>
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
