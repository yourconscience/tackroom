import { div } from "gpui-kit";
import { h_flex, v_flex, InputState, Input } from "gpui-base";
import { exec, props, closeOverlay, theme, notify, run } from "herdr";

// The dialogs of the tackroom panel. main.js opens one as an overlay of the
// same mod, with props { form: { mode, job?, plan? }, tackroom, config }:
//   add, edit, delete  a cron, through `tackroom cron add|edit|remove`
//   sync               the plan of `sync --dry-run --json`, applied with
//                      `sync --plan-digest`, so only the plan shown applies
// A refusal shows in the dialog. After a failed sync the plan shown is out of
// date, so only Cancel stays.

const DONE = { add: "Added", edit: "Saved", delete: "Deleted", sync: "Synced" };
const TITLE = { add: "Add cron", edit: "Edit", delete: "Delete", sync: "Sync" };

// Plan lines read "agent: what"; agents with the same change share one line.
function grouped(lines) {
  const out = new Map();
  for (const line of lines) {
    const at = line.indexOf(": ");
    const [agent, what] = at < 0 ? ["", line] : [line.slice(0, at), line.slice(at + 2)];
    if (what === "up to date" || what === "not installed, skipped") continue;
    out.set(what, [...(out.get(what) ?? []), agent].filter(Boolean));
  }
  return [...out];
}

let home = null;
// exec does not expand ~, so a leading ~/ is expanded here.
async function expand(path) {
  if (!path.startsWith("~/")) return path;
  if (home == null) home = (await exec("/bin/sh", ["-c", 'printf %s "$HOME"'])).stdout;
  return home + path.slice(1);
}

// args is ["cron", verb, ...]; --config goes right after the verb. The job id
// may follow it: the verbs take their flags and the id in any order.
export async function runTackroom(p, args, timeout) {
  const bin = await expand(p.bin);
  const cfg = p.config ? ["--config", await expand(p.config)] : [];
  return exec(bin, [...args.slice(0, 2), ...cfg, ...args.slice(2)], { timeout });
}

export default class JobForm {
  init(p, cx) {
    const f = p.form ?? {};
    this.mode = f.mode;
    this.job = f.job ?? null;
    this.plan = f.plan ?? null;
    this.conn = { bin: p.tackroom ?? "tackroom", config: p.config ?? "" };
    this.overlay = props()?.overlay;
    this.error = "";
    this.busy = false;
    this.stale = false;
    this.handle = cx.focus_handle();

    // No placeholders: Herdr draws them nearly as bright as typed text, so an
    // empty form looked filled in. The labels carry the examples instead.
    const field = (value) => {
      const state = InputState.new({ value: value ?? "" });
      state.on("submit", (_e, cx) => this.submit(cx));
      return state;
    };
    const [exe = "", ...args] = this.job?.declared_command ?? [];
    this.id = field(this.job?.id);
    this.schedule = field(this.job?.declared_schedule);
    this.exe = field(exe);
    this.args = (args.length ? args : [""]).map((a) => field(a));
    this.effect = field(this.job?.effect);
    this.field = field;

    if (this.mode === "delete" || this.mode === "sync") this.handle.focus();
    else {
      const first = this.mode === "add" ? this.id : this.schedule;
      first.focus();
      cx.timer.after(600, () => first.focus());
    }
  }

