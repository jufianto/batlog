# F6 — `batlog report`

**Story:** As a user, I want a digest of my battery life, worst apps, health
trend and charging habits — conclusions, not raw data.

## CLI contract

```
batlog report [--daily] [--weekly] [--since <dur>] [--json]
```

Default `--daily` (today). Pull only: no scheduled delivery or notifications
in v1 (see the PRD's out-of-scope list).

## Behaviour

Four sections, all computed at read time from existing tables. Each section
degrades on its own; one missing source never blanks the report.

**Ranges:** `--daily` is local midnight → now, `--weekly` the last seven
days as `history --week` (midnight six days ago → now), `--since` as F3.
Only one of them (exit 2).

**1. Battery life** — time on battery / AC / asleep; longest session (most
awake minutes); the session list from F3. The list shows every session that
overlaps the range; the longest, the worst, effective life and the
completed-session count use only sessions that **started** in it, so
consecutive reports never judge the same session twice. Weekly and `--since` add
*effective battery life*: the mean of completed sessions that **started in
the range** at ≥ 80 % and ran ≥ 30 awake minutes, each normalised to a full
100→0 run at its own drain rate (100 ÷ drain hours). Weekly adds the change
versus the seven days before. The 30-minute floor exists because percent is
a whole number: a 10-minute session's rate is mostly rounding.

**2. Drain** — average drain is the percent used per awake hour on battery
over the range (F3 totals; none under 5 minutes on battery). The worst is
the session with the highest drain among those with ≥ 30 awake minutes, the
ongoing one included; its top app is the first non-system app (F4) in that
session's window.

**3. Top apps** — the F4 accumulated table, N = 5, with battery cost and
F4's notes.

**4. Habits and health**
- Median percent at plug-in and at unplug (the range's plug and unplug
  events; the mean of the middle two for an even count).
- Share of **awake** time above 90 % and below 20 % (a sample's percent holds
  until the next sample); none with under 30 awake minutes, so a report run
  just after midnight raises no flag from three minutes of data.
- Longest continuous stretch at 100 % on AC. Unlike every other duration,
  this one runs through sleep between two samples that are both at 100 % on
  AC: a full battery left on the charger overnight is the habit it measures.
  A recorder restart in the gap breaks it, because nothing is known then.
- Rule-based flags, exact thresholds:
  - time above 90 % > 50 % → `battery spends X% of time above 90% — consider Optimized Charging or unplugging earlier`
  - median plug-in < 15 % → `you often run very low (median X%) — deep discharges add wear`
  - > 4 h/day pinned at 100 % on AC → `sat at 100% for Xh/day` (all
    stretches at 100 % on AC ÷ the days of the range: 1 daily, 7 weekly
    (also across a DST change), ⌈hours ÷ 24⌉ for `--since`)
  - A flag fires only *above* its threshold: exactly 50 %, 15 % or 4 h/day
    does not.
- Health line from F2: raw health % of the newest `health` row and the
  90-day trend if available.

## Output

Human, `--weekly`, abridged:
```
📊 batlog report · 20 Sep – 26 Sep
── battery life ──────────────────────────────────
on battery 31h 40m · on AC 23h 12m · asleep 100h 52m
longest session 6h 17m (0922-0926)
  0922-0926   Tue 22 Sep 09:26 → Tue 22 Sep 23:21   6h 17m awake   100% → 22%   11.9 %/hr
  …
est. full-charge life 5h 18m (▼ 22m vs last week)
── drain ─────────────────────────────────────────
avg 14.2 %/hr · worst 24.1 %/hr (0924-1405, Thu 24 Sep 14:05) — top app Docker (41%)
── top apps ──────────────────────────────────────
 #   APP             SHARE   BATTERY COST
 1   Google Chrome   32%     ≈ 58% of battery
 …
shares are of app energy (kernel counters); battery cost is an estimate
── habits & health ───────────────────────────────
you typically plug in at 23% and unplug at 100%
above 90% 80% of the time · below 20% 2% · longest at 100% on AC 9h 10m
⚠ battery spends 80% of time above 90% — consider Optimized Charging or unplugging earlier
health 86.8% (−0.37 %/month)
```

JSON (one object; a value that cannot be computed is `null`):

```json
{
  "range": {"from": 1790355600, "to": 1790398740},
  "kind": "weekly",
  "battery": {
    "battery_min": 1900, "ac_min": 1392, "sleep_min": 6052, "gap_min": 0,
    "longest_session": "0922-0926",
    "sessions": [ /* F3 session objects */ ],
    "effective_life": {"minutes": 318, "sessions": 6},
    "effective_life_change_min": -22
  },
  "drain": {
    "avg_pct_per_hr": 14.2,
    "worst": {"session_id": "0924-1405", "drain_pct_per_hr": 24.1,
              "top_app": {"app": "Docker", "share": 0.41}}
  },
  "top_apps": [ /* F4 row objects, at most 5 */ ],
  "energy_since": null,
  "habits": {
    "completed_sessions": 9,
    "plug_in_median_pct": 23, "unplug_median_pct": 100,
    "time_above_90_share": 0.8, "time_below_20_share": 0.02,
    "max_minutes_at_100_on_ac": 550,
    "flags": [{"id": "above_90", "message": "battery spends 80% of time above 90% — …"}]
  },
  "health": {"health_pct": 86.8, "day": "2026-09-26",
             "trend": { /* F2 trend object, or null */ }}
}
```

- `kind` is `daily`, `weekly` or `since`.
- `effective_life` is `null` on a daily report and with fewer than 2
  qualifying sessions; `effective_life_change_min` is `null` without a
  previous week to compare.
- `energy_since` is F4's: when app energy recording began, if inside the range.
- Flag IDs are stable: `above_90`, `runs_low`, `pinned_100`.
- `health` is `null` when the `health` table is empty.

## Edge cases

| Case | Behaviour |
|---|---|
| < 2 completed sessions in range | Skip medians with `charging habits: not enough sessions yet (N)`, and effective life with `not enough sessions yet (N; needs 2 that …)`, N being the qualifying sessions; render the rest |
| No app energy recorded | Top apps says `no app energy recorded yet`; the worst session has no top app |
| No previous week | Omit the delta |
| No daemon data at all | Exit 1: `report needs daemon history — run 'batlog daemon install'` |
| `health` table empty | Omit the health line |

## Acceptance criteria

- [x] Every flag fires exactly at its threshold (table-driven boundary tests).
- [x] Sections degrade independently on fixtures with one table empty.
- [x] Weekly effective-battery-life matches a manual calculation on seeded
      sessions.
- [x] `--json` schema is stable and documented here.
