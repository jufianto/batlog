> **Historical snapshot.** As delivered on 2026-11-09, before batlog adopted the kit; kept for the record, not maintained. Current rules: [docs/reference/brand.md](../../../../docs/reference/brand.md).

# Batlog application alignment

As of 2026-11-09. Local audit of the project README and `internal/ui/style.go`, preserved as bundled snapshots. No public website is claimed or checked.

Batlog is a local-only macOS CLI/TUI that records battery history and per-app energy. It offers status, session history, top apps, reports, health and CSV/JSON exports. Estimated per-app battery percentage cost is allocated from energy shares; it is not an exact direct battery-cost measurement. It measures without changing charging or process behavior.

No existing logo image was found in the inspected repository outside generated distribution folders. The delivered identity comes from the two supplied Flow concepts, not existing application artwork. Their original filenames and hashes are preserved.

Current terminal styles use ANSI cyan (6) for battery, green (2) for charging, yellow (3) for cursor/warnings, red (1) for problems, faint for sleep/secondary text, bold titles and reverse selection. Those are theme-dependent terminal colors. `#0891B2` is only a proposed brand accent; there is no fixed source hex to match. The terminal's font is chosen by the user; the kit's new JetBrains Mono wordmark and Plus Jakarta Sans supporting type do not replace it.

The source README and command name consistently use lowercase `batlog`; the new wordmark preserves that spelling. The primary symbol is the solid battery with one history trace; the nested-frame alternative remains reference artwork. New wordmarks, accent and usage rules await review.

No code, command output, terminal styling, installation behavior or release was edited. App tiles and platform icon formats are optional publishing assets; adopting them in software would be separate work.
