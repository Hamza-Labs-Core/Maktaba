package hdhr

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
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
	// VideoID is the block's video (uuid.Nil: none, e.g. a slate or a live page).
	VideoID uuid.UUID
}

// GuideSource is the data the guide endpoints read.
type GuideSource interface {
	GuideChannels(ctx context.Context) ([]GuideChannel, error)
	GuidePrograms(ctx context.Context, start, end time.Time) ([]GuideProgram, error)
	ChannelLogo(ctx context.Context, id uuid.UUID) (string, error)
	// PlanArtRoots are the art folders the latest lineup plan names (art.go).
	PlanArtRoots(ctx context.Context) ([]string, error)
	// ProgrammeArt reads what video's image is chosen from, and whether it is
	// in channel's live schedule overlapping [start, end).
	ProgrammeArt(ctx context.Context, channel, video uuid.UUID, start, end time.Time) (ProgrammeArt, error)
}

// GuideHandler serves the guide surface Plex (and IPTV players) need,
// unauthenticated on the streaming port:
//
//	/xmltv.xml                            XMLTV guide (channel id == channel number == HDHomeRun GuideNumber)
//	/lineup.m3u                           M3U playlist (live HLS per channel, url-tvg → /xmltv.xml)
//	/channel/{id}/logo                    the channel's logo (XMLTV/M3U icon, art.go)
//	/tv/art/channel/{id}/video/{video}    a programme's image (XMLTV programme icon, art.go)
//
// Every URL they give is on this service (PublicBase, else the request's
// host): a guide reader such as Plex fetches the icons from where it read the
// guide.
//
// It is independent of the HDHomeRun device toggle.
type GuideHandler struct {
	Repo    GuideSource
	PathMap pathmap.Map
	// PathMapSource, when set, is read instead of PathMap (a node's map
	// follows the cluster's).
	PathMapSource func() pathmap.Map
	PublicBase    string
	// DataDir (MAKTABA_DATA_DIR): uploaded channel art is under
	// <DataDir>/channel-art, an art folder logos are served from.
	DataDir string
	// ThumbnailRoot is the pipeline's thumbnail cache ("" →
	// MAKTABA_THUMBNAIL_ROOT, else ~/.maktaba/cache/thumbnails).
	ThumbnailRoot string
	// Remote, on a cluster node, fetches the images from the home (signed);
	// nil: they are read from this host's disk.
	Remote ArtFetcher
	// MaxAgeFunc gives the images' Cache-Control max-age per request (rules
	// tv.guide_image_max_age_s); nil: DefaultImageMaxAge.
	MaxAgeFunc func(ctx context.Context) time.Duration
	Now        func() time.Time
	// Back/Ahead bound the programme window (default 2 h back, 7 days ahead).
	Back, Ahead time.Duration
	// AheadFunc, when set, gives Ahead per request: the guide follows the
	// schedule horizon setting (rules tv.horizon_days).
	AheadFunc func(ctx context.Context) time.Duration
	// RulesFunc gives the filler rules per request (rules tv.guide_*); nil:
	// the zero GuideRules (every filler run its own channel-name entry).
	RulesFunc func(ctx context.Context) GuideRules

	roots artRootCache
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
	g.MountArt(r)
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
	start, end := g.window(r.Context())
	progs, err := g.Repo.GuidePrograms(r.Context(), start, end)
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
	WriteXMLTV(bw, XMLTVOptions{Base: publicBase(g.PublicBase, r), Rules: rules, ProgrammeArt: true}, chans, progs)
}

