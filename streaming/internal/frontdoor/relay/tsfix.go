package relay

import (
	"bytes"
	"errors"
)

// The relay feeds one long-lived ffmpeg (stream copy to RTMPS/SRT) from a
// channel's MPEG-TS, whatever station produces it. When the source changes
// (a failover or a move to another host now; an Epic 33 swap or rollback
// later) the new station's timestamps start somewhere else entirely. The
// Retimer makes the stream ffmpeg sees one continuous program (Epic 33 story
// 33.8 AC2-AC3):
//
//   - after Switch it drops the new source's packets until its PAT/PMT and a
//     video keyframe (IDR) start, and joins there;
//   - it shifts every PTS, DTS and PCR of the new source by one offset so the
//     first joined frame lands one frame after the last frame sent: output
//     timestamps stay monotonic with a gap of at most one frame;
//   - audio packets that would land before the last audio sent are dropped
//     (whole PES), so audio stays monotonic too;
//   - a jump inside one source (a muxer restart) is evened out the same way;
//   - PIDs of a new source are mapped onto the first source's (by stream
//     type), the first source's PAT/PMT stand in for the new ones, and
//     continuity counters run on per output PID.
//
// A new source whose streams do not match the first (another codec, a
// missing audio track) is a parameter change: Process returns
// ErrParamChange and the relay reconnects (33.8 AC7).

const (
	tsSize   = 188
	tsSync   = 0x47
	ptsWrap  = int64(1) << 33
	ptsHalf  = int64(1) << 32
	clock90k = 90000
	// defaultFrame is one frame at 30 fps (90 kHz) until the stream tells.
	defaultFrame = int64(3000)
	// maxJump: a forward jump of more than this inside a source is a discontinuity.
	maxJump = 10 * clock90k
)

// ErrParamChange: the new source's streams differ from the first source's.
var ErrParamChange = errors.New("relay: the new source's streams differ (parameter change)")

// stream types
const (
	stH264 = 0x1B
	stHEVC = 0x24
	stAAC  = 0x0F
	stAC3  = 0x81
)

func isVideo(t byte) bool { return t == stH264 || t == stHEVC || t == 0x02 || t == 0x10 }

type esInfo struct {
	pid uint16
	typ byte
}

// Retimer rewrites a channel's TS into one continuous program.
type Retimer struct {
	// output program (the first source's)
	outPMT   uint16
	outES    []esInfo // in PMT order
	outVideo uint16
	patPkt   []byte
	pmtPkt   []byte

	// current source
	srcPMT    uint16
	srcES     []esInfo
	pidMap    map[uint16]uint16 // source pid -> output pid
	srcVideo  uint16
	haveSrc   bool // PMT of the current source seen
	needIDR   bool
	unwrapPTS map[uint16]int64
	unwrapPCR map[uint16]int64
	dropPES   map[uint16]bool

	offset   int64
	last     map[uint16]int64 // output pid -> last output DTS (unwrapped)
	lastV    int64
	haveV    bool
	frame    int64
	cc       map[uint16]byte
	pending  []byte // a partial packet between writes
	out      bytes.Buffer
	switches int
	jumps    int
}

// NewRetimer starts with no program; the first source defines it.
func NewRetimer() *Retimer {
	r := &Retimer{frame: defaultFrame, last: map[uint16]int64{}, cc: map[uint16]byte{}}
	r.resetSource()
	return r
}

func (r *Retimer) resetSource() {
	r.haveSrc, r.needIDR = false, true
	r.srcPMT, r.srcES, r.srcVideo = 0, nil, 0
	r.pidMap = map[uint16]uint16{}
	r.unwrapPTS = map[uint16]int64{}
	r.unwrapPCR = map[uint16]int64{}
	r.dropPES = map[uint16]bool{}
	r.pending = r.pending[:0]
}

// Switch announces that the next bytes come from a new source (another
// station, host or slot): the retimer waits for its PAT/PMT and an IDR.
func (r *Retimer) Switch() {
	r.resetSource()
	r.switches++
}

// Resync keeps the source but waits for its next IDR (a restarted ffmpeg
// must begin on a keyframe, after the PAT/PMT).
func (r *Retimer) Resync() {
	r.needIDR = true
	r.dropPES = map[uint16]bool{}
}

// Switches is how many source switches happened; Jumps how many in-source
// discontinuities were evened out.
func (r *Retimer) Switches() int { return r.switches }

