//go:build cloudrehearsal

package rehearsal

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/direct"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/seed"
)

// The router's address on "the internet" (compose-direct.yml): every home behind it is seen
// from there, and its mapped ports open there.
var routerWAN = netip.MustParseAddr("198.51.100.2")

// directPorts are the scenario's host ports: Postgres, the dns role (UDP) and both Pebbles'
// management APIs.
type directPorts struct{ pg, dns, pebble, pebbleZS int }

func portsOfDirect(project string) directPorts {
	p := portsFor(project)
	return directPorts{pg: p[0], dns: p[0] + 400, pebble: p[0] + 410, pebbleZS: p[0] + 411}
}

// upDirect is the direct-remote scenario's compose project (compose-direct.yml), nothing
// started yet: the scenario starts each part in its order. Logs are kept and the project torn
// down as up's are.
func upDirect(t *testing.T, scenario string) *cluster {
	t.Helper()
	c := &cluster{t: t, project: "mr-" + scenario, dir: filepath.Join(repoRoot(t), "tools/rehearsal-cloud"), base: "compose-direct.yml"}
	ports := portsOfDirect(c.project)
	c.extra = []string{
		fmt.Sprintf("DNS_PORT=%d", ports.dns),
		fmt.Sprintf("PEBBLE_MGMT_PORT=%d", ports.pebble),
		fmt.Sprintf("PEBBLE_ZS_MGMT_PORT=%d", ports.pebbleZS),
	}
	if img := os.Getenv("HOME_IMAGE"); img != "" {
		c.extra = append(c.extra, "HOME_IMAGE="+img)
	}
	_, _ = c.compose("down", "-v", "--remove-orphans")
	t.Cleanup(func() {
		logs, _ := c.compose("logs", "--no-color", "--timestamps")
		_ = os.WriteFile(filepath.Join(outDir(t), scenario+".log"), []byte(logs), 0o644)
		if c.db != nil {
			_ = c.db.Close()
		}
		if os.Getenv("REHEARSAL_KEEP") == "" {
			_, _ = c.compose("down", "-v", "--remove-orphans")
		}
	})
	return c
}

// signedByPebbleCA is a server certificate for name, signed by Pebble's test CA (whose key the
// Pebble image ships for this): the second Pebble's TLS, so the cloud trusts both through one
// CA file.
func signedByPebbleCA(t *testing.T, caCertPEM, caKeyPEM []byte, name string) (certPEM, keyPEM []byte) {
	t.Helper()
	ca, err := tls.X509KeyPair(caCertPEM, caKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(ca.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, key.Public(), ca.PrivateKey.(crypto.Signer))
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder})
}

// pebbleRoot is a Pebble's issuing root, from its management API.
func pebbleRoot(port int) ([]byte, error) {
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // Pebble's management API on loopback, its test CA
	}}
	resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/roots/0", port))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("roots: %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// mediaSum is the sha256 of the home's /media?bytes=n.
func mediaSum(n int) string {
	h := sha256.New()
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte('a' + i%26)
	}
	h.Write(buf)
	return hex.EncodeToString(h.Sum(nil))
}

type directCert struct {
	homeID, domain, ca, chain string
	issued                    bool
}

func certOf(db *sql.DB, serverID string) (directCert, bool) {
	var d directCert
	var chain sql.NullString
	var issued sql.NullTime
	if err := db.QueryRow(`SELECT home_id, domain, ca, cert_pem, issued_at FROM direct_certs WHERE server_id = $1`, serverID).
		Scan(&d.homeID, &d.domain, &d.ca, &chain, &issued); err != nil {
		return d, false
	}
	d.chain, d.issued = chain.String, issued.Valid
	return d, d.issued && strings.Contains(d.chain, "BEGIN CERTIFICATE")
}

