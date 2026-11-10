> **Historical snapshot.** As delivered on 2026-11-09, before batlog adopted the kit; kept for the record, not maintained. Current rules: [docs/reference/brand.md](../../../../docs/reference/brand.md).

# batlog brand kit

As of 2026-11-09. Start with `batlog-overview.png`, `brand-information.md` and `batlog-brand-guide.pdf`.

## Ready-to-use files

- **Main mark:** `svg/reconstructed/batlog-mark-ink.svg` and its transparent `png/` export.
- **Small symbols:** `batlog-mark-micro-*`, with a wider negative-space trace. Use the actual-size candidates in `icons/batlog-symbol-*` for 16, 24, 32, 48 and 64px widths.
- **Name + symbol:** `batlog-horizontal-*` and `batlog-stacked-*`. The lowercase JetBrains Mono Semibold lettering is a new proposed wordmark, delivered as outlines.
- **Name only:** `batlog-wordmark-*`.
- **Colors:** ink, white, black; optional cyan on the primary/horizontal variants.
- **Square icons:** dark/light PNGs from 16 to 1024px; two multi-resolution ICOs; dark macOS ICNS and `batlog.iconset`; favicon, Apple touch icon and sample relative-path web manifest.
- **Originals:** both supplied 2048px JPEGs are preserved with original filenames in `source/originals/`. `svg/reference/` embeds each raster image in an SVG container; those two reference files are not vector reconstructions.

## Identity and artwork status

The solid battery with one descending stepped line is the working primary. The nested-shape alternative is preserved as a reference. `brand-information.md` covers naming, product facts, audience, messaging, voice, color, typography and proposed usage rules.

All SVGs in `svg/reconstructed/` use vector paths only, with no embedded images, external fonts, scripts or masks. The primary battery is a compound even-odd path with a transparent channel. It is hand-authored reconstructed artwork, not a pixel-identical copy or an original Flow vector. No automatic tracing is used. The micro channel is deliberately wider.

Nineteen new SVGs have matching PNGs: 2400px wide for marks/lockups, 2048px for square app tiles. Logo PNGs are RGBA with true transparent cutouts and canvas margins. Filled app tiles are intentionally different: their internal negative space reveals the tile's fill.

The mono identity follows the supplied artwork; cyan, lettering, headline and usage defaults are proposals for review. Current terminal colors are ANSI/theme-dependent, not fixed brand hex values. No app changes or deployment were made. Platform icon assets are publishing candidates, not installed application features.

## Portable rebuild

Requires Python 3.11+ and Node.js 20+ with npm:

```sh
python3 -m pip install -r source/requirements.txt
npm ci --prefix source
python3 source/build_kit.py
python3 source/verify_kit.py
```

The builder reads bundled inputs, regenerates outputs and creates the sibling ZIP. It requires no app checkout or network after dependencies are installed. Pinned dependencies and stable ZIP/PDF settings support repeatable builds.

After an intentional artwork/PDF edit, render every PDF page with Poppler and inspect it. Update `reference/pdf-review.json` with the current PDF hash; the verifier rejects a stale review. Temporary `.build`, `node_modules`, Python caches and `.DS_Store` are excluded from the manifest/archive.

## Review evidence

`reference/` contains source comparisons, high-zoom geometry details, actual/enlarged small-size checks, input hashes, brand tokens, PDF review and validation records. `manifest.json` records output file hashes and sizes. Fonts and their OFL licenses are bundled. `source/geometry.py` is the editable symbol source; `source/build_kit.py` assembles the complete kit.
