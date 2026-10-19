package channel

import (
	"strconv"
	"time"
)

// Splice-ready output (Epic 33 story 33.4, design §5). Every station of a
// channel, in any slot on any host, stamps the same timestamps and puts its
// keyframes at the same instants, so the output of two stations airing the
// same channel can be spliced frame-exactly:
//
//   - the channel timeline epoch is the Unix epoch: a frame's PTS is its
//     wall-clock instant in 90 kHz ticks, modulo 2^33 (a wrap every ~26.5 h);
//   - a block starts on the wall-clock frame grid, at a multiple of
//     FrameQuantum, which is a whole number of frames, microseconds and 90 kHz
//     ticks, so the -output_ts_offset handed to ffmpeg is exact;
//   - IDRs are forced on wall-clock multiples of the keyframe interval
//     (KeyframeExpr), whatever instant a block started at;
//   - the PMT, video and audio PIDs and the service id are fixed in the
//     arguments (TSPMTPID, TSVideoPID, TSAudioPID, TSServiceID).

// Fixed MPEG-TS layout of every station's output (story 33.4 AC3). Variables
// only so that a deliberately misbuilt binary (BuildDefect) can be made for the
// staging verification tests; nothing assigns them otherwise.
var (
	TSServiceID = 1
	TSPMTPID    = 0x1000
	TSVideoPID  = 0x100 // the first elementary stream (-map video first)
	TSAudioPID  = 0x101
)

// BuildDefect, set only with -ldflags "-X .../channel.BuildDefect=...", builds a
// broken streaming binary on purpose so the staging verification of a TV release
// (Epic 33 story 33.5) can be shown to catch it: "pids" moves the PMT and the
// elementary stream PIDs (0x1100, 0x200, 0x201), "pts:<ms>" shifts every
// timestamp off the wall clock by ms. It takes effect only in a build with the
// tvdefect tag (align_defect_on.go); any other build refuses to start with it
// set (align_defect_off.go), so a release build can never ship it.
var BuildDefect = ""

// ptsShiftTicks is BuildDefect's timestamp shift (0: none).
var ptsShiftTicks int64

// PTSWrapTicks is the 33-bit PTS modulus.
const PTSWrapTicks = int64(1) << 33

// lowOffsetTicks: a block whose offset falls this close after a PTS wrap gets
// the offset of one wrap later (the same 33-bit value): ffmpeg's PCR, a little
// before the DTS, then never goes below zero.
const lowOffsetTicks = 3 * 90000

// DefaultKeyintSeconds is tv_output.keyint_seconds' default.
const DefaultKeyintSeconds = 2

// FrameQuantum is the step block starts are aligned to: the shortest whole
// number of frames that is also a whole number of microseconds and of 90 kHz
// ticks (100 ms at 30 fps, 40 ms at 25 fps).
func FrameQuantum(fps int) time.Duration {
	if fps <= 0 {
		fps = DefaultProfile().FPS
	}
	k := 1
	for ; k <= fps; k++ {
		if (k*1_000_000)%fps == 0 && (k*90_000)%fps == 0 {
			break
		}
	}
	return time.Duration(int64(k) * int64(time.Second) / int64(fps))
}

// AlignStart rounds t up to the wall-clock frame grid (a multiple of
// FrameQuantum since the Unix epoch).
func AlignStart(t time.Time, fps int) time.Time {
	q := int64(FrameQuantum(fps))
	ns := t.UnixNano()
	if r := ns % q; r != 0 {
		ns += q - r
	}
	return time.Unix(0, ns)
}

// WallTicks is t in 90 kHz ticks since the Unix epoch (exact for an aligned t).
func WallTicks(t time.Time) int64 {
	return t.Unix()*90000 + int64(t.Nanosecond())*9/100000
}

// TSOffsetTicks is the -output_ts_offset of a block starting at t: its
// wall-clock ticks modulo 2^33 (one wrap later when just past a wrap).
func TSOffsetTicks(t time.Time) int64 {
	off := (WallTicks(t) + ptsShiftTicks) % PTSWrapTicks
	if off < lowOffsetTicks {
		off += PTSWrapTicks
	}
	return off
}

// TSOffsetSeconds renders an offset in ticks for ffmpeg (microseconds: the
// round trip through ffmpeg's microsecond parse back to 90 kHz is exact).
func TSOffsetSeconds(ticks int64) string {
	return strconv.FormatFloat(float64(ticks)/90000, 'f', 6, 64)
}

