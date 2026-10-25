package releaseverify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A node that cannot reach Sigstore's TUF repository reads it through its home (design
// §3.3): the root comes through the mirror, verified the same way, and says where it came
// from. Both out of reach keep the last good root, and the error names both.
func TestRootStoreRefreshThroughTheMirror(t *testing.T) {
	raw, err := os.ReadFile("trusted_root.json")
	if err != nil {
		t.Fatal(err)
	}
	prevPublic, prevMirror := fetchTUF, fetchTUFMirror
	t.Cleanup(func() { fetchTUF, fetchTUFMirror = prevPublic, prevMirror })
	fetchTUF = func(context.Context, string) ([]byte, error) { return nil, errors.New("no route to tuf-repo-cdn") }
	var via *TUFMirror
	fetchTUFMirror = func(_ context.Context, _ string, m *TUFMirror) ([]byte, error) {
		via = m
		return raw, nil
	}
	emb, _ := EmbeddedRootInfo()
	now := emb.FetchedAt.Add(24 * time.Hour)
	dir := t.TempDir()
	mirror := &TUFMirror{BaseURL: "http://home:8080/api/cluster/updates/tuf", Transport: http.DefaultTransport}
	s := &RootStore{Dir: dir, Now: func() time.Time { return now }, Mirror: mirror}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if via != mirror {
		t.Fatal("the mirror was not asked")
	}
	b, err := os.ReadFile(filepath.Join(dir, "trusted_root.meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m rootMeta
	if err := json.Unmarshal(b, &m); err != nil || m.Source != mirror.BaseURL || !m.FetchedAt.Equal(now) {
		t.Fatalf("meta = %+v, %v", m, err)
	}
	if _, info, err := s.TrustedMaterial(); err != nil || info.Source == "embedded" {
		t.Fatalf("the refreshed root is not used: %+v, %v", info, err)
	}

	fetchTUFMirror = func(context.Context, string, *TUFMirror) ([]byte, error) { return nil, errors.New("the home is away") }
	err = s.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no route to tuf-repo-cdn") || !strings.Contains(err.Error(), "the home is away") {
		t.Fatalf("both out of reach: %v", err)
	}
	if _, info, err := s.TrustedMaterial(); err != nil || !info.FetchedAt.Equal(now) {
		t.Fatalf("the last good root was lost: %+v, %v", info, err)
	}

	// a store with a Fetch of its own (tests, tools) never goes to the mirror
	via = nil
	fetchTUFMirror = func(_ context.Context, _ string, m *TUFMirror) ([]byte, error) {
		via = m
		return raw, nil
	}
	s.Fetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }
	if err := s.Refresh(context.Background()); err == nil || via != nil {
		t.Fatalf("a store with its own Fetch: %v, mirror asked: %v", err, via != nil)
	}
}
