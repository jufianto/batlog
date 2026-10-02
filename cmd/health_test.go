package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/store"
)

func hip(v int) *int         { return &v }
func hfp(v float64) *float64 { return &v }

// The real Mac16,8 fixture's values.
var macHealth = battery.Health{Cycles: hip(388), DesignMAh: hip(6249), RawMaxMAh: hip(5424), NominalMAh: hip(5576),
	TempC: hfp(30.91), VoltageV: hfp(13.1), FailureStatus: hip(0)}

func healthDB(t *testing.T, raws map[int]int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "batlog.db")
	db, err := store.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for daysAgo, raw := range raws {
		day := testNow.AddDate(0, 0, -daysAgo).Format("2006-01-02")
		if err := db.Exec(ctx, `INSERT INTO health(day, raw_max_mah, design_mah) VALUES(?,?,1000)`, day, raw); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func runHealthT(t *testing.T, h battery.Health, dbFile string, trend, asJSON, color bool) (string, string) {
	t.Helper()
	stubStatus(t, battery.Snapshot{Percent: 80, Health: h}, dbFile)
	var out, errw bytes.Buffer
	if err := runHealth(context.Background(), &out, &errw, trend, asJSON, color); err != nil {
		t.Fatal(err)
	}
	return out.String(), errw.String()
}

func TestHealthHuman(t *testing.T) {
	got, _ := runHealthT(t, macHealth, filepath.Join(t.TempDir(), "none.db"), false, false, false)
	want := "🔎 Battery health\n" +
		"health         86.8%   (5 424 / 6 249 mAh design)\n" +
		"Apple reports  89.2%   (smoothed)\n" +
		"cycles         388\n" +
		"temperature    30.9 °C\n" +
		"voltage        13.10 V\n" +
		"condition      Normal\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestHealthTrendWithoutHistory(t *testing.T) {
	got, _ := runHealthT(t, macHealth, filepath.Join(t.TempDir(), "none.db"), true, false, false)
	if !strings.HasSuffix(got, "condition      Normal\n\ntrend: not enough history yet (have 0 days, need 30)\n") {
		t.Errorf("got:\n%s", got)
	}
}

func TestHealthTrendFromDaemonRows(t *testing.T) {
	// 880 → 877 → 874 over 60 days (design 1000): -0.3 %/month. The row
	// 120 days ago is outside the 90-day window and must be ignored.
	db := healthDB(t, map[int]int{120: 950, 60: 880, 30: 877, 0: 874})
	got, _ := runHealthT(t, macHealth, db, true, false, false)
	if !strings.HasSuffix(got, "\ntrend (60 days)   88.0% → 87.4%   ≈ −0.30 %/month\n") {
		t.Errorf("got:\n%s", got)
	}
}

func TestHealthTrendTooShort(t *testing.T) {
	db := healthDB(t, map[int]int{3: 880, 0: 879})
	got, _ := runHealthT(t, macHealth, db, true, false, false)
	if !strings.HasSuffix(got, "trend: not enough history yet (have 3 days, need 30)\n") {
		t.Errorf("got:\n%s", got)
	}
}

func TestHealthJSON(t *testing.T) {
	db := healthDB(t, map[int]int{60: 880, 30: 877, 0: 874})
	got, _ := runHealthT(t, macHealth, db, true, true, false)
	var j map[string]any
	if err := json.Unmarshal([]byte(got), &j); err != nil {
		t.Fatalf("invalid JSON %q: %v", got, err)
	}
	want := map[string]any{
		"health_pct": 86.8, "apple_health_pct": 89.2, "raw_max_mah": 5424.0, "nominal_mah": 5576.0,
		"design_mah": 6249.0, "cycle_count": 388.0, "temperature_c": 30.9, "voltage_v": 13.1, "condition": "Normal",
	}
	for k, v := range want {
		if j[k] != v {
			t.Errorf("%s = %v, want %v", k, j[k], v)
		}
	}
	tr, ok := j["trend"].(map[string]any)
	if !ok || tr["from_pct"] != 88.0 || tr["to_pct"] != 87.4 || tr["pct_per_month"] != -0.3 || tr["days"] != 60.0 {
		t.Errorf("trend = %v", j["trend"])
	}
}

func TestHealthJSONTrendNullAndMissingKeys(t *testing.T) {
	h := macHealth
	h.DesignMAh, h.NominalMAh, h.FailureStatus = nil, nil, nil
	got, _ := runHealthT(t, h, filepath.Join(t.TempDir(), "none.db"), false, true, false)
	for _, want := range []string{`"health_pct":null`, `"apple_health_pct":null`, `"design_mah":null`, `"condition":null`, `"trend":null`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON %s lacks %s", got, want)
		}
	}
}

