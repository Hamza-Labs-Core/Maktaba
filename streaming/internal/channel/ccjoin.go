package channel

// ccJoin keeps a continuous MPEG-TS's continuity counters running on, PID by PID,
// where one source of packets follows another: a muxer restarted inside a station
// (the hub), or a live.ts connection that carries on with a restarted station
// (StreamChannelTS: the staging slot's rolling window stops and restarts stations
// under a read). A fresh muxer counts every PID from its own start, so without this
// a client sees a continuity_counter jump on every PID at the join and no
// discontinuity_indicator (ISO/IEC 13818-1 §2.4.3.3): a continuity error (TV change
// #56, 2026-10-28: "live.ts: 4 continuity errors", one per PID).
//
// Inside one source the counters pass through as the source counts them (shifted by
// that source's offset), so a fault in the source itself still shows. At a join each
// PID's first payload packet takes the counter after the last one sent, and every
// packet of that source on that PID is shifted the same; a packet without payload
// (the counter does not count it) gets the counter of the last payload packet sent.
// The null PID is left alone.
type ccJoin struct {
	pids    map[uint16]*ccPID
	resumed bool // a join happened: counters may be rewritten
}

type ccPID struct {
	out      byte // the last counter sent on a payload packet
	haveOut  bool
	delta    byte // this source's shift
	anchored bool // delta is set for this source
}

func newCCJoin() *ccJoin { return &ccJoin{pids: map[uint16]*ccPID{}} }

// resume marks the start of a new source of packets.
func (j *ccJoin) resume() {
	for _, p := range j.pids {
		p.anchored = false
	}
	j.resumed = true
}

// apply runs b's whole packets through the join. It rewrites b itself when inPlace,
// otherwise a copy, made only when a counter changes (b may be shared with other
// clients); it returns the bytes to send.
func (j *ccJoin) apply(b []byte, inPlace bool) []byte {
	copied := inPlace
	for i := 0; i+tsPacket <= len(b); i += tsPacket {
		if b[i] != 0x47 {
			continue
		}
		pid := uint16(b[i+1]&0x1f)<<8 | uint16(b[i+2])
		afc := (b[i+3] >> 4) & 3
		if pid == 0x1fff || afc == 0 {
			continue
		}
		p := j.pids[pid]
		if p == nil {
			p = &ccPID{}
			j.pids[pid] = p
		}
		in := b[i+3] & 0x0f
		var out byte
		switch {
		case afc&1 != 0: // payload: the counter counts it
			if !p.anchored {
				p.delta, p.anchored = 0, true
				if p.haveOut && j.resumed {
					p.delta = (p.out + 1 - in) & 0x0f
				}
			}
			out = (in + p.delta) & 0x0f
			p.out, p.haveOut = out, true
		case p.anchored:
			out = (in + p.delta) & 0x0f
		case p.haveOut:
			out = p.out
		default:
			continue
		}
		if out == in {
			continue
		}
		if !copied {
			b = append([]byte(nil), b...)
			copied = true
		}
		b[i+3] = b[i+3]&0xf0 | out
	}
	return b
}
