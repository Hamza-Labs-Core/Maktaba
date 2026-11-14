package broadcast

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
)

var update = flag.Bool("update", false, "rewrite testdata/vectors.json")

// testKey is a TEST key from a public label: never for production.
func testKey(t *testing.T, label string) Key {
	t.Helper()
	seed := sha256.Sum256([]byte("maktaba-broadcast TEST KEY, never for production: " + label))
	k, err := KeyFromSeed(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	return k
}

var vectorAt = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

func pubkeys(keys ...Key) map[string]ed25519.PublicKey {
	m := map[string]ed25519.PublicKey{}
	for _, k := range keys {
		m[k.Kid] = k.Priv.Public().(ed25519.PublicKey)
	}
	return m
}

func goodClaims() IngestClaims {
	return IngestClaims{Pub: "pub-1", Ch: ChannelID("srv-1", "7"), Rungs: []string{"src", "720p", "480p"}, Home: "srv-1", Kbps: 10000}
}

func TestIssueAndVerify(t *testing.T) {
	k := testKey(t, "cloud-1")
	tok, err := IssueIngest(k, goodClaims(), vectorAt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, IngestPrefix) {
		t.Fatalf("token %q", tok)
	}
	c, err := VerifyIngest(pubkeys(k), tok, vectorAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if c.Exp-c.Iat != int64(IngestTTL/time.Second) || c.Kid != k.Kid || c.Typ != "ingest" {
		t.Fatalf("claims %+v", c)
	}
	// expired after 24 h
	if _, err := VerifyIngest(pubkeys(k), tok, vectorAt.Add(IngestTTL)); err == nil {
		t.Fatal("an expired token verified")
	}
	// another key's set does not verify it
	if _, err := VerifyIngest(pubkeys(testKey(t, "cloud-2")), tok, vectorAt); err == nil {
		t.Fatal("verified with the wrong key")
	}
	// a control signature never passes as an ingest token
	ctl, _ := SignControl(k, "plan", map[string]any{"pub": "pub-1"}, vectorAt, time.Hour)
	if _, err := VerifyIngest(pubkeys(k), IngestPrefix+strings.TrimPrefix(ctl, ControlPrefix), vectorAt); err == nil {
		t.Fatal("a control post passed as an ingest token")
	}
}

func TestIssueRefusesBadClaims(t *testing.T) {
	k := testKey(t, "cloud-1")
	for name, mut := range map[string]func(*IngestClaims){
		"no rungs":      func(c *IngestClaims) { c.Rungs = nil },
		"five rungs":    func(c *IngestClaims) { c.Rungs = []string{"a", "b", "c", "d", "e"} },
		"slate":         func(c *IngestClaims) { c.Rungs = []string{"slate"} },
		"upper case":    func(c *IngestClaims) { c.Rungs = []string{"SRC"} },
		"repeated rung": func(c *IngestClaims) { c.Rungs = []string{"src", "src"} },
		"no kbps":       func(c *IngestClaims) { c.Kbps = 0 },
		"no publisher":  func(c *IngestClaims) { c.Pub = "" },
	} {
		c := goodClaims()
		mut(&c)
		if _, err := IssueIngest(k, c, vectorAt); !errors.Is(err, ErrBadClaims) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestChannelIDStableAndOpaque(t *testing.T) {
	a, b := ChannelID("srv-1", "7"), ChannelID("srv-1", "7")
	if a != b || len(a) != 32 {
		t.Fatalf("%q %q", a, b)
	}
	if ChannelID("srv-1", "8") == a || ChannelID("srv-2", "7") == a || strings.Contains(a, "srv") {
		t.Fatal("ids collide or leak the home")
	}
}

func TestLoadKeyFormats(t *testing.T) {
	k := testKey(t, "cloud-1")
	dir := t.TempDir()
	seed := k.Priv.Seed()
	for name, content := range map[string][]byte{
		"raw":    seed,
		"b64url": []byte(b64.EncodeToString(seed) + "\n"),
	} {
		p := dir + "/" + name
		if err := os.WriteFile(p, content, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadKey(p)
		if err != nil || got.Kid != k.Kid {
			t.Fatalf("%s: %v %v", name, got.Kid, err)
		}
	}
}

func TestVerifyEdgeRequest(t *testing.T) {
	k := testKey(t, "edge-1")
	body := []byte(`{"windows":[]}`)
	ms := vectorAt.UnixMilli()
	h := http.Header{}
	SignEdgeRequest(k.Priv, h, "POST", "/_edge/usage", ms, body)
	if h.Get(HeaderEdgeTime) != strconv.FormatInt(ms, 10) {
		t.Fatalf("time header %q", h.Get(HeaderEdgeTime))
	}
	pub := k.Priv.Public().(ed25519.PublicKey)
	if err := VerifyEdgeRequest(pub, "POST", "/_edge/usage", h, body, vectorAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEdgeRequest(pub, "POST", "/_edge/usage", h, []byte(`{}`), vectorAt); !errors.Is(err, ErrEdgeSignature) {
		t.Fatalf("another body: %v", err)
	}
	if err := VerifyEdgeRequest(pub, "POST", "/_edge/usage", h, body, vectorAt.Add(time.Hour)); !errors.Is(err, ErrEdgeSignature) {
		t.Fatalf("an old post: %v", err)
	}
}

// vectors are what both sides check: the edge (tokens.test.ts) verifies the Go-signed token and
// control posts, and signs the edge post byte-for-byte as Go does.
type vectors struct {
	Note         string       `json:"note"`
	AtMs         int64        `json:"at_ms"`
	CloudPubkeys string       `json:"cloud_pubkeys"`
	Ingest       string       `json:"ingest"`
	IngestClaims IngestClaims `json:"ingest_claims"`
	Plan         string       `json:"plan"`
	Revocations  string       `json:"revocations"`
	// Edge is a usage post as the edge signs it: Body is UsagePost's JSON with one window
	// (publisher.ts builds the same bytes), UsageReply the cloud's answer to it.
	Edge struct {
		Seed       string `json:"seed"`
		Public     string `json:"public"`
		Method     string `json:"method"`
		Path       string `json:"path"`
		Ms         int64  `json:"ms"`
		Body       string `json:"body"`
		Signature  string `json:"signature"`
		UsageReply string `json:"usage_reply"`
	} `json:"edge"`
	// State is a publisher's state; PlanState the Plan the edge holds for it (Plan above is
	// that Plan signed as a push).
	State     billing.PublisherState `json:"state"`
	PlanState Plan                   `json:"plan_state"`
	Channel   struct {
		Server string `json:"server"`
		Local  string `json:"local"`
		ID     string `json:"id"`
	} `json:"channel"`
}

func makeVectors(t *testing.T) []byte {
	k := testKey(t, "cloud-1")
	var v vectors
	v.Note = "TEST keys from public labels (tokens_test.go), never for production. go test ./internal/broadcast -run TestVectors -update rewrites this file."
	v.AtMs = vectorAt.UnixMilli()
	v.CloudPubkeys = PublicKeysJSON(k, testKey(t, "cloud-2"))
	var err error
	if v.Ingest, err = IssueIngest(k, goodClaims(), vectorAt); err != nil {
		t.Fatal(err)
	}
	v.IngestClaims, _ = VerifyIngest(pubkeys(k), v.Ingest, vectorAt)
	v.State = vectorState()
	v.PlanState = PlanFromState(v.State)
	if v.Plan, err = SignPlan(k, v.PlanState, vectorAt); err != nil {
		t.Fatal(err)
	}
	v.Revocations, _ = SignControl(k, "revocations", map[string]any{"version": 1, "pubs": []string{}, "channels": []string{ChannelID("srv-1", "7")}, "homes": []string{}}, vectorAt, 24*time.Hour)
	e := testKey(t, "edge-1")
	v.Edge.Seed = b64.EncodeToString(e.Priv.Seed())
	v.Edge.Public = b64.EncodeToString(e.Priv.Public().(ed25519.PublicKey))
	w := vectorWindow()
	body, _ := json.Marshal(UsagePost{Edge: "broadcast-edge", Windows: []UsageWindow{w}})
	v.Edge.Method, v.Edge.Path, v.Edge.Ms, v.Edge.Body = "POST", UsagePath, vectorAt.UnixMilli(), string(body)
	v.Edge.Signature = b64.EncodeToString(ed25519.Sign(e.Priv, []byte(EdgeSigningInput(v.Edge.Method, v.Edge.Path, v.Edge.Ms, body))))
	reply, _ := json.Marshal(UsageReply{Taken: []string{w.Key}, Duplicates: []string{}, Rejected: []Rejected{},
		States: []billing.PublisherState{v.State}, Plans: []Plan{v.PlanState}})
	v.Edge.UsageReply = string(reply)
	v.Channel.Server, v.Channel.Local = "srv-1", "7"
	v.Channel.ID = ChannelID("srv-1", "7")
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

// vectorPublisher is a home id (publishers are homes, story 50.20).
const vectorPublisher = "6f1c0a52-0d3e-4c55-9a51-3b1f0e7d2a10"

// vectorWindow is the 5 minutes before vectorAt, every counter set.
func vectorWindow() UsageWindow {
	start := vectorAt.Add(-billing.WindowLength)
	return UsageWindow{Key: billing.WindowKey(vectorPublisher, start), Publisher: vectorPublisher, WindowStart: start, WindowEnd: vectorAt,
		MinutesFull: 12, MinutesLowest: 3, MinutesStream: 2, SegmentsIn: 150, BytesIn: 123456789, PeakSessions: 9}
}

func vectorState() billing.PublisherState {
	return billing.PublisherState{Publisher: vectorPublisher, Plan: "broadcaster", Mode: "lowest", Enforce: true,
		Limits:   billing.Limits{MaxChannels: 3, MaxConcurrentViewers: 500},
		MaxRungs: 4, MaxTopKbps: 8000, Access: []string{"invite", "open"}, Month: "2026-11",
		Usage:            billing.BroadcastUsage{MinutesFull: 300000, MinutesLowest: 1200},
		AllowanceMinutes: 300000, FairUseMinutes: 600000, PAYG: billing.BroadcastPAYG{}, At: vectorAt}
}

func TestVectors(t *testing.T) {
	want := makeVectors(t)
	if *update {
		if err := os.WriteFile("testdata/vectors.json", want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("testdata/vectors.json is stale: go test ./internal/broadcast -run TestVectors -update")
	}
}
