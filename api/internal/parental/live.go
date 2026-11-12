package parental

// The filter on the live feeds (Epic 57 story 57.03): /ws/home, /ws/library/{id},
// /ws/playback/{video_id} and the GraphQL subscriptions over the same channels.
// Each connection gets a LiveGuard; every event it would send (the first frame,
// the replayed ones and the live ones) is asked of it first.
//
// The limit is read again for every event (Gate.Limit: the cache, dropped when a
// policy changes), so a change made while a connection is open holds from the
// next event on. For a restricted account:
//
//   - the channel's own subject (the library, the video) must still be allowed,
//     or the feed ends;
//   - an event is sent only when every video, series, collection, library and
//     channel its payload names is allowed;
//   - an event that names nothing is sent only when its type is known to carry no
//     content (LiveNoContent), or its channel is one video's (the channel is the
//     subject);
//   - the operators' channels (jobs, queues) send nothing.
//
// It fails closed: when the limit or an answer cannot be read the event is not
// sent and the feed ends (the client connects again and is asked anew).

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// LiveNoContent are the event types that name no content and may reach a
// restricted account as they are. A new type is not sent to one until it is
// listed here or its payload names what it is about.
var LiveNoContent = map[string]bool{
	// the ETag of GET /api/capabilities, which each client reads with its own role
	"capabilities_changed": true,
}

// liveKeys maps a payload key to the kind of subject its value names. The same
// keys are read one level down in "video", "item", "job" and "subject" objects
// and in "items" and "videos" lists.
var liveKeys = map[string]string{
	"video_id":      SubjectVideo,
	"video_ids":     SubjectVideo,
	"series_id":     SubjectSeries,
	"collection_id": SubjectCollection,
	"library_id":    SubjectLibrary,
	"channel_id":    SubjectChannel,
}

// liveNested are the payload keys whose object (or list of objects) is read for
// subjects too; an "id" inside "video"/"videos" is a video's.
var liveNested = map[string]string{
	"video": SubjectVideo, "videos": SubjectVideo, "item": "", "items": "", "job": "", "subject": "",
}

// LiveGuard is one live connection's filter.
type LiveGuard struct {
	gate    *Gate
	p       *principal.Principal
	channel string
	// own is the subject the channel itself is about (zero: none).
	own Subject
	// q answers Allowed (nil: the gate's store).
	q RowQueryer

	mu      sync.Mutex
	checked bool // own was found allowed for the limit below
	limit   *Effective
	at      time.Time
}

// liveRecheck bounds how long the channel's own subject stays answered for one
// limit: a tier can change with no change to the account's policy.
const liveRecheck = 30 * time.Second

// LiveGuard is the filter of one connection of p to channel ("home",
// "library:<id>", "playback:<video id>", "jobs", "queues").
func (g *Gate) LiveGuard(p *principal.Principal, channel string) *LiveGuard {
	lg := &LiveGuard{gate: g, p: p, channel: channel}
	switch {
	case strings.HasPrefix(channel, "library:"):
		lg.own = Subject{Kind: SubjectLibrary, ID: strings.TrimPrefix(channel, "library:")}
	case strings.HasPrefix(channel, "playback:"):
		lg.own = Subject{Kind: SubjectVideo, ID: strings.TrimPrefix(channel, "playback:")}
	}
	return lg
}

func (lg *LiveGuard) queryer() (RowQueryer, error) {
	if lg.q != nil {
		return lg.q, nil
	}
	c := lg.gate.Cache()
	if c == nil {
		return nil, ErrNoCache
	}
	return c.Store().DB, nil
}

