package cf_test

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
)

func TestClientRetriesAndErrors(t *testing.T) {
	ctx := context.Background()
	f := cftest.New(t)
	f.Fail5xx = 2
	c := f.Client("acc1", "tok-secret")
	st, err := c.VerifyToken(ctx)
	if err != nil || st.Status != "active" {
		t.Fatalf("verify after two 503s: %+v %v", st, err)
	}
	if n := f.Count("GET /client/v4/user/tokens/verify"); n != 3 {
		t.Errorf("tries = %d, want 3", n)
	}
	f.Fail5xx = 10
	if _, err := c.ListLiveInputs(ctx); err == nil {
		t.Error("persistent 503 must fail")
	}
	f.Fail5xx = 0

	bad := f.Client("acc1", "wrong-token")
	_, err = bad.ListLiveInputs(ctx)
	ae, ok := cf.AsAPIError(err)
	if !ok || ae.Status != 401 || !ae.HasCode(1000) {
		t.Fatalf("error = %#v", err)
	}
	if !strings.Contains(err.Error(), "1000 Invalid API Token") || strings.Contains(err.Error(), "wrong-token") {
		t.Errorf("error text %q: needs the code and message, never the token", err)
	}
	if strings.Contains(c.String(), "tok-secret") {
		t.Error("String() leaks the token")
	}
}

func TestVerifyAccountToken(t *testing.T) {
	f := cftest.New(t)
	f.AccountToken = true
	st, err := f.Client("acc1", "tok-secret").VerifyToken(context.Background())
	if err != nil || !st.AccountOwned || st.Status != "active" {
		t.Fatalf("account token: %+v %v", st, err)
	}
}

func TestInputsBothShapes(t *testing.T) {
	ctx := context.Background()
	f := cftest.New(t)
	c := f.Client("acc1", "tok-secret")
	off := false
	in, err := c.CreateLiveInput(ctx, cf.CreateLiveInput{Meta: map[string]any{"name": "maktaba-pool-1"}, Enabled: &off})
	if err != nil || in.UID == "" || in.RTMPS == nil {
		t.Fatalf("create: %+v %v", in, err)
	}
	list, err := c.ListLiveInputs(ctx)
	if err != nil || len(list) != 1 || list[0].Name() != "maktaba-pool-1" {
		t.Fatalf("list (wrapped shape): %+v %v", list, err)
	}
	if err := c.DeleteLiveInput(ctx, in.UID); err != nil {
		t.Fatal(err)
	}
}

func TestSignToken(t *testing.T) {
	ctx := context.Background()
	f := cftest.New(t)
	k, err := f.Client("acc1", "tok-secret").CreateSigningKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	tok, err := cf.SignToken(k, "video123", 48*time.Hour, now,
		[]cf.AccessRule{{Type: "ip.geoip.country", Action: "allow", Country: []string{"JO"}}, {Type: "any", Action: "block"}})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q", tok)
	}
	var hdr map[string]string
	b, _ := base64.RawURLEncoding.DecodeString(parts[0])
	_ = json.Unmarshal(b, &hdr)
	if hdr["alg"] != "RS256" || hdr["kid"] != k.ID {
		t.Errorf("header %v", hdr)
	}
	var cl cf.TokenClaims
	b, _ = base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(b, &cl)
	if cl.Sub != "video123" || cl.Kid != k.ID || cl.Exp != now.Add(cf.MaxTokenTTL).Unix() || cl.Nbf != now.Unix()-30 || len(cl.AccessRules) != 2 {
		t.Errorf("claims %+v (exp capped at 24 h)", cl)
	}
	priv, err := cf.ParseSigningKey(k.PEM)
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&priv.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Errorf("signature: %v", err)
	}
}

func TestCustomerCode(t *testing.T) {
	if got := cf.CustomerCode(`{"hls":"https://customer-f33zs165nr7gyfy4.cloudflarestream.com/x/manifest/video.m3u8"}`); got != "f33zs165nr7gyfy4" {
		t.Errorf("got %q", got)
	}
	if cf.CustomerCode("nothing here") != "" {
		t.Error("false positive")
	}
}

func TestAnalyticsProbe(t *testing.T) {
	ctx := context.Background()
	f := cftest.New(t)
	c := f.Client("acc1", "tok-secret")
	days, err := c.MinutesViewed(ctx, time.Now().AddDate(0, 0, -1), 1)
	if err != nil || len(days) != 1 || days[0].Minutes != 12 {
		t.Fatalf("days %+v %v", days, err)
	}
	f.AnalyticsDenied = true
	if _, err := c.MinutesViewed(ctx, time.Now(), 1); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Errorf("denied: %v", err)
	}
}
