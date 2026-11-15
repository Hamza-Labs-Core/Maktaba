package broadcast

import (
	"errors"
	"testing"
	"time"
)

// A batch verifies only with the broadcast key, under its own type, before it expires and for
// this home.
func TestVerifyInstructions(t *testing.T) {
	s := newRevSigner("cloud-1")
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	in := Instruction{ID: "a-1", Kind: "takedown", Scope: "item", ItemRef: "v-1"}
	good := s.signInstructions("takedowns", "srv-1", now, time.Hour, in)

	list, err := VerifyInstructions(s.keys(), good, "srv-1", now.Add(time.Minute))
	if err != nil || len(list) != 1 || list[0] != in {
		t.Fatalf("a good batch: %+v %v", list, err)
	}
	if _, err := VerifyInstructions(s.keys(), good, "", now); err != nil {
		t.Fatalf("a home that does not know its id yet: %v", err)
	}
	refused := map[string]struct {
		text string
		home string
		at   time.Time
	}{
		"another key":        {newRevSigner("x").signInstructions("takedowns", "srv-1", now, time.Hour, in), "srv-1", now},
		"expired":            {good, "srv-1", now.Add(time.Hour)},
		"another home":       {good, "srv-2", now},
		"a revocation list":  {s.sign(1, now), "srv-1", now},
		"another type":       {s.signInstructions("revocations-home", "srv-1", now, time.Hour, in), "srv-1", now},
		"not a control post": {"hello", "srv-1", now},
		"tampered":           {good[:len(good)-4] + "AAAA", "srv-1", now},
	}
	for name, c := range refused {
		if _, err := VerifyInstructions(s.keys(), c.text, c.home, c.at); !errors.Is(err, ErrTakedownSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
