# Design — 90-day rollup and prune

Date: 2026-09-26 · Status: approved in conversation ("lets go")

Implements the retention rule in [docs/specs/README.md](../../specs/README.md)
("raw `samples`/`app_energy` older than 90 days are rolled up into
`daily_rollup` … and deleted") and step 3 of the F5 run loop. No schema change:
`daily_rollup` exists since migration 0001.

## What is rolled up

Whole local days. The cutoff is local midnight 90 days before today; every
date from the oldest raw row's up to the cutoff is rolled up in order, one
transaction per day: upsert its `daily_rollup` row, delete its `samples` and
`app_energy` rows. A crash leaves a day either untouched or fully rolled up.

Walking dates (not jumping from one raw row to the next) gives a day with no
raw rows its row too: a Mac asleep all day gets `min_asleep = 1440`. A day
with nothing known about it (inside a data gap, or already rolled up) gets
no row.

"Local midnight" is the first instant of the local date
(`internal/localday`), not `time.Date(y, m, d, 0, 0, 0, 0, loc)`: where DST
skips midnight (Chile, Cuba, the Azores) Go moves the missing 00:00 back into
the day before, which once made the walk loop forever. `history`'s ranges use
the same helper.

Per day:

| column | from |
|---|---|
| `min_battery`, `min_ac`, `min_asleep` | `history.Build` totals for the day, rounded to minutes (data-gap time is in none of them) |
| `pct_consumed` | percent dropped over awake on-battery intervals that start that day |
| `app_energy` | JSON `{app: sum of energy}`; NULL when the day has no rows |

A day that already has a row (raw rows for it reappeared, e.g. after a clock
change) is merged: minutes and percent added, app sums added.

## Day boundaries

`history.Build` needs the sample before the day (a sleep that began the night
before) and the one after it (a sleep that runs past midnight). The one after
is still raw. The one before was deleted with the previous day, so each
rollup keeps the last sample it deleted in `meta.rollup_carry`
(`ts,pct,on_ac`) and uses it as the next day's lead-in. `runs` is not pruned
(one row per daemon start), so data gaps across the boundary still classify.

## When

The recorder runs it on the first tick of each local day, and the first after
it starts, right after the health row. An error is logged
(`rollup failed: …`) and never skips the tick; a success that rolled up days
logs `rolled up N days (… → …)`.

## Not in scope

`history` and `export` read raw rows only, so they see 90 days. `report` (F6)
may read `daily_rollup` later.
