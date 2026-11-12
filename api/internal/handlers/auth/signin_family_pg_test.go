package auth

import (
	"context"
	"errors"
	"testing"
)

// Epic 57 story 57.04: every access token minted beside a refresh token names that
// token's family (`fam`), a refresh keeps it and carries the sign-in's device age
// limit (`ac`), and a limit that cannot be read mints no token.
func TestAccessTokenNamesItsSignIn(t *testing.T) {
	c := newCloudHome(t)
	u := c.user("layla", false)
	c.link(u.ID, cloudViewer, "play")

	family := func(userID string) string {
		t.Helper()
		var fam string
		if err := c.db.QueryRow(`SELECT CAST(family_id AS TEXT) FROM refresh_tokens WHERE user_id = $1 ORDER BY issued_at DESC LIMIT 1`, userID).Scan(&fam); err != nil {
			t.Fatal(err)
		}
		return fam
	}

	// a sign-in with the password
	a := c.do("POST", "/api/auth/login", "native", map[string]string{"username": "layla", "password": "correct horse battery"})
	if a.code != 200 {
		t.Fatalf("sign-in: %d %v", a.code, a.body)
	}
	local := c.claims(a.body["access_token"].(string))
	localFam := family(u.ID)
	if local.Fam == "" || local.Fam != localFam || local.Ac != nil {
		t.Fatalf("password sign-in: fam %q (family %q), ac %v", local.Fam, localFam, local.Ac)
	}
	localRefresh := a.body["refresh_token"].(string)

	// a sign-in through the cloud is its own sign-in
	access, cloudRefresh, ca := c.signIn(cloudViewer)
	if ca.code != 200 {
		t.Fatalf("cloud sign-in: %d %v", ca.code, ca.body)
	}
	cloud := c.claims(access)
	if cloud.Fam == "" || cloud.Fam == local.Fam || cloud.Fam != family(u.ID) {
		t.Fatalf("cloud sign-in: fam %q, password sign-in's %q", cloud.Fam, local.Fam)
	}

	// a refresh asks for the sign-in's limit and carries it
	var asked [][2]string
	limit := 9
	c.h.AgeCeiling = func(_ context.Context, userID, fam string) (*int, error) {
		asked = append(asked, [2]string{userID, fam})
		if fam == localFam {
			return &limit, nil
		}
		return nil, nil
	}
	ref := c.do("POST", "/api/auth/refresh", "", map[string]string{"refresh_token": localRefresh})
	if ref.code != 200 {
		t.Fatalf("refresh: %d %v", ref.code, ref.body)
	}
	got := c.claims(ref.body["access_token"].(string))
	if got.Fam != localFam || got.Ac == nil || *got.Ac != 9 {
		t.Fatalf("refreshed: fam %q, ac %v; want %q and 9", got.Fam, got.Ac, localFam)
	}
	if len(asked) != 1 || asked[0] != [2]string{u.ID, localFam} {
		t.Fatalf("asked for %v", asked)
	}
	localRefresh = ref.body["refresh_token"].(string)
	// the cloud sign-in's refresh: its own family, no limit there
	ref = c.do("POST", "/api/auth/refresh", "", map[string]string{"refresh_token": cloudRefresh})
	if ref.code != 200 {
		t.Fatalf("cloud refresh: %d %v", ref.code, ref.body)
	}
	if got := c.claims(ref.body["access_token"].(string)); got.Fam != cloud.Fam || got.Ac != nil || got.Scp != "play" {
		t.Fatalf("cloud refreshed: fam %q, ac %v, scp %q", got.Fam, got.Ac, got.Scp)
	}

	// fail closed: the limit cannot be read, so no token is minted
	c.h.AgeCeiling = func(context.Context, string, string) (*int, error) { return nil, errors.New("database away") }
	ref = c.do("POST", "/api/auth/refresh", "", map[string]string{"refresh_token": localRefresh})
	if ref.code != 500 || ref.body["access_token"] != nil {
		t.Fatalf("refresh with an unreadable limit: %d %v", ref.code, ref.body)
	}
}
