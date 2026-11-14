package broadcast

// Takedowns on the home (Epic 50 story 50.24 §2). Maktaba Cloud's service admin accepts a
// copyright notice about a programme on a published channel; the cloud signs an instruction for
// this home (the broadcast signing key, typ "takedowns": the keys that sign the revocation list)
// and the home, asking every minute through the relay agent:
//
//   - verifies it (a forged, expired or another home's batch is refused);
//   - takes the item down (the programme that aired at the notice's time on the channel, or the
//     item named) or the source (a YouTube channel or playlist, a speaker, a label): a row in
//     broadcast_takedowns, which slot 0228's video_publishable_reason() reads, so the item stops
//     being publishable whatever made it so; the slot plays the slate and the next top-up
//     replaces it (story 50.23 §3);
//   - answers the cloud: applied (with the item's slot end when it is on air now, shown to staff;
//     the edge's slate keeps its own end), not_found (the item is already gone) or failed.
//
// A restore (after a counter-notice, or a withdrawn notice) clears the row the same way. Nothing
// of the library leaves the home: the cloud learns only the item's opaque id and its title, the
// guide's own fields.

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

const takedownsType = "takedowns"

// Instruction is one takedown or restore, as the cloud signed it.
type Instruction struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"` // takedown or restore
	NoticeID   string     `json:"notice_id,omitempty"`
	Restores   string     `json:"restores,omitempty"`
	Scope      string     `json:"scope"` // item or source
	Channel    string     `json:"channel,omitempty"`
	At         *time.Time `json:"at,omitempty"`
	ItemRef    string     `json:"item_ref,omitempty"`
	SourceKind string     `json:"source_kind,omitempty"`
	SourceRef  string     `json:"source_ref,omitempty"`
}

// TakedownAck is the home's answer to an instruction.
type TakedownAck struct {
	Result      string     `json:"result"` // applied, not_found, failed
	Detail      string     `json:"detail,omitempty"`
	ItemRef     string     `json:"item_ref,omitempty"`
	Programme   string     `json:"programme,omitempty"`
	AiringUntil *time.Time `json:"airing_until,omitempty"`
}

