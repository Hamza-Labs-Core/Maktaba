package telemetry

// Ranges are what every count in the server's report is sent as: how big a
// home is, never how big exactly (Maktaba Cloud refuses any other value).
var Ranges = []string{"0", "1", "2-5", "6-20", "21-100", "101-1k", "1k-10k", "10k-100k", "100k+"}

// Range is the range n falls in.
func Range(n int64) string {
	switch {
	case n <= 0:
		return "0"
	case n == 1:
		return "1"
	case n <= 5:
		return "2-5"
	case n <= 20:
		return "6-20"
	case n <= 100:
		return "21-100"
	case n <= 1000:
		return "101-1k"
	case n <= 10000:
		return "1k-10k"
	case n <= 100000:
		return "10k-100k"
	}
	return "100k+"
}
