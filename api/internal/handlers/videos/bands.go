package videos

// Epic 45 Phase 3 (story 45.20): the grid's length, added-date and
// resolution filters on GET /api/videos, and the bands the facets count.
//
//	duration=short|medium|long      under 15 min, 15 to 60 min, over an hour
//	added=week|month|year           created_at in the last 7, 30 or 365 days
//	resolution=sd|hd|fhd|uhd        media_info.height: under 720, 720 to 1079,
//	                                1080 to 2159, 2160 and up
//
// A video with no known duration (or no media_info row) is in no band.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Band is one value of a banded filter: [Lo, Hi), Hi 0 for no upper bound.
type Band struct {
	Value string
	Lo    float64
	Hi    float64
}

// DurationBands are the length filter's bands, in seconds.
var DurationBands = []Band{
	{Value: "short", Lo: 0, Hi: 15 * 60},
	{Value: "medium", Lo: 15 * 60, Hi: 60 * 60},
	{Value: "long", Lo: 60 * 60},
}

// ResolutionBands are the resolution filter's bands, on the picture's height.
var ResolutionBands = []Band{
	{Value: "sd", Lo: 0, Hi: 720},
	{Value: "hd", Lo: 720, Hi: 1080},
	{Value: "fhd", Lo: 1080, Hi: 2160},
	{Value: "uhd", Lo: 2160},
}

// AddedWindows are the added-date filter's windows, in days back from now.
var AddedWindows = []struct {
	Value string
	Days  int
}{{"week", 7}, {"month", 30}, {"year", 365}}

// FindBand is the band of bands named value.
func FindBand(bands []Band, value string) (Band, bool) {
	for _, b := range bands {
		if b.Value == value {
			return b, true
		}
	}
	return Band{}, false
}

// AddedSince is the start of the added-date window named value.
func AddedSince(now time.Time, value string) (time.Time, bool) {
	for _, w := range AddedWindows {
		if w.Value == value {
			return now.AddDate(0, 0, -w.Days), true
		}
	}
	return time.Time{}, false
}

// BandCond is col within b: "col >= lo AND col < hi", the numbers inlined
// (they are the bands' own constants, never the request's).
func BandCond(col string, b Band) string {
	cond := col + " >= " + strconv.FormatFloat(b.Lo, 'f', -1, 64)
	if b.Hi > 0 {
		cond += " AND " + col + " < " + strconv.FormatFloat(b.Hi, 'f', -1, 64)
	}
	return cond
}

// ResolutionCond is the WHERE fragment that keeps videos (column id) of
// the resolution band b.
func ResolutionCond(idCol string, b Band) string {
	return idCol + " IN (SELECT mi.video_id FROM media_info mi WHERE " + BandCond("mi.height", b) + ")"
}

// timeArg is a timestamp parameter: native on Postgres; on SQLite the text
// the columns hold (2006-01-02T15:04:05.000Z), so a comparison on the same
// day compares the hour too (the driver's own form sorts before it).
func (h *Handler) timeArg(t time.Time) any {
	if h.DB != nil && strings.Contains(strings.ToLower(fmt.Sprintf("%T", h.DB.Driver())), "sqlite") {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.UTC()
}