func TestHealthMissingDesignNamesKey(t *testing.T) {
	h := macHealth
	h.DesignMAh = nil
	got, _ := runHealthT(t, h, filepath.Join(t.TempDir(), "none.db"), false, false, false)
	if !strings.Contains(got, "health         unknown   (ioreg has no DesignCapacity)\n") {
		t.Errorf("got:\n%s", got)
	}
	if strings.Contains(got, "Apple reports") {
		t.Errorf("Apple figure needs DesignCapacity too:\n%s", got)
	}
}

func TestHealthServiceRecommendedIsRedOnlyWithColor(t *testing.T) {
	h := macHealth
	h.FailureStatus = hip(4)
	plain, _ := runHealthT(t, h, filepath.Join(t.TempDir(), "none.db"), false, false, false)
	if !strings.Contains(plain, "condition      Service recommended (status 4)\n") || strings.Contains(plain, "\x1b[") {
		t.Errorf("plain:\n%q", plain)
	}
	colored, _ := runHealthT(t, h, filepath.Join(t.TempDir(), "none.db"), false, false, true)
	if !strings.Contains(colored, "condition      \x1b[31mService recommended (status 4)\x1b[0m\n") {
		t.Errorf("colored:\n%q", colored)
	}
}

func TestHealthExtraArgIsUsageError(t *testing.T) {
	rootCmd.SetArgs([]string{"health", "extra"})
	if _, ok := rootCmd.Execute().(usageError); !ok {
		t.Error("health with an argument must be a usage error")
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{5424: "5 424", 999: "999", 1000000: "1 000 000", 0: "0"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}

// Golden output for every ioreg fixture, straight from the parser
// (F2 acceptance: ≥ 3 fixtures, never crashing on a missing key).
func TestHealthGoldenFromFixtures(t *testing.T) {
	cases := map[string]string{
		"Mac16,8-15.7.3.plist": "🔎 Battery health\n" +
			"health         86.8%   (5 424 / 6 249 mAh design)\n" +
			"Apple reports  89.2%   (smoothed)\n" +
			"cycles         388\n" +
			"temperature    30.9 °C\n" +
			"voltage        13.10 V\n" +
			"condition      Normal\n",
		"intel-nominal-synthetic.plist": "🔎 Battery health\n" +
			"health         87.9%   (5 100 / 5 800 mAh design)\n" +
			"Apple reports  85.3%   (smoothed)\n" +
			"cycles         512\n" +
			"temperature    30.1 °C\n" +
			"voltage        11.80 V\n" +
			"condition      Service recommended (status 4)\n",
		// No NominalChargeCapacity and no PermanentFailureStatus.
		"intel-synthetic.plist": "🔎 Battery health\n" +
			"health         87.9%   (5 100 / 5 800 mAh design)\n" +
			"cycles         512\n" +
			"temperature    30.1 °C\n" +
			"voltage        11.80 V\n",
	}
	for file, want := range cases {
		raw, err := os.ReadFile(filepath.Join("..", "internal", "battery", "testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		snap, err := battery.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if got, _ := runHealthT(t, snap.Health, filepath.Join(t.TempDir(), "none.db"), false, false, false); got != want {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", file, got, want)
		}
	}
}

func TestHealthStaleTrendSaysWhenLastRecorded(t *testing.T) {
	// The daemon stopped 60 days ago: the trend is real but old.
	db := healthDB(t, map[int]int{90: 880, 60: 877})
	got, _ := runHealthT(t, macHealth, db, true, false, false)
	last := testNow.AddDate(0, 0, -60).Format("02 Jan")
	if !strings.HasSuffix(got, "≈ −0.30 %/month   (last recorded "+last+")\n") {
		t.Errorf("got:\n%s", got)
	}
	js, _ := runHealthT(t, macHealth, db, true, true, false)
	if !strings.Contains(js, `"last_day":"`+testNow.AddDate(0, 0, -60).Format("2006-01-02")+`"`) {
		t.Errorf("JSON trend lacks last_day: %s", js)
	}
	// A trend that ends today carries no note.
	fresh := healthDB(t, map[int]int{30: 880, 0: 877})
	if got, _ := runHealthT(t, macHealth, fresh, true, false, false); strings.Contains(got, "last recorded") {
		t.Errorf("fresh trend:\n%s", got)
	}
}

func TestHealthDesignCapacityZeroWording(t *testing.T) {
	h := macHealth
	h.DesignMAh = hip(0)
	got, _ := runHealthT(t, h, filepath.Join(t.TempDir(), "none.db"), false, false, false)
	if !strings.Contains(got, "health         unknown   (DesignCapacity is 0)\n") {
		t.Errorf("got:\n%s", got)
	}
}
