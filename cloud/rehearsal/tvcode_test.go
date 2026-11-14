//go:build cloudrehearsal

package rehearsal

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/seed"
)

// tv-code (Epic 58 story 58.03 T04): a TV at a relative's house signs in to a home through the
// relay. The canary home is online on the relay; a TV client asks this commit's api role for a
// code; the home claims it through its relay agent (cloudlink.TVCodesHandler, as
// maktaba-cloudlink serves it) with a single-use grant, as the home's cloud-approve does; the
// TV's poll gets the payload once (then 410, holding no secret), takes the relay connection, and
// redeems the grant on the home through the edge. A second TV's code stays pending throughout.
func tvCode(t *testing.T, c *cluster) Result {
	start := time.Now()
	work := filepath.Join(c.dir, ".work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	// the api profile mounts a symbols token; this scenario uploads none
	if err := os.WriteFile(filepath.Join(work, "symbols.token"), []byte("unused-in-tv-code\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ids, err := seed.Homes(context.Background(), c.db, 0, 1, "pro")
	if err != nil {
		t.Fatal(err)
	}
	c.startHomes(ids, 0, 1, 1)
	_, secret, slug := simhome.Creds(0)

	t.Cleanup(func() {
		logs, _ := c.compose("--profile", "api", "logs", "--no-color", "--timestamps", "api")
		_ = os.WriteFile(filepath.Join(outDir(t), "tv-code-api.log"), []byte(logs), 0o644)
		if os.Getenv("REHEARSAL_KEEP") == "" {
			_, _ = c.compose("--profile", "api", "rm", "-fsv", "api")
		}
	})
	c.must("--profile", "api", "up", "-d", "--wait", "api")
	ports := portsFor(c.project)
	api := fmt.Sprintf("http://127.0.0.1:%d", ports[1]+503)
	users := &http.Client{Timeout: 30 * time.Second, Transport: hostTransport{"api.test"}}

	post := func(path string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		resp, err := users.Post(api+path, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		if v == nil {
			v = map[string]any{"raw": string(raw)}
		}
		return resp.StatusCode, v
	}

	// the TV, with no account, and a second TV beside it
	st, created := post("/v1/tv/codes", map[string]string{"platform": "tizen", "app_version": "1.0.0"})
	if st != http.StatusCreated {
		t.Fatalf("a code: %d %v", st, created)
	}
	code, pollSecret := created["code"].(string), created["poll_secret"].(string)
	_, other := post("/v1/tv/codes", map[string]string{"platform": "webos"})
	if st, v := post("/v1/tv/codes/poll", map[string]string{"code": code, "poll_secret": pollSecret}); st != http.StatusAccepted || v["status"] != "pending" {
		t.Fatalf("pending: %d %v", st, v)
	}

	// the home approves it: its agent claims the code as the home, with a grant the home accepts
	agent := cloudlink.TVCodesHandler(&cloudlink.LinkReporter{CloudAPI: api,
		Creds: cloudlink.Credentials{ServerID: ids[slug], Secret: secret}, Client: users})
	g := make([]byte, 28)
	_, _ = rand.Read(g)
	grant := base64.RawURLEncoding.EncodeToString(g)
	simhome.AcceptGrant(ids[slug], grant)
	rec := httptest.NewRecorder()
	agent(rec, httptest.NewRequest(http.MethodGet, "/admin/cloud-link/tv-codes/"+code, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"platform":"tizen"`) {
		t.Fatalf("the home reads the code: %d %s", rec.Code, rec.Body)
	}
	approved := time.Now()
	rec = httptest.NewRecorder()
	agent(rec, httptest.NewRequest(http.MethodPost, "/admin/cloud-link/tv-codes/"+code+"/claim", strings.NewReader(`{"grant":"`+grant+`"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("the home claims the code: %d %s", rec.Code, rec.Body)
	}

	// the TV's next poll (within one interval) gets the payload, once
	time.Sleep(5 * time.Second)
	st, payload := post("/v1/tv/codes/poll", map[string]string{"code": code, "poll_secret": pollSecret})
	if st != http.StatusOK || payload["grant"] != grant || payload["home_id"] != ids[slug] {
		t.Fatalf("the payload: %d %v", st, payload)
	}
	delivered := time.Since(approved)
	st, again := post("/v1/tv/codes/poll", map[string]string{"code": code, "poll_secret": pollSecret})
	if raw, _ := json.Marshal(again); st != http.StatusGone || strings.Contains(string(raw), grant) {
		t.Fatalf("polled again: %d %s", st, raw)
	}
	if st, v := post("/v1/tv/codes/poll", map[string]string{"code": other["code"].(string), "poll_secret": other["poll_secret"].(string)}); st != http.StatusAccepted {
		t.Fatalf("the other TV: %d %v", st, v)
	}

	// it is not on the home's network: it takes the relay, and redeems the grant on the home
	var relayURL string
	conns, _ := payload["connections"].([]any)
	for _, x := range conns {
		if m, _ := x.(map[string]any); m["kind"] == "relay" {
			relayURL, _ = m["url"].(string)
		}
	}
	u, err := url.Parse(relayURL)
	if err != nil || !strings.HasPrefix(u.Host, slug+".") {
		t.Fatalf("no relay connection for %s in %v", slug, conns)
	}
	tv := &http.Client{Timeout: 30 * time.Second, Transport: hostTransport{u.Host}}
	resp, err := tv.Post(c.edge+"/api/pairing/device/token", "application/json", strings.NewReader(`{"device_code":"`+grant+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "tv-token-"+ids[slug]) {
		t.Fatalf("the TV redeems the grant through the relay: %d %s", resp.StatusCode, body)
	}
	resp, err = tv.Post(c.edge+"/api/pairing/device/token", "application/json", strings.NewReader(`{"device_code":"`+grant+`"}`))
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("the grant redeemed twice")
		}
	}
	took := time.Since(start)
	t.Logf("a TV signed in through the relay %s after the approval (in %s)", delivered.Round(time.Millisecond), took)
	return Result{Requests: 6, Notes: []string{fmt.Sprintf("signed in through the relay; the payload delivered once, %s after the approval",
		delivered.Round(time.Millisecond))}}
}
