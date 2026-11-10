# batlog brand

The rules for batlog's logo, colours and type, which file to use for which
job, and how to change each one. Adopted on 2026-11-10 from the brand kit
delivered on 2026-11-09 (batlog#33). The recipes for working on a surface are
in the [`batlog-brand` skill](../../.claude/skills/batlog-brand/SKILL.md).

## Where things are

| Path | What it is |
|---|---|
| [`brand/tokens.json`](../../brand/tokens.json) | The one palette source: every colour and terminal style, with its role, and which name in which file uses it |
| [`brand/check/`](../../brand/check/) | The check behind `make brand-check`, with its mutation tests |
| [`brand/kit/`](../../brand/kit/) | The logo files, icons, fonts, guide PDF, overview and review sheets |
| [`brand/kit/source/`](../../brand/kit/source/) | The kit builder: `geometry.py` is the symbol, `build_kit.py` builds everything else |
| [`brand/kit/batlog-brand-guide.pdf`](../../brand/kit/batlog-brand-guide.pdf) | The seven-page guide, built from the same tokens and geometry |
| [`brand/kit/history/`](../../brand/kit/history/) | The kit's own notes as delivered, a historical snapshot |

## The logo

The **mark** is a solid battery with one stepped line cut out of it,
descending from left to right: a battery, and its recorded history. It is one
vector path with a transparent cutout, a hand-made reconstruction of the
supplied concept. The nested-frame concept is kept for reference only
(`brand/kit/source/originals/`), not as a second mark.

| Form | Files in `brand/kit/svg/` | Use |
|---|---|---|
| Mark | `batlog-mark-{ink,white,black,cyan}.svg` | The symbol alone, 48 px wide or more |
| Micro mark | `batlog-mark-micro-{ink,white,black}.svg` | 16-32 px: the cutout is wider (90 design units instead of 52) so it survives small sizes |
| Wordmark | `batlog-wordmark-{ink,white,black}.svg` | The name alone |
| Horizontal lockup | `batlog-horizontal-{ink,white,black,cyan}.svg` | Mark and name side by side, 180 px wide or more: headings, banners |
| Stacked lockup | `batlog-stacked-{ink,white,black}.svg` | Mark above name, for squarer spaces |
| App tiles | `batlog-app-icon-{dark,light}.svg` | A rounded square with the micro mark: ink tile and white mark, or the reverse |

Each SVG has a PNG of the same name in `brand/kit/png/` (2400 px wide, app
tiles 2048 px), with a transparent background and cutout.

### Rules

Adopted for **placed logos**: images in the README, docs, social posts,
release pages, slides and print. They do not apply to terminal chrome: the
`batlog` title in `batlog ui`'s sidebar is text, not a logo (see
[Surfaces](#surfaces)).

- **Clear space:** keep a quarter of the visible battery's height empty on
  every side, measured from the visible shape. A file's own canvas padding
  may be less than that.
- **Minimum sizes:** the mark 48 px wide; below that use the micro mark
  (16-32 px) and check it at the real size and background. The horizontal
  lockup 180 px wide; narrower, use the mark alone.
- **Contrast:** ink or black on light backgrounds, white on dark. Cyan is
  optional, only through the `-cyan` files, and only where its contrast is
  strong.
- **Meaning:** scale uniformly; keep the battery's terminal on the right and
  the history descending left to right; keep the cutout transparent; no
  shadows, extra chart detail or redrawn steps.
- **Not a status light:** the mark is an identity, not a battery gauge. Never
  recolour it to show a live level, charging or a warning.
- **The name** is always lowercase `batlog`, in prose and headings too; in
  code formatting when it means the command.
- **The wordmark is only the name.** It is outlined lettering, never the name
  typed in a font. Anything next to it (a page name, "docs") is ordinary text
  in the surrounding face, not part of the wordmark.

### Which file for which job

| Job | File |
|---|---|
| A README or docs heading | `svg/batlog-horizontal-ink.svg`, with `-white` for dark mode in a `<picture>` (as the README does) |
| The symbol in docs or slides | `svg/batlog-mark-ink.svg` or `-white.svg` |
| A symbol at 16-32 px | `svg/batlog-mark-micro-*.svg`, or the ready-sized `icons/batlog-symbol-{ink,white,black}-{16,24,32,48,64}.png` |
| A profile picture, avatar or distribution page | `icons/batlog-app-dark-{size}.png` (or `-light`), 16 to 1024 px |
| A website's favicon, touch icon and manifest | `icons/favicon-32.png`, `icons/apple-touch-icon.png`, `icons/site.webmanifest` |
| A native app icon (batlog has none) | `icons/batlog.icns`, `icons/batlog.iconset/`, `icons/batlog-{dark,light}.ico` |
| One-colour print or a stamp | the `-black` files |
| Where SVG is not accepted | the PNG of the same name in `png/` |

Favicons, app tiles and icons are the one place the logo sits on a coloured
tile; everywhere else it stands on the page's own background.

## Colour

Every colour is a token in `brand/tokens.json`. `make brand-check` fails when
a line here shows a different value from the token it names.

| Name | Value | Token | Role |
|---|---|---|---|
| Battery Ink | `#242021` | `brand.ink` | The identity colour: the logo on light backgrounds, the dark app tile |
| White | `#ffffff` | `brand.white` | Reverse artwork on dark backgrounds, the light app tile |
| Black | `#000000` | `brand.black` | The strict one-colour variant, for print |
| Signal Cyan | `#0891b2` | `brand.cyan` | The optional accent: the cyan files only. Never a terminal colour, never a status |
| Mist | `#f4f6f8` | `guide.mist` | Guide and overview panels |
| Muted | `#5b6573` | `guide.muted` | Guide and overview secondary text |
| Swatch edge | `#dce1e5` | `guide.swatch-edge` | The outline that shows the white swatch in the overview |

The terminal has no fixed colours: batlog paints ANSI colour numbers, and the
user's terminal theme decides what they look like.

| Style | Value | Token | Where |
|---|---|---|---|
| Battery | `ansi:6` | `terminal.battery` | `batlog ui`: battery % awake on battery (cyan) |
| Charging | `ansi:2` | `terminal.charging` | `batlog ui`: battery % on AC (green) |
| Cursor | `ansi:3` | `terminal.cursor` | `batlog ui`: the chart cursor (yellow) |
| Warning | `ansi:3` | `terminal.warn` | `batlog ui`: warnings and read errors, the report's ⚠ lines, a stopped recorder (yellow) |
| Problem | `ansi:1` | `terminal.problem` | The battery condition when it needs service, in `batlog ui`'s sidebar and `batlog health` (red) |
| Secondary | `faint` | `terminal.secondary` | `batlog ui`: asleep columns, secondary text |
| Title | `bold` | `terminal.title` | `batlog ui`: box titles, including `batlog`, and report section headings |
| Selected | `reverse` | `terminal.selected` | `batlog ui`: the selected row |

## Type

- **Wordmark:** JetBrains Mono SemiBold (600), lowercase. In every SVG and
  PNG the letters are outlines, so nothing needs the font installed and no
  surface loads it.
- **Guide text:** Plus Jakarta Sans Regular and SemiBold, in the guide PDF and
  the overview only.
- **Terminal:** the user's own terminal font, in `batlog ui` and all command
  output. batlog never asks for a font.

The font files and their SIL Open Font Licences are in `brand/kit/fonts/`; the
kit builder is their only user.

## Surfaces

| Surface | What carries the brand | Source |
|---|---|---|
| `batlog ui` | The ANSI styles above; the sidebar's bold `batlog` title and the window title, in the terminal font. No logo: the view uses Unicode box and block characters only ([F8](../specs/F8-ui.md#colour)). | `internal/ui/style.go` |
| Command output | Plain text, except that `batlog health` paints the condition red when the battery needs service. | `cmd/health.go` |
| README | The horizontal lockup as the heading, 280 px wide, alt text `batlog`. | `README.md` |
| Releases and Homebrew | Text only: the archives and the cask carry no image. | `.goreleaser.yaml` |
| The kit | Artwork, icons and the guide. | `brand/kit/` |

Colour in the terminal follows the user: `batlog ui` paints none when
`NO_COLOR` is set, `TERM=dumb` or the output is not a terminal, and
`batlog health` paints none when `NO_COLOR` is set or the output is not a
terminal. Without colour, layout and figures stay the same.

## White-label

batlog has no white-label mode and no brand packs. The name is the command's
name, so it is fixed in code (the command, the window and sidebar titles,
messages), and nothing reads it from configuration. The only theming is the
user's terminal theme, through the ANSI roles. A fork that renames batlog
replaces the tokens and the kit, the README's `<picture>`, and the command
name; the check then names every colour still left to map.

## How to change

**A terminal colour or style.** Change the token's value in
`brand/tokens.json`, then the style in `internal/ui/style.go` (or the escape
code in `cmd/health.go`); `make brand-check` names any place that still
differs. The golden screens are drawn without colour, so they do not change.
Look at `batlog ui` in a dark and a light terminal theme, and with
`NO_COLOR=1`.

**A new terminal style.** Add a token with its role under `terminal`, add the
field to `newStyles`, map it as `"newStyles.<field>"` under the `style.go`
surface, and paint with `st.paint`. Values are ANSI numbers (`ansi:N`,
`bg:ansi:N`) and `bold`, `faint`, `italic`, `underline`, `blink`, `reverse`
or `strike`, joined with `+`.

**A brand or guide colour.** Change the token's value. `make brand-check`
then fails because the kit was drawn with the old value; run `make brand-kit`,
render all seven guide pages and look at each, record the new PDF hash in
`brand/kit/reference/pdf-review.json`, run `make brand-kit` again (it verifies
the review) and `make brand-kit-check`, and commit the rebuilt kit with the
token.

**The logo artwork.** Edit `brand/kit/source/geometry.py`, then as above.
Never edit files in `brand/kit/svg`, `png` or `icons` by hand: `make
brand-check` fails on any file that no longer matches the kit manifest.

**The guide's text.** Edit `guide()` in `brand/kit/source/build_kit.py`, then
as above.

**The README logo.** Change the `<picture>` in `README.md`, and its two keys
under the `README.md` surface in `brand/tokens.json`. Keep it 180 px wide or
more.

**A new surface** (a website, a social image, a new file that paints).
Use the tokens' values, and add a surface entry for the file: `map` ties a
name in the file to a token, `allow` lists the tokens it may use anywhere
(`"*"` for any). A file with colour and no surface fails.

**An exception.** Add it to `unchecked` in `brand/tokens.json` with its
reason. There are exceptions only for tests, the check itself, and binary
files (PNG, PDF, ICO, ICNS, fonts, the supplied JPEGs), whose content the
manifest covers instead.

## Checks

**`make brand-check`** needs only Go, and runs in the `brand` workflow on
every push and pull request, Markdown-only ones included (the main `ci`
workflow skips those). It reads every file git would track (it skips
`.gitignore`d paths and dot-folders other than `.github` and `.claude`) and
fails when:

- a mapped name holds a value other than its token's (drift);
- a file paints a colour or style that maps to no token: a file with no
  surface, or a value outside its surface's `allow` list;
- a mapped name is no longer in its file, or a surface matches no file;
- a Markdown line names one token and shows a different value, as in the
  tables above;
- text under `brand/`, this page or the brand skill names a personal path,
  the kit's old working folder or the product's old name;
- `brand/kit` is stale: a file the builder reads or writes changed, appeared
  or went missing since the last `make brand-kit`, or a token it drew with has
  another value now;
- a binary file is not an explicit exception, or `tokens.json` has a token
  without a role or a mapping to a token that does not exist.

What it reads, by file type:

- **Go:** parsed, not grepped. lipgloss chains (`Color`, `Foreground`,
  `Background`, `Bold(true)` and the other attributes) and ANSI escape codes
  and hex in string literals. A value is named by where it sits:
  `newStyles.bat` for a struct field, `renderHealth.c` for an assignment.
- **SVG, CSS, HTML:** hex (3 to 8 digits), colour functions (`rgb()`, `hsl()`
  and the rest, which always fail because every token is hex), and named
  colours in paint attributes such as `fill`.
- **JSON:** colours in string values, named by their path (`theme_color`).
- **Markdown:** hex and escape codes, and the colours of each local SVG it
  shows with `<img>`, `<source>` or `![]()`.
- **Other text:** 6- and 8-digit hex and escape codes.

It does not see colours set through other libraries (batlog uses lipgloss and
raw escape codes only), or the colour a terminal theme gives an ANSI number.

**`go test ./brand/check`** (part of `make brand-check` and `go test ./...`)
plants each fault in a copy of the repo and expects the check to name it: a
changed terminal value, a changed escape code, an unmapped style, a colour (even a token's value) in
a file with no surface, a renamed style, a token without a role, docs drift, a personal
path, a binary file, recoloured artwork, a named colour or colour function in
artwork, a token changed without a rebuild, a hand-edited, extra or missing
kit file, the README's dark mode showing the light file, a recoloured
dark-mode wordmark, and the README losing its lockup.

**`make brand-kit-check`** needs Python 3.11+ and npm. It rebuilds the kit
into a temporary copy with pinned packages and fails unless every file comes
out byte for byte the same, then re-verifies the kit's structure and the PDF
review. It runs locally, not in CI; the byte-for-byte rebuild is verified on
macOS on Apple Silicon. Run it whenever `brand/kit` changes.

**`make test`** runs `make brand-check`, then `go vet`, `go test` and the
Linux cross-compile `go vet` that CI runs.

## History

The identity began as two concept images supplied by the owner
(`brand/kit/source/originals/`). The kit reconstructed the chosen one as
vectors and proposed the wordmark, accent and usage rules; its notes are in
`brand/kit/history/2026-11-09/`. batlog adopted it on 2026-11-10, with the
terminal keeping its ANSI colours and the user's font.
