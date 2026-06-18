package watch

import "strconv"

// pidx builds a $N placeholder for both Postgres (lib/pq) and SQLite
// (modernc accepts $N too), matching the videos/channels handlers.
func pidx(n int) string { return "$" + strconv.Itoa(n) }
