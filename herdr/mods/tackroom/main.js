import { View, div, svg } from "gpui-kit";
import { h_flex, v_flex } from "gpui-base";
import { exec, props, theme, notify, openOverlay, nextCommand } from "herdr";
import JobForm, { runTackroom } from "./form.js";

// tackroom inside Herdr, one tab for each tab of the web view. Every tab draws
// the JSON of a tackroom command, the same data the web view draws:
//   Overview    `sync --dry-run --json`  harness state, held and blocked items
//   Skills, Roles, MCP & hooks   `config inventory`  what /api/inventory serves
//   Crons       `cron collect`  and acts through `cron run|pause|resume`
// Add, Edit and Delete on a cron open form.js as a dialog of this same mod.
// Nothing here writes except the cron actions: sync stays with the CLI and the
// web view, because the plan JSON does not list every change a sync makes.

const SOURCES = {
  plan: { args: ["sync", "--dry-run", "--json"], timeout: 120000 },
  inventory: { args: ["config", "inventory"], timeout: 120000 },
  crons: { args: ["cron", "collect"], timeout: 300000 },
};
// web is the tab of the web view the Web button opens.
const TABS = [
  { id: "overview", label: "Overview", source: "plan", web: "overview" },
  { id: "skills", label: "Skills", source: "inventory", web: "skills" },
  { id: "roles", label: "Roles", source: "inventory", web: "roles" },
  { id: "wiring", label: "MCP & hooks", source: "inventory", web: "wiring" },
  { id: "crons", label: "Crons", source: "crons", web: "crons" },
];

