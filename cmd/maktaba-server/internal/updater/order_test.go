package updater

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

func init() {
	// a stage's quick tries of a download: instant in the tests
	stageRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
}

// Only releaseverify's own failures are verify_* (the home blocks such a release); a
// download's error is download_failed (an attempt, tried again); an older build than the
// installed release is older_build; a failure classified already stays as it is.
func TestVerifyFailureKinds(t *testing.T) {
	code := func(err error) string {
		var f *Failure
		if !errors.As(verifyFailure(err), &f) {
			t.Fatalf("not a failure: %v", err)
		}
		return f.Code
	}
	for err, want := range map[error]string{
		errors.New("GET https://github.com/x: 502 Bad Gateway"):                        "download_failed",
		fmt.Errorf("download maktaba.tar.gz: %w", context.DeadlineExceeded):            "download_failed",
		errors.New("write /var/lib/maktaba/bin/releases/x: no space left on device"):   "download_failed",
		&releaseverify.Error{Kind: releaseverify.ErrChecksum, File: "a.tar.gz"}:        "verify_checksum",
		&releaseverify.Error{Kind: releaseverify.ErrSignature}:                         "verify_signature",
		&releaseverify.Error{Kind: releaseverify.ErrReplay}:                            "verify_replay",
		&releaseverify.Error{Kind: releaseverify.ErrOlderBuild}:                        "older_build",
		fmt.Errorf("stage: %w", &releaseverify.Error{Kind: releaseverify.ErrIdentity}): "verify_identity",
		Fail("release_incomplete", "no archive"):                                       "release_incomplete",
	} {
		if got := code(err); got != want {
			t.Errorf("%v: %s, want %s", err, got, want)
		}
	}
}

// A download that failed, or arrived damaged, is fetched again before the step fails; a
// release that is not what it says is not.
func TestRetryFetch(t *testing.T) {
	ctx := context.Background()
	tries := 0
	err := retryFetch(ctx, func() error {
		tries++
		if tries < 3 {
			return Fail("download_failed", "502")
		}
		return nil
	})
	if err != nil || tries != 3 {
		t.Fatalf("a download that comes back: %v after %d tries", err, tries)
	}
	tries = 0
	err = retryFetch(ctx, func() error { tries++; return Fail("verify_checksum", "damaged") })
	if f := (*Failure)(nil); !errors.As(err, &f) || f.Code != "verify_checksum" || tries != 3 {
		t.Fatalf("a damaged download that stays: %v after %d tries", err, tries)
	}
	tries = 0
	err = retryFetch(ctx, func() error { tries++; return Fail("verify_signature", "forged") })
	if err == nil || tries != 1 {
		t.Fatalf("a forged release fetched again: %v after %d tries", err, tries)
	}
}

// An app bundle or a runtime applies the release rules against the signed record of the
// release it committed last: an older build of its line (v0.3.0 after an rc.7 tagged after
// it) is older_build; a record of another release than the running one says nothing; a
// commit keeps the new record, never a lower one.
func TestCheckNotBack(t *testing.T) {
	dir := t.TempDir()
	at := func(s string) time.Time {
		v, _ := time.Parse(time.RFC3339, s)
		return v
	}
	res := func(v, signed string) *releaseverify.Result {
		return &releaseverify.Result{Manifest: &releaseverify.Manifest{Version: v}, ChecksumsSHA256: "c-" + v, SignedAt: at(signed)}
	}
	if err := checkNotBack(dir, "0.3.0-rc.7", res("0.3.0", "2026-10-24T05:44:07Z")); err != nil {
		t.Fatalf("no record: %v", err)
	}
	if err := keepSigned(dir, signedOf(res("0.3.0-rc.7", "2026-10-24T06:17:37Z"))); err != nil {
		t.Fatal(err)
	}
	var f *Failure
	if err := checkNotBack(dir, "0.3.0-rc.7", res("0.3.0", "2026-10-24T05:44:07Z")); !errors.As(err, &f) || f.Code != "older_build" {
		t.Fatalf("the older build: %v", err)
	}
	if err := checkNotBack(dir, "0.3.0-rc.7", res("0.3.1", "2026-10-24T17:57:06Z")); err != nil {
		t.Fatalf("0.3.1: %v", err)
	}
	// the app's own updater moved it to another release since: the record says nothing
	if err := checkNotBack(dir, "0.3.0-rc.8", res("0.3.0", "2026-10-24T05:44:07Z")); err != nil {
		t.Fatalf("a record of another release: %v", err)
	}
	// a lower record never replaces a higher one
	_ = keepSigned(dir, signedOf(res("0.3.0-rc.5", "2026-10-23T00:00:00Z")))
	if w, _ := signedRecord(dir).Load(); w.Version != "0.3.0-rc.7" || !w.SignedAt.Equal(at("2026-10-24T06:17:37Z")) {
		t.Fatalf("the record: %+v", w)
	}
}
