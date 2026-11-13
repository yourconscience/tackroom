// What the pane keeps between drawings. `Job` and `Access` are the fields the
// pane reads from `tackroom cron collect`.

export type Job = {
  host: string
  source: string
  id: string
  unit?: string
  state?: string
  command?: string
  log?: string
  schedule?: string
  declared_schedule?: string
  last_run?: string
  last_exit?: number
  last_status?: string
  problems?: string[]
  health: string
  short_name: string
  schedule_text: string
}

export type Access = { host: string; state: string; detail?: string }

export type Panel = {
  jobs: Job[]
  access: Access[]
  // When the last collect ran, as `HH:MM`; empty before the first one.
  collectedAt: string
  // Harnesses `tackroom status` reports as out of sync; null before the first status.
  drifted: string[] | null
  error: string
  isLoading: boolean
  // The job an action is running on, as `host/source/id`.
  busy: string
  // The jobs whose details are open, as `host/source/id`.
  open: string[]
}

declare module 'claude-code' {
  interface PluginState {
    tackroom: { panel: Shaped<Panel> }
  }
}
