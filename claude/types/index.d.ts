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

// The fields the pane reads from `tackroom sync --dry-run --json`. A tackroom
// older than 2.6 prints no plan_digest and has no `config set` or
// `sync --plan-digest`, so the pane then changes nothing but crons.
export type Surface = {
  managed: string[]
  missing: string[]
  drifted: string[]
}

export type Harness = {
  name: string
  detected: boolean
  synced: boolean
  error?: string | null
  conflicts: string[]
  root_instructions?: { state: string } | null
  skills: Surface
  roles: Surface
  mcp: Surface
  hooks: Surface
}

export type Plan = {
  synced: boolean
  changes: number
  agents: Harness[]
  plan: string[]
  destructive: string[]
  plan_digest?: string
}

// The fields the pane reads from `tackroom config inventory`.
export type Agent = {
  name: string
  display_name?: string
  enabled: boolean
  supports_mcp: boolean
  supports_hooks: boolean
}

// An MCP server or a hook. An entry without an agents list (explicit false)
// targets every agent that supports its kind.
export type Wired = {
  name: string
  enabled: boolean
  event?: string
  command?: string
  explicit: boolean
  targets: string[]
  states: Record<string, string>
}

export type Inventory = { agents: Agent[]; mcp: Wired[]; hooks: Wired[] }

// One machine of `tackroom hosts --json`.
export type Machine = {
  host: string
  health: string
  summary: string
  fix?: string
}

// The plan Sync shows before it applies it.
export type Review = {
  lines: string[]
  destructive: string[]
  digest: string
  error: string
  // Set once a sync of this plan failed: its digest is never sent again.
  isStale: boolean
}

export type Panel = {
  jobs: Job[]
  access: Access[]
  // When the last collect ran, as `HH:MM`; empty before the first one.
  collectedAt: string
  // Harnesses `tackroom status` reports as out of sync; null before the first status.
  drifted: string[] | null
  error: string
  isLoading: boolean
  // What runs now: a job's action as `host/source/id`, an edit as
  // `cfg:<path>`, or `sync`.
  busy: string
  // The rows and sections that are open: jobs as `host/source/id`.
  open: string[]
  // `sync --dry-run --json`, `config inventory` and `hosts --json`.
  plan: Plan | null
  inventory: Inventory | null
  machines: Machine[]
  // Which of those three are loading, and why the last load of each failed.
  loading: string[]
  failed: Record<string, string>
  // The plan Sync shows; null while it is not shown.
  review: Review | null
}

declare module 'claude-code' {
  interface PluginState {
    tackroom: { panel: Shaped<Panel> }
  }
}
