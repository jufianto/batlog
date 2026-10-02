package charge

import "testing"

func TestDefaultCurveMatchesTheMeasuredCharges(t *testing.T) {
	c := Learn([Bands]BandStat{})
	// 20 → 100 %: 4 tenths at 78, one at 69, 64, 48, 22 %/hr.
	// 40/78 + 10/69 + 10/64 + 10/48 + 10/22 = 1.476 h = 89 min, in line
	// with the 82–96 min the user's charges from 19–23 % took.
	if got := c.MinutesToFull(20); got != 89 {
		t.Errorf("20%% → full = %d min, want 89", got)
	}
	// 95 → 100 %: 5 / 22 %/hr = 13.6 min.
	if got := c.MinutesToFull(95); got != 14 {
		t.Errorf("95%% → full = %d min, want 14", got)
	}
	if c.MinutesToFull(100) != 0 || c.MinutesToFull(120) != 0 {
		t.Error("a full battery has no time to full")
	}
	if got, want := c.MinutesToFull(-5), c.MinutesToFull(0); got != want {
		t.Errorf("below 0%%: %d, want %d", got, want)
	}
}

func TestLearnTrustsOnlyBandsWithEnoughCharging(t *testing.T) {
	var st [Bands]BandStat
	st[9] = BandStat{Gained: 5, Sec: 30 * 60} // 10 %/hr: learned
	st[8] = BandStat{Gained: 9, Sec: 9 * 60}  // 9 minutes: too little
	st[7] = BandStat{Gained: 0, Sec: 60 * 60} // an hour on hold: no rate
	c := Learn(st)
	if !c.Learned[9] || c.Rates[9] != 10 {
		t.Errorf("band 9 = %v %v, want learned 10 %%/hr", c.Rates[9], c.Learned[9])
	}
	if c.Learned[8] || c.Rates[8] != 48 || c.Learned[7] || c.Rates[7] != 64 {
		t.Errorf("bands 7–8 = %v, want the defaults", c.Rates[7:9])
	}
	// 95 → 100 % at the learned 10 %/hr is 30 minutes.
	if got := c.MinutesToFull(95); got != 30 {
		t.Errorf("95%% → full = %d, want 30", got)
	}
}
