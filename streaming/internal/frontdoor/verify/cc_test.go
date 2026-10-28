package verify

import "testing"

// ccPkt is one TS packet on pid with continuity counter cc: payload or not, and
// discontinuity_indicator set or not (in an adaptation field).
func ccPkt(pid, cc int, payload, disc bool) []byte {
	p := make([]byte, 188)
	p[0], p[1], p[2] = 0x47, byte(pid>>8&0x1f), byte(pid)
	afc := byte(0x20) // an adaptation field, so it can carry the flag
	if payload {
		afc |= 0x10
	}
	p[3] = afc | byte(cc&0x0f)
	p[4] = 1
	if disc {
		p[5] = 0x80
	}
	for i := 6; i < 188; i++ {
		p[i] = 0xff
	}
	return p
}

// The continuity check follows ISO/IEC 13818-1 §2.4.3.3: per PID, the first packet
// seen starts the count, discontinuity_indicator restarts it, a packet without payload
// does not count, a duplicate is allowed once (not twice), the null PID is not
// counted; anything else that is not the next counter is an error.
func TestCCRules(t *testing.T) {
	type pk struct {
		pid, cc       int
		payload, disc bool
	}
	cases := []struct {
		name string
		pkts []pk
		want int
	}{
		{"in order and wrapping", []pk{{0x100, 14, true, false}, {0x100, 15, true, false}, {0x100, 0, true, false}}, 0},
		{"first packet per PID joins mid-stream", []pk{{0x100, 9, true, false}, {0x101, 3, true, false}, {0x100, 10, true, false}, {0x101, 4, true, false}}, 0},
		{"per PID, interleaved", []pk{{0x100, 1, true, false}, {0x101, 7, true, false}, {0x100, 2, true, false}, {0x101, 8, true, false}}, 0},
		{"jump", []pk{{0x100, 1, true, false}, {0x100, 5, true, false}}, 1},
		{"jump flagged by discontinuity_indicator", []pk{{0x100, 1, true, false}, {0x100, 5, true, true}, {0x100, 6, true, false}}, 0},
		{"no payload does not count", []pk{{0x100, 1, true, false}, {0x100, 1, false, false}, {0x100, 2, true, false}}, 0},
		{"duplicate once", []pk{{0x100, 1, true, false}, {0x100, 1, true, false}, {0x100, 2, true, false}}, 0},
		{"duplicate twice", []pk{{0x100, 1, true, false}, {0x100, 1, true, false}, {0x100, 1, true, false}}, 1},
		{"null PID", []pk{{0x1fff, 3, true, false}, {0x1fff, 9, true, false}}, 0},
		{"a fresh muxer on every PID", []pk{{0, 5, true, false}, {0x1000, 5, true, false}, {0x100, 5, true, false},
			{0x101, 5, true, false}, {0, 0, true, false}, {0x1000, 0, true, false}, {0x100, 0, true, false}, {0x101, 0, true, false}}, 4},
	}
	for _, c := range cases {
		var b []byte
		for _, p := range c.pkts {
			b = append(b, ccPkt(p.pid, p.cc, p.payload, p.disc)...)
		}
		if got := AnalyzeTS(b, Options{}).CCErrors; got != c.want {
			t.Errorf("%s: %d continuity errors, want %d", c.name, got, c.want)
		}
	}
}
