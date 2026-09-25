# Design — F1 `batlog status` and the Go skeleton

Date: 2026-09-26 · Status: approved in conversation

Implements [docs/specs/F1-status.md](../../specs/F1-status.md) in full (live
fields and the daemon-derived half) and lays down the module layout every
later command inherits. Behaviour is defined by the spec; this document only
records how the code is shaped.

## Layout

Same shape as caddyku and serverku. Module `github.com/jufianto/batlog`,
`go 1.27`, always built with `CGO_ENABLED=0` ([ADR-0001](../../adr/0001-pure-go-no-cgo.md)).

```
main.go                        calls cmd.Execute()
cmd/root.go                    cobra root: batlog, --version, --json persistent flag
cmd/status.go                  F1: probe + store + compute, then render
internal/battery/              ioreg probe. Parse([]byte) is pure; Read(ctx) shells out
internal/battery/testdata/     ioreg -a dumps per Mac model, Serial keys stripped
internal/store/                SQLite via modernc.org/sqlite, embedded migrations, typed readers
internal/status/               drain-rate fit, estimate, worst offender — pure functions
internal/paths/                BATLOG_HOME, Application Support and Logs paths (ADR-0005)
```

Everything under `internal/` is private on purpose: nothing is a public API
yet. The battery parser is the one candidate for promotion to a top-level
package later, if someone wants to import it.

Probe packages follow one pattern: a pure `Parse` tested with golden files and
a thin `Read` that runs the system command with a 10 s timeout. No generic
prober interface; commands take an injected byte source only where a test
needs it.

## Data flow for `batlog status`

1. `battery.Read` runs `ioreg -rc AppleSmartBattery -a` and `Parse` turns the
   plist into a `Snapshot`. Every key is optional; a missing key leaves the
   field unset ([ADR-0002](../../adr/0002-battery-via-ioreg-plist.md)).
2. If, and only if, the database file already exists, open it read-only and
   load `samples` and `app_energy` rows from the last 10 minutes. `status`
   never creates the database; that is `daemon install`'s job.
3. `status.Build(snapshot, samples, energy, now)` returns a `Report` with
   optional fields (pointers or `ok` booleans, never zero-as-unknown).
4. `cmd/status.go` renders the `Report` as the human block or the JSON schema
   from the spec. A field the report could not compute is omitted (human) or
   `null` (JSON).

## Store

The first migration creates the whole schema from
[docs/specs/README.md](../../specs/README.md) — `samples`, `app_energy`,
`health`, `meta`, `daily_rollup` — so F5 adds no schema change. Migrations are
embedded `.sql` files applied in order against `meta.schema_version`. Open
sets WAL and `busy_timeout`. F1 needs only `Open`, `Migrate`,
`SamplesSince(ts)` and `EnergySince(ts)`; writers arrive with F5.

## Drain math (`internal/status`)

Window = samples with `ts >= now - 10 min`. Keep only the on-battery run that
ends now: drop everything up to the last row with `on_ac = true` or before the
last gap greater than 90 s (sleep), whichever is later, so a fit never spans a
plug-in. If the newest row is itself more than 90 s old, the Mac has just
woken or the daemon stopped, and nothing is usable. Fewer than three rows
left → rate omitted, shown as `collecting…`. Otherwise the rate is the
least-squares slope of `pct` against time in hours (computed from deviations
around the means, so a flat battery is exactly 0), reported as a positive
%/hr while discharging; under 0.05 %/hr either way is flat. Estimate =
`percent / rate` in minutes, only while discharging; over 12 h, or a rate of
0, renders as `> 12h`. Worst offender = the app with the largest
`SUM(energy)` over the same window, with its share of the window total.

## Errors

| Condition | Result |
|---|---|
| No `AppleSmartBattery` entry, or `BatteryInstalled` false | exit 1, `no battery found on this machine` |
| Plist parses but has no capacity keys | exit 1, `cannot read battery (ioreg output not recognised)` |
| `ioreg` missing or times out | exit 1, message names `ioreg` |
| Database exists but fails to open or query | warning on stderr, treated as absent; live fields still print |
| Bad flags | exit 2 (cobra usage error) |

## Tests

- `battery.Parse` against `testdata/Mac16,8-15.7.3.plist` (real, captured
  2026-09-26) and `testdata/intel-synthetic.plist` (mAh capacities, no
  `NominalChargeCapacity`; labelled synthetic until a real dump arrives).
- Table tests for the fit, the sleep-gap cut, the AC filter, the `> 12h` case
  and the worst-offender share.
- Store tests against a temp `BATLOG_HOME`, seeded through SQL.
- One command-level test: fixture bytes + seeded database → `--json` output
  compared field by field, including nulls.
- Nothing in the test suite shells out, so it runs on Linux CI.

## Tooling

`go build`, `go test`, and `.github/workflows/ci.yml` running `go vet` and
`go test ./...` with `CGO_ENABLED=0` on ubuntu and macos. goreleaser and the
Homebrew tap wait for the first release.
