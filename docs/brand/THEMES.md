# Theme candidates

Graphite is the applied theme: `docs/site/tokens.css` and `tokens-graphite.css` hold it. The other three candidates below were explored and not taken. The dashboard and terminal panels are not rethemed yet.

## Diagnosis

1. Brass does three jobs. It is the accent, the pinned state and the rail, so pinned and the accent are one colour (OKLab distance 0.05 in dark) and held orange sits beside both (0.07; 0.09 in light). Spent on the large Copy slab and a rail redrawn on every row of the phone grid, it reads as mustard.
2. No clear ground. In dark the surface (`#131217`) is darker than the page (`#171719`); plates carry a double border; vendor marks sit on white patches; black terminal slabs are a third ground in both modes.
3. A UI product sold through terminal dumps. The hero's most detailed object is a wrapped `tackroom status` log that overflows on phones, and the dashboard uses yet another palette (white primary, blue focus, dark only).

## Candidates

Every candidate keeps the `--tr-*` names of `docs/site/tokens.css`; a pick is a file swap (`tokens-<slug>.css`). Status shapes are fixed across themes: pinned is a solid stamp, held and findings are dashed, pass is outlined, blocked is a 2px solid fill, each with its glyph (`✓ ! ✗`) and word. Marks keep one slot shape (32px, 22% radius): marks that bring their own tile are shown full size with no backing, the rest sit on a neutral backing, never recoloured.

| Theme | Ground (light / dark) | Accent | Type | Pinned |
|---|---|---|---|---|
| Graphite | `#fafafa` / `#141416` | ink, `#111113` / `#f2f2f3` | Public Sans, IBM Plex Mono | blue `#0b5cad` / `#6aaef0` |
| Ledger | `#fbfbfa` / `#121315` | ink blue `#0b5cad` / `#6aaef0` | Public Sans, IBM Plex Mono | ink stamp |
| Instrument | `#f5f6f7` / `#000000` | signal cyan `#007385` / `#3cc8de` | IBM Plex Sans, IBM Plex Mono | ink stamp |
| Rosette | `#e9e6e1` / `#1c1a18` | magenta `#b0136a` / `#f27cbd` | Public Sans (800), IBM Plex Mono | ink stamp |

- **Graphite.** Monochrome; colour appears only where the lock speaks. It is the dashboard's existing white-primary look given a light mode, so the site finally looks like the product.
- **Ledger.** Light first, a ruled book of pins: white paper, black ink, one ink blue for actions. Terminal blocks become light inset pages. Its dark mode is close to Graphite's.
- **Instrument.** Dark first, true black, cyan only for what you can act on, Plex Sans for an engineered voice. Light mode keeps black readouts on purpose.
- **Rosette.** Warm stone with the show-ribbon magenta, pill controls, heavier display. Blocked is pushed to vermilion and always filled.

## Recommendation

1. **Graphite.** It removes all three causes with the least new identity to maintain, and it is the only candidate whose look survives inside Herdr, the Claude Code pane and Pi, which receive neutrals and red, amber and green from the host and nothing else. Every coloured mark means a lock state, which makes the lock the most designed thing on every surface. Risk: it can read plain; the rail, plates and lock components carry the character.
2. **Ledger,** if the maintainer wants a light-first site with one hue of its own. Best light mode of the four.
3. **Instrument,** if dark first is the identity. Cyan on black is a common developer look.
4. **Rosette.** Most distinctive, but warm stone drifts toward skillshare's paper and magenta reads consumer.

Contrast: 290 text and UI pairs across five themes and two modes; all candidate pairs pass (body 4.5:1, UI 3:1). The only fails are the current theme's faint grey (3.22 light, 3.53 dark), used as text in the dashboard. Closest status pair per candidate: 0.10 to 0.16 OKLab, against 0.05 today.

## UI first

The web view (`tackroom view`) becomes the primary surface; `tackroom tui` and the CLI are the same state for scripts and SSH.

- **Site and README (restyle plus new assets).** Hero: headline, install command, a desktop frame of the view with the phone view overlapping it (real screenshots from the demo home, the slot already marked in `docs/site/index.html`). Order: hero, the lock as UI components, the agents rail (one rail, vertical on phones), first five minutes ending in `tackroom view`, one compact terminal block without box-drawing tables, comparison, name, FAQ. README's first screen swaps its status block for the view screenshot; `docs/brand/BRAND.md` "Do" needs the same edit.
- **Dashboard restyle.** Map `internal/app/web/style.css` variables (`--ground`, `--surface`, `--primary`, `--focus`, pills) onto `--tr-*`, add the light theme and the prefers-color-scheme guards, raise body text from 13px to 14px and controls from 34px to 44px on touch.
- **Dashboard new work.** A Lock view (pins, holds with the 72-hour bar, audit results, actions; needs to check that the inventory JSON carries holds and audit state); a phone layout with a bottom tab bar instead of the folded top menu; a compact panel mode for widths of 320 to 420px (container queries, no rail); a web app manifest and icons for install, which only helps once the phone can reach the local server.
- **Philosophy.** `docs/philosophy.md` has a section "The CLI is the product". It needs rewording to say the CLI is the source of truth and the view the primary surface; not edited here.

## Applying the pick to each surface

| Surface | Files | What it can style itself |
|---|---|---|
| Site, docs | `docs/site/tokens.css`, `style.css`, `tools/sitedocs/docs.css` | Everything. Token swap, then the hero and rail changes above. Instrument also needs IBM Plex Sans woff2 (OFL) in `docs/site/fonts/`. |
| `tackroom view` | `internal/app/web/style.css`, `index.html`, `app.js` | Everything; today it has its own token set. Logo symbol in `index.html` is the old lavender one. |
| Herdr | `herdr/mods/tackroom/main.js`, `herdr/layouts/tackroom.json` | Layout, sizes, glyphs, labels and which `theme()` slot each element uses. Herdr cannot embed a web page, so the panel stays native; to carry the theme it would pass token values as colours instead of slots, which fights the user's Herdr theme. Sentence-case its uppercase section titles. It already opens the full view with `exec`. |
| Tern | `tern/plugins/tackroom-crons/` | The native block takes span tones only. Tern's browser block beside a pane, with `tern web serve`, can host the compact web view itself, so in Tern the theme applies in full. |
| Claude Code pane | `claude/hooks/register.tsx` | Ink colour names (success, warning, error, inactive), bold and dim. No palette. |
| Pi | `pi/extensions/tackroom.ts` | The terminal's palette. No palette of its own. |

## Least sure

- Graphite as first pick: the maintainer called the old near-black site less interesting, and Graphite is closest to it in mood.
- Pinned as a solid stamp in every theme: strong, but it makes the most common state the heaviest mark.
- Ledger's dark mode is nearly Graphite's; a bluer dark ground would collide with HarnessKit's indigo.
