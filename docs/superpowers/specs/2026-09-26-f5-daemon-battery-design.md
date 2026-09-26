# Design — F5 `batlog daemon`, battery half

Date: 2026-09-26 · Status: approved in conversation ("lets go")

Implements [docs/specs/F5-daemon.md](../../specs/F5-daemon.md) except the
`top` energy probe, which waits for the spike verdict on
[ADR-0003](../../adr/0003-app-energy-from-top-without-sudo.md). Decisions come
from [ADR-0004](../../adr/0004-daemon-is-a-per-user-launchagent.md) and
[ADR-0005](../../adr/0005-data-in-application-support.md).

## Scope

In: `install`, `uninstall`, `status [--json]`, `logs [-f]`, `run [--once]`; a
60 s loop writing one `samples` row per tick, one `health` row per calendar
day, and `meta.last_tick`; log truncation at startup.

Deferred, with reasons:
- **`app_energy` rows** — spike verdict pending (~2026-09-28).
- **90-day rollup and prune** — needs the session logic F3 builds, and no raw
  row can be 90 days old before 2026-12-25. Built after F3: see
  [the rollup design](2026-09-26-rollup-design.md).

## Packages

```
internal/launchd/    Plist(bin, log, env) bytes; Client{UID, Run} with
                     Loaded / Bootstrap / Bootout over `launchctl`
internal/recorder/   Recorder{DB, Read, Now, Log}: Tick(ctx), Run(ctx, every);
                     TruncateLog(path, max, keep)
internal/store/      + WriteTick (one transaction), Meta, SampleStats
internal/paths/      + LaunchAgent(), LogDir()
cmd/daemon.go        the five subcommands; launchd client, executable path
                     and clock injectable for tests
```

`launchctl` is behind a `Run func(ctx context.Context, args ...string) ([]byte, error)` so tests
check the exact arguments without touching launchd.

## Behaviour notes

- **Plist:** label `dev.jufi.batlog`, `ProgramArguments = [<abs bin>, daemon,
  run]`, `RunAtLoad`, `KeepAlive`, `ProcessType=Background`, stdout and
  stderr to the daemon log. `BATLOG_HOME` is passed through when set at
  install time, so the daemon writes where the installer read.
- **install:** use `os.Executable` as invoked, not resolved through symlinks,
  so the plist names Homebrew's stable link rather than the versioned Cellar
  path `brew upgrade` deletes; refuse a binary that resolves into a `go run`
  build directory. Create data and log directories, create and migrate the
  database, write the plist, `bootout` if loaded, `bootstrap` with up to three
  retries one second apart (launchd often reports I/O error while a previous
  instance is still tearing down). A final failure exits 1 with launchctl's
  message and leaves the plist for inspection.
- **tick:** read the battery with a 10 s timeout; write the sample, the health
  row when the calendar day differs from the last one written by this process
  (so first tick after start or midnight; upsert), and `meta.last_tick`, all
  in one transaction. Any error is logged and the tick skipped; the loop never
  exits on a data error. SIGTERM/SIGINT end it cleanly.
- **health.day** is the local date `YYYY-MM-DD`; `condition` is `Normal`,
  `Service recommended` or NULL.
- **status:** installed = plist exists; loaded = `launchctl print` succeeds;
  alive from `last_tick` age: running < 2 min, stale 2–10 min, dead > 10 min.
  If the plist's binary no longer exists: `re-run 'batlog daemon install'`.
  Paths print with `~`. `--json` gives the same facts.
- **run:** warns when `last_tick` is under 30 s old (another recorder is
  running). At startup a log over 5 MB is cut to its last 1 MB **in place**,
  because launchd keeps the file open in append mode.
- **uninstall:** `bootout` if loaded, delete the plist, never touch data; print
  `data kept at <path> — delete it yourself if you want it gone`.

## Tests

Nothing shells out. Fake `launchctl` runner records arguments and scripts
failures; temp `HOME` for LaunchAgents and data; `Recorder` driven with a fake
battery reader and clock; the loop tested with a 10 ms interval and a
cancelled context. The real install is exercised once by hand at the end.
