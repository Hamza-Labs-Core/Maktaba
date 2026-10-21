package hdhr

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// GuideChannel is a channel header for the XMLTV guide / M3U playlist.
type GuideChannel struct {
	ID       uuid.UUID
	Number   int
	Name     string
	Slug     string
	LogoPath string
	Category string
}

// GuideProgram is one channel_programs block as read for the guide.
type GuideProgram struct {
	ChannelID uuid.UUID
	Kind      string
	StartAt   time.Time
	EndAt     time.Time
	Snapshot  []byte // title_snapshot JSON
}

// GuideSource is the data the guide endpoints read.
type GuideSource interface {
	GuideChannels(ctx context.Context) ([]GuideChannel, error)
	GuidePrograms(ctx context.Context, start, end time.Time) ([]GuideProgram, error)
	ChannelLogo(ctx context.Context, id uuid.UUID) (string, error)
}

// GuideHandler serves the guide surface Plex (and IPTV players) need,
// unauthenticated on the streaming port:
//
//	/xmltv.xml            XMLTV guide (channel id == channel number == HDHomeRun GuideNumber)
//	/lineup.m3u           M3U playlist (live HLS per channel, url-tvg → /xmltv.xml)
//	/channel/{id}/logo    the channel's logo file (XMLTV/M3U icon)
//
// It is independent of the HDHomeRun device toggle.
type GuideHandler struct {
	Repo       GuideSource
	PathMap    pathmap.Map
	PublicBase string
	// WebBase (MAKTABA_PUBLIC_WEB_BASE) is where the web/API is reachable;
	// channel icons point at its public /api/channels/{id}/logo. Empty →
	// the request's scheme+host.
	WebBase string
	Now     func() time.Time
	// Back/Ahead bound the programme window (default 2 h back, 7 days ahead).
	Back, Ahead time.Duration
	// AheadFunc, when set, gives Ahead per request: the guide follows the
	// schedule horizon setting (rules tv.horizon_days).
	AheadFunc func(ctx context.Context) time.Duration
	// RulesFunc gives the filler rules per request (rules tv.guide_*); nil:
	// the zero GuideRules (every filler run its own channel-name entry).
	RulesFunc func(ctx context.Context) GuideRules
}

// GuideRules are how the guide shows filler, as dizqueTV's guide
// (guideMaxPaddingSeconds, guideFlexPlaceholder, guideMinimumDurationSeconds).
type GuideRules struct {
	// MaxPadding: a filler run up to this long joins the programme before
	// it (or, with none before it, the one after); a longer one is an entry
	// titled FillerTitle (empty: the channel's name).
	MaxPadding  time.Duration
	FillerTitle string
	// MinProgram: programmes shorter than this are not listed; they count
	// as filler.
	MinProgram time.Duration
}

// GuideRulesFromSeconds builds GuideRules from the rules' seconds.
func GuideRulesFromSeconds(maxPaddingS int, fillerTitle string, minProgramS int) GuideRules {
	return GuideRules{MaxPadding: time.Duration(maxPaddingS) * time.Second, FillerTitle: fillerTitle,
		MinProgram: time.Duration(minProgramS) * time.Second}
}

// Mount wires the guide routes.
func (g *GuideHandler) Mount(r chi.Router) {
	r.Get("/xmltv.xml", g.XMLTV)
	r.Get("/lineup.m3u", g.M3U)
	r.Get("/channel/{id}/logo", g.Logo)
}

func (g *GuideHandler) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// XMLTV streams the guide document.
func (g *GuideHandler) XMLTV(w http.ResponseWriter, r *http.Request) {
	chans, err := g.Repo.GuideChannels(r.Context())
	if err != nil {
		http.Error(w, "guide unavailable", http.StatusInternalServerError)
		return
	}
	back, ahead := g.Back, g.Ahead
	if g.AheadFunc != nil {
		if a := g.AheadFunc(r.Context()); a > 0 {
			ahead = a
		}
	}
	if back <= 0 {
		back = 2 * time.Hour
	}
	if ahead <= 0 {
		ahead = 7 * 24 * time.Hour
	}
	now := g.now()
	progs, err := g.Repo.GuidePrograms(r.Context(), now.Add(-back), now.Add(ahead))
	if err != nil {
		http.Error(w, "guide unavailable", http.StatusInternalServerError)
		return
	}
	var rules GuideRules
	if g.RulesFunc != nil {
		rules = g.RulesFunc(r.Context())
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	WriteXMLTV(bw, publicBase(g.WebBase, r), chans, progs, rules)
}

// M3U renders the playlist.
func (g *GuideHandler) M3U(w http.ResponseWriter, r *http.Request) {
	chans, err := g.Repo.GuideChannels(r.Context())
	if err != nil {
		http.Error(w, "lineup unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	_, _ = w.Write([]byte(BuildM3U(publicBase(g.PublicBase, r), publicBase(g.WebBase, r), chans)))
}

// Logo serves a channel's logo_path (path-mapped) or redirects to it
// when it is a URL.
func (g *GuideHandler) Logo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad channel id", http.StatusBadRequest)
		return
	}
	logo, err := g.Repo.ChannelLogo(r.Context(), id)
	if err != nil || logo == "" {
		http.NotFound(w, r)
		return
	}
	if strings.Contains(logo, "://") && !pathmap.IsCanonical(logo) {
		if u, err := url.Parse(logo); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			http.Redirect(w, r, u.String(), http.StatusFound)
			return
		}
		http.NotFound(w, r)
		return
	}
	p := g.PathMap.Apply(logo)
	if !imageExt[strings.ToLower(filepath.Ext(p))] {
		http.NotFound(w, r) // only images are served from logo_path
		return
	}
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFile(w, r, p)
}

