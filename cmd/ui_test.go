package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jufianto/batlog/internal/ui"
)

// stubUI fakes the terminal check and captures what ui.Run gets.
func stubUI(t *testing.T, terminal bool) *ui.Source {
	t.Helper()
	oldTerm, oldRun := isTerminal, runUI
	var got ui.Source
	isTerminal = func() bool { return terminal }
	runUI = func(_ context.Context, src ui.Source, _ ui.Options, _ ...tea.ProgramOption) error {
		got = src
		return nil
	}
	t.Cleanup(func() { isTerminal, runUI = oldTerm, oldRun })
	return &got
}

func TestUIRejectsJSON(t *testing.T) {
	stubUI(t, true)
	_, err := run(t, "ui", "--json")
	var ue usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "ui is interactive") {
		t.Errorf("err = %v, want a usage error", err)
	}
}

func TestUINeedsATerminal(t *testing.T) {
	src := stubUI(t, false)
	_, err := run(t, "ui")
	var ue usageError
	if err == nil || err.Error() != "batlog ui needs a terminal" || errors.As(err, &ue) || *src != nil {
		t.Errorf("err = %v (exit 1 wanted), ran = %v", err, *src != nil)
	}
	src = stubUI(t, true)
	if _, err := run(t, "ui"); err != nil || *src == nil {
		t.Errorf("in a terminal: err = %v, ran = %v", err, *src != nil)
	}
}

// The UI's numbers are the CLI's: each Source method calls what its
// command calls, on the same fixture.
func TestUISourceMatchesTheCLI(t *testing.T) {
	f := stubTop(t, 719, dayEnergy...)
	seedHealth(t, f.db, [2]any{"2026-08-26", 4500}, [2]any{"2026-09-25", 4480})
	ctx, src, today := context.Background(), uiSource{}, ui.MakeRange(now(), false, 0)

	same := func(name, got, want string, err error) {
		t.Helper()
		if err != nil || got != want {
			t.Errorf("%s: %v\ngot:\n%s\nwant:\n%s", name, err, got, want)
		}
	}
	rep, err := src.Report(ctx, today)
	cli, _ := run(t, "report")
	same("report", rep, cli, err)

	sess, err := src.Session(ctx, "0926-0200")
	cli, _ = run(t, "top", "--session", "0926-0200")
	same("session", sess, cli, err)

	h, err := src.Health(ctx)
	cli, _ = run(t, "health", "--trend")
	_, cli, _ = strings.Cut(cli, "\n")
	same("health", h.Text, cli, err)
	if len(h.Rows) != 2 {
		t.Errorf("health rows = %v", h.Rows)
	}

	apps, err := src.Apps(ctx, today)
	cli, _ = run(t, "top", "--json")
	var top struct {
		Apps []struct {
			App   string  `json:"app"`
			Share float64 `json:"share"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(cli), &top); err != nil {
		t.Fatalf("%v: %s", err, cli)
	}
	if err != nil || len(apps.Rows) != len(top.Apps) {
		t.Fatalf("apps: %v, %d rows, top has %d", err, len(apps.Rows), len(top.Apps))
	}
	for i, r := range apps.Rows {
		if r.App != top.Apps[i].App || round4(r.Share) != top.Apps[i].Share {
			t.Errorf("app %d: %s %v, top says %s %v", i, r.App, r.Share, top.Apps[i].App, top.Apps[i].Share)
		}
	}

	hist, err := src.History(ctx, today)
	cli, _ = run(t, "history", "--json")
	for _, s := range hist.Result.Sessions {
		if !strings.Contains(cli, `"id":"`+s.ID+`"`) {
			t.Errorf("session %s is not in history --json", s.ID)
		}
	}
	if err != nil || len(hist.Samples) == 0 {
		t.Errorf("history: %v, %d samples", err, len(hist.Samples))
	}
}

// batlog ui only reads: going through every view leaves the database as
// it was.
func TestUIOnlyReads(t *testing.T) {
	f := stubTop(t, 719, dayEnergy...)
	before, err := os.Stat(f.db)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // a write now would move the mtime
	ui.Snapshot(context.Background(), uiSource{}, ui.Options{Now: now}, 120, 40,
		"enter", "esc", "down", "enter", "w", "[", "2", "enter", "3", "4", "t")
	after, err := os.Stat(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Errorf("database changed: %v %d → %v %d", before.ModTime(), before.Size(), after.ModTime(), after.Size())
	}
}

// Without the daemon's database every view still opens: the live reading
// in the sidebar and Health, "no history yet" in the others.
func TestUIWithoutHistory(t *testing.T) {
	stubHistory(t, 719, nil, nil, nil, nil)
	for keys, want := range map[string]string{
		"1": "no history yet — run 'batlog daemon install'",
		"2": "no app energy recorded yet — run 'batlog daemon install'",
		"3": "no history yet — run 'batlog daemon install'",
		"4": "no daily history yet",
	} {
		got := ui.Snapshot(context.Background(), uiSource{}, ui.Options{Now: now}, 120, 40, keys)
		if !strings.Contains(got, want) || !strings.Contains(got, "no history yet") {
			t.Errorf("view %s lacks %q:\n%s", keys, want, got)
		}
	}
}
