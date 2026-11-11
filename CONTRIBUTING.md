# Contributing

tackroom has one maintainer, so small, checked changes get merged fastest.

## The one rule

You must understand your change and be able to explain it without an AI tool. Using AI is fine. Point your agent at [AGENTS.md](AGENTS.md), which holds the architecture rules and repo conventions.

## Before you start

- **Bug fixes and small doc fixes:** open a PR directly.
- **Anything else:** open an issue first and wait for a reply. This covers new commands, config keys, harness adapters and starter content.

A harness adapter must match what the harness really supports, so link its docs or source.

## Pull requests

- Make one change per PR.
- Describe it briefly in your own words: what broke, what changed, how you checked it. Long generated descriptions may be ignored.
- Keep commit messages to one short line, with no bot trailers.
- Don't add new unit tests. Put what you checked in the PR instead.

## Check it on the real thing

Run `go test ./...` and `go vet ./...`. CI also runs lint, skill frontmatter, memory and packaging checks; `.github/workflows/ci.yml` lists them. If your change affects what tackroom writes, that isn't enough: run it in a temporary home and load the result in the harness itself. A file that parses can still stop a harness from starting.

```bash
TMP_HOME="$(mktemp -d)"
go build -o "$TMP_HOME/tackroom" ./cmd/tackroom
(
  set -e
  export HOME="$TMP_HOME"
  unset TACKROOM_HOME XDG_CONFIG_HOME XDG_STATE_HOME CODEX_HOME
  "$HOME/tackroom" setup --agents codex --memory off --yes
  codex mcp list
)
```

If you touch shared code, such as config loading or `skill_root` handling, try more than one harness. Never test against your live config.

## After you open it

- CI for first-time contributors waits for the maintainer's approval, so checks may not start right away.
- Keep "Allow edits by maintainers" on. The maintainer may push a fix to your branch instead of asking for another round. Keep it, or replace it with your own.
- If a PR that needs your input goes quiet for a few days, the maintainer may finish and merge it, or close it.
- PRs are squash-merged.

## Security

Report security problems privately through the [Security tab](https://github.com/yourconscience/tackroom/security/advisories/new), not in a public issue.

## License

By contributing, you agree that your work is released under the [MIT license](LICENSE).
