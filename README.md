# batlog

[![ci](https://github.com/jufianto/batlog/actions/workflows/ci.yml/badge.svg)](https://github.com/jufianto/batlog/actions/workflows/ci.yml)
[![MIT licence](https://img.shields.io/badge/licence-MIT-blue.svg)](./LICENSE)

A macOS command-line tool that records your battery and per-app energy in the
background, so "my battery only lasts 5 hours" becomes a question you can
answer with numbers.

```
$ batlog top --session last
⚡ session 0929-1903 · 19:03 → now · 3h 24m awake · 100% → 41% (17.3 %/hr)
 #   APP                    SHARE   BATTERY COST
 1   T3 Code (Alpha)        46%     ≈ 9% of battery
 2   WindowServer ⚙         9%      ≈ 2%
 3   AddressBookManager ⚙   8%      ≈ 2%
 4   Google Chrome          7%      ≈ 1%
 5   kernel_task ⚙          6%      ≈ 1%
app energy from about 21:45 only, when recording started; battery cost covers the 20% used since
heaviest 30 min: 21:20 → 21:50 · 20.0 W average
```

> **Status: early (v0.1, pre-release), and in daily use.** Every v1 command
> works: `status`, `health`, `history`, `top`, `report`, `export` and the
> background recorder.

## Why another battery tool

Activity Monitor only shows the last 12 hours, and menu-bar apps show live
power. Nothing keeps **per-app energy accumulated over days and weeks**, or
answers "what drained *that* charge?". That is what batlog is for.

## Install

**With Homebrew:**

```sh
brew install jufianto/tools/batlog
batlog daemon install     # start recording, now and at every login
```

**Or download a release.** Get the archive for your Mac from
[Releases](https://github.com/jufianto/batlog/releases): `darwin_arm64` for
Apple Silicon, `darwin_amd64` for Intel. Then:

```sh
tar -xzf batlog_*_darwin_*.tar.gz
xattr -d com.apple.quarantine batlog   # the binary is not notarized yet
mv batlog /usr/local/bin/              # or anywhere on your PATH
batlog daemon install                  # start recording, now and at every login
```

**Or with Go** 1.27 or newer (`brew install go`):

```sh
go install github.com/jufianto/batlog@latest   # installs to ~/go/bin/batlog
batlog daemon install
```

Or build from a clone: `CGO_ENABLED=0 go build .`

`batlog daemon install` installs a per-user LaunchAgent. It needs no sudo and
records one sample a minute. Remove it with `batlog daemon uninstall`; your
data is kept.

### Shell completion

Homebrew installs tab completion for zsh, bash and fish. Open a new terminal
and try `batlog hi<Tab>`.

**zsh with oh-my-zsh (or another framework):** if Tab does nothing, Homebrew's
completion folder is not on `FPATH` when the framework runs `compinit`. Add
this line to `~/.zshrc` **before** `source $ZSH/oh-my-zsh.sh`:

```sh
FPATH="$(brew --prefix)/share/zsh/site-functions:${FPATH}"
```

Then open a new terminal. `echo $_comps[batlog]` should print `_batlog`.

**Plain zsh:** make sure `~/.zshrc` runs `autoload -Uz compinit && compinit`
after setting the `FPATH` above.

**bash:** needs `brew install bash-completion@2` and its line in
`~/.bash_profile` (`brew info bash-completion@2` prints it).

**Without Homebrew,** write the script yourself:

```sh
# zsh: then add  fpath=(~/.zsh/completions $fpath)  to ~/.zshrc, before compinit
mkdir -p ~/.zsh/completions && batlog completion zsh > ~/.zsh/completions/_batlog
# bash (with bash-completion 2)
mkdir -p ~/.local/share/bash-completion/completions
batlog completion bash > ~/.local/share/bash-completion/completions/batlog
# fish
batlog completion fish > ~/.config/fish/completions/batlog.fish
```

## Commands

| Command | Answers |
|---|---|
| `batlog status` | level, drain rate, watts, a steady time-left estimate (or time to full while charging), the app using the most energy now |
| `batlog health [--trend]` | real capacity vs design, cycles, temperature, condition; decline per month |
| `batlog history [--today\|--week\|--since 3d]` | first charge, last unplug, each battery session with its ID and drain, and each charge: time to full and how long it then sat at 100% |
| `batlog top [--today\|--week\|--since 12h\|--session <id\|last>\|--live]` | which apps used the most energy, and roughly what each cost the battery |
| `batlog daemon install\|status\|logs\|uninstall` | the background recorder |
| `batlog report [--daily\|--weekly\|--since 3d]` | a digest: battery life, worst drain, top 5 apps, your charges (time to full, time left at 100%), charging habits with warnings, health |
| `batlog export samples\|apps\|health [--since 30d] [--json] [-o file]` | your raw data as CSV (the default) or JSON, for your own charts |
| `batlog ui` | all of it in one full-screen view: status, a battery chart with your sessions and charges, apps, the report and health |

Every command takes `--json`. Exit codes: 0 success (including "no data yet"),
1 operational error, 2 usage error.

A typical workflow when a charge drained too fast:

```sh
batlog history --week          # find the session, e.g. 0926-1656
batlog top --session 0926-1656 # which apps drained it, and its heaviest 30 minutes
```

### `batlog ui`

A full-screen view in the terminal, for when you would rather look around
than run five commands. The sidebar keeps the live status and health in
view; the main area has four views:

1. **Battery:** battery % over today or the week (green on AC, dim asleep),
   and every battery session and charge. Enter on one shows its chart and
   top apps, or a charge's time to full and time at 100%.
2. **Apps:** which apps used the most energy and what each cost the
   battery. Enter on one shows its energy by hour or day.
3. **Report:** the daily or weekly report.
4. **Health:** health now and per day since recording began.

Keys: `1`–`4` switch views, `↑↓` select, `enter`/`esc` open and close,
`t`/`w` today or the last 7 days, `[` `]` the day or week before or after,
`?` help, `q` quit. It refreshes every minute and only reads: it never
writes to your data. It needs a window of at least 80×24.

### Reading `top`

- **SHARE** is an app's part of all app energy in the range.
- **BATTERY COST** is its share of the energy used *on battery*, times the
  percent the battery dropped while awake. It is an estimate, and it sums to
  the percent used.
- **⚙** marks macOS itself: WindowServer, kernel_task, the Contacts and
  Spotlight daemons, and so on. Activity Monitor hides these, but their drain
  is real, so batlog shows them. It never names one as the "worst" app,
  because you cannot quit them.
- An app's helpers and child processes count as the app: Chrome's renderers
  are Chrome, and an editor's language servers are the editor.

## How it works

- **Battery:** `ioreg -rc AppleSmartBattery`, parsed as a plist.
- **Per-app energy:** the kernel's per-app *coalition* energy counters
  (CPU, GPU, Neural Engine, in nanojoules). This is the grouping Activity
  Monitor uses, and it includes processes that have already exited. batlog
  reads them with plain syscalls: no sudo, no `powermetrics`, and it costs
  about 0.004 % of the energy it measures.
- **Storage:** SQLite in `~/Library/Application Support/batlog/`:
  - one battery sample a minute;
  - app energy in 15-minute buckets;
  - raw data kept 90 days, then rolled up into one row per day.

  That comes to a few MB a month.
- **Pure Go:** `CGO_ENABLED=0`, one static binary.

## Principles

- **Local only.** All data stays on your Mac. No telemetry, no update checks,
  no network calls at all.
- **Measure, never change.** batlog does not throttle processes, toggle Low
  Power Mode or limit charging.
- **No sudo.** Everything works as a normal user.
- **Cheap.** batlog must never show up in its own top ten.

## Requirements

- macOS on Apple Silicon or Intel.
- Tested on macOS 15 with Apple Silicon.
- Older macOS versions and Intel Macs should work, but are not verified yet.
  A capture from your Mac helps: see [CONTRIBUTING.md](./CONTRIBUTING.md).

## Contributing

Contributions are welcome: bug reports, battery captures from Mac models we
have not tested, docs and code. [CONTRIBUTING.md](./CONTRIBUTING.md) explains:
- how to build and test;
- how the design docs work;
- which contributions help most right now.

Please report security problems privately, as [SECURITY.md](./SECURITY.md)
describes.

## Licence

[MIT](./LICENSE) © Jufianto Hendri
