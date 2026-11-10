// Command check is batlog's brand check: brand/tokens.json is the one palette
// source, and every colour or terminal style a surface paints is a token.
//
// It fails (exit 1) when
//   - a surface's mapped name holds a value other than its token's (drift);
//   - a file paints a colour that maps to no token: a new style, escape code
//     or hex in a file with no surface entry, or a value outside a file's
//     allow list;
//   - a mapped name is no longer in its file, or a surface matches no file;
//   - a Markdown line names one token (`brand.ink`) next to a different value;
//   - brand text (brand/, the brand docs and skill) names a personal path or
//     the old product name;
//   - brand/kit is stale: a file the builder reads or writes changed, appeared
//     or went missing since the last make brand-kit, or a token it drew with
//     has another value now. (make brand-kit-check proves the rebuild itself.)
//
// Exceptions live in tokens.json "unchecked", each with its reason. It reads
// every file git would track: .gitignore'd paths are skipped. Standard library
// only, so CI needs nothing but Go. Usage: go run ./brand/check [-root DIR]
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func main() {
	root := flag.String("root", ".", "the repository root")
	flag.Parse()
	r, err := Run(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "brand check:", err)
		os.Exit(1)
	}
	if len(r.Problems) > 0 {
		for _, p := range r.Problems {
			fmt.Fprintln(os.Stderr, "brand check:", p)
		}
		fmt.Fprintf(os.Stderr, "brand check: %d problem(s); how to fix: docs/reference/brand.md\n", len(r.Problems))
		os.Exit(1)
	}
	fmt.Printf("brand check: ok, %d files read, %d colours and styles found, %d mapped to a named token\n",
		r.Files, r.Colours, r.Mapped)
}

// Token is one palette entry: a value and what it is for.
type Token struct {
	Value string `json:"value"`
	Role  string `json:"role"`
}

// Surface says how one file (or a glob of files) uses the tokens.
type Surface struct {
	About string            `json:"about"`
	Map   map[string]string `json:"map"`   // a name in the file -> token path
	Allow []string          `json:"allow"` // token paths the file may use anywhere; "*" is any token
}

// Unchecked is an explicit exception: files the check does not read for colour.
type Unchecked struct {
	Glob   string `json:"glob"`
	Reason string `json:"reason"`
}

type config struct {
	About     string                      `json:"about"`
	Tokens    map[string]map[string]Token `json:"tokens"`
	Surfaces  map[string]Surface          `json:"surfaces"`
	Unchecked []Unchecked                 `json:"unchecked"`
}

// Result is what one run found.
type Result struct {
	Problems []string
	Files    int
	Colours  int
	Mapped   int
}

// found is one colour or style in a file, under the name that holds it.
type found struct {
	key   string
	value string
	line  int
}

// TokensPath is where the palette lives, relative to the root.
const TokensPath = "brand/tokens.json"

// Run checks the repository at root.
func Run(root string) (Result, error) {
	var r Result
	cfg, err := loadConfig(filepath.Join(root, filepath.FromSlash(TokensPath)))
	if err != nil {
		return r, err
	}
	r.Problems = append(r.Problems, cfg.validate()...)
	values := cfg.values()

	files, err := walk(root)
	if err != nil {
		return r, err
	}
	surfaceHits := map[string]int{}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return r, err
		}
		r.Problems = append(r.Problems, guard(rel, data)...)
		if rel == TokensPath || cfg.unchecked(rel) { // the palette itself, or an exception
			continue
		}
		if isBinary(data) {
			r.Problems = append(r.Problems, fmt.Sprintf("%s: a binary file the check cannot read; add its type to \"unchecked\" in %s with a reason", rel, TokensPath))
			continue
		}
		r.Files++
		key, s, err := cfg.surface(rel)
		if err != nil {
			r.Problems = append(r.Problems, err.Error())
			continue
		}
		if key != "" {
			surfaceHits[key]++
		}
		got, err := extract(root, rel, data)
		if err != nil {
			r.Problems = append(r.Problems, err.Error())
			continue
		}
		r.Colours += len(got)
		seen := map[string]bool{}
		for _, f := range got {
			seen[f.key] = true
			where := fmt.Sprintf("%s:%d %s", rel, f.line, f.key)
			if tok, ok := s.Map[f.key]; ok {
				r.Mapped++
				if want := cfg.value(tok); want != f.value {
					r.Problems = append(r.Problems, fmt.Sprintf("%s is %s, but its token %s is %s (drift)", where, f.value, tok, want))
				}
				continue
			}
			if s.allows(cfg, f.value, values) {
				continue
			}
			if key == "" {
				r.Problems = append(r.Problems, fmt.Sprintf("%s paints %s, which maps to no token: this file has no surface in %s", where, f.value, TokensPath))
			} else {
				r.Problems = append(r.Problems, fmt.Sprintf("%s paints %s, which maps to no token in its surface %q", where, f.value, key))
			}
		}
		for _, name := range sortedKeys(s.Map) {
			if !seen[name] {
				r.Problems = append(r.Problems, fmt.Sprintf("%s: mapped name %q (token %s) is no longer in the file; update %s", rel, name, s.Map[name], TokensPath))
			}
		}
		if strings.HasSuffix(rel, ".md") {
			r.Problems = append(r.Problems, docsDrift(rel, data, cfg)...)
		}
	}
	r.Problems = append(r.Problems, kitStale(root, cfg, files)...)
	for _, key := range sortedKeys(cfg.Surfaces) {
		if surfaceHits[key] == 0 {
			r.Problems = append(r.Problems, fmt.Sprintf("%s: surface %q matches no file", TokensPath, key))
		}
	}
	return r, nil
}

