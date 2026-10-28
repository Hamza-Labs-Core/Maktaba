// Package verify holds the stream checks of a staged TV release (Epic 33 story
// 33.5, design §6.2): the front door samples the staging slot's output (and the
// live slot's, to compare) and these functions measure it. Everything here is a
// pure function over bytes, parsed in Go (plan D4: no ffprobe in the hot path),
// so each check is testable with crafted streams.
package verify

import (
	"sort"
	"time"
)

const tsPacket = 188

// Stream types in a PMT.
const (
	StreamH264 = 0x1b
	StreamHEVC = 0x24
	StreamAAC  = 0x0f
	StreamAC3  = 0x81
	StreamMP2  = 0x03
)

// TSReport is what one sample of MPEG-TS (one or more HLS segments, or a
// stretch of live.ts) measured.
type TSReport struct {
	Packets   int     `json:"packets"`
	Bytes     int     `json:"bytes"`
	Duration  float64 `json:"duration"` // seconds of video (PTS span + one frame)
	SyncLoss  int     `json:"sync_loss,omitempty"`
	PATs      int     `json:"pats"`
	PMTs      int     `json:"pmts"`
	MaxPATGap float64 `json:"max_pat_gap"` // seconds between PATs (stream clock)
	ServiceID int     `json:"service_id"`
	PMTPID    int     `json:"pmt_pid"`
	VideoPID  int     `json:"video_pid"`
	AudioPID  int     `json:"audio_pid"`
	VideoType int     `json:"video_type"`
	AudioType int     `json:"audio_type"`

	// Video (from the SPS and the slice headers).
	Codec     string  `json:"codec"` // h264 | hevc | other
	Profile   int     `json:"profile"`
	Level     int     `json:"level"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	FPS       float64 `json:"fps"`
	Frames    int     `json:"frames"`
	IDRs      int     `json:"idrs"`
	MaxIDRGap float64 `json:"max_idr_gap"` // seconds
	GridIDRs  int     `json:"grid_idrs"`   // IDRs on a wall-clock multiple of the keyint (Grid)
	BFrames   int     `json:"b_frames"`
	OpenGOP   int     `json:"open_gop"` // random-access pictures that are not IDRs
	PTSNotDTS int     `json:"pts_not_dts"`
	FirstPTS  int64   `json:"first_pts"`
	FirstIDR  bool    `json:"first_idr"` // the sample starts with an IDR
	CCErrors  int     `json:"cc_errors"`
	PCRs      int     `json:"pcrs"`
	// PCRSpan is the seconds of stream clock the sample's PCRs span (first to last):
	// how much of a live.ts read there was, PAT or not (Duration needs the PMT).
	PCRSpan     float64 `json:"pcr_span,omitempty"`
	PCRBack     int     `json:"pcr_back"`
	VideoPTSBad int     `json:"video_pts_back"`

	// Audio (from the ADTS / AC-3 headers).
	AudioCodec    string `json:"audio_codec"`
	AudioRate     int    `json:"audio_rate"`
	AudioChannels int    `json:"audio_channels"`

	// Bitrate is the sample's combined bit rate (bits per second of video).
	Bitrate float64 `json:"bitrate"`
}

// Options steer an analysis.
type Options struct {
	// Grid is the keyframe grid in seconds (tv_output keyint_seconds): an IDR
	// whose PTS is a wall-clock multiple of it counts in GridIDRs (0: none).
	Grid int
	// Now unwraps the 33-bit PTS to wall-clock ticks for the grid (zero: the
	// PTS as is, right until the first wrap after the Unix epoch).
	Now time.Time
}

const ptsWrap = int64(1) << 33

// WallTicks unwraps a 33-bit PTS to the 90 kHz wall-clock ticks nearest now.
func WallTicks(pts int64, now time.Time) int64 {
	if now.IsZero() {
		return pts
	}
	nt := now.Unix()*90000 + int64(now.Nanosecond())*9/100000
	d := (pts - nt) % ptsWrap
	if d < 0 {
		d += ptsWrap
	}
	if d > ptsWrap/2 {
		d -= ptsWrap
	}
	return nt + d
}

// PTSTime is a PTS as a wall-clock instant (story 33.4: PTS is the wall clock).
func PTSTime(pts int64, now time.Time) time.Time {
	t := WallTicks(pts, now)
	return time.Unix(t/90000, (t%90000)*100000/9).UTC()
}

type pidState struct {
	cc      int
	haveCC  bool
	dup     bool // the last payload packet repeated the one before
	pes     []byte
	started bool
}

// analyzer is one pass over a sample.
type analyzer struct {
	o        Options
	r        TSReport
	pids     map[int]*pidState
	clock    int64 // the latest PCR (90 kHz) or video PTS, for the PAT gaps
	haveClk  bool
	lastPAT  int64
	havePAT  bool
	lastPCR  int64
	havePCR  bool
	firstPCR int64
	ptss     []int64
	idrs     []int64
	lastVPT  int64
	haveVPT  bool
	sawPic   bool
}

// AnalyzeTS measures a TS sample. Segments may be given one by one (Segments)
// so continuity is not judged across their boundaries.
func AnalyzeTS(b []byte, o Options) TSReport {
	return AnalyzeSegments([][]byte{b}, o)
}

// AnalyzeSegments measures consecutive HLS segments as one sample; continuity
// counters restart at each segment (the muxer opens each file afresh).
func AnalyzeSegments(segs [][]byte, o Options) TSReport {
	a := &analyzer{o: o, pids: map[int]*pidState{}}
	for _, b := range segs {
		for _, st := range a.pids {
			st.haveCC = false
		}
		a.feed(b)
		a.flushAll()
	}
	a.finish()
	return a.r
}

func (a *analyzer) feed(b []byte) {
	for i := 0; i+tsPacket <= len(b); {
		if b[i] != 0x47 {
			a.r.SyncLoss++
			i++
			continue
		}
		a.packet(b[i : i+tsPacket])
		i += tsPacket
	}
}

func (a *analyzer) state(pid int) *pidState {
	st := a.pids[pid]
	if st == nil {
		st = &pidState{}
		a.pids[pid] = st
	}
	return st
}

func (a *analyzer) packet(p []byte) {
	a.r.Packets++
	a.r.Bytes += tsPacket
	pid := int(p[1]&0x1f)<<8 | int(p[2])
	if pid == 0x1fff {
		return
	}
	pusi := p[1]&0x40 != 0
	afc := (p[3] >> 4) & 3
	cc := int(p[3] & 0x0f)
	off := 4
	disc := false
	if afc&2 != 0 {
		alen := int(p[4])
		if alen > 0 && 5+alen <= tsPacket {
			flags := p[5]
			disc = flags&0x80 != 0
			if flags&0x10 != 0 && alen >= 7 {
				base := int64(p[6])<<25 | int64(p[7])<<17 | int64(p[8])<<9 | int64(p[9])<<1 | int64(p[10]>>7)
				a.pcr(base)
			}
		}
		off += 1 + alen
	}
	st := a.state(pid)
	// ISO/IEC 13818-1 §2.4.3.3, per PID: the counter goes up by one on each packet with
	// payload (one without does not count); the first packet seen on a PID (a read
	// that joined mid-stream) and one with discontinuity_indicator set start afresh; a
	// packet may be sent twice in a row (a duplicate: the same counter), not three times.
	dup := false
	if afc&1 != 0 {
		if st.haveCC && !disc {
			switch {
			case cc == (st.cc+1)&0x0f:
			case cc == st.cc && !st.dup:
				dup = true
			default:
				a.r.CCErrors++
			}
		}
		st.cc, st.haveCC, st.dup = cc, true, dup
	}
	if afc&1 == 0 || off >= tsPacket || dup { // a duplicate's payload is already in
		return
	}
	payload := p[off:]
	switch {
	case pid == 0:
		if pusi {
			a.pat(payload)
		}
	case pid == a.r.PMTPID && a.r.PMTPID != 0:
		if pusi {
			a.pmt(payload)
		}
	case pid == a.r.VideoPID || pid == a.r.AudioPID:
		if pusi {
			a.flush(pid, st)
			st.pes = append(st.pes[:0], payload...)
			st.started = true
		} else if st.started {
			st.pes = append(st.pes, payload...)
		}
	}
}

func (a *analyzer) pcr(base int64) {
	a.r.PCRs++
	if a.havePCR {
		d := base - a.lastPCR
		if d < 0 && d > -(1<<32) { // not a wrap
			a.r.PCRBack++
		}
	} else {
		a.firstPCR = base
	}
	if span := (base - a.firstPCR + ptsWrap) % ptsWrap; span < ptsWrap/2 {
		if s := float64(span) / 90000; s > a.r.PCRSpan {
			a.r.PCRSpan = s
		}
	}
	a.lastPCR, a.havePCR = base, true
	a.clock, a.haveClk = base, true
}

// section returns a PSI section (pointer field skipped) or nil.
func section(payload []byte) []byte {
	if len(payload) < 1 {
		return nil
	}
	ptr := int(payload[0])
	if 1+ptr >= len(payload) {
		return nil
	}
	s := payload[1+ptr:]
	if len(s) < 3 {
		return nil
	}
	n := int(s[1]&0x0f)<<8 | int(s[2])
	if 3+n > len(s) {
		return s
	}
	return s[:3+n]
}

func (a *analyzer) pat(payload []byte) {
	s := section(payload)
	if len(s) < 12 || s[0] != 0 {
		return
	}
	a.r.PATs++
	if a.haveClk {
		if a.havePAT {
			if g := float64(a.clock-a.lastPAT) / 90000; g > a.r.MaxPATGap {
				a.r.MaxPATGap = g
			}
		}
		a.lastPAT, a.havePAT = a.clock, true
	}
	end := len(s) - 4 // CRC
	for i := 8; i+4 <= end; i += 4 {
		prog := int(s[i])<<8 | int(s[i+1])
		pid := int(s[i+2]&0x1f)<<8 | int(s[i+3])
		if prog != 0 {
			a.r.ServiceID, a.r.PMTPID = prog, pid
			return
		}
	}
}

func (a *analyzer) pmt(payload []byte) {
	s := section(payload)
	if len(s) < 16 || s[0] != 2 {
		return
	}
	a.r.PMTs++
	pil := int(s[10]&0x0f)<<8 | int(s[11])
	end := len(s) - 4
	for i := 12 + pil; i+5 <= end; {
		typ := int(s[i])
		pid := int(s[i+1]&0x1f)<<8 | int(s[i+2])
		esl := int(s[i+3]&0x0f)<<8 | int(s[i+4])
		switch typ {
		case StreamH264, StreamHEVC, 0x02, 0x10:
			if a.r.VideoPID == 0 {
				a.r.VideoPID, a.r.VideoType = pid, typ
			}
		case StreamAAC, StreamAC3, StreamMP2, 0x04, 0x11, 0x87:
			if a.r.AudioPID == 0 {
				a.r.AudioPID, a.r.AudioType = pid, typ
			}
		}
		i += 5 + esl
	}
}

func (a *analyzer) flushAll() {
	for pid, st := range a.pids {
		if pid == a.r.VideoPID || pid == a.r.AudioPID {
			a.flush(pid, st)
		}
	}
}

// flush handles a complete PES.
func (a *analyzer) flush(pid int, st *pidState) {
	if !st.started || len(st.pes) < 9 {
		st.started = false
		return
	}
	pes := st.pes
	st.started = false
	if pes[0] != 0 || pes[1] != 0 || pes[2] != 1 {
		return
	}
	flags := pes[7]
	hlen := int(pes[8])
	if 9+hlen > len(pes) {
		return
	}
	var pts, dts int64
	havePTS := flags&0x80 != 0 && hlen >= 5
	if havePTS {
		pts = readTS(pes[9:])
		dts = pts
		if flags&0x40 != 0 && hlen >= 10 {
			dts = readTS(pes[14:])
		}
	}
	es := pes[9+hlen:]
	if pid == a.r.VideoPID {
		a.video(es, pts, dts, havePTS)
	} else {
		a.audioES(es)
	}
}

func readTS(b []byte) int64 {
	return int64(b[0]>>1&0x07)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1)
}

func (a *analyzer) video(es []byte, pts, dts int64, havePTS bool) {
	if havePTS {
		if pts != dts {
			a.r.PTSNotDTS++
		}
		if a.haveVPT {
			if d := pts - a.lastVPT; d <= 0 && d > -(1<<32) {
				a.r.VideoPTSBad++
			}
		}
		a.lastVPT, a.haveVPT = pts, true
		if !a.havePCR {
			a.clock, a.haveClk = pts, true
		}
	}
	idr, slice, sliceType := false, false, -1
	codec := a.r.VideoType
	for _, nal := range splitNALs(es) {
		if len(nal) == 0 {
			continue
		}
		if codec == StreamHEVC {
			t := int(nal[0]>>1) & 0x3f
			if t >= 16 && t <= 21 {
				idr = true
			}
			if t < 32 {
				slice = true
			}
			continue
		}
		switch nal[0] & 0x1f {
		case 7:
			a.sps(nal)
		case 5:
			idr, slice = true, true
			if sliceType < 0 {
				sliceType = sliceTypeOf(nal)
			}
		case 1:
			slice = true
			if sliceType < 0 {
				sliceType = sliceTypeOf(nal)
			}
		}
	}
	if !slice {
		return
	}
	a.r.Frames++
	if havePTS {
		a.ptss = append(a.ptss, pts)
	}
	if !a.sawPic {
		a.sawPic = true
		a.r.FirstPTS = pts
		a.r.FirstIDR = idr
	}
	if sliceType%5 == 1 {
		a.r.BFrames++
	}
	if idr {
		a.r.IDRs++
		if havePTS {
			a.idrs = append(a.idrs, pts)
			if g := int64(a.o.Grid) * 90000; g > 0 {
				if r := WallTicks(pts, a.o.Now) % g; r <= 2 || g-r <= 2 {
					a.r.GridIDRs++
				}
			}
		}
	} else if sliceType%5 == 2 && a.r.Frames > 1 && randomAccess(es) {
		a.r.OpenGOP++
	}
}

// randomAccess reports a recovery-point SEI (an I picture meant as an entry
// point: an open GOP when it is not an IDR).
func randomAccess(es []byte) bool {
	for _, nal := range splitNALs(es) {
		if len(nal) > 1 && nal[0]&0x1f == 6 && nal[1] == 6 {
			return true
		}
	}
	return false
}

func (a *analyzer) audioES(es []byte) {
	if a.r.AudioCodec != "" || len(es) < 7 {
		return
	}
	switch {
	case es[0] == 0xff && es[1]&0xf0 == 0xf0: // ADTS
		rates := []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
		if idx := int(es[2]>>2) & 0x0f; idx < len(rates) {
			a.r.AudioRate = rates[idx]
		}
		a.r.AudioChannels = int(es[2]&0x01)<<2 | int(es[3]>>6)
		a.r.AudioCodec = "aac"
	case es[0] == 0x0b && es[1] == 0x77: // AC-3
		switch es[4] >> 6 {
		case 0:
			a.r.AudioRate = 48000
		case 1:
			a.r.AudioRate = 44100
		case 2:
			a.r.AudioRate = 32000
		}
		a.r.AudioCodec = "ac3"
	default:
		switch a.r.AudioType {
		case StreamMP2, 0x04:
			a.r.AudioCodec = "mp2"
		default:
			a.r.AudioCodec = "other"
		}
	}
}

func (a *analyzer) finish() {
	switch a.r.VideoType {
	case StreamH264:
		a.r.Codec = "h264"
	case StreamHEVC:
		a.r.Codec = "hevc"
	case 0:
	default:
		a.r.Codec = "other"
	}
	if len(a.ptss) > 1 {
		s := append([]int64(nil), a.ptss...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		deltas := make([]int64, 0, len(s)-1)
		for i := 1; i < len(s); i++ {
			if d := s[i] - s[i-1]; d > 0 && d < 90000 {
				deltas = append(deltas, d)
			}
		}
		if len(deltas) > 0 {
			sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
			med := deltas[len(deltas)/2]
			a.r.FPS = float64(int(90000.0/float64(med)*100+0.5)) / 100
			span := s[len(s)-1] - s[0] + med
			a.r.Duration = float64(span) / 90000
		}
	}
	for i := 1; i < len(a.idrs); i++ {
		if g := float64(a.idrs[i]-a.idrs[i-1]) / 90000; g > a.r.MaxIDRGap {
			a.r.MaxIDRGap = g
		}
	}
	if a.r.Duration > 0 {
		a.r.Bitrate = float64(a.r.Bytes*8) / a.r.Duration
	}
}

// splitNALs splits Annex B byte stream into NAL units (start codes removed).
func splitNALs(b []byte) [][]byte {
	var out [][]byte
	start := -1
	for i := 0; i+2 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				end := i
				for end > start && b[end-1] == 0 {
					end--
				}
				out = append(out, b[start:end])
			}
			start = i + 3
			i += 2
		}
	}
	if start >= 0 && start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// rbsp removes emulation prevention bytes.
func rbsp(nal []byte) []byte {
	out := make([]byte, 0, len(nal))
	zeros := 0
	for _, c := range nal {
		if zeros >= 2 && c == 3 {
			zeros = 0
			continue
		}
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
		out = append(out, c)
	}
	return out
}

type bitReader struct {
	b   []byte
	pos int
	bad bool
}

func (r *bitReader) u(n int) int {
	v := 0
	for i := 0; i < n; i++ {
		if r.pos/8 >= len(r.b) {
			r.bad = true
			return 0
		}
		bit := int(r.b[r.pos/8]>>(7-uint(r.pos%8))) & 1
		v = v<<1 | bit
		r.pos++
	}
	return v
}

func (r *bitReader) ue() int {
	z := 0
	for r.u(1) == 0 {
		z++
		if z > 31 || r.bad {
			r.bad = true
			return 0
		}
	}
	return (1<<z - 1) + r.u(z)
}

func (r *bitReader) se() int {
	k := r.ue()
	if k%2 == 1 {
		return (k + 1) / 2
	}
	return -k / 2
}

func sliceTypeOf(nal []byte) int {
	r := &bitReader{b: rbsp(nal[1:])}
	r.ue() // first_mb_in_slice
	t := r.ue()
	if r.bad {
		return -1
	}
	return t
}

// sps reads profile, level and the picture size of an H.264 SPS.
func (a *analyzer) sps(nal []byte) {
	if len(nal) < 4 {
		return
	}
	b := rbsp(nal[1:])
	r := &bitReader{b: b}
	profile := r.u(8)
	r.u(8)
	level := r.u(8)
	r.ue()
	chroma := 1
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chroma = r.ue()
		if chroma == 3 {
			r.u(1)
		}
		r.ue()
		r.ue()
		r.u(1)
		if r.u(1) == 1 {
			n := 8
			if chroma == 3 {
				n = 12
			}
			for i := 0; i < n; i++ {
				if r.u(1) == 1 {
					size := 16
					if i >= 6 {
						size = 64
					}
					last, next := 8, 8
					for j := 0; j < size; j++ {
						if next != 0 {
							next = (last + r.se() + 256) % 256
						}
						if next != 0 {
							last = next
						}
					}
				}
			}
		}
	}
	r.ue() // log2_max_frame_num_minus4
	switch r.ue() {
	case 0:
		r.ue()
	case 1:
		r.u(1)
		r.se()
		r.se()
		n := r.ue()
		for i := 0; i < n && !r.bad; i++ {
			r.se()
		}
	}
	r.ue()
	r.u(1)
	w := r.ue()
	h := r.ue()
	fmo := r.u(1)
	if fmo == 0 {
		r.u(1)
	}
	r.u(1)
	cl, cr, ct, cb := 0, 0, 0, 0
	if r.u(1) == 1 {
		cl, cr, ct, cb = r.ue(), r.ue(), r.ue(), r.ue()
	}
	if r.bad {
		return
	}
	cx, cy := 2, 2*(2-fmo)
	switch chroma {
	case 0, 3:
		cx, cy = 1, 2-fmo
	case 2:
		cy = 2 - fmo
	}
	a.r.Profile, a.r.Level = profile, level
	a.r.Width = (w+1)*16 - cx*(cl+cr)
	a.r.Height = (2-fmo)*(h+1)*16 - cy*(ct+cb)
}

// PSIScan watches TS bytes as they arrive for the first PAT and then its PMT (a live.ts
// read goes on until it has them: one that joined between two PATs, or a station that
// restarted under it, is not a stream without them). The bytes may come in any pieces.
type PSIScan struct {
	tail     []byte
	pmtPID   int
	PAT, PMT bool
}

// Feed scans b and reports whether a PAT and then its PMT were seen so far.
func (p *PSIScan) Feed(b []byte) bool {
	if p.PAT && p.PMT {
		return true
	}
	buf := append(p.tail, b...)
	i := 0
	for i+tsPacket <= len(buf) {
		if buf[i] != 0x47 {
			i++
			continue
		}
		p.packet(buf[i : i+tsPacket])
		i += tsPacket
	}
	p.tail = append(p.tail[:0], buf[i:]...)
	return p.PAT && p.PMT
}

func (p *PSIScan) packet(pkt []byte) {
	pid := int(pkt[1]&0x1f)<<8 | int(pkt[2])
	if pkt[1]&0x40 == 0 || (pid != 0 && (!p.PAT || pid != p.pmtPID)) {
		return
	}
	afc := (pkt[3] >> 4) & 3
	if afc&1 == 0 {
		return
	}
	off := 4
	if afc&2 != 0 {
		off += 1 + int(pkt[4])
	}
	if off >= tsPacket {
		return
	}
	s := section(pkt[off:])
	switch {
	case pid == 0 && len(s) >= 12 && s[0] == 0:
		for i := 8; i+4 <= len(s)-4; i += 4 {
			if prog := int(s[i])<<8 | int(s[i+1]); prog != 0 {
				p.pmtPID, p.PAT = int(s[i+2]&0x1f)<<8|int(s[i+3]), true
				return
			}
		}
	case pid == p.pmtPID && len(s) >= 16 && s[0] == 2:
		p.PMT = true
	}
}
