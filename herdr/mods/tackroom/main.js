import { View, div, svg } from "gpui-kit";
import { h_flex, v_flex } from "gpui-base";
import { exec, props, theme, notify } from "herdr";

// tackroom's Crons tab inside Herdr. It draws the same `tackroom cron collect`
// JSON the web view draws and acts through the same `tackroom cron
// run|pause|resume` verbs, so the two show and do the same things. Health,
// names and schedules follow internal/app/web/app.js (jobHealth, shortName,
// describeSchedule).

const PAUSED = ["disabled", "paused", "not_loaded"];
const OK_STATUS = new Set(["ok", "success", "succeeded", "completed", "done", "cancelled", "skipped"]);
const LABEL_ROOTS = new Set(["com", "org", "net", "io", "ai", "sh", "dev", "me", "app", "co"]);
const HEALTH = { fail: "Failing", warn: "Last run failed", idle: "No result", ok: "Healthy", paused: "Paused" };
const ORDER = ["fail", "warn", "idle", "ok", "paused"];
const DONE = { run: "Started", pause: "Paused", resume: "Resumed" };

const attention = (j) => !PAUSED.includes(j.state) && ((j.problems ?? []).length > 0 || ["dead", "missing"].includes(j.state));

function health(j) {
  if (PAUSED.includes(j.state)) return "paused";
  if (attention(j)) return "fail";
  if (j.last_exit != null) return j.last_exit === 0 ? "ok" : "warn";
  if (j.last_status) return OK_STATUS.has(j.last_status) ? "ok" : "warn";
  return j.state === "service" ? "ok" : "idle";
}

function shortName(j) {
  const name = j.name || j.id;
  const parts = name.split(".");
  if (j.source !== "launchd" || !LABEL_ROOTS.has(parts[0]) || parts.length < 2) return name;
  return parts.slice(parts.length > 2 ? 2 : 1).join(".");
}

const hostName = (h) => (h === "local" ? "This Mac" : h);

// The web's duration.
function duration(s) {
  if (s % 3600 === 0) return s === 3600 ? "hour" : `${s / 3600} h`;
  if (s % 60 === 0) return s === 60 ? "minute" : `${s / 60} min`;
  return `${s} s`;
}

const DAY_NAMES = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

// The web's cronField: a cron field as a set of numbers, or null for a shape it doesn't read.
function cronField(text, min, max) {
  const values = new Set();
  for (const part of text.split(",")) {
    const [range, stepText] = part.split("/");
    const step = Number(stepText || 1);
    let [lo, hi] = range === "*" ? [min, max] : range.split("-").map(Number);
    if (hi === undefined) hi = stepText ? max : lo;
    if (![lo, hi, step].every(Number.isFinite) || step < 1 || lo < min || hi > max) return null;
    for (let value = lo; value <= hi; value += step) values.add(max === 7 && value === 7 ? 0 : value);
  }
  return values;
}

