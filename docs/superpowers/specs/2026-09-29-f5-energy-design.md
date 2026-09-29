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

**Size.** On a developer's Mac, measured 2026-09-29:
- about 400 coalitions exist, and about 200 apps use *some* energy every
  minute;
- only about 20 use 0.1 J or more in a minute, and the rest together are
  about 1 % of all energy.

The recorder therefore folds each tick's deltas under 0.1 J into one
`(other)` row, tagged system (`energy.Fold`). A bucket then holds tens of
apps rather than about 290. That is a few thousand rows a day, or 10–20 MB
over the 90 days kept raw, instead of about 70 MB.

Every UTC offset in use
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
`gpu_energy_nj` 41; 40 is `phys_footprint`, in bytes). `energy.FromUsage`
parses a reply and is tested against a real one in `testdata/`.

The kernel copies `min(struct, buffer)` and writes the size back unchanged,
so a macOS that moved these fields cannot be detected from the reply. It is
caught only by the Tracker's sanity check, and by the live test on the
macOS CI runner.

**Naming** (pure; it started as the spike's `leaderName`, then was fixed in
review). Each member counts toward its outermost `.app` bundle. A bundle
inside a `.framework/` does not count: Homebrew's
`Python.framework/…/Python.app` once named T3 Code's coalition "Python".

The best bundle is chosen in this order:
1. one installed in an Applications folder (`/Applications`,
   `~/Applications`, `/System/Applications`, Safari's cryptex), so a
   Playwright Chromium in a cache never names the agent that runs it;
2. then one whose own main executable (`X.app/Contents/MacOS/Y`) is a
   member;
3. then the one with the most members, then the one with the lowest pid.

Without any bundle, the name is the lowest pid's executable name, or its
command when the path is unreadable.

**`is_system`** is true for:
- bundles under `/System/Library/` or `/Library/Apple/` (Dock,
  NotificationCenter, ControlCenter), except `Finder`;
- coalitions without a bundle whose readable paths are all under
  `/System/`, `/usr/` (but not `/usr/local/`), `/bin/`, `/sbin/` or
  `/Library/Apple/`, or that have no readable path (root-owned).

`WindowServer` and `kernel_task` are therefore system. `Finder`,
System Settings, Safari and batlog's own daemon are not.

In `apps`, an app once seen as non-system stays non-system. Two coalitions
can share a name, like Homebrew's `python3` and `/usr/bin/python3`, and the
one flag per name applies to every past bucket.

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
| Coalition missing from a read | Kept for a day of reads (1440), so one that regains a member later is not counted from zero; its energy between the last read and its end is lost (at most one minute) |

Deltas for the same name are summed, which covers two coalitions of one app.
The sum is taken in float64, so garbage fields cannot wrap below the limit.

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
