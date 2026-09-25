# ADR-0005: Data lives in `~/Library/Application Support/batlog/`
Date: 2026-09-26
Status: accepted

## Context
The daemon writes a database and a log; users must be able to find, back up
and delete them. The first draft used `~/.batlog/`, the Unix habit. macOS has
its own convention, and both tools we compared against follow it.

## Options considered
- **`~/.batlog/`:** short to type; invisible in Finder; not where macOS
  backup and privacy tooling expects app data.
- **`~/Library/Application Support/batlog/`:** the macOS convention for user
  data; Time Machine backs it up; `~/Library/Logs/batlog/` is where Console.app
  looks for logs.
- **XDG (`~/.local/share/batlog`):** a Linux convention with no meaning on
  macOS.

## Decision
- Database: `~/Library/Application Support/batlog/batlog.db` (SQLite, WAL).
- Daemon log: `~/Library/Logs/batlog/daemon.log`.
- Override with `BATLOG_HOME=<dir>` for tests and for people who insist.
`batlog daemon status` prints both paths. `uninstall` never deletes them; it
prints the paths and lets the user decide.

## Consequences
+ Behaves like a native Mac tool; backed up by default; easy to find.
- Longer path to type. Every command that mentions the path prints it in
  full, so nobody has to remember it.