func loadConfig(p string) (*config, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", TokensPath, err)
	}
	for _, toks := range c.Tokens {
		for n, t := range toks {
			if v, ok := normalize(t.Value); ok {
				t.Value = v
				toks[n] = t
			}
		}
	}
	return &c, nil
}

// validate checks tokens.json itself: every token has a valid value and a
// role, every map and allow entry names a real token, every exception a reason.
func (c *config) validate() []string {
	var out []string
	for _, g := range sortedKeys(c.Tokens) {
		for _, n := range sortedKeys(c.Tokens[g]) {
			t := c.Tokens[g][n]
			if _, ok := normalize(t.Value); !ok {
				out = append(out, fmt.Sprintf("%s: token %s.%s has value %q, which is not a colour or terminal style", TokensPath, g, n, t.Value))
			}
			if strings.TrimSpace(t.Role) == "" {
				out = append(out, fmt.Sprintf("%s: token %s.%s has no role", TokensPath, g, n))
			}
		}
	}
	for _, key := range sortedKeys(c.Surfaces) {
		s := c.Surfaces[key]
		for _, name := range sortedKeys(s.Map) {
			if c.value(s.Map[name]) == "" {
				out = append(out, fmt.Sprintf("%s: surface %q maps %q to %q, which is not a token", TokensPath, key, name, s.Map[name]))
			}
		}
		for _, a := range s.Allow {
			if a != "*" && c.value(a) == "" {
				out = append(out, fmt.Sprintf("%s: surface %q allows %q, which is not a token", TokensPath, key, a))
			}
		}
	}
	for _, u := range c.Unchecked {
		if strings.TrimSpace(u.Reason) == "" {
			out = append(out, fmt.Sprintf("%s: unchecked %q has no reason", TokensPath, u.Glob))
		}
	}
	return out
}

// value is a token path's normalized value, or "" when there is no such token.
func (c *config) value(p string) string {
	g, n, ok := strings.Cut(p, ".")
	if !ok {
		return ""
	}
	t, ok := c.Tokens[g][n]
	if !ok {
		return ""
	}
	return t.Value
}

// values is every token value, for allow "*".
func (c *config) values() map[string]bool {
	out := map[string]bool{}
	for _, toks := range c.Tokens {
		for _, t := range toks {
			out[t.Value] = true
		}
	}
	return out
}

func (c *config) unchecked(rel string) bool {
	for _, u := range c.Unchecked {
		if globMatch(u.Glob, rel) {
			return true
		}
	}
	return false
}

// surface is the one surface entry that covers rel, or none.
func (c *config) surface(rel string) (string, Surface, error) {
	var keys []string
	for k := range c.Surfaces {
		if globMatch(k, rel) {
			keys = append(keys, k)
		}
	}
	switch len(keys) {
	case 0:
		return "", Surface{}, nil
	case 1:
		return keys[0], c.Surfaces[keys[0]], nil
	}
	sort.Strings(keys)
	return "", Surface{}, fmt.Errorf("%s: matches more than one surface in %s: %s", rel, TokensPath, strings.Join(keys, ", "))
}

