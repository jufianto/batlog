---
name: batlog-brand
description: batlog's brand rules as recipes. Use for any change to how batlog looks (batlog ui styles, colour in command output, the README heading, a logo or icon placed anywhere, the brand guide or kit), and before adding a colour or escape code, showing the logo or the product name, or touching brand/, internal/ui/style.go or cmd/health.go's colour.
---

# batlog brand

The rules and the reasons are in `docs/reference/brand.md`; read it before
changing anything visual. This skill gives the recipe for each surface and the
definition of done.

## Every change

- **One palette source.** Every colour and terminal style is a token in
  `brand/tokens.json`, with its role, and the name that uses it is mapped
  under `surfaces`. `make brand-check` names anything missed.
- **The terminal keeps the user's theme and font.** Paint ANSI colour
  numbers (`ansi:N` in tokens), `faint`, `bold` and `reverse`; never a fixed
  hex, and never Signal Cyan (`brand.cyan`). Nothing in batlog asks for a font.
- **The logo is a file, never typed.** The wordmark is outlined lettering in
  the SVGs; the name typed in a font is text, not the logo. Lowercase
  `batlog` everywhere.
- **The mark is not a gauge.** Never recolour it to show a battery level,
  charging or a warning.

## Recipes

**batlog ui: a new style or colour.** Add a token under `terminal` with its
role (for example `ansi:5`, or `bold+ansi:6`). Add the field to `newStyles` in
`internal/ui/style.go` with `lipgloss.NewStyle()...`, map it in tokens.json as
`"newStyles.<field>"`, and paint with `m.st.paint(m.st.<field>, text)` so it
stays plain under `NO_COLOR`, `TERM=dumb` or no terminal. Keep the layout the
same with and without colour; the golden screens are drawn without colour.

**batlog ui: the product name.** The sidebar box title and the window title
are the text `batlog`, bold in the terminal font. Terminals show no images and
F8 allows Unicode box and block characters only, so there is no logo in the
UI, and the logo size rules do not apply to it.

**Command output: colour.** Prefer no colour. If a command must colour
something, reuse a role's ANSI number (red is `terminal.problem`, as in
`renderHealth` in `cmd/health.go`), colour only when `wantColor` says so, and
map the escape code's name in tokens.json (`"renderHealth.c"`). A test that
asserts escape codes lives in a `_test.go` file, which the check skips.

**README or docs: show the logo.** Use the horizontal lockup in a
`<picture>`, white for dark mode and ink otherwise, as `README.md` does:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="brand/kit/svg/batlog-horizontal-white.svg">
  <img src="brand/kit/svg/batlog-horizontal-ink.svg" alt="batlog" width="280">
</picture>
```

GitHub cannot tint an image, so each colour is its own baked file, and the
check reads the colours of every local SVG a Markdown file shows. Map the keys
(`source[(prefers-color-scheme: dark)]`, `img`) to tokens under that file's
surface, or give the file `"allow": ["*"]`. Keep a lockup 180 px wide or
more and a mark 48 px or more (16-32 px: the micro mark), with a quarter of
the battery's height clear around it. A label next to it is ordinary text.

**Social image, release page, avatar.** Use `brand/kit/icons/batlog-app-*`
tiles for square spaces and the `png/` lockups elsewhere. A new image that
the repo should keep is built by `brand/kit/source/build_kit.py` from the
tokens, not drawn by hand.

**Artwork, the guide or a brand colour.** Edit `brand/kit/source/geometry.py`
(the symbol), `build_kit.py` (lockups, sheets, guide text) or the token value,
then run `make brand-kit`. It fails until the PDF is reviewed: render all
seven pages (`pdftoppm -r 100 -png brand/kit/batlog-brand-guide.pdf <dir>/p`),
look at each, record the new hash and pages in
`brand/kit/reference/pdf-review.json`, run `make brand-kit` again, then
`make brand-kit-check`. Commit the rebuilt files with the change. Never edit
`brand/kit/svg`, `png` or `icons` by hand.

**Text in brand files.** Nothing under `brand/`, in `docs/reference/brand.md`
or in this skill may name a personal path, the kit's old working folder or
the product's old name; the check fails on them.

## Definition of done (brand)

- [ ] `make brand-check` passes (also run by `make test` and by the `brand`
      CI workflow on every pull request).
- [ ] A new colour or style is a token with a role, and the name that uses it
      is mapped; no colour sits in a file without a surface.
- [ ] If a token value, `brand/kit/source` or anything the kit builds changed:
      `make brand-kit` was run, all seven guide pages were looked at and
      `pdf-review.json` updated, `make brand-kit-check` passes, and the
      rebuilt files are committed with the change.
- [ ] `batlog ui` changes: `go test ./internal/ui/` passes (goldens updated
      with `-update` only for an intended layout change), and the view was
      looked at in a dark and a light terminal theme and with `NO_COLOR=1`.
- [ ] A placed logo is a kit file at or above its minimum size, in the right
      colour for its background; the name is never typed as the logo.
- [ ] `docs/reference/brand.md` still tells the truth; it is updated in the
      same commit when a rule, a token or a file moves.
