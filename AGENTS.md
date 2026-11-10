# batlog: notes for agents

batlog is a macOS command-line tool and terminal UI, in pure Go, that records
battery and per-app energy in the background. Start with
[README.md](./README.md), then [docs/README.md](./docs/README.md).

## Build and test

```sh
make test                 # what CI runs: brand check, go vet, go test, Linux cross-compile vet
CGO_ENABLED=0 go build .  # produces ./batlog
```

## Docs

- `docs/prd/`, `docs/adr/` and `docs/specs/`: how they work is in
  [CONTRIBUTING.md](./CONTRIBUTING.md#how-the-docs-work). A behaviour change
  updates its command's spec in the same pull request.
- [docs/reference/brand.md](./docs/reference/brand.md): the logo, colours and
  type, which file to use for which job, and how to change each.

## Rules

- Pure Go, `CGO_ENABLED=0` ([ADR-0001](./docs/adr/0001-pure-go-no-cgo.md)).
- Every command takes `--json`; exit codes are 0, 1 and 2 as in
  [docs/README.md](./docs/README.md).
- Local only, no network calls; batlog measures and never changes the Mac.
- Every colour and terminal style is a token in `brand/tokens.json`, and
  `make brand-check` fails on one that is not. Anything visual follows the
  `batlog-brand` skill.

## Skills

| Skill | Use for |
|---|---|
| [`batlog-brand`](./.claude/skills/batlog-brand/SKILL.md) | Anything visual: `batlog ui` styles, colour in command output, the README logo, icons, the brand kit and guide |