func (s Surface) allows(c *config, v string, all map[string]bool) bool {
	for _, a := range s.Allow {
		if a == "*" && all[v] || c.value(a) == v {
			return true
		}
	}
	return false
}

// --- values -----------------------------------------------------------------

var attrs = []string{"blink", "bold", "faint", "italic", "reverse", "strike", "underline"}

// normalize turns a value into its canonical form: lowercase 6-digit hex
// (8 when it has alpha), or terminal parts "ansi:6", "bg:ansi:4", "bold"...
// sorted and joined with "+". It reports false for anything else.
func normalize(v string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(v), "+")
	var out []string
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		bg := strings.HasPrefix(p, "bg:")
		q := strings.TrimPrefix(p, "bg:")
		switch {
		case strings.HasPrefix(q, "#"):
			h, ok := hexNorm(q[1:], true)
			if !ok {
				return "", false
			}
			q = h
		case strings.HasPrefix(q, "ansi:"):
			n, err := strconv.Atoi(q[5:])
			if err != nil || n < 0 || n > 255 {
				return "", false
			}
			q = "ansi:" + strconv.Itoa(n)
		case !bg && slices.Contains(attrs, q):
		default:
			return "", false
		}
		if bg {
			q = "bg:" + q
		}
		out = append(out, q)
	}
	sort.Strings(out)
	out = slices.Compact(out)
	return strings.Join(out, "+"), len(out) > 0
}

// hexNorm expands and lowercases hex digits; short forms only when allowed.
func hexNorm(h string, short bool) (string, bool) {
	h = strings.ToLower(h)
	switch len(h) {
	case 3, 4:
		if !short {
			return "", false
		}
		var b strings.Builder
		for _, c := range h {
			b.WriteRune(c)
			b.WriteRune(c)
		}
		h = b.String()
	case 6, 8:
	default:
		return "", false
	}
	for _, c := range h {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", false
		}
	}
	if len(h) == 8 && h[6:] == "ff" {
		h = h[:6]
	}
	return "#" + h, true
}

var hexRun = regexp.MustCompile(`#[0-9A-Fa-f]+`)

// hexes finds hex colours in text. A run glued to a word, an entity or a path
// ("batlog#33", "&#123;", "/#add") is not a colour; 3- and 4-digit forms
// count only where short hex is normal (SVG, CSS, HTML).
func hexes(s string, short bool) []string {
	var out []string
	for _, m := range hexRun.FindAllStringIndex(s, -1) {
		if m[0] > 0 {
			c := s[m[0]-1]
			if isWord(c) || c == '&' || c == '/' {
				continue
			}
		}
		if m[1] < len(s) && (isWord(s[m[1]]) || s[m[1]] == '-') {
			continue
		}
		if h, ok := hexNorm(s[m[0]+1:m[1]], short); ok {
			out = append(out, h)
		}
	}
	return out
}

