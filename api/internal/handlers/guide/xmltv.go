package guide

import (
	"bufio"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// xmltvHorizon caps the XMLTV export window (AC / EC3 — large lineups
// must not balloon memory). The writer streams, but the range is still
// bounded to the generated horizon by default.
const xmltvHorizon = 48 * time.Hour

// XMLTV implements GET /api/channels/xmltv (AC4). The document is
// streamed (not buffered) so a 7-day × 50-channel export is
// constant-memory. `tvg-id` / `<channel id>` == channel slug (AC6).
func (h *Handler) XMLTV(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	chans, err := h.visibleChannels(r, p, r.URL.Query().Get("category"))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list channels").Wrap(err))
		return
	}
	start := h.now()
	end := start.Add(xmltvHorizon)
	if s := r.URL.Query().Get("start"); s != "" {
		if t, e := time.Parse(time.RFC3339, s); e == nil {
			start = t
		}
	}
	if s := r.URL.Query().Get("end"); s != "" {
		if t, e := time.Parse(time.RFC3339, s); e == nil {
			end = t
		}
	}
	rows, err := h.repo().programsOverlapping(r.Context(), metaIDs(chans), start, end)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read programs").Wrap(err))
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	writeXMLTV(bw, baseURL(r), strings.TrimRight(strings.TrimSpace(h.StreamBase), "/"), chans, rows)
}

// xmlWriter is the minimal surface writeXMLTV needs (an *bufio.Writer or
// a *strings.Builder in tests).
type xmlWriter interface {
	WriteString(string) (int, error)
}

// writeXMLTV streams a valid XMLTV document: <channel> headers then
// <programme> bodies. Filler/bumper blocks are collapsed into generic
// "Up Next" programmes (AC10). Each programme carries what the streaming
// service's guide (streaming/internal/hdhr) gives, as dizqueTV's: an <icon>
// (the streaming service's image route of its video under streamBase, else
// the channel's logo), a <desc> (its description, else its series, else the
// channel's name), <previously-shown/> (not for a live broadcast) and its
// genre and channel category. No URL carries a token or a secret.
func writeXMLTV(w xmlWriter, base, streamBase string, chans []ChannelMeta, rows []ProgramRow) {
	_, _ = w.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	_, _ = w.WriteString(`<tv generator-info-name="Maktaba">` + "\n")
	for _, c := range chans {
		_, _ = w.WriteString(`  <channel id="` + xmlEscape(c.Slug) + `">` + "\n")
		_, _ = w.WriteString(`    <display-name>` + xmlEscape(c.Name) + `</display-name>` + "\n")
		_, _ = w.WriteString(`    <display-name>` + itoa(c.Number) + `</display-name>` + "\n")
		if logo := logoRef(base, c); logo != "" {
			_, _ = w.WriteString(`    <icon src="` + xmlEscape(logo) + `"/>` + "\n")
		}
		_, _ = w.WriteString(`  </channel>` + "\n")
	}
	// Index the channels by id for the programme rows.
	byID := map[string]ChannelMeta{}
	for _, c := range chans {
		byID[c.ID] = c
	}
	for _, pr := range rows {
		c, ok := byID[pr.ChannelID]
		if !ok {
			continue // channel not visible to this consumer
		}
		writeProgramme(w, c, programmeIcon(base, streamBase, c, pr), pr)
	}
	_, _ = w.WriteString(`</tv>` + "\n")
}

// programmeIcon is a programme's image: the streaming service's image route
// of its video (GET /tv/art/channel/{id}/video/{video}, public, only for a
// video on the channel's air) when its base is known, else the channel's
// logo.
func programmeIcon(base, streamBase string, c ChannelMeta, pr ProgramRow) string {
	if streamBase != "" && pr.VideoID != "" && pr.Kind == "program" {
		return streamBase + "/tv/art/channel/" + url.PathEscape(c.ID) + "/video/" + url.PathEscape(pr.VideoID)
	}
	return logoRef(base, c)
}

func writeProgramme(w xmlWriter, c ChannelMeta, icon string, pr ProgramRow) {
	s := pr.Snapshot
	title := s.Title
	if title == "" && isFiller(pr.Kind) {
		title = "Up Next"
	}
	_, _ = w.WriteString(`  <programme start="` + xmltvTime(pr.StartAt) +
		`" stop="` + xmltvTime(pr.EndAt) + `" channel="` + xmlEscape(c.Slug) + `">` + "\n")
	_, _ = w.WriteString(`    <title>` + xmlEscape(title) + `</title>` + "\n")
	if s.EpisodeTitle != "" {
		_, _ = w.WriteString(`    <sub-title>` + xmlEscape(s.EpisodeTitle) + `</sub-title>` + "\n")
	}
	if desc := firstNonEmpty(s.Description, s.Series, s.StripSeries, c.Name); desc != "" {
		_, _ = w.WriteString(`    <desc>` + xmlEscape(desc) + `</desc>` + "\n")
	}
	for _, cat := range categories(s.Genre, c.Category) {
		_, _ = w.WriteString(`    <category>` + xmlEscape(cat) + `</category>` + "\n")
	}
	if icon != "" {
		_, _ = w.WriteString(`    <icon src="` + xmlEscape(icon) + `"/>` + "\n")
	}
	// the elements in the XMLTV DTD's order
	if s.IsEpisodic() {
		// xmltv_ns is zero-based "season.episode."; onscreen is the
		// human SxxExx form.
		ns := itoa(*s.Season-1) + "." + itoa(*s.Episode-1) + "."
		_, _ = w.WriteString(`    <episode-num system="xmltv_ns">` + ns + `</episode-num>` + "\n")
		_, _ = w.WriteString(`    <episode-num system="onscreen">S` + itoa(*s.Season) + `E` + itoa(*s.Episode) + `</episode-num>` + "\n")
	}
	if pr.Kind != "live" {
		_, _ = w.WriteString(`    <previously-shown/>` + "\n")
	}
	if s.Rating != "" {
		_, _ = w.WriteString(`    <rating><value>` + xmlEscape(s.Rating) + `</value></rating>` + "\n")
	}
	_, _ = w.WriteString(`  </programme>` + "\n")
}

// categories are the programme's genre and the channel's category, without
// repeats.
func categories(genre, channelCategory string) []string {
	var out []string
	for _, c := range []string{genre, channelCategory} {
		c = strings.TrimSpace(c)
		if c == "" || (len(out) > 0 && strings.EqualFold(out[0], c)) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// xmltvTime formats a time as XMLTV's "YYYYMMDDHHMMSS +0000" (UTC).
func xmltvTime(t time.Time) string {
	return t.UTC().Format("20060102150405 -0700")
}

// xmlEscape escapes the five XML predefined entities.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