  // The CLI call for the form as filled in, or an error to show.
  command() {
    const text = (state) => state.value().trim();
    const job = this.job;
    // --plan-digest=… stays one argument: runTackroom puts --config after it.
    if (this.mode === "sync") return { args: ["sync", `--plan-digest=${this.plan.digest}`], name: "", reload: "synced", timeout: 300000 };
    if (this.mode === "delete") return { args: ["cron", "remove", job.id], name: job.short_name };
    const exe = text(this.exe);
    const args = this.args.map((a) => a.value()).filter((a) => a !== "");
    const schedule = text(this.schedule);
    const effect = text(this.effect);
    const withCommand = ["--command", exe, ...args.flatMap((a) => ["--arg", a])];
    if (this.mode === "add") {
      const id = text(this.id);
      return { args: ["cron", "add", id, "--schedule", schedule, ...withCommand, ...(effect ? ["--effect", effect] : [])], name: id };
    }
    // Only what changed goes to the CLI, so the rest of the entry stays as written.
    const flags = [];
    if (schedule !== (job.declared_schedule ?? "")) flags.push("--schedule", schedule);
    if ([exe, ...args].join("\n") !== (job.declared_command ?? []).join("\n")) flags.push(...withCommand);
    if (effect !== (job.effect ?? "")) flags.push("--effect", effect);
    if (!flags.length) return { error: "Nothing to change." };
    return { args: ["cron", "edit", job.id, ...flags], name: job.short_name };
  }

  submit(cx) {
    if (this.busy || this.stale) return;
    const c = this.command();
    if (c.error) { this.error = c.error; cx.notify(); return; }
    this.busy = true;
    this.error = "";
    cx.notify();
    cx.spawn(async (cx) => {
      let ok = false;
      try {
        const r = await runTackroom(this.conn, c.args, c.timeout ?? 150000);
        ok = r.code === 0;
        if (!ok) this.error = (r.stderr || r.stdout || `exit ${r.code}`).trim().slice(0, 500);
      } catch (e) {
        this.error = String(e?.message ?? e);
      }
      // The panel loads again either way: a failed apply leaves the config changed.
      run(`tackroom.${c.reload ?? "reload"}`);
      this.busy = false;
      // A retry would send the digest of a plan that is no longer the plan.
      this.stale = !ok && this.mode === "sync";
      if (ok) {
        closeOverlay(this.overlay);
        notify({ title: `${DONE[this.mode]} ${c.name}`.trim(), level: "success", timeout: 3000 });
      }
      cx.notify();
    });
  }

