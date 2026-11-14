package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	discoveryh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/discovery"
)

// A TV's code approved on maktaba.tv (story 58.03): the cloud's tvcode assertion stands for the
// home user its account is linked to, once, for that code only; a user who could not sign in
// with the cloud approves nothing.
func TestTVCodeApprover(t *testing.T) {
	c := newCloudHome(t)
	viewer := c.user("tv-viewer", false)
	locked := c.user("tv-locked", false)
	changing := c.user("tv-changing", false)
	c.link(viewer.ID, "cloud-viewer", principal.CloudScopePlay)
	c.link(locked.ID, "cloud-locked", principal.CloudScopePlay)
	c.link(changing.ID, "cloud-changing", principal.CloudScopeManage)
	if _, err := c.db.Exec(`UPDATE users SET locked_until = now() + interval '1 hour' WHERE id = $1`, locked.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`UPDATE users SET must_change_password = true WHERE id = $1`, changing.ID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	a := c.cloud.Assertion(cloudlink.PurposeTVCode, "cloud-viewer", "TVCD2345")
	got, cloudUser, err := c.h.TVCodeApprover(ctx, a, "TVCD2345")
	if err != nil || got != viewer.ID || cloudUser != "cloud-viewer" {
		t.Fatalf("approver = %q %q, %v", got, cloudUser, err)
	}
	if _, _, err := c.h.TVCodeApprover(ctx, a, "TVCD2345"); !errors.Is(err, discoveryh.ErrCloudAssertionRefused) {
		t.Fatalf("replayed: %v", err)
	}

	for name, tc := range map[string]struct{ assertion, code string }{
		"another code":            {c.cloud.Assertion(cloudlink.PurposeTVCode, "cloud-viewer", "TVCE2345"), "TVCF2345"},
		"a sign-in assertion":     {c.cloud.Assertion(cloudlink.PurposeSignIn, "cloud-viewer", ""), "TVCG2345"},
		"an unlinked account":     {c.cloud.Assertion(cloudlink.PurposeTVCode, "cloud-stranger", "TVCH2345"), "TVCH2345"},
		"a locked user":           {c.cloud.Assertion(cloudlink.PurposeTVCode, "cloud-locked", "TVCJ2345"), "TVCJ2345"},
		"a password to change":    {c.cloud.Assertion(cloudlink.PurposeTVCode, "cloud-changing", "TVCK2345"), "TVCK2345"},
		"not an assertion at all": {"mkha1.x.y", "TVCM2345"},
	} {
		if got, cloudUser, err := c.h.TVCodeApprover(ctx, tc.assertion, tc.code); !errors.Is(err, discoveryh.ErrCloudAssertionRefused) || got != "" || cloudUser != "" {
			t.Errorf("%s: %q %q, %v", name, got, cloudUser, err)
		}
	}

	// a home with no cloud links approves nothing, and records nothing
	bare := &Handler{}
	if _, _, err := bare.TVCodeApprover(ctx, a, "TVCD2345"); !errors.Is(err, discoveryh.ErrCloudNotLinked) {
		t.Fatalf("no cloud: %v", err)
	}
	if err := bare.RecordTVCodeSignIn(ctx, "fam", viewer.ID, "cloud-viewer"); !errors.Is(err, discoveryh.ErrCloudNotLinked) {
		t.Fatalf("no cloud record: %v", err)
	}
}

// The TV's sign-in is recorded as a refresh-family cloud sign-in of its link, scope play, so
// Unlink revokes it.
func TestRecordTVCodeSignIn(t *testing.T) {
	c := newCloudHome(t)
	viewer := c.user("tv-recorded", false)
	c.link(viewer.ID, "cloud-recorded", principal.CloudScopeManage)
	ctx := context.Background()
	if err := c.h.RecordTVCodeSignIn(ctx, "", viewer.ID, "cloud-recorded"); err == nil {
		t.Fatal("recorded a sign-in with no family")
	}
	if err := c.h.RecordTVCodeSignIn(ctx, "family-tv-1", viewer.ID, "cloud-recorded"); err != nil {
		t.Fatal(err)
	}
	// play, whatever the link's own scope: a TV never manages the home
	if scope, err := c.h.Cloud.Store.ScopeOf(ctx, cloudlink.KindRefresh, "family-tv-1"); err != nil || scope != principal.CloudScopePlay {
		t.Fatalf("scope = %q, %v", scope, err)
	}
}

// The sign-in is recorded only while the link still names the cloud account that approved the
// TV, checked in the recording transaction: none at all, another account, or an approval with no
// account (a release before this one) records nothing.
func TestRecordTVCodeSignInChecksTheLink(t *testing.T) {
	c := newCloudHome(t)
	viewer := c.user("tv-relinked", false)
	gone := c.user("tv-unlinked", false)
	c.link(viewer.ID, "cloud-now", principal.CloudScopePlay)
	ctx := context.Background()
	for name, tc := range map[string]struct{ user, cloud, family string }{
		"no link":              {gone.ID, "cloud-was", "family-tv-gone"},
		"another account":      {viewer.ID, "cloud-was", "family-tv-other"},
		"no approving account": {viewer.ID, "", "family-tv-none"},
	} {
		err := c.h.RecordTVCodeSignIn(ctx, tc.family, tc.user, tc.cloud)
		if !errors.Is(err, discoveryh.ErrCloudLinkChanged) {
			t.Fatalf("%s: %v", name, err)
		}
		if scope, err := c.h.Cloud.Store.ScopeOf(ctx, cloudlink.KindRefresh, tc.family); err != nil || scope != "" {
			t.Fatalf("%s: recorded as %q, %v", name, scope, err)
		}
	}
	if err := c.h.RecordTVCodeSignIn(ctx, "family-tv-now", viewer.ID, "cloud-now"); err != nil {
		t.Fatalf("the approving account: %v", err)
	}
}

// The race itself: an unlink still in flight when the TV redeems. The record waits on the link
// row the unlink holds, and once the unlink commits it finds no link and records nothing; the
// family it was handed is revoked, so no token of it works.
func TestRecordTVCodeSignInWaitsForAnUnlinkInFlight(t *testing.T) {
	c := newCloudHome(t)
	viewer := c.user("tv-racing", false)
	c.link(viewer.ID, "cloud-racing", principal.CloudScopePlay)
	ctx := context.Background()
	const fam = "00000000-0000-4000-8000-0000005803f1"
	if _, err := c.db.Exec(`INSERT INTO refresh_tokens (id, family_id, user_id, hash, expires_at)
		VALUES (gen_random_uuid(), $1, $2, 'racing-hash', now() + interval '1 day')`, fam, viewer.ID); err != nil {
		t.Fatal(err)
	}

	unlink, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlink.Rollback() }()
	if _, err := unlink.Exec(`DELETE FROM home_user_links WHERE user_id = $1`, viewer.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.h.RecordTVCodeSignIn(ctx, fam, viewer.ID, "cloud-racing") }()
	select {
	case err := <-done:
		t.Fatalf("recorded past an unlink in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := unlink.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, discoveryh.ErrCloudLinkChanged) {
		t.Fatalf("after the unlink: %v", err)
	}
	var live int
	if err := c.db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE family_id = $1 AND revoked_at IS NULL`, fam).Scan(&live); err != nil || live != 0 {
		t.Fatalf("live tokens = %d, %v", live, err)
	}
}