func isWord(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

var sgr = regexp.MustCompile("\x1b\\[([0-9;]*)m")

// escapes reads ANSI SGR escape codes ("\x1b[31m") into terminal parts.
// Resets (0, 22-29, 39, 49) paint nothing and are skipped.
func escapes(s string) []string {
	var out []string
	for _, m := range sgr.FindAllStringSubmatch(s, -1) {
		ps := strings.Split(m[1], ";")
		for i := 0; i < len(ps); i++ {
			n, err := strconv.Atoi(ps[i])
			if err != nil {
				continue
			}
			bg := ""
			switch {
			case n == 1:
				out = append(out, "bold")
			case n == 2:
				out = append(out, "faint")
			case n == 3:
				out = append(out, "italic")
			case n == 4:
				out = append(out, "underline")
			case n == 5:
				out = append(out, "blink")
			case n == 7:
				out = append(out, "reverse")
			case n == 9:
				out = append(out, "strike")
			case n >= 30 && n <= 37:
				out = append(out, "ansi:"+strconv.Itoa(n-30))
			case n >= 40 && n <= 47:
				out = append(out, "bg:ansi:"+strconv.Itoa(n-40))
			case n >= 90 && n <= 97:
				out = append(out, "ansi:"+strconv.Itoa(n-90+8))
			case n >= 100 && n <= 107:
				out = append(out, "bg:ansi:"+strconv.Itoa(n-100+8))
			case n == 48:
				bg = "bg:"
				fallthrough
			case n == 38:
				if i+2 < len(ps) && ps[i+1] == "5" {
					out = append(out, bg+"ansi:"+ps[i+2])
					i += 2
				} else if i+4 < len(ps) && ps[i+1] == "2" {
					r, _ := strconv.Atoi(ps[i+2])
					g, _ := strconv.Atoi(ps[i+3])
					b, _ := strconv.Atoi(ps[i+4])
					out = append(out, fmt.Sprintf("%s#%02x%02x%02x", bg, r, g, b))
					i += 4
				}
			}
		}
	}
	return out
}

// textColours is every colour in a piece of text: escape codes and hex.
func textColours(s string, short bool) []string {
	return append(escapes(s), hexes(s, short)...)
}

// join is several parts as one value; "" when there are none.
func join(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	v, ok := normalize(strings.Join(parts, "+"))
	if !ok {
		return strings.Join(parts, "+")
	}
	return v
}

// --- extraction -------------------------------------------------------------

func extract(root, rel string, data []byte) ([]found, error) {
	switch ext := strings.ToLower(path.Ext(rel)); ext {
	case ".go":
		return goColours(rel, data)
	case ".svg", ".css", ".html":
		return markupColours(string(data)), nil
	case ".json", ".webmanifest":
		return jsonColours(rel, data)
	case ".md":
		out := lineColours(string(data), false)
		imgs, err := imageColours(root, rel, string(data))
		return append(out, imgs...), err
	default:
		return lineColours(string(data), false), nil
	}
}

// lineColours names each colour by its line: such files are checked by allow
// lists, not maps.
func lineColours(s string, short bool) []found {
	var out []found
	for i, line := range strings.Split(s, "\n") {
		for _, v := range textColours(line, short) {
			out = append(out, found{key: "line " + strconv.Itoa(i+1), value: v, line: i + 1})
		}
	}
	return out
}

var (
	paintAttr = regexp.MustCompile(`(?i)\b(fill|stroke|stop-color|flood-color|lighting-color|color)\s*(=\s*"([^"]*)"|:\s*([^;"'>]+))`)
	namedOK   = map[string]string{"white": "#ffffff", "black": "#000000"}
)

// markupColours reads hex anywhere, and named colours in paint attributes
// (fill="white"), which must also be tokens.
func markupColours(s string) []found {
	out := lineColours(s, true)
	for i, line := range strings.Split(s, "\n") {
		for _, m := range paintAttr.FindAllStringSubmatch(line, -1) {
			v := strings.TrimSpace(m[3] + m[4])
			lv := strings.ToLower(v)
			switch {
			case lv == "", strings.HasPrefix(lv, "#"), strings.HasPrefix(lv, "url("), strings.HasPrefix(lv, "var("),
				lv == "none", lv == "currentcolor", lv == "transparent", lv == "inherit":
				continue
			}
			if h, ok := namedOK[lv]; ok {
				v = h
			}
			out = append(out, found{key: strings.ToLower(m[1]), value: v, line: i + 1})
		}
	}
	return out
}

// jsonColours names each colour by its JSON path ("theme_color", "a.b[2]").
func jsonColours(rel string, data []byte) ([]found, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	var out []found
	var visit func(p string, v any)
	visit = func(p string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for _, k := range sortedKeys(v) {
				q := k
				if p != "" {
					q = p + "." + k
				}
				visit(q, v[k])
			}
		case []any:
			for i, e := range v {
				visit(fmt.Sprintf("%s[%d]", p, i), e)
			}
		case string:
			for _, c := range textColours(v, false) {
				out = append(out, found{key: p, value: c})
			}
		}
	}
	visit("", v)
	return out, nil
}

var (
	htmlImg = regexp.MustCompile(`(?is)<(source|img)\b([^>]*)>`)
	mdImg   = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)`)
	attrRe  = regexp.MustCompile(`(?is)\b(media|srcset|src)\s*=\s*"([^"]*)"`)
)