  render(cx) {
    const t = theme();
    // The example stays on one line: it wraps below the label as a whole.
    const label = (text, example) => h_flex().flex_wrap().gap_x(6).pt(12).pb(6).text_size(11.5).text_color(t.dim)
      .child(div().child(text))
      .when(Boolean(example), (el) => el.child(div().whitespace_nowrap().child(`for example ${example}`)));
    const input = (state) => Input.new(state).h(34).px(10).rounded(8).border_1().border_color(t.input).bg(t.background).text_size(12.5).text_color(t.foreground);
    const button = (id, text, kind, action) => h_flex().id(id).h(32).px(14).items_center().rounded(8).border_1()
      .border_color(kind === "primary" ? t.solid : kind === "danger" ? t.ansi1 : t.input)
      .bg(kind === "primary" ? t.solid : t.raised)
      .text_color(kind === "primary" ? t.solid_foreground : kind === "danger" ? t.ansi1 : t.foreground)
      .text_size(12.5).font_medium().cursor_pointer()
      .when(this.busy, (el) => el.opacity(0.5))
      .hover((s) => s.opacity(0.88))
      .on_click((_e, cx) => { if (!this.busy) action(cx); })
      .child(text);
    const title = this.mode === "add" || this.mode === "sync" ? TITLE[this.mode] : this.mode === "edit" ? `${TITLE.edit} ${this.job.short_name}` : `${TITLE.delete} ${this.job.short_name}?`;
    const close = () => closeOverlay(this.overlay);

    let body;
    const removes = this.plan?.destructive ?? [];
    if (this.mode === "sync") {
      const change = ([what, agents], color) => v_flex().gap(1)
        .child(div().text_size(12.5).text_color(color).child(what))
        .when(agents.length > 0, (el) => el.child(div().text_size(11.5).text_color(t.dim).child(agents.join(", "))));
      const changes = grouped(this.plan.lines);
      body = v_flex().max_h(380).overflow_y_scrollbar().gap(8).pt(12)
        .when(changes.length === 0 && removes.length === 0, (el) => el.child(div().text_size(12.5).text_color(t.muted).child("Nothing to change.")))
        .children(changes.map((g) => change(g, t.foreground)))
        .when(removes.length > 0, (el) => el
          .child(div().pt(6).text_size(11).font_semibold().text_color(t.ansi1).child("REMOVES OR OVERWRITES"))
          .children(grouped(removes).map((g) => change(g, t.ansi1))))
        .child(div().pt(6).text_size(11.5).text_color(t.dim).child("Sync applies this plan from the config it was made from. If the plan or the config changed since it loaded, it stops and the panel loads the new plan. Edits inside skill and role files are not checked."));
    } else if (this.mode === "delete") {
      const command = (this.job.declared_command ?? []).join(" ");
      body = v_flex().gap(6).pt(12).text_size(12.5).text_color(t.muted)
        .child("This removes the job from your tackroom config and its unit from this machine. tackroom touches no other job.")
        .children([this.job.declared_schedule, command].filter(Boolean).map((line) => div().text_color(t.dim).truncate().child(line)));
    } else {
      body = v_flex().max_h(380).overflow_y_scrollbar()
        .when(this.mode === "add", (el) => el.child(label("Name", "nightly-backup")).child(input(this.id)))
        .child(label("Schedule (minute hour day-of-month month weekday)", "0 9 * * 1-5")).child(input(this.schedule))
        .child(label("Command (a name on PATH or an absolute path)", "/usr/bin/git")).child(input(this.exe))
        .child(label("Arguments, one per box"))
        .children(this.args.map((state, i) => h_flex().id(`arg-${i}`).gap(6).pb(6).items_center()
          .child(div().flex_1().min_w(0).child(input(state)))
          .child(h_flex().id(`arg-rm-${i}`).w(28).h(28).flex_none().items_center().justify_center().rounded(6).cursor_pointer().text_color(t.dim)
            .hover((s) => s.bg(t.hover))
            .on_click((_e, cx) => { this.args.splice(i, 1); if (!this.args.length) this.args.push(this.field("")); cx.notify(); })
            .child("−"))))
        .child(h_flex().id("arg-add").h(26).px(8).items_center().rounded(6).cursor_pointer().text_size(12).text_color(t.muted)
          .hover((s) => s.bg(t.hover))
          .on_click((_e, cx) => { const s = this.field(""); this.args.push(s); s.focus(); cx.notify(); })
          .child("+ Add argument"))
        .child(label("Effect (optional): what it changes")).child(input(this.effect));
    }

    return v_flex().id("tackroom-form").track_focus(this.handle)
      .on_key_down((e, cx) => { if (e.key === "escape") { close(); cx.stop_propagation(); } })
      .w_full().min_h(0).p(18).bg(t.popover).text_color(t.foreground)
      .child(h_flex().items_center()
        .child(div().flex_1().min_w(0).truncate().text_size(15).font_semibold().child(title))
        .child(div().text_size(11).text_color(t.dim).child("Esc")))
      .child(body)
      .when(Boolean(this.error), (el) => el.child(div().pt(10).text_size(11.5).text_color(t.ansi1).child(this.error)))
      .when(this.stale, (el) => el.child(div().pt(6).text_size(11.5).text_color(t.dim).child("Nothing more is applied from this plan. Cancel, then press Sync for the current one.")))
      .child(h_flex().pt(18).gap(8).justify_end()
        .child(button("form-cancel", "Cancel", "plain", close))
        .child(button("form-ok", this.busy ? "Working…" : { add: "Add cron", edit: "Save", delete: "Delete", sync: removes.length ? "Sync and remove" : "Sync" }[this.mode],
          this.mode === "delete" || removes.length ? "danger" : "primary", (cx) => this.submit(cx))
          .when(this.stale, (el) => el.opacity(0.5))));
  }
}
