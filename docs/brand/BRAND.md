# tackroom brand

Proposal, not yet applied to the site, dashboard or README. Open `preview.html` to compare both directions in light and dark.

## Positioning

tackroom is the strict one: one `~/.agents` repo synced into 15 coding agents, with every external skill pinned to a commit in `tackroom.lock`, audited, and held until it is 72 hours old.

## Two directions

| | A, Rail and plate (recommended) | B, Lavender unified |
|---|---|---|
| Base | dashboard greys (`#171719` ground in dark) plus a new light theme | current site palette |
| Accent | brass: `#c9a45a` fill, `#d8b56e` text (dark); `#b08a3f` fill, `#7a5a17` text (light) | lavender `#b4befe` (dark), `#4f56c9` (light) |
| Type | Public Sans, IBM Plex Mono | Bricolage Grotesque, Geist, Geist Mono |
| Shape | 2px plates, 6px cards and controls, 3px rail | 12px plates and cards, pill controls |
| Tokens | `tokens-a.css` | `tokens-b.css` |
| Mark | `logo-a.svg`, `logo-a-mono.svg` | `logo-b.svg` |

**Recommendation: A.** Reasons, in order:

1. Distance from competitors. Lavender on near-black sits next to HarnessKit's indigo (hue 267). skillshare owns warm paper and crayon. A neutral grey system with one brass accent belongs to neither.
2. Least change where the product lives. The dashboard already uses these greys, Public Sans, IBM Plex Mono and Phosphor. A adds a light theme and one accent; B would replace the dashboard's type and colour.
3. The accent means something. Brass is reserved for the pin, the lock and the rail, which is the feature neither competitor has. In B, lavender is decoration.
4. Fewer fonts. A ships two families (both already self-hosted by the dashboard); B needs three from a CDN.

B stays a valid fallback if the maintainer values continuity with the current site over distance from HarnessKit.

## Tokens

Both files define the same `--tr-*` names, so the site and `tackroom view` can import either one without code changes. Light is the default; dark follows `prefers-color-scheme` unless `<html data-theme="light">` is set, and `data-theme="dark"` forces dark. Every text pair in use reaches 4.5:1 and every UI graphic 3:1; the ratios are listed in `preview.html`.

## Mark

- Geometry is the existing rail and hook. Changes: strokes 7 to 8 units, drawing re-centred on the tile, corner radius 15 to 14.
- Minimum size 16px. Clear space on every side: one stroke width (8 units at 64).
- The tile version is the default. Use `logo-a-mono.svg` (`currentColor`, no tile) for favicons on coloured chrome, terminal panels, and one-colour print.
- The logo's colours are fixed per direction and do not switch with the theme.
- Wordmark: lowercase `tackroom`, display face, weight 700 in A, tracking -0.025em. Never capitalised.

## Status vocabulary

The glyph is the severity and is identical in the CLI, dashboard, site and panels. Colour follows the glyph, never the other way round, so meaning survives `NO_COLOR` and colour blindness. The Phosphor icon names the kind of check.

| State | Glyph | Colour role | Icon | Meaning |
|---|---|---|---|---|
| synced | `✓` | ok (green) | none | native config matches the repo |
| pinned | `✓` | pin (brass in A), glyph ok | push-pin | external skill locked to a commit; show the short hash in mono |
| held | `!` | warn (orange) | timer | new pin younger than 72h; show `41h of 72h` and the time rail |
| audit pass | `✓` | ok | stethoscope | no findings in the pinned content |
| audit finding | `!` | warn | stethoscope | open finding; an unaccepted CRITICAL renders `✗ blocked` in bad |
| drifted | `!` | warn | arrows | agent copy edited by hand; the next sync repairs it |
| missing | `✗` | bad (red) | file-x | declared item absent from the agent's config |

Rules:

- CLI mapping stays ANSI: `✓` green (32), `!` yellow (33), `✗` red (31). This fixes today's red `✗` beside a yellow "drifted".
- Pinned is the only state that uses the accent. Brass and the warn orange are 13 to 17 degrees of hue apart and differ in saturation, and pinned never appears without `✓`, so the two do not depend on colour alone.
- `doctor` should print the same glyphs as `status` and `sync`.
- Words are lowercase in the CLI and sentence case in the web UI. Health words for jobs stay as they are: Healthy, No result, Last run failed, Failing, Paused.

## Nameplates and vendor marks

- A nameplate is finished with the product name alone. Name in the body face at weight 600, the `tackroom.yaml` key under it in mono, the state line last.
- No initial-letter tiles or other shapes that stand in for a logo.
- The vendor mark is optional. It goes in one fixed slot, 24px, top right of the plate, on the neutral backing `#f4f4f6` with a `#76777e` hairline. The backing is the same in light and dark.
- Show a mark unmodified and in its own colours. Never recolour, mask, redraw, crop or animate one, and never put one on the brand accent.
- Only marks whose terms clearly allow this use go in the slot. Six products (Claude Code, Codex, GitHub Copilot CLI, Gemini CLI, Antigravity CLI, Grok Build) need permission first, and two have no official mark. Until a mark is cleared, its plate ships without one.
- The identity must work with every slot empty. No layout, colour or icon decision depends on a vendor mark.
- Each mark that ships is recorded with source, version and licence in `THIRD_PARTY_NOTICES.md`.

## Harness display names

Use these exact names in every surface. The CLI prints the key.

| Display name | Key |
|---|---|
| Claude Code | `claude` |
| Codex | `codex` |
| Factory Droid | `droid` |
| OMP | `omp` |
| OpenCode | `opencode` |
| Pi | `pi` |
| Qwen Code | `qwen-code` |
| Cursor | `cursor` |
| Gemini CLI | `gemini` |
| GitHub Copilot CLI | `copilot` |
| Grok Build | `grok` |
| DeepSeek Harness | `dsh` |
| Antigravity CLI | `agy` |
| Kimi Code | `kimi` |
| Amp | `amp` |

Terminals: Herdr (panel), Tern (workspace), Rex (doctor check). They hang on their own rail and are never counted among the 15 harnesses.

## Icons

One set: Phosphor Regular 2.1.1 (MIT), already shipped by the dashboard. Site, dashboard and panels use the same names. Replace the Lucide-style Herdr crons icon and the site's unattributed copies with Phosphor, credited in the notices file. `✓ ! ✗` are text glyphs, not icons.

## Do

- Put the lockfile on the hero. A pinned hash, a held countdown and an audit result say what no competitor shows.
- Keep the README's first screen to the mark, one line, the install command and one status block.
- Use one tagline everywhere: site title, README, GitHub About, npm and Homebrew.
- Ship vector files for every mark and a 1200x630 social card with the name and line on it.
- Capture screenshots from a real install with demo config, and regenerate them on release.
- Keep numbers such as the harness count in one constant, not typed into prose.
- Write short sentences in plain verbs. No banned words such as seamless, powerful or effortless.

## Don't

- Hand-drawn strokes, paper textures, pinboards, sticky notes or crayon marks (skillshare's look).
- Indigo on near-black, serif headlines over Geist, or cartoon mascots (HarnessKit's look).
- Leather, wood grain, stitching or other tack-room skeuomorphism. The metaphor is structural: a rail, hooks, plates.
- A trust score as a lone coloured number. State plus glyph plus detail, every time.
- Badge walls, star charts, avatar walls or multi-megabyte GIFs above the install command.
- Raster-only marks, several renditions of the logo, or a logo that fails on dark backgrounds.
- Emoji in headings or copy, or em dashes in copy.
- Status colour as the only signal.
