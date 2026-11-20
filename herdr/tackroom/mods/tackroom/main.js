import { View, div, svg } from "gpui-kit";
import { h_flex, v_flex } from "gpui-base";
import { exec, props, theme, notify, openOverlay, nextCommand, run } from "herdr";
import JobForm, { runTackroom, expand } from "./form.js";

// tackroom inside Herdr, one tab for each tab of the web view. Every tab draws
// the JSON of a tackroom command, the same data the web view draws:
//   Overview    one answer from every source: `sync --dry-run --json` (harnesses,
//               items in cooldown or blocked), `hosts --json` (tackroom on each
//               machine in crons.hosts) and `cron collect`; under it what is
//               failing, what needs attention, then everything else, one line per kind
//   Skills, Roles, MCP & hooks   `config inventory`  what /api/inventory serves
//   Crons       `cron collect`  and acts through `cron run|pause|resume`
// It changes what the web view changes, through the CLI:
//   Sync        the plan in a dialog, applied with `sync --plan-digest`
//   Turn on/off an agent, MCP server or hook, and which agents an MCP server
//               or hook targets, with `config set`; Sync then applies it
//   Crons       Add, Edit and Delete open form.js as a dialog of this same mod;
//               Log opens the job's log in a terminal tab

const SOURCES = {
  plan: { args: ["sync", "--dry-run", "--json"], timeout: 120000 },
  inventory: { args: ["config", "inventory"], timeout: 120000 },
  crons: { args: ["cron", "collect"], timeout: 300000 },
  hosts: { args: ["hosts", "--json"], timeout: 300000 },
};
// sources: what a tab draws, which Refresh reloads; the first decides when it
// has data to draw. web: the tab of the web view the Web button opens.
const TABS = [
  { id: "overview", label: "Overview", sources: ["plan", "inventory", "crons", "hosts"], web: "overview" },
  { id: "skills", label: "Skills", sources: ["inventory"], web: "skills" },
  { id: "roles", label: "Roles", sources: ["inventory"], web: "roles" },
  { id: "wiring", label: "MCP & hooks", sources: ["inventory"], web: "wiring" },
  { id: "crons", label: "Crons", sources: ["crons"], web: "crons" },
];

// One status vocabulary for every tab: fail, warn, ok and off (not installed,
// turned off, paused, not targeted), each drawn in one theme colour.
const RANK = { fail: 3, warn: 2, ok: 1, off: 0 };
const bad = (level) => level === "fail" || level === "warn";
const worst = (levels) => levels.reduce((a, b) => (RANK[b] > RANK[a] ? b : a), "off");
const tone = (t, level) => ({ fail: t.ansi1, warn: t.ansi3, ok: t.ansi2, off: t.dim })[level] ?? t.dim;

