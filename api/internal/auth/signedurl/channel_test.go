package signedurl

import (
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
)

const chn = "44444444-0000-4000-8000-00000000000A"

func TestSignChannelURL(t *testing.T) {
	m, set := newMinter(t)
	low := strings.ToLower(chn)
	signed, exp, err := m.SignChannelURL("/stream/channel/"+low+"/index.m3u8", chn, usr, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(signed, "/stream/channel/"+low+"/index.m3u8?sig=") {
		t.Fatalf("url %s", signed)
	}
	c := claimsOf(t, set, signed, AudChannel)
	if c.Sub != low || c.Usr != usr || len(c.Lib) != 0 {
		t.Fatalf("claims %+v", c)
	}
	if d := time.Until(exp); d <= 0 || d > DefaultChannelTTL+time.Minute {
		t.Fatalf("expiry in %s", d)
	}
	// a channel token is no video session's token
	if _, err := jwt.Verify(set, signed[strings.Index(signed, "sig=")+4:], AudSession); err == nil {
		t.Fatal("a channel token verified as a video session's")
	}
	for name, call := range map[string]func() error{
		"another channel's path": func() error {
			_, _, err := m.SignChannelURL("/stream/channel/"+strings.ToLower(vid)+"/index.m3u8", chn, usr, 0)
			return err
		},
		"a video path": func() error { _, _, err := m.SignChannelURL("/stream/"+sess+"/manifest.m3u8", chn, usr, 0); return err },
		"no user": func() error {
			_, _, err := m.SignChannelURL("/stream/channel/"+low+"/index.m3u8", chn, "", 0)
			return err
		},
		"a user that is no account": func() error {
			_, _, err := m.SignChannelURL("/stream/channel/"+low+"/index.m3u8", chn, "admin", 0)
			return err
		},
		"the nil account": func() error {
			_, _, err := m.SignChannelURL("/stream/channel/"+low+"/index.m3u8", chn, "00000000-0000-0000-0000-000000000000", 0)
			return err
		},
		"no channel": func() error { _, _, err := m.SignChannelURL("/stream/channel//index.m3u8", "", usr, 0); return err },
	} {
		if call() == nil {
			t.Errorf("%s was signed", name)
		}
	}
	if _, _, err := (*Minter)(nil).SignChannelURL("/stream/channel/"+low+"/index.m3u8", chn, usr, 0); err != ErrKeyUnavailable {
		t.Errorf("no key: %v", err)
	}
}

// The admin token's channel URL names no account and says so in the signed is_admin
// claim; an account's URL never carries that claim.
func TestSignChannelURLForAdminToken(t *testing.T) {
	m, set := newMinter(t)
	low := strings.ToLower(chn)
	signed, _, err := m.SignChannelURLForAdminToken("/stream/channel/"+low+"/index.m3u8", chn, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := claimsOf(t, set, signed, AudChannel)
	if c.Sub != low || c.Usr != AdminTokenUsr || !c.IsAdmin {
		t.Fatalf("claims %+v", c)
	}
	signed, _, err = m.SignChannelURL("/stream/channel/"+low+"/index.m3u8", chn, usr, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c := claimsOf(t, set, signed, AudChannel); c.IsAdmin {
		t.Fatalf("an account's URL carries is_admin: %+v", c)
	}
	if _, _, err := m.SignChannelURLForAdminToken("/stream/"+sess+"/manifest.m3u8", chn, 0); err == nil {
		t.Fatal("a video path was signed")
	}
}

// Review finding 11: a channel URL carries the sign-in it was signed for (its
// family and its device's age limit), so the stream is held to them for its whole
// life; a sign-in with neither carries neither.
func TestSignChannelURLForSignIn(t *testing.T) {
	m, set := newMinter(t)
	low := strings.ToLower(chn)
	const family = "0F0F0F0F-1111-4222-8333-444444444444"
	nine := 9
	signed, _, err := m.SignChannelURLForSignIn("/stream/channel/"+low+"/index.m3u8", chn, usr, family, &nine, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := claimsOf(t, set, signed, AudChannel)
	if c.Usr != usr || c.Fam != strings.ToLower(family) || c.Ac == nil || *c.Ac != 9 {
		t.Fatalf("claims %+v", c)
	}
	nothing := -1
	signed, _, err = m.SignChannelURLForSignIn("/stream/channel/"+low+"/index.m3u8", chn, usr, "", &nothing, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c := claimsOf(t, set, signed, AudChannel); c.Fam != "" || c.Ac == nil || *c.Ac != -1 {
		t.Fatalf("a ceiling of nothing with no family: %+v", c)
	}
	signed, _, _ = m.SignChannelURL("/stream/channel/"+low+"/index.m3u8", chn, usr, 0)
	if c := claimsOf(t, set, signed, AudChannel); c.Fam != "" || c.Ac != nil {
		t.Fatalf("no sign-in: %+v", c)
	}
	if _, _, err := m.SignChannelURLForSignIn("/stream/channel/"+low+"/index.m3u8", chn, usr, "not-a-family", nil, 0); err == nil {
		t.Fatal("a family that is no id was signed")
	}
	// the admin token's URL names no sign-in
	signed, _, _ = m.SignChannelURLForAdminToken("/stream/channel/"+low+"/index.m3u8", chn, 0)
	if c := claimsOf(t, set, signed, AudChannel); c.Fam != "" || c.Ac != nil {
		t.Fatalf("the admin token: %+v", c)
	}
}