// Event reports whether an event of typ with payload may be sent on the
// connection now (send), and whether the connection must end (end: send is then
// false).
func (lg *LiveGuard) Event(ctx context.Context, typ string, payload map[string]any) (send, end bool) {
	if lg == nil || lg.gate == nil {
		return false, true
	}
	e, err := lg.gate.Limit(ctx, lg.p)
	if err != nil || e == nil {
		return false, true
	}
	if !e.Limits() {
		return true, false
	}
	if lg.channel == "jobs" || lg.channel == "queues" {
		return false, true
	}
	q, err := lg.queryer()
	if err != nil {
		return false, true
	}
	if lg.own.Kind != "" {
		ok, err := lg.ownAllowed(ctx, q, e)
		if err != nil || !ok {
			return false, true
		}
	}
	if LiveNoContent[typ] {
		return true, false
	}
	subjects, sound := liveSubjects(payload)
	if !sound {
		return false, false
	}
	if len(subjects) == 0 {
		// nothing named: only one video's channel says what the event is about
		return lg.own.Kind == SubjectVideo, false
	}
	var videos []string
	for _, s := range subjects {
		if s.Kind == SubjectVideo {
			videos = append(videos, s.ID)
			continue
		}
		ok, err := Allowed(ctx, q, e, s.Kind, s.ID)
		if err != nil {
			return false, true
		}
		if !ok {
			return false, false
		}
	}
	if len(videos) > 0 {
		ok, err := AllowedVideos(ctx, q, e, videos)
		if err != nil {
			return false, true
		}
		for _, id := range videos {
			// a video that is gone has nothing to hide, but an event about it is
			// not sent either: what it was cannot be asked any more
			if !ok[id] {
				return false, false
			}
		}
	}
	return true, false
}

// ownAllowed is whether the channel's own subject is allowed under e, answered
// once per limit and asked again after liveRecheck.
func (lg *LiveGuard) ownAllowed(ctx context.Context, q RowQueryer, e *Effective) (bool, error) {
	now := time.Now()
	lg.mu.Lock()
	fresh := lg.checked && lg.limit == e && now.Sub(lg.at) < liveRecheck
	lg.mu.Unlock()
	if fresh {
		return true, nil
	}
	ok, err := Allowed(ctx, q, e, lg.own.Kind, lg.own.ID)
	if err != nil || !ok {
		return false, err
	}
	lg.mu.Lock()
	lg.checked, lg.limit, lg.at = true, e, now
	lg.mu.Unlock()
	return true, nil
}

// liveSubjects is every subject a payload names. sound is false when a value
// under a subject key is not an id (the event is then not sent: it cannot be
// checked).
func liveSubjects(payload map[string]any) (out []Subject, sound bool) {
	sound = true
	add := func(kind string, v any) {
		switch x := v.(type) {
		case nil:
		case string:
			if x == "" {
				return
			}
			if _, ok := CanonicalUUID(x); !ok {
				sound = false
				return
			}
			out = append(out, Subject{Kind: kind, ID: x})
		case []string:
			for _, id := range x {
				if _, ok := CanonicalUUID(id); !ok {
					sound = false
					continue
				}
				out = append(out, Subject{Kind: kind, ID: id})
			}
		case []any:
			for _, it := range x {
				id, ok := it.(string)
				if _, isID := CanonicalUUID(id); !ok || !isID {
					sound = false
					continue
				}
				out = append(out, Subject{Kind: kind, ID: id})
			}
		default:
			sound = false
		}
	}
	read := func(m map[string]any, idKind string) {
		for k, v := range m {
			if kind, ok := liveKeys[k]; ok {
				add(kind, v)
			}
		}
		if idKind != "" {
			add(idKind, m["id"])
		}
		// a job's or a subject's own kind and id
		if kind, _ := m["subject_kind"].(string); kind != "" {
			switch kind {
			case SubjectVideo, SubjectSeries, SubjectCollection, SubjectLibrary, SubjectChannel:
				add(kind, m["subject_id"])
			default:
				sound = false
			}
		}
	}
	read(payload, "")
	for k, idKind := range liveNested {
		switch x := payload[k].(type) {
		case map[string]any:
			read(x, idKind)
		case []any:
			for _, it := range x {
				if m, ok := it.(map[string]any); ok {
					read(m, idKind)
				} else if _, isStr := it.(string); !isStr || idKind == "" {
					sound = false
				} else {
					add(idKind, it)
				}
			}
		case []map[string]any:
			for _, m := range x {
				read(m, idKind)
			}
		}
	}
	return out, sound
}