// A launchd job that is off offers Resume, as in the web view.
const OFF = ["disabled", "not_loaded"];
// A job's health as a level, in the web view's words.
const JOB = { fail: ["fail", "failing"], warn: ["warn", "last run failed"], idle: ["off", "not run yet"], ok: ["ok", "healthy"], paused: ["off", "paused"] };
const jobLevel = (j) => (JOB[j.health] ?? ["warn"])[0];
const DONE = { run: "Started", pause: "Paused", resume: "Resumed" };
const VERB = { run: "Run now", pause: "Pause", resume: "Resume" };
const MACHINE = { ok: "healthy", warn: "needs attention", fail: "failing" };
// What a matrix cell of the inventory says, in the web view's words, and its level.
const CELL = {
  ok: ["in place", "ok"], drift: ["drifted", "warn"], missing: ["missing", "warn"], conflict: ["conflict", "fail"],
  off: ["not targeted", "off"], disabled: ["turned off", "off"], unsupported: ["not supported", "off"],
  absent: ["not installed", "off"], error: ["config unreadable", "fail"], unknown: ["unknown", "warn"],
};
const cellLevel = (state) => (CELL[state] ?? CELL.unknown)[1];
// Where a row is wrong, as the web view says it: "is missing in Claude Code and Pi".
const WRONG = { missing: "is missing in", drift: "has drifted in", conflict: "has a conflict in", error: "cannot be checked in", unknown: "is in an unknown state in" };
// An outside source as owner/repo, the form a skill's origin starts with.
const sourceRepo = (l) => (l.source || "").replace(/^https:\/\/github\.com\//, "").replace(/\.git$/, "");

const hostName = (h) => (h === "local" ? "This Mac" : h || "Unknown host");
const plural = (n, one, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
const andList = (xs) => (xs.length < 3 ? xs.join(" and ") : `${xs.slice(0, -1).join(", ")} and ${xs[xs.length - 1]}`);
const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : "");
const sentence = (s) => (s && !/[.!?]$/.test(s) ? `${s}.` : s || "");
const shellQuote = (s) => `'${s.replace(/'/g, "'\\''")}'`;
// The file a job logs to, in a form tail opens: systemd's file: and append:
// prefixes dropped, and "" for journal, inherit or anything else not a path.
function logPath(log) {
  const path = (log || "").replace(/^(file|append):/, "");
  return path.startsWith("/") || path.startsWith("~/") ? path : "";
}
// Items sync skips, in cooldown or blocked; a tackroom older than 3.0 names the list `held`.
const skippedItems = (d) => d.skipped ?? d.held ?? [];

// A harness of the plan: off when not installed, fail on a conflict or an
// unreadable config, warn while it waits for a sync.
function harnessLevel(a) {
  if (!a.detected) return "off";
  if (a.error || a.conflicts.length) return "fail";
  return a.synced ? "ok" : "warn";
}
const surfaces = (a) => [["Skills", a.skills], ["Roles", a.roles], ["MCP", a.mcp], ["Hooks", a.hooks]];

// A Claude Desktop task's short name is its whole prompt; its id reads better.
const jobName = (j) => (j.source === "claude-desktop" ? j.id : j.short_name);
const jobSchedule = (j) => j.schedule_text || j.schedule || (j.source === "claude-desktop" ? "One-time" : "");
const key = (j) => `${j.host}/${j.source}/${j.id}`;

// Why a job needs attention, as one sentence, and the path a problem names on
// a line of its own: "path does not exist: /Users/…" reads "Path does not exist."
function jobReason(j) {
  const text = j.problems?.[0]
    || (j.health === "warn" ? (j.last_exit != null ? `last run exited ${j.last_exit}` : `last run ${j.last_status || "failed"}`)
      : j.health === "idle" ? "scheduled once, has not run yet" : "");
  const m = /^(.*?):\s+(\/.+|~\/.+)$/.exec(text);
  return m ? { why: sentence(cap(m[1])), path: m[2] } : { why: sentence(cap(text)), path: "" };
}

// Hosts a crons collect could not probe: every source not scanned, one because the probe failed.
function downHosts(d) {
  return [...new Set(d.access.map((a) => a.host))].filter((h) => {
    const access = d.access.filter((a) => a.host === h);
    return access.every((a) => a.state === "not_scanned") && access.some((a) => (a.detail || "").startsWith("probe failed"));
  });
}
const downDetail = (d, h) => d.access.find((a) => a.host === h && (a.detail || "").startsWith("probe failed"))?.detail ?? "";

// Healthy, paused and one-time jobs not run yet, as "12 healthy, 2 paused, 1 not run yet".
function restCounts(jobs) {
  const n = (h) => jobs.filter((j) => j.health === h).length;
  return [n("ok") ? `${n("ok")} healthy` : "", n("paused") ? `${n("paused")} paused` : "", n("idle") ? `${n("idle")} not run yet` : ""]
    .filter(Boolean).join(", ");
}

// A cooldown row's short line: "owner/repo@4b4c947 · 37h left of 72h". The reason reads
// "<git|npm|pypi>:<source>@<version> published <time> (...)"; with no usable `until`, the reason itself.
function cooldownLine(h) {
  const until = Date.parse(h.until);
  const ref = /^(git|npm|pypi):(\S+)@(\S+)/.exec(h.reason ?? "");
  if (!until || !ref) return h.reason;
  const source = ref[1] === "git" ? (/([^/:]+\/[^/]+?)(?:\.git)?$/.exec(ref[2])?.[1] ?? ref[2]) : ref[2];
  const version = ref[1] === "git" ? ref[3].slice(0, 7) : ref[3];
  const published = Date.parse(/ published (\S+)/.exec(h.reason)?.[1]);
  const left = Math.max(0, Math.ceil((until - Date.now()) / 36e5));
  const of = published && until > published ? ` of ${Math.round((until - published) / 36e5)}h` : "";
  return `${source}@${version} · ${left}h left${of}`;
}

function ago(iso) {
  const t = Date.parse(iso);
  if (!t) return "";
  const m = Math.round((Date.now() - t) / 60e3);
  if (m < 1) return "just now";
  if (m < 60) return `${m} min ago`;
  const h = Math.round(m / 60);
  return h < 48 ? `${h} h ago` : `${Math.round(h / 24)} d ago`;
}

function clock(ms) {
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

const lastLine = (r) => (r.stderr || r.stdout || "").trim().split("\n").pop() || `exit ${r.code}`;

export default class Tackroom extends View {
  init(_props, cx) {
    const p = props() ?? {};
    // The add, edit and delete dialog is this mod opened as an overlay.
    if (p.form) { this.form = new JobForm(); this.form.init(p, cx); return; }
    this.bin = p.tackroom ?? "tackroom";
    this.config = p.config ?? "";
    this.tab = "overview";
    this.src = Object.fromEntries(Object.keys(SOURCES).map((name) => [name, { data: null, error: "", loading: false, again: false, at: 0 }]));
    this.busy = "";
    this.open = new Set();
    // A crons collect probes every host over SSH, so poll gently; Refresh reloads the open tab at once.
    cx.timer.every(Math.max(60, p.refresh ?? 300) * 1000, (cx) => Object.keys(SOURCES).forEach((name) => this.load(name, cx)));
    // The first loads start from a timer: an exec started in init never
    // returned (Herdr 0.1.15, a panel created hidden or covered), which kept
    // every source "loading" and the panel on "Loading…" for good.
    cx.timer.after(300, (cx) => Object.keys(SOURCES).forEach((name) => this.load(name, cx)));
    // The dialog sends "reload" after it changes a job and "synced" after a sync.
    cx.spawn(async (cx) => {
      for (;;) {
        const c = await nextCommand();
        if (c === "reload") this.load("crons", cx);
        if (c === "synced") ["plan", "inventory", "hosts"].forEach((name) => this.load(name, cx));
      }
    });
  }

  // args is ["cron", verb, ...]; --config goes right after the verb, because
  // Go's flag parsing stops at the first positional argument (the job id).
  tackroom(args, timeout) {
    return runTackroom({ bin: this.bin, config: this.config }, args, timeout);
  }

  openForm(mode, j, plan) {
    const job = j && { id: j.id, short_name: j.short_name, declared_schedule: j.declared_schedule, declared_command: j.declared_command, effect: j.effect };
    openOverlay({ mod: "tackroom", kind: "dialog", width: 420, props: { form: { mode, job, plan }, tackroom: this.bin, config: this.config } });
  }

  // Edits need a tackroom with `config set` and `sync --plan-digest`. Its dry run
  // prints plan_digest; an older one does not, and the panel stays read-only,
  // since Herdr's gallery serves this mod from main, ahead of a release.
  canEdit() {
    return Boolean(this.src.plan.data?.plan_digest);
  }

  openSync() {
    const d = this.src.plan.data;
    this.openForm("sync", null, { lines: d.plan, destructive: d.destructive, digest: d.plan_digest });
  }

  // One field of tackroom.yaml (or tackroom.local.yaml, where the entry is
  // defined there), as the web view's switches and cells change it. The plan
  // and inventory load again, so the Overview shows the sync waiting.
  configSet(path, value, done, cx) {
    this.busy = `cfg:${path}`;
    cx.notify();
    cx.spawn(async (cx) => {
      try {
        const r = await this.tackroom(["config", "set", path, JSON.stringify(value)], 60000);
        if (r.code === 0) notify({ title: done, body: "Saved. Sync applies it.", level: "success", timeout: 3000 });
        else notify({ title: `Could not change ${path}`, body: lastLine(r), level: "error" });
      } catch (e) {
        notify({ title: `Could not change ${path}`, body: String(e?.message ?? e), level: "error" });
      } finally {
        this.busy = "";
      }
      ["plan", "inventory"].forEach((name) => this.load(name, cx));
    });
  }

  // Adds or removes one agent from an MCP server's or hook's targets, as a
  // matrix cell of the web view does. An entry without an agents list targets
  // every agent that supports it, so the list starts from those.
  toggleTarget(r, agent, targeted, cx) {
    const inv = this.src.inventory.data;
    const order = inv.agents.map((a) => a.name);
    const supports = r.kind === "mcp" ? "supports_mcp" : "supports_hooks";
    const current = r.entry.explicit ? [...r.entry.targets] : inv.agents.filter((a) => a[supports]).map((a) => a.name);
    const next = (targeted ? current.filter((n) => n !== agent) : [...new Set([...current, agent])])
      .sort((a, b) => order.indexOf(a) - order.indexOf(b));
    if (!next.length) {
      notify({ title: `${r.name} needs at least one agent`, body: "Turn it off instead.", level: "error" });
      return;
    }
    this.configSet(`${r.section}/${r.name}/agents`, next, `${r.name} ${targeted ? "removed from" : "added to"} ${agent}`, cx);
  }

  // Turn on or Turn off for an agent, MCP server or hook.
  switchButton(section, name, enabled, t) {
    const path = `${section}/${name}/enabled`;
    return this.link(`on:${path}`, this.busy === `cfg:${path}` ? "…" : enabled ? "Turn off" : "Turn on",
      (cx) => this.configSet(path, !enabled, `${name} turned ${enabled ? "off" : "on"}`, cx), this.busy !== "", t);
  }

  load(name, cx) {
    // A load asked for while one runs (say, right after an action) runs
    // next, so the panel never keeps data from before the action.
    const s = this.src[name];
    if (s.loading) { s.again = true; return; }
    s.loading = true;
    s.again = false;
    cx.notify();
    cx.spawn(async (cx) => {
      try {
        const r = await this.tackroom(SOURCES[name].args, SOURCES[name].timeout);
        if (r.code !== 0) throw new Error(lastLine(r));
        s.data = JSON.parse(r.stdout);
        s.error = "";
        s.at = Date.now();
      } catch (e) {
        s.error = String(e?.message ?? e);
      }
      s.loading = false;
      cx.notify();
      if (s.again) this.load(name, cx);
    });
  }

  act(job, verb, cx) {
    this.busy = key(job);
    cx.notify();
    cx.spawn(async (cx) => {
      try {
        const r = await this.tackroom(["cron", verb, "--host", job.host, "--source", job.source, job.id], 120000);
        if (r.code === 0) notify({ title: `${DONE[verb]} ${job.short_name}`, level: "success", timeout: 3000 });
        else notify({ title: `Could not ${verb} ${job.short_name}`, body: lastLine(r), level: "error" });
      } catch (e) {
        notify({ title: `Could not ${verb} ${job.short_name}`, body: String(e?.message ?? e), level: "error" });
      } finally {
        this.busy = "";
      }
      this.load("crons", cx);
    });
  }

  // A job's log in a new terminal tab, followed as it grows: tail on this Mac,
  // or tail over ssh on the host the job runs on. exec and terminal.open do not
  // expand ~, so this Mac's ~/ is expanded here; a remote ~/ stays outside the
  // quotes for the remote shell to expand. -- keeps a path or host that starts
  // with - from reading as an option.
  openLog(j, cx) {
    const path = logPath(j.log);
    cx.spawn(async () => {
      try {
        const argv = j.host === "local" ? ["tail", "-n", "200", "-F", "--", await expand(path)]
          : ["ssh", "-t", "--", j.host, `tail -n 200 -F -- ${path.startsWith("~/") ? `~/${shellQuote(path.slice(2))}` : shellQuote(path)}`];
        run("terminal.open", { argv, name: `${jobName(j)} log`, place: "tab" });
      } catch (e) {
        notify({ title: `Could not open the log of ${jobName(j)}`, body: String(e?.message ?? e), level: "error" });
      }
    });
  }

  // `tackroom view --json` starts the view when none runs and prints its signed-in
  // URL; Web opens that on the matching tab.
  openWeb(tab, cx) {
    cx.spawn(async () => {
      try {
        const r = await this.tackroom(["view", "--json"], 30000);
        // A tackroom without `view --json` (2.8 and older): open the running view
        // from the files it saved, as this button did before.
        if (r.code !== 0 && r.stderr.includes("flag provided but not defined")) {
          const opened = await exec("/bin/sh", ["-c", 'd="${XDG_STATE_HOME:-$HOME/.local/state}/tackroom"; open "http://$(tr -d "\\n" < "$d/view.addr")/?token=$(tr -d "\\n" < "$d/view.token")#$1"', "sh", tab.web]);
          if (opened.code !== 0) throw new Error(lastLine(opened));
          return;
        }
        if (r.code !== 0) throw new Error(lastLine(r));
        const opened = await exec("/usr/bin/open", [`${JSON.parse(r.stdout).url}#${tab.web}`]);
        if (opened.code !== 0) throw new Error(lastLine(opened));
      } catch (e) {
        notify({ title: "Could not open tackroom view", body: String(e?.message ?? e), level: "error" });
      }
    });
  }

  // A harness's name as the inventory shows it ("Claude Code"), else its id.
  display(name) {
    return (this.src.inventory.data?.agents ?? []).find((a) => a.name === name)?.display_name || name;
  }

  // The machines of `tackroom hosts`, only with more than one host in crons.hosts.
  machines() {
    const m = this.src.hosts.data;
    return m && m.hosts.length > 1 ? m.hosts : [];
  }

  // What the Overview answers, from every source. The headline, the rows under
  // it and the tab's badge all come from here, so they cannot disagree.
  // Failing: a failing cron, a harness with a conflict or an unreadable config,
  // a failing machine (unreachable, not set up, a major version behind), a host
  // the crons collect could not reach. Needs attention: a cron whose last run
  // failed, a sync waiting, the machines that need attention (one row for them
  // all, as in the web view), a source the audit blocked, and a source that did
  // not load. pending: sources still loading for the first time.
  overviewModel() {
    const d = this.src.plan.data;
    const c = this.src.crons.data;
    const jobs = c?.jobs ?? [];
    const machines = this.machines();
    const sick = machines.filter((m) => m.health !== "ok" && m.health !== "fail");
    const fail = [
      ...jobs.filter((j) => j.health === "fail").map((job) => ({ kind: "job", job })),
      ...(d?.agents ?? []).filter((a) => harnessLevel(a) === "fail").map((a) => ({ kind: "harness", a })),
      ...machines.filter((m) => m.health === "fail").map((m) => ({ kind: "machines", hosts: [m] })),
      ...(c ? downHosts(c) : []).filter((h) => !machines.some((m) => m.host === h && m.health !== "ok")).map((host) => ({ kind: "down", host })),
    ];
    const warn = [
      ...jobs.filter((j) => j.health === "warn").map((job) => ({ kind: "job", job })),
      ...(d && !d.synced ? [{ kind: "sync" }] : []),
      ...(sick.length ? [{ kind: "machines", hosts: sick }] : []),
      ...(d ? skippedItems(d) : []).filter((h) => h.kind === "blocked").map((h) => ({ kind: "blocked", h })),
      ...["plan", "crons"].filter((name) => !this.src[name].data && this.src[name].error).map((name) => ({ kind: "error", name })),
    ];
    const pending = ["plan", "crons", "hosts"].filter((name) => !this.src[name].data && !this.src[name].error);
    return { fail, warn, pending };
  }

  // The badge after a tab's label: how many things fail, else how many need attention.
  badge(tab) {
    let levels;
    if (tab.id === "overview") {
      const o = this.overviewModel();
      levels = [...o.fail.map(() => "fail"), ...o.warn.map(() => "warn")];
    } else if (tab.id === "crons") {
      const d = this.src.crons.data;
      if (!d) return null;
      levels = [...d.jobs.map(jobLevel), ...downHosts(d).map(() => "fail")];
    } else {
      if (!this.src.inventory.data) return null;
      levels = [...this.inventoryRows(tab.id).map((r) => r.level), ...(tab.id === "skills" ? this.orphanBlocked().map(() => "warn") : [])];
    }
    const level = worst(levels.filter(bad));
    return bad(level) ? { level, count: levels.filter((l) => l === level).length } : null;
  }

  // The inventory as rows of the Skills, Roles and MCP & hooks tabs: a name, a
  // tag, the state in each harness, its level and the lines shown when it opens.
  inventoryRows(id) {
    const inv = this.src.inventory.data;
    const wired = (list, kind) => list.map((w) => ({
      name: w.name, tag: w.enabled ? w.event || "" : "turned off", states: w.states, kind, entry: w,
      section: kind === "mcp" ? "mcp_servers" : "hooks",
      fields: [["Command", w.command], ["Targets", w.explicit ? w.targets.join(", ") : "all agents"]],
    }));
    let rows;
    if (id === "skills") {
      rows = inv.skills.map((s) => {
        const lock = s.origin === "local" ? null
          : (inv.lock ?? []).find((l) => (l.state === "blocked" || l.state === "cooldown") && sourceRepo(l) === s.origin.split("@")[0]) ?? null;
        return {
          name: s.name, states: s.states, kind: "skill", note: s.description, lock,
          tag: lock?.state === "blocked" ? "source blocked" : lock ? "in cooldown" : s.origin === "local" ? "" : s.origin.replace(/^[^/]+\//, ""),
          fields: [["Origin", s.origin === "local" ? "yours" : s.origin], ["Path", s.path.replace(inv.home, "~")],
            ...(lock ? [["Source", lock.state === "blocked" ? "blocked by the audit" : `in cooldown${lock.until ? ` until ${lock.until}` : ""}`],
              ["Reason", lock.state === "blocked" ? lock.reason : ""], ["Override", lock.command]] : [])],
        };
      });
    } else if (id === "roles") {
      rows = inv.roles.map((r) => ({ name: r.name, tag: "", states: r.states, kind: "role", fields: [] }));
    } else {
      rows = [...wired(inv.mcp, "mcp"), ...wired(inv.hooks, "hook")];
    }
    // A skill of a source the audit blocked needs attention, whatever its states;
    // one of a source in cooldown stays with the rest, as on Overview: sync waits
    // for the cooldown to end on its own.
    return rows.map((r) => {
      const level = worst(Object.values(r.states).map(cellLevel));
      return { ...r, level: r.lock?.state === "blocked" ? worst([level, "warn"]) : r.lock && level === "warn" ? "off" : level };
    });
  }

  // Outside sources the audit blocked that no skill here comes from: they need attention on Skills too.
  orphanBlocked() {
    const inv = this.src.inventory.data;
    const rows = this.inventoryRows("skills");
    return (inv.lock ?? []).filter((l) => l.state === "blocked" && !rows.some((r) => r.lock === l));
  }

  // The rows that need attention, named, as the web view's line under its answer:
  // "agent-insights is missing in Claude Code. 2 more need attention too."
  lead(rows) {
    const wrong = (states) => {
      const by = {};
      for (const [a, state] of Object.entries(states)) if (bad(cellLevel(state))) (by[state] = by[state] || []).push(a);
      return Object.keys(WRONG).filter((state) => by[state]).map((state) => `${WRONG[state]} ${by[state].length <= 2
        ? andList(by[state].map((a) => this.display(a))) : plural(by[state].length, "harness", "harnesses")}`).join(" and ");
    };
    const named = rows.filter((r) => wrong(r.states));
    return [...named.slice(0, 3).map((r) => `${r.name} ${wrong(r.states)}.`), named.length > 3 ? `${named.length - 3} more need attention too.` : ""].filter(Boolean).join(" ");
  }

  // ---------- pieces every tab draws with ----------

  // An action in the theme's accent, as the web view's text buttons.
  link(id, label, onClick, disabled, t) {
    return div().id(id).flex_none().text_size(12).font_medium().text_color(t.accent)
      .when(disabled, (el) => el.opacity(0.5))
      .when(!disabled, (el) => el.cursor_pointer().hover((s) => s.opacity(0.75)))
      .on_click((_e, cx) => { if (!disabled) onClick(cx); })
      .child(label);
  }

  // A quiet button of the header.
  button(id, label, onClick, disabled, t) {
    return h_flex().id(id).h(24).px(8).flex_none().items_center().rounded(6).text_size(12).text_color(t.muted)
      .when(disabled, (el) => el.opacity(0.5))
      .hover((s) => s.bg(t.hover))
      .on_click((_e, cx) => { if (!disabled) onClick(cx); })
      .child(label);
  }

  dot(color, size = 8) {
    return div().w(size).h(size).flex_none().rounded(size / 2).bg(color);
  }

  // A row's status mark: a dot only for what fails or needs attention, so healthy rows stay quiet.
  mark(level, t) {
    return bad(level) ? this.dot(tone(t, level)) : div().w(8).h(8).flex_none();
  }

  note(text, t) {
    return div().px(6).text_size(12).text_color(t.dim).child(text);
  }

  field(label, value, t) {
    return h_flex().gap(8).items_start()
      .child(div().w(62).flex_none().text_color(t.dim).child(label))
      .child(div().flex_1().min_w(0).text_color(t.muted).child(String(value)));
  }

  toggle(k, cx) {
    if (this.open.has(k)) this.open.delete(k);
    else this.open.add(k);
    cx.notify();
  }

  // A line that opens into its details when clicked; detail() builds them only while open.
  fold(k, head, detail, t, { start = false, indent = 22 } = {}) {
    const open = this.open.has(k);
    const row = h_flex().id(k).gap(8).px(6).py(4).rounded(5)
      .when(start, (el) => el.items_start())
      .when(!start, (el) => el.items_center())
      .when(open, (el) => el.bg(t.raised))
      .hover((s) => s.bg(t.hover))
      .on_click((_e, cx) => this.toggle(k, cx))
      .children(head);
    return open ? v_flex().gap(4).child(row).child(detail().ml(indent).mb(6)) : row;
  }

  // The answer at the top of a tab: a status dot, one line that says it, and a line under it.
  answer(level, title, sub, t, right) {
    return h_flex().gap(10).px(6).items_start()
      .child(this.dot(tone(t, level), 12).mt(6))
      .child(v_flex().flex_1().min_w(0).gap(3)
        .child(div().text_size(18).font_semibold().child(title))
        .when(Boolean(sub), (el) => el.child(div().text_size(12).text_color(t.muted).child(sub))))
      .when(Boolean(right), (el) => el.child(h_flex().flex_none().pt(4).child(right)));
  }

  // A group under a sentence-case title in its level's colour, with an optional
  // dim note after the title and a column label over the right-hand numbers.
  group(title, level, rows, t, { gap = 2, note = "", column = "" } = {}) {
    return v_flex().gap(gap)
      .child(h_flex().gap(8).px(6).items_center().text_size(11)
        .child(div().flex_none().font_semibold().text_color(level ? tone(t, level) : t.muted).child(title))
        .child(div().flex_1().min_w(0).truncate().text_color(t.dim).child(note))
        .when(Boolean(column), (el) => el.child(div().flex_none().w(48).text_right().text_color(t.dim).child(column))))
      .children(rows);
  }

  // One thing that fails or needs attention: its name and where, why, a path it
  // names, then its actions and when it last ran. Details opens what else there is.
  card(k, c, t) {
    const open = this.open.has(k);
    const acts = [...(c.acts ?? [])];
    if (c.detail) acts.push(this.link(`${k}:details`, open ? "Hide details" : "Details", (cx) => this.toggle(k, cx), false, t));
    return v_flex().gap(3).px(6)
      .child(h_flex().gap(8).items_center()
        .child(div().flex_1().min_w(0).truncate().text_size(13).font_semibold().child(c.name))
        .when(Boolean(c.where), (el) => el.child(div().flex_none().max_w(170).truncate().text_size(11).text_color(t.muted).child(c.where))))
      .when(Boolean(c.why), (el) => el.child(div().text_size(12).text_color(t.muted).child(c.why)))
      .when(Boolean(c.path), (el) => el.child(div().min_w(0).overflow_hidden().whitespace_nowrap().text_ellipsis_start().text_size(11).text_color(t.dim).child(c.path)))
      .children((c.notes ?? []).filter(Boolean).map((n) => div().text_size(11).text_color(t.dim).child(n)))
      .when(acts.length > 0 || Boolean(c.when), (el) => el.child(h_flex().gap(14).pt(3).items_center()
        .children(acts)
        .child(div().flex_1())
        .when(Boolean(c.when), (row) => row.child(div().flex_none().text_size(11).text_color(t.dim).child(c.when)))))
      .when(open && Boolean(c.detail), (el) => el.child(c.detail().pt(4)));
  }

  // A line of "Everything else": a dot, what it says and the lines under it. It
  // opens into detail(), or goes to another tab.
  line(k, level, title, subs, t, { detail, go } = {}) {
    const head = [this.dot(tone(t, level)).mt(5),
      v_flex().flex_1().min_w(0).gap(2)
        .child(div().text_size(13).font_medium().text_color(level === "off" ? t.muted : t.foreground).child(title))
        .children(subs.filter(Boolean).map((s) => div().text_size(12).text_color(t.muted).child(s)))];
    if (detail) return this.fold(k, head, detail, t, { start: true });
    return h_flex().id(k).gap(8).px(6).py(4).items_start().rounded(5)
      .when(Boolean(go), (el) => el.cursor_pointer().hover((s) => s.bg(t.hover)).on_click((_e, cx) => { this.tab = go; cx.notify(); }))
      .children(head);
  }

  tabButton(tab, t) {
    const on = this.tab === tab.id;
    const b = this.badge(tab);
    return h_flex().id(`tab-${tab.id}`).pt(4).gap(5).flex_none().items_center().text_size(12)
      .text_color(on ? t.foreground : t.muted)
      .when(on, (el) => el.pb(6).border_b(2).border_color(t.accent))
      .when(!on, (el) => el.pb(8).cursor_pointer().hover((s) => s.text_color(t.foreground)))
      .on_click((_e, cx) => { this.tab = tab.id; cx.notify(); })
      .child(tab.label)
      .when(Boolean(b), (el) => el.child(div().text_size(11).font_semibold().text_color(tone(t, b?.level)).child(String(b?.count))));
  }

  render(cx) {
    if (this.form) return this.form.render(cx);
    const t = theme();
    const tab = TABS.find((x) => x.id === this.tab);
    const srcs = tab.sources.map((name) => this.src[name]);
    const loading = srcs.some((s) => s.loading);
    const times = srcs.filter((s) => s.at).map((s) => s.at);
    // Refresh reloads every source of the open tab that is not loading already.
    const header = h_flex().flex_none().h(40).pl(14).pr(8).gap(4).items_center()
      .child(svg("mods/tackroom/tackroom.svg").size(14).text_color(t.muted))
      .child(div().flex_1().pl(4).text_size(13).font_semibold().child("tackroom"))
      .child(div().pr(4).text_size(11).text_color(t.dim).child(loading ? "loading…" : times.length ? clock(Math.min(...times)) : ""))
      .child(this.button("refresh", "Refresh", (cx) => tab.sources.filter((name) => !this.src[name].loading).forEach((name) => this.load(name, cx)),
        srcs.every((s) => s.loading), t))
      .child(this.button("web", "Web", (cx) => this.openWeb(tab, cx), false, t));
    const strip = h_flex().flex_none().flex_wrap().gap_x(14).px(14).border_b(1).border_color(t.border)
      .children(TABS.map((x) => this.tabButton(x, t)));

    // The Overview draws whatever has loaded, and says what has not; the other
    // tabs wait for their one source.
    const s = srcs[0];
    const ready = tab.id === "overview" ? srcs.some((x) => x.data || x.error) : Boolean(s.data);
    const items = [];
    if (s.error && (s.data || tab.id !== "overview")) items.push(div().px(6).text_size(11).text_color(t.ansi1).child(s.data ? `Last load failed: ${s.error}` : s.error));
    if (!ready && !s.error) items.push(this.note("Loading…", t));
    if (ready) items.push(...this[tab.id](t));
    // One scroll position for each tab.
    return v_flex().size_full().bg(t.surface).text_color(t.foreground)
      .child(header).child(strip)
      .child(v_flex().id(`tackroom-${tab.id}`).flex_1().min_h(0).overflow_y_scrollbar().px(8).pt(16).pb(16).gap(22).children(items));
  }

  // ---------- Overview ----------

  overview(t) {
    const o = this.overviewModel();
    const d = this.src.plan.data;
    const title = [o.fail.length ? `${o.fail.length} failing` : "", o.warn.length ? `${o.warn.length} need${o.warn.length === 1 ? "s" : ""} attention` : ""]
      .filter(Boolean).join(", ") || (o.pending.length ? "Checking…" : "All clear");
    const level = o.fail.length ? "fail" : o.warn.length ? "warn" : o.pending.length ? "off" : "ok";
    // The line under it: whether the harnesses are in sync, what the failures
    // are when they are all one kind, and what is still loading.
    const subs = [];
    const live = (d?.agents ?? []).filter((a) => a.detected);
    if (d && d.synced && live.length && live.every((a) => harnessLevel(a) === "ok")) subs.push(live.length === 1 ? "The installed harness is in sync." : `All ${live.length} installed harnesses are in sync.`);
    const kinds = { job: ["a cron", "crons"], harness: ["a harness", "harnesses"], machines: ["a machine", "machines"], down: ["a machine", "machines"] };
    const failKind = (i) => (i.kind === "down" ? "machines" : i.kind);
    const failKinds = [...new Set(o.fail.map(failKind))];
    if (failKinds.length === 1) {
      const [one, many] = kinds[failKinds[0]];
      subs.push(o.fail.length === 1 ? `The failure is ${one}.` : `${o.fail.length === 2 ? "Both failures" : "The failures"} are ${many}.`);
    }
    if (o.pending.length) subs.push(`Still checking ${andList(o.pending.map((name) => ({ plan: "harnesses", crons: "crons", hosts: "machines" })[name]))}.`);

    const out = [this.answer(level, title, subs.join(" "), t)];
    if (o.fail.length) out.push(this.group("Failing", "fail", o.fail.map((i) => this.item(i, t)), t, { gap: 14 }));
    if (o.warn.length) out.push(this.group("Needs attention", "warn", o.warn.map((i) => this.item(i, t)), t, { gap: 14 }));
    const rest = this.restLines(t);
    if (rest.length) out.push(this.group("Everything else", "", rest, t, { gap: 6 }).pt(18).border_t(1).border_color(t.border));
    return out;
  }

  // One row of Failing or Needs attention, with its fix or its actions.
  item(i, t) {
    const d = this.src.plan.data;
    if (i.kind === "job") return this.jobCard(i.job, t);
    if (i.kind === "harness") {
      const a = i.a;
      return this.card(`hx:${a.name}`, {
        name: this.display(a.name), where: a.error ? "config unreadable" : plural(a.conflicts.length, "conflict"),
        why: sentence(a.error || a.conflicts[0]), detail: () => this.harnessDetail(a, t),
      }, t);
    }
    if (i.kind === "sync") {
      const out = d.agents.filter((a) => harnessLevel(a) === "warn");
      const count = (kind) => out.reduce((n, a) => n + surfaces(a).reduce((m, [, s]) => m + s[kind].length, 0), 0);
      const what = [count("missing") ? `${count("missing")} missing` : "", count("drifted") ? `${count("drifted")} drifted` : ""].filter(Boolean).join(", ");
      const name = d.changes > 0 ? `${plural(d.changes, "change")} waiting to sync`
        : out.length ? `${plural(out.length, "harness", "harnesses")} out of sync` : `The config root link is ${d.repo_link}`;
      return this.card("sync", {
        name,
        why: out.length ? `${andList(out.map((a) => this.display(a.name)))} ${out.length === 1 ? "needs" : "need"} it${what ? `: ${what}` : ""}.` : "",
        notes: [
          d.repo_link !== "synced" && out.length ? `The config root link is ${d.repo_link}.` : "",
          d.not_fetched.length ? `A sync would first fetch ${d.not_fetched.join(", ")}.` : "",
          this.canEdit() ? "Sync shows the plan before it changes anything." : "Run tackroom sync to apply it. Update tackroom to sync and edit from this panel.",
        ],
        acts: this.canEdit() ? [this.link("sync-open", "Sync", () => this.openSync(), this.busy !== "" || this.src.plan.loading, t)] : [],
        detail: out.length ? () => v_flex().gap(2).children(out.map((a) => this.harnessRow(a, t))) : null,
      }, t);
    }
    if (i.kind === "machines") {
      // One machine reads as its name and summary; several with the same summary
      // share it as the title, as in the web view. Each line under it is a fix:
      // `tackroom hosts`'s, or a look at the uncommitted changes it counts.
      const many = i.hosts.length > 1;
      const same = many && new Set(i.hosts.map((m) => m.summary)).size === 1;
      const home = this.src.inventory.data?.home;
      const fix = (m) => m.fix || (m.dirty > 0 && m.config_root
        ? `${m.host === "local" ? "" : `ssh ${m.host} `}git -C ${m.host === "local" && home ? m.config_root.replace(home, "~") : m.config_root} status` : "");
      return this.card(`machines:${i.hosts.map((m) => m.host).join(",")}`, {
        name: !many ? hostName(i.hosts[0].host) : same ? i.hosts[0].summary.replace(/^1 /, "One ") : "Machines need attention",
        where: !many ? MACHINE[i.hosts[0].health] ?? i.hosts[0].health : andList(i.hosts.map((m) => hostName(m.host))),
        why: same ? "" : i.hosts.map((m) => `${many ? `${hostName(m.host)}: ` : ""}${sentence(m.summary)}`).join(" "),
        notes: i.hosts.filter(fix).map((m) => `${many ? `${hostName(m.host)}: ` : "Fix: "}${fix(m)}`),
      }, t);
    }
    if (i.kind === "blocked") {
      return this.card(`blocked:${i.h.item}`, { name: i.h.item, where: "blocked", why: sentence(cap(i.h.reason)), notes: [`Override: ${i.h.command}`] }, t);
    }
    if (i.kind === "down") {
      const c = this.src.crons.data;
      return this.card(`down:${i.host}`, { name: `${hostName(i.host)} unreachable`, where: "crons", why: sentence(cap(downDetail(c, i.host))) }, t);
    }
    // A source that did not load.
    return this.card(`error:${i.name}`, {
      name: i.name === "plan" ? "The sync plan did not load" : "Crons did not load", why: this.src[i.name].error,
      acts: [this.link(`retry:${i.name}`, "Retry", (cx) => this.load(i.name, cx), this.src[i.name].loading, t)],
    }, t);
  }

  // Everything that is healthy or off, folded to one line per kind.
  restLines(t) {
    const d = this.src.plan.data;
    const c = this.src.crons.data;
    const inv = this.src.inventory.data;
    const lines = [];
    const ok = (d?.agents ?? []).filter((a) => harnessLevel(a) === "ok");
    if (ok.length) {
      lines.push(this.line("rest:harnesses", "ok", `${plural(ok.length, "harness", "harnesses")} in sync${d.synced ? ", nothing to sync" : ""}`, [
        inv ? [plural(inv.skills.length, "skill"), plural(inv.roles.length, "role"), plural(inv.mcp.length, "MCP server"), plural(inv.hooks.length, "hook")].join(", ") : "",
        ok.map((a) => this.display(a.name)).join(", "),
        d.synced && d.not_fetched.length ? `A sync would first fetch ${d.not_fetched.join(", ")}.` : "",
      ], t, { detail: () => v_flex().gap(2).children(ok.map((a) => this.harnessRow(a, t))) }));
    }
    if (c && c.jobs.length) {
      const healthy = c.jobs.filter((j) => j.health === "ok").length;
      const quiet = c.jobs.filter((j) => j.health === "paused" || j.health === "idle").length;
      const sub = restCounts(c.jobs.filter((j) => j.health === "paused" || j.health === "idle"));
      if (healthy || quiet) lines.push(this.line("rest:crons", healthy ? "ok" : "off", healthy ? `${plural(healthy, "cron")} healthy` : plural(quiet, "cron"), [sub], t, { go: "crons" }));
    }
    const hosts = this.machines();
    if (hosts.length) {
      const commits = new Set(hosts.map((m) => m.commit));
      const same = commits.size === 1 && hosts[0].commit;
      const versions = new Map();
      for (const m of hosts) if (m.version) versions.set(m.version, [...(versions.get(m.version) ?? []), hostName(m.host)]);
      lines.push(this.line("rest:machines", same ? "ok" : "off",
        same ? `${hosts.length === 2 ? "Both" : `All ${hosts.length}`} machines on config ${hosts[0].commit.slice(0, 7)}` : plural(hosts.length, "machine"),
        [[...versions].map(([v, names]) => `tackroom ${v} on ${andList(names)}`).join("; ")],
        t, { detail: () => v_flex().gap(6).children(hosts.map((m) => this.machineRow(m, t))) }));
    }
    const pinned = (inv?.lock ?? []).filter((l) => l.state === "pinned");
    if (pinned.length) {
      const findings = pinned.filter((l) => l.findings).map((l) => `${l.name} has ${plural(l.findings, "audit finding")}, ${l.critical ? `${l.critical} critical` : "none critical"}`);
      const unaudited = pinned.filter((l) => !l.audited).length;
      lines.push(this.line("rest:sources", "ok", `${plural(pinned.length, "outside source")} pinned`,
        [[...findings, unaudited ? `${unaudited} not audited yet` : ""].filter(Boolean).join("; ") || "No audit findings"], t, { go: "skills" }));
    }
    const cool = d ? skippedItems(d).filter((h) => h.kind !== "blocked") : [];
    if (cool.length) {
      lines.push(this.line("rest:cooldown", "off", `${cool.length} in cooldown`, cool.map((h) => (h.kind === "cooldown" ? cooldownLine(h) : `${h.item}: ${h.reason}`)), t, {
        detail: () => v_flex().gap(6).text_size(11).children(cool.map((h) => v_flex().gap(3)
          .child(div().text_size(12).child(h.item))
          .when(Boolean(h.until), (el) => el.child(this.field("Cooldown ends", h.until, t)))
          .child(this.field("Override", h.command, t)))),
      }));
    }
    // Harnesses not installed and those turned off in the config share one line;
    // the plan lists the first, the inventory the second.
    const absent = (d?.agents ?? []).filter((a) => !a.detected);
    const off = (inv?.agents ?? []).filter((a) => !a.enabled);
    if (absent.length || off.length) {
      const idle = [...absent.map((a) => ({ name: a.name, enabled: true })), ...off.map((a) => ({ name: a.name, enabled: false }))];
      lines.push(this.line("rest:idle", "off", [absent.length ? `${absent.length} not installed` : "", off.length ? `${off.length} turned off` : ""].filter(Boolean).join(", "),
        [idle.map((a) => this.display(a.name)).join(", ")], t, {
          detail: () => v_flex().gap(4).text_size(12).children(idle.map((a) => h_flex().gap(8).items_center()
            .child(div().flex_1().min_w(0).truncate().text_color(t.muted).child(this.display(a.name)))
            .child(div().flex_none().text_size(11).text_color(t.dim).child(a.enabled ? "not installed" : "turned off"))
            .when(this.canEdit(), (el) => el.child(h_flex().w(56).flex_none().justify_end().child(this.switchButton("agents", a.name, a.enabled, t)))))),
        }));
    }
    return lines;
  }

  // One harness of the plan: what it holds, and its state. It opens into its details.
  harnessRow(a, t) {
    const level = harnessLevel(a);
    const word = level === "off" ? "not installed" : a.error ? "config unreadable"
      : a.conflicts.length ? plural(a.conflicts.length, "conflict") : level === "warn" ? "needs sync" : "synced";
    const count = (kind) => surfaces(a).reduce((n, [, s]) => n + s[kind].length, 0);
    // What needs attention first: the line wraps instead of cutting it off.
    const summary = a.detected
      ? [...(count("missing") ? [`${count("missing")} missing`] : []), ...(count("drifted") ? [`${count("drifted")} drifted`] : []),
        plural(a.skills.managed.length, "skill"), plural(a.roles.managed.length, "role"), `${a.mcp.managed.length} MCP`, plural(a.hooks.managed.length, "hook")].join(" · ")
      : "";
    return this.fold(`h:${a.name}`,
      [this.mark(level, t),
        v_flex().flex_1().min_w(0)
          .child(div().truncate().text_size(12).child(this.display(a.name)))
          .child(div().text_size(11).text_color(t.dim).child(summary)),
        div().flex_none().text_size(11).text_color(bad(level) ? tone(t, level) : t.dim).child(word)],
      () => this.harnessDetail(a, t), t);
  }

  harnessDetail(a, t) {
    return v_flex().gap(3).text_size(11)
      .when(Boolean(a.error), (el) => el.child(div().text_color(t.ansi1).child(a.error)))
      .children(a.conflicts.map((x) => div().text_color(t.ansi1).child(x)))
      .when(Boolean(a.root_instructions && a.root_instructions.state !== "synced"), (el) => el.child(this.field("Root file", a.root_instructions.state, t)))
      .children(surfaces(a).flatMap(([label, s]) => ["missing", "drifted"].filter((kind) => s[kind].length)
        .map((kind) => this.field(label, `${kind}: ${s[kind].join(", ")}`, t))))
      .when(this.canEdit(), (el) => el.child(h_flex().pt(4).child(this.switchButton("agents", a.name, true, t))));
  }

  // One machine of `tackroom hosts`: its name, summary, the command that fixes it and its health.
  machineRow(m, t) {
    const level = { ok: "ok", warn: "warn", fail: "fail" }[m.health] ?? "warn";
    return h_flex().gap(8).items_start().text_size(12)
      .child(div().w(76).flex_none().truncate().font_semibold().child(hostName(m.host)))
      .child(v_flex().flex_1().min_w(0).gap(2)
        .child(div().text_color(t.muted).child(m.summary))
        .when(Boolean(m.fix), (el) => el.child(div().text_size(11).text_color(t.dim).child(`Fix: ${m.fix}`))))
      .child(div().flex_none().text_size(11).text_color(bad(level) ? tone(t, level) : t.dim).child(MACHINE[m.health] ?? m.health));
  }

  // ---------- Skills, Roles, MCP & hooks ----------

  // A row of an inventory tab: its mark, name, tag and how many of the harnesses
  // it targets have it in place (2/3). It opens into its state in each harness.
  invRow(r, t) {
    const agents = Object.keys(r.states);
    const targeted = agents.filter((a) => !["off", "disabled", "unsupported", "absent"].includes(r.states[a]));
    const inPlace = targeted.filter((a) => r.states[a] === "ok").length;
    return this.fold(`${r.kind}:${r.name}`,
      [this.mark(r.level, t),
        div().flex_1().min_w(0).truncate().text_size(12).text_color(r.level === "off" ? t.muted : t.foreground).child(r.name),
        ...(r.tag ? [div().flex_none().max_w(120).truncate().text_size(11).text_color(t.dim).child(r.tag)] : []),
        div().flex_none().w(48).text_right().text_size(11).text_color(bad(r.level) ? tone(t, r.level) : t.muted).child(targeted.length ? `${inPlace}/${targeted.length}` : "-")],
      () => this.invDetail(r, t), t);
  }

  invDetail(r, t) {
    return v_flex().gap(3).text_size(11)
      .when(Boolean(r.note), (el) => el.child(div().pb(2).text_color(t.muted).child(r.note)))
      .children(r.fields.filter(([, v]) => v).map(([k, v]) => this.field(k, v, t)))
      .child(div().h(4))
      .children(Object.keys(r.states).map((a) => {
        const [word, level] = CELL[r.states[a]] ?? CELL.unknown;
        // MCP servers and hooks can change their targets, as the web view's cells do.
        const targeted = !["off", "unsupported", "absent", "error"].includes(r.states[a]);
        const locked = ["unsupported", "absent", "error"].includes(r.states[a]);
        return h_flex().gap(8).items_center()
          .child(div().flex_1().min_w(0).truncate().text_color(t.muted).child(this.display(a)))
          .child(div().flex_none().text_color(bad(level) ? tone(t, level) : level === "ok" ? t.muted : t.dim).child(word))
          .when(Boolean(r.entry) && this.canEdit(), (el) => el.child(h_flex().w(52).flex_none().justify_end()
            .when(!locked, (box) => box.child(this.link(`t:${r.kind}:${r.name}:${a}`, targeted ? "Remove" : "Add",
              (cx) => this.toggleTarget(r, a, targeted, cx), this.busy !== "", t)))));
      }))
      .when(Boolean(r.entry) && this.canEdit(), (el) => el.child(h_flex().pt(4).child(this.switchButton(r.section, r.name, r.entry.enabled, t))));
  }

  // An inventory tab: the answer, which names what needs attention, then a
  // "Needs attention" group (a card for each blocked source, then the rows),
  // then the rest of each group by name, numbers in one right-hand column.
  // orphans: blocked sources no row comes from, counted in the answer as well.
  invTab(groups, okTitle, sub, noun, t, { lead = "", blocked = [], orphans = 0 } = {}) {
    const all = groups.flatMap(([, rows]) => rows);
    const worse = all.filter((r) => bad(r.level)).sort((a, b) => RANK[b.level] - RANK[a.level] || a.name.localeCompare(b.name));
    const level = worse.length ? worst(["warn", ...worse.map((r) => r.level)]) : orphans ? "warn" : "ok";
    const asks = worse.length + orphans;
    const title = asks ? `${[worse.length ? noun(worse) : "", orphans ? plural(orphans, "outside source") : ""].filter(Boolean).join(" and ")} need${asks === 1 ? "s" : ""} attention` : okTitle;
    const out = [this.answer(level, title, [lead || this.lead(worse), sub].filter(Boolean).join(" "), t)];
    if (asks || blocked.length) {
      out.push(this.group("Needs attention", level, [
        ...(blocked.length ? [v_flex().gap(12).pt(2).pb(8).children(blocked.map((l) => this.card(`lock:${l.name}`, {
          name: sourceRepo(l) || l.name, where: "blocked", why: sentence(cap(l.reason || "blocked by the audit")), notes: [l.command ? `Override: ${l.command}` : ""],
        }, t)))] : []),
        ...worse.map((r) => this.invRow(r, t)),
      ], t, { column: worse.length ? "in place" : "" }));
    }
    for (const [title, rows] of groups) {
      const rest = rows.filter((r) => !bad(r.level)).sort((a, b) => a.name.localeCompare(b.name));
      if (rest.length) out.push(this.group(title, "", rest.map((r) => this.invRow(r, t)), t, { note: String(rest.length), column: "in place" }));
    }
    return out;
  }

  skills(t) {
    const inv = this.src.inventory.data;
    const rows = this.inventoryRows("skills");
    const blocked = (inv.lock ?? []).filter((l) => l.state === "blocked");
    if (!rows.length && !blocked.length) return [this.note("No canonical skills yet. Add one with tackroom skill new <name>.", t)];
    const yours = inv.skills.filter((s) => s.origin === "local").length;
    const sources = new Set(inv.skills.filter((s) => s.origin !== "local").map((s) => s.origin.split("@")[0])).size;
    const from = blocked.map((l) => {
      const names = rows.filter((r) => r.lock === l).map((r) => r.name);
      return names.length ? `${andList(names)} come${names.length === 1 ? "s" : ""} from ${sourceRepo(l) || l.name}, which the audit blocked.`
        : `${sourceRepo(l) || l.name} is blocked by the audit.`;
    });
    const lead = [...from, this.lead(rows.filter((r) => bad(r.level) && r.lock?.state !== "blocked"))].filter(Boolean).join(" ");
    return this.invTab([["Skills", rows]], `${plural(rows.length, "skill")}, in place everywhere`,
      `${yours} yours${sources ? `, ${rows.length - yours} from ${plural(sources, "outside source")}` : ""}.`, (xs) => plural(xs.length, "skill"), t,
      { lead, blocked, orphans: this.orphanBlocked().length });
  }

  roles(t) {
    const inv = this.src.inventory.data;
    const rows = this.inventoryRows("roles");
    if (!rows.length) return [this.note("No agent roles are configured. Roles live in agents/*.md in your config root.", t)];
    const live = inv.agents.filter((a) => a.enabled && a.detected);
    const takes = live.filter((a) => a.supports_roles !== false);
    const not = live.filter((a) => a.supports_roles === false);
    return this.invTab([["Roles", rows]], `${plural(rows.length, "role")}, in place in all ${plural(takes.length, "harness", "harnesses")} that take roles`,
      not.length ? `${andList(not.map((a) => a.display_name || a.name))} ${not.length === 1 ? "does" : "do"} not take roles.` : "",
      (xs) => plural(xs.length, "role"), t);
  }

  wiring(t) {
    const rows = this.inventoryRows("wiring");
    const mcp = rows.filter((r) => r.kind === "mcp");
    const hooks = rows.filter((r) => r.kind === "hook");
    const off = rows.filter((r) => !r.entry.enabled).length;
    const noun = (xs) => (xs.every((r) => r.kind === "mcp") ? plural(xs.length, "MCP server")
      : xs.every((r) => r.kind === "hook") ? plural(xs.length, "hook") : `${xs.length} MCP servers and hooks`);
    const out = rows.length
      ? this.invTab([["MCP servers", mcp], ["Hooks", hooks]], `${plural(mcp.length, "MCP server")} and ${plural(hooks.length, "hook")}, ${off ? `${off} turned off` : "all in place"}`,
        this.canEdit() ? "Each change saves to tackroom.yaml; Sync on Overview applies it." : "Update tackroom to edit from this panel.", noun, t)
      : [];
    if (!mcp.length) out.push(this.note("No MCP servers yet. Add one with tackroom mcp add.", t));
    if (!hooks.length) out.push(this.note("No hooks yet. Add them under hooks: in tackroom.yaml.", t));
    return out;
  }

  // ---------- Crons ----------

  crons(t) {
    const d = this.src.crons.data;
    const jobs = d.jobs;
    const hosts = [...new Set([...d.access.map((a) => a.host), ...jobs.map((j) => j.host)])];
    const down = downHosts(d);
    const fails = jobs.filter((j) => j.health === "fail");
    const warns = jobs.filter((j) => j.health === "warn");
    const title = [jobs.length ? (fails.length ? `${fails.length} of ${jobs.length} jobs failing` : `${plural(jobs.length, "job")}, none failing`) : "",
      ...down.map((h) => `${hostName(h)} unreachable`)].filter(Boolean).join(", ") || "No scheduled jobs";
    const level = fails.length || down.length ? "fail" : warns.length ? "warn" : jobs.length ? "ok" : "off";
    const counts = restCounts(jobs);
    const items = [this.answer(level, title, hosts.length ? `On ${andList(hosts.map(hostName))}${counts ? `: ${counts}` : ""}.` : "", t,
      this.link("add", "Add cron", () => this.openForm("add"), false, t))];
    const failing = [
      ...down.map((h) => this.card(`down:${h}`, { name: `${hostName(h)} unreachable`, why: sentence(cap(downDetail(d, h))) }, t)),
      ...fails.map((j) => this.jobCard(j, t)),
    ];
    if (failing.length) items.push(this.group("Failing", "fail", failing, t, { gap: 14 }));
    if (warns.length) items.push(this.group("Needs attention", "warn", warns.map((j) => this.jobCard(j, t)), t, { gap: 14 }));
    // Everything else, folded per machine: healthy jobs first, then paused and not run yet, by name.
    const rest = hosts.map((h) => {
      const list = jobs.filter((j) => j.host === h && !bad(jobLevel(j)))
        .sort((a, b) => RANK[jobLevel(b)] - RANK[jobLevel(a)] || (a.health === "paused") - (b.health === "paused") || jobName(a).localeCompare(jobName(b)));
      return list.length ? this.group(hostName(h), "", list.map((j) => this.jobLine(j, t)), t, { note: restCounts(list) }) : null;
    }).filter(Boolean);
    if (rest.length) items.push(v_flex().gap(18).pt(18).border_t(1).border_color(t.border).children(rest));
    return items;
  }

  // Run now and Pause, or Resume when it is off, for a launchd job; Log for a job with a log file.
  jobActs(j, prefix, t) {
    const busy = this.busy !== "" || this.src.crons.loading;
    const verbs = j.source === "launchd" && j.unit ? (OFF.includes(j.state) ? ["resume"] : ["run", "pause"]) : [];
    const acts = verbs.map((v) => this.link(`${prefix}:${key(j)}:${v}`, this.busy === key(j) ? "…" : VERB[v], (cx) => this.act(j, v, cx), busy, t));
    if (logPath(j.log)) acts.push(this.link(`${prefix}:${key(j)}:log`, "Log", (cx) => this.openLog(j, cx), false, t));
    return acts;
  }

  // A job that fails or needs attention, on Overview and Crons alike.
  jobCard(j, t) {
    const { why, path } = jobReason(j);
    const home = this.src.inventory.data?.home;
    return this.card(`jc:${key(j)}`, {
      name: jobName(j), where: [hostName(j.host), jobSchedule(j)].filter(Boolean).join(" · "),
      why, path: home && j.host === "local" ? path.replace(home, "~") : path,
      acts: this.jobActs(j, "c", t), when: j.last_run ? ago(j.last_run) : "", detail: () => this.jobDetail(j, false, t),
    }, t);
  }

  // A healthy, paused or not-run-yet job: name, schedule and last run in aligned columns.
  jobLine(j, t) {
    const off = jobLevel(j) === "off";
    const sched = j.health === "paused" ? "Paused" : jobSchedule(j) || (j.state === "missing" ? "missing" : "-");
    const last = j.last_run ? ago(j.last_run) : j.health === "idle" ? "not run" : "-";
    return this.fold(`j:${key(j)}`, [
      div().flex_1().min_w(0).truncate().text_size(12).text_color(off ? t.dim : t.foreground).child(jobName(j)),
      div().flex_none().w(112).truncate().text_size(11.5).text_color(off ? t.dim : t.muted).child(sched),
      div().flex_none().w(64).text_right().text_size(11).text_color(off ? t.dim : t.muted).child(last),
    ], () => this.jobDetail(j, true, t), t, { indent: 6 });
  }

  // Everything about a job, and its buttons; a card shows its first problem and
  // its run actions already, so they are left out there.
  jobDetail(j, all, t) {
    const result = j.last_exit != null ? `exit ${j.last_exit}` : j.last_status;
    const fields = [["Health", (JOB[j.health] ?? [, j.health])[1]], ["Host", hostName(j.host)], ["ID", j.id], ["Command", j.command], ["Effect", j.effect], ["Log", j.log],
      ["Schedule", j.declared_schedule || j.schedule], ["Last run", j.last_run], ["Result", result]].filter(([, v]) => v);
    const busy = this.busy !== "" || this.src.crons.loading;
    const acts = all ? this.jobActs(j, "d", t) : [];
    // The server says which jobs tackroom owns here (editable) and why not for the rest.
    if (j.editable) {
      acts.push(this.link(`d:${key(j)}:edit`, "Edit", () => this.openForm("edit", j), busy, t));
      acts.push(this.link(`d:${key(j)}:delete`, "Delete", () => this.openForm("delete", j), busy, t));
    }
    return v_flex().gap(3).text_size(11)
      .children((j.problems ?? []).slice(all ? 0 : 1).map((p) => div().text_color(t.ansi1).child(p)))
      .children(fields.map(([label, value]) => this.field(label, value, t)))
      .when(acts.length > 0, (el) => el.child(h_flex().gap(14).pt(4).items_center().children(acts)))
      .when(!j.editable && Boolean(j.edit_note), (el) => el.child(div().pt(2).text_color(t.dim).child(`No edit or delete. ${j.edit_note}`)));
  }
}
