package guide

// The guide under parental controls (Epic 57, story 57.10). A restricted account
// gets the channels it is permitted; on those, a programme above its limit keeps its
// time and loses everything else (the apps show "Not available"). The exports feed
// other software and are refused.

import (
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// liveFilter is one request's parental filter. nil: nothing is filtered (the account
// is unrestricted, or the handler was built with no parental checks).
type liveFilter struct {
	view  *parental.LiveView
	chans map[string]parental.ChannelFacts
	progs *parental.ProgrammeVerdicts
}

// view is the request's parental view; nil when the account is unrestricted. An
// error means the policy could not be read: the caller refuses.
func (h *Handler) view(r *http.Request, p *principal.Principal) (*parental.LiveView, error) {
	if h.Live == nil {
		return nil, nil
	}
	v, err := h.Live.View(r.Context(), p)
	if err != nil {
		return nil, err
	}
	if !v.Restricted() {
		return nil, nil
	}
	return v, nil
}

// filter drops the channels the account is not permitted and judges the programmes
// of the rest over [start, end).
func (h *Handler) filter(r *http.Request, p *principal.Principal, chans []ChannelMeta, start, end time.Time) ([]ChannelMeta, *liveFilter, error) {
	v, err := h.view(r, p)
	if err != nil {
		return nil, nil, err
	}
	if v == nil {
		return chans, nil, nil
	}
	permitted, err := v.Channels(r.Context(), metaIDs(chans))
	if err != nil {
		return nil, nil, err
	}
	kept := make([]ChannelMeta, 0, len(chans))
	for _, c := range chans {
		if _, ok := permitted[lower(c.ID)]; ok {
			kept = append(kept, c)
		}
	}
	progs, err := v.Programmes(r.Context(), permitted, start, end)
	if err != nil {
		return nil, nil, err
	}
	return kept, &liveFilter{view: v, chans: permitted, progs: progs}, nil
}

// block is toBlock under the filter: a programme the account is not permitted keeps
// its channel, kind and time and nothing that says what it is.
func (f *liveFilter) block(p ProgramRow, now time.Time) Block {
	b := toBlock(p, now)
	if f == nil || f.progs.Allowed(p.ChannelID, p.Seq) {
		return b
	}
	return Block{
		ChannelID: b.ChannelID, Kind: b.Kind, Start: b.Start, Stop: b.Stop,
		IsLive: b.IsLive, Progress: b.Progress, Restricted: true,
	}
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'F' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// refuseExport answers 403 restricted to a restricted account (and refuses when the
// policy cannot be read). It reports whether it answered.
func (h *Handler) refuseExport(w http.ResponseWriter, r *http.Request, p *principal.Principal) bool {
	v, err := h.view(r, p)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
		return true
	}
	if v != nil {
		httperror.Write(w, r, parentalh.Restricted())
		return true
	}
	return false
}
