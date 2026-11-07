package directaccess

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// fakeAgent is the relay agent and the cloud behind it: it assigns a name, signs CSRs with a
// test CA (as the worker would after ACME) and keeps the endpoints it is told.
type fakeAgent struct {
	t      *testing.T
	ca     *x509.Certificate
	caKey  *ecdsa.PrivateKey
	mu     sync.Mutex
	st     AgentStatus
	posted []AgentRequest
	csrs   int
	life   time.Duration
}

func newFakeAgent(t *testing.T) (*fakeAgent, *httptest.Server) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	a := &fakeAgent{t: t, ca: ca, caKey: k, life: 90 * 24 * time.Hour}
	a.st.Online = true
	srv := httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(srv.Close)
	return a, srv
}

func (a *fakeAgent) serve(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(a.st)
		return
	}
	var req AgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	a.posted = append(a.posted, req)
	if req.AskName {
		a.st.Domain, a.st.HomeID = "direct-a.example", HomeID("srv-1")
	}
	if len(req.CSR) > 0 {
		a.csrs++
		csr, err := x509.ParseCertificateRequest(req.CSR)
		if err != nil || csr.CheckSignature() != nil {
			a.t.Errorf("a bad CSR: %v", err)
			return
		}
		if len(csr.DNSNames) != 1 || csr.DNSNames[0] != "*."+HomeID("srv-1")+".direct-a.example" {
			a.t.Errorf("CSR names %v", csr.DNSNames)
		}
		now := time.Now()
		tpl := &x509.Certificate{SerialNumber: big.NewInt(int64(a.csrs + 1)), DNSNames: csr.DNSNames,
			NotBefore: now.Add(-time.Minute), NotAfter: now.Add(a.life), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, err := x509.CreateCertificate(rand.Reader, tpl, a.ca, csr.PublicKey, a.caKey)
		if err != nil {
			a.t.Errorf("sign: %v", err)
			return
		}
		a.st.Chain = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
			string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.ca.Raw}))
	}
	if req.CheckNow {
		a.st.Check = &Check{Result: "ok", Port: 8444, At: time.Now()}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *fakeAgent) requests() []AgentRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AgentRequest(nil), a.posted...)
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

