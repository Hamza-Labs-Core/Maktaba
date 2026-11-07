package fakeacmedns

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// An update through the API is answered as TXT for _acme-challenge.<zone> by a
// real resolver; another name gets nothing; a bad key is refused.
func TestUpdateThenResolve(t *testing.T) {
	s := &Server{Zone: "relay.test", User: "u", Key: "k"}
	api := httptest.NewServer(s)
	defer api.Close()
	post := func(user, key, body string) int {
		req, _ := http.NewRequest(http.MethodPost, api.URL+"/update", bytes.NewBufferString(body))
		req.Header.Set("X-Api-User", user)
		req.Header.Set("X-Api-Key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if post("u", "bad", `{"subdomain":"s","txt":"x"}`) != http.StatusUnauthorized {
		t.Error("a bad key was accepted")
	}
	for _, v := range []string{"apex-token", "wildcard-token"} {
		if st := post("u", "k", `{"subdomain":"s","txt":"`+v+`"}`); st != 200 {
			t.Fatalf("update: %d", st)
		}
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	go func() { _ = s.ServeDNS(pc) }()
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", pc.LocalAddr().String())
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	txt, err := r.LookupTXT(ctx, "_acme-challenge.relay.test")
	if err != nil || len(txt) != 2 || txt[0] != "apex-token" || txt[1] != "wildcard-token" {
		t.Fatalf("TXT: %v %v", txt, err)
	}
	if txt, _ := r.LookupTXT(ctx, "_acme-challenge.other.test"); len(txt) != 0 {
		t.Errorf("another zone answered %v", txt)
	}
	// the zone's SOA answers for a name in it (how Caddy finds the zone)
	q := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, l := range []string{"_acme-challenge", "relay", "test"} {
		q = append(q, byte(len(l)))
		q = append(q, l...)
	}
	q = append(q, 0, 0, 6, 0, 1) // SOA, IN
	out, err := s.Answer(q)
	if err != nil || len(out) <= len(q) || !bytes.Contains(out, []byte("admin")) {
		t.Errorf("SOA answer %x %v", out, err)
	}
	if s.Updates() != 2 {
		t.Errorf("updates %d", s.Updates())
	}
}