// Jumps is the count of in-source timestamp discontinuities evened out.
func (r *Retimer) Jumps() int { return r.jumps }

// Joined reports whether the current source is being forwarded.
func (r *Retimer) Joined() bool { return r.haveSrc && !r.needIDR }

// Process takes source bytes (any chunking) and returns the rewritten TS
// (whole packets). The returned slice is valid until the next call.
func (r *Retimer) Process(b []byte) ([]byte, error) {
	r.out.Reset()
	if len(r.pending) > 0 {
		need := tsSize - len(r.pending)
		if len(b) < need {
			r.pending = append(r.pending, b...)
			return nil, nil
		}
		r.pending = append(r.pending, b[:need]...)
		b = b[need:]
		pkt := append([]byte(nil), r.pending...)
		r.pending = r.pending[:0]
		if err := r.packet(pkt); err != nil {
			return nil, err
		}
	}
	for len(b) > 0 {
		if b[0] != tsSync {
			// resync: the next 0x47 followed by another one a packet later
			i := 1
			for ; i < len(b); i++ {
				if b[i] == tsSync && (i+tsSize >= len(b) || b[i+tsSize] == tsSync) {
					break
				}
			}
			b = b[i:]
			continue
		}
		if len(b) < tsSize {
			r.pending = append(r.pending[:0], b...)
			break
		}
		pkt := append([]byte(nil), b[:tsSize]...)
		b = b[tsSize:]
		if err := r.packet(pkt); err != nil {
			return nil, err
		}
	}
	return r.out.Bytes(), nil
}

func pidOf(p []byte) uint16 { return uint16(p[1]&0x1f)<<8 | uint16(p[2]) }

func setPID(p []byte, pid uint16) {
	p[1] = p[1]&0xe0 | byte(pid>>8)&0x1f
	p[2] = byte(pid)
}

// payloadStart is the offset of the payload (-1: none).
func payloadStart(p []byte) int {
	afc := (p[3] >> 4) & 3
	off := 4
	if afc&2 != 0 {
		off = 5 + int(p[4])
	}
	if afc&1 == 0 || off >= tsSize {
		return -1
	}
	return off
}

func hasAdaptation(p []byte) bool { return (p[3]>>4)&2 != 0 && p[4] > 0 }

// section returns a PSI section's body (after the pointer field) or nil.
func section(p []byte) []byte {
	if p[1]&0x40 == 0 {
		return nil
	}
	off := payloadStart(p)
	if off < 0 {
		return nil
	}
	ptr := int(p[off])
	s := off + 1 + ptr
	if s+3 > tsSize {
		return nil
	}
	n := int(p[s+1]&0x0f)<<8 | int(p[s+2])
	end := s + 3 + n
	if end > tsSize {
		end = tsSize
	}
	return p[s:end]
}

func parsePAT(sec []byte) uint16 {
	if len(sec) < 12 || sec[0] != 0x00 {
		return 0
	}
	body := sec[8 : len(sec)-4]
	for i := 0; i+4 <= len(body); i += 4 {
		prog := uint16(body[i])<<8 | uint16(body[i+1])
		if prog != 0 {
			return uint16(body[i+2]&0x1f)<<8 | uint16(body[i+3])
		}
	}
	return 0
}

func parsePMT(sec []byte) []esInfo {
	if len(sec) < 16 || sec[0] != 0x02 {
		return nil
	}
	infoLen := int(sec[10]&0x0f)<<8 | int(sec[11])
	i := 12 + infoLen
	end := len(sec) - 4
	var out []esInfo
	for i+5 <= end {
		t := sec[i]
		pid := uint16(sec[i+1]&0x1f)<<8 | uint16(sec[i+2])
		esLen := int(sec[i+3]&0x0f)<<8 | int(sec[i+4])
		out = append(out, esInfo{pid: pid, typ: t})
		i += 5 + esLen
	}
	return out
}

// keyframe reports whether a video packet starts an IDR access unit: the
// random access indicator, or an IDR/SPS NAL in its first payload.
func keyframe(p []byte) bool {
	if hasAdaptation(p) && p[5]&0x40 != 0 {
		return true
	}
	off := payloadStart(p)
	if off < 0 || off+9 > tsSize {
		return false
	}
	h := off + 9 + int(p[off+8])
	for i := h; i+3 < tsSize; i++ {
		if p[i] == 0 && p[i+1] == 0 && p[i+2] == 1 {
			switch p[i+3] & 0x1f {
			case 5, 7:
				return true
			}
		}
	}
	return false
}

