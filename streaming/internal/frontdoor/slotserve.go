package frontdoor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/relay"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slot"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
)

// The front door's side of the TV slots (story 33.1): the channels placed on
// this host are read from the live slot — HLS through the stitcher, TS through
// a retiming splicer — and every upstream failure makes the router look at the
// live slot at once (Slots.Suspect), so a crash fails over within a probe.

// slotStitchIdle: a channel's stitched playlist nobody fetched for this long
// starts over (its old entries are long gone).
const slotStitchIdle = 90 * time.Second

type stitchState struct {
	st   *Stitcher
	used time.Time
}

type stitchers struct {
	mu sync.Mutex
	m  map[uuid.UUID]*stitchState
}

func (s *stitchers) get(ch uuid.UUID) *Stitcher {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[uuid.UUID]*stitchState{}
	}
	now := time.Now()
	for id, v := range s.m {
		if now.Sub(v.used) > slotStitchIdle {
			delete(s.m, id)
		}
	}
	v := s.m[ch]
	if v == nil {
		v = &stitchState{st: &Stitcher{}}
		s.m[ch] = v
	}
	v.used = now
	return v.st
}

// Stitcher is a channel's stitched playlist (tests).
func (h *Handler) Stitcher(ch uuid.UUID) *Stitcher { return h.stitch.get(ch) }

