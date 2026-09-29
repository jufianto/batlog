# Design — F4 `batlog top` and session IDs

Date: 2026-09-29 · Status: approved in conversation ("ok good", "ok, lets do all the steps")

Implements [F4](../../specs/F4-top.md) on the buckets from the
[F5 energy design](./2026-09-29-f5-energy-design.md), and adds session IDs
to F3.

## Session IDs (`internal/history`)

`history.Build` gives each session an `ID`: its start in local time as
`MMDD-HHMM`. A later session in the same minute of the same build gets `b`,
`c`, …. `history` prints the ID as the first column and as `id` in the JSON.

`top` needs to resolve an ID, so `history.ParseID(id, now)` finds the start
minute:
- **Date:** the year that makes `MMDD` most recent and not in the future.
- **Lookup:** build the local day that holds that minute, extended to now
  (a session can run for days), then pick the session whose ID matches.
- **`last`:** the newest session in the last 90 days.

## Range energy (`internal/top`, pure)

Inputs are the buckets overlapping `[from, to)`, the samples in that range
and F3's totals.

**Bucket weight.** For each bucket `[b, b+900)`, `w` is the share of the
bucket's samples that fall inside the range. Samples exist only while awake,
so this apportions by awake minutes. A bucket with no samples falls back to
time overlap ÷ 900.

**On-battery part.** `wb` is the share of the bucket's in-range samples that
were on battery.

For each app:
- **Share** is `Σ w·E` ÷ the same over all apps.
- **Battery cost** is `(Σ wb·E ÷ Σ over all apps) × pct_used`, where
  `pct_used` is F3's `Totals.PctUsed` for the range. With no battery time
  the cost is `nil`.

Rows are sorted by share desc, then name asc, and cut to `-n`. Each row's
`energy_j` is `Σ w·E / 1e9`.

**Heaviest 30 min** (sessions only) is the 30-minute window of consecutive
awake samples with the highest mean `watts`. It is found with a sliding sum
over the session's samples, and sleep gaps break windows.

## Live mode

Live mode calls `energy.Read` twice, 1 s apart, through the same `Tracker`.
It is used on non-darwin only as `ErrUnsupported`, which exits 1 with the
message.

## CLI (`cmd/top.go`)

The range flags are parsed by the same parser as `history`, moved to a
shared helper. `--session` is exclusive with the range flags (exit 2). The
default is `--today` when `app_energy` has any row, else `--live` with a
notice. The database is opened read-only, as `history` does.

## Tests

- Pure weight and share math: a partial bucket at each end, a bucket with
  no samples, an AC-only app costing `0%`, and the sums-to-100 and
  sums-to-`pct_used` invariants.
- Session ID generation: a suffix in the same minute, a session crossing
  midnight, and the year boundary in `ParseID` (`1231-2350` read on 1 Jan).
- cmd golden tests for `--today`, `--session`, not found (exit 1), the
  exclusive flags (exit 2), `--json` and `-n`.
