import { View, svg } from "gpui-kit";
import { h_flex } from "gpui-base";
import { run, theme, layout } from "herdr";

// The Crons button of the tackroom layout. Herdr main's tab bar opens Files and
// Changes in the "panel-body" stack; this block sits right of it and opens the
// tackroom panel in the same stack, drawn like those two buttons.
function find(node, id) {
  if (node?.id === id) return node;
  for (const child of node?.children ?? []) {
    const found = find(child, id);
    if (found) return found;
  }
  return null;
}

const stop = (_e, cx) => cx.stop_propagation();

export default class CronsToggle extends View {
  render() {
    const t = theme();
    const root = layout(), panel = find(root, "panel"), body = find(root, "panel-body");
    const on = Boolean(panel && !panel.hidden && body?.active === "tackroom");
    return h_flex().size_full().pr(10).items_center().justify_end().bg(t.chrome).border_b(1).border_color(t.border)
      .child(h_flex().id("panel-tackroom").h(26).px(8).gap(5).flex_none().items_center().rounded(6)
        .text_size(12).bg(on ? t.selected : t.chrome).text_color(on ? t.foreground : t.muted)
        .hover((s) => s.bg(t.hover)).on_mouse_down("left", stop)
        .on_click(() => {
          if (on) run("layout.hide", { id: "panel" });
          else { run("layout.select", { stack: "panel-body", child: "tackroom" }); run("layout.show", { id: "panel" }); }
        })
        .child(svg("mods/tackroom/crons.svg").size(14)).child("Crons"));
  }
}
