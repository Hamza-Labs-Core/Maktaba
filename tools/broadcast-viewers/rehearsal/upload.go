package rehearsal

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// HomePut is one segment PUT as the rehearsal home's proxy counter logged it: the edge's answer,
// or the client's error when there was none (Status 0).
type HomePut struct {
	Rung   string
	Seq    int64
	Status int
	TookMS int64
	Err    string
}

// UploadTally is one rung's upload as the home and the edge each counted it.
//
// Own is what the uploader reported sent. Answered is the segments the edge answered as
// having (a 2xx or a 409); it must equal Own. Edge is what the edge stored (its 201s); each
// segment is stored once (ChannelLive takes a seq once, and a same-body re-PUT is a duplicate),
// so Edge may exceed Answered only by segments whose every PUT ended with no answer at all (a
// client timeout or a dropped connection after the edge had the body): an ambiguous end that
// the uploader skips, as it must past its retry window. Those are Unanswered, and kept few.
type UploadTally struct {
	Rung       string
	Own        int
	Answered   int
	Edge       int
	Unanswered []HomePut
	// Refused is every other PUT that was not a 201 (a 200 duplicate or stale, a 409, a 4xx or
	// 5xx retried), listed when the tally fails
	Refused []HomePut
}

// TallyRung reconciles a rung's counts from the home's PUT log.
func TallyRung(puts []HomePut, rung string, own, edge int) UploadTally {
	t := UploadTally{Rung: rung, Own: own, Edge: edge}
	answered := map[int64]bool{}
	tries := map[int64][]HomePut{}
	for _, p := range puts {
		if p.Rung != rung {
			continue
		}
		tries[p.Seq] = append(tries[p.Seq], p)
		if p.Status/100 == 2 || p.Status == http.StatusConflict {
			answered[p.Seq] = true
		}
		if p.Status != 0 && p.Status != http.StatusCreated {
			t.Refused = append(t.Refused, p)
		}
	}
	t.Answered = len(answered)
	seqs := make([]int64, 0, len(tries))
	for s := range tries {
		seqs = append(seqs, s)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for _, s := range seqs {
		if !answered[s] {
			ps := tries[s]
			t.Unanswered = append(t.Unanswered, ps[len(ps)-1])
		}
	}
	return t
}

// MaxUnanswered is the most segments of a rung that may end with no answer: 2 % of those
// answered, at least 1.
func (t UploadTally) MaxUnanswered() int {
	return max(1, t.Answered*2/100)
}

// OK says the counts reconcile: the uploader and the edge's answers agree exactly, the edge
// stored no segment the home did not send once, any extra stored segment is one that ended
// unanswered, and those are within MaxUnanswered.
func (t UploadTally) OK() bool {
	extra := t.Edge - t.Answered
	return t.Own == t.Answered && extra >= 0 && extra <= len(t.Unanswered) &&
		len(t.Unanswered) <= t.MaxUnanswered()
}

// String is the tally's detail: the counts, and on a failure or an unanswered segment the PUTs
// that explain it.
func (t UploadTally) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "uploader %d, answered %d, edge %d; %d unanswered (bound %d)",
		t.Own, t.Answered, t.Edge, len(t.Unanswered), t.MaxUnanswered())
	for _, p := range t.Unanswered {
		fmt.Fprintf(&b, " [%s/%d no answer after %d ms: %s]", p.Rung, p.Seq, p.TookMS, p.Err)
	}
	if !t.OK() {
		for i, p := range t.Refused {
			if i == 20 {
				fmt.Fprintf(&b, " … %d more", len(t.Refused)-i)
				break
			}
			fmt.Fprintf(&b, " [%s/%d %d after %d ms]", p.Rung, p.Seq, p.Status, p.TookMS)
		}
	}
	return b.String()
}
