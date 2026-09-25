# F4 — `batlog top`

**Story:** As a user, I want to know which apps use the most energy — right
now *and* accumulated over the day or week — so I know what to quit or
replace. The accumulated view is the reason batlog exists.

## CLI contract

```
batlog top [--live] [--today] [--week] [--since <dur>] [-n N] [--json]
```

Default: `--today` if daemon data exists, else `--live`. `-n` default 10.

## Behaviour

**Live:** run `top -l 2 -o power -n 40 -stats pid,command,power,cpu,mem`
([ADR-0003](../adr/0003-app-energy-from-top-without-sudo.md)). Use the
second sample only; the first always reads zero. Group, rank by energy, show
the top N with share = energy / total.

**Accumulated:**
```sql
SELECT app, SUM(energy) AS e FROM app_energy
WHERE ts BETWEEN ? AND ? GROUP BY app ORDER BY e DESC LIMIT ?
```
share = e / SUM over all apps in the range. **Estimated battery cost** =
share × total battery percent consumed while discharging in the range,
rendered as `≈ 31% of your battery`.

**Grouping (shared with the daemon, one function):**
1. Strip ` Helper`, ` Helper (Renderer)`, ` Helper (GPU)`, ` Helper (Plugin)`
   → the parent app's name.
2. Otherwise group by command name. `kernel_task`, `WindowServer`, `mds*`,
   `coreaudiod` and other known system processes are tagged `system` and shown
   with ⚙.

## Output

Human, `--today`:
```
⚡ top energy · today   (9h 17m tracked, 6h 05m on battery)
 #  APP                SHARE   EST. BATTERY COST
 1  Google Chrome       34%    ≈ 31% of battery
 2  Docker              18%    ≈ 17%
 3  WindowServer ⚙      11%    ≈ 10%
 …
shares are relative (Apple energy-impact units); battery cost is an estimate
```

JSON: `{range, tracked_minutes, rows: [{app, energy_share, est_battery_pct,
is_system}]}`. Live mode adds `pid`, `cpu_pct`, `mem_mb` per row.

## Edge cases

| Case | Behaviour |
|---|---|
| `--today` but no daemon data | Fall back to `--live` with a notice |
| App ran only while on AC | Appears in share with `est_battery_pct: 0` |
| < 30 min tracked in range | Show the table + `short window — shares may be noisy` |
| `top` columns not recognised | Exit 1 naming the macOS version; never print partial rows |
| Two apps with the same command name | Merged (documented limitation) |

## Acceptance criteria

- [ ] Live top 3 matches Activity Monitor's Energy tab order in a manual
      spot check.
- [ ] Chrome and all its helpers appear as one `Google Chrome` row (fixture).
- [ ] Accumulated shares sum to 100 ± 1 %; battery-cost column sums to at most
      the percent consumed.
- [ ] `-n 25 --json` returns ≤ 25 rows, ordered energy desc then name asc.
- [ ] The parser has golden-file tests for macOS 12, 14 and 15 `top` output.
