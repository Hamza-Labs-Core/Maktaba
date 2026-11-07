//go:build cloudrehearsal

package rehearsal

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Epic 50 story 50.31's rehearsal (T06–T08): the origin is reached only through Cloudflare.
// tools/rehearsal-cloud/compose-origin.yml on top of the pair: the nodes run production's
// Caddyfile and UFW, and while leading the api on the loopback with a stand-in connector
// (socat, cloudflared's place); a stand-in edge writes CF-Connecting-IP as Cloudflare does; an
// attacker on the Floating IP's network tries everything else.

const (
	edgeAddr     = "10.61.0.40" // the stand-in edge on the Floating IP's network (the test "Cloudflare ranges")
	attackerAddr = "10.61.7.66" // another /24 from the edge and the gateway (10.61.0.1)
	forgedIP     = "6.6.6.6"
)

// Every port something on a node listens on: setup.sh's UFW list, the api (8090, 8091), the
// connector (7844), Caddy's admin (2019), cloudflared's metrics (20241), the relay's own.
var originScanPorts = []int{22, 53, 80, 443, 2019, 2379, 2380, 5432, 7844, 8008, 8081, 8090, 8091,
	8443, 8444, 8453, 8454, 9100, 9187, 9443, 9610, 20241}

func upOrigin(t *testing.T, scenario, mode string) *ha {
	t.Helper()
	writeOriginPKI(t, filepath.Join(repoRoot(t), "tools/rehearsal-cloud/.work/origin"))
	return upHAWith(t, scenario, &haExtra{
		files:      []string{"compose-origin.yml"},
		env:        []string{"ORIGIN_MODE=" + mode},
		noEdgeWait: true,
	})
}

