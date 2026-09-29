# ADR-0003: Per-app energy comes from `top -o power`, shown as a relative share
Date: 2026-09-26
Status: superseded by [ADR-0006](./0006-app-energy-from-kernel-coalition-counters.md) (2026-09-29): `top` ranked apps wrongly and cost 3–5 % of the energy it measured

## Context
The product's core is "which apps cost me the most battery over the week".
macOS has two sources of per-process energy: `powermetrics` (real milliwatts,
needs `sudo`) and `top` (Apple's unitless "energy impact", no sudo — the same
number Activity Monitor's Energy tab shows).

## Options considered
- **`powermetrics`:** real units, but a root daemon or a sudoers rule on every
  user's machine, and an expensive process start per sample.
- **`top -l 2 -o power`:** no privileges, 1.5 s per sample (the first of the
  two samples is always zero and must be discarded), unitless.
- **Both, powermetrics optional:** doubles the parser and the test surface for
  a feature most users would not enable.

## Decision
v1 uses `top -l 2 -o power -stats pid,command,power,cpu,mem`, keeps the
second sample, and stores the top 15 processes per tick after grouping helper
processes under their app (strip ` Helper`, ` Helper (Renderer|GPU|Plugin)`;
otherwise group by command name). Energy is always presented as a **share of
the total in the period**, never as watts. `powermetrics` is not used.

## Consequences
+ No sudo, no installer prompts, the same numbers users already see in
  Activity Monitor.
+ A share sums to 100 %, which makes "≈ 31 % of your battery" a defensible
  estimate: share × battery-percent consumed while discharging in the period.
- 1.5 s of `top` per minute is batlog's dominant cost. It is inside the <1 %
  CPU budget but must be measured, and batlog must never appear in its own top
  ten.
- Two apps with the same command name are merged. Accepted and documented.
- Helper processes that do not follow Apple's naming (some Electron apps)
  appear as their own row. A parent-process walk would fix this but needs a
  second `ps` call per tick; deferred until a real case shows up.
