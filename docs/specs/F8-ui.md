# F8 — `batlog ui`

**Story:** As a user, I want one full-screen view of my battery: where it
stands now, how today and this week went, which apps drained it, and how
the battery is ageing. I want to move through it with the keyboard instead
of running five commands.

## CLI contract

```
batlog ui
```

- **No flags.** `--json` is a usage error (exit 2) with the message
  `ui is interactive; use the other commands for JSON`.
- **Needs a terminal on stdin and stdout.** Without one it exits 1 with
  `batlog ui needs a terminal`.
- **Size:** works from 80×24. In a smaller window the screen shows only
  `make the window at least 80×24 (now W×H)` until it grows.
- **Exit codes:** quitting exits 0. A failure to start the terminal exits 1.

## Principles

These carry over from the PRD: the UI adds a view, not a new behaviour.

- **Read-only.** It opens the database read-only. It never writes to the
  database, installs anything or touches the recorder.
- **No network calls** and no sudo.
- **Same numbers as the CLI.** Every figure comes from the code behind
  F1–F7. Sessions, IDs, battery cost and flags all match what the
  matching command prints for the same range.
- **Cheap.** It refreshes once a minute, like the recorder, and reads app
  energy only from the database. The live 1-second probe from `top --live`
  never runs. Idle, it must use under 1 % CPU.

## Layout

```
┌ batlog ─────────────────────┐┌ 1 Battery · last 7 days ───────────────────────────────────┐
│ 🔋 69%  on battery  5.0 W   ││100│█▇     █▇      ▇█     ▇█      █▇     █▇    █▇           │
│ drain 12.0 %/hr             ││   │  ▆▅▄▃ ▃ ▆▅▄  ▃ ▃ ▆▅▄▃ ▃  ▆▅▃  ▃ ▆▅▄▃  ▆▅▄▃ ▃ ▆▅▃▄▅▆▆      │
│ left 5h 45m (macOS 6h 55m)  ││ 50│      ▂     ▂▁     ▂▁       ▂▁       ▂     ▂               │
│ worst now T3 Code (Alpha)   ││  0└──────┬──────┬──────┬──────┬──────┬──────┬──────┬─────── │
├ health ─────────────────────┤│        Sun    Mon    Tue    Wed    Thu    Fri    Sat         │
│ 90.0% · 396 cycles · Normal ││ on battery 39h 17m · on AC 19h 00m · asleep 104h 21m       │
├ view ───────────────────────┤│                                                            │
│ ▸ 1 Battery                 ││  ID          WHEN                 %          RESULT        │
│   2 Apps                    ││▸ 1002-2327   yesterday 23:27→now  99→69%     6.0 %/hr …    │
│   3 Report                  ││  1002-2156 ⚡ yesterday 21:56     21→99%     before full   │
│   4 Health                  ││  1002-1730   yesterday 17:30      100→21%    22.1 %/hr     │
│                             ││  1002-1538 ⚡ yesterday 15:38     35→100%    full 1h 16m   │
│ range  last 7 days          ││  1001-2113   Thu 21:13            100→35%    17.9 %/hr     │
│ data   1 min ago            ││  1001-1925 ⚡ Thu 19:25            32→100%    full 1h 14m   │
└─────────────────────────────┘└────────────────────────────────────────────────────────────┘
 1-4 view · ↑↓ select · enter open · t today · w week · [ ] earlier/later · r refresh · ? help · q quit
```

- **Sidebar** (30 columns, always visible): live status, a health line,
  the view list, the current range and the age of the newest sample.
- **Main area:** the selected view. It fills the rest of the window and
  scrolls when its content is taller than the window.
- **Key line** at the bottom: the keys that work in the current view. It
  also shows errors, in place of the keys, until the next key press.

### Sidebar

| Part | Content |
|---|---|
| Status | Percent, power source, watts. Then the same estimate F1 prints: on battery, drain and time left (batlog's, then macOS's); charging, `full in` (batlog's, then macOS's). Then the worst app now. A line F1 would omit is omitted. |
| Health | Health %, cycles, condition (F2). If the condition is `Service recommended`, it is shown in red. |
| View | The four views. The selected one is marked with `▸`. |
| Range | `today`, `last 7 days`, or a past day or week such as `Thu 01 Oct` or `21 – 27 Sep` (see Ranges). |
| Data | The age of the newest sample: `1 min ago`, `3 h ago`. If it is more than 10 minutes old while the Mac is awake, it reads `recorder stopped?` in yellow, as `daemon status` would. |

## Views

### 1 Battery (the default view)

- **Chart:** the battery percent over the range, one column per time
  slice: the range divided by the chart width.
  - A column's height is the **lowest** percent in its slice, so a drain
    is never hidden.
  - Colour shows the power source: on battery (default colour), on AC
    (green), asleep (dim), no data (blank).
  - The x axis marks hours for a day and weekdays for a week.
  - Without colour, AC columns use `▓` instead of `█`.
- **Totals line:** the same one `history` prints under its lists.
- **List:** battery sessions and charge sessions together, newest first.
  - Charges are marked `⚡`.
  - The RESULT column holds the session's drain, or the charge's outcome:
    `full 1h 16m`, `full ≤ 1h 21m`, `before full`, `charging · ~37m`.
  - The same `(ongoing)` and `(data gap)` tags as F3, shortened to `…`
    when the row is too narrow.
