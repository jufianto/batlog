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

**1. Battery life** — time on battery / AC / asleep; longest session; the
session list from F3. Weekly adds *effective battery life*: the mean of
completed sessions that started at ≥ 80 %, each normalised to a full 100→0
run at its own drain rate, with the change versus the previous week.

**2. Drain** — average and worst drain %/hr across sessions; for the worst
session, its top app from `app_energy` within that window.

**3. Top apps** — the F4 accumulated table, N = 5, with battery cost.

**4. Habits and health**
- Median percent at plug-in and at unplug.
- Share of time above 90 % and below 20 %; longest continuous stretch at
  100 % on AC.
- Rule-based flags, exact thresholds:
  - time above 90 % > 50 % → `battery spends X% of time above 90% — consider Optimized Charging or unplugging earlier`
  - median plug-in < 15 % → `you often run very low (median X%) — deep discharges add wear`
  - > 4 h/day pinned at 100 % on AC → `sat at 100% for Xh/day`
- Health line from F2: raw health % and the trend if available.

## Output

Human, `--weekly`, abridged:
```
📊 batlog report · 20–26 Sep
── battery life ──────────────────────────────────
on battery 31h 40m · est. full-charge life 5h 18m (▼ 22m vs last week)
── drain ─────────────────────────────────────────
avg 14.2 %/hr · worst: Tue 24.1 %/hr — top app Docker (41%)
── top apps ──────────────────────────────────────
1. Google Chrome 32% (≈ 2h 06m of battery)  …
── habits & health ───────────────────────────────
you typically plug in at 23% · 80% of time above 90% ⚠
health 86.8% (−0.37 %/month)
```

JSON: `{range, battery: {…}, drain: {…}, top_apps: […], habits:
{plug_in_median_pct, unplug_median_pct, time_above_90_share,
time_below_20_share, max_minutes_at_100_on_ac, flags: […]}, health: {…}}`.

## Edge cases

| Case | Behaviour |
|---|---|
| < 2 completed sessions in range | Skip medians and weekly life with `not enough sessions yet (N)`; render the rest |
| No previous week | Omit the delta |
| No daemon data at all | Exit 1: `report needs daemon history — run 'batlog daemon install'` |
| `health` table empty | Omit the health line |

## Acceptance criteria

- [ ] Every flag fires exactly at its threshold (table-driven boundary tests).
- [ ] Sections degrade independently on fixtures with one table empty.
- [ ] Weekly effective-battery-life matches a manual calculation on seeded
      sessions.
- [ ] `--json` schema is stable and documented here.
