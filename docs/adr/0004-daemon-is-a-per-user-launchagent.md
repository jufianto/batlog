# ADR-0004: The recorder is a per-user LaunchAgent that owns its own history
Date: 2026-09-26
Status: accepted

## Context
Accumulated history only exists if something records it continuously. macOS
keeps `pmset -g log` (plug/unplug events, truncated after days) and nothing at
all for per-app energy. The recorder must survive reboots, cost almost
nothing, and need no privileges.

## Options considered
- **LaunchDaemon (root, system-wide):** starts before login, but needs sudo to
  install and runs as root — against the no-sudo principle.
- **LaunchAgent (per user):** starts at login, runs as the user, installs by
  writing one plist into `~/Library/LaunchAgents/`. Cannot record while
  nobody is logged in, which is exactly when there is nothing to record.
- **No daemon, derive from `pmset -g log`:** free, but no per-app data and no
  history older than the log's rotation.

## Decision
`batlog daemon install` writes `~/Library/LaunchAgents/dev.jufi.batlog.plist`
(`RunAtLoad`, `KeepAlive`, `ProcessType=Background`) and loads it with
`launchctl bootstrap gui/$UID`. The agent runs `batlog daemon run`, which
samples every 60 s into SQLite ([ADR-0005](./0005-data-in-application-support.md)).
`pmset -g log` is only a read-time fallback for dates before installation, and
its rows are always labelled as such.

## Consequences
+ One-command install, no password, `launchd` restarts it after a crash,
  uninstall is `bootout` + delete the plist.
+ The database is the single source of truth; every other command is a read.
- If the binary moves (Homebrew upgrade), the plist points nowhere. `install`
  is idempotent so re-running it fixes this, and `daemon status` reports
  "loaded but not running" with that hint.
- History starts on install day. Every day the daemon is not running is data
  we never get, which is why the daemon is built third, not last.