// imageColours is the colours of each local SVG a Markdown file shows: a
// <picture>'s <source media="..."> is "source[<media>]", an <img> or ![]()
// is "img". GitHub cannot tint an image, so the file's colours are what the
// page paints.
func imageColours(root, rel, s string) ([]found, error) {
	var out []found
	count := map[string]int{}
	add := func(key, src string, line int) error {
		if strings.Contains(src, "://") || !strings.HasSuffix(strings.ToLower(src), ".svg") {
			return nil
		}
		p := path.Join(path.Dir(rel), src)
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return fmt.Errorf("%s:%d shows %s, which is not there", rel, line, src)
		}
		var vs []string
		for _, f := range markupColours(string(data)) {
			vs = append(vs, f.value)
		}
		sort.Strings(vs)
		vs = slices.Compact(vs)
		count[key]++
		if count[key] > 1 {
			key = fmt.Sprintf("%s#%d", key, count[key])
		}
		out = append(out, found{key: key, value: strings.Join(vs, " and "), line: line})
		return nil
	}
	for i, line := range strings.Split(s, "\n") {
		for _, m := range htmlImg.FindAllStringSubmatch(line, -1) {
			a := map[string]string{}
			for _, am := range attrRe.FindAllStringSubmatch(m[2], -1) {
				a[strings.ToLower(am[1])] = am[2]
			}
			key, src := "img", a["src"]
			if strings.EqualFold(m[1], "source") {
				key, src = "source["+a["media"]+"]", strings.Fields(a["srcset"] + " ")[0]
			}
			if err := add(key, src, i+1); err != nil {
				return nil, err
			}
		}
		for _, m := range mdImg.FindAllStringSubmatch(line, -1) {
			if err := add("img", m[1], i+1); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// goColours reads a Go file's colours and styles by name. A composite-literal
// field is "func.field" (newStyles.bat), an assignment "func.var", a
// package-level var or const its name; anything else the enclosing function,
// numbered when it has several ("render#2"). A lipgloss chain is one value:
// Foreground(Color("6")) is ansi:6, Faint(true) is faint, Background adds bg:.
func goColours(rel string, src []byte) ([]found, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []found
	count := map[string]int{}
	add := func(key string, parts []string, pos token.Pos) {
		v := join(parts)
		if v == "" {
			return
		}
		count[key]++
		if count[key] > 1 {
			key = fmt.Sprintf("%s#%d", key, count[key])
		}
		out = append(out, found{key: key, value: v, line: fset.Position(pos).Line})
	}
	var walkBody func(n ast.Node, ctx string)
	walkBody = func(n ast.Node, ctx string) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.KeyValueExpr:
				if id, ok := n.Key.(*ast.Ident); ok {
					if p := goParts(n.Value); len(p) > 0 {
						add(ctx+"."+id.Name, p, n.Pos())
						return false
					}
				}
			case *ast.AssignStmt:
				if len(n.Lhs) == len(n.Rhs) {
					done := false
					for i := range n.Lhs {
						if id, ok := n.Lhs[i].(*ast.Ident); ok {
							if p := goParts(n.Rhs[i]); len(p) > 0 {
								add(ctx+"."+id.Name, p, n.Pos())
								done = true
							}
						}
					}
					if done {
						return false
					}
				}
			case *ast.ValueSpec:
				done := false
				for i, id := range n.Names {
					if i < len(n.Values) {
						if p := goParts(n.Values[i]); len(p) > 0 {
							add(ctx+"."+id.Name, p, n.Pos())
							done = true
						}
					}
				}
				if done {
					return false
				}
			case *ast.CallExpr, *ast.BasicLit:
				if p := goParts(n); len(p) > 0 {
					add(ctx, p, n.Pos())
					return false
				}
			}
			return true
		})
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name = recvName(d.Recv.List[0].Type) + "." + name
			}
			if d.Body != nil {
				walkBody(d.Body, name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					if i < len(vs.Values) {
						add(id.Name, goParts(vs.Values[i]), vs.Pos())
					}
				}
			}
		}
	}
	return out, nil
}

func recvName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return recvName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		return recvName(e.X)
	}
	return "?"
}