// imageExt lists the logo file types served (no SVG: it can carry script).
var imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// guideSnapshot is the subset of title_snapshot the guide renders (the
// same keys the API guide reads).
type guideSnapshot struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	Poster       string `json:"poster"`
	Genre        string `json:"genre"`
	Rating       string `json:"rating"`
	Series       string `json:"series"`
	Season       *int   `json:"season"`
	Episode      *int   `json:"episode"`
	EpisodeTitle string `json:"episode_title"`
}

// guideEntry is one rendered <programme> after filler collapsing.
type guideEntry struct {
	start, stop time.Time
	snap        guideSnapshot
	isFiller    bool
}

func isFillerKind(k string) bool { return k == "filler" || k == "bumper" || k == "slate" }

// collapse applies the guide's filler rules (dizqueTV's): filler/bumper/
// slate blocks and programmes shorter than rules.MinProgram are filler. A
// run of adjacent filler up to rules.MaxPadding is folded into the
// programme before it (with none before it, into the one after: a short
// lead-in); a longer run is one entry titled rules.FillerTitle (empty:
// the channel name). Consecutive pieces of the same programme (a split
// marathon) merge.
func collapse(channelName string, progs []GuideProgram, rules GuideRules) []guideEntry {
	var out []guideEntry
	const adj = 2 * time.Second
	adjacent := func(a, b time.Time) bool { return absDur(b.Sub(a)) <= adj }
	isFill := func(p GuideProgram) bool {
		return isFillerKind(p.Kind) || p.EndAt.Sub(p.StartAt) < rules.MinProgram
	}
	fillerTitle := rules.FillerTitle
	if fillerTitle == "" {
		fillerTitle = channelName
	}
	for i := 0; i < len(progs); {
		p := progs[i]
		var last *guideEntry
		if n := len(out); n > 0 && adjacent(out[n-1].stop, p.StartAt) {
			last = &out[n-1]
		}
		if isFill(p) {
			// the whole run of adjacent filler [i, j)
			j := i + 1
			for j < len(progs) && isFill(progs[j]) && adjacent(progs[j-1].EndAt, progs[j].StartAt) {
				j++
			}
			stop := progs[j-1].EndAt
			i = j
			if stop.Sub(p.StartAt) <= rules.MaxPadding && last != nil && !last.isFiller {
				last.stop = stop
				continue
			}
			out = append(out, guideEntry{start: p.StartAt, stop: stop,
				snap: guideSnapshot{Title: fillerTitle}, isFiller: true})
			continue
		}
		i++
		var s guideSnapshot
		if len(p.Snapshot) > 0 {
			_ = json.Unmarshal(p.Snapshot, &s)
		}
		if s.Title == "" {
			if p.Kind == "live" {
				s.Title = "Live"
			} else {
				s.Title = channelName
			}
		}
		if last != nil && !last.isFiller && sameProgramme(last.snap, s) {
			last.stop = p.EndAt
			continue
		}
		if last != nil && last.isFiller && last.stop.Sub(last.start) <= rules.MaxPadding {
			// A short filler lead-in is absorbed into the programme.
			*last = guideEntry{start: last.start, stop: p.EndAt, snap: s}
			continue
		}
		out = append(out, guideEntry{start: p.StartAt, stop: p.EndAt, snap: s})
	}
	return out
}

func sameProgramme(a, b guideSnapshot) bool {
	return a.Title == b.Title && a.EpisodeTitle == b.EpisodeTitle &&
		eqIntPtr(a.Season, b.Season) && eqIntPtr(a.Episode, b.Episode)
}

func eqIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

type stringWriter interface {
	WriteString(string) (int, error)
}

