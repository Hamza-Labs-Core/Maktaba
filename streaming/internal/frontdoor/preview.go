package frontdoor

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/hdhr"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slot"
)

// The staging preview (Epic 33 story 33.6, design §7): an admin watches a staged
// change in the browser before it goes live. Every route needs the change's
// preview token (?pt=, minted by POST /api/tv/changes/{id}/preview, an hour),
// and on the web port the stream gate also requires an admin session. The
// playlists come from the staging slot (the channel is warmed there within the
// staging budget, counting as staging demand only: never live demand, never a
// push); there is no staging HDHomeRun device.
//
//	GET /stream/staging/channel/{id}/index.m3u8?pt=…     the staging slot's HLS
//	GET /stream/staging/channel/{id}/s/{slot}/seg-N.ts?pt=…
//	GET /staging/lineup.m3u?pt=…                          the staged lineup (VLC)
//	GET /staging/xmltv.xml?pt=…                           the staged generation's guide

// previewStates are the change states a preview token works in (AC1).
var previewStates = map[string]bool{"verifying": true, "ready": true, "approved": true}

type previewCheck struct {
	at      time.Time
	state   string
	release string
}

type previewCache struct {
	mu sync.Mutex
	m  map[string]previewCheck
}

// MountPreview wires the preview routes (a front door with slots only).
func (h *Handler) MountPreview(r chi.Router) {
	r.Get("/stream/staging/channel/{id}/index.m3u8", h.previewPlaylist)
	r.Get("/stream/staging/channel/{id}/s/{slot}/{segment}", h.previewSegment)
	r.Get("/staging/lineup.m3u", h.previewLineup)
	r.Get("/staging/xmltv.xml", h.previewXMLTV)
}

// previewAuth checks the token and that the change it names is the staged one,
// in a previewable state. It answers the error itself.
func (h *Handler) previewAuth(w http.ResponseWriter, r *http.Request) (changeID, releaseID, slotName string, removed []string, ok bool) {
	if h.Slots == nil || h.Keyring == nil {
		http.Error(w, "no staging on this host", http.StatusNotFound)
		return "", "", "", nil, false
	}
	tok := r.URL.Query().Get("pt")
	cid, err := h.Keyring.CheckPreview(tok)
	if err != nil {
		http.Error(w, "a valid preview token is required (POST /api/tv/changes/{id}/preview)", http.StatusForbidden)
		return "", "", "", nil, false
	}
	state, rel := h.previewState(r.Context(), cid)
	if !previewStates[state] {
		http.Error(w, "the change is "+orUnknown(state)+": no preview", http.StatusGone)
		return "", "", "", nil, false
	}
	stCh, stRel, name, rm := h.Slots.StagingRelease()
	if stCh != cid || stRel != rel || name == "" {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "the change's staging slot is not running", http.StatusServiceUnavailable)
		return "", "", "", nil, false
	}
	return cid, rel, name, rm, true
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// previewState reads the change's state and release (cached 3 s).
func (h *Handler) previewState(ctx context.Context, changeID string) (string, string) {
	if h.PreviewLookup != nil {
		return h.PreviewLookup(ctx, changeID)
	}
	h.preview.mu.Lock()
	if h.preview.m == nil {
		h.preview.m = map[string]previewCheck{}
	}
	if c, ok := h.preview.m[changeID]; ok && time.Since(c.at) < 3*time.Second {
		h.preview.mu.Unlock()
		return c.state, c.release
	}
	h.preview.mu.Unlock()
	if h.Slots.Pool == nil {
		return "", ""
	}
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var state string
	var rel *string
	if err := h.Slots.Pool.QueryRow(qctx, `SELECT state, release_id::text FROM tv_changes WHERE id = $1`, changeID).
		Scan(&state, &rel); err != nil {
		return "", ""
	}
	c := previewCheck{at: time.Now(), state: state}
	if rel != nil {
		c.release = *rel
	}
	h.preview.mu.Lock()
	h.preview.m[changeID] = c
	h.preview.mu.Unlock()
	return c.state, c.release
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (h *Handler) previewPlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := channelParam(w, r)
	if !ok {
		return
	}
	_, _, name, removed, ok := h.previewAuth(w, r)
	if !ok {
		return
	}
	src := name
	if contains(removed, id.String()) {
		// EC1: a channel the change removes is shown as it is on live
		src, _ = h.Slots.Live()
		w.Header().Set("X-Maktaba-Preview", "removed")
	} else if warm, why := h.Slots.PreviewTouch(r.Context(), id); !warm {
		w.Header().Set("Retry-After", "10")
		w.Header().Set("X-Maktaba-Reason", why)
		http.Error(w, "staging preview unavailable: "+why, http.StatusServiceUnavailable)
		return
	} else {
		w.Header().Set("X-Maktaba-Preview", "staging")
	}
	resp, err := h.slotGet(r.Context(), src, "/stream/channel/"+id.String()+"/index.m3u8", nil)
	if err != nil {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "the staging slot did not answer", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		copyResponse(w, resp, nil)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		http.Error(w, "the staging slot did not answer", http.StatusBadGateway)
		return
	}
	q := "?pt=" + url.QueryEscape(r.URL.Query().Get("pt"))
	var out strings.Builder
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if l != "" && !strings.HasPrefix(l, "#") {
			if !strings.HasPrefix(l, "seg-") || strings.ContainsAny(l, "/\\?") {
				continue
			}
			l = "s/" + src + "/" + l + q
		}
		out.WriteString(l + "\n")
	}
	writePlaylist(w, []byte(out.String()))
}

