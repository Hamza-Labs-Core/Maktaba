package frontdoor

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/relay"
)

// RelaySource is the front door as the push relay's source (Epic 32.7 as
// amended by 33.8): the channel's TS from wherever its placement says, the
// home's own station or a node's signed live.ts, one source at a time. When
// the source ends (the node died, the channel moved) the relay calls Stream
// again and the front door resolves the channel anew, so a failover is a
// source switch, never a new push session.
func (h *Handler) RelaySource() relay.Source { return relaySource{h: h} }

type relaySource struct{ h *Handler }

// Attach is the relay's front-door lease: the channel stays in demand (the
// sweeper does not release it) while the push runs.
func (s relaySource) Attach(ch uuid.UUID) func() { return s.h.Tracker.AttachPush(ch) }

func (s relaySource) Stream(ctx context.Context, ch uuid.UUID, w io.Writer, started func(relay.Origin)) error {
	h := s.h
	p, err := h.Svc.Resolve(ctx, ch)
	if err != nil {
		return err
	}
	cw := newClientWriter(w, nil)
	if p.Host == h.Self && h.Slots != nil {
		// story 33.1: the live slot's TS; a failover (or a swap) ends it and the
		// relay's next Stream reads the new live slot, one push session
		err = h.slotTS(ctx, ch, cw, func(name string) {
			started(relay.Origin{Host: p.Host, Gen: p.Gen, Slot: name})
		})
		if err == nil && ctx.Err() == nil {
			err = errors.New("frontdoor: the slot's stream ended")
		}
		return err
	}
	started(relay.Origin{Host: p.Host, Gen: p.Gen})
	if p.Host == h.Self {
		err = h.Engine.StreamChannelTS(ctx, ch, cw)
	} else {
		err = h.remoteTS(ctx, p.Host, p.Gen, ch, cw)
	}
	if ctx.Err() == nil {
		h.Svc.Invalidate(ch) // resolve again: the host may be gone
	}
	if err == nil && ctx.Err() == nil {
		err = errors.New("frontdoor: the channel's stream ended")
	}
	return err
}