// ModerationCloud is what the home asks Maktaba Cloud about takedowns and reports (the relay
// agent in production, a fake in tests).
type ModerationCloud interface {
	Takedowns(ctx context.Context) (string, error)
	AckTakedown(ctx context.Context, id string, ack TakedownAck) error
	Moderation(ctx context.Context) (json.RawMessage, error)
	CounterNotice(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
}

var (
	// ErrTakedownSignature is a batch that does not verify, has expired, or names another home.
	ErrTakedownSignature = errors.New("broadcast: takedown instructions do not verify")
	uuidText             = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// VerifyInstructions checks a signed batch with the broadcast keys: its signature, its type, its
// expiry and, when the home knows its own id, that the batch is this home's.
func VerifyInstructions(keys map[string]ed25519.PublicKey, text, home string, now time.Time) ([]Instruction, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), revControlPrefix)
	if !ok {
		return nil, ErrTakedownSignature
	}
	body, sigText, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(sigText, ".") {
		return nil, ErrTakedownSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, ErrTakedownSignature
	}
	var p struct {
		Typ          string        `json:"typ"`
		Kid          string        `json:"kid"`
		Exp          int64         `json:"exp"`
		Home         string        `json:"home"`
		Instructions []Instruction `json:"instructions"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return nil, ErrTakedownSignature
	}
	pub, ok := keys[p.Kid]
	sig, err := base64.RawURLEncoding.DecodeString(sigText)
	if !ok || err != nil || !ed25519.Verify(pub, []byte(revControlDomain+"."+body), sig) {
		return nil, ErrTakedownSignature
	}
	if p.Typ != takedownsType || now.Unix() >= p.Exp || (home != "" && p.Home != home) {
		return nil, ErrTakedownSignature
	}
	return p.Instructions, nil
}

// applyTakedowns asks the cloud for this home's instructions, applies each and answers it. It
// needs the broadcast keys (without them no instruction can be trusted) and the cloud link.
func (s *Service) applyTakedowns(ctx context.Context) {
	if s.Moderation == nil || !s.Revocations.Enabled() || s.Store == nil || s.Store.DB == nil {
		return
	}
	text, err := s.Moderation.Takedowns(ctx)
	if err != nil {
		return // the cloud is away: the instructions wait on the cloud link
	}
	_, home := s.identity(ctx)
	list, err := VerifyInstructions(s.Revocations.Keys, text, home, s.now())
	if err != nil {
		s.log().Error("broadcast: takedown instructions refused", "err", err, "event", "broadcast_takedowns_refused", "alert", "SEV2")
		return
	}
	for _, in := range list {
		ack := s.ApplyInstruction(ctx, in)
		if err := s.Moderation.AckTakedown(ctx, in.ID, ack); err != nil {
			s.log().Warn("broadcast: a takedown's answer did not reach the cloud; it is asked again", "err", err, "id", in.ID)
		}
	}
}

// ApplyInstruction applies one instruction and says how it went.
func (s *Service) ApplyInstruction(ctx context.Context, in Instruction) TakedownAck {
	var ack TakedownAck
	var err error
	switch {
	case in.Kind == "restore":
		ack, err = s.restoreTakedown(ctx, in)
	case in.Kind == "takedown" && in.Scope == "item":
		ack, err = s.takeDownItem(ctx, in)
	case in.Kind == "takedown" && in.Scope == "source" && in.SourceRef != "":
		ack, err = s.takeDownSource(ctx, in)
	default:
		return TakedownAck{Result: "failed", Detail: "unknown instruction"}
	}
	if err != nil {
		s.log().Warn("broadcast: a takedown failed", "err", err, "id", in.ID)
		return TakedownAck{Result: "failed", Detail: "the home could not apply it"}
	}
	if ack.Result == "applied" {
		// the scheduler rewrites the published channels from the block on air (story 50.23 §3)
		s.RescheduleLimited(ctx)
		s.log().Warn("broadcast: Maktaba's instruction applied", "event", "broadcast_takedown", "id", in.ID, "kind", in.Kind,
			"scope", in.Scope, "item", ack.ItemRef)
		if ch := s.localChannel(ctx, in.Channel); ch != "" {
			s.notice(ctx, ch, "takedown."+in.Kind)
		}
	}
	return ack
}

// localChannel is the home's channel a cloud id names ("" when none).
func (s *Service) localChannel(ctx context.Context, cloudID string) string {
	if cloudID == "" {
		return ""
	}
	var ch string
	_ = s.Store.DB.QueryRowContext(ctx, `SELECT channel_id::text FROM broadcast_channels WHERE cloud_id = $1`, cloudID).Scan(&ch)
	return ch
}

// takeDownItem resolves the item (its id, else the programme that aired at the time on the
// channel) and takes it down.
func (s *Service) takeDownItem(ctx context.Context, in Instruction) (TakedownAck, error) {
	db := s.Store.DB
	var vid, title string
	if uuidText.MatchString(in.ItemRef) {
		err := db.QueryRowContext(ctx, `SELECT id::text, COALESCE(title, filename) FROM videos WHERE id = $1::uuid`, in.ItemRef).Scan(&vid, &title)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return TakedownAck{}, err
		}
	}
	ch := s.localChannel(ctx, in.Channel)
	if vid == "" && in.At != nil && ch != "" {
		err := db.QueryRowContext(ctx, `
            SELECT cp.video_id::text, COALESCE(NULLIF(cp.title_snapshot->>'title', ''), v.title, v.filename, '')
              FROM channel_programs cp JOIN videos v ON v.id = cp.video_id
             WHERE cp.channel_id = $1::uuid AND cp.start_at <= $2 AND cp.end_at > $2
               AND cp.kind IN ('program', 'filler', 'bumper')
             ORDER BY cp.seq DESC LIMIT 1`, ch, *in.At).Scan(&vid, &title)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return TakedownAck{}, err
		}
	}
	if vid == "" {
		// deleted before the takedown arrived, or no slot at that time: nothing to take down (the
		// notice still stands in the cloud)
		return TakedownAck{Result: "not_found", Detail: "no item at that reference or time", ItemRef: in.ItemRef}, nil
	}
	if _, err := db.ExecContext(ctx, `
        INSERT INTO broadcast_takedowns (id, notice_id, scope, video_id, channel_id, programme)
        VALUES ($1, $2, 'item', $3::uuid, NULLIF($4, '')::uuid, $5) ON CONFLICT (id) DO NOTHING`,
		in.ID, in.NoticeID, vid, ch, title); err != nil {
		return TakedownAck{}, err
	}
	ack := TakedownAck{Result: "applied", ItemRef: vid, Programme: title}
	ack.AiringUntil = s.airingUntil(ctx, `cp.video_id = $1::uuid`, vid)
	return ack, nil
}

// takeDownSource takes a whole source down.
func (s *Service) takeDownSource(ctx context.Context, in Instruction) (TakedownAck, error) {
	switch in.SourceKind {
	case KindChannel, KindPlaylist, KindSpeaker, KindLabel:
	default:
		return TakedownAck{Result: "failed", Detail: "unknown source kind"}, nil
	}
	if _, err := s.Store.DB.ExecContext(ctx, `
        INSERT INTO broadcast_takedowns (id, notice_id, scope, source_kind, source_ref, channel_id)
        VALUES ($1, $2, 'source', $3, $4, NULLIF($5, '')::uuid) ON CONFLICT (id) DO NOTHING`,
		in.ID, in.NoticeID, in.SourceKind, strings.TrimSpace(in.SourceRef), s.localChannel(ctx, in.Channel)); err != nil {
		return TakedownAck{}, err
	}
	ack := TakedownAck{Result: "applied", Programme: in.SourceRef}
	ack.AiringUntil = s.airingUntil(ctx, `video_taken_down(cp.video_id)`)
	return ack, nil
}

// airingUntil is the end of the slot on air now on a published channel whose item matches cond
// (nil: none is on air).
func (s *Service) airingUntil(ctx context.Context, cond string, args ...any) *time.Time {
	var end time.Time
	q := `SELECT cp.end_at FROM channel_programs cp JOIN broadcast_channels b ON b.channel_id = cp.channel_id
           WHERE b.state IN ('live', 'offline') AND cp.video_id IS NOT NULL AND cp.start_at <= now() AND cp.end_at > now()
             AND ` + cond + ` ORDER BY cp.end_at DESC LIMIT 1`
	if err := s.Store.DB.QueryRowContext(ctx, q, args...).Scan(&end); err != nil {
		return nil
	}
	u := end.UTC()
	return &u
}

// restoreTakedown clears the takedown a restore names (else the standing one on the same item or
// source).
func (s *Service) restoreTakedown(ctx context.Context, in Instruction) (TakedownAck, error) {
	res, err := s.Store.DB.ExecContext(ctx, `
        UPDATE broadcast_takedowns SET restored_at = now(), restored_by = 'maktaba:' || $1
         WHERE restored_at IS NULL
           AND (id = $2
             OR ($2 = '' AND scope = $3 AND (
                    (scope = 'item' AND video_id::text = $4)
                 OR (scope = 'source' AND source_kind = $5 AND lower(source_ref) = lower($6)))))`,
		in.ID, in.Restores, in.Scope, in.ItemRef, in.SourceKind, in.SourceRef)
	if err != nil {
		return TakedownAck{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return TakedownAck{Result: "not_found", Detail: "no standing takedown to restore", ItemRef: in.ItemRef}, nil
	}
	return TakedownAck{Result: "applied", ItemRef: in.ItemRef}, nil
}

// Takedown is one takedown the home applied, for the Broadcast page.
type Takedown struct {
	ID         string     `json:"id"`
	NoticeID   string     `json:"notice_id,omitempty"`
	Scope      string     `json:"scope"`
	VideoID    string     `json:"video_id,omitempty"`
	SourceKind string     `json:"source_kind,omitempty"`
	SourceRef  string     `json:"source_ref,omitempty"`
	Programme  string     `json:"programme"`
	AppliedAt  time.Time  `json:"applied_at"`
	RestoredAt *time.Time `json:"restored_at,omitempty"`
}

// Takedowns are the takedowns this home applied, newest first.
func (s *Store) Takedowns(ctx context.Context) ([]Takedown, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, notice_id, scope, COALESCE(video_id::text, ''), source_kind, source_ref,
        programme, applied_at, restored_at FROM broadcast_takedowns ORDER BY applied_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Takedown{}
	for rows.Next() {
		var t Takedown
		var restored sql.NullTime
		if err := rows.Scan(&t.ID, &t.NoticeID, &t.Scope, &t.VideoID, &t.SourceKind, &t.SourceRef, &t.Programme, &t.AppliedAt, &restored); err != nil {
			return nil, err
		}
		if restored.Valid {
			r := restored.Time
			t.RestoredAt = &r
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
