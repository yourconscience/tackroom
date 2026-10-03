# SPEC: `tackroom view` dashboard

Status: approved for implementation, PR and merge on 2026-10-03. The completed v1.0 spec is in git history (`git show 0d74d9e:SPEC.md`).

## Goal

Turn `tackroom view` from a single column of toggles into a web dashboard that shows what tackroom manages on every agent and lets the user change it safely. It stays a loopback web UI served by the CLI: no desktop app, no JavaScript build, no new dependency.

## Non-goals

- Desktop app, marketplace, or a skill registry browser.
- Per-agent skill scoping. Every canonical skill goes to every enabled agent today (`expectedSkillsForAgent` returns the full set), so the skill matrix is read-only.
- Writing native harness files from the browser. Every edit goes to `tackroom.yaml` or `tackroom.local.yaml`; native files change only through the existing sync preview and apply.
- Usage, eval or security data from other tools (AgentsView, skill-evals, HarnessKit). Follow-up candidates.

## User story / behavior

One page with six views, switched from a top bar and reachable by `#hash`:

1. **Overview**: totals (agents, skills, roles, MCP servers, hooks), overall sync state, and one card per configured agent: installed or not, synced or the number of pending changes, managed/drifted/missing counts, estimated skill-listing tokens, root instructions state, and an enable toggle.
2. **Skills**: a skills × agents matrix. Each cell shows linked, drifted, missing, conflict or not installed. Search and a "needs attention" filter. A row opens details: description, origin (`local` or `owner/repo@sha`), and per-agent state.
3. **Roles**: the same matrix for agent roles.
4. **MCP & hooks**: editable matrices. A row toggle enables or disables the entry; a cell toggles whether that agent is targeted. Cells also show the native state (synced, drifted, missing, unsupported). An empty `agents:` list means "every supporting agent"; unchecking one agent writes the explicit list of the others.
5. **Unmanaged**: items in agent skill roots that tackroom does not manage, grouped by name with the agents they appear in, plus conflicts and stale links, each with the next command to run.
6. **Config**: Shared / Local / Effective layers. Shows the YAML with path and revision; Shared and Local can be edited, validated (diff shown) and saved with the revision guard.

A **Review sync** button (count of pending changes) opens a panel with the sync plan grouped by agent. Destructive items must each be ticked in the page before Apply is enabled (replaces `window.confirm`). After apply, all views refresh.

Edits target the layer that defines the entry: an entry overridden in `tackroom.local.yaml` is edited there and marked "local".

## Acceptance tests

- `GET /api/inventory` requires the session cookie, works with zero enabled agents, and returns agents, skills, roles, MCP servers, hooks and unmanaged items with per-agent states. Unit tests cover state mapping from synthetic `agentReport`s: linked, drifted, missing, conflict, not installed, unreadable config; MCP and hook targeting with an empty and an explicit `agents:` list and unsupported harnesses; unmanaged grouping.
- A handler test with a temp `HOME` and repo returns a skill with its frontmatter description and a `local` origin.
- Existing config web tests keep passing; `go test ./...` passes.
- Browser check against a sandbox config in a temp `HOME`: all six views render with real data at desktop and phone width, in light and dark; toggling an MCP target writes the YAML; the sync panel previews, requires ticking destructive items, and applies.

## Constraints

- Same CSP (`'self'` only): no inline scripts or style attributes, no external fonts or CDNs. System font stacks.
- Vanilla ES module JS, embedded assets, all text set via `textContent`.
- Keep the existing API contracts (`/api/state`, `/api/config`, `/api/config/raw`, `/api/config/validate`, `/api/sync/*`, `/api/status`).
- Loopback-only bind, token and CSRF rules unchanged.

## Codebase notes

- Server: `internal/app/config_web.go` (routes, auth), new `internal/app/view_inventory.go`.
- Reuse `inspectAgents`, `expectedSkills`, `skillOrigins`, `parseSkillFrontmatter`, `estimateTokens`, `hasMCPSupport`, `hookTargetForHarness`, `describeExternalSkillEntry`, `conflictMentions`.
- UI: `internal/app/web/{index.html,style.css,app.js}`.

## Outcome / Deviations

- Shipped as specified: `GET /api/inventory` (`internal/app/view_inventory.go`) and a six-view dashboard in `internal/app/web/`. The live config of the maintainer (6 agents, 25 skills, 6 roles, 2 MCP servers, 4 hooks, 27 unmanaged items) builds its inventory in about 0.2 s.
- Added beyond the spec, found while exercising the UI:
  - The index page now reissues the CSRF cookie. With `--token-file`, a restarted server kept the session but minted a new CSRF secret, so every save failed with "CSRF header is required" until the tokenized URL was opened again.
  - JSON-sourced strings are written unquoted (`- codex`, not `- "codex"`); strings that would read back as another type stay quoted.
  - Configured `ui.links` moved into a Links menu so they no longer push the view tabs off the bar.
  - Hidden folders in agent skill roots (Codex's `.system`) are listed as unmanaged without a `skill promote` hint.
- Browser check ran in Brave against a temp `HOME` sandbox (toggle, sync preview and apply, destructive confirmation, YAML check and save) and read-only against the live config. Phone width was measured in a 400 px window (no horizontal overflow in any view) and inspected with the mobile rules forced on; the light theme was inspected by removing the dark rules in the page. No landing-page screenshot was replaced: a public screenshot would need non-personal demo data.
