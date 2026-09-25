# Functional specs

One spec per command. Each follows the same shape: **story → CLI contract →
behaviour → output (human + JSON) → edge cases → acceptance criteria.** All
implement [prd/tracker.md](../prd/tracker.md). Shared rules are in
[docs/README.md](../README.md).

| ID | Command | Needs the daemon? | Depends on |
|----|---------|-------------------|------------|
| [F1](./F1-status.md) | `batlog status` | optional (drain rate) | ioreg probe; `samples` |
| [F2](./F2-health.md) | `batlog health` | only for `--trend` | ioreg probe; `health` |
| [F3](./F3-history.md) | `batlog history` | yes (pmset fallback) | `samples`; sessions |
| [F4](./F4-top.md) | `batlog top` | for accumulated modes | top probe; `app_energy` |
| [F5](./F5-daemon.md) | `batlog daemon` | — | all probes, store, launchd |
| [F6](./F6-report.md) | `batlog report` | yes | F2 + F3 + F4 |
| [F7](./F7-export.md) | `batlog export` | yes | `samples`, `app_energy`, `health` |

**Build order:** F1 → F2 → F5 → F3 → F4 → F6 → F7.

## Tables (shared by every spec)

```
samples     ts, pct, on_ac, charging, watts, raw_cur_mah, raw_max_mah
app_energy  ts, app, energy, cpu_pct, is_system         -- top 15 per tick
health      day, cycles, raw_max_mah, nominal_mah, design_mah, temp_c, condition
meta        key, value                                   -- last_tick, schema_version
```

Raw `samples`/`app_energy` older than 90 days are rolled up into
`daily_rollup` (per day: minutes on battery/AC/asleep, pct consumed, per-app
energy sum) and deleted. `health` is kept forever (one row a day).