// KeyframePhase is how far into a block starting at t its first grid IDR is:
// (0, k] seconds — a block starting on the grid gets its next one k later
// (its first frame is an IDR anyway).
func KeyframePhase(t time.Time, k, fps int) float64 {
	if k <= 0 {
		k = DefaultKeyintSeconds
	}
	kns := int64(k) * int64(time.Second)
	r := t.UnixNano() % kns
	half := int64(time.Second) / int64(2*maxInt(fps, 1))
	if r < half || kns-r < half {
		return float64(k)
	}
	return float64(kns-r) / float64(time.Second)
}

// KeyframeExpr is the -force_key_frames expression for IDRs on the wall-clock
// grid of k seconds in a block whose first grid IDR is phase seconds in: the
// first frame (n_forced = 0), then phase, phase + k, ... (half a frame of
// slack against float rounding).
func KeyframeExpr(phase float64, k, fps int) string {
	if k <= 0 {
		k = DefaultKeyintSeconds
	}
	lead := phase - float64(k) - 0.5/float64(maxInt(fps, 1))
	return "expr:gte(t,n_forced*" + itoa(k) + "+(" + strconv.FormatFloat(lead, 'f', 6, 64) + "))"
}

// PTSToWall is the wall-clock instant of a 33-bit PTS: the instant nearest now
// whose ticks are congruent to it.
func PTSToWall(pts int64, now time.Time) time.Time {
	nowTicks := WallTicks(now)
	d := (pts - nowTicks) % PTSWrapTicks
	if d < 0 {
		d += PTSWrapTicks
	}
	if d > PTSWrapTicks/2 {
		d -= PTSWrapTicks
	}
	ticks := nowTicks + d
	sec := ticks / 90000
	rem := ticks % 90000
	return time.Unix(sec, rem*100000/9)
}

// OnGrid reports whether a PTS falls on a wall-clock multiple of seconds
// (within two ticks).
func OnGrid(pts int64, now time.Time, seconds int) bool {
	if seconds <= 0 {
		return true
	}
	nowTicks := WallTicks(now)
	d := (pts - nowTicks) % PTSWrapTicks
	if d < 0 {
		d += PTSWrapTicks
	}
	if d > PTSWrapTicks/2 {
		d -= PTSWrapTicks
	}
	step := int64(seconds) * 90000
	r := (nowTicks + d) % step
	if r < 0 {
		r += step
	}
	return r <= 2 || step-r <= 2
}

// effectiveKeyint is the keyframe interval a profile uses: the setting (1–10 s,
// default 2) or, when it does not divide the segment length, their greatest
// common divisor, so every segment boundary on the grid has an IDR.
func effectiveKeyint(keyint, segment int) int {
	if keyint <= 0 {
		keyint = DefaultKeyintSeconds
	}
	if keyint > 10 {
		keyint = 10
	}
	if segment > 0 && segment%keyint != 0 {
		a, b := segment, keyint
		for b != 0 {
			a, b = b, a%b
		}
		keyint = a
	}
	return keyint
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// tsVideoKeyframe reports a TS packet that starts a video PES with an IDR
// (random access indicator, or an IDR / SPS NAL in its payload) and its PTS.
func tsVideoKeyframe(p []byte) (int64, bool) {
	if len(p) != tsPacket || p[0] != 0x47 || p[1]&0x40 == 0 {
		return 0, false
	}
	if int(p[1]&0x1f)<<8|int(p[2]) != TSVideoPID {
		return 0, false
	}
	pts, ok := packetPTS(p)
	if !ok {
		return 0, false
	}
	afc := (p[3] >> 4) & 3
	if afc&2 != 0 && p[4] > 0 && p[5]&0x40 != 0 {
		return pts, true
	}
	off := 4
	if afc&2 != 0 {
		off += 1 + int(p[4])
	}
	if off+9 > len(p) {
		return 0, false
	}
	h := off + 9 + int(p[off+8])
	for i := h; i+3 < len(p); i++ {
		if p[i] == 0 && p[i+1] == 0 && p[i+2] == 1 {
			switch p[i+3] & 0x1f {
			case 5, 7:
				return pts, true
			}
		}
	}
	return 0, false
}

// EffectiveKeyint is the IDR grid a profile with these settings uses (story
// 33.5 checks the output against it).
func EffectiveKeyint(keyint, segment int) int { return effectiveKeyint(keyint, segment) }
