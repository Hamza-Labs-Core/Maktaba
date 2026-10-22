package handlers

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// availabilityFile keeps, in a DASH session's folder, the
// availabilityStartTime of the dynamic MPD ffmpeg writes while it transcodes:
// its final, static MPD has none.
const availabilityFile = ".availability-start"

// startMargin is how much further back than the transcode's start the served
// suggestedPresentationDelay reaches (the live edge moves on while a player
// starts).
const startMargin = time.Minute

// finalUpdates is the margin before a finished transcode's MPD turns static:
// a player updating it every minimumUpdatePeriod has had the whole timeline
// (and, by its clock, every segment is available).
const finalUpdates = 15 * time.Second

// mpdTimeFormat is the MPD's xs:dateTime as ffmpeg writes it.
const mpdTimeFormat = "2006-01-02T15:04:05.000Z"

var (
	mpdTag   = regexp.MustCompile(`<MPD\b[^>]*>`)
	mpdType  = regexp.MustCompile(`\btype="(dynamic|static)"`)
	mpdAST   = regexp.MustCompile(`\savailabilityStartTime="([^"]*)"`)
	mpdDelay = regexp.MustCompile(`\s+suggestedPresentationDelay="[^"]*"`)
	// maxSegmentDuration, as ffmpeg's dynamic MPD has its minimumUpdatePeriod
	mpdMaxSegment = regexp.MustCompile(`\smaxSegmentDuration="([^"]*)"`)
	mpdDuration   = regexp.MustCompile(`\smediaPresentationDuration="([^"]*)"`)
	// an xs:duration as ffmpeg writes one: PT1H2M3.4S, its parts optional
	isoDuration = regexp.MustCompile(`^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?$`)
)

// transcodeMPD serves a transcode's MPD (written at written) so that DASH
// players start at its first segment and play to its end:
//
//   - While ffmpeg runs, its MPD is dynamic, and a player starts its
//     suggestedPresentationDelay before the live edge (no earlier than the
//     first segment): the served delay reaches back past the transcode's
//     start. Its availabilityStartTime is kept beside it (availabilityFile).
//   - When ffmpeg is done, its MPD turns static. A player that had it dynamic
//     ends when it turns static (dash.js: DASH-IF IOP 4.6.4), after the
//     segments of the timeline it last had that are available by then (its
//     start plus each one's end): straight from ffmpeg's last dynamic MPD, or
//     while the transcode is ahead of the clock, it would stop short. So once
//     a player may have had it dynamic (the kept start), it stays dynamic (the
//     kept start, the static MPD's timeline and mediaPresentationDuration,
//     still updated) until every segment is available and the whole timeline
//     has been out for finalUpdates. Then, and for an MPD nobody had dynamic,
//     it is served static.
func transcodeMPD(data []byte, dir string, written, now time.Time) []byte {
	loc := mpdTag.FindIndex(data)
	if loc == nil {
		return data
	}
	tag := string(data[loc[0]:loc[1]])
	m := mpdType.FindStringSubmatch(tag)
	if m == nil {
		return data
	}
	keep := filepath.Join(dir, availabilityFile)
	dynamic := m[1] == "dynamic"
	var ast string
	if dynamic {
		a := mpdAST.FindStringSubmatch(tag)
		if a == nil {
			return data
		}
		ast = a[1]
	} else {
		b, err := os.ReadFile(keep)
		if err != nil {
			return data // nobody had it dynamic
		}
		ast = strings.TrimSpace(string(b))
	}
	start, err := time.Parse(time.RFC3339Nano, ast)
	if err != nil {
		return data
	}
	if dynamic {
		if _, err := os.Stat(keep); err != nil {
			keepAvailability(keep, ast)
		}
	} else {
		d := mpdDuration.FindStringSubmatch(tag)
		if d == nil {
			return data
		}
		duration, ok := parseISODuration(d[1])
		if !ok {
			return data
		}
		end := start.Add(duration) // the last segment available
		if written.After(end) {
			end = written
		}
		if !now.Before(end.Add(finalUpdates)) {
			return data
		}
		// still updated: the switch to static is what ends a player
		update := "PT6S"
		if s := mpdMaxSegment.FindStringSubmatch(tag); s != nil {
			update = s[1]
		}
		tag = mpdType.ReplaceAllLiteralString(tag, `type="dynamic"`+
			"\n\tminimumUpdatePeriod=\""+update+"\""+
			"\n\tavailabilityStartTime=\""+ast+"\""+
			"\n\tpublishTime=\""+now.UTC().Format(mpdTimeFormat)+"\"")
	}
	delay := int(math.Ceil((now.Sub(start) + startMargin).Seconds()))
	tag = mpdDelay.ReplaceAllLiteralString(tag, "")
	tag = strings.Replace(tag, `type="dynamic"`, fmt.Sprintf("type=\"dynamic\"\n\tsuggestedPresentationDelay=\"PT%dS\"", delay), 1)
	out := make([]byte, 0, len(data)+len(tag)-(loc[1]-loc[0]))
	out = append(out, data[:loc[0]]...)
	out = append(out, tag...)
	return append(out, data[loc[1]:]...)
}

// parseISODuration reads an xs:duration as ffmpeg writes one (PT1H2M3.4S).
func parseISODuration(s string) (time.Duration, bool) {
	m := isoDuration.FindStringSubmatch(s)
	if m == nil || s == "PT" {
		return 0, false
	}
	var d time.Duration
	for i, unit := range []time.Duration{time.Hour, time.Minute} {
		if m[i+1] != "" {
			n, err := strconv.Atoi(m[i+1])
			if err != nil {
				return 0, false
			}
			d += time.Duration(n) * unit
		}
	}
	if m[3] != "" {
		f, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			return 0, false
		}
		d += time.Duration(f * float64(time.Second))
	}
	return d, true
}

// keepAvailability writes the start whole or not at all (two players' first
// requests write the same one).
func keepAvailability(path, ast string) {
	tmp := fmt.Sprintf("%s.%d.part", path, os.Getpid())
	if err := os.WriteFile(tmp, []byte(ast), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}
