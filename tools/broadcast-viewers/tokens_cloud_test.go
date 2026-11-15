package viewers

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// payloadOf decodes a signed token's claims.
func payloadOf(t *testing.T, tok, prefix string) map[string]any {
	t.Helper()
	body, _, ok := strings.Cut(strings.TrimPrefix(tok, prefix), ".")
	if !ok {
		t.Fatalf("not a token: %s", tok)
	}
	raw, err := b64.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func num(t *testing.T, v any) int64 {
	t.Helper()
	n, err := v.(json.Number).Int64()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The plan push is the cloud's byte for byte (vectors.json's plan, SignPlan).
func TestPlanPostMatchesTheCloudsVector(t *testing.T) {
	raw, err := os.ReadFile("../../cloud/internal/broadcast/testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		AtMS int64  `json:"at_ms"`
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	p := payloadOf(t, v.Plan, ControlPrefix)
	got, err := PlanPost(CloudTestKey("cloud-1"), Plan{Pub: p["pub"].(string), Version: num(t, p["version"]), Mode: p["mode"].(string),
		MaxRungs: int(num(t, p["max_rungs"])), MaxTopKbps: int(num(t, p["max_top_kbps"])), MaxViewers: int(num(t, p["max_viewers"]))},
		time.UnixMilli(v.AtMS))
	if err != nil {
		t.Fatal(err)
	}
	if got != v.Plan {
		t.Fatalf("plan push differs from the cloud's vector\n got %s\nwant %s", got, v.Plan)
	}
}

// The invite and the access post are the cloud's byte for byte (invite_vectors.json, which the
// edge's own tests read too).
func TestInviteAndAccessMatchTheCloudsVectors(t *testing.T) {
	raw, err := os.ReadFile("../../cloud/internal/broadcast/testdata/invite_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		AtMS   int64  `json:"at_ms"`
		Invite string `json:"invite"`
		Access string `json:"access"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(v.AtMS)
	key := CloudTestKey("cloud-1")
	ip := payloadOf(t, v.Invite, InvitePrefix)
	var chs []string
	for _, c := range ip["chs"].([]any) {
		chs = append(chs, c.(string))
	}
	inv, err := Invite(key, InviteClaims{Pub: ip["pub"].(string), Channels: chs, Inv: ip["inv"].(string), Max: int(num(t, ip["max"])),
		IssuedAt: at, TTL: time.Duration(num(t, ip["exp"])-num(t, ip["iat"])) * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if inv != v.Invite {
		t.Fatalf("invite differs from the cloud's vector\n got %s\nwant %s", inv, v.Invite)
	}

	ap := payloadOf(t, v.Access, ControlPrefix)
	st := AccessState{Pub: ap["pub"].(string), Version: num(t, ap["version"]), OpenAllowed: ap["open_allowed"].(bool),
		MaxSessionsPerIP: int(num(t, ap["max_sessions_per_ip"])), DailyMinutes: int(num(t, ap["daily_minutes"])),
		Channels: map[string]AccessChannel{}, Codes: map[string]string{}}
	for ch, c := range ap["channels"].(map[string]any) {
		m := c.(map[string]any)
		st.Channels[ch] = AccessChannel{Access: m["access"].(string), MaxViewers: int(num(t, m["max_viewers"])), Indexable: m["indexable"].(bool)}
	}
	for _, r := range ap["revoked_invites"].([]any) {
		st.RevokedInvites = append(st.RevokedInvites, r.(string))
	}
	for code, tok := range ap["codes"].(map[string]any) {
		st.Codes[code] = tok.(string)
	}
	acc, err := AccessPost(key, st, at)
	if err != nil {
		t.Fatal(err)
	}
	if acc != v.Access {
		t.Fatalf("access post differs from the cloud's vector\n got %s\nwant %s", acc, v.Access)
	}
	if _, err := Invite(key, InviteClaims{Pub: "p"}); err == nil {
		t.Fatal("an invite with no id or uses was signed")
	}
}

// The edge's signature on its usage posts verifies as the cloud verifies it (vectors.json's
// edge), and the rehearsal's own seed is a key.
func TestVerifyEdgePost(t *testing.T) {
	raw, err := os.ReadFile("../../cloud/internal/broadcast/testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Edge struct {
			Seed, Method, Path, Body, Signature string
			MS                                  int64
		} `json:"edge"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	e := v.Edge
	pub, err := EdgePublicKey(e.Seed)
	if err != nil {
		t.Fatal(err)
	}
	msText := strconv.FormatInt(e.MS, 10)
	if !VerifyEdgePost(pub, e.Method, e.Path, msText, e.Signature, []byte(e.Body)) {
		t.Fatal("the cloud's edge vector does not verify")
	}
	if VerifyEdgePost(pub, e.Method, e.Path, msText, e.Signature, []byte(e.Body+" ")) {
		t.Fatal("a changed body verified")
	}
	if _, err := EdgePublicKey(RehearsalEdgeSeed); err != nil {
		t.Fatal(err)
	}
	if _, err := EdgePublicKey("short"); err == nil {
		t.Fatal("a short seed was taken")
	}
}