// slotGet sends a GET to a slot.
func (h *Handler) slotGet(ctx context.Context, name, path string, hdr http.Header) (*http.Response, error) {
	base := h.Slots.Base(name)
	if base == "" {
		return nil, errors.New("frontdoor: no address for slot " + name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	return h.client().Do(req)
}

// serveSlotPlaylist serves a channel's playlist from the live slot: the media
// playlist stitched (it survives a change of slot), the master as is.
func (h *Handler) serveSlotPlaylist(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	ctx := r.Context()
	if !strings.HasSuffix(r.URL.Path, "/index.m3u8") {
		live := h.Slots.Route(id)
		resp, err := h.slotGet(ctx, live, r.URL.Path, nil)
		if err != nil {
			h.Slots.Suspect(ctx, live)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "channel unavailable", http.StatusBadGateway)
			return
		}
		copyResponse(w, resp, h.count("hls", "slot:"+live))
		return
	}
	st := h.stitch.get(id)
	// story 33.7: a planned switch (swap, rollback) of this channel
	if plan, ok := h.Slots.Plan(id); ok && plan.To != plan.From && st.Source() != plan.To {
		now := time.Now()
		if plan.Removed && !now.Before(plan.At) {
			if st.Len() == 0 {
				http.NotFound(w, r)
				return
			}
			st.End()
			writePlaylist(w, st.Render())
			return
		}
		if st.Len() > 0 && plan.At.Sub(now) <= swapPrepLead {
			if !plan.Removed {
				st.Plan(plan.To, plan.At)
			}
			fromOK := h.stitchFrom(ctx, st, id, plan.From)
			// the instant reached on the old slot — or the old slot is gone (a
			// crash: an automatic rollback, a failover inside a swap)
			if (st.Reached() || (!fromOK && !now.Before(plan.At))) && !plan.Removed {
				if h.stitchFrom(ctx, st, id, plan.To) && st.Source() == plan.To {
					tvmetrics.HLSStitches.Inc(plan.Kind)
				}
			}
			writePlaylist(w, st.Render())
			return
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		src := h.Slots.Route(id)
		resp, err := h.slotGet(ctx, src, "/stream/channel/"+id.String()+"/index.m3u8", nil)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			h.Slots.Suspect(ctx, src)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode >= 500 && resp.StatusCode != http.StatusServiceUnavailable && attempt < 2 {
				_ = resp.Body.Close()
				h.Slots.Suspect(ctx, src)
				continue
			}
			copyResponse(w, resp, nil) // today's answers (starting, busy, no such channel)
			return
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if err != nil {
			h.Slots.Suspect(ctx, src)
			continue
		}
		pl, err := parseMediaPlaylist(body)
		if err != nil {
			http.Error(w, "bad playlist from the TV slot", http.StatusBadGateway)
			return
		}
		before := st.Source()
		st.Update(src, pl)
		if before != "" && before != src && st.Source() == src {
			tvmetrics.HLSStitches.Inc("failover")
		}
		writePlaylist(w, st.Render())
		return
	}
	if st.Len() > 0 {
		writePlaylist(w, st.Render()) // what was listed: the player retries
		return
	}
	w.Header().Set("Retry-After", "2")
	http.Error(w, "channel unavailable", http.StatusBadGateway)
}

// stitchFrom merges one slot's current playlist of a channel into st.
func (h *Handler) stitchFrom(ctx context.Context, st *Stitcher, id uuid.UUID, src string) bool {
	resp, err := h.slotGet(ctx, src, "/stream/channel/"+id.String()+"/index.m3u8", nil)
	if err != nil {
		if ctx.Err() == nil {
			h.Slots.Suspect(ctx, src)
		}
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		return false
	}
	pl, err := parseMediaPlaylist(body)
	if err != nil {
		return false
	}
	st.Update(src, pl)
	return true
}

func writePlaylist(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(b)
}

// serveSlotSegment serves /stream/channel/{id}/s/{slot}/seg-N.ts from that slot
// (a player holding an older playlist still gets the old slot's segments); when
// the slot is gone, from its directory on this disk.
func (h *Handler) serveSlotSegment(w http.ResponseWriter, r *http.Request) {
	id, ok := channelParam(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "slot")
	seg := chi.URLParam(r, "segment")
	if h.Slots == nil || !slot.Valid(name) || !strings.HasPrefix(seg, "seg-") || !strings.HasSuffix(seg, ".ts") ||
		strings.ContainsAny(seg, `/\`) || strings.Contains(seg, "..") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	h.Tracker.TouchHLS(id, r)
	hdr := http.Header{}
	if rg := r.Header.Get("Range"); rg != "" {
		hdr.Set("Range", rg)
	}
	resp, err := h.slotGet(r.Context(), name, "/stream/channel/"+id.String()+"/"+seg, hdr)
	if err == nil && resp.StatusCode == http.StatusOK || err == nil && resp.StatusCode == http.StatusPartialContent {
		copyResponse(w, resp, h.count("hls", "slot:"+name))
		return
	}
	if err == nil {
		_ = resp.Body.Close()
	} else if r.Context().Err() == nil {
		if live, _ := h.Slots.Live(); live == name {
			h.Slots.Suspect(r.Context(), name)
		}
	}
	if p, ok := h.slotSegmentFile(name, id, seg); ok {
		if f, ferr := os.Open(p); ferr == nil { //nolint:gosec // built from validated parts only (slotSegmentFile)
			defer f.Close()
			w.Header().Set("Content-Type", "video/mp2t")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			n, _ := io.Copy(w, f)
			tvmetrics.Bytes.Add(float64(n), "hls", "slot-disk:"+name)
			return
		}
	}
	http.NotFound(w, r)
}

// slotSegmentFile is a slot's segment file on this disk, built only from
// known parts: the instance name from the fixed list, the parsed channel id and
// the segment number (never the request's own strings).
func (h *Handler) slotSegmentFile(name string, id uuid.UUID, seg string) (string, bool) {
	if h.CacheRoot == "" {
		return "", false
	}
	inst := ""
	for _, n := range slot.Names {
		if n == name {
			inst = n
		}
	}
	num, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(seg, "seg-"), ".ts"), 10, 63)
	if inst == "" || err != nil {
		return "", false
	}
	root := filepath.Clean(h.CacheRoot)
	p := filepath.Join(root, "tv", inst, "channel-"+uuid.UUID(id).String(), "seg-"+strconv.FormatUint(num, 10)+".ts")
	if rel, err := filepath.Rel(root, p); err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return p, true
}

// proxyLiveSlot hands a request for a channel on this host to the live slot.
func (h *Handler) proxyLiveSlot(w http.ResponseWriter, r *http.Request) {
	live, _ := h.Slots.Live()
	hdr := http.Header{}
	if rg := r.Header.Get("Range"); rg != "" {
		hdr.Set("Range", rg)
	}
	resp, err := h.slotGet(r.Context(), live, r.URL.Path, hdr)
	if err != nil {
		if r.Context().Err() == nil {
			h.Slots.Suspect(r.Context(), live)
		}
		http.Error(w, "channel unavailable", http.StatusBadGateway)
		return
	}
	copyResponse(w, resp, h.count("hls", "slot:"+live))
}

// retimeWriter turns a client's sequence of TS sources into one continuous
// program (the push relay's Retimer, design §8.5's re-timestamping splicer):
// at each new source it waits for PAT/PMT and an IDR and continues the
// timestamps and continuity counters. A source whose streams differ restarts
// the program (a parameter change: the client's decoder starts over).
type retimeWriter struct {
	w  io.Writer
	rt *relay.Retimer
	n  int
}

func newRetimeWriter(w io.Writer) *retimeWriter { return &retimeWriter{w: w, rt: relay.NewRetimer()} }

// next announces a new source.
func (r *retimeWriter) next() {
	if r.n > 0 {
		r.rt.Switch()
	}
}

func (r *retimeWriter) Write(b []byte) (int, error) {
	out, err := r.rt.Process(b)
	if errors.Is(err, relay.ErrParamChange) {
		r.rt = relay.NewRetimer()
		out, err = r.rt.Process(b)
	}
	if err != nil {
		return 0, err
	}
	if len(out) > 0 {
		r.n += len(out)
		if _, werr := r.w.Write(out); werr != nil {
			return 0, werr
		}
	}
	return len(b), nil
}

// Flush satisfies http.Flusher (the client writer flushes per write).
func (r *retimeWriter) Flush() {}
