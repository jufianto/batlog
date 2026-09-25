# Architecture decision records

One decision per file, under one page. ADRs are **append-only**: to change a
decision, write a new ADR that supersedes the old one and link both ways.
Easily reversible choices do not get an ADR — pick one and move on.

| # | Decision | Status |
|---|---|---|
| [0001](./0001-pure-go-no-cgo.md) | Pure Go, built with `CGO_ENABLED=0` | Accepted |
| [0002](./0002-battery-via-ioreg-plist.md) | Battery data comes from `ioreg` plist output, not the IOKit API | Accepted |
| [0003](./0003-app-energy-from-top-without-sudo.md) | Per-app energy comes from `top -o power`, shown as a relative share | Accepted |
| [0004](./0004-daemon-is-a-per-user-launchagent.md) | The recorder is a per-user LaunchAgent that owns its own history | Accepted |
| [0005](./0005-data-in-application-support.md) | Data lives in `~/Library/Application Support/batlog/` | Accepted |
