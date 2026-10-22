package clusterauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
)

// The machines of the test (sessions name them by id: origin_node_id).
const (
	nodeA = "0d6a2f5e-7c41-4b8e-9f3a-2b1c4d5e6f70"
	nodeB = "1e7b3a6f-8d52-4c9f-a04b-3c2d5e6f7a81"
)

// #145: the sign-outs a node made while the home did not answer, handed over once it
// answers: sessions by the hash of their id, "sign out everywhere" by user and moment;
// only a node may hand them over.
func TestTheHomeTakesANodesSignOuts(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	us := users.New(db)
	u, err := us.Create(ctx, users.CreateInput{Username: "amal", Password: "pw-amal-123456"})
	if err != nil {
		t.Fatal(err)
	}
	ss := sessions.New(db)
	s1, _ := ss.Create(ctx, sessions.CreateInput{UserID: u.ID, OriginNodeID: nodeA})
	s2, _ := ss.Create(ctx, sessions.CreateInput{UserID: u.ID, OriginNodeID: nodeA})
	h := &Home{DB: db, Sessions: ss, Users: us}
	call := func(node string, req delegation.SignOutRequest) (int, delegation.SignOutAnswer) {
		b, _ := json.Marshal(req)
		r := httptest.NewRequest(http.MethodPost, "/api/cluster/delegate/signout", bytes.NewReader(b))
		if node != "" {
			r = r.WithContext(delegation.WithNode(r.Context(), node))
		}
		w := httptest.NewRecorder()
		h.SignOut(w, r)
		var a delegation.SignOutAnswer
		_ = json.Unmarshal(w.Body.Bytes(), &a)
		return w.Code, a
	}
	key := func(id string) string { s := sha256.Sum256([]byte(id)); return hex.EncodeToString(s[:]) }
	live := func(id string) bool {
		s, err := ss.Lookup(ctx, id)
		return err == nil && s.Active(time.Now())
	}
	if code, _ := call("", delegation.SignOutRequest{SessionSHA256: []string{key(s1.ID)}}); code != http.StatusUnauthorized || !live(s1.ID) {
		t.Fatalf("not a node: %d", code)
	}
	if code, a := call(nodeA, delegation.SignOutRequest{SessionSHA256: []string{key(s1.ID), key("unknown")}}); code != http.StatusOK ||
		a.Revoked != 1 || live(s1.ID) || !live(s2.ID) {
		t.Fatalf("by hash: %d %+v (s1 live %v, s2 live %v)", code, a, live(s1.ID), live(s2.ID))
	}
	// everywhere, before a moment: a session made since is kept
	before := time.Now()
	time.Sleep(10 * time.Millisecond)
	s3, _ := ss.Create(ctx, sessions.CreateInput{UserID: u.ID})
	if code, a := call(nodeA, delegation.SignOutRequest{UserID: u.ID, Before: &before}); code != http.StatusOK ||
		a.Revoked != 1 || live(s2.ID) || !live(s3.ID) {
		t.Fatalf("everywhere: %d %+v (s2 live %v, s3 live %v)", code, a, live(s2.ID), live(s3.ID))
	}
	if code, _ := call(nodeA, delegation.SignOutRequest{UserID: "not-a-user", Before: &before}); code != http.StatusBadRequest {
		t.Fatalf("a user id that is none: %d, want 400", code)
	}
}

// A machine ends only what was signed in through it: every machine's copy holds every
// session's hash, and none may sign out the sessions of another machine or of the
// home, nor sign out everywhere a person who never signed in through it.
func TestAMachineSignsOutOnlyWhatWasSignedInThroughIt(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	us := users.New(db)
	amal, err := us.Create(ctx, users.CreateInput{Username: "amal", Password: "pw-amal-123456"})
	if err != nil {
		t.Fatal(err)
	}
	badr, err := us.Create(ctx, users.CreateInput{Username: "badr", Password: "pw-badr-123456"})
	if err != nil {
		t.Fatal(err)
	}
	ss := sessions.New(db)
	onA, _ := ss.Create(ctx, sessions.CreateInput{UserID: amal.ID, OriginNodeID: nodeA})
	onB, _ := ss.Create(ctx, sessions.CreateInput{UserID: amal.ID, OriginNodeID: nodeB})
	atHome, _ := ss.Create(ctx, sessions.CreateInput{UserID: amal.ID})
	badrAtHome, _ := ss.Create(ctx, sessions.CreateInput{UserID: badr.ID})
	h := &Home{DB: db, Sessions: ss, Users: us}
	call := func(node string, req delegation.SignOutRequest) delegation.SignOutAnswer {
		t.Helper()
		b, _ := json.Marshal(req)
		r := httptest.NewRequest(http.MethodPost, "/api/cluster/delegate/signout", bytes.NewReader(b))
		r = r.WithContext(delegation.WithNode(r.Context(), node))
		w := httptest.NewRecorder()
		h.SignOut(w, r)
		var a delegation.SignOutAnswer
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &a) != nil {
			t.Fatalf("hand-over: %d %s", w.Code, w.Body.String())
		}
		return a
	}
	key := func(id string) string { s := sha256.Sum256([]byte(id)); return hex.EncodeToString(s[:]) }
	live := func(id string) bool {
		s, err := ss.Lookup(ctx, id)
		return err == nil && s.Active(time.Now())
	}
	// A names all three of amal's sessions: only the one made through it ends
	if a := call(nodeA, delegation.SignOutRequest{SessionSHA256: []string{key(onA.ID), key(onB.ID), key(atHome.ID)}}); a.Revoked != 1 ||
		live(onA.ID) || !live(onB.ID) || !live(atHome.ID) {
		t.Fatalf("by hash: %+v (on A %v, on B %v, at home %v)", a, live(onA.ID), live(onB.ID), live(atHome.ID))
	}
	// badr never signed in through A: A cannot sign him out everywhere
	before := time.Now().Add(time.Minute)
	if a := call(nodeA, delegation.SignOutRequest{UserID: badr.ID, Before: &before}); a.Revoked != 0 || !live(badrAtHome.ID) {
		t.Fatalf("everywhere, a person who never signed in through it: %+v (live %v)", a, live(badrAtHome.ID))
	}
	// amal did (her session through A has ended since): everywhere is everywhere
	if a := call(nodeA, delegation.SignOutRequest{UserID: amal.ID, Before: &before}); a.Revoked != 2 ||
		live(onB.ID) || live(atHome.ID) {
		t.Fatalf("everywhere: %+v (on B %v, at home %v)", a, live(onB.ID), live(atHome.ID))
	}
}
