package router

import (
	"context"
	"errors"
	"time"

	"github.com/go-chi/chi/v5"

	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Epic 57 story 57.14: time limits and time slots.

// errNoTimeLimits is every answer of a streaming client that cannot hold time
// limits: the keeper then refuses a restricted account's stream instead of letting
// it play uncounted.
var errNoTimeLimits = errors.New("the streaming service connected here keeps no time limits")

// timePusher adapts the streaming gRPC client to the time keeper.
type timePusher struct{ limits grpcstreaming.TimeLimits }

// newTimePusher is the keeper's pusher over client. A client that lacks the calls
// (nil, or a build without them) gives a pusher that always fails.
func newTimePusher(client grpcstreaming.Client) parental.TimePusher {
	limits, _ := client.(grpcstreaming.TimeLimits)
	return timePusher{limits: limits}
}

func (t timePusher) SetTimeLimit(ctx context.Context, userID, libraryID string, lim *parental.TimeLimit) error {
	if t.limits == nil {
		return errNoTimeLimits
	}
	if lim == nil {
		return t.limits.SetTimeLimit(ctx, userID, libraryID, nil)
	}
	return t.limits.SetTimeLimit(ctx, userID, libraryID, &grpcstreaming.TimeLimit{
		EndAt: lim.EndAt, Reason: lim.Reason, NextAllowedAt: lim.NextAllowedAt,
		SessionID: lim.SessionID, LeaseUntil: lim.LeaseUntil,
	})
}

func (t timePusher) ClearTimeLimits(ctx context.Context, userID string) error {
	if t.limits == nil {
		return errNoTimeLimits
	}
	return t.limits.ClearTimeLimits(ctx, userID)
}

func (t timePusher) SetTimeKept(ctx context.Context, userID string, kept bool) error {
	if t.limits == nil {
		return errNoTimeLimits
	}
	return t.limits.SetTimeKept(ctx, userID, kept)
}

func (t timePusher) TimeServed(ctx context.Context, userID, _ string, sessionID, _ string) (time.Time, bool, error) {
	if t.limits == nil {
		return time.Time{}, false, errNoTimeLimits
	}
	return t.limits.TimeServed(ctx, userID, sessionID)
}

// timeKeptTimeout bounds the signer's question of whether an account's time is
// kept (a cached policy read).
const timeKeptTimeout = 3 * time.Second

// refusingSigner signs for accounts whose time is not kept only: it is what a
// signer that cannot carry the session lease is wrapped in, so no stream URL of a
// kept account is ever signed without it.
type refusingSigner struct {
	inner streaming.URLSigner
	kept  func(userID string) (bool, error)
}

func (s refusingSigner) SignStreamURL(rawURL, sessionID, userID, libraryID string, ttl time.Duration) (string, time.Time, error) {
	on, err := s.kept(userID)
	if err != nil {
		return "", time.Time{}, err
	}
	if on {
		return "", time.Time{}, errors.New("this signer cannot sign a stream URL for an account whose time is kept")
	}
	return s.inner.SignStreamURL(rawURL, sessionID, userID, libraryID, ttl)
}

// MountParentalTime mounts the story's routes and returns the time keeper and the
// stream URL signer to use from here on: signer, told how to learn whether an
// account's time is kept, so every URL it signs says so and names its session
// (signedurl.Minter.KeepTime); or, for a signer that cannot be told, one that signs
// nothing for a kept account.
func MountParentalTime(r chi.Router, cache *parental.Cache, client grpcstreaming.Client, signer streaming.URLSigner) (*parental.TimeKeeper, streaming.URLSigner) {
	keeper := parental.NewTimeKeeper(cache, newTimePusher(client))
	// making an account restricted, and lifting it, reach streaming at once
	cache.Store().OnKeptChange = keeper.KeptChanged
	(&parentalh.Handler{Store: cache.Store(), Cache: cache, Time: keeper}).MountTime(r)
	if signer == nil {
		return keeper, nil
	}
	kept := func(userID string) (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeKeptTimeout)
		defer cancel()
		return keeper.KeptUser(ctx, userID)
	}
	if m, ok := signer.(interface {
		KeepTime(func(userID string) (bool, error))
	}); ok {
		m.KeepTime(kept)
		return keeper, signer
	}
	return keeper, refusingSigner{inner: signer, kept: kept}
}
