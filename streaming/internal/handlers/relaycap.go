package handlers

import (
	"bytes"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// RelayCapHeader is the bitrate Maktaba Cloud's relay asks a capped plan's media to fit (Epic
// 50 story 50.19 §3). The web port keeps it only on a request the relay agent forwarded.
const RelayCapHeader = "X-Maktaba-Relay-Cap-Kbps"

// relayCapBits is the highest BANDWIDTH (bits a second) a relayed request's renditions may have:
// the cap less 10 % headroom; 0 when the request is not capped.
func relayCapBits(r *http.Request) int64 {
	if r.Header.Get("X-Maktaba-Via") != "relay" {
		return 0
	}
	n, err := strconv.ParseUint(strings.TrimSpace(r.Header.Get(RelayCapHeader)), 10, 32)
	if err != nil || n == 0 || n > 1_000_000 {
		return 0
	}
	return int64(n) * 1000 * 100 / 110
}

var streamInfBandwidth = regexp.MustCompile(`(?:^|[:,])BANDWIDTH=(\d+)`)

// FitMasterToCap lists only the variants of an HLS master playlist whose BANDWIDTH fits maxBits
// (the session was opened at the cap, so normally all do; this holds a plan changed mid-stream
// to its new cap at the next fetch). When none fits, the lowest stays: the relay's pacing then
// slows it, never stops it. Other lines (media groups, subtitles) are kept.
func FitMasterToCap(master []byte, maxBits int64) []byte {
	if maxBits <= 0 {
		return master
	}
	lines := strings.Split(string(master), "\n")
	type variant struct {
		at   int // the EXT-X-STREAM-INF line
		bits int64
	}
	var variants []variant
	for i, l := range lines {
		if strings.HasPrefix(l, "#EXT-X-STREAM-INF:") {
			bits := int64(-1)
			if m := streamInfBandwidth.FindStringSubmatch(l); m != nil {
				if v, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					bits = v
				}
			}
			variants = append(variants, variant{at: i, bits: bits})
		}
	}
	if len(variants) == 0 {
		return master
	}
	keep := map[int]bool{}
	lowest := variants[0]
	for _, v := range variants {
		if v.bits >= 0 && (lowest.bits < 0 || v.bits < lowest.bits) {
			lowest = v
		}
		if v.bits >= 0 && v.bits <= maxBits {
			keep[v.at] = true
		}
	}
	if len(keep) == 0 {
		keep[lowest.at] = true
	}
	var out bytes.Buffer
	skipURI := false
	for i, l := range lines {
		if strings.HasPrefix(l, "#EXT-X-STREAM-INF:") {
			skipURI = !keep[i]
			if skipURI {
				continue
			}
		} else if skipURI && l != "" && !strings.HasPrefix(l, "#") {
			skipURI = false // the dropped variant's URI
			continue
		}
		out.WriteString(l)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.Bytes()
}
