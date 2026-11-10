> **Historical snapshot.** As delivered on 2026-11-09, before batlog adopted the kit; kept for the record, not maintained. Current rules: [docs/reference/brand.md](../../../../docs/reference/brand.md).

# batlog brand information

Working proposal as of 2026-11-09. Product facts follow the local project README and terminal styles; the visual system is a new proposal based on the supplied logo.

## Name and description

**Name:** `batlog`, lowercase, matching the command and repository. Use `batlog` in headings and prose; use code formatting when referring to a command.

**Short description:** A macOS battery-history and per-app energy tracker.

**One sentence:** batlog records battery and per-app energy in the background, helping you understand what drained a charge and how your battery behaves over time.

**Proposed headline:** Understand what drained your battery.

**Proposed supporting line:** Record quietly. Compare sessions. See which apps used the most energy.

The headline and supporting line are suggestions for future brand materials, not historical approved taglines and not part of the delivered logo lockups.

## Product foundation

- **Audience:** terminal-comfortable MacBook users, especially developers investigating battery drain from browsers, editors and other demanding applications.
- **Problem:** live battery levels alone do not explain which apps drained a particular charge, what happened over previous days, or whether capacity is changing.
- **Value:** accumulated history of battery sessions and per-app energy, with reports, health trends and CSV/JSON exports.
- **Experience:** macOS CLI with a terminal UI; local-only recording, no network calls and no sudo for normal operation.
- **Behavior:** measures and reports. It does not throttle processes, limit charging, enable Low Power Mode, or automatically improve battery life.
- **Measurement language:** per-app energy is recorded; per-app battery percentage cost is an estimate allocated from energy shares. Do not describe that estimated cost as an exact physical measurement.

Useful example command: `batlog top --session last`.

## Logo concept and source

Primary source: `source/originals/Designing_batlog_logo_symbol_343191ad_2K.jpg`. A solid battery with one descending stepped history line is the selected working direction. It communicates battery + recorded history with fewer shapes than the alternative.

Preserved alternative: `source/originals/Design_logo_for_battery_tool_e655feea_2K.jpg`. Its nested frame/history shape is retained as reference rather than promoted to a second official mark.

The new SVG is a hand-authored geometric reconstruction: one compound path with an even-odd hole, using straight segments and exact circular arcs. Corner radii, step spacing and surface fill are regularized. It is not an original Flow vector or a pixel-identical trace. The source JPEGs are unchanged.

The **micro** version widens the same negative-space history line from 52 to 90 design units for small uses. It retains the battery silhouette and the two downward steps. Its artwork is a derived proposal.

## Proposed color system

| Color | Hex | Role |
|---|---|---|
| Battery Ink | `#242021` | Main monochrome mark; normalized median dark source color |
| White | `#FFFFFF` | Light surfaces and reverse artwork |
| Signal Cyan | `#0891B2` | Optional proposed digital accent and alternate mark |
| Black | `#000000` | Strict one-color production variant |

The app currently uses ANSI cyan for battery charts, green for charging, yellow for cursor/warnings and red for problems. Actual RGB colors depend on the user's terminal theme. Signal Cyan is proposed for brand materials; it is not an existing fixed app hex value.

## Proposed typography

- **Wordmark and command examples:** JetBrains Mono Semibold (600). Lowercase `batlog`; SVG lettering is outlined and needs no font installation.
- **Supporting copy:** Plus Jakarta Sans Regular/Semibold. Used in the guide, not applied to the terminal.
- **Terminal UI:** the user's terminal font and theme remain in control.

Bundled font files include their SIL Open Font License texts and are separate from the original raster artwork.

## Proposed usage defaults

- Primary symbol: start at 48px wide. Micro symbol: use at 16-32px; inspect the actual output at 16px.
- Full horizontal lockup: start at 180px wide; choose the symbol alone if lettering becomes too small.
- Clear space: one quarter of the visible battery height, outside every side of the visible shape. Export canvas padding alone may not supply the full clear space.
- Keep the terminal on the right and the history descending from left to right. Scale uniformly and preserve the transparent cutout.
- Use ink/black on light surfaces and white on dark. Keep strong contrast when using the optional cyan.
- The logo is an identity mark, not a live battery-level display or a low-battery warning. Do not recolor it automatically to imply live device status.

## Voice

Write plainly and use concrete descriptions. Sound calm, helpful and technically honest. Explain what a number means, especially whether it is measured or estimated.

Use: "See which apps used the most energy," "Compare battery sessions over time," and "Keep battery history on your Mac."

Avoid promises such as "double your battery life," "exact battery cost for every app," "automatic battery optimization," or unsupported claims of compatibility and measurement accuracy.

## Adoption status

This kit is for local review. No application code, CLI output, terminal colors, installation behavior or release assets were changed. Square tiles, ICOs, ICNS, the macOS iconset and browser assets are reusable publishing candidates; their presence does not introduce a native GUI, browser UI or support for another operating system.

Review reconstructed contours, proposed typography, optional cyan, spacing and minimum sizes before adopting the identity.
