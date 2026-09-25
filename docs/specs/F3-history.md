# F3 — `batlog history`

**Story:** As a user, I want to see when I first charged today, when I last
went on battery, and how long each battery session lasted — so I can put a
number on "my battery only lasts 5 hours".

## CLI contract

```
batlog history [--today] [--week] [--since <dur|date>] [--events] [--json]
```

Default range `--today`. `--since` accepts `3d`, `12h` or `2026-06-01`.
`--events` prints raw plug/unplug events instead of session summaries.

## Behaviour

1. Load `samples` for the range, plus one sample before it (to know the
   starting state).
2. **Events:** every change of `on_ac` → `{ts, type: plugged|unplugged, pct}`.
3. **Sessions:** unplug → plug is a battery session; plug → unplug is an AC
   session. For each battery session: start, end, awake duration (gaps > 90 s
   excluded), start/end percent, average drain %/hr over awake time only.
4. **Headline answers** for the range:
   - *First charge* = first `plugged` event.
   - *Last on battery* = most recent `unplugged` event.
   - *Battery lasted* = awake duration of the last completed battery session,
     or `ongoing, Xh so far`.
5. **Fallback:** if the range starts before the daemon's first sample, parse
   `Using AC` / `Using Batt` lines from `pmset -g log` for events only (no
   drain maths) and tag every such row `(pmset)`. Never mix the two sources
   silently.

## Output

Human, `--today`:
```
📅 Today, Fri 26 Sep
first charge       07:42  (at 31%)
last on battery    13:05  (at 100%)

battery sessions
  09:12 → 14:05   4h 53m awake   100% → 8%    18.8 %/hr
  15:30 → now     1h 12m so far   95% → 81%   11.6 %/hr   (ongoing)

on battery 6h 05m · on AC 4h 12m · asleep 1h 40m
```

JSON: `{range, first_charge, last_unplug, sessions: [{start, end,
awake_minutes, start_pct, end_pct, drain_pct_per_hr, ongoing, source}],
totals: {battery_min, ac_min, sleep_min}}`.

## Edge cases

| Case | Behaviour |
|---|---|
| Session spans midnight with `--today` | Include it; prefix the start with the date (`yesterday 23:10 →`) |
| Mac slept mid-session | Sleep excluded from duration and drain; shown in totals |
| No events in range | `no charge/discharge events in this range`, exit 0 |
| Range before install and pmset log empty | Same message + `history starts <install date>` |
| Daemon was down for hours | Treat like a sleep gap; mark the session `(data gap)` |

## Acceptance criteria

- [ ] The three headline answers are correct on fixture data that includes a
      midnight-spanning session and an ongoing one.
- [ ] A fixture with an 8 h sleep gap does not inflate battery life.
- [ ] `--events --json` is sorted ascending with a stable schema.
- [ ] pmset-fallback rows carry `source: "pmset"` and are never merged into
      daemon sessions.
