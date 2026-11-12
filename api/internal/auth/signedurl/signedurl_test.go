package signedurl

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
)

const (
	sess = "0b8c8f5e-5b0a-4c38-9a59-0c5e7d1c2a11"
	vid  = "22222222-0000-4000-8000-000000000001"
	usr  = "33333333-0000-4000-8000-000000000001"
	lib  = "11111111-0000-4000-8000-000000000001"
)

func newMinter(t *testing.T) (*Minter, *keys.Set) {
	t.Helper()
	k, _, err := LoadOrCreateKey(filepath.Join(t.TempDir(), "stream-signing.key"))
	if err != nil {
		t.Fatal(err)
	}
	m := FromKey(k)
	return m, m.set
}

func claimsOf(t *testing.T, set *keys.Set, signed, aud string) *jwt.Claims {
	t.Helper()
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	tok := u.Query().Get("sig")
	if tok == "" {
		t.Fatalf("no sig on %s", signed)
	}
	c, err := jwt.Verify(set, tok, aud)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return c
}

func TestSignManifestURL_Claims(t *testing.T) {
	m, set := newMinter(t)
	now := time.Now().UTC()
	signed, exp, err := m.SignStreamURL("/stream/"+sess+"/manifest.m3u8", sess, usr, lib, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(signed, "/stream/"+sess+"/manifest.m3u8?sig=") {
		t.Fatalf("url %s", signed)
	}
	c := claimsOf(t, set, signed, AudSession)
	if c.Sub != sess || c.Usr != usr || len(c.Lib) != 1 || c.Lib[0] != lib || c.Jti == "" {
		t.Fatalf("claims %+v", c)
	}
	if d := time.Unix(c.Exp, 0).Sub(now); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("exp in %s, want ~1h", d)
	}
	if exp.Unix() != c.Exp {
		t.Fatalf("returned expiry %v != exp claim %d", exp, c.Exp)
	}
}

func TestSignDirectURL_ForTheVideo(t *testing.T) {
	m, set := newMinter(t)
	signed, _, err := m.SignStreamURL("/stream/direct/"+vid, sess, usr, lib, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c := claimsOf(t, set, signed, AudDirect)
	if c.Sub != vid {
		t.Fatalf("direct sub=%s want the video", c.Sub)
	}
}

func TestSign_RefusesAnotherSessionsURL(t *testing.T) {
	m, _ := newMinter(t)
	if _, _, err := m.SignStreamURL("/stream/"+vid+"/manifest.m3u8", sess, usr, lib, time.Hour); err == nil {
		t.Fatal("a URL of another session must not be signed for this one")
	}
	if _, _, err := m.SignStreamURL("/stream/"+sess+"/manifest.m3u8", sess, usr, "", time.Hour); err == nil {
		t.Fatal("the library is required (lib[] claim)")
	}
}

func TestSign_TTLClamped(t *testing.T) {
	m, set := newMinter(t)
	signed, _, err := m.SignStreamURL("/stream/"+sess+"/manifest.mpd", sess, usr, lib, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c := claimsOf(t, set, signed, AudSession)
	if d := time.Until(time.Unix(c.Exp, 0)); d > DefaultMaxTTL+time.Minute {
		t.Fatalf("ttl %s above the cap", d)
	}
}

func TestSign_NoKey(t *testing.T) {
	var m *Minter
	if _, _, err := m.SignManifestURL(sess, usr, lib, time.Hour); err != ErrKeyUnavailable {
		t.Fatalf("err=%v", err)
	}
	if _, _, err := New(keys.NewSet(0)).SignManifestURL(sess, usr, lib, time.Hour); err != ErrKeyUnavailable {
		t.Fatalf("empty set: err=%v", err)
	}
}

func TestLoadOrCreateKey_StableAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "stream-signing.key")
	k1, created, err := LoadOrCreateKey(path)
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	k2, created, err := LoadOrCreateKey(path)
	if err != nil || created {
		t.Fatalf("reload: %v %v", created, err)
	}
	if k1.KID != k2.KID || k1.KID == "" {
		t.Fatalf("kid changed across loads: %s %s", k1.KID, k2.KID)
	}
	if st, err := os.Stat(path); err != nil || (st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("key file mode: %v %v", st, err)
	}
	if _, err := ParseKey([]byte("not a key")); err == nil {
		t.Fatal("garbage must not parse")
	}
}

// Epic 52 (story 52.8): a review clip is signed for its video, like direct play.
func TestSignClipURL_ForTheVideo(t *testing.T) {
	m, set := newMinter(t)
	signed, _, err := m.SignStreamURL("/stream/clip/"+vid+"?track=1&start=2&end=5", vid, usr, lib, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if c := claimsOf(t, set, signed, AudDirect); c.Sub != vid {
		t.Fatalf("clip sub=%s want the video", c.Sub)
	}
}

// Epic 57 story 57.14, security finding 4: every stream URL signed for an account
// whose time is kept names its stream session (tls), on the session route and on
// direct play alike; an unlimited account's URL does not; and when it cannot be
// told whether the account is kept, nothing is signed.
func TestKeptAccountsURLsNameTheirSession(t *testing.T) {
	m, set := newMinter(t)
	const child = "44444444-0000-4000-8000-000000000001"
	var fail error
	m.KeepTime(func(userID string) (bool, error) { return userID == child, fail })

	for _, c := range []struct{ raw, aud string }{
		{"/stream/" + sess + "/manifest.m3u8", AudSession},
		{"/stream/" + sess + "/manifest.mpd", AudSession},
		{"/stream/direct/" + vid, AudDirect},
	} {
		signed, _, err := m.SignStreamURL(c.raw, sess, child, lib, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if got := claimsOf(t, set, signed, c.aud); got.TLS != sess || got.Tk != "1" {
			t.Errorf("%s for a kept account: tls %q tk %q, want the session and 1", c.raw, got.TLS, got.Tk)
		}
		signed, _, err = m.SignStreamURL(c.raw, sess, usr, lib, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if got := claimsOf(t, set, signed, c.aud); got.TLS != "" || got.Tk != "0" {
			t.Errorf("%s for an unlimited account: tls %q tk %q, want none and 0", c.raw, got.TLS, got.Tk)
		}
	}
	// a clip is signed with its video id for a session: no stream session has that
	// id, so streaming holds no lease for it and refuses it
	signed, _, err := m.SignStreamURL("/stream/clip/"+vid+"?track=0", vid, child, lib, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got := claimsOf(t, set, signed, AudDirect); got.TLS != vid {
		t.Errorf("a kept account's clip: tls %q", got.TLS)
	}
	// a signer that was never told how to find out says neither, so streaming
	// refuses what it signs: forgetting the wiring cannot hand out free time
	bare, bareSet := newMinter(t)
	signed, _, err = bare.SignStreamURL("/stream/direct/"+vid, sess, child, lib, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := claimsOf(t, bareSet, signed, AudDirect); got.Tk != "" || got.TLS != "" {
		t.Errorf("a signer with no time keeper: tk %q tls %q, want neither", got.Tk, got.TLS)
	}
	fail = os.ErrDeadlineExceeded
	for _, who := range []string{child, usr} {
		if _, _, err := m.SignStreamURL("/stream/direct/"+vid, sess, who, lib, time.Hour); err == nil {
			t.Errorf("signed for %s although the time rules could not be read", who)
		}
	}
}
