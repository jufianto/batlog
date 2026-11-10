# batlog brand kit

The logo artwork, its exports and the brand guide. Start with
[docs/reference/brand.md](../../docs/reference/brand.md): which file to use
for which job, the rules, and how to change anything here.

| Folder or file | What it holds |
|---|---|
| `batlog-brand-guide.pdf` | The seven-page brand guide |
| `batlog-overview.png` | One-page overview: mark, reverse, lockup, palette |
| `svg/` | The 19 vector files: marks, micro marks, wordmarks, horizontal and stacked lockups, app tiles |
| `png/` | A transparent PNG of each SVG: 2400 px wide, app tiles 2048 px |
| `icons/` | Square app tiles 16-1024 px, ICO, ICNS and iconset, a favicon, an Apple touch icon and a sample web manifest |
| `fonts/` | JetBrains Mono SemiBold (the wordmark) and Plus Jakarta Sans (the guide), with their OFL licences |
| `reference/` | Review sheets (source comparison, curve detail, small sizes) and the PDF review record |
| `history/` | The kit's own notes as delivered, a historical snapshot |

Every colour in the SVGs and the manifest is a token in
[brand/tokens.json](../tokens.json); `make brand-check` fails if one drifts.
