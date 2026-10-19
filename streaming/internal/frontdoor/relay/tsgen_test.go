package relay

import "testing"

// A small MPEG-TS writer for the relay's tests: one program, H.264 video and
// AAC audio, one PES per frame (one packet each), PCR on the video PID, a
// keyframe (random access + an IDR NAL) every gop frames.

type tsGen struct {
	pmt, video, audio uint16
	vType, aType      byte
	noAudio           bool
	pts               int64 // next video PTS (90 kHz)
	apts              int64
	step, astep       int64
	gop, n            int
	cc                map[uint16]byte
}

func newGen(pmt, video, audio uint16, start int64) *tsGen {
	return &tsGen{pmt: pmt, video: video, audio: audio, vType: stH264, aType: stAAC, pts: start, apts: start,
		step: 6000, astep: 1920, gop: 30, cc: map[uint16]byte{}}
}

// crc32mpeg is CRC-32/MPEG-2 (non-reflected).
func crc32mpeg(b []byte) uint32 {
	crc := uint32(0xffffffff)
	for _, x := range b {
		crc ^= uint32(x) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func (g *tsGen) header(pid uint16, pusi bool, adapt bool) []byte {
	p := make([]byte, tsSize)
	for i := range p {
		p[i] = 0xff
	}
	p[0] = tsSync
	p[1] = byte(pid>>8) & 0x1f
	if pusi {
		p[1] |= 0x40
	}
	p[2] = byte(pid)
	afc := byte(1)
	if adapt {
		afc = 3
	}
	p[3] = afc<<4 | g.cc[pid]
	g.cc[pid] = (g.cc[pid] + 1) & 0x0f
	return p
}

func (g *tsGen) psi(pid uint16, sec []byte) []byte {
	crc := crc32mpeg(sec)
	sec = append(sec, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	p := g.header(pid, true, false)
	p[4] = 0 // pointer field
	copy(p[5:], sec)
	return p
}

func (g *tsGen) pat() []byte {
	sec := []byte{0x00, 0xb0, 13, 0x00, 0x01, 0xc1, 0x00, 0x00,
		0x00, 0x01, 0xe0 | byte(g.pmt>>8), byte(g.pmt)}
	return g.psi(0, sec)
}

func (g *tsGen) pmtPkt() []byte {
	es := []byte{g.vType, 0xe0 | byte(g.video>>8), byte(g.video), 0xf0, 0x00}
	if !g.noAudio {
		es = append(es, g.aType, 0xe0|byte(g.audio>>8), byte(g.audio), 0xf0, 0x00)
	}
	body := append([]byte{0x00, 0x01, 0xc1, 0x00, 0x00, 0xe0 | byte(g.video>>8), byte(g.video), 0xf0, 0x00}, es...)
	n := len(body) + 4
	sec := append([]byte{0x02, 0xb0 | byte(n>>8), byte(n)}, body...)
	return g.psi(g.pmt, sec)
}

func putTS(b []byte, prefix byte, v int64) {
	b[0] = prefix<<4 | 1
	writeTS(b, v)
	b[0] = b[0]&0x0f | prefix<<4
}

// frame is one video packet (and an audio packet), with PAT/PMT before keyframes.
func (g *tsGen) frame() []byte {
	var out []byte
	key := g.n%g.gop == 0
	if key {
		out = append(out, g.pat()...)
		out = append(out, g.pmtPkt()...)
	}
	p := g.header(g.video, true, true)
	// adaptation: length 7, flags (RAI on keys) + PCR
	p[4] = 7
	p[5] = 0x10
	if key {
		p[5] |= 0x40
	}
	base := (g.pts - 3000) & (ptsWrap - 1)
	if base < 0 {
		base += ptsWrap
	}
	setPCR(p, base, 0)
	o := 12
	pes := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0xc0, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	putTS(pes[9:], 3, g.pts&(ptsWrap-1))
	putTS(pes[14:], 1, g.pts&(ptsWrap-1))
	copy(p[o:], pes)
	nal := byte(1)
	if key {
		nal = 5
	}
	copy(p[o+len(pes):], []byte{0, 0, 0, 1, 0x09, 0xf0, 0, 0, 1, nal})
	out = append(out, p...)
	if !g.noAudio {
		a := g.header(g.audio, true, false)
		apes := []byte{0, 0, 1, 0xc0, 0, 0, 0x80, 0x80, 5, 0, 0, 0, 0, 0}
		putTS(apes[9:], 2, g.apts&(ptsWrap-1))
		copy(a[4:], apes)
		out = append(out, a...)
		g.apts += g.astep
		// audio runs at about the video's pace
		for g.apts < g.pts+g.step {
			a := g.header(g.audio, true, false)
			apes := []byte{0, 0, 1, 0xc0, 0, 0, 0x80, 0x80, 5, 0, 0, 0, 0, 0}
			putTS(apes[9:], 2, g.apts&(ptsWrap-1))
			copy(a[4:], apes)
			out = append(out, a...)
			g.apts += g.astep
		}
	}
	g.pts += g.step
	g.n++
	return out
}

func (g *tsGen) frames(n int) []byte {
	var out []byte
	for i := 0; i < n; i++ {
		out = append(out, g.frame()...)
	}
	return out
}

// tsTrack is what a test reads back from TS: per PID the (unwrapped) DTS
// sequence, the continuity counters, the PMT PID and the stream PIDs.
type tsTrack struct {
	dts     map[uint16][]int64
	unw     map[uint16]int64
	cc      map[uint16]int
	ccBreak int
	pmt     uint16
	es      []esInfo
	keys    []int // video frame indexes that are keyframes
	first   []uint16
}

func readTrack(t *testing.T, b []byte) *tsTrack {
	t.Helper()
	tr := &tsTrack{dts: map[uint16][]int64{}, unw: map[uint16]int64{}, cc: map[uint16]int{}}
	if len(b)%tsSize != 0 {
		t.Fatalf("output is not whole packets: %d bytes", len(b))
	}
	for i := 0; i < len(b); i += tsSize {
		p := b[i : i+tsSize]
		if p[0] != tsSync {
			t.Fatalf("lost sync at %d", i)
		}
		pid := pidOf(p)
		if len(tr.first) < 3 {
			tr.first = append(tr.first, pid)
		}
		if (p[3]>>4)&1 != 0 {
			c := int(p[3] & 0x0f)
			if last, ok := tr.cc[pid]; ok && c != (last+1)&0x0f {
				tr.ccBreak++
			}
			tr.cc[pid] = c
		}
		switch {
		case pid == 0:
			tr.pmt = parsePAT(section(p))
		case pid == tr.pmt && tr.pmt != 0:
			if es := parsePMT(section(p)); es != nil {
				tr.es = es
			}
		default:
			if p[1]&0x40 != 0 {
				if _, dts, _, ok := pesTimes(p); ok {
					u := unwrap(tr.unw, pid, dts)
					if len(tr.es) > 0 && pid == tr.es[0].pid && keyframe(p) {
						tr.keys = append(tr.keys, len(tr.dts[pid]))
					}
					tr.dts[pid] = append(tr.dts[pid], u)
				}
			}
		}
	}
	return tr
}

// monotonic checks a DTS sequence: strictly increasing, no gap over maxGap.
func monotonic(t *testing.T, name string, ds []int64, maxGap int64) {
	t.Helper()
	for i := 1; i < len(ds); i++ {
		d := ds[i] - ds[i-1]
		if d <= 0 {
			t.Fatalf("%s: timestamp %d not after %d (index %d)", name, ds[i], ds[i-1], i)
		}
		if maxGap > 0 && d > maxGap {
			t.Fatalf("%s: gap %d > %d at index %d", name, d, maxGap, i)
		}
	}
}