// pesTimes reads a PES header's PTS/DTS (dts = pts when there is no DTS).
func pesTimes(p []byte) (pts, dts int64, hasDTS, ok bool) {
	off := payloadStart(p)
	if off < 0 || off+14 > tsSize || p[off] != 0 || p[off+1] != 0 || p[off+2] != 1 {
		return 0, 0, false, false
	}
	flags := p[off+7] >> 6
	if flags&2 == 0 {
		return 0, 0, false, false
	}
	pts = readTS(p[off+9:])
	dts = pts
	if flags == 3 && off+19 <= tsSize {
		dts = readTS(p[off+14:])
		hasDTS = true
	}
	return pts, dts, hasDTS, true
}

func readTS(b []byte) int64 {
	return int64(b[0]>>1&0x07)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1)
}

func writeTS(b []byte, v int64) {
	v &= ptsWrap - 1
	b[0] = b[0]&0xf0 | byte(v>>29)&0x0e | 1
	b[1] = byte(v >> 22)
	b[2] = byte(v>>14)&0xfe | 1
	b[3] = byte(v >> 7)
	b[4] = byte(v<<1) | 1
}

func setPESTimes(p []byte, pts, dts int64, hasDTS bool) {
	off := payloadStart(p)
	writeTS(p[off+9:], pts)
	if hasDTS {
		writeTS(p[off+14:], dts)
	}
}

// pcr reads an adaptation field's PCR base (90 kHz) and extension.
func pcr(p []byte) (base int64, ext int, ok bool) {
	if !hasAdaptation(p) || p[5]&0x10 == 0 || p[4] < 7 {
		return 0, 0, false
	}
	b := p[6:12]
	base = int64(b[0])<<25 | int64(b[1])<<17 | int64(b[2])<<9 | int64(b[3])<<1 | int64(b[4]>>7)
	ext = int(b[4]&1)<<8 | int(b[5])
	return base, ext, true
}

func setPCR(p []byte, base int64, ext int) {
	base &= ptsWrap - 1
	b := p[6:12]
	b[0] = byte(base >> 25)
	b[1] = byte(base >> 17)
	b[2] = byte(base >> 9)
	b[3] = byte(base >> 1)
	b[4] = byte(base<<7) | 0x7e | byte(ext>>8)&1
	b[5] = byte(ext)
}

// unwrap turns a 33-bit timestamp into a continuous 64-bit one near prev.
func unwrap(m map[uint16]int64, pid uint16, raw int64) int64 {
	prev, ok := m[pid]
	if !ok {
		m[pid] = raw
		return raw
	}
	d := (raw - prev) % ptsWrap
	if d < 0 {
		d += ptsWrap
	}
	if d > ptsHalf {
		d -= ptsWrap
	}
	u := prev + d
	m[pid] = u
	return u
}

func (r *Retimer) emit(p []byte, outPID uint16) {
	setPID(p, outPID)
	if (p[3]>>4)&1 != 0 { // a payload: the counter advances
		c, seen := r.cc[outPID]
		if seen {
			c = (c + 1) & 0x0f
		} else {
			c = p[3] & 0x0f
		}
		r.cc[outPID] = c
		p[3] = p[3]&0xf0 | c
	}
	r.out.Write(p)
}

func (r *Retimer) emitHeaders() {
	if r.patPkt != nil {
		r.emit(append([]byte(nil), r.patPkt...), 0)
	}
	if r.pmtPkt != nil {
		r.emit(append([]byte(nil), r.pmtPkt...), r.outPMT)
	}
}

