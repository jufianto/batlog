# PRD — batlog tracker (v1)

**Owner:** Jufianto · **Date:** 2026-09-26 · **Status:** Draft — supersedes the June 2026 "macbat" draft

> A macOS CLI that quietly records your battery and per-app energy in the background, so "my battery only lasts 5 hours" becomes a question you can answer with numbers.

## Problem

My MacBook used to last all day; now it lasts ~5 hours, and macOS won't tell me why. Activity Monitor's Energy tab only covers the recent past, System Settings shows a battery-level chart but not which apps caused the drops, and battery health is a single number with no history. So I can't tell whether the cell is wearing out or an app is draining it — or which app.

Existing tools don't close the gap: menu-bar apps (MacBat, StillCore) show live power, threshold notifiers (qzeleza/macbat) nag about charge levels. None keeps **per-app energy accumulated over days**.

## Target user

Me first, then other terminal-comfortable MacBook users — mostly developers running heavy tools (Docker, browsers, IDEs) who suspect one of them is eating the battery. macOS 12+, Apple Silicon or Intel, installs via Homebrew. Not for: desktop Macs, people who want a GUI.

## Core workflow

Install once → a background process records battery and per-app energy every 60 s → any time later, `batlog top --week` and `batlog history` answer **"which apps cost me the most battery, and how long did it actually last?"** The accumulated per-app history is the product; every other command supports it.

## v1 scope

| Command | Answers |
|---|---|
| `batlog status` | Level, source, drain %/hr, power draw (W), a steady time-left estimate |
| `batlog health` | Real (gauge-measured) capacity vs design, cycles, temperature, condition; `--trend` shows decline per month |
| `batlog history` | First charge today, last unplug, each battery session's length and drain |
| `batlog top` | Top apps live, and **accumulated per day/week with estimated battery cost** |
| `batlog daemon` | Install/uninstall/status/logs for the background recorder |
| `batlog report` | Digest: battery-life trend, worst sessions, top apps, charging habits, health |
| `batlog export` | Raw samples as CSV or JSON, for your own charts |

Every command supports `--json`. Behaviour lives in `docs/specs/`, one spec per command.

## Success metrics

1. `batlog history --today` answers first charge / last on battery / how long it lasted in **< 1 s**.
2. At the end of a day, the top 3 apps from `batlog top --today` match Activity Monitor's *12 hr Power* column in **4 of 5** spot checks.
3. The recorder runs **30 days unattended** with no crash or data loss, uses < 1% CPU, and never appears in its own top 10.
4. After 30 days I can state my health change and my top 3 drain apps **as numbers**.

## Out of scope for v1

- **`batlog ui`**, a lazygit-style terminal UI → v1.1 (its history panels need real data to design against)
- **Charge-threshold notifications** → v1.1, its own PRD
- **Changing anything on the system:** no process throttling, Low Power toggles or charge limiting — batlog only measures
- Menu bar / GUI, iPhone/iPad batteries, desktop Macs
- Any network call — no telemetry, no update checks, no cloud sync
- Per-app watts — Apple's energy impact is unitless, so apps are shown as a relative share
- Anything that requires `sudo` (`powermetrics` stays optional)

## Riskiest assumption

Sampling `top -l 2 -o power` once a minute and summing it over a day produces a per-app ranking that matches reality — and the sampling costs less battery than it measures.

**Test (2 days, no Go):** a shell loop that appends `top -l 2 -o power -stats pid,command,power` to a CSV every 60 s. Each evening, compare the summed ranking with Activity Monitor's *12 hr Power* column, and check the loop's own cost in `top`. If the rankings disagree, rethink the data source before building `top` or the recorder.
