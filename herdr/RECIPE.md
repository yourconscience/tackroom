# tackroom

Herdr's main layout with tackroom's Crons panel on the right: every scheduled job on every host tackroom scans, with Run now, Pause and Resume, one click from the tab bar. Spaces, agents, tabs, panes, Files and Changes work as in main.

## What you see

- **Everything from main**, unchanged: the 268px sidebar with spaces and agents, the 42px tab bar, panes as cards, the 28px status bar.
- **A Crons button** at the right end of the tab bar, after main's Files and Changes: a 14px clock icon and "Crons", 12px, in an 84px block that continues the tab bar's `chrome` background and bottom border. It reads `foreground` on `selected` while the Crons panel shows, `muted` otherwise.
- **The Crons panel** in the right column (368px, 280 to 900, hidden until opened), the third child of the `panel-body` stack after Files and Changes. What it shows is in `mods/tackroom/RECIPE.md`.

## How it behaves

- Crons opens the panel on the Crons view; clicking it again, or Files or Changes, hides it or switches the view, as main's buttons do.
- The panel collects when it opens and every 5 minutes, and acts through `tackroom cron run|pause|resume`.
- Switching spaces restores that space's Files or Changes choice, as in main; Crons is not remembered per space.

## Built from

- `layouts/tackroom.json`: a clone of main 0.1.15 (`cloned_from`). Main's `tabs` block becomes the first child (`weight: 1`) of a 42px row `tabs-row`, with a second block `crons-toggle` (84px, `window_drag`) showing `tackroom-toggle`. `panel-body` gets a third block, `tackroom`, showing the `tackroom` mod with props `{ "refresh": 300 }`.
- `mods/tackroom-toggle`: `place: "none"`; reads `layout()` for the `panel` column and `panel-body` stack and runs `layout.select {stack: "panel-body", child: "tackroom"}` with `layout.show {id: "panel"}`, or `layout.hide {id: "panel"}`. Icon: `mods/tackroom/crons.svg`.
- `mods/tackroom`: the panel; it runs the `tackroom` program (see its recipe).
- Shipped mods it uses: `workspace-chrome`, `spaces`, `agents`, `tabs`, `pane`, `files`, `changes`, `workspace-status`.
- `layouts/tackroom.svg`: the Settings card, main's frame with the panel open and one failing job lit.

## Watch out

- The panel needs `tackroom` on the PATH Herdr runs programs with, and the default config (`~/.agents/tackroom.yaml`). Otherwise set the `tackroom` block's props: `"tackroom": "~/.local/bin/tackroom"`, `"config": "~/.agents/crons.yaml"`.
- Main's `tabs` mod draws only the Files and Changes buttons and remembers only those per space; that is why Crons is a separate block, and why a space switch can close the Crons panel.
- On Windows, main puts the window buttons at the end of the tab bar, so the Crons block lands right of them. tackroom's actions work on launchd jobs only.
- To add the panel to another layout instead, copy only `mods/tackroom` and add its block to that layout's `panel-body` stack, with whatever opens it there.

## Done when

- `herdr-app check mods/tackroom` and `herdr-app check mods/tackroom-toggle` pass, and `layouts/tackroom.error` does not appear.
- `layout.switch {"name": "tackroom"}` shows main with a Crons button after Changes.
- Crons opens the panel with the same hosts and jobs as `tackroom view`'s Crons tab; clicking it again hides the panel.
