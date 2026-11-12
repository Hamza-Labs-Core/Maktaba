package channels

// Channels under parental controls (Epic 57, story 57.10): a restricted account
// lists and reads only the channels it is permitted, and what such a channel plays
// is named only when the programme is within its limit.

import (
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// liveFilter is one request's parental filter over channels. nil: nothing is
// filtered (an unrestricted account, or a handler built with no parental checks).
type liveFilter struct {
	chans map[string]parental.ChannelFacts
	progs *parental.ProgrammeVerdicts
}

// filter judges the channels ids and their programmes over [start, end) for the
// request's account. An error means the policy could not be read: the caller refuses.
func (h *Handler) filter(r *http.Request, p *principal.Principal, ids []string, start, end time.Time) (*liveFilter, error) {
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
	chans, err := v.Channels(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	progs, err := v.Programmes(r.Context(), chans, start, end)
	if err != nil {
		return nil, err
	}
	return &liveFilter{chans: chans, progs: progs}, nil
}

// channel reports whether the account is permitted the channel.
func (f *liveFilter) channel(id string) bool {
	if f == nil {
		return true
	}
	_, ok := f.chans[strings.ToLower(id)]
	return ok
}

// playingAt reports whether what the channel plays at t may be named.
func (f *liveFilter) playingAt(id string, t time.Time) bool {
	return f == nil || f.progs.AllowedAt(id, t)
}

// programme reports whether the programme seq of the channel may be named.
func (f *liveFilter) programme(id string, seq int64) bool {
	return f == nil || f.progs.Allowed(id, seq)
}
