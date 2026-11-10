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
| `source/` | The builder: `geometry.py` (the symbol), `build_kit.py`, `verify_kit.py`, `render.mjs`, pinned `requirements.txt` and npm lockfile, and the two supplied JPEGs in `originals/` |
| `manifest.json` | The builder's record: a hash of every input and output, and the token values it drew with |

Every file here except this README, `history/`, `reference/pdf-review.json`
and `reference/validation.json` is built by `source/`. Do not edit them by
hand:

```sh
make brand-kit         # rebuild brand/kit from brand/tokens.json, the geometry and the fonts
make brand-kit-check   # rebuild into a temporary copy; fails unless every file is the same, byte for byte
```

Both need Python 3.11+ and npm; they install pinned packages into
`source/.venv` and `source/node_modules` (both gitignored). `make brand-check`
needs only Go: it fails when a kit file or a token the kit was drawn with no
longer matches `manifest.json`, so CI catches a stale kit without Python.
The byte-for-byte rebuild is verified on macOS on Apple Silicon.
