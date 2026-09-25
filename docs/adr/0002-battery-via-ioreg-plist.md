# ADR-0002: Battery data comes from `ioreg` plist output, not the IOKit API
Date: 2026-09-26
Status: accepted

## Context
Every command needs battery state (level, source, charging) and `health` needs
the detail (cycles, capacities, voltage, current, temperature). macOS exposes
all of it in the `AppleSmartBattery` IORegistry entry. It can be read through
the IOKit C API, through `pmset -g batt` (four fields, prose), or through
`ioreg -rc AppleSmartBattery -a` (all 64 keys, as a plist).

Measured on a 2024 MacBook, macOS 15.7: `pmset` 8 ms, `ioreg` 13 ms. The
values are identical to the IOKit API because `ioreg` is a viewer over the same
registry.

## Options considered
- **`pmset` for state + `ioreg` for health:** two parsers, one of them prose.
- **`ioreg -a` for everything:** one process, one structured parser, all keys.
  Same approach as `distatus/battery`, the standard Go battery library.
- **IOKit via cgo/purego:** ~0.1 ms per read and change notifications, at the
  cost of [ADR-0001](./0001-pure-go-no-cgo.md).

## Decision
All battery data comes from `ioreg -rc AppleSmartBattery -a`, parsed as a
plist. `pmset` is used for one thing only: `pmset -g log` as the history
fallback for days before batlog was installed.

Key rules, verified against System Settings on Apple Silicon:
- **Health % = `AppleRawMaxCapacity` / `DesignCapacity`.** This is the gauge
  chip's real measured full-charge capacity (it equals the chip's own
  `FccComp` value). It is shown as the headline.
- **`NominalChargeCapacity` / `DesignCapacity`** is Apple's smoothed figure and
  is what System Settings shows. It is displayed beside the headline, labelled
  "Apple reports", so users do not think batlog disagrees with macOS. Both are
  logged daily; the key is absent on older Intel Macs, then only raw is shown.
- **Percent:** on Apple Silicon `CurrentCapacity`/`MaxCapacity` are percentages
  (`MaxCapacity == 100`); on Intel they are mAh. If `MaxCapacity == 100`, use
  `CurrentCapacity` directly; otherwise compute from
  `AppleRawCurrentCapacity / AppleRawMaxCapacity`.
- **Watts** = `Voltage` (mV) × `Amperage` (mA) / 10⁶; negative amperage means
  discharging.

## Consequences
+ One parser, structured input, golden-file tests per Mac model from a single
  `ioreg -a` dump that any contributor can send.
+ Power draw in watts and the full health picture come for free.
- Polling only: no "plugged in" event. At one sample a minute this is fine;
  v1.1 notifications can also live with it.
- Keys vary by model. Every key is optional in the parser; a missing key omits
  the field rather than failing the command.
