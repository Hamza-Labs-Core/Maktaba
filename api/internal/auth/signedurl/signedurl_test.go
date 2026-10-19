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
