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

> **Status: early, and in daily use.** `status`, `health`, `history`, `top`
> and the background recorder work. `report` and `export` are next (see
> [docs/README.md](./docs/README.md)). There is no release yet; install from
> source below.

## Why another battery tool

Activity Monitor only shows the last 12 hours, and menu-bar apps show live
power. Nothing keeps **per-app energy accumulated over days and weeks**, or
answers "what drained *that* charge?". That is what batlog is for.

## Install

You need Go 1.27 or newer (`brew install go`).

```sh
go install github.com/jufianto/batlog@latest   # installs to ~/go/bin/batlog
batlog daemon install                          # start recording, now and at every login
```

Or build from a clone: `CGO_ENABLED=0 go build .`

`batlog daemon install` installs a per-user LaunchAgent. It needs no sudo and
records one sample a minute. Remove it with `batlog daemon uninstall`; your
data is kept.

## Commands

| Command | Answers |
|---|---|
| `batlog status` | level, drain rate, watts, a steady time-left estimate, the app using the most energy now |
| `batlog health [--trend]` | real capacity vs design, cycles, temperature, condition; decline per month |
| `batlog history [--today\|--week\|--since 3d]` | first charge, last unplug, and each battery session with its ID and drain |
| `batlog top [--today\|--week\|--since 12h\|--session <id\|last>\|--live]` | which apps used the most energy, and roughly what each cost the battery |
| `batlog daemon install\|status\|logs\|uninstall` | the background recorder |
| `batlog report` · `batlog export` | *coming next*: a weekly digest, and your raw data as CSV/JSON |

Every command takes `--json`. Exit codes: 0 success (including "no data yet"),
1 operational error, 2 usage error.

A typical workflow when a charge drained too fast:

```sh
batlog history --week          # find the session, e.g. 0926-1656
batlog top --session 0926-1656 # which apps drained it, and its heaviest 30 minutes
```

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
