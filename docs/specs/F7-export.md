# F7 — `batlog export`

**Story:** As a user who wants my own charts, I want the raw data out as CSV or
JSON, so I am never locked into batlog's views.

## CLI contract

```
batlog export <samples|apps|health> [--since <dur|date>] [--csv|--json] [-o <file>]
```

Default `--csv` to stdout. `--since` defaults to everything still in raw form
(90 days); `health` has no limit.

## Behaviour

1. Select the table. Rows are ordered by timestamp ascending.
2. CSV: header row, RFC 4180 quoting, timestamps as ISO 8601 in local time
   with offset (`2026-09-26T14:05:00+07:00`) — spreadsheets understand these.
   JSON: an array of objects with the same field names and unix `ts`.
3. Stream rows; never load the whole table into memory.

## Columns

```
samples   ts, pct, on_ac, charging, watts, raw_cur_mah, raw_max_mah
apps      ts, app, is_system, cpu_j, gpu_j, ane_j            -- one row per app per 15-min bucket
health    day, cycles, raw_max_mah, nominal_mah, design_mah, health_pct, temp_c, condition
```

Rolled-up days (older than 90 days) are not exported by `samples`/`apps`; a
note on stderr says how many days were rolled up and that `health` is
complete.

## Edge cases

| Case | Behaviour |
|---|---|
| No daemon data | Exit 1: `nothing to export — run 'batlog daemon install'` |
| Empty range | Header only (CSV) / `[]` (JSON), exit 0 |
| `-o` file exists | Exit 1 unless `--force` |
| App name contains a comma or quote | Quoted per RFC 4180 |

## Acceptance criteria

- [ ] `export samples --csv` opens in Numbers/Excel with correct dates.
- [ ] `export apps --json | jq length` equals the row count in the range.
- [ ] Exporting 90 days (~130 k rows) stays under 50 MB memory.
