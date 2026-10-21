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
	s1, _ := ss.Create(ctx, sessions.CreateInput{UserID: u.ID})
	s2, _ := ss.Create(ctx, sessions.CreateInput{UserID: u.ID})
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
	if code, a := call("node-1", delegation.SignOutRequest{SessionSHA256: []string{key(s1.ID), key("unknown")}}); code != http.StatusOK ||
		a.Revoked != 1 || live(s1.ID) || !live(s2.ID) {
		t.Fatalf("by hash: %d %+v (s1 live %v, s2 live %v)", code, a, live(s1.ID), live(s2.ID))
	}
	// everywhere, before a moment: a session made since is kept
	before := time.Now()
	time.Sleep(10 * time.Millisecond)
	s3, _ := ss.Create(ctx, sessions.CreateInput{UserID: u.ID})
	if code, a := call("node-1", delegation.SignOutRequest{UserID: u.ID, Before: &before}); code != http.StatusOK ||
		a.Revoked != 1 || live(s2.ID) || !live(s3.ID) {
		t.Fatalf("everywhere: %d %+v (s2 live %v, s3 live %v)", code, a, live(s2.ID), live(s3.ID))
	}
}