func (r *Retimer) packet(p []byte) error {
	pid := pidOf(p)
	switch {
	case pid == 0:
		if pmt := parsePAT(section(p)); pmt != 0 {
			r.srcPMT = pmt
			if r.patPkt == nil {
				r.patPkt = append([]byte(nil), p...)
				r.outPMT = pmt
			}
		}
		if r.Joined() {
			r.emitPSI(p, 0)
		}
		return nil
	case r.srcPMT != 0 && pid == r.srcPMT:
		if es := parsePMT(section(p)); es != nil && !r.haveSrc {
			if err := r.mapSource(es, p); err != nil {
				return err
			}
		}
		if r.Joined() {
			r.emitPSI(p, r.outPMT)
		}
		return nil
	case pid < 0x20 || pid == 0x1fff:
		if r.Joined() && pid != 0x1fff {
			r.emit(p, pid) // SDT/NIT etc. pass through
		}
		return nil
	}
	if !r.haveSrc {
		return nil
	}
	out, ok := r.pidMap[pid]
	if !ok {
		return nil
	}
	pusi := p[1]&0x40 != 0
	if r.needIDR {
		if pid != r.srcVideo || !pusi || !keyframe(p) {
			return nil
		}
		_, dts, _, okT := pesTimes(p)
		if !okT {
			return nil
		}
		u := unwrap(r.unwrapPTS, pid, dts)
		if r.haveV {
			r.offset = r.lastV + r.frame - u
		}
		r.needIDR = false
		r.emitHeaders()
		for in := range r.pidMap {
			if in != r.srcVideo {
				r.dropPES[in] = true // join audio at its next PES start
			}
		}
	}
	if pusi {
		pts, dts, hasDTS, okT := pesTimes(p)
		if okT {
			if _, seen := r.unwrapPTS[pid]; !seen && pid != r.srcVideo {
				// unwrap a new audio PID near the video's clock, not from its own
				// first value: one on the other side of a 33-bit wrap from the
				// joined keyframe would land 2^33 off (all its audio dropped)
				if v, ok := r.unwrapPTS[r.srcVideo]; ok {
					r.unwrapPTS[pid] = v
				}
			}
			up := unwrap(r.unwrapPTS, pid, dts)
			ptsDelta := (pts - dts) % ptsWrap
			if ptsDelta > ptsHalf {
				ptsDelta -= ptsWrap
			} else if ptsDelta < -ptsHalf {
				ptsDelta += ptsWrap
			}
			o := up + r.offset
			if pid == r.srcVideo {
				if r.haveV && (o <= r.lastV || o > r.lastV+maxJump) {
					r.offset += r.lastV + r.frame - o
					o = r.lastV + r.frame
					r.jumps++
				}
				if r.haveV {
					if d := o - r.lastV; d > 0 && d <= clock90k/5 {
						r.frame = d
					}
				}
				r.lastV, r.haveV = o, true
			} else {
				if last, seen := r.last[out]; seen && o <= last {
					r.dropPES[pid] = true
					return nil
				}
				r.dropPES[pid] = false
			}
			r.last[out] = o
			setPESTimes(p, o+ptsDelta, o, hasDTS)
		}
	} else if r.dropPES[pid] {
		return nil
	}
	if base, ext, okP := pcr(p); okP {
		u := unwrap(r.unwrapPCR, pid, base)
		setPCR(p, u+r.offset, ext)
	}
	if hasAdaptation(p) {
		p[5] &^= 0x80 // no discontinuity: the program is continuous
	}
	r.emit(p, out)
	return nil
}

// emitPSI passes a PAT/PMT: the source's own when its layout is the output's,
// else the first source's.
func (r *Retimer) emitPSI(p []byte, outPID uint16) {
	if r.identity() {
		r.emit(p, outPID)
		return
	}
	if outPID == 0 && r.patPkt != nil {
		r.emit(append([]byte(nil), r.patPkt...), 0)
	} else if r.pmtPkt != nil {
		r.emit(append([]byte(nil), r.pmtPkt...), r.outPMT)
	}
}

func (r *Retimer) identity() bool {
	if r.srcPMT != r.outPMT {
		return false
	}
	for in, out := range r.pidMap {
		if in != out {
			return false
		}
	}
	return true
}

// mapSource maps a new source's streams onto the output program (the first
// source defines it).
func (r *Retimer) mapSource(es []esInfo, pmt []byte) error {
	if r.outES == nil {
		r.outES = es
		r.pmtPkt = append([]byte(nil), pmt...)
		for _, e := range es {
			if isVideo(e.typ) && r.outVideo == 0 {
				r.outVideo = e.pid
			}
		}
	}
	m := map[uint16]uint16{}
	used := map[uint16]bool{}
	video := uint16(0)
	for _, e := range es {
		found := false
		for _, o := range r.outES {
			if o.typ == e.typ && !used[o.pid] {
				m[e.pid] = o.pid
				used[o.pid] = true
				found = true
				break
			}
		}
		if !found {
			return ErrParamChange
		}
		if isVideo(e.typ) && video == 0 {
			video = e.pid
		}
	}
	if len(m) != len(r.outES) || video == 0 {
		return ErrParamChange
	}
	r.pidMap, r.srcES, r.srcVideo, r.haveSrc = m, es, video, true
	return nil
}