// M3U renders the playlist.
func (g *GuideHandler) M3U(w http.ResponseWriter, r *http.Request) {
	chans, err := g.Repo.GuideChannels(r.Context())
	if err != nil {
		http.Error(w, "lineup unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	_, _ = w.Write([]byte(BuildM3U(publicBase(g.PublicBase, r), chans)))
}

// guideSnapshot is the subset of title_snapshot the guide renders (the
// same keys the API guide reads).
type guideSnapshot struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	Genre        string `json:"genre"`
	Rating       string `json:"rating"`
	Series       string `json:"series"`
	StripSeries  string `json:"strip_series"`
	Season       *int   `json:"season"`
	Episode      *int   `json:"episode"`
	EpisodeTitle string `json:"episode_title"`
}

// guideEntry is one rendered <programme> after filler collapsing.
type guideEntry struct {
	start, stop time.Time
	snap        guideSnapshot
	isFiller    bool
	live        bool      // a live broadcast (not previously shown)
	video       uuid.UUID // the programme's video (uuid.Nil: none)
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
			*last = guideEntry{start: last.start, stop: p.EndAt, snap: s, live: p.Kind == "live", video: p.VideoID}
			continue
		}
		out = append(out, guideEntry{start: p.StartAt, stop: p.EndAt, snap: s, live: p.Kind == "live", video: p.VideoID})
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

// XMLTVOptions are how WriteXMLTV renders.
type XMLTVOptions struct {
	// Base is this service's public base: the icons point at its image routes
	// (art.go). No URL carries a token or a secret.
	Base string
	// Rules: how filler is shown (collapse).
	Rules GuideRules
	// ProgrammeArt gives each programme of a video its own image route; false
	// (a staged release's guide, whose videos are not on air): the channel's
	// logo.
	ProgrammeArt bool
}

// WriteXMLTV renders the XMLTV document. Channel ids are the channel
// numbers so they match the HDHomeRun lineup's GuideNumber in Plex; filler
// is shown as the rules say (collapse). Each programme carries, as
// dizqueTV's guide: an <icon> (its image route, the channel's logo for
// filler), a <desc> (its description, else its series, else the channel's
// name), <previously-shown/> (not for a live broadcast) and a <category>
// (its genre and the channel's category).
func WriteXMLTV(w stringWriter, o XMLTVOptions, chans []GuideChannel, progs []GuideProgram) {
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
		if icon := logoURL(o.Base, c); icon != "" {
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
			for _, e := range collapse(c.Name, progs[i:j], o.Rules) {
				icon := logoURL(o.Base, c)
				if o.ProgrammeArt && !e.isFiller && e.video != uuid.Nil {
					icon = ProgrammeArtURL(o.Base, c.ID, e.video)
				}
				writeProgramme(w, num, c, icon, e)
			}
		}
		i = j
	}
	_, _ = w.WriteString(`</tv>` + "\n")
}

func writeProgramme(w stringWriter, channel string, c GuideChannel, icon string, e guideEntry) {
	s := e.snap
	_, _ = w.WriteString(`  <programme start="` + xmltvTime(e.start) + `" stop="` + xmltvTime(e.stop) +
		`" channel="` + channel + `">` + "\n")
	_, _ = w.WriteString(`    <title lang="en">` + xmlEscape(s.Title) + `</title>` + "\n")
	if s.EpisodeTitle != "" {
		_, _ = w.WriteString(`    <sub-title lang="en">` + xmlEscape(s.EpisodeTitle) + `</sub-title>` + "\n")
	}
	if desc := programmeDesc(s, c.Name); desc != "" {
		_, _ = w.WriteString(`    <desc lang="en">` + xmlEscape(desc) + `</desc>` + "\n")
	}
	for _, cat := range programmeCategories(s.Genre, c.Category) {
		_, _ = w.WriteString(`    <category lang="en">` + xmlEscape(cat) + `</category>` + "\n")
	}
	if icon != "" {
		_, _ = w.WriteString(`    <icon src="` + xmlEscape(icon) + `"/>` + "\n")
	}
	if s.Season != nil && s.Episode != nil && *s.Season > 0 && *s.Episode > 0 {
		ns := strconv.Itoa(*s.Season-1) + "." + strconv.Itoa(*s.Episode-1) + "."
		_, _ = w.WriteString(`    <episode-num system="xmltv_ns">` + ns + `</episode-num>` + "\n")
		_, _ = w.WriteString(`    <episode-num system="onscreen">S` + pad2(*s.Season) + `E` + pad2(*s.Episode) + `</episode-num>` + "\n")
	}
	if !e.live {
		_, _ = w.WriteString(`    <previously-shown/>` + "\n")
	}
	if s.Rating != "" {
		_, _ = w.WriteString(`    <rating><value>` + xmlEscape(s.Rating) + `</value></rating>` + "\n")
	}
	_, _ = w.WriteString(`  </programme>` + "\n")
}

// programmeDesc is a programme's <desc>: its description, else its series'
// name, else the channel's name (dizqueTV's guide gives the show's name).
func programmeDesc(s guideSnapshot, channelName string) string {
	for _, d := range []string{s.Description, s.Series, s.StripSeries, channelName} {
		if d = strings.TrimSpace(d); d != "" {
			return d
		}
	}
	return ""
}

// programmeCategories are the programme's genre and the channel's category,
// without repeats.
func programmeCategories(genre, channelCategory string) []string {
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

// BuildM3U renders the IPTV playlist: one live HLS URL per channel,
// tvg-id matching the XMLTV channel id, tvg-logo this service's logo route.
func BuildM3U(base string, chans []GuideChannel) string {
	return BuildM3UWith(base, base+"/xmltv.xml", chans, func(c GuideChannel) string {
		return base + "/stream/channel/" + c.ID.String() + "/index.m3u8"
	})
}

// logoURL is this service's logo route for a channel with a logo.
func logoURL(base string, c GuideChannel) string {
	if strings.TrimSpace(c.LogoPath) == "" {
		return ""
	}
	return ChannelLogoURL(base, c.ID)
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
