// Package relay is the home front door's push relay (Epic 32 story 32.7 as
// amended by Epic 33 story 33.8, design §7.4): for every channel pushed to a
// Cloudflare Stream live input it pulls the channel's MPEG-TS from wherever
// the channel is encoded (the home's own station, or a node's signed live.ts)
// and feeds one long-lived ffmpeg that stream-copies it to the input over
// RTMPS or SRT. One RTMPS/SRT session per channel survives failovers and
// moves (and, with 33.8, swaps): the relay switches its source, never its
// connection. No node runs a pusher and no node ever sees an ingest key.
package relay

import (
	"net/url"
	"strconv"
	"strings"
)

// Credentials are a push's ingest endpoint (tv_push_credentials). Key is a
// secret: it is held in memory only and masked in every log line.
type Credentials struct {
	URL      string
	Key      string
	StreamID string
	Ingest   string // rtmps | srt
}

// Audio is how the relay treats audio: copy when it is AAC already, else
// transcode to AAC (Cloudflare accepts AAC only).
type Audio struct {
	Codec string // the channel's output audio codec (tv_output.audio_codec)
	Kbps  int
}

// Args is the relay ffmpeg's command line (no second video encode: video is
// always copied).
func Args(c Credentials, a Audio) []string {
	args := []string{"-hide_banner", "-nostats", "-loglevel", "warning",
		"-fflags", "+genpts", "-f", "mpegts", "-i", "pipe:0",
		"-map", "0:v:0", "-map", "0:a:0?", "-c:v", "copy"}
	if strings.EqualFold(a.Codec, "aac") || a.Codec == "" {
		args = append(args, "-c:a", "copy")
	} else {
		kbps := a.Kbps
		if kbps <= 0 {
			kbps = 160
		}
		args = append(args, "-c:a", "aac", "-b:a", strconv.Itoa(kbps)+"k")
	}
	if c.Ingest == "srt" {
		return append(args, "-f", "mpegts", SRTURL(c))
	}
	return append(args, "-f", "flv", "-flvflags", "no_duration_filesize", RTMPURL(c))
}

// RTMPURL is the input's RTMPS URL with the stream key appended.
func RTMPURL(c Credentials) string {
	u := c.URL
	if !strings.HasSuffix(u, "/") {
		u += "/"
	}
	return u + c.Key
}

// SRTURL is the input's SRT URL in caller mode with its stream id and passphrase.
func SRTURL(c Credentials) string {
	q := url.Values{}
	if c.StreamID != "" {
		q.Set("streamid", c.StreamID)
	}
	if c.Key != "" {
		q.Set("passphrase", c.Key)
	}
	q.Set("mode", "caller")
	sep := "?"
	if strings.Contains(c.URL, "?") {
		sep = "&"
	}
	return c.URL + sep + q.Encode()
}

// Mask replaces every occurrence of the secret in s (log lines).
func Mask(s, secret string) string {
	if secret == "" {
		return s
	}
	s = strings.ReplaceAll(s, secret, "****")
	return strings.ReplaceAll(s, url.QueryEscape(secret), "****")
}

// MaskedArgs is the command line for the log, the key masked.
func MaskedArgs(bin string, args []string, c Credentials) string {
	return Mask(bin+" "+strings.Join(args, " "), c.Key)
}
