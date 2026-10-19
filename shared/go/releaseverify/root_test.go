package releaseverify_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

func TestEmbeddedRootParses(t *testing.T) {
	tr, err := rv.EmbeddedTrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.RekorLogs()) == 0 || len(tr.FulcioCertificateAuthorities()) == 0 {
		t.Fatal("embedded root has no Rekor log or Fulcio CA")
	}
	// rekor.sigstore.dev's log id (sha256 of its public key).
	if _, ok := tr.RekorLogs()["c0d23d6ad406973f9559f3ba2d1ca01f84147d8ffc5b8445c224f98b9591801d"]; !ok {
		t.Fatal("public-good Rekor log missing from the embedded root")
	}
	info, err := rv.EmbeddedRootInfo()
	if err != nil || info.FetchedAt.IsZero() {
		t.Fatalf("embedded root info = %+v, %v", info, err)
	}
}

// The release workflow sets MAKTABA_RELEASE_BUILD=1: a release never ships an
// embedded root more than 90 days old (plan 35.1 §3). Regular CI skips it so
// unrelated PRs do not fail when the root ages.
func TestEmbeddedRootAge(t *testing.T) {
	if os.Getenv("MAKTABA_RELEASE_BUILD") != "1" {
		t.Skip("release builds only")
	}
	info, _ := rv.EmbeddedRootInfo()
	if age := time.Since(info.FetchedAt); age > 90*24*time.Hour {
		t.Fatalf("embedded trusted root is %s old; run `go generate ./...` in shared/go/releaseverify and commit", age.Round(time.Hour))
	}
}

func TestVerifyExpiredRoot(t *testing.T) {
	var reported error
	s := &rv.RootStore{
		Now:       func() time.Time { return time.Now().Add(2 * rv.DefaultRootMaxAge) },
		OnExpired: func(err error) { reported = err },
	}
	_, _, err := s.TrustedMaterial()
	wantKind(t, err, rv.ErrRootExpired)
	if !errors.Is(reported, rv.ErrRootExpired) {
		t.Fatalf("expiry not reported: %v", reported)
	}
}

func TestRootStoreRefreshKeepsLastGood(t *testing.T) {
	dir := t.TempDir()
	emb, _ := rv.EmbeddedRootInfo()
	now := emb.FetchedAt.Add(24 * time.Hour)
	raw, err := os.ReadFile("trusted_root.json")
	if err != nil {
		t.Fatal(err)
	}
	s := &rv.RootStore{Dir: dir, Now: func() time.Time { return now },
		Fetch: func(context.Context, string) ([]byte, error) { return raw, nil }}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, info, err := s.TrustedMaterial()
	if err != nil || info.Source != filepath.Join(dir, "trusted_root.json") || !info.FetchedAt.Equal(now) {
		t.Fatalf("after refresh: %+v, %v", info, err)
	}

	// A failed refresh keeps the last good root.
	s.Fetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }
	if err := s.Refresh(context.Background()); err == nil {
		t.Fatal("failed refresh reported success")
	}
	// A refresh returning garbage is refused and changes nothing.
	s.Fetch = func(context.Context, string) ([]byte, error) { return []byte("{not json"), nil }
	if err := s.Refresh(context.Background()); err == nil {
		t.Fatal("garbage root accepted")
	}
	if _, info, err := s.TrustedMaterial(); err != nil || !info.FetchedAt.Equal(now) {
		t.Fatalf("last good root lost: %+v, %v", info, err)
	}

	// TUF's expired-metadata error is ErrRootExpired (critical notification).
	var reported error
	s.OnExpired = func(err error) { reported = err }
	s.Fetch = func(context.Context, string) ([]byte, error) {
		return nil, &metadata.ErrExpiredMetadata{Msg: "timestamp.json expired"}
	}
	wantKind(t, s.Refresh(context.Background()), rv.ErrRootExpired)
	if reported == nil {
		t.Fatal("expiry not reported")
	}

	// A damaged cache falls back to the embedded root.
	if err := os.WriteFile(filepath.Join(dir, "trusted_root.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, info, err := s.TrustedMaterial(); err != nil || info.Source != "embedded" {
		t.Fatalf("damaged cache: %+v, %v", info, err)
	}
}
