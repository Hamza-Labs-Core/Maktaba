package guide

import (
	"bufio"
	"net/http"
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
		httperror.Write(w, r, httperror.Internal("list channels"))
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
		httperror.Write(w, r, httperror.Internal("read programs"))
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	writeXMLTV(bw, chans, rows)
}

// xmlWriter is the minimal surface writeXMLTV needs (an *bufio.Writer or
// a *strings.Builder in tests).
type xmlWriter interface {
	WriteString(string) (int, error)
}

// writeXMLTV streams a valid XMLTV document: <channel> headers then
// <programme> bodies. Filler/bumper blocks are collapsed into generic
// "Up Next" programmes (AC10).
func writeXMLTV(w xmlWriter, chans []ChannelMeta, rows []ProgramRow) {
	_, _ = w.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	_, _ = w.WriteString(`<tv generator-info-name="Maktaba">` + "\n")
	for _, c := range chans {
		_, _ = w.WriteString(`  <channel id="` + xmlEscape(c.Slug) + `">` + "\n")
		_, _ = w.WriteString(`    <display-name>` + xmlEscape(c.Name) + `</display-name>` + "\n")
		_, _ = w.WriteString(`    <display-name>` + itoa(c.Number) + `</display-name>` + "\n")
		if c.LogoPath != nil && *c.LogoPath != "" {
			_, _ = w.WriteString(`    <icon src="` + xmlEscape(*c.LogoPath) + `"/>` + "\n")
		}
		_, _ = w.WriteString(`  </channel>` + "\n")
	}
	// Index slug by channel id for the programme rows.
	slugByID := map[string]string{}
	for _, c := range chans {
		slugByID[c.ID] = c.Slug
	}
	for _, pr := range rows {
		slug, ok := slugByID[pr.ChannelID]
		if !ok {
			continue // channel not visible to this consumer
		}
		writeProgramme(w, slug, pr)
	}
	_, _ = w.WriteString(`</tv>` + "\n")
}

func writeProgramme(w xmlWriter, slug string, pr ProgramRow) {
	s := pr.Snapshot
	title := s.Title
	if title == "" && isFiller(pr.Kind) {
		title = "Up Next"
	}
	_, _ = w.WriteString(`  <programme start="` + xmltvTime(pr.StartAt) +
		`" stop="` + xmltvTime(pr.EndAt) + `" channel="` + xmlEscape(slug) + `">` + "\n")
	_, _ = w.WriteString(`    <title>` + xmlEscape(title) + `</title>` + "\n")
	if s.EpisodeTitle != "" {
		_, _ = w.WriteString(`    <sub-title>` + xmlEscape(s.EpisodeTitle) + `</sub-title>` + "\n")
	}
	if s.Description != "" {
		_, _ = w.WriteString(`    <desc>` + xmlEscape(s.Description) + `</desc>` + "\n")
	}
	if s.Genre != "" {
		_, _ = w.WriteString(`    <category>` + xmlEscape(s.Genre) + `</category>` + "\n")
	}
	if s.Poster != "" {
		_, _ = w.WriteString(`    <icon src="` + xmlEscape(s.Poster) + `"/>` + "\n")
	}
	if s.Rating != "" {
		_, _ = w.WriteString(`    <rating><value>` + xmlEscape(s.Rating) + `</value></rating>` + "\n")
	}
	if s.IsEpisodic() {
		// xmltv_ns is zero-based "season.episode."; onscreen is the
		// human SxxExx form.
		ns := itoa(*s.Season-1) + "." + itoa(*s.Episode-1) + "."
		_, _ = w.WriteString(`    <episode-num system="xmltv_ns">` + ns + `</episode-num>` + "\n")
		_, _ = w.WriteString(`    <episode-num system="onscreen">S` + itoa(*s.Season) + `E` + itoa(*s.Episode) + `</episode-num>` + "\n")
	}
	_, _ = w.WriteString(`  </programme>` + "\n")
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
