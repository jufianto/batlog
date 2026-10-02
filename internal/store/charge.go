package store

import "context"

// ChargeBand is the charging inside one tenth of the battery (band 0 is
// 0–9 %, band 9 is 90–99 %): the percent gained over awake seconds.
type ChargeBand struct {
	Band   int
	Gained int
	Sec    int64
}

// ChargeBands sums, per band, the awake intervals since since in which the
// Mac was charging on AC from one sample to the next: at most 90 s apart
// (longer is sleep, F3), charging at the first, below 100 % there.
func (d *DB) ChargeBands(ctx context.Context, since int64) ([]ChargeBand, error) {
	rows, err := d.sql.QueryContext(ctx,
		`WITH s AS (
		   SELECT ts, pct, on_ac, charging,
		          LEAD(ts) OVER w AS nts, LEAD(pct) OVER w AS npct, LEAD(on_ac) OVER w AS nac
		   FROM samples WHERE ts >= ? WINDOW w AS (ORDER BY ts))
		 SELECT pct / 10, SUM(npct - pct), SUM(nts - ts) FROM s
		 WHERE on_ac = 1 AND nac = 1 AND charging = 1 AND pct < 100 AND pct >= 0 AND nts - ts <= 90
		 GROUP BY pct / 10 ORDER BY 1`, since)
	if err != nil {
		return nil, err
	}
	var out []ChargeBand
	err = each(rows, func() error {
		var b ChargeBand
		if err := rows.Scan(&b.Band, &b.Gained, &b.Sec); err != nil {
			return err
		}
		out = append(out, b)
		return nil
	})
	return out, err
}
