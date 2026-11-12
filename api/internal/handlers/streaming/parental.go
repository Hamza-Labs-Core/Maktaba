package streaming

// The parental filter's part in streaming (Epic 57 story 57.03). Every place a
// stream is granted or kept asks MayPlay; a restricted account's signed URLs are
// short, so a block made while a video plays ends it at the next URL.

import (
	"context"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// RestrictedURLTTL is the longest a restricted account's signed stream URL lives.
const RestrictedURLTTL = 15 * time.Minute

// MayPlay reports whether the request's account may play the video now. It is the
// one check of stream creation (OpenSession), of a session's read (GetSession) and
// of URL minting: a further rule on playing (the time rules of story 57.14) is
// added here and holds on all three. A refusal is told to the blocked hook. It
// fails closed: a check that cannot be made refuses.
func (h *Handler) MayPlay(ctx context.Context, videoID string) bool {
	return parental.Check(ctx, h.DB, parental.SubjectVideo, videoID)
}

// signedTTL is the lifetime of a signed stream URL for the request's account:
// ttl, or RestrictedURLTTL when the account is restricted and ttl is longer.
func signedTTL(ctx context.Context, ttl time.Duration) time.Duration {
	if parental.From(ctx).Limits() && ttl > RestrictedURLTTL {
		return RestrictedURLTTL
	}
	return ttl
}
