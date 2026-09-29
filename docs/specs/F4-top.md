# F4 — `batlog top`

**Story:** As a user, I want to know which apps use the most energy, right
now *and* accumulated over a day, a week or one battery session, so I know
what to quit or replace, and why a charge drained fast. The accumulated view
is the reason batlog exists.

## CLI contract

```
batlog top [--live] [--today] [--week] [--since <dur|date>] [--session <id|last>] [-n N] [--json]
```

- **Default:** `--today` if the daemon has recorded app energy, else
  `--live`.
- **`-n`:** default 10.
- **Range flags** are the same as `history` (F3), and the same
  one-range-only rule applies (exit 2).
- **`--session`** takes a session ID from `batlog history` (e.g. `0926-1656`)
  or `last`.

## Data

The source is per-app energy from the kernel's coalition counters
([ADR-0006](../adr/0006-app-energy-from-kernel-coalition-counters.md)). A
coalition is the responsible-app group Activity Monitor uses. It includes the
app's helpers, its short-lived children and energy the app spent through
processes that have since exited.

- **The daemon (F5)** stores each app's CPU, GPU and Neural Engine energy in
  15-minute buckets (`app_energy`).
- **Live mode** reads the counters twice, 1 s apart, and needs no daemon.

Apps without an app bundle that run from system paths (`WindowServer`,
`kernel_task`, `mds_stores`, …) are tagged `system` and shown with ⚙.
Activity Monitor hides them. batlog shows them because they are real drain,
but never names one as the worst offender.

## Behaviour

**Share** is an app's energy ÷ all apps' energy in the range.

- A bucket that lies partly outside the range counts in proportion to how
  much of it lies inside.
- The proportion comes from the minute samples, or from time when the bucket
  has none.

**Battery cost** is the app's share of *on-battery* energy × the percent
consumed while awake on battery in the range. The percent comes from F3's
awake intervals.

- A bucket's on-battery part is its fraction of in-range samples that were on
  battery.
- Cost is rendered as `≈ 31% of battery`, or `—` when the range had no
  battery time.
- Shares sum to 100 %, and battery costs sum to the percent consumed.

**`--session <id>`**:
- The range is that battery session (F3): from unplug to plug-in, or to now.
- The header repeats the session line.
- It adds the session's heaviest 30 awake minutes by average watts, from
  `samples`.

A session ID is its start in local time as `MMDD-HHMM`. The date is the most
recent one not in the future; a second session starting in the same minute
gets `b`, `c`, ….

## Output

Human, `--today`:
```
⚡ top energy · today   (9h 17m awake, 6h 05m on battery, 64% used)
 #  APP                SHARE   BATTERY COST
 1  Google Chrome       34%    ≈ 31% of battery
 2  OrbStack            18%    ≈ 12%
 3  WindowServer ⚙      11%    ≈ 7%
 …
shares are of app energy (kernel counters); battery cost is an estimate
```

Human, `--session 0926-1656`:
```
⚡ session 0926-1656 · Sat 26 Sep 16:56 → Sun 27 Sep 01:40 · 2h 14m awake · 100% → 20% (34.9 %/hr)
 #  APP                SHARE   BATTERY COST
 1  Brave Browser       49%    ≈ 39% of battery
 2  WindowServer ⚙      15%    ≈ 12%
 …
heaviest 30 min: Sun 00:40 → 01:10 · 28.4 W average
```

Human, `--live`: `⚡ top energy · live (1 s)`, with SHARE only; no battery
column.

JSON:
- `{range, awake_min, battery_min, pct_used, rows: [{app, share, est_battery_pct, is_system, energy_j}]}`
- `--session` adds `session: {id, start, end, ongoing, heaviest: {start, end, avg_watts}}`.
- `--live` has `rows` with `share`, `is_system` and `energy_j` only.
- `share` is 0–1. `est_battery_pct` is `null` without battery time. Rows are
  ordered by share desc, then name asc.

## Edge cases

| Case | Behaviour |
|---|---|
| No app energy recorded for the range | `no app energy recorded for this range` + when recording started, exit 0 |
| `--today` but no daemon data at all | Fall back to `--live` with a notice |
| `--session` ID not found | Exit 1: `no battery session <id> in the last 90 days — see batlog history` |
| `--session` of a `(pmset)` session, or before energy recording began | The session header + `no app energy for this session (recording started <date>)` |
| App ran only while on AC | In the share, with battery cost `0%` |
| < 30 min awake in range | Show the table + `short window — shares may be noisy` |
| Counters look implausible on some macOS | The daemon logs it and skips energy for that tick (F5); `top` shows what was recorded |

## Acceptance criteria

- [ ] Over a day, the top 3 non-system apps match Activity Monitor's *12 hr
      Power* order in 4 of 5 spot checks (PRD metric 2).
- [ ] All of Chrome's helpers and children appear as one `Google Chrome` row
      (fixture of coalition members).
- [ ] Shares sum to 100 ± 1 %; battery costs sum to the percent consumed ± 1.
- [ ] `--session <id>` from `batlog history` round-trips, including a session
      that crosses midnight and a second session in the same minute.
- [ ] `-n 25 --json` returns ≤ 25 rows, ordered share desc then name asc.
- [ ] batlog's own daemon never appears in its own top 10 over a day (PRD metric 3).
