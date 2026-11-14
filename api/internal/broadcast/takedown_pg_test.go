package broadcast

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"crypto/ed25519"
)

// signInstructions signs a batch the way the cloud does (cloud/internal/moderation).
func (s revSigner) signInstructions(typ, home string, issued time.Time, ttl time.Duration, list ...Instruction) string {
	if list == nil {
		list = []Instruction{}
	}
	j, _ := json.Marshal(map[string]any{"typ": typ, "kid": s.kid, "iat": issued.Unix(), "exp": issued.Add(ttl).Unix(),
		"schema": 1, "home": home, "instructions": list})
	body := base64.RawURLEncoding.EncodeToString(j)
	sig := ed25519.Sign(s.priv, []byte(revControlDomain+"."+body))
	return revControlPrefix + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// fakeModeration is the cloud's takedown routes as the relay agent answers them.
type fakeModeration struct {
	mu   sync.Mutex
	text string
	acks map[string]TakedownAck
}

func (f *fakeModeration) Takedowns(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text, nil
}

func (f *fakeModeration) AckTakedown(_ context.Context, id string, ack TakedownAck) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.acks == nil {
		f.acks = map[string]TakedownAck{}
	}
	f.acks[id] = ack
	return nil
}

func (f *fakeModeration) Moderation(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (f *fakeModeration) CounterNotice(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("not here")
}

func (f *fakeModeration) set(text string) {
	f.mu.Lock()
	f.text, f.acks = text, nil
	f.mu.Unlock()
}

func (f *fakeModeration) ack(id string) (TakedownAck, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.acks[id]
	return a, ok
}

// Story 50.24 §2 on the home, against the database: a signed takedown by time resolves the
// programme that airs then on the published channel, takes the item off (it is publishable no
// more, whatever made it so) and answers with the end of the slot on air now; a restore puts it
// back; a time with no slot is not_found; a source takedown (the reciter's library) takes every
// item from it; a forged batch applies nothing.
func TestTheHomeAppliesTakedowns(t *testing.T) {
	db, ch, admin := homeWithChannel(t)
	ctx := context.Background()
	s := newRevSigner("cloud-1")
	now := time.Now().UTC().Truncate(time.Second)
	cloud := &fakeCloud{plan: broadcasterPlan}
	store := &Store{DB: db}
	rev := &Revocations{Store: store, Source: &fakeRevSource{text: s.sign(1, now)}, Keys: s.keys(), Now: func() time.Time { return now }}
	mod := &fakeModeration{}
	svc := &Service{Store: store, Cloud: cloud, Revocations: rev, Moderation: mod, Now: func() time.Time { return now }, QuietHour: 99}
	if _, err := svc.SaveSetup(ctx, ch, SetupRequest{Slug: "seerah"}); err != nil {
		t.Fatal(err)
	}
	_ = store.AddUplink(ctx, 100_000, "publish")
	got, err := svc.Publish(ctx, ch, true, Admin{UserID: admin, Name: "Amina"})
	if err != nil || got.State != StateLive {
		t.Fatalf("publish: %+v %v", got, err)
	}
	var vid, cloudID string
	if err := db.QueryRow(`SELECT video_id::text FROM channel_programs WHERE channel_id = $1::uuid AND seq = 1`, ch).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT cloud_id FROM broadcast_channels WHERE channel_id = $1::uuid`, ch).Scan(&cloudID); err != nil || cloudID == "" {
		t.Fatalf("cloud id %q %v", cloudID, err)
	}
	reason := func() string {
		t.Helper()
		r, err := store.ItemReason(ctx, vid)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if reason() != "own" {
		t.Fatalf("before: %q", reason())
	}

	// the notice names 1 h 10 m from now: the second slot, the same recording as the one on air
	at := now.Add(70 * time.Minute)
	mod.set(s.signInstructions("takedowns", "", now, time.Hour,
		Instruction{ID: "a-1", Kind: "takedown", NoticeID: "n-1", Scope: "item", Channel: cloudID, At: &at}))
	svc.Tick(ctx)
	ack, ok := mod.ack("a-1")
	if !ok || ack.Result != "applied" || ack.ItemRef != vid || ack.Programme != "Seerah" {
		t.Fatalf("the takedown's answer: %+v %v", ack, ok)
	}
	// on air now: the slate holds until the first slot ends (48 minutes after it began)
	if ack.AiringUntil == nil || ack.AiringUntil.Before(now) || ack.AiringUntil.After(now.Add(49*time.Minute)) {
		t.Fatalf("airing until %v", ack.AiringUntil)
	}
	if reason() != "" {
		t.Fatalf("a taken down item is still publishable: %q", reason())
	}
	list, err := store.Takedowns(ctx)
	if err != nil || len(list) != 1 || list[0].NoticeID != "n-1" || list[0].VideoID != vid || list[0].RestoredAt != nil {
		t.Fatalf("the record: %+v %v", list, err)
	}

	// the same batch again (the ack was lost): applied once
	svc.Tick(ctx)
	if list, _ := store.Takedowns(ctx); len(list) != 1 {
		t.Fatalf("a repeated instruction added a row: %d", len(list))
	}

	// restored after a counter-notice
	mod.set(s.signInstructions("takedowns", "", now, time.Hour,
		Instruction{ID: "a-2", Kind: "restore", Restores: "a-1", Scope: "item", ItemRef: vid, Channel: cloudID}))
	svc.Tick(ctx)
	if ack, _ := mod.ack("a-2"); ack.Result != "applied" || reason() != "own" {
		t.Fatalf("the restore: %+v, reason %q", ack, reason())
	}
	if ack, _ := svc.restoreTakedown(ctx, Instruction{ID: "a-3", Kind: "restore", Restores: "a-1"}); ack.Result != "not_found" {
		t.Fatalf("a second restore: %+v", ack)
	}

	// a time with no slot (between 48 and 60 minutes): nothing to take down
	gap := now.Add(54 * time.Minute)
	if ack := svc.ApplyInstruction(ctx, Instruction{ID: "a-4", Kind: "takedown", Scope: "item", Channel: cloudID, At: &gap}); ack.Result != "not_found" {
		t.Fatalf("a gap: %+v", ack)
	}
	// the item by its id
	if ack := svc.ApplyInstruction(ctx, Instruction{ID: "a-5", Kind: "takedown", Scope: "item", ItemRef: vid}); ack.Result != "applied" || reason() != "" {
		t.Fatalf("by id: %+v", ack)
	}
	if ack := svc.ApplyInstruction(ctx, Instruction{ID: "a-6", Kind: "restore", Scope: "item", ItemRef: vid}); ack.Result != "applied" || reason() != "own" {
		t.Fatalf("a restore by the item: %+v", ack)
	}

	// a whole source: the speaker (the library it is in)
	if ack := svc.ApplyInstruction(ctx, Instruction{ID: "a-7", Kind: "takedown", Scope: "source", SourceKind: KindSpeaker, SourceRef: "seerah"}); ack.Result != "applied" ||
		ack.AiringUntil == nil || reason() != "" {
		t.Fatalf("a source: %+v, reason %q", ack, reason())
	}
	if ack := svc.ApplyInstruction(ctx, Instruction{ID: "a-8", Kind: "takedown", Scope: "source", SourceKind: "website", SourceRef: "x"}); ack.Result != "failed" {
		t.Fatalf("an unknown source kind: %+v", ack)
	}
	if ack := svc.ApplyInstruction(ctx, Instruction{ID: "a-9", Kind: "erase", Scope: "item"}); ack.Result != "failed" {
		t.Fatalf("an unknown instruction: %+v", ack)
	}
	if ack := svc.ApplyInstruction(ctx, Instruction{ID: "a-10", Kind: "restore", Scope: "source", SourceKind: KindSpeaker, SourceRef: "SEERAH"}); ack.Result != "applied" ||
		reason() != "own" {
		t.Fatalf("a source restore: %+v, reason %q", ack, reason())
	}

	// a batch another key signed applies nothing and answers nothing
	forger := newRevSigner("someone else")
	mod.set(forger.signInstructions("takedowns", "", now, time.Hour,
		Instruction{ID: "a-11", Kind: "takedown", Scope: "item", ItemRef: vid}))
	svc.Tick(ctx)
	if _, ok := mod.ack("a-11"); ok || reason() != "own" {
		t.Fatalf("a forged batch was applied: reason %q", reason())
	}
}

// Without the broadcast keys the home trusts no instruction and does not ask.
func TestTakedownsNeedTheKeys(t *testing.T) {
	mod := &fakeModeration{text: "mkbc1.x.y"}
	svc := &Service{Store: &Store{}, Moderation: mod}
	svc.applyTakedowns(context.Background())
	if len(mod.acks) != 0 {
		t.Fatal("answered without keys")
	}
}
