package channel

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Program date-times from the timestamps (story 33.4 AC4). ffmpeg's HLS muxer
// dates a segment by the clock at its own start plus the durations before it,
// so two muxers of the same channel (two TV slots) disagree by however far
// apart they started. The segments' PTS are the wall clock (align.go), so the
// playlist served dates each segment by its first video PTS instead: two slots
// then give the same segment the same EXT-X-PROGRAM-DATE-TIME to the
// millisecond, and the front door can line them up (design §8.4).

const pdtTag = "#EXT-X-PROGRAM-DATE-TIME:"

// pdtLayout is the date-time written (UTC, milliseconds).
const pdtLayout = "2006-01-02T15:04:05.000Z"

// pdtCache remembers each segment file's first PTS.
type pdtCache struct {
	mu sync.Mutex
	m  map[string]int64
}

func (c *pdtCache) firstPTS(path string) (int64, bool) {
	c.mu.Lock()
	if v, ok := c.m[path]; ok {
		c.mu.Unlock()
		return v, true
	}
	c.mu.Unlock()
	pts, ok := segmentFirstPTS(path)
	if !ok {
		return 0, false
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]int64{}
	}
	c.m[path] = pts
	c.mu.Unlock()
	return pts, true
}

// prune forgets segments no longer listed in dir.
func (c *pdtCache) prune(dir string, listed map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.m {
		if filepath.Dir(k) == dir && !listed[k] {
			delete(c.m, k)
		}
	}
}

// segmentFirstPTS reads the first video PES timestamp of a TS segment.
func segmentFirstPTS(path string) (int64, bool) {
	f, err := openShared(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	buf := make([]byte, 128*tsPacket)
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]
	var anyPTS int64
	haveAny := false
	for i := 0; i+tsPacket <= len(buf); i += tsPacket {
		p := buf[i : i+tsPacket]
		if p[0] != 0x47 {
			continue
		}
		pts, ok := packetPTS(p)
		if !ok {
			continue
		}
		if int(p[1]&0x1f)<<8|int(p[2]) == TSVideoPID {
			return pts, true
		}
		if !haveAny {
			anyPTS, haveAny = pts, true
		}
	}
	return anyPTS, haveAny
}

// rewritePDT re-dates every segment of an HLS media playlist in dir by its
// first PTS. A segment it cannot read keeps the muxer's date.
func (c *pdtCache) rewritePDT(dir string, body []byte, now time.Time) []byte {
	if !bytes.Contains(body, []byte(pdtTag)) {
		return body
	}
	lines := strings.Split(string(body), "\n")
	pending := -1
	listed := map[string]bool{}
	for i, raw := range lines {
		l := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(l, pdtTag):
			pending = i
		case l == "" || strings.HasPrefix(l, "#"):
		default:
			if !safeComponent(l) {
				pending = -1
				continue
			}
			path := filepath.Join(dir, l)
			listed[path] = true
			if pending >= 0 {
				if pts, ok := c.firstPTS(path); ok {
					lines[pending] = pdtTag + PTSToWall(pts, now).UTC().Format(pdtLayout)
				}
			}
			pending = -1
		}
	}
	c.prune(dir, listed)
	return []byte(strings.Join(lines, "\n"))
}
