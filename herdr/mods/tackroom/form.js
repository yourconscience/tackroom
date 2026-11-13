import { div } from "gpui-kit";
import { h_flex, v_flex, InputState, Input } from "gpui-base";
import { exec, props, closeOverlay, theme, notify, run } from "herdr";

// The add, edit and delete dialog of tackroom's Crons panel. main.js opens it
// as an overlay of the same mod, with props { form: { mode, job? }, tackroom,
// config }, and it acts through `tackroom cron add|edit|remove`, the verbs
// behind the web view's form. A refusal shows in the dialog.

const DONE = { add: "Added", edit: "Saved", delete: "Deleted" };
const TITLE = { add: "Add cron", edit: "Edit", delete: "Delete" };

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
    this.conn = { bin: p.tackroom ?? "tackroom", config: p.config ?? "" };
    this.overlay = props()?.overlay;
    this.error = "";
    this.busy = false;
    this.handle = cx.focus_handle();

    const field = (value, placeholder) => {
      const state = InputState.new({ value: value ?? "", placeholder });
      state.on("submit", (_e, cx) => this.submit(cx));
      return state;
    };
    const [exe = "", ...args] = this.job?.declared_command ?? [];
    this.id = field(this.job?.id, "nightly-backup");
    this.schedule = field(this.job?.declared_schedule, "0 9 * * 1-5");
    this.exe = field(exe, "/usr/bin/git");
    this.args = (args.length ? args : [""]).map((a) => field(a, "argument"));
    this.effect = field(this.job?.effect, "what it changes");
    this.field = field;

    if (this.mode === "delete") this.handle.focus();
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
    if (this.busy) return;
    const c = this.command();
    if (c.error) { this.error = c.error; cx.notify(); return; }
    this.busy = true;
    this.error = "";
    cx.notify();
    cx.spawn(async (cx) => {
      let ok = false;
      try {
        const r = await runTackroom(this.conn, c.args, 150000);
        ok = r.code === 0;
        if (!ok) this.error = (r.stderr || r.stdout || `exit ${r.code}`).trim().slice(0, 500);
      } catch (e) {
        this.error = String(e?.message ?? e);
      }
      // The panel collects again either way: a failed apply leaves the config changed.
      run("tackroom.reload");
      this.busy = false;
      if (ok) {
        closeOverlay(this.overlay);
        notify({ title: `${DONE[this.mode]} ${c.name}`, level: "success", timeout: 3000 });
      }
      cx.notify();
    });
  }

  render(cx) {
    const t = theme();
    const label = (text) => div().pt(12).pb(6).text_size(11.5).text_color(t.dim).child(text);
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
    const title = this.mode === "add" ? TITLE.add : this.mode === "edit" ? `${TITLE.edit} ${this.job.short_name}` : `${TITLE.delete} ${this.job.short_name}?`;
    const close = () => closeOverlay(this.overlay);

    let body;
    if (this.mode === "delete") {
      const command = (this.job.declared_command ?? []).join(" ");
      body = v_flex().gap(6).pt(12).text_size(12.5).text_color(t.muted)
        .child("This removes the job from your tackroom config and its unit from this machine. tackroom touches no other job.")
        .children([this.job.declared_schedule, command].filter(Boolean).map((line) => div().text_color(t.dim).truncate().child(line)));
    } else {
      body = v_flex().max_h(380).overflow_y_scrollbar()
        .when(this.mode === "add", (el) => el.child(label("Name")).child(input(this.id)))
        .child(label("Schedule (minute hour day-of-month month weekday)")).child(input(this.schedule))
        .child(label("Command (a name on PATH or an absolute path)")).child(input(this.exe))
        .child(label("Arguments, one per box"))
        .children(this.args.map((state, i) => h_flex().id(`arg-${i}`).gap(6).pb(6).items_center()
          .child(div().flex_1().min_w(0).child(input(state)))
          .child(h_flex().id(`arg-rm-${i}`).w(28).h(28).flex_none().items_center().justify_center().rounded(6).cursor_pointer().text_color(t.dim)
            .hover((s) => s.bg(t.hover))
            .on_click((_e, cx) => { this.args.splice(i, 1); if (!this.args.length) this.args.push(this.field("", "argument")); cx.notify(); })
            .child("−"))))
        .child(h_flex().id("arg-add").h(26).px(8).items_center().rounded(6).cursor_pointer().text_size(12).text_color(t.muted)
          .hover((s) => s.bg(t.hover))
          .on_click((_e, cx) => { const s = this.field("", "argument"); this.args.push(s); s.focus(); cx.notify(); })
          .child("+ Add argument"))
        .child(label("Effect (optional)")).child(input(this.effect));
    }

    return v_flex().id("tackroom-form").track_focus(this.handle)
      .on_key_down((e, cx) => { if (e.key === "escape") { close(); cx.stop_propagation(); } })
      .w_full().min_h(0).p(18).bg(t.popover).text_color(t.foreground)
      .child(h_flex().items_center()
        .child(div().flex_1().min_w(0).truncate().text_size(15).font_semibold().child(title))
        .child(div().text_size(11).text_color(t.dim).child("Esc")))
      .child(body)
      .when(Boolean(this.error), (el) => el.child(div().pt(10).text_size(11.5).text_color(t.ansi1).child(this.error)))
      .child(h_flex().pt(18).gap(8).justify_end()
        .child(button("form-cancel", "Cancel", "plain", close))
        .child(button("form-ok", this.busy ? "Working…" : this.mode === "add" ? "Add cron" : this.mode === "edit" ? "Save" : "Delete", this.mode === "delete" ? "danger" : "primary", (cx) => this.submit(cx))));
  }
}