// A launchd job that is off offers Resume, as in the web view.
const OFF = ["disabled", "not_loaded"];
const HEALTH = { fail: "Failing", warn: "Last run failed", idle: "No result", ok: "Healthy", paused: "Paused" };
const ORDER = ["fail", "warn", "idle", "ok", "paused"];
const DONE = { run: "Started", pause: "Paused", resume: "Resumed" };
// What a matrix cell of the inventory says, in the web view's words, and the tone it takes.
const CELL = {
  ok: ["in place", "ok"], drift: ["drifted", "warn"], missing: ["missing", "warn"], conflict: ["conflict", "fail"],
  off: ["not targeted", "idle"], disabled: ["turned off", "idle"], unsupported: ["not supported", "idle"],
  absent: ["not installed", "idle"], error: ["config unreadable", "fail"], unknown: ["unknown", "warn"],
};
const RANK = { fail: 3, warn: 2, ok: 1, idle: 0 };
const hostName = (h) => (h === "local" ? "This Mac" : h || "Unknown host");
const plural = (n, one, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

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

const key = (j) => `${j.host}/${j.source}/${j.id}`;
const lastLine = (r) => (r.stderr || r.stdout || "").trim().split("\n").pop() || `exit ${r.code}`;
const tone = (t, h) => ({ fail: t.ansi1, warn: t.ansi3, ok: t.ansi2, idle: t.dim, paused: t.muted })[h];
const worst = (tones) => tones.reduce((a, b) => (RANK[b] > RANK[a] ? b : a), "idle");
const cellTone = (state) => (CELL[state] ?? CELL.unknown)[1];

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
    Object.keys(SOURCES).forEach((name) => this.load(name, cx));
    // The dialog sends "reload" after it changes a job.
    cx.spawn(async (cx) => { for (;;) { if ((await nextCommand()) === "reload") this.load("crons", cx); } });
  }

  // args is ["cron", verb, ...]; --config goes right after the verb, because
  // Go's flag parsing stops at the first positional argument (the job id).
  tackroom(args, timeout) {
    return runTackroom({ bin: this.bin, config: this.config }, args, timeout);
  }

  openForm(mode, j) {
    const job = j && { id: j.id, short_name: j.short_name, declared_schedule: j.declared_schedule, declared_command: j.declared_command, effect: j.effect };
    openOverlay({ mod: "tackroom", kind: "dialog", width: 420, props: { form: { mode, job }, tackroom: this.bin, config: this.config } });
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

  // The running `tackroom view` on the matching tab, signed in with the token it saved.
  openWeb(tab) {
    exec("/bin/sh", ["-c", 'd="${XDG_STATE_HOME:-$HOME/.local/state}/tackroom"; open "http://$(tr -d "\\n" < "$d/view.addr")/?token=$(tr -d "\\n" < "$d/view.token")#$1"', "sh", tab.web]);
  }

  // The inventory as rows of the Skills, Roles and MCP & hooks tabs: a name, a
  // tag, the state in each harness and the lines shown when the row opens.
  inventoryRows(id) {
    const inv = this.src.inventory.data;
    const wired = (list, kind) => list.map((w) => ({
      name: w.name, tag: w.event || (w.enabled ? "" : "turned off"), states: w.states, kind,
      fields: [["Command", w.command], ["Targets", w.explicit ? w.targets.join(", ") : "all agents"]],
    }));
    if (id === "skills") {
      return inv.skills.map((s) => ({
        name: s.name, tag: s.origin, states: s.states, kind: "skill", note: s.description,
        fields: [["Origin", s.origin], ["Path", s.path.replace(inv.home, "~")]],
      }));
    }
    if (id === "roles") return inv.roles.map((r) => ({ name: r.name, tag: "", states: r.states, kind: "role", fields: [] }));
    return [...wired(inv.mcp, "mcp"), ...wired(inv.hooks, "hook")];
  }

  // The tone a tab's dot takes in the tab strip: fail, warn or none.
  attention(tab) {
    const d = this.src[tab.source].data;
    if (!d) return "";
    let level = "idle";
    if (tab.id === "overview") {
      level = d.agents.some((a) => a.conflicts.length || a.error) ? "fail" : !d.synced || d.held.length ? "warn" : "idle";
    } else if (tab.id === "crons") {
      level = d.jobs.some((j) => j.health === "fail") ? "fail" : "idle";
    } else {
      level = worst(this.inventoryRows(tab.id).map((r) => worst(Object.values(r.states).map(cellTone))));
    }
    return level === "fail" || level === "warn" ? level : "";
  }

  button(id, label, onClick, primary, disabled, t) {
    return h_flex().id(id).h(24).px(9).flex_none().items_center().justify_center().rounded(5)
      .border(1).border_color(primary ? t.solid : t.border)
      .bg(primary ? t.solid : t.surface).text_color(primary ? t.solid_foreground : t.foreground).text_size(12)
      .when(disabled, (el) => el.opacity(0.5))
      .hover((s) => s.bg(primary ? t.solid : t.hover))
      .on_click((_e, cx) => { if (!disabled) onClick(cx); })
      .child(label);
  }

  tabButton(tab, t) {
    const on = this.tab === tab.id;
    const flag = this.attention(tab);
    return h_flex().id(`tab-${tab.id}`).h(24).px(8).gap(5).flex_none().items_center().rounded(5).text_size(12)
      .bg(on ? t.selected : t.surface).text_color(on ? t.foreground : t.muted)
      .hover((s) => s.bg(on ? t.selected : t.hover))
      .on_click((_e, cx) => { this.tab = tab.id; cx.notify(); })
      .child(tab.label)
      .when(Boolean(flag), (el) => el.child(div().w(6).h(6).rounded(3).bg(tone(t, flag))));
  }

  render(cx) {
    if (this.form) return this.form.render(cx);
    const t = theme();
    const tab = TABS.find((x) => x.id === this.tab);
    const s = this.src[tab.source];

    const header = h_flex().flex_none().h(36).px(10).gap(6).items_center()
      .child(svg("mods/tackroom/tackroom.svg").size(14).text_color(t.muted))
      .child(div().flex_1().text_size(13).font_semibold().child("tackroom"))
      .child(div().text_size(11).text_color(t.dim).child(s.loading ? "loading…" : s.at ? clock(s.at) : ""))
      .child(this.button("refresh", "Refresh", (cx) => this.load(tab.source, cx), false, s.loading, t))
      .child(this.button("web", "Web", () => this.openWeb(tab), false, false, t));
    const strip = h_flex().flex_none().flex_wrap().gap(2).px(8).pb(6).border_b(1).border_color(t.border)
      .children(TABS.map((x) => this.tabButton(x, t)));

    const items = [];
    if (s.error) items.push(div().text_size(11).text_color(t.ansi1).child(s.data ? `Last load failed: ${s.error}` : s.error));
    if (!s.data && !s.error) items.push(div().text_size(12).text_color(t.muted).child("Loading…"));
    if (s.data) items.push(...this[tab.id](t));
    // One scroll position for each tab.
    return v_flex().size_full().bg(t.surface).text_color(t.foreground)
      .child(header).child(strip)
      .child(v_flex().id(`tackroom-${tab.id}`).flex_1().min_h(0).overflow_y_scrollbar().p(10).gap(14).children(items));
  }

  section(title, rows, t) {
    return v_flex().gap(2)
      .child(h_flex().gap(6).pb(2).items_center()
        .child(div().text_size(11).font_semibold().text_color(t.muted).child(title.toUpperCase()))
        .child(div().text_size(11).text_color(t.dim).child(String(rows.length))))
      .children(rows);
  }

  note(text, t) {
    return div().text_size(11).text_color(t.dim).child(text);
  }

  // A line that opens into its details when clicked; detail() builds them only while open.
  fold(k, head, detail, t) {
    const open = this.open.has(k);
    const row = h_flex().id(k).gap(8).px(6).py(4).items_center().rounded(5)
      .when(open, (el) => el.bg(t.raised))
      .hover((s) => s.bg(t.hover))
      .on_click((_e, cx) => { if (this.open.has(k)) this.open.delete(k); else this.open.add(k); cx.notify(); })
      .children(head);
    return open ? v_flex().gap(4).child(row).child(detail()) : row;
  }

  dot(color) {
    return div().w(8).h(8).flex_none().rounded(4).bg(color);
  }

  field(label, value, t) {
    return h_flex().gap(8)
      .child(div().w(62).flex_none().text_color(t.dim).child(label))
      .child(div().flex_1().min_w(0).text_color(t.muted).child(String(value)));
  }

  overview(t) {
    const d = this.src.plan.data;
    const c = this.src.crons.data;
    const waiting = d.agents.filter((a) => a.detected && !a.synced).length;
    const headline = d.synced ? "All synced" : d.changes > 0 ? `${plural(d.changes, "change")} waiting` : `${plural(waiting, "harness", "harnesses")} out of sync`;
    const out = [
      v_flex().gap(3)
        .child(h_flex().gap(8).items_center().text_size(13).font_semibold()
          .child(this.dot(d.synced ? t.ansi2 : t.ansi3)).child(headline))
        .when(!d.synced, (el) => el.child(this.note("Run tackroom sync to apply it, or tackroom sync --dry-run for the plan.", t)))
        .when(d.repo_link !== "synced", (el) => el.child(this.note(`The config root link is ${d.repo_link}.`, t))),
      this.section("Harnesses", d.agents.map((a) => this.harnessRow(a, t)), t),
    ];
    if (d.held.length) {
      out.push(this.section("Held or blocked", d.held.map((h) => this.fold(`x:${h.item}`,
        [this.dot(h.kind === "blocked" ? t.ansi1 : t.ansi3),
          v_flex().flex_1().min_w(0)
            .child(div().truncate().text_size(12).child(h.item))
            .child(div().text_size(11).text_color(t.dim).child(h.reason)),
          div().flex_none().text_size(11).text_color(t.dim).child(h.kind)],
        () => v_flex().ml(22).mb(6).gap(3).text_size(11)
          .when(Boolean(h.until), (el) => el.child(this.field("Hold ends", h.until, t)))
          .child(this.field("Override", h.command, t)), t)), t));
    }
    if (d.not_fetched.length) out.push(this.note(`A sync would first fetch ${d.not_fetched.join(", ")}.`, t));
    if (c) {
      const failing = c.jobs.filter((j) => j.health === "fail").length;
      out.push(h_flex().id("overview-crons").gap(8).px(6).py(4).items_center().rounded(5).hover((s) => s.bg(t.hover))
        .on_click((_e, cx) => { this.tab = "crons"; cx.notify(); })
        .child(this.dot(failing ? t.ansi1 : t.ansi2))
        .child(div().flex_1().text_size(12).child("Crons"))
        .child(div().text_size(11).text_color(t.dim).child(failing ? `${failing} failing of ${c.jobs.length}` : plural(c.jobs.length, "job"))));
    }
    return out;
  }

  harnessRow(a, t) {
    const [health, word] = !a.detected ? ["idle", "not installed"]
      : a.error ? ["fail", "config unreadable"]
      : a.conflicts.length ? ["fail", plural(a.conflicts.length, "conflict")]
      : !a.synced ? ["warn", "needs sync"] : ["ok", "synced"];
    const surfaces = [["Skills", a.skills], ["Roles", a.roles], ["MCP", a.mcp], ["Hooks", a.hooks]];
    const count = (kind) => surfaces.reduce((n, [, s]) => n + s[kind].length, 0);
    // What needs attention first: the line wraps instead of cutting it off.
    const summary = a.detected
      ? [...(count("missing") ? [`${count("missing")} missing`] : []), ...(count("drifted") ? [`${count("drifted")} drifted`] : []),
        plural(a.skills.managed.length, "skill"), plural(a.roles.managed.length, "role"), `${a.mcp.managed.length} MCP`, plural(a.hooks.managed.length, "hook")].join(" · ")
      : "";
    return this.fold(`h:${a.name}`,
      [this.dot(tone(t, health)),
        v_flex().flex_1().min_w(0)
          .child(div().truncate().text_size(12).child(a.name))
          .child(div().text_size(11).text_color(t.dim).child(summary)),
        div().flex_none().text_size(11).text_color(tone(t, health)).child(word)],
      () => v_flex().ml(22).mb(6).gap(3).text_size(11)
        .when(Boolean(a.error), (el) => el.child(div().text_color(t.ansi1).child(a.error)))
        .children(a.conflicts.map((x) => div().text_color(t.ansi1).child(x)))
        .when(Boolean(a.root_instructions && a.root_instructions.state !== "synced"), (el) => el.child(this.field("Root file", a.root_instructions.state, t)))
        .children(surfaces.flatMap(([label, s]) => ["missing", "drifted"].filter((kind) => s[kind].length)
          .map((kind) => this.field(label, `${kind}: ${s[kind].join(", ")}`, t)))), t);
  }

  // One inventory section: the rows that need attention first, then by name.
  matrix(title, rows, empty, t) {
    if (!rows.length) return this.note(empty, t);
    const inv = this.src.inventory.data;
    const label = (agent) => (inv.agents ?? []).find((x) => x.name === agent)?.display_name || agent;
    const sorted = rows.map((r) => ({ ...r, level: worst(Object.values(r.states).map(cellTone)) }))
      .sort((a, b) => RANK[b.level] - RANK[a.level] || a.name.localeCompare(b.name));
    return this.section(title, sorted.map((r) => {
      const agents = Object.keys(r.states);
      const targeted = agents.filter((a) => !["off", "disabled", "unsupported", "absent"].includes(r.states[a]));
      const inPlace = targeted.filter((a) => r.states[a] === "ok").length;
      return this.fold(`${r.kind}:${r.name}`,
        [this.dot(tone(t, r.level)),
          div().flex_1().min_w(0).truncate().text_size(12).text_color(r.level === "idle" ? t.muted : t.foreground).child(r.name),
          ...(r.tag ? [div().flex_none().max_w(110).truncate().text_size(11).text_color(t.dim).child(r.tag)] : []),
          div().flex_none().w(28).text_size(11).text_color(t.dim).child(targeted.length ? `${inPlace}/${targeted.length}` : "-")],
        () => v_flex().ml(22).mb(6).gap(3).text_size(11)
          .when(Boolean(r.note), (el) => el.child(div().text_color(t.muted).child(r.note)))
          .children(r.fields.filter(([, v]) => v).map(([k, v]) => this.field(k, v, t)))
          .children(agents.map((a) => {
            const [word, level] = CELL[r.states[a]] ?? CELL.unknown;
            return h_flex().gap(8).items_center()
              .child(this.dot(tone(t, level)))
              .child(div().flex_1().min_w(0).truncate().text_color(t.muted).child(label(a)))
              .child(div().flex_none().text_color(t.dim).child(word));
          })), t);
    }), t);
  }

  skills(t) {
    return [this.matrix("Skills", this.inventoryRows("skills"), "No canonical skills yet. Add one with tackroom skill new <name>.", t)];
  }

  roles(t) {
    return [this.matrix("Roles", this.inventoryRows("roles"), "No agent roles are configured. Roles live in agents/*.md in your config root.", t)];
  }

  wiring(t) {
    const rows = this.inventoryRows("wiring");
    return [this.matrix("MCP servers", rows.filter((r) => r.kind === "mcp"), "No MCP servers yet. Add one with tackroom mcp add.", t),
      this.matrix("Hooks", rows.filter((r) => r.kind === "hook"), "No hooks yet. Add them under hooks: in tackroom.yaml.", t)];
  }

  crons(t) {
    const d = this.src.crons.data;
    const jobs = d.jobs;
    const hosts = [...new Set([...d.access.map((a) => a.host), ...jobs.map((j) => j.host)])];
    const items = [
      h_flex().items_center().justify_between()
        .child(this.note(`Scheduled jobs on ${plural(hosts.length, "host")}`, t))
        .child(this.button("add", "Add cron", () => this.openForm("add"), false, false, t)),
      v_flex().gap(6).children(hosts.map((h) => this.hostRow(h, d, t))),
    ];
    const att = jobs.filter((j) => j.health === "fail");
    if (att.length) items.push(this.section("Needs attention", att.map((j) => this.jobRow(j, t, true)), t));
    for (const h of hosts) {
      const list = jobs.filter((j) => j.host === h).sort((a, b) => ORDER.indexOf(a.health) - ORDER.indexOf(b.health));
      if (list.length) items.push(this.section(hostName(h), list.map((j) => this.jobRow(j, t, false)), t));
    }
    return items;
  }

  hostRow(h, d, t) {
    const jobs = d.jobs.filter((j) => j.host === h);
    const access = d.access.filter((a) => a.host === h);
    const down = access.length > 0 && access.every((a) => a.state === "not_scanned") && access.some((a) => (a.detail || "").startsWith("probe failed"));
    const counts = ORDER.map((k) => [k, jobs.filter((j) => j.health === k).length]).filter(([, n]) => n > 0);
    return h_flex().gap(10).items_center().text_size(12)
      .child(div().w(84).flex_none().truncate().font_semibold().child(hostName(h)))
      .child(down
        ? div().flex_1().min_w(0).truncate().text_color(t.ansi1).child(`unreachable: ${access[0].detail}`)
        : h_flex().flex_1().min_w(0).flex_wrap().gap(10).children(counts.map(([k, n]) => h_flex().gap(4).items_center()
          .child(this.dot(tone(t, k)))
          .child(div().text_color(t.muted).child(`${n} ${HEALTH[k].toLowerCase()}`)))));
  }

  jobRow(j, t, inAttention) {
    const h = j.health;
    return this.fold(`${inAttention ? "a" : "j"}:${key(j)}`,
      [this.dot(tone(t, h)),
        div().flex_1().min_w(0).truncate().text_size(12).text_color(h === "paused" ? t.muted : t.foreground).child(j.short_name),
        div().flex_none().max_w(120).truncate().text_size(11).text_color(t.dim).child(inAttention ? hostName(j.host) : j.schedule_text || j.schedule || (j.state === "missing" ? "missing" : "-")),
        div().flex_none().w(66).text_size(11).text_color(t.dim).child(h === "paused" ? "paused" : j.last_run ? ago(j.last_run) : "-")],
      () => this.detail(j, t), t);
  }

  detail(j, t) {
    const result = j.last_exit != null ? `exit ${j.last_exit}` : j.last_status;
    const fields = [["Health", HEALTH[j.health]], ["ID", j.id], ["Command", j.command], ["Effect", j.effect], ["Log", j.log],
      ["Schedule", j.declared_schedule || j.schedule], ["Last run", j.last_run], ["Result", result]].filter(([, v]) => v);
    const busy = this.busy === key(j) || this.busy !== "" || this.src.crons.loading;
    const acts = j.source === "launchd" && j.unit
      ? (OFF.includes(j.state) ? [["resume", "Resume"]] : [["run", "Run now"], ["pause", "Pause"]])
      : [];
    const lines = [
      ...(j.problems ?? []).map((p) => div().text_color(t.ansi1).child(p)),
      ...fields.map(([label, value]) => this.field(label, value, t)),
    ];
    const buttons = acts.map(([verb, label], i) =>
      this.button(`${key(j)}:${verb}`, this.busy === key(j) ? "…" : label, (cx) => this.act(j, verb, cx), i === 0, busy, t));
    // The server says which jobs tackroom owns here (editable) and why not for the rest.
    if (j.editable) {
      buttons.push(this.button(`${key(j)}:edit`, "Edit", () => this.openForm("edit", j), false, busy, t));
      buttons.push(this.button(`${key(j)}:delete`, "Delete", () => this.openForm("delete", j), false, busy, t));
    }
    if (buttons.length) lines.push(h_flex().gap(6).pt(4).children(buttons));
    if (!j.editable && j.edit_note) lines.push(div().pt(2).text_color(t.dim).child(`No edit or delete. ${j.edit_note}`));
    return v_flex().ml(22).mb(6).gap(3).text_size(11).children(lines);
  }
}