// WriteXMLTV renders the XMLTV document. Channel ids are the channel
// numbers so they match the HDHomeRun lineup's GuideNumber in Plex;
// icons point at the API's public logo endpoint under webBase; filler is
// shown as rules say (collapse).
func WriteXMLTV(w stringWriter, webBase string, chans []GuideChannel, progs []GuideProgram, rules GuideRules) {
	_, _ = w.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	_, _ = w.WriteString(`<!DOCTYPE tv SYSTEM "xmltv.dtd">` + "\n")
	_, _ = w.WriteString(`<tv generator-info-name="Maktaba">` + "\n")
	byID := map[uuid.UUID]GuideChannel{}
	for _, c := range chans {
		byID[c.ID] = c
		num := strconv.Itoa(c.Number)
		_, _ = w.WriteString(`  <channel id="` + num + `">` + "\n")
		_, _ = w.WriteString(`    <display-name>` + xmlEscape(c.Name) + `</display-name>` + "\n")
		_, _ = w.WriteString(`    <display-name>` + num + `</display-name>` + "\n")
		if icon := logoURL(webBase, c); icon != "" {
			_, _ = w.WriteString(`    <icon src="` + xmlEscape(icon) + `"/>` + "\n")
		}
		_, _ = w.WriteString(`  </channel>` + "\n")
	}
	// progs are ordered by channel then time; group per channel.
	for i := 0; i < len(progs); {
		j := i
		for j < len(progs) && progs[j].ChannelID == progs[i].ChannelID {
			j++
		}
		c, ok := byID[progs[i].ChannelID]
		if ok {
			num := strconv.Itoa(c.Number)
			for _, e := range collapse(c.Name, progs[i:j], rules) {
				writeProgramme(w, num, e)
			}
		}
		i = j
	}
	_, _ = w.WriteString(`</tv>` + "\n")
}

func writeProgramme(w stringWriter, channel string, e guideEntry) {
	s := e.snap
	_, _ = w.WriteString(`  <programme start="` + xmltvTime(e.start) + `" stop="` + xmltvTime(e.stop) +
		`" channel="` + channel + `">` + "\n")
	_, _ = w.WriteString(`    <title lang="en">` + xmlEscape(s.Title) + `</title>` + "\n")
	if s.EpisodeTitle != "" {
		_, _ = w.WriteString(`    <sub-title lang="en">` + xmlEscape(s.EpisodeTitle) + `</sub-title>` + "\n")
	}
	if s.Description != "" {
		_, _ = w.WriteString(`    <desc lang="en">` + xmlEscape(s.Description) + `</desc>` + "\n")
	}
	if s.Genre != "" {
		_, _ = w.WriteString(`    <category lang="en">` + xmlEscape(s.Genre) + `</category>` + "\n")
	}
	if strings.HasPrefix(s.Poster, "http://") || strings.HasPrefix(s.Poster, "https://") {
		_, _ = w.WriteString(`    <icon src="` + xmlEscape(s.Poster) + `"/>` + "\n")
	}
	if s.Season != nil && s.Episode != nil && *s.Season > 0 && *s.Episode > 0 {
		ns := strconv.Itoa(*s.Season-1) + "." + strconv.Itoa(*s.Episode-1) + "."
		_, _ = w.WriteString(`    <episode-num system="xmltv_ns">` + ns + `</episode-num>` + "\n")
		_, _ = w.WriteString(`    <episode-num system="onscreen">S` + pad2(*s.Season) + `E` + pad2(*s.Episode) + `</episode-num>` + "\n")
	}
	if s.Rating != "" {
		_, _ = w.WriteString(`    <rating><value>` + xmlEscape(s.Rating) + `</value></rating>` + "\n")
	}
	_, _ = w.WriteString(`  </programme>` + "\n")
}

// BuildM3U renders the IPTV playlist: one live HLS URL per channel,
// tvg-id matching the XMLTV channel id.
func BuildM3U(base, webBase string, chans []GuideChannel) string {
	var b strings.Builder
	xmltv := base + "/xmltv.xml"
	b.WriteString(`#EXTM3U url-tvg="` + xmltv + `" x-tvg-url="` + xmltv + `"` + "\n")
	for _, c := range chans {
		num := strconv.Itoa(c.Number)
		b.WriteString(`#EXTINF:-1 tvg-id="` + num + `" tvg-chno="` + num + `" tvg-name="` + m3uAttr(c.Name) + `"`)
		if icon := logoURL(webBase, c); icon != "" {
			b.WriteString(` tvg-logo="` + m3uAttr(icon) + `"`)
		}
		if c.Category != "" {
			b.WriteString(` group-title="` + m3uAttr(c.Category) + `"`)
		}
		b.WriteString("," + strings.NewReplacer("\n", " ", "\r", "").Replace(c.Name) + "\n")
		b.WriteString(base + "/stream/channel/" + c.ID.String() + "/index.m3u8\n")
	}
	return b.String()
}

// logoURL is the API's public logo endpoint for channels with a logo.
func logoURL(webBase string, c GuideChannel) string {
	if c.LogoPath == "" {
		return ""
	}
	return webBase + "/api/channels/" + c.ID.String() + "/logo"
}

func m3uAttr(s string) string {
	return strings.NewReplacer(`"`, "'", "\n", " ", "\r", "").Replace(s)
}

func xmltvTime(t time.Time) string { return t.UTC().Format("20060102150405 -0700") }

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
