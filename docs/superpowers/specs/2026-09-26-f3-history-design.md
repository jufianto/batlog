# Design — F3 `batlog history`

Date: 2026-09-26 · Status: approved in conversation ("ok lets do")

Implements [docs/specs/F3-history.md](../../specs/F3-history.md). Shared rules
in [docs/README.md](../../README.md).

## Two facts that shape it

- **`pmset -g log` takes ~1.9 s** on a week of log (50 000 lines). Running it
  on every call would break the < 1 s budget of `history --today` (PRD metric
  1). It is therefore used only for the spec's fallback: when the range starts
  before the daemon's first sample.
- **Sleep versus "daemon down" is told apart by recorder starts, not by
  pmset.** A live recorder only misses ticks while the Mac sleeps. So the
  recorder writes its start time to a new `runs` table, and a gap > 90 s that
  contains a start is a *data gap* (daemon down, Mac off, reboot); any other
  gap is *sleep*. Gaps in data recorded before `runs` existed count as sleep.

## Packages

```
internal/store/migrations/0002_runs.sql   runs(started INTEGER PRIMARY KEY)
internal/store/      + RecordRunStart, RunStartsBetween, LastSampleBefore,
                       LastSampleBeforeWithState
internal/recorder/   Run records its start before the first tick
internal/pmset/      Parse(io.Reader) []Reading (ts, on_ac, pct) from every
                     "Using AC|Batt (Charge…)" line, all four spellings;
                     Changes(readings) → events; Read(ctx) runs pmset
internal/history/    pure: Build(Input) Result — events, battery sessions,
                     headline answers, totals
cmd/history.go       flags, range parsing, loading, human and JSON output
```

## Loading

Range: `--today` (default) is local midnight → now; `--week` is local midnight
six days ago → now; `--since` takes `3d`, `12h` or `2026-06-01` (local
midnight). More than one range flag, an unparseable or future `--since`: exit 2.

Samples are loaded from the last state change before the range, so a session
that spans midnight has its real start: take the last sample before `from`,
then the last sample before that with the other `on_ac`, and load everything
from there to now. Only a battery run needs the look-back: if the last
sample before `from` is on AC, loading starts at that sample; if a battery run
has no AC sample before it, loading starts at the first sample ever.

`history` opens the database read-only and never migrates it: a schema-1 file
(before `runs`) has no recorder starts, so all its gaps are sleep until the
recorder's next start upgrades it.

## Rules

- **Intervals** between consecutive samples: ≤ 90 s is awake time in the
  earlier sample's state; longer is sleep or a data gap as above. Totals clip
  intervals to the range; a session's own awake time is not clipped.
- **Events**: each change of `on_ac` between consecutive samples, at the later
  sample's time and percent. Only events inside the range are shown.
- **Battery session**: a run of on-battery samples; ends at the plug event, or
  is ongoing if it is the last run. Awake minutes = its awake intervals; drain
  = percent dropped over awake intervals ÷ awake hours, shown only with at
  least 5 awake minutes. Marked `(data gap)` if a data gap falls inside it, or
  if it is ongoing and the newest sample is more than 10 min old.
- **Headline**: first charge = first plug event in range; last on battery =
  last unplug event in range; battery lasted = the latest session if ongoing
  (`ongoing, Xh so far`), otherwise the last completed one's awake time.
- **pmset fallback** covers only the part of the range before the first daemon
  sample: events from source changes in the log, and battery sessions between
  them (end cut at the first daemon sample), each `source: "pmset"`, with no
  awake time, drain or totals. A pmset session starts only at an unplug: the
  log's first line is where the log begins, not when the Mac went on battery.
  They are never merged with daemon sessions, and "battery lasted" comes from
  daemon sessions only.

## Output

Human as in the spec, plus a `battery lasted` headline line, `· no data Xh`
in the totals when there were data gaps, times prefixed with `yesterday` or
`Thu 24 Sep` when not today (for `--today` that is the range's first day),
durations as `4h 05m` (shared with `status`), and `(pmset)` on fallback
rows. JSON as in the spec plus `battery_lasted {minutes, ongoing}`,
`data_gap` per session and `totals.gap_min`; timestamps are unix seconds;
`--events --json` is `{range, events: [{ts, type, pct, source}]}` ascending.
