package rehearsal

import (
	"strings"
	"testing"
)

func put(rung string, seq int64, status int, err string) HomePut {
	return HomePut{Rung: rung, Seq: seq, Status: status, TookMS: 70, Err: err}
}

// The fan-out's last check: the uploader, the edge's answers and the edge's stores agree, and
// an extra stored segment is allowed only where the home's PUT ended with no answer.
func TestTallyRung(t *testing.T) {
	clean := []HomePut{put("480p", 1, 201, ""), put("480p", 2, 201, ""), put("src", 1, 201, "")}
	timeout := "context deadline exceeded"
	for _, c := range []struct {
		name      string
		puts      []HomePut
		own, edge int
		ok        bool
		want      string
	}{
		{"every segment once", clean, 2, 2, true, "uploader 2, answered 2, edge 2; 0 unanswered"},
		// the nightly at 1,000 viewers: the edge stored a segment whose PUT timed out at the home
		{"stored after the home gave up", append(clean, put("480p", 3, 0, timeout)), 2, 3, true,
			"[480p/3 no answer after 70 ms: context deadline exceeded]"},
		{"unanswered and not stored (a skip)", append(clean, put("480p", 3, 0, timeout)), 2, 2, true, "1 unanswered"},
		// a retry after a reset, answered as a duplicate: one segment, answered once
		{"a replay answered 200 duplicate", []HomePut{put("480p", 1, 0, "connection reset by peer"), put("480p", 1, 200, "")}, 1, 1, true, "0 unanswered"},
		{"a 429 then a 201", []HomePut{put("480p", 1, 429, ""), put("480p", 1, 201, "")}, 1, 1, true, "edge 1"},
		{"the edge stored one the home never sent", clean, 2, 3, false, "edge 3"},
		{"the uploader counts one the edge never answered", clean, 3, 2, false, "uploader 3"},
		{"the edge stored fewer than it answered", append(clean, put("480p", 3, 200, "")), 3, 2, false, "[480p/3 200 after 70 ms]"},
		{"too many unanswered", []HomePut{put("480p", 1, 201, ""), put("480p", 2, 0, timeout), put("480p", 3, 0, timeout)}, 1, 3, false, "2 unanswered (bound 1)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := TallyRung(c.puts, "480p", c.own, c.edge)
			if got.OK() != c.ok || !strings.Contains(got.String(), c.want) {
				t.Fatalf("ok %v (want %v): %s", got.OK(), c.ok, got)
			}
		})
	}
}

// The bound is 2 % of a rung's answered segments, at least 1: 6 of a nightly's 316.
func TestMaxUnanswered(t *testing.T) {
	for answered, want := range map[int]int{0: 1, 49: 1, 100: 2, 316: 6} {
		if got := (UploadTally{Answered: answered}).MaxUnanswered(); got != want {
			t.Errorf("%d answered: %d, want %d", answered, got, want)
		}
	}
}