// writeOriginPKI writes the Authenticated Origin Pulls test CA (aop-ca.pem) and the client
// certificate it signed (client.crt, client.key), the stand-in for Cloudflare's.
func writeOriginPKI(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "rehearsal AOP CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "rehearsal edge"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]*pem.Block{
		"aop-ca.pem": {Type: "CERTIFICATE", Bytes: caDER},
		"client.crt": {Type: "CERTIFICATE", Bytes: leafDER},
		"client.key": {Type: "PRIVATE KEY", Bytes: keyDER},
	} {
		// the containers read them as root or caddy; a test key, never a real one
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// in runs a bash script in one of the project's containers.
func (h *ha) in(container, script string) (string, error) {
	out, err := exec.Command("docker", "exec", h.project+"-"+container+"-1", "bash", "-c", script).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// status is the HTTP status curl got in container ("000": no HTTP answer, a refused TLS
// handshake included).
func (h *ha) status(container, args string) string {
	out, _ := h.in(container, "curl -s -o /dev/null -w '%{http_code}' --max-time 5 "+args)
	if len(out) >= 3 {
		return out[len(out)-3:]
	}
	return "000"
}

// fip is the Floating IP's current address (the relay.test alias fakehcloud moves).
func (h *ha) fip(t *testing.T) string {
	t.Helper()
	out, err := h.in("attacker", "getent hosts relay.test | awk '{print $1}'")
	if err != nil || out == "" {
		t.Fatalf("relay.test from the attacker: %v %q", err, out)
	}
	return out
}

// openPorts are the ports of originScanPorts that accept a TCP connection at ip, from the attacker.
func (h *ha) openPorts(t *testing.T, ip string) []int {
	t.Helper()
	ports := make([]string, len(originScanPorts))
	for i, p := range originScanPorts {
		ports[i] = strconv.Itoa(p)
	}
	out, _ := h.in("attacker", fmt.Sprintf(
		`for p in %s; do timeout 2 bash -c "</dev/tcp/%s/$p" 2>/dev/null && echo "$p"; done`,
		strings.Join(ports, " "), ip))
	var open []int
	for _, f := range strings.Fields(out) {
		if p, err := strconv.Atoi(f); err == nil {
			open = append(open, p)
		}
	}
	return open
}

// connector reports whether node runs the stand-in connector.
func (h *ha) connector(node string) bool {
	// [T]: the pattern must not match the bash -c running it (its command line holds the text)
	_, err := h.in(node, `pgrep -f "[T]CP-LISTEN:7844" >/dev/null`)
	return err == nil
}

// logLine waits for the line of node's /var/log/<file> that holds needle.
func (h *ha) logLine(t *testing.T, node, file, needle string) string {
	t.Helper()
	var line string
	waitFor(t, 30*time.Second, needle+" in "+node+"'s "+file, func() bool {
		out, _ := h.in(node, "grep -F -- "+strconv.Quote(needle)+" /var/log/"+file+" | tail -n1")
		line = out
		return out != ""
	})
	return line
}

// replica reports whether node's Patroni calls it a running replica.
func (h *ha) replica(node string) bool {
	resp, err := (&http.Client{Timeout: time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/replica", h.api[node]))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == 200
}

func (h *ha) edgeReady() bool { return h.status("attacker", "http://"+edgeAddr+"/readyz") == "200" }

// checkForgedThroughEdge: a CF-Connecting-IP the attacker writes is replaced by the edge, and
// the api (on the loopback, trusting only its local peer) takes the edge's value. The edge
// echoes the {remote_host} it writes as X-Edge-Saw. The shared log redaction zeroes an IPv4
// address's last octet (shared/log/go/redact_rules.json), so the api's line holds the
// attacker's address as 10.61.7.0: not the forged one (6.6.6.0), not its TCP peer (127.0.0.1,
// which the redaction keeps: the header ignored), and not the edge's or the gateway's
// 10.61.0.0 (a hop that rewrote the header with its own peer).
func (h *ha) checkForgedThroughEdge(t *testing.T, leader string) {
	t.Helper()
	probe := fmt.Sprintf("/origin-probe/edge-%d", time.Now().UnixNano())
	headers, _ := h.in("attacker", fmt.Sprintf("curl -s -D- -o /dev/null --max-time 5 -H 'CF-Connecting-IP: %s' http://%s%s",
		forgedIP, edgeAddr, probe))
	if !strings.Contains(headers, "X-Edge-Saw: "+attackerAddr) {
		t.Errorf("the edge saw the attacker as something other than %s:\n%s", attackerAddr, headers)
	}
	line := h.logLine(t, leader, "api.log", probe)
	var entry struct {
		RemoteIP string `json:"remote_ip"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("the api's access log line is not JSON: %v: %s", err, line)
	}
	if want := redactedV4(attackerAddr); entry.RemoteIP != want {
		t.Errorf("the api's access log for a forged CF-Connecting-IP through the edge: remote_ip %q, want %q (%s redacted), never %q (forged) or 127.0.0.1 (header ignored): %s",
			entry.RemoteIP, want, attackerAddr, redactedV4(forgedIP), line)
	}
}

// redactedV4 is an IPv4 address as the shared log redaction writes it: the last octet zeroed.
func redactedV4(ip string) string { return ip[:strings.LastIndex(ip, ".")] + ".0" }

// T06, T07: origin-only-via-edge.
func originOnlyViaEdge(t *testing.T, _ *cluster) Result {
	h := upOrigin(t, "origin-only-via-edge", "tunnel")
	lead := h.leader()
	waitFor(t, 2*time.Minute, "the api through the edge and the leader's connector", h.edgeReady)
	if !h.connector(lead) || h.connector(other(lead)) {
		t.Errorf("the connector runs on the leader only: %s %v, %s %v", lead, h.connector(lead), other(lead), h.connector(other(lead)))
	}

	// T06: from the internet the Floating IP answers 80 and 443 only (53 too where the dns role runs)
	ip := h.fip(t)
	open := h.openPorts(t, ip)
	for _, p := range open {
		if p != 80 && p != 443 && p != 53 {
			t.Errorf("port %d of the Floating IP answers from the internet (open: %v)", p, open)
		}
	}
	if !slices.Contains(open, 80) || !slices.Contains(open, 443) {
		t.Errorf("80 and 443 must answer (open: %v)", open)
	}
	// 443 has relay names only: no certificate, no site for the api's name
	waitFor(t, time.Minute, "the relay over TLS at the Floating IP", func() bool {
		return h.status("attacker", "-k https://relay.test/readyz") == "200"
	})
	if got := h.status("attacker", "-k --resolve api.maktaba.tv:443:"+ip+" https://api.maktaba.tv/readyz"); got != "000" {
		t.Errorf("443 answered SNI api.maktaba.tv with %s; want the handshake refused", got)
	}
	if got := h.status("attacker", "-H 'Host: api.maktaba.tv' http://"+ip+"/readyz"); got != "421" {
		t.Errorf("plain HTTP for api.maktaba.tv at the Floating IP: %s, want 421", got)
	}
	// the forged header: through the edge it is replaced; straight to the relay it is dropped
	h.checkForgedThroughEdge(t, lead)
	probe := fmt.Sprintf("/origin-probe/relay-%d", time.Now().UnixNano())
	h.status("attacker", fmt.Sprintf("-k -H 'CF-Connecting-IP: %s' https://relay.test%s", forgedIP, probe))
	if line := h.logLine(t, lead, "relay.log", probe); strings.Contains(line, redactedV4(forgedIP)) {
		t.Errorf("the relay logged the forged CF-Connecting-IP (%s, redacted): %s", redactedV4(forgedIP), line)
	}

	// T07: power off the leader; the connector moves with the lead, the api is back through the edge
	start := time.Now()
	h.mustHA("kill", lead)
	var promoted, back time.Duration
	waitFor(t, 90*time.Second, "the other node to lead", func() bool {
		if h.leader() == other(lead) {
			promoted = time.Since(start)
			return true
		}
		return false
	})
	waitFor(t, 3*time.Minute, "the api back through the edge", func() bool {
		if h.edgeReady() {
			back = time.Since(start)
			return true
		}
		return false
	})
	if !h.connector(other(lead)) {
		t.Errorf("the new leader %s runs no connector", other(lead))
	}
	if back > 3*time.Minute {
		t.Errorf("the api was back through the edge after %s; the RTO is 3 min", back)
	}
	h.checkForgedThroughEdge(t, other(lead))
	// the old leader comes back as a replica, without a connector
	h.mustHA("start", lead)
	waitFor(t, 3*time.Minute, lead+" back as a replica", func() bool { return h.replica(lead) })
	time.Sleep(5 * time.Second)
	if h.connector(lead) {
		t.Errorf("the old leader %s runs a connector as a replica", lead)
	}
	if l := h.leader(); l != other(lead) {
		t.Errorf("the leader after %s came back: %q, want %s", lead, l, other(lead))
	}
	t.Logf("open ports %v; %s killed: %s led after %s, the api back through the edge after %s",
		open, lead, other(lead), promoted, back)
	return Result{DowntimeMS: back.Milliseconds(), BudgetMS: (3 * time.Minute).Milliseconds(),
		Notes: []string{fmt.Sprintf("Floating IP answers %v; promoted after %s; api back through the edge after %s", open, promoted, back)}}
}

// T08: origin-aop, the fallback origin. Spec 50.31 T08 lists 403 without the certificate;
// Caddy's client_auth require_and_verify refuses the TLS handshake before any HTTP, which is
// stricter, so the check is "no HTTP answer".
func originAOP(t *testing.T, _ *cluster) Result {
	h := upOrigin(t, "origin-aop", "aop")
	lead := h.leader()
	waitFor(t, 2*time.Minute, "the api through the edge's AOP connection", h.edgeReady)
	ip := h.fip(t)
	resolve := "--resolve api.maktaba.tv:443:" + ip + " https://api.maktaba.tv/readyz"
	cert := "--cert /origin/client.crt --key /origin/client.key "

	if got := h.status("edge", "-k "+cert+resolve); got != "200" {
		t.Errorf("with the certificate, from the Cloudflare ranges: %s, want 200", got)
	}
	if got := h.status("edge", "-k "+resolve); got != "000" {
		t.Errorf("without the certificate, from the Cloudflare ranges: %s, want the handshake refused", got)
	}
	if got := h.status("attacker", "-k "+cert+resolve); got != "403" {
		t.Errorf("with the certificate, from outside the Cloudflare ranges (%s): %s, want 403", attackerAddr, got)
	}
	if got := h.status("attacker", "-k "+resolve); got != "000" {
		t.Errorf("without the certificate, from outside the Cloudflare ranges: %s, want the handshake refused", got)
	}
	// behind Caddy on the loopback the api believes the edge's CF-Connecting-IP, never the client's
	h.checkForgedThroughEdge(t, lead)
	return Result{Notes: []string{"200 with the certificate from the ranges; handshake refused without it; 403 from outside the ranges"}}
}
