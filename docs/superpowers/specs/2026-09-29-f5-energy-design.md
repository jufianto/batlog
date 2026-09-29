# Design — F5 energy half: coalition counters into 15-minute buckets

Date: 2026-09-29 · Status: approved in conversation ("ok, lets do all the steps")

Implements step 2 of the F5 run loop with the source chosen in
[ADR-0006](../../adr/0006-app-energy-from-kernel-coalition-counters.md). The
spike it builds on is `scripts/spike/energy/`.

## Schema (migration 0003)

The `app_energy` table from 0001 was never written, so 0003 drops it and
creates two new tables:

```sql
CREATE TABLE apps (
    id        INTEGER PRIMARY KEY,
    name      TEXT    NOT NULL UNIQUE,
    is_system INTEGER NOT NULL DEFAULT 0
);
-- One row per app per 15-minute bucket; ts = bucket start (unix, multiple of 900).
CREATE TABLE app_energy (
    ts     INTEGER NOT NULL,
    app_id INTEGER NOT NULL,
    cpu_nj INTEGER NOT NULL DEFAULT 0,
    gpu_nj INTEGER NOT NULL DEFAULT 0,
    ane_nj INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (ts, app_id)
) WITHOUT ROWID;
```

Size: about 40 active apps × 96 buckets comes to roughly 4 k rows a day at
~30 bytes each, or 3–4 MB for the 90 days kept raw. Every UTC offset in use
is a multiple of 15 minutes, so a bucket that starts on a multiple of 900 s
also starts on a local quarter hour.

## `internal/energy`

**Probe** (`probe_darwin.go`, with a stub on other systems that returns
`ErrUnsupported`):

```go
type Reading struct {
    Coalition     uint64
    CPU, GPU, ANE uint64 // cumulative nJ
    Members       []Member // filled only for coalitions the caller has not named
}
func Read(named func(coalition uint64) bool) ([]Reading, error)
```

Each read:
1. lists processes with `kern.proc.all`;
2. reads each process's coalition id;
3. reads each coalition's resource usage.

It reads executable paths only for coalitions not yet named, so a steady
minute costs about one syscall per process plus one per coalition. Field
indexes are those of the spike (`energy` 11, `ane_energy_nj` 39,
`gpu_energy_nj` 41). A reply shorter than 42 fields is an error.

**Naming** (pure, from the spike's `leaderName`):
- The name is the bundle of the member that is an app's main executable
  (`X.app/Contents/MacOS/Y`, outermost bundle), lowest pid first.
- Else it is the most common outermost bundle among the members.
- Else it is the lowest pid's command name.

**`is_system`** is true when:
- no member lives in a `.app` bundle,
- and every readable path is under `/System/`, `/usr/`, `/bin/`, `/sbin/`
  or `/Library/Apple/`, or no path is readable (root-owned).

`WindowServer` and `kernel_task` are therefore system. `Finder` and
batlog's own daemon are not.

**Tracker** (pure; it holds the previous reading per coalition):

```go
func (t *Tracker) Update(now time.Time, rs []Reading) ([]Delta, error) // Delta{App string; System bool; CPU, GPU, ANE uint64}
```

| Case | Result |
|---|---|
| First read after start | Baseline; no deltas |
| Coalition first seen later | Counted from zero: it was created since the previous read |
| A counter decreased | That coalition is re-baselined and skipped, with one log line per process lifetime |
| All deltas together exceed 200 W over the elapsed time | The whole read is dropped with a logged warning; the baseline moves to it |
| Coalition gone | Forgotten; its energy after the previous read is lost (at most one minute) |

Deltas for the same name are summed, which covers two coalitions of one app.

## Recorder and store

On each tick, after the battery read succeeds, `Recorder.Energy` (injectable)
reads, updates the tracker and hands the deltas to `WriteTick`. In the same
transaction as the sample:
- apps are upserted by name,
- each delta is added to the `(bucket(now), app_id)` row with
  `INSERT … ON CONFLICT DO UPDATE SET cpu_nj = cpu_nj + excluded.cpu_nj, …`.

App ids are not cached. About 40 upserts a minute is nothing for SQLite,
and without a cache there is nothing to go stale.

Failure rules:
- A failed battery read skips the whole tick, as before. The tracker is not
  touched, so the next delta covers both minutes.
- An energy error or `ErrUnsupported` writes the sample without energy. The
  error is logged once until the next success.

Other readers:
- **`status`** (F1) takes its worst offender from the current and previous
  buckets, non-system apps only.
- **`rollup`** sums each app's `cpu_nj + gpu_nj + ane_nj` for the day into
  `daily_rollup.app_energy` as JSON `{app: joules}`.
- **`OldestRawTS`** keeps reading `app_energy.ts`.

## Tests

- A tracker table test for every rule above.
- Naming and system classification from fixtures of member paths: Chrome
  helpers, Electron apps, T3 Code's children, WindowServer, `(comm)`.
- Store tests: bucket upsert adds; the migration drops the old table on a
  schema-2 database.
- The recorder test checks that an energy error keeps the sample.
- A darwin-only live test runs `Read` twice 2 s apart after the test itself
  burns CPU. It checks that at least one coalition rose, that no counter
  fell, and that the test's own coalition is found.
