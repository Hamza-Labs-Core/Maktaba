package channel

import (
	"strconv"
	"strings"
)

// ConcatEntry is one input file in the concat-demuxer list, trimmed to
// the source slice this block plays.
type ConcatEntry struct {
	Path        string
	InpointSec  float64 // seek into the source (offset, or join offset for the head)
	OutpointSec float64 // where to stop reading the source
}

// BuildConcat turns a located join + the look-ahead blocks into the
// concat-input entries. The head block is seeked to the join offset; the
// rest play from their own source offset. Only blocks with a resolved
// Path participate (a missing file is skipped, never a user string —
// threat model). At most `lookahead` blocks (including the head) are
// included so the demuxer always has the next program queued (D7) without
// pinning the whole 48 h schedule.
func BuildConcat(blocks []ProgramBlock, j Join, lookahead int) []ConcatEntry {
	if lookahead < 1 {
		lookahead = 1
	}
	entries := make([]ConcatEntry, 0, lookahead)
	for k := 0; k < lookahead && j.Index+k < len(blocks); k++ {
		b := blocks[j.Index+k]
		if b.Path == "" {
			continue
		}
		var inSec float64
		if k == 0 {
			inSec = float64(j.SeekMS) / 1000.0
		} else {
			inSec = float64(b.SourceOffsetMS) / 1000.0
		}
		outSec := float64(b.SourceOffsetMS+b.SourceDurationMS) / 1000.0
		entries = append(entries, ConcatEntry{Path: b.Path, InpointSec: inSec, OutpointSec: outSec})
	}
	return entries
}

// FormatConcat renders the FFmpeg concat-demuxer script (ffconcat v1.0).
// Single quotes in paths are escaped per the concat-demuxer rule
// (' → '\”). inpoint/outpoint follow their file line and trim the
// source slice this block plays.
func FormatConcat(entries []ConcatEntry) string {
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for _, e := range entries {
		b.WriteString("file '")
		b.WriteString(escapeConcatPath(e.Path))
		b.WriteString("'\n")
		if e.InpointSec > 0 {
			b.WriteString("inpoint ")
			b.WriteString(formatSec(e.InpointSec))
			b.WriteString("\n")
		}
		if e.OutpointSec > 0 {
			b.WriteString("outpoint ")
			b.WriteString(formatSec(e.OutpointSec))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// escapeConcatPath escapes single quotes for the concat demuxer's
// single-quoted file directive.
func escapeConcatPath(p string) string {
	return strings.ReplaceAll(p, "'", `'\''`)
}

// formatSec renders a second value with millisecond precision, trimming
// trailing zeros (e.g. 12.34, 45, 6.5).
func formatSec(s float64) string {
	str := strconv.FormatFloat(s, 'f', 3, 64)
	if strings.Contains(str, ".") {
		str = strings.TrimRight(str, "0")
		str = strings.TrimRight(str, ".")
	}
	return str
}