- **Enter on a battery session** opens its detail view:
  - the session's chart, zoomed to its own start and end;
  - its F3 line;
  - its top 5 apps with battery cost (the same as `top --session <id>`);
  - its heaviest 30 minutes (F4).
- **Enter on a charge** opens its detail view:
  - the charge's chart;
  - start → end percent;
  - time to full, or an upper bound;
  - time at 100 %, and any time not charging below full (F3);
  - for an ongoing charge, the curve's estimate and macOS's.
- **Esc** goes back to the list. The selected row is kept.

### 2 Apps

- **Table:** the F4 table for the range: rank, app, share with a bar, and
  battery cost.
  - System apps are marked `⚙`, as in F4.
  - All apps are listed, scrollable; `top` cuts off at its N.
  - It shows the same notes as `top`, such as `app energy from about …`.
- **Enter on an app** opens a chart of its energy over the range: one bar
  per hour for a day, one per day for a week. It comes from the 15-minute
  `app_energy` buckets. Below the chart is the app's total and share, and
  its peak hour or day.
- With no app energy recorded: `no app energy recorded yet`.

### 3 Report

- The F6 report for the range, as `report` prints it, in a scrolling view.
  - `today` uses the daily report and `last 7 days` the weekly one.
  - A past day or week uses `--since`-style bounds for that day or week.
  - Flags (`⚠`) are shown in yellow.

### 4 Health

- **Current:** the F2 lines: health, Apple's figure, mAh, cycles,
  temperature, voltage, condition.
- **Chart:** health % per day from the `health` table. It shows every row,
  however long the history.
  - The y axis covers the data's own range, not 0–100 %, so a 2 % change
    is visible.
  - Under the chart is the F2 trend line, or `trend needs 30 days of
    history (N so far)`.

## Ranges

| Key | Range |
|---|---|
| `t` | today: local midnight → now (the default) |
| `w` | the last 7 days, as `history --week` |
| `[` / `]` | move the range one day (today mode) or one week (week mode) earlier or later. It stops at the first day with data and at today. |

- A past day is shown in full (midnight → midnight) and gets no `now`.
- Every view uses the same range. Switching views keeps it.

## Keys

| Key | Action |
|---|---|
| `1`–`4`, `Tab`, `Shift-Tab` | switch views |
| `↑` `↓` / `k` `j`, `PgUp` `PgDn`, `g` `G` | move the selection or scroll |
| `Enter` | open the selected row's detail |
| `Esc` | back out of a detail, or close help |
| `t` `w` `[` `]` | change the range (see Ranges) |
| `r` | refresh now |
| `?` | help: every key, with a line on what each view shows |
| `q`, `Ctrl-C` | quit |

## Refresh

- **When:** on start, every 60 s, on `r`, and when the range changes.
- **In the background:** a refresh loads in the background, and the
  screen keeps showing the last data until the new data arrives. Only the
  first load shows `loading…`.
- **Selection:** a refresh keeps the selected row by its ID, not its
  position. A new session at the top does not move the selection.
- **Errors:** a read that fails shows its error in the key line. That view
  keeps its last data, and every other view still refreshes.
- **Freshness:** the live battery reading (ioreg, as F1) is taken on every
  refresh. Everything else comes from the database.

## Colour

- **Colour is off when** `NO_COLOR` is set or `TERM=dumb`, as in the
  other commands.
- **Glyphs:** Unicode box drawing and block characters only, no Nerd Font
  icons. Without colour the layout and every figure stay the same.

## Edge cases

| Case | Behaviour |
|---|---|
| No database or no samples | Status and Health work from live readings. Battery, Apps and Report show `no history yet — run 'batlog daemon install'` |
| Recorder stopped | Data line in yellow (see Sidebar). The views show the data that exists |
| No app energy | Apps: `no app energy recorded yet`. Battery details have no app list |
| Health table empty | The Health view shows the current reading only, with `no daily history yet` instead of the chart |
| Range has no sessions | Battery: the chart (likely all AC or asleep), then `no sessions in this range` |
| Window resized | Re-laid out at once. The selection is kept |
| Window under 80×24 | The size message (see the CLI contract). The data keeps refreshing |
| Database locked by the recorder's write | Retried on the next refresh. The error shows only if three refreshes in a row fail |
| Mac sleeps with the UI open | On wake, the next tick refreshes. The gap shows as asleep, as F3 reads it |

## Acceptance criteria

- [ ] For the same range, every number in a view equals the matching
      command's output: `status`, `health`, `history`, `top`, `report`.
      Tested against the CLI on one fixture database.
- [ ] Rendering is a pure function of data and window size. Each view has
      a golden test at 80×24 and at 120×40, in colour and with `NO_COLOR`.
- [ ] Key flows are tested end to end on a fake data source:
  - switch views;
  - open and close a detail;
  - `[` `]` at both ends of the data;
  - refresh keeps the selection by ID.
- [ ] The database is opened read-only (test: the file's modification
      time is unchanged after a session of key presses).
- [ ] Idle CPU under 1 % and memory under 50 MB with a week of data,
      measured on the author's Mac.
- [ ] A refresh of `last 7 days` takes under 300 ms on that Mac.
