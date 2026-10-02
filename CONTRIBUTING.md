# Contributing

Thanks for helping. Bug reports, captures from Mac models we have not
tested, docs fixes and code are all welcome. For anything bigger than a fix,
open an issue first so we can agree on the approach before you build it.

## How the docs work

Three kinds of document, each with a strict length:

| Folder | What | Length |
|---|---|---|
| `docs/prd/` | Why a feature exists, for whom, what "done" means | one page |
| `docs/adr/` | Why we chose X over Y, and what we gave up | under one page, append-only |
| `docs/specs/` | Exactly how one command behaves, with edge cases | 1–2 pages, written just before building |

A new command that serves an existing PRD gets a spec. A new *problem* gets a
PRD. A hard-to-reverse technical choice gets an ADR; an easy one gets a code
comment.

## The most useful thing you can contribute right now

batlog reads the battery through `ioreg`, and the keys differ between Mac
models. If you have an Intel Mac, or a macOS version we have not tested, run:

```sh
ioreg -rc AppleSmartBattery -a > "batt-$(sysctl -n hw.model)-$(sw_vers -productVersion).plist"
```

and attach the file to an issue. It becomes a test fixture, and batlog is then
verified against your model forever. The file contains your battery's serial
number — remove the `Serial` lines first if you would rather not share them.

### Check `batlog top` against Activity Monitor

After batlog has recorded for half a day, compare these two:
- `batlog top --since 12h`;
- Activity Monitor → Energy → *12 hr Power*.

The top three non-⚙ apps should be in the same order. If they are not, open
an issue with both lists and your Mac model. App naming is a heuristic, and
real mismatches are how it gets better.

## Build and test

```sh
CGO_ENABLED=0 go build .      # produces ./batlog
CGO_ENABLED=0 go test ./...   # no test shells out; passes on Linux too
```

Go 1.27 or newer. With an older Go installed, the `toolchain` line in
`go.mod` makes `go` download the right version automatically.

Battery parsing is tested against `ioreg` dumps in
`internal/battery/testdata/`. `intel-synthetic.plist` is hand-written; a real
Intel dump (see above) would replace it.

`internal/energy` has a live test that reads the running kernel's energy
counters. It runs only on macOS, and skips itself on machines that report
no energy (some virtual machines).

## Pull requests

- Keep one change per PR, with tests. Behaviour changes update the command's
  spec in the same PR.
- CI runs `go vet`, `go test` and `go build` on Linux, then on macOS once
  Linux passes. Changes that touch only docs (`*.md`, `docs/`) skip CI.
- Commit messages say what changed and why, e.g. `fix(top): …` or
  `feat(daemon): …`.

## Releasing

Releases are built with [GoReleaser](https://goreleaser.com) from
`.goreleaser.yaml`. Until the release runs on GitHub Actions, run it on a
Mac from a clean `master`:

```sh
git tag -a v0.2.0 -m v0.2.0 && git push origin v0.2.0
GITHUB_TOKEN=$(gh auth token) goreleaser release --clean --release-notes notes.md
```

`goreleaser release --snapshot --clean` builds everything into `dist/`
without publishing.

The release also commits `Casks/batlog.rb` to
[jufianto/homebrew-tools](https://github.com/jufianto/homebrew-tools), so
`brew upgrade batlog` picks it up. Check the rendered cask in
`dist/homebrew/Casks/` before a real release.