// goParts is every colour and style part in an expression; a function
// literal is its own scope and is skipped.
func goParts(n ast.Node) []string {
	var out []string
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch name := sel.Sel.Name; name {
			case "Color", "ANSIColor":
				if len(n.Args) == 1 {
					if v, ok := goColourArg(n.Args[0]); ok {
						out = append(out, v)
						return false
					}
				}
			case "Background", "BorderBackground":
				out = append(out, goParts(sel.X)...)
				for _, a := range n.Args {
					for _, p := range goParts(a) {
						out = append(out, "bg:"+strings.TrimPrefix(p, "bg:"))
					}
				}
				return false
			case "Bold", "Faint", "Italic", "Underline", "Blink", "Reverse", "Strikethrough":
				if len(n.Args) == 1 {
					if id, ok := n.Args[0].(*ast.Ident); ok && id.Name == "true" {
						a := strings.ToLower(name)
						if a == "strikethrough" {
							a = "strike"
						}
						out = append(out, a)
					}
				}
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				if s, err := strconv.Unquote(n.Value); err == nil {
					out = append(out, textColours(s, false)...)
				}
			}
		}
		return true
	})
	return out
}

// goColourArg reads lipgloss.Color's argument: "6" or 6 is ANSI 6, "#0891B2"
// is hex; anything else is reported as it is, and maps to no token.
func goColourArg(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok {
		return "", false
	}
	s := lit.Value
	if lit.Kind == token.STRING {
		u, err := strconv.Unquote(s)
		if err != nil {
			return "", false
		}
		s = u
	}
	if n, err := strconv.Atoi(s); err == nil {
		return "ansi:" + strconv.Itoa(n), true
	}
	if strings.HasPrefix(s, "#") {
		if h, ok := hexNorm(s[1:], true); ok {
			return h, true
		}
	}
	return "colour " + strconv.Quote(s), true
}

// --- docs -------------------------------------------------------------------

var (
	tokenRef  = regexp.MustCompile("`([a-z]+)\\.([a-z0-9-]+)`")
	backticks = regexp.MustCompile("`([^`]+)`")
)

// docsDrift: a Markdown line that names exactly one token, like a palette
// table row, must not show a different value for it.
func docsDrift(rel string, data []byte, c *config) []string {
	var out []string
	for i, line := range strings.Split(string(data), "\n") {
		var refs []string
		for _, m := range tokenRef.FindAllStringSubmatch(line, -1) {
			if _, ok := c.Tokens[m[1]][m[2]]; ok {
				refs = append(refs, m[1]+"."+m[2])
			}
		}
		refs = slices.Compact(refs)
		if len(refs) != 1 {
			continue
		}
		want := c.value(refs[0])
		vals := textColours(line, false)
		for _, m := range backticks.FindAllStringSubmatch(line, -1) {
			if v, ok := normalize(m[1]); ok && !strings.HasPrefix(v, "#") {
				vals = append(vals, v)
			}
		}
		for _, v := range vals {
			if v != want {
				out = append(out, fmt.Sprintf("%s:%d names %s but shows %s; the token is %s (drift)", rel, i+1, refs[0], v, want))
			}
		}
	}
	return out
}

// --- guard ------------------------------------------------------------------

// Brand text must not point at the owner's machine or the kit's old working
// folder, or use the product's old name. Built from parts so the check does
// not match itself.
var (
	guardWords = []string{"/" + "Users/", "~/" + "projects", "logo" + "-branding", "mac" + "bat"}
	guardScope = []string{"brand/**", "docs/reference/brand.md", ".claude/skills/**"}
	guardExts  = []string{".md", ".json", ".py", ".mjs", ".txt", ".svg", ".go", ".webmanifest", ".toml"}
)

func guard(rel string, data []byte) []string {
	in := false
	for _, g := range guardScope {
		if globMatch(g, rel) {
			in = true
		}
	}
	if !in || !slices.Contains(guardExts, strings.ToLower(path.Ext(rel))) {
		return nil
	}
	var out []string
	low := strings.ToLower(string(data))
	for _, w := range guardWords {
		if i := strings.Index(low, strings.ToLower(w)); i >= 0 {
			line := strings.Count(low[:i], "\n") + 1
			out = append(out, fmt.Sprintf("%s:%d names %q: brand text must not carry personal paths or the old product name", rel, line, w))
		}
	}
	return out
}

// --- kit manifest -----------------------------------------------------------

const kitDir = "brand/kit/"

