# F3 — `batlog history`

**Story:** As a user, I want to see when I first charged today, when I last
went on battery, and how long each battery session lasted — so I can put a
number on "my battery only lasts 5 hours". And for each charge: how long it
took to fill, and how long the Mac then sat plugged in at 100 %.

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
4. **Charging sessions:** each run on AC, from the plug event to the
   unplug, built from batlog samples only (pmset has no charging detail):
   - *Time to full* is from the plug-in to the first sample at 100 %, in
     wall-clock time: a Mac charges while asleep. When that sample is the
     first after a sleep that began below 100 %, the battery may have been
     full for a while, so the time is an upper bound (`full in ≤ 1h 21m`).
   - A charge unplugged before full says how far it got and how long that
     took: `96% in 1h 03m · unplugged before full`, timed to the first
     sample at its highest percent (`≤` after a sleep, as above).
   - *At 100 %* is from that sample to the unplug (or now), sleep on the
     charger included, as F6's "pinned at 100 %" rule: a full battery left
     plugged in overnight is the habit it measures.
   - *Not charging* is the awake time on AC, not charging, below 100 %,
     before the first 100 % (after it, macOS lets the battery drift a few
     percent before topping up: that is time at 100 %, not a hold):
     macOS holding the charge (Optimized Charging, the macOS 26.4 Charge
     Limit, heat) or a charger too weak to charge. It is named for what was
     seen, not a guessed cause: `NotChargingReason` in ioreg is
     undocumented. Shown from 5 minutes.
   - An ongoing session that is charging shows an estimated **time to full**
     from this Mac's own charge curve (below), only when its newest sample
     is from the last 90 s and the recorder is writing: just after a wake,
     the newest percent can be hours old.
   - A run already on AC at the first sample batlog ever wrote has no known
     plug-in and is left out. Every other run that the range starts inside
     keeps its real start: samples load from the battery sample before it.
   - IDs are the plug-in minute as for battery sessions (`0927-0140`); they
     are a separate list, and `top --session` takes battery sessions only.
5. **Charge curve.** Charging slows as the battery fills (measured on an M4
   Pro: ~78 %/hr to 60 %, 64 %/hr in the 70s, 48 %/hr in the 80s, 22 %/hr
   in the 90s), so a rate measured now says little about the rest. batlog
   learns a rate per tenth of the battery from the last 30 days: the percent
   gained over awake intervals on AC that were charging, below 100 %. A
   tenth with under 10 minutes of such charging uses the default curve
   (those M4 Pro rates). The time to full sums the remaining tenths. A
   backtest on seven real charges from 19–36 % was within ±7 minutes.
6. **Headline answers** for the range:
   - *First charge* = first `plugged` event.
   - *Last on battery* = most recent `unplugged` event.
   - *Battery lasted* = awake duration of the last completed battery session,
     or `ongoing, Xh so far`.
7. **Fallback:** if the range starts before the daemon's first sample, parse
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
  0926-0912  09:12 → 14:05   4h 53m awake   100% → 8%    18.8 %/hr
  0926-1530  15:30 → now     1h 12m so far   95% → 81%   11.6 %/hr   (ongoing)

charging sessions
  0926-0742  07:42 → 09:12   31% → 100%   full in 1h 11m · 19m at 100%
  0926-1405  14:05 → 15:30   12% → 96%    96% in 1h 18m · unplugged before full

on battery 6h 05m · on AC 4h 12m · asleep 1h 40m
```

Other charge lines: `full in ≤ 1h 21m · 1h 50m at 100%` (full after a
sleep), `already full · 2h 10m at 100%`, `charging · full in ~45m
(ongoing)`, `not charging at 80% for 1h 10m` (appended to any of them).

JSON: `{range, first_charge, last_unplug, sessions: [{id, start, end,
awake_minutes, start_pct, end_pct, drain_pct_per_hr, ongoing, source}],
charge_sessions: […], totals: {battery_min, ac_min, sleep_min}}`.

A charge session:
```json
{"id": "0927-0140", "start": 1790448052, "end": 1790475999,
 "start_pct": 20, "end_pct": 100,
 "full_at": 1790453512, "minutes_to_full": 91, "full_is_upper_bound": false,
 "max_pct": 100, "minutes_to_max_pct": 91, "max_is_upper_bound": false,
 "minutes_at_full": 374,
 "not_charging_minutes": 0, "not_charging_pct": null,
 "est_minutes_to_full": null, "charging": false, "ongoing": false, "data_gap": false}
```
`end` is `null` while ongoing; `full_at`, `minutes_to_full` and
`minutes_at_full` are `null` when it never reached 100 %;
`max_pct` is the highest percent on the charger and `minutes_to_max_pct`
the time to its first sample, an upper bound when `max_is_upper_bound`;
`not_charging_pct` is `null` without a hold; `est_minutes_to_full` is set
only for an ongoing session that is charging; `charging` is true only for
one that is ongoing and charging at its newest sample.

Each session has an **ID**: its start in local time as `MMDD-HHMM`
(`0926-1656`). A second session starting in the same minute gets `b`, `c`,
…. `batlog top --session <id>` (F4) shows which apps drained it.

Suffixes are counted within the range shown. `top` resolves an ID over a
history from the start of its day, so the two agree unless a `--since`
range starts between two sessions of the same minute. That is rare enough
to leave alone.

## Edge cases

| Case | Behaviour |
|---|---|
| Session spans midnight with `--today` | Include it; prefix the start with the date (`yesterday 23:10 →`) |
| Mac slept mid-session | Sleep excluded from duration and drain; shown in totals |
| No events in range | `no charge/discharge events in this range`, exit 0 |
| Range before install and pmset log empty | Same message + `history starts <install date>` |
| Daemon was down for hours | Treat like a sleep gap; mark the session `(data gap)` |
| Plugged in at 100 % | `already full`, time at 100 % from the plug-in |
| Unplugged before 100 % | `unplugged before full`; no time to full or at 100 % |
| Recorder silent while still on AC | Ongoing, `(data gap)`; time at 100 % runs to the last sample, not to now |

## Acceptance criteria

- [ ] The three headline answers are correct on fixture data that includes a
      midnight-spanning session and an ongoing one.
- [ ] A fixture with an 8 h sleep gap does not inflate battery life.
- [ ] `--events --json` is sorted ascending with a stable schema.
- [ ] pmset-fallback rows carry `source: "pmset"` and are never merged into
      daemon sessions.
- [x] Charge sessions: time to full, its upper bound after a sleep, time at
      100 % through sleep, holds, a data gap and a session crossing the
      range start match hand-computed fixtures.
- [x] The time-to-full estimate is within ±10 minutes on real charges (seven
      from 19–36 %, ±7 minutes).
