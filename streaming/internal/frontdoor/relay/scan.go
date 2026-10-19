package relay

// Scanner reads a channel's MPEG-TS packet by packet for the front door's
// aligned splice (Epic 33 story 33.7 AC6, design §8.5): which packets are the
// program tables, and which video packets start an IDR access unit and at what
// PTS. It follows the PAT to the PMT and the PMT to the first video stream, so
// it works on any layout (story 33.4 fixes the PIDs; nothing here assumes them).
type Scanner struct {
	pmt   uint16
	video uint16
}

// PacketInfo is what Scan learnt of one packet.
type PacketInfo struct {
	// PAT / PMT: the packet carries the program association / map table.
	PAT, PMT bool
	// Video: the packet belongs to the program's video stream.
	Video bool
	// IDR: a video packet that starts a PES with a keyframe; PTS is its 33-bit
	// presentation time (HasPTS when the header carries one).
	IDR    bool
	PTS    int64
	HasPTS bool
}

// PacketSize is an MPEG-TS packet's size.
const PacketSize = tsSize

// Scan reads one 188-byte packet (a packet of another size answers nothing).
func (s *Scanner) Scan(p []byte) PacketInfo {
	var pi PacketInfo
	if len(p) != tsSize || p[0] != tsSync {
		return pi
	}
	pid := pidOf(p)
	switch {
	case pid == 0:
		pi.PAT = true
		if pmt := parsePAT(section(p)); pmt != 0 {
			s.pmt = pmt
		}
		return pi
	case s.pmt != 0 && pid == s.pmt:
		pi.PMT = true
		for _, e := range parsePMT(section(p)) {
			if isVideo(e.typ) {
				s.video = e.pid
				break
			}
		}
		return pi
	}
	if s.video == 0 || pid != s.video {
		return pi
	}
	pi.Video = true
	if p[1]&0x40 == 0 {
		return pi
	}
	if pts, _, _, ok := pesTimes(p); ok {
		pi.PTS, pi.HasPTS = pts, true
	}
	pi.IDR = keyframe(p)
	return pi
}

// Ready reports whether the program's video stream is known (a PAT and PMT seen).
func (s *Scanner) Ready() bool { return s.video != 0 }

// PTSDiff is a - b for two 33-bit timestamps, across a wrap (|result| < 2^32).
func PTSDiff(a, b int64) int64 {
	d := (a - b) % ptsWrap
	if d < 0 {
		d += ptsWrap
	}
	if d > ptsHalf {
		d -= ptsWrap
	}
	return d
}

// Offset is the shift the retimer applies to the current source's timestamps
// now (0 for an aligned splice: the new source continues the old one's clock).
func (r *Retimer) Offset() int64 { return r.offset }
