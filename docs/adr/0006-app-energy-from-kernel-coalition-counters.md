# ADR-0006: Per-app energy comes from the kernel's coalition energy counters
Date: 2026-09-29
Status: accepted · supersedes [ADR-0003](./0003-app-energy-from-top-without-sudo.md)

## Context
ADR-0003 chose `top -o power`. The PRD's riskiest-assumption spike tested it
against Activity Monitor's *12 hr Power* column for three nights
(`scripts/spike/`), and it failed on two counts:

- **Wrong ranking.** `top` sees only processes alive at the sample. A
  developer's editor or agent app starts millions of short-lived children
  (T3 Code's group had started 1.5 M), and 95 % of that group's energy came
  from processes that had already exited. `top` put OrbStack above T3 Code
  and inflated Telegram eleven-fold.
- **Too expensive.** The one-a-minute `top` loop cost 3–5 % of all app energy.
  That broke PRD metric 3 ("never appears in its own top 10").

macOS keeps per-process counters, which a second spike read from pure Go
(`scripts/spike/energy/`):
- the kernel's `proc_info` gives `proc_pid_rusage` v6 and the process's
  coalition id;
- `coalition_info` gives per-coalition totals.

A *coalition* is the responsible-app group Activity Monitor uses. Its
counters are cumulative for the app's life, *including members that
exited*, in nanojoules for CPU, GPU and Neural Engine. Over 12 h the
coalition ranking matched Activity Monitor's top 8 apps in the same order,
and it cost 0.004 % of what it measured.

## Options considered
- **Keep `top`**: wrong order for exactly the apps users care about, and a
  top-ten cost.
- **`top` plus a parent-process walk**: fixes grouping for live processes
  only; still misses exited ones and still costs the same.
- **Per-process `proc_pid_rusage` deltas**: accurate for live processes, but
  a process that exits between reads loses its last minute, and grouping
  needs a walk.
- **Per-coalition `coalition_info` totals** (chosen): grouping by the kernel,
  exited members included, one syscall per coalition.
- **`powermetrics`**: still needs sudo (unchanged from ADR-0003).

## Decision
Once a minute, the daemon does four things:
1. lists processes (`sysctl kern.proc.all`) and reads each one's coalition
   id (`proc_info`, `PROC_PIDCOALITIONINFO`);
2. reads each coalition's resource usage (`coalition_info`,
   `COALITION_INFO_RESOURCE_USAGE`);
3. adds the rise of `energy` (CPU), `gpu_energy_nj` and `ane_energy_nj`
   since the previous read to that app's 15-minute bucket;
4. names each coalition once, by its app bundle (see the F5 design).

These are raw syscalls through `golang.org/x/sys/unix`: no cgo and no root.
This is the first macOS source read by syscall rather than by running a
command. That refines [ADR-0001](./0001-pure-go-no-cgo.md)'s "run a system
command and parse its output". Its point, `CGO_ENABLED=0`, still holds. The struct field indexes
come from xnu headers and are checked by a fixture test and a live sanity
test on macOS CI.

Presentation is unchanged: energy is a **share of the period's total**, and
battery cost is share × percent consumed. Joules are stored because the
kernel gives real units, but no per-app watts are shown in v1 (PRD scope).

## Consequences
+ Rankings match Activity Monitor, including apps whose work runs in
  short-lived children.
+ Sampling costs about 1 J over 12 h, so batlog cannot appear in its own
  top ten.
+ GPU and Neural Engine energy are counted, which `top` did not see.
- The syscalls and the `coalition_resource_usage` layout are not public API.
  They are stable across recent macOS releases but unversioned, so a macOS
  update could move a field. Mitigations: a sanity check refuses implausible
  readings (energy decreasing, one tick claiming more than 200 W), logs one
  warning, and skips energy for that tick without failing it.
- Processes owned by root or by other users are skipped (EPERM). Their
  coalition ids are still readable, so system daemons like WindowServer
  appear under their own names, tagged system, as Activity Monitor hides them.
- Shares are joule shares; Activity Monitor's figures come from Apple's own
  impact model. Orders agree, but percentages can differ by a few points.