// The web's describeSchedule. An owned job shows its declared expression,
// which launchd may have expanded into many entries ("0 8 */2 * *" into 16).
function describe(j) {
  if (j.state === "service") return "Always on";
  const s = j.declared_schedule || j.schedule || "";
  const every = /^every (\d+)s$/.exec(s);
  if (every) return `Every ${duration(Number(every[1]))}`;
  const lines = s.split(";").map((line) => line.trim().split(/\s+/));
  if (!s || lines.some((f) => f.length !== 5)) return s || "-";
  const [m, hr] = lines[0];
  const step = /^\*\/(\d+)$/.exec(m);
  if (lines.length === 1 && step && lines[0].slice(1).every((f) => f === "*")) return `Every ${step[1]} min`;
  if (lines.every((f) => /^\d+$/.test(f[0]) && f.slice(1).every((x) => x === "*"))) {
    const minutes = lines.map((f) => Number(f[0])).sort((a, b) => a - b);
    const gap = 60 / minutes.length;
    if (minutes.length > 1 && minutes.every((v, i) => v === minutes[0] + i * gap)) return `Every ${gap} min`;
    return `Hourly at ${minutes.map((v) => `:${String(v).padStart(2, "0")}`).join(", ")}`;
  }
  if (!/^\d+$/.test(m) || !/^\d+$/.test(hr) || !lines.every((f) => f[0] === m && f[1] === hr && f[3] === "*")) return s;
  const time = `${hr.padStart(2, "0")}:${m.padStart(2, "0")}`;
  if (lines.every((f) => f[2] !== "*" && f[4] === "*")) {
    const dates = new Set();
    for (const f of lines) {
      const set = cronField(f[2], 1, 31);
      if (!set) return s;
      set.forEach((d) => dates.add(d));
    }
    const order = [...dates].sort((a, b) => a - b);
    const gap = order.length > 1 ? order[1] - order[0] : 0;
    if (order[0] === 1 && gap > 1 && order.every((d, i) => d === 1 + i * gap) && order[order.length - 1] + gap > 31) return `Every ${gap} days ${time}`;
    return `${order.length === 1 ? "Day" : "Days"} ${order.join(", ")} of the month, ${time}`;
  }
  if (!lines.every((f) => f[2] === "*")) return s;
  const days = new Set();
  for (const f of lines) {
    const set = cronField(f[4], 0, 7);
    if (!set) return s;
    set.forEach((d) => days.add(d));
  }
  if (days.size === 7) return `Daily ${time}`;
  if (days.size === 5 && [1, 2, 3, 4, 5].every((d) => days.has(d))) return `Weekdays ${time}`;
  return `${[...days].sort((a, b) => ((a + 6) % 7) - ((b + 6) % 7)).map((d) => DAY_NAMES[d]).join(", ")} ${time}`;
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

function clock(iso) {
  const d = new Date(Date.parse(iso));
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

const key = (j) => `${j.host}/${j.source}/${j.id}`;
const lastLine = (r) => (r.stderr || r.stdout || "").trim().split("\n").pop() || `exit ${r.code}`;
const tone = (t, h) => ({ fail: t.ansi1, warn: t.ansi3, ok: t.ansi2, idle: t.dim, paused: t.muted })[h];

export default class Crons extends View {
  init(_props, cx) {
    const p = props() ?? {};
    this.bin = p.tackroom ?? "tackroom";
    this.config = p.config ?? "";
    this.data = null;
    this.error = "";
    this.loading = false;
    this.busy = "";
    this.open = new Set();
    this.home = null;
    // A collect probes every host over SSH, so poll gently; Collect refreshes at once.
    cx.timer.every(Math.max(60, p.refresh ?? 300) * 1000, (cx) => this.load(cx));
    this.load(cx);
  }

  async expand(path) {
    if (!path.startsWith("~/")) return path;
    if (this.home == null) this.home = (await exec("/bin/sh", ["-c", 'printf %s "$HOME"'])).stdout;
    return this.home + path.slice(1);
  }

  // args is ["cron", verb, ...]; --config goes right after the verb, because
  // Go's flag parsing stops at the first positional argument (the job id).
  async tackroom(args, timeout) {
    const bin = await this.expand(this.bin);
    const cfg = this.config ? ["--config", await this.expand(this.config)] : [];
    return exec(bin, [...args.slice(0, 2), ...cfg, ...args.slice(2)], { timeout });
  }

  load(cx) {
    if (this.loading) return;
    this.loading = true;
    cx.notify();
    cx.spawn(async (cx) => {
      try {
        const r = await this.tackroom(["cron", "collect"], 300000);
        if (r.code !== 0) throw new Error(lastLine(r));
        this.data = JSON.parse(r.stdout);
        this.error = "";
      } catch (e) {
        this.error = String(e?.message ?? e);
      }
      this.loading = false;
      cx.notify();
    });
  }

  act(job, verb, cx) {
    this.busy = key(job);
    cx.notify();
    cx.spawn(async (cx) => {
      const r = await this.tackroom(["cron", verb, "--host", job.host, "--source", job.source, job.id], 120000);
      this.busy = "";
      if (r.code === 0) notify({ title: `${DONE[verb]} ${shortName(job)}`, level: "success", timeout: 3000 });
      else notify({ title: `Could not ${verb} ${shortName(job)}`, body: lastLine(r), level: "error" });
      this.load(cx);
    });
  }

  // The running `tackroom view`, signed in with the token it saved.
  openWeb() {
    exec("/bin/sh", ["-c", 'd="${XDG_STATE_HOME:-$HOME/.local/state}/tackroom"; open "http://$(tr -d "\\n" < "$d/view.addr")/?token=$(tr -d "\\n" < "$d/view.token")#crons"']);
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

  render() {
    const t = theme();
    const d = this.data;
    const jobs = d?.jobs ?? [];
    const hosts = [...new Set([...(d?.access ?? []).map((a) => a.host), ...jobs.map((j) => j.host)])];

    const header = h_flex().flex_none().h(36).px(10).gap(6).items_center().border_b(1).border_color(t.border)
      .child(svg("mods/tackroom/crons.svg").size(14).text_color(t.muted))
      .child(div().flex_1().text_size(13).font_semibold().child("Crons"))
      .child(div().text_size(11).text_color(t.dim).child(this.loading ? "collecting…" : d ? clock(d.scanned_at) : ""))
      .child(this.button("collect", "Collect", (cx) => this.load(cx), false, this.loading, t))
      .child(this.button("web", "Web", () => this.openWeb(), false, false, t));

    const items = [];
    if (this.error) items.push(div().text_size(11).text_color(t.ansi1).child(d ? `Last collect failed: ${this.error}` : this.error));
    if (!d && !this.error) items.push(div().text_size(12).text_color(t.muted).child("Collecting…"));
    if (d) {
      items.push(v_flex().gap(6).children(hosts.map((h) => this.hostRow(h, d, t))));
      const att = jobs.filter(attention);
      if (att.length) items.push(this.section("Needs attention", att.map((j) => this.jobRow(j, t, true)), t));
      for (const h of hosts) {
        const list = jobs.filter((j) => j.host === h).sort((a, b) => ORDER.indexOf(health(a)) - ORDER.indexOf(health(b)));
        if (list.length) items.push(this.section(hostName(h), list.map((j) => this.jobRow(j, t, false)), t));
      }
    }
    return v_flex().size_full().bg(t.surface).text_color(t.foreground)
      .child(header)
      .child(v_flex().id("crons-body").flex_1().min_h(0).overflow_y_scrollbar().p(10).gap(14).children(items));
  }

  section(title, rows, t) {
    return v_flex().gap(2)
      .child(h_flex().gap(6).pb(2).items_center()
        .child(div().text_size(11).font_semibold().text_color(t.muted).child(title.toUpperCase()))
        .child(div().text_size(11).text_color(t.dim).child(String(rows.length))))
      .children(rows);
  }

  hostRow(h, d, t) {
    const jobs = d.jobs.filter((j) => j.host === h);
    const access = d.access.filter((a) => a.host === h);
    const down = access.length > 0 && access.every((a) => a.state === "not_scanned") && access.some((a) => (a.detail || "").startsWith("probe failed"));
    const counts = ORDER.map((k) => [k, jobs.filter((j) => health(j) === k).length]).filter(([, n]) => n > 0);
    return h_flex().gap(10).items_center().text_size(12)
      .child(div().w(84).flex_none().truncate().font_semibold().child(hostName(h)))
      .child(down
        ? div().flex_1().min_w(0).truncate().text_color(t.ansi1).child(`unreachable: ${access[0].detail}`)
        : h_flex().flex_1().min_w(0).flex_wrap().gap(10).children(counts.map(([k, n]) => h_flex().gap(4).items_center()
          .child(div().w(8).h(8).rounded(4).bg(tone(t, k)))
          .child(div().text_color(t.muted).child(`${n} ${HEALTH[k].toLowerCase()}`)))));
  }

  jobRow(j, t, inAttention) {
    const k = `${inAttention ? "a" : "j"}:${key(j)}`;
    const open = this.open.has(k);
    const h = health(j);
    const row = h_flex().id(k).gap(8).px(6).py(4).items_center().rounded(5)
      .when(open, (el) => el.bg(t.raised))
      .hover((s) => s.bg(t.hover))
      .on_click((_e, cx) => { if (this.open.has(k)) this.open.delete(k); else this.open.add(k); cx.notify(); })
      .child(div().w(8).h(8).flex_none().rounded(4).bg(tone(t, h)))
      .child(div().flex_1().min_w(0).truncate().text_size(12).text_color(h === "paused" ? t.muted : t.foreground).child(shortName(j)))
      .child(div().flex_none().max_w(120).truncate().text_size(11).text_color(t.dim).child(inAttention ? hostName(j.host) : describe(j)))
      .child(div().flex_none().w(66).text_size(11).text_color(t.dim).child(h === "paused" ? "paused" : j.last_run ? ago(j.last_run) : "-"));
    return open ? v_flex().gap(4).child(row).child(this.detail(j, t)) : row;
  }

  detail(j, t) {
    const result = j.last_exit != null ? `exit ${j.last_exit}` : j.last_status;
    const fields = [["Health", HEALTH[health(j)]], ["ID", j.id], ["Command", j.command], ["Log", j.log],
      ["Schedule", j.declared_schedule || j.schedule], ["Last run", j.last_run], ["Result", result]].filter(([, v]) => v);
    const busy = this.busy === key(j) || this.busy !== "" || this.loading;
    const acts = j.source === "launchd" && j.unit
      ? (PAUSED.includes(j.state) ? [["resume", "Resume"]] : [["run", "Run now"], ["pause", "Pause"]])
      : [];
    const lines = [
      ...(j.problems ?? []).map((p) => div().text_color(t.ansi1).child(p)),
      ...fields.map(([label, value]) => h_flex().gap(8)
        .child(div().w(62).flex_none().text_color(t.dim).child(label))
        .child(div().flex_1().min_w(0).text_color(t.muted).child(String(value)))),
    ];
    if (acts.length) {
      lines.push(h_flex().gap(6).pt(4).children(acts.map(([verb, label], i) =>
        this.button(`${key(j)}:${verb}`, this.busy === key(j) ? "…" : label, (cx) => this.act(j, verb, cx), i === 0, busy, t))));
    }
    return v_flex().ml(22).mb(6).gap(3).text_size(11).children(lines);
  }
}
