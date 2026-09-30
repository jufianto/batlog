# F1 — `batlog status`

**Story:** As a user, I want a one-command snapshot of my battery right now —
level, source, drain rate, watts, time left, and what is eating it — so I can
decide whether to plug in or quit something.

## CLI contract

```
batlog status [--json]
```

No other flags. Must answer in under 1 s.

## Behaviour

1. Probe `ioreg -rc AppleSmartBattery -a` ([ADR-0002](../adr/0002-battery-via-ioreg-plist.md))
   → percent, on AC, charging, macOS time-remaining (`TimeRemaining`, minutes;
   65535 means unknown), watts = voltage × |amperage| / 10⁶.
2. If daemon data exists, read `samples` from the last 10 minutes:
   - **Drain rate** = slope of a linear fit of `pct` over time, in %/hr. Needs
     ≥ 3 samples and no gap > 90 s inside the window; otherwise omitted.
     The fit — not the last two samples — is what keeps the number steady.
   - **batlog estimate** = percent ÷ drain rate, only while discharging. If
     the estimate is over 12 h, or the rate is 0, show `> 12h` instead of a
     number. JSON `est_minutes_left` keeps the minutes whenever the rate is
     above 0, and is `null` only at a rate of 0.
   - **Worst offender** = the non-system app with the most energy in the
     current and previous 15-minute `app_energy` buckets (so 15–30 min of
     data; F4 has the data source). No offender when only system apps used
     energy.
3. Render. Anything that cannot be computed is omitted (human) or `null`
   (JSON). Never guessed.

## Output

Human, on battery with daemon running:
```
🔋 67%  ·  on battery  ·  using 8.4 W
drain       11.8 %/hr   (last 10 min)
est. left   5h 40m  (batlog) · 5h 12m (macOS)
worst now   Google Chrome  (38% of energy)
```

Human, on AC: `⚡ 82% · AC · charging at 41.8 W`, no drain or estimate lines,
worst offender kept.

The watts are the power through the battery, without its direction: what
the Mac uses on battery, or what goes into the battery while charging. On
AC but not charging they are neither, so the human line leaves them out
(`⚡ 100% · AC · charged`); JSON `watts` keeps the reading.

JSON (stable):
```json
{
  "ts": 1790000000,
  "percent": 67,
  "on_ac": false,
  "charging": false,
  "watts": 8.4,
  "drain_pct_per_hr": 11.8,
  "est_minutes_left": 340,
  "macos_est_minutes": 312,
  "worst_offender": {"app": "Google Chrome", "energy_share": 0.38}
}
```

## Edge cases

| Case | Behaviour |
|---|---|
| Daemon never installed / no recent samples | Live fields only, plus `tip: run 'batlog daemon install' for drain analysis` unless the database exists but cannot be opened |
| Just unplugged (< 3 samples on battery) | Omit drain; show `drain: collecting…` |
| Sleep gap inside the 10-min window | Use post-wake samples only; if < 3, omit |
| `TimeRemaining` = 65535 | Omit macOS estimate |
| `ioreg` output not recognised | Exit 1: `cannot read battery (ioreg output not recognised)` |
| No battery (desktop Mac) | Exit 1: `no battery found on this machine` |

## Acceptance criteria

- [ ] `batlog status` answers in < 1 s with percent, source, state and watts
      on any MacBook with no daemon.
- [ ] With ≥ 10 min of daemon samples on battery, the drain rate is within
      ±1 %/hr of a manual linear fit over the same rows.
- [ ] `--json` matches the schema; missing values are `null`.
- [ ] On AC, no drain rate or estimate is shown.
- [ ] Percent is correct on both an Apple Silicon fixture (`MaxCapacity`=100)
      and an Intel fixture (mAh keys).
