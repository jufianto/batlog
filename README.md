# batlog

A macOS command-line tool that records your battery and per-app energy in the
background, so "my battery only lasts 5 hours" becomes a question you can
answer with numbers.

> **Status: design phase.** The docs are written; the code is not. See
> [docs/](./docs/) for the product requirements, decisions and specs.

## What it will do

```
batlog status            level, drain rate, watts, a steady time-left estimate
batlog health            cycles, real capacity vs design, temperature, trend
batlog history --today   first charge, last unplug, how long each session lasted
batlog top --week        which apps cost you the most battery — accumulated
batlog report            weekly digest: battery-life trend, worst apps, habits
batlog export --csv      your raw data, for your own charts
batlog daemon install    the background recorder (a per-user LaunchAgent)
```

Every command takes `--json`.

## Why another battery tool

Activity Monitor only shows the last 12 hours. Menu-bar apps show live power.
Nothing keeps **per-app energy accumulated over days and weeks** — that is
what batlog is for.

## Principles

- **Local only.** All data stays in `~/Library/Application Support/batlog/`.
  No telemetry, no update checks, no network calls at all.
- **Measure, never change.** batlog does not throttle processes, toggle Low
  Power or limit charging.
- **No sudo.** Everything works as a normal user.
- **Cheap.** Under 1 % CPU; batlog must never show up in its own top ten.

## Requirements

macOS 12 or newer, Apple Silicon or Intel. Homebrew install will come with the
first release.

## Licence

[MIT](./LICENSE).
