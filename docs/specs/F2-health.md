# F2 — `batlog health`

**Story:** As a user worried my battery "isn't what it used to be", I want hard
numbers on battery health and its trend, so I know how much of my problem is
the cell versus my apps.

## CLI contract

```
batlog health [--trend] [--json]
```

## Behaviour

1. Probe `ioreg -rc AppleSmartBattery -a` and read: `CycleCount`,
   `DesignCapacity`, `AppleRawMaxCapacity`, `NominalChargeCapacity` (optional),
   `Temperature` (÷ 100 → °C), `Voltage` (mV), `Amperage` (mA),
   `PermanentFailureStatus`.
2. **Health % = `AppleRawMaxCapacity` / `DesignCapacity` × 100**, one decimal.
   This is the gauge chip's measured full-charge capacity — the real number.
3. If `NominalChargeCapacity` exists, also compute Apple's figure
   (`NominalChargeCapacity` / `DesignCapacity`) and show it labelled
   *Apple reports*. That is the number System Settings shows; showing both
   stops users thinking batlog is wrong ([ADR-0002](../adr/0002-battery-via-ioreg-plist.md)).
4. `--trend`: read the `health` table (one row a day, written by the daemon).
   Show raw health % at the start and end of the last 90 days and the slope
   of a linear fit in %/month. Needs ≥ 2 rows at least 30 days apart: the
   gauge's raw maximum recalibrates by several percent over days (seen live:
   85.4 % → 90.4 % in a week) while real wear is under 1 %/month, so a
   shorter fit reports noise as a trend.
5. Condition: `PermanentFailureStatus` = 0 → `Normal`; anything else → show
   the raw value and the text `Service recommended`, in red.

## Output

Human:
```
🔎 Battery health
health         86.8%   (5 424 / 6 249 mAh design)
Apple reports  89.2%   (smoothed)
cycles         388
temperature    31.3 °C
voltage        12.50 V
condition      Normal

trend (90 days)   87.9% → 86.8%   ≈ −0.37 %/month
```

JSON: `{health_pct, apple_health_pct, raw_max_mah, nominal_mah, design_mah,
cycle_count, temperature_c, voltage_v, condition,
trend: {from_pct, to_pct, pct_per_month, days, last_day} | null}`.
`days` is the span between the first and last row used; the human label
shows it (`trend (60 days)`). If the newest row is more than 3 days old the
line ends with `(last recorded DD Mon)`. Rows whose day does not parse are
skipped.

## Edge cases

| Case | Behaviour |
|---|---|
| `--trend` with < 2 rows or < 30 days span | Print current values + `trend: not enough history yet (have N days, need 30)` |
| `NominalChargeCapacity` missing (older Intel) | Omit the *Apple reports* line |
| `DesignCapacity` or `AppleRawMaxCapacity` missing | Omit health % with a note naming the missing key |
| Condition ≠ Normal | Render in red; include the raw status value |
| No battery | Exit 1: `no battery found` |

## Acceptance criteria

- [ ] On an Apple Silicon Mac, *Apple reports* matches System Settings →
      Battery → Maximum Capacity within ±1 point.
- [ ] Health % equals `AppleRawMaxCapacity` / `DesignCapacity` on every
      fixture (compare against coconutBattery on a real machine once).
- [ ] Golden-file tests pass for ≥ 3 `ioreg -a` fixtures (Apple Silicon, Intel
      with nominal key, Intel without), never crashing on a missing key.
- [ ] `--trend` slope matches a manual linear fit on seeded rows.
