# F5 — `batlog daemon`

**Story:** As a user, I want batlog to quietly record battery and app-energy
data from the moment I log in, so every other command has history to work
with — without me ever thinking about it.

## CLI contract

```
batlog daemon install | uninstall | status | logs [-f] | run [--once]
```

`run` is what launchd executes; it is also usable by hand for debugging.
`--once` does one tick and exits (used by tests).

## Behaviour

**install** ([ADR-0004](../adr/0004-daemon-is-a-per-user-launchagent.md))
1. Resolve the absolute path of the running binary. Write
   `~/Library/LaunchAgents/dev.jufi.batlog.plist` with `RunAtLoad=true`,
   `KeepAlive=true`, `ProcessType=Background`, stdout/stderr →
   `~/Library/Logs/batlog/daemon.log`.
2. `launchctl bootstrap gui/$UID <plist>`. Idempotent: if already loaded,
   `bootout` first (this also fixes a moved binary after an upgrade).
3. Create the data directory ([ADR-0005](../adr/0005-data-in-application-support.md)),
   create the database, run migrations. Print the paths.

**run loop, every 60 s**
1. Battery probe (`ioreg`) → one `samples` row.
2. Energy probe (`top`, grouped, top 15) → `app_energy` rows. Same transaction.
3. Once per calendar day (first tick after midnight or after boot): write a
   `health` row; roll up and prune raw rows older than 90 days.
4. Update `meta.last_tick`. Per-probe timeout 10 s. On any probe or write
   error: log it, skip the tick, continue. Never exit on a data error.

**status:** installed? (plist exists) · loaded? (`launchctl print`) · alive?
(`last_tick` age: healthy < 2 min, stale 2–10 min, dead > 10 min) · database
path and size · sample count · date of oldest sample.

**uninstall:** `bootout` + delete the plist. **Never deletes data**; prints
`data kept at <path> — delete it yourself if you want it gone`.

**logs:** print the log; `-f` follows. On startup, if the log is over 5 MB,
truncate it to the last 1 MB.

## Output

`status`, human:
```
● batlog daemon: running   (last tick 38 s ago)
plist      ~/Library/LaunchAgents/dev.jufi.batlog.plist   (loaded)
database   ~/Library/Application Support/batlog/batlog.db   3.2 MB
samples    41 203 since 02 May
```

## Resource budget (hard requirements)

Under 1 % average CPU, under 30 MB resident memory, database growth under
5 MB a month, one tick under 5 s. batlog must never appear in its own top ten.

## Edge cases

| Case | Behaviour |
|---|---|
| Binary moved after install | `status` shows loaded-but-dead and says `re-run 'batlog daemon install'` |
| Manual `run` while launchd's is running | SQLite `busy_timeout` keeps writes safe; `run` warns if `last_tick` is under 30 s old |
| Disk full | Log each tick, keep trying; read commands still work |
| Mac wakes from sleep | Next tick is normal; the gap is handled at read time (F3) |
| `launchctl bootstrap` fails | Exit 1 with launchctl's own message; plist left in place for inspection |

## Acceptance criteria

- [ ] `install` → reboot → `status` reports running with no user action.
- [ ] 24 h soak: no crash, within budget, ~1 440 samples minus sleep gaps.
- [ ] `uninstall` stops sampling and leaves the database intact.
- [ ] `kill -9` the daemon → launchd restarts it within 10 s; no corruption
      (WAL mode).
- [ ] `run --once` exits 0 and writes exactly one tick.
- [ ] `batlog top --week` after a week does not list batlog in the top ten.