// C18 direct-remote (story 50.15 T06, T10, T12): two homes behind a router that speaks NAT-PMP
// reach the internet; the cloud's relay, worker and name server and two Pebbles (Let's Encrypt,
// and ZeroSSL as the overflow CA) are on the internet's side.
//
//   - T06: each home maps its port, asks its name, sends a CSR over the relay agent and gets a
//     certificate by DNS-01, which Pebble validated against the cloud's own name server; no
//     private key ever reaches the cloud's database or logs.
//   - T12: the domain's weekly budget is one, so the second home's first certificate comes from
//     the overflow CA. (A renewal staying with its CA is directjobs' integration test: a home
//     asks again only after an hour.)
//   - T10: the outside check passes; the home's name resolves, through the cloud's name server,
//     to the router; an app outside gets the API and a media file over HTTPS straight from the
//     home, verified against the CA, and the relay carries none of it. Both homes are on Free:
//     direct access is on every plan (T13).
func directRemote(t *testing.T, c *cluster) Result {
	ctx := context.Background()
	work := filepath.Join(c.dir, ".work", "direct")
	_ = os.RemoveAll(work)
	for _, d := range []string{"home0", "home1"} {
		if err := os.MkdirAll(filepath.Join(work, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ports := portsOfDirect(c.project)

	// 1. the CAs: Pebble, then the second one with a certificate from Pebble's test CA
	c.must("up", "-d", "pebble")
	c.must("cp", "pebble:/test/certs/pebble.minica.pem", filepath.Join(work, "pebble.minica.pem"))
	c.must("cp", "pebble:/test/certs/pebble.minica.key.pem", filepath.Join(work, "pebble.minica.key.pem"))
	caPEM, _ := os.ReadFile(filepath.Join(work, "pebble.minica.pem"))
	caKey, _ := os.ReadFile(filepath.Join(work, "pebble.minica.key.pem"))
	zsCert, zsKey := signedByPebbleCA(t, caPEM, caKey, "pebble-zs")
	_ = os.WriteFile(filepath.Join(work, "pebble-zs.pem"), zsCert, 0o644)
	_ = os.WriteFile(filepath.Join(work, "pebble-zs.key"), zsKey, 0o644)
	conf, _ := json.Marshal(map[string]any{"pebble": map[string]any{
		"listenAddress": "0.0.0.0:14000", "managementListenAddress": "0.0.0.0:15000",
		"certificate": "/work/pebble-zs.pem", "privateKey": "/work/pebble-zs.key",
		"httpPort": 5002, "tlsPort": 5001, "ocspResponderURL": "", "externalAccountBindingRequired": false,
		"retryAfter": map[string]int{"authz": 3, "order": 5},
		"profiles":   map[string]any{"default": map[string]any{"description": "rehearsal", "validityPeriod": 7776000}},
	}})
	_ = os.WriteFile(filepath.Join(work, "pebble-zs.json"), conf, 0o644)
	c.must("up", "-d", "pebble-zs")
	var leRoot, zsRoot []byte
	waitFor(t, time.Minute, "both Pebbles' roots", func() bool {
		var err1, err2 error
		leRoot, err1 = pebbleRoot(ports.pebble)
		zsRoot, err2 = pebbleRoot(ports.pebbleZS)
		return err1 == nil && err2 == nil
	})
	roots := append(append(append([]byte{}, caPEM...), leRoot...), zsRoot...)
	if err := os.WriteFile(filepath.Join(work, "roots.pem"), roots, 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. the cloud: Postgres and the relay (which migrates), then the name server, the worker and
	//    the router
	c.must("up", "-d", "--wait", "postgres", "relay")
	c.must("up", "-d", "dns", "worker", "router")
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://maktaba:rehearsal@127.0.0.1:%d/maktaba?sslmode=disable", ports.pg))
	if err != nil {
		t.Fatal(err)
	}
	c.db = db

	// 3. two homes on Free, their agents' sealed credentials
	ids, err := seed.Homes(ctx, db, 0, 2, "free")
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	serverIDs := make([]string, 2)
	for i := range 2 {
		_, secret, slug := simhome.Creds(i)
		serverIDs[i] = ids[slug]
		if err := cloudlink.SaveCredentials(filepath.Join(work, fmt.Sprintf("home%d", i), "cloudlink.cred"), key,
			cloudlink.Credentials{ServerID: ids[slug], Secret: secret, Slug: slug}); err != nil {
			t.Fatal(err)
		}
		// the agent runs as root in the container; the file is 0600 on purpose elsewhere
		_ = os.Chmod(filepath.Join(work, fmt.Sprintf("home%d", i), "cloudlink.cred"), 0o644)
	}
	c.extra = append(c.extra, "HOME0_ID="+serverIDs[0], "HOME1_ID="+serverIDs[1], "HOME_CRED_KEY="+hex.EncodeToString(key))

	// 4. the homes one after the other: the first takes the domain's one certificate this week,
	//    the second overflows
	var notes []string
	certs := make([]directCert, 2)
	for i := range 2 {
		start := time.Now()
		c.must("up", "-d", fmt.Sprintf("home%d", i))
		waitFor(t, 4*time.Minute, fmt.Sprintf("home %d's certificate", i), func() bool {
			var ok bool
			certs[i], ok = certOf(db, serverIDs[i])
			return ok
		})
		notes = append(notes, fmt.Sprintf("home %d: certificate from %s for *.%s.%s in %s", i, certs[i].ca,
			certs[i].homeID, certs[i].domain, time.Since(start).Round(time.Second)))
	}
	if certs[0].ca != "letsencrypt" || certs[1].ca != "zerossl" {
		t.Errorf("T12: CAs %q and %q, want letsencrypt then the overflow zerossl", certs[0].ca, certs[1].ca)
	}
	for i, cert := range certs {
		if cert.homeID != direct.HomeID(serverIDs[i]) {
			t.Errorf("home %d: home id %s, want %s", i, cert.homeID, direct.HomeID(serverIDs[i]))
		}
	}

	// T06: no private key reached the cloud
	var keysInDB int
	if err := db.QueryRow(`SELECT count(*) FROM direct_certs WHERE cert_pem LIKE '%PRIVATE KEY%' OR encode(csr, 'escape') LIKE '%PRIVATE KEY%'`).Scan(&keysInDB); err != nil || keysInDB != 0 {
		t.Errorf("T06: a private key in direct_certs (%d, %v)", keysInDB, err)
	}
	if logs, _ := c.compose("logs", "--no-color", "relay", "worker", "dns"); strings.Contains(logs, "PRIVATE KEY") {
		t.Error("T06: a private key in the cloud's logs")
	}

	// 5. the outside check passes for both
	ext := make([]struct{ port int }, 2)
	for i := range 2 {
		waitFor(t, 3*time.Minute, fmt.Sprintf("home %d's outside check", i), func() bool {
			var res sql.NullString
			var port sql.NullInt64
			_ = db.QueryRow(`SELECT c.result, s.direct_port FROM direct_checks c JOIN servers s ON s.id = c.server_id WHERE c.server_id = $1`,
				serverIDs[i]).Scan(&res, &port)
			ext[i].port = int(port.Int64)
			return res.String == direct.CheckOK
		})
	}

	// 6. an app outside: the name through the cloud's name server, then HTTPS straight to the home
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.dns)))
	}}
	const mediaBytes = 5_000_000
	want := mediaSum(mediaBytes)
	for i := range 2 {
		name := direct.Name(routerWAN, certs[i].homeID, certs[i].domain)
		addrs, err := resolver.LookupHost(ctx, name)
		if err != nil || len(addrs) != 1 || addrs[0] != routerWAN.String() {
			t.Errorf("home %d: %s resolves to %v (%v), want %s", i, name, addrs, err, routerWAN)
			continue
		}
		base := fmt.Sprintf("https://%s:%d", name, ext[i].port)
		resolve := fmt.Sprintf("%s:%d:%s", name, ext[i].port, routerWAN)
		head, err := c.compose("exec", "-T", "relay", "curl", "-sS", "-o", "/dev/null", "-D", "-",
			"--cacert", "/work/roots.pem", "--resolve", resolve, base+"/api/health")
		if err != nil || !strings.Contains(strings.ToLower(head), strings.ToLower(direct.ServerIDHeader)+": "+serverIDs[i]) {
			t.Errorf("home %d: /api/health from outside: %v\n%s", i, err, head)
		}
		sum, err := c.compose("exec", "-T", "relay", "sh", "-c", fmt.Sprintf(
			"curl -sSf --cacert /work/roots.pem --resolve %s '%s/media?bytes=%d' | sha256sum", resolve, base, mediaBytes))
		if err != nil || !strings.HasPrefix(strings.TrimSpace(sum), want) {
			t.Errorf("home %d: the media file from outside: %v %q", i, err, sum)
		}
		// the relay carried none of it: every request reached the home by its direct listener
		out, err := c.compose("exec", "-T", fmt.Sprintf("home%d", i), "curl", "-sS", "http://127.0.0.1:8080/rehearsal/vias")
		var vias map[string]int
		if err != nil || json.Unmarshal([]byte(out), &vias) != nil || vias["relay"] != 0 || vias["direct"] < 2 {
			t.Errorf("home %d: requests by way %v (%v): want direct only", i, vias, err)
		}
		notes = append(notes, fmt.Sprintf("home %d: %s -> %s, port %d; ways %v", i, name, routerWAN, ext[i].port, vias))
	}
	return Result{Requests: 6, Notes: notes}
}