// Story 50.15 (T06's home side, T13): with direct access on, the home asks its name, sends a
// CSR for *.<home id>.<domain> (the key stays in its own 0600 file), installs the chain and
// serves it on the direct listener, which answers /api/health with the home's server_id, marks
// requests as from the internet and drops forwarding headers a client sent. No plan is read:
// a Free home does the same.
func TestTheCertificateAndTheListener(t *testing.T) {
	agent, agentSrv := newFakeAgent(t)
	var seen http.Header
	var seenMu sync.Mutex
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		seen = r.Header.Clone()
		seenMu.Unlock()
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer web.Close()
	up, _ := url.Parse(web.URL)
	dir := t.TempDir()
	s := &Service{
		Files:    &Files{Dir: filepath.Join(dir, "direct")},
		Agent:    &Agent{Addr: strings.TrimPrefix(agentSrv.URL, "http://")},
		Listen:   "127.0.0.1:0",
		Upstream: up,
		ServerID: func() string { return "srv-1" },
		Settings: func(context.Context) (Settings, error) { return Settings{Enabled: true, MapPort: false}, nil },
		LAN:      func(port int) []string { return []string{"192.168.1.20:" + itoa(port)} },
		Poll:     20 * time.Millisecond,
	}
	if st := s.Status(); st.LANURLs != nil {
		t.Fatalf("LAN names before a certificate: %v", st.LANURLs)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.RunCert(ctx) }()
	go func() { defer wg.Done(); s.RunListener(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	waitUntil(t, "the certificate", func() bool { st := s.Status(); return !st.NotAfter.IsZero() && st.Listening })
	st := s.Status()
	if st.Name != "*."+HomeID("srv-1")+".direct-a.example" {
		t.Fatalf("name %q", st.Name)
	}
	// story 50.15 §3: the LAN names the settings page tries from the viewer's browser
	if want := "https://192-168-1-20." + HomeID("srv-1") + ".direct-a.example:" + itoa(st.Internal); len(st.LANURLs) != 1 || st.LANURLs[0] != want {
		t.Fatalf("lan_urls %v, want [%s]", st.LANURLs, want)
	}
	if fi, err := os.Stat(filepath.Join(dir, "direct", "key.pem")); err != nil || (fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("key file: %v %v", fi, err)
	}
	for _, r := range agent.requests() {
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "PRIVATE KEY") {
			t.Fatal("the key left the home")
		}
	}

	// the listener, as the cloud's outside check reaches it
	roots := x509.NewCertPool()
	roots.AddCert(agent.ca)
	name := "81-2-3-4." + HomeID("srv-1") + ".direct-a.example"
	cl := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: name},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", itoa(s.Status().Internal)))
		},
	}}
	req, _ := http.NewRequest(http.MethodGet, "https://"+name+"/api/health", nil)
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("X-Real-Ip", "6.6.6.6")
	req.Header.Set(ViaHeader, "relay")
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.Header.Get(ServerIDHeader) != "srv-1" {
		t.Fatalf("health named %q", resp.Header.Get(ServerIDHeader))
	}
	seenMu.Lock()
	// the peer's address only: what the client claimed is dropped
	if seen.Get(ViaHeader) != "direct" || seen.Get("X-Forwarded-For") != "127.0.0.1" || seen.Get("X-Real-Ip") != "" ||
		seen.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("forwarded headers %v", seen)
	}
	seenMu.Unlock()

	// Check now reaches the cloud, and its result shows
	if err := s.CheckNow(ctx); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the check", func() bool { c := s.Status().Check; return c != nil && c.Result == "ok" })

	// one CSR: no renewal is due
	agent.mu.Lock()
	csrs := agent.csrs
	agent.mu.Unlock()
	if csrs != 1 {
		t.Fatalf("%d CSRs", csrs)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// The LAN names are the cloud's (direct.EncodeIP): dashes for dots or colons; zoned or
// malformed addresses are skipped.
func TestLANURLs(t *testing.T) {
	got := lanURLs([]string{"192.168.1.20:8444", "[fd00::7]:8444", "[fe80::1%en0]:8444", "nope", "[::ffff:10.0.0.7]:9"}, ".hid.direct-a.example")
	want := []string{
		"https://192-168-1-20.hid.direct-a.example:8444",
		"https://fd00--7.hid.direct-a.example:8444",
		"https://10-0-0-7.hid.direct-a.example:9",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

// A chain for another key, or another name, is refused and the last good one stays.
func TestAChainForAnotherKeyIsRefused(t *testing.T) {
	agent, _ := newFakeAgent(t)
	f := &Files{Dir: t.TempDir()}
	other := &Files{Dir: t.TempDir()}
	n := Name{Domain: "direct-a.example", HomeID: HomeID("srv-1")}
	csr, err := other.CSR(n)
	if err != nil {
		t.Fatal(err)
	}
	agent.serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"csr":`+jsonBytes(csr)+`}`)))
	if err := f.SetChain([]byte(agent.st.Chain), n); err == nil {
		t.Fatal("kept a chain for another key")
	}
	if f.Cert() != nil {
		t.Fatal("a certificate after a refusal")
	}
}

func jsonBytes(b []byte) string { j, _ := json.Marshal(b); return string(j) }

// Renewal is due at two thirds of the certificate's life, or once it names another home.
func TestRenewDue(t *testing.T) {
	n := Name{Domain: "direct-a.example", HomeID: "h1"}
	now := time.Now()
	leaf := &x509.Certificate{DNSNames: []string{"*.h1.direct-a.example"}, NotBefore: now, NotAfter: now.Add(90 * 24 * time.Hour)}
	c := &tls.Certificate{Leaf: leaf}
	if RenewDue(c, n, now.Add(59*24*time.Hour)) {
		t.Fatal("due at day 59")
	}
	if !RenewDue(c, n, now.Add(60*24*time.Hour)) {
		t.Fatal("not due at day 60")
	}
	if !RenewDue(c, Name{Domain: "direct-b.example", HomeID: "h1"}, now) {
		t.Fatal("not due for another name")
	}
	if !RenewDue(nil, n, now) {
		t.Fatal("not due without one")
	}
}

// T04 and T05 through the loop: the port is mapped at start and the cloud told on; behind
// CGNAT it is told cgnat; stopping removes the mapping and tells the cloud off.
func TestThePortmapLoop(t *testing.T) {
	for _, tc := range []struct {
		external, want string
	}{{"81.2.3.4", StateOn}, {"100.64.7.8", StateCGNAT}} {
		t.Run(tc.want, func(t *testing.T) {
			agent, agentSrv := newFakeAgent(t)
			gw := newFakePMP(t, tc.external, false)
			s := &Service{
				Agent:    &Agent{Addr: strings.TrimPrefix(agentSrv.URL, "http://")},
				Listen:   ":8444",
				Settings: func(context.Context) (Settings, error) { return Settings{Enabled: true, MapPort: true}, nil },
				Mappers:  []Mapper{&NATPMP{Gateway: gw.addr()}},
				LAN:      func(port int) []string { return []string{"192.168.1.20:" + itoa(port)} },
				Poll:     20 * time.Millisecond,
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { s.RunPortmap(ctx); close(done) }()
			waitUntil(t, "the report", func() bool { return s.Status().State == tc.want })
			if m, _ := gw.state(); m[8444] != 8444 {
				t.Fatalf("mapped %v", m)
			}
			cancel()
			<-done
			if _, removed := gw.state(); len(removed) != 1 {
				t.Fatalf("removed %v", removed)
			}
			reqs := agent.requests()
			first, last := reqs[0].Endpoints, reqs[len(reqs)-1].Endpoints
			if first == nil || first.State != tc.want || first.Port != 8444 || first.External != tc.external ||
				len(first.LAN) != 1 || first.LAN[0] != "192.168.1.20:8444" {
				t.Fatalf("first report %+v", first)
			}
			if last == nil || last.State != StateOff {
				t.Fatalf("last report %+v", last)
			}
		})
	}
}

// With mapping off and a port forwarded by hand, the cloud is told that port; with no port, off.
func TestAManualPort(t *testing.T) {
	agent, agentSrv := newFakeAgent(t)
	var mu sync.Mutex
	set := Settings{Enabled: true, MapPort: false, ManualPort: 32443}
	s := &Service{
		Agent:    &Agent{Addr: strings.TrimPrefix(agentSrv.URL, "http://")},
		Listen:   ":8444",
		Settings: func(context.Context) (Settings, error) { mu.Lock(); defer mu.Unlock(); return set, nil },
		Mappers:  []Mapper{},
		LAN:      func(int) []string { return nil },
		Poll:     10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunPortmap(ctx); close(done) }()
	waitUntil(t, "manual", func() bool { st := s.Status(); return st.State == StateOn && st.Port == 32443 && st.By == "manual" })
	mu.Lock()
	set.Enabled = false
	mu.Unlock()
	waitUntil(t, "off", func() bool { return s.Status().State == StateOff })
	cancel()
	<-done
	found := false
	for _, r := range agent.requests() {
		if r.Endpoints != nil && r.Endpoints.Port == 32443 && r.Endpoints.State == StateOn {
			found = true
		}
	}
	if !found {
		t.Fatal("the manual port was never reported")
	}
}

// T13: cloud.direct needs no licence: a Free home runs it.
func TestDirectNeedsNoLicence(t *testing.T) {
	c, ok := components.Default().Get("cloud.direct")
	if !ok {
		t.Fatal("cloud.direct is not in the catalog")
	}
	if c.Licence != "" || !strings.Contains(strings.Join(c.Requires, ","), "cloud.link") {
		t.Fatalf("licence %q requires %v", c.Licence, c.Requires)
	}
}
