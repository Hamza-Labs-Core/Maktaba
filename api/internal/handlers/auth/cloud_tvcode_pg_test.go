package auth

import (
	"context"
	"errors"
	"testing"

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
	got, err := c.h.TVCodeApprover(ctx, a, "TVCD2345")
	if err != nil || got != viewer.ID {
		t.Fatalf("approver = %q, %v", got, err)
	}
	if _, err := c.h.TVCodeApprover(ctx, a, "TVCD2345"); !errors.Is(err, discoveryh.ErrCloudAssertionRefused) {
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
		if got, err := c.h.TVCodeApprover(ctx, tc.assertion, tc.code); !errors.Is(err, discoveryh.ErrCloudAssertionRefused) || got != "" {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}

	// a home with no cloud links approves nothing, and records nothing
	bare := &Handler{}
	if _, err := bare.TVCodeApprover(ctx, a, "TVCD2345"); !errors.Is(err, discoveryh.ErrCloudNotLinked) {
		t.Fatalf("no cloud: %v", err)
	}
	if err := bare.RecordTVCodeSignIn(ctx, "fam", viewer.ID); !errors.Is(err, discoveryh.ErrCloudNotLinked) {
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
	if err := c.h.RecordTVCodeSignIn(ctx, "", viewer.ID); err == nil {
		t.Fatal("recorded a sign-in with no family")
	}
	if err := c.h.RecordTVCodeSignIn(ctx, "family-tv-1", viewer.ID); err != nil {
		t.Fatal(err)
	}
	// play, whatever the link's own scope: a TV never manages the home
	if scope, err := c.h.Cloud.Store.ScopeOf(ctx, cloudlink.KindRefresh, "family-tv-1"); err != nil || scope != principal.CloudScopePlay {
		t.Fatalf("scope = %q, %v", scope, err)
	}
}