// kitUnmanaged are the hand-written files in brand/kit: the builder neither
// reads nor writes them. build_kit.py's UNMANAGED is the same list.
var kitUnmanaged = []string{"README.md", "manifest.json", "history/", "reference/pdf-review.json", "reference/validation.json"}

func kitManaged(rel string) bool {
	for _, u := range kitUnmanaged {
		if rel == u || strings.HasSuffix(u, "/") && strings.HasPrefix(rel, u) {
			return false
		}
	}
	return true
}

// kitStale compares brand/kit with the manifest the builder last wrote. It
// needs no Python: it only hashes files and compares token values.
func kitStale(root string, cfg *config, files []string) []string {
	if _, err := os.Stat(filepath.Join(root, "brand", "kit", "source", "build_kit.py")); err != nil {
		return nil // no builder, so no manifest to keep
	}
	data, err := os.ReadFile(filepath.Join(root, "brand", "kit", "manifest.json"))
	if err != nil {
		return []string{"brand/kit/manifest.json is missing; run make brand-kit"}
	}
	var m struct {
		Tokens map[string]string `json:"tokens"`
		Files  map[string]struct {
			Sha256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return []string{"brand/kit/manifest.json: " + err.Error()}
	}
	var out []string
	for _, tok := range sortedKeys(m.Tokens) {
		was, _ := normalize(m.Tokens[tok])
		if now := cfg.value(tok); now != was {
			out = append(out, fmt.Sprintf("brand/kit was built with %s %s, but the token is now %q; run make brand-kit", tok, was, now))
		}
	}
	seen := map[string]bool{}
	for _, rel := range files {
		k, ok := strings.CutPrefix(rel, kitDir)
		if !ok || !kitManaged(k) {
			continue
		}
		seen[k] = true
		want, ok := m.Files[k]
		if !ok {
			out = append(out, fmt.Sprintf("%s is not in the kit manifest: the builder did not make it; run make brand-kit, or remove it", rel))
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			out = append(out, err.Error())
			continue
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != want.Sha256 || int64(len(data)) != want.Bytes {
			out = append(out, fmt.Sprintf("%s changed since the last make brand-kit; rebuild it rather than edit it", rel))
		}
	}
	for _, k := range sortedKeys(m.Files) {
		if !seen[k] {
			out = append(out, fmt.Sprintf("%s%s is in the kit manifest but missing; run make brand-kit", kitDir, k))
		}
	}
	return out
}

// --- files ------------------------------------------------------------------

// walk lists every file under root that git would track: dot-directories
// other than .github and .claude, and anything .gitignore matches, are skipped.
func walk(root string) ([]string, error) {
	ign, err := readIgnore(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil, err
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && d.Name() != ".github" && d.Name() != ".claude" || ign.match(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if ign.match(rel, false) || !d.Type().IsRegular() {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

type ignore []struct {
	pat      string
	anchored bool
	dirOnly  bool
}

// readIgnore reads the simple .gitignore forms this repo uses: globs, a
// leading "/" to anchor, a trailing "/" for directories. No negation.
func readIgnore(p string) (ignore, error) {
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var ig ignore
	for l := range strings.SplitSeq(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "!") {
			continue
		}
		e := struct {
			pat      string
			anchored bool
			dirOnly  bool
		}{pat: l}
		if p, ok := strings.CutSuffix(e.pat, "/"); ok {
			e.dirOnly, e.pat = true, p
		}
		if strings.HasPrefix(e.pat, "/") || strings.Contains(e.pat, "/") {
			e.anchored, e.pat = true, strings.TrimPrefix(e.pat, "/")
		}
		ig = append(ig, e)
	}
	return ig, nil
}

func (ig ignore) match(rel string, dir bool) bool {
	for _, e := range ig {
		if e.dirOnly && !dir {
			continue
		}
		if e.anchored {
			if ok, _ := path.Match(e.pat, rel); ok {
				return true
			}
		} else if ok, _ := path.Match(e.pat, path.Base(rel)); ok {
			return true
		}
	}
	return false
}

// globMatch matches a slash path against a glob where ** spans directories.
func globMatch(glob, rel string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; {
		case strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	ok, _ := regexp.MatchString(b.String(), rel)
	return ok
}

func isBinary(data []byte) bool {
	n := min(len(data), 8000)
	return slices.Contains(data[:n], 0)
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
