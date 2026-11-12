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