func (h *Handler) previewSegment(w http.ResponseWriter, r *http.Request) {
	id, ok := channelParam(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "slot")
	seg := chi.URLParam(r, "segment")
	if !slot.Valid(name) || !strings.HasPrefix(seg, "seg-") || !strings.HasSuffix(seg, ".ts") ||
		strings.ContainsAny(seg, `/\`) || strings.Contains(seg, "..") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if _, _, _, _, ok := h.previewAuth(w, r); !ok {
		return
	}
	hdr := http.Header{}
	if rg := r.Header.Get("Range"); rg != "" {
		hdr.Set("Range", rg)
	}
	resp, err := h.slotGet(r.Context(), name, "/stream/channel/"+id.String()+"/"+seg, hdr)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	copyResponse(w, resp, h.count("hls", "preview:"+name))
}

func (h *Handler) previewGuide(w http.ResponseWriter, r *http.Request, write func(rel uuid.UUID, tok string)) {
	_, rel, _, _, ok := h.previewAuth(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(rel)
	if err != nil {
		http.Error(w, "no release", http.StatusNotFound)
		return
	}
	write(id, r.URL.Query().Get("pt"))
}

func (h *Handler) previewLineup(w http.ResponseWriter, r *http.Request) {
	h.previewGuide(w, r, func(rel uuid.UUID, tok string) {
		now := time.Now()
		chans, _, err := hdhr.ReleaseGuide(r.Context(), h.Slots.Pool, rel, now, now)
		if err != nil {
			http.Error(w, "lineup unavailable", http.StatusInternalServerError)
			return
		}
		base := hdhr.BaseURL(h.PublicBase, r)
		q := "?pt=" + url.QueryEscape(tok)
		body := hdhr.BuildM3UWith(hdhr.BaseURL(h.WebBase, r), base+"/staging/xmltv.xml"+q, chans,
			func(c hdhr.GuideChannel) string {
				return base + "/stream/staging/channel/" + c.ID.String() + "/index.m3u8" + q
			})
		w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(body))
	})
}

func (h *Handler) previewXMLTV(w http.ResponseWriter, r *http.Request) {
	h.previewGuide(w, r, func(rel uuid.UUID, _ string) {
		now := time.Now()
		chans, progs, err := hdhr.ReleaseGuide(r.Context(), h.Slots.Pool, rel, now.Add(-2*time.Hour), now.Add(7*24*time.Hour))
		if err != nil {
			http.Error(w, "guide unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		bw := bufio.NewWriter(w)
		defer bw.Flush()
		hdhr.WriteXMLTV(bw, hdhr.BaseURL(h.WebBase, r), chans, progs)
	})
}
