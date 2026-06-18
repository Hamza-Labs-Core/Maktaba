package analytics

// HeatCell is one (day-of-week, hour) bucket from the DB, with watched
// seconds. Dow is 0=Sunday..6=Saturday (Postgres EXTRACT(DOW) and SQLite
// strftime('%w') agree on this), Hour is 0..23.
type HeatCell struct {
	Dow      int
	Hour     int
	WatchSec int64
}

// BuildHeatmap folds the sparse DB rows into a dense 7×24 matrix. Pure
// (no DB) so it is unit-tested directly. Out-of-range cells are ignored
// defensively.
func BuildHeatmap(cells []HeatCell) [7][24]int64 {
	var m [7][24]int64
	for _, c := range cells {
		if c.Dow < 0 || c.Dow > 6 || c.Hour < 0 || c.Hour > 23 {
			continue
		}
		m[c.Dow][c.Hour] += c.WatchSec
	}
	return m
}
