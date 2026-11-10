package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the repository this package lives in.
const repoRoot = "../.."

// copyRepo copies every file the check would read into a temporary root, so
// a mutation never touches the working tree.
func copyRepo(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	files, err := walk(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatal(err)
		}
		write(t, dst, rel, string(data))
	}
	return dst
}

func write(t *testing.T, root, rel, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// edit replaces old with new exactly once in a file, failing when old is not
// there: a mutation that silently does nothing would pass for the wrong reason.
func edit(t *testing.T, root, rel, old, new string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), old) != 1 {
		t.Fatalf("%s: want exactly one %q to mutate", rel, old)
	}
	write(t, root, rel, strings.Replace(string(data), old, new, 1))
}

func problems(t *testing.T, root string) []string {
	t.Helper()
	r, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	return r.Problems
}

func TestRepoPasses(t *testing.T) {
	if ps := problems(t, repoRoot); len(ps) > 0 {
		t.Fatalf("the repository fails its own brand check:\n%s", strings.Join(ps, "\n"))
	}
}

// Each mutation plants one fault in a copy of the repository; the check must
// fail and name it.
func TestMutationsFail(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, root string)
		want   string
	}{
		{"a changed terminal value drifts from its token",
			func(t *testing.T, root string) {
				edit(t, root, "internal/ui/style.go", `Foreground(lipgloss.Color("6"))`, `Foreground(lipgloss.Color("4"))`)
			},
			"newStyles.bat is ansi:4, but its token terminal.battery is ansi:6 (drift)"},
		{"the CLI's escape code drifts from its token",
			func(t *testing.T, root string) {
				edit(t, root, "cmd/health.go", `"\x1b[31m"`, `"\x1b[35m"`)
			},
			"renderHealth.c is ansi:5, but its token terminal.problem is ansi:1 (drift)"},
		{"an unmapped new style in a surface has no token",
			func(t *testing.T, root string) {
				edit(t, root, "internal/ui/style.go", "func newStyles(", "var accent = lipgloss.NewStyle().Foreground(lipgloss.Color(\"#FF00FF\"))\n\nfunc newStyles(")
			},
			"accent paints #ff00ff, which maps to no token"},
		{"a colour in a file with no surface has no token",
			func(t *testing.T, root string) {
				write(t, root, "internal/fresh/fresh.go", "package fresh\n\nconst green = \"\\x1b[32m\"\n")
			},
			"internal/fresh/fresh.go:3 green paints ansi:2, which maps to no token: this file has no surface"},
		{"a renamed style loses its mapping",
			func(t *testing.T, root string) {
				edit(t, root, "internal/ui/style.go", "\t\tsel:    lipgloss", "\t\tpick:   lipgloss")
			},
			`mapped name "newStyles.sel" (token terminal.selected) is no longer in the file`},
		{"a token needs a role",
			func(t *testing.T, root string) {
				edit(t, root, TokensPath, `"role": "The strict one-colour variant, for print and stamps."`, `"role": ""`)
			},
			"token brand.black has no role"},
		{"a docs line shows a different value for the token it names",
			func(t *testing.T, root string) {
				write(t, root, "docs/palette.md", "| Ink | `#111111` | `brand.ink` |\n")
			},
			"docs/palette.md:1 names brand.ink but shows #111111; the token is #242021 (drift)"},
		{"brand text must not carry a personal path",
			func(t *testing.T, root string) {
				write(t, root, "brand/notes.md", "built in /"+"Users/someone/kit\n")
			},
			"brand/notes.md:1 names"},
		{"baked kit artwork drifts from its token",
			func(t *testing.T, root string) {
				edit(t, root, "brand/kit/svg/batlog-mark-white.svg", `fill="#FFFFFF"`, `fill="#F5F5F5"`)
			},
			`brand/kit/svg/batlog-mark-white.svg:1 line 1 paints #f5f5f5, which maps to no token in its surface "brand/kit/svg/*-white.svg"`},
		{"a named colour in artwork must be a token too",
			func(t *testing.T, root string) {
				edit(t, root, "brand/kit/svg/batlog-mark-cyan.svg", `fill="#0891B2"`, `fill="teal"`)
			},
			"paints teal, which maps to no token"},
		{"a binary file needs an explicit exception",
			func(t *testing.T, root string) {
				write(t, root, "brand/blob.bin", "\x00\x01")
			},
			"brand/blob.bin: a binary file the check cannot read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := copyRepo(t)
			c.mutate(t, root)
			ps := problems(t, root)
			for _, p := range ps {
				if strings.Contains(p, c.want) {
					return
				}
			}
			t.Fatalf("want a problem containing %q, got:\n%s", c.want, strings.Join(ps, "\n"))
		})
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"#FFF":            "#ffffff",
		"#0891B2ff":       "#0891b2",
		"ansi:06":         "ansi:6",
		"bold+ansi:6":     "ansi:6+bold",
		"bg:#000+reverse": "bg:#000000+reverse",
	} {
		if got, ok := normalize(in); !ok || got != want {
			t.Errorf("normalize(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"red", "#12", "ansi:x", "bg:bold"} {
		if _, ok := normalize(bad); ok {
			t.Errorf("normalize(%q) is valid; want invalid", bad)
		}
	}
}

func TestEscapes(t *testing.T) {
	got := strings.Join(escapes("\x1b[1;31m x \x1b[0m \x1b[38;5;208m \x1b[48;2;8;145;178m \x1b[92m"), " ")
	want := "bold ansi:1 ansi:208 bg:#0891b2 ansi:10"
	if got != want {
		t.Fatalf("escapes = %q, want %q", got, want)
	}
}

func TestHexesSkipsReferencesAndFragments(t *testing.T) {
	got := hexes("batlog#333333 &#123456; /#abcdef (#deadbeef) #0891B2 #abc", false)
	if strings.Join(got, " ") != "#deadbeef #0891b2" {
		t.Fatalf("hexes = %q", got)
	}
}
