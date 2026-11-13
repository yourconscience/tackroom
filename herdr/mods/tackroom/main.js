import { View, div, svg } from "gpui-kit";
import { h_flex, v_flex } from "gpui-base";
import { exec, props, theme, notify, openOverlay, nextCommand } from "herdr";
import JobForm, { runTackroom } from "./form.js";

// tackroom's Crons tab inside Herdr. It draws the same `tackroom cron collect`
// JSON the web view draws, including the health, short name and schedule
// words tackroom works out once for both, and acts through the same
// `tackroom cron run|pause|resume` verbs. Add, Edit and Delete open form.js as
// a dialog of this same mod and run `tackroom cron add|edit|remove`.

// A launchd job that is off offers Resume, as in the web view.
const OFF = ["disabled", "not_loaded"];
const HEALTH = { fail: "Failing", warn: "Last run failed", idle: "No result", ok: "Healthy", paused: "Paused" };
const ORDER = ["fail", "warn", "idle", "ok", "paused"];
const DONE = { run: "Started", pause: "Paused", resume: "Resumed" };
const hostName = (h) => (h === "local" ? "This Mac" : h);

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
    // The add, edit and delete dialog is this mod opened as an overlay.
    if (p.form) { this.form = new JobForm(); this.form.init(p, cx); return; }
    this.bin = p.tackroom ?? "tackroom";
    this.config = p.config ?? "";
    this.data = null;
    this.error = "";
    this.loading = false;
    this.busy = "";
    this.open = new Set();
    // A collect probes every host over SSH, so poll gently; Collect refreshes at once.
    cx.timer.every(Math.max(60, p.refresh ?? 300) * 1000, (cx) => this.load(cx));
    this.load(cx);
    // The dialog sends "reload" after it changes a job.
    cx.spawn(async (cx) => { for (;;) { if ((await nextCommand()) === "reload") this.load(cx); } });
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

  load(cx) {
    // A collect asked for while one runs (say, right after an action) runs
    // next, so the panel never keeps data from before the action.
    if (this.loading) { this.again = true; return; }
    this.loading = true;
    this.again = false;
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
      if (this.again) this.load(cx);
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

  render(cx) {
    if (this.form) return this.form.render(cx);
    const t = theme();
    const d = this.data;
    const jobs = d?.jobs ?? [];
    const hosts = [...new Set([...(d?.access ?? []).map((a) => a.host), ...jobs.map((j) => j.host)])];

    const header = h_flex().flex_none().h(36).px(10).gap(6).items_center().border_b(1).border_color(t.border)
      .child(svg("mods/tackroom/crons.svg").size(14).text_color(t.muted))
      .child(div().flex_1().text_size(13).font_semibold().child("Crons"))
      .child(div().text_size(11).text_color(t.dim).child(this.loading ? "collecting…" : d ? clock(d.scanned_at) : ""))
      .child(this.button("add", "Add", () => this.openForm("add"), false, false, t))
      .child(this.button("collect", "Collect", (cx) => this.load(cx), false, this.loading, t))
      .child(this.button("web", "Web", () => this.openWeb(), false, false, t));

    const items = [];
    if (this.error) items.push(div().text_size(11).text_color(t.ansi1).child(d ? `Last collect failed: ${this.error}` : this.error));
    if (!d && !this.error) items.push(div().text_size(12).text_color(t.muted).child("Collecting…"));
    if (d) {
      items.push(v_flex().gap(6).children(hosts.map((h) => this.hostRow(h, d, t))));
      const att = jobs.filter((j) => j.health === "fail");
      if (att.length) items.push(this.section("Needs attention", att.map((j) => this.jobRow(j, t, true)), t));
      for (const h of hosts) {
        const list = jobs.filter((j) => j.host === h).sort((a, b) => ORDER.indexOf(a.health) - ORDER.indexOf(b.health));
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
    const counts = ORDER.map((k) => [k, jobs.filter((j) => j.health === k).length]).filter(([, n]) => n > 0);
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
    const h = j.health;
    const row = h_flex().id(k).gap(8).px(6).py(4).items_center().rounded(5)
      .when(open, (el) => el.bg(t.raised))
      .hover((s) => s.bg(t.hover))
      .on_click((_e, cx) => { if (this.open.has(k)) this.open.delete(k); else this.open.add(k); cx.notify(); })
      .child(div().w(8).h(8).flex_none().rounded(4).bg(tone(t, h)))
      .child(div().flex_1().min_w(0).truncate().text_size(12).text_color(h === "paused" ? t.muted : t.foreground).child(j.short_name))
      .child(div().flex_none().max_w(120).truncate().text_size(11).text_color(t.dim).child(inAttention ? hostName(j.host) : j.schedule_text))
      .child(div().flex_none().w(66).text_size(11).text_color(t.dim).child(h === "paused" ? "paused" : j.last_run ? ago(j.last_run) : "-"));
    return open ? v_flex().gap(4).child(row).child(this.detail(j, t)) : row;
  }

  detail(j, t) {
    const result = j.last_exit != null ? `exit ${j.last_exit}` : j.last_status;
    const fields = [["Health", HEALTH[j.health]], ["ID", j.id], ["Command", j.command], ["Log", j.log],
      ["Schedule", j.declared_schedule || j.schedule], ["Last run", j.last_run], ["Result", result]].filter(([, v]) => v);
    const busy = this.busy === key(j) || this.busy !== "" || this.loading;
    const acts = j.source === "launchd" && j.unit
      ? (OFF.includes(j.state) ? [["resume", "Resume"]] : [["run", "Run now"], ["pause", "Pause"]])
      : [];
    const lines = [
      ...(j.problems ?? []).map((p) => div().text_color(t.ansi1).child(p)),
      ...fields.map(([label, value]) => h_flex().gap(8)
        .child(div().w(62).flex_none().text_color(t.dim).child(label))
        .child(div().flex_1().min_w(0).text_color(t.muted).child(String(value)))),
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
