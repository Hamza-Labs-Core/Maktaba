package pgbundle

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func clusterBundle(t *testing.T, lan, tls bool) Bundle {
	t.Helper()
	dir := t.TempDir()
	b := Bundle{Data: filepath.Join(dir, "postgres"), Keys: filepath.Join(dir, "keys"), Port: DefaultPort, LAN: lan, TLS: tls}
	if err := os.MkdirAll(b.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHBAConf(t *testing.T) {
	loop := clusterBundle(t, false, true).hbaConf()
	if strings.Contains(loop, "maktaba_worker") || !strings.Contains(loop, "127.0.0.1/32") {
		t.Fatalf("loopback only:\n%s", loop)
	}
	lan := clusterBundle(t, true, true).hbaConf()
	for _, want := range []string{
		"hostssl maktaba      +maktaba_worker",
		"hostssl maktaba      +maktaba_replicas",
		"hostssl replication  +maktaba_replicas",
	} {
		if !strings.Contains(lan, want) {
			t.Errorf("LAN pg_hba lacks %q:\n%s", want, lan)
		}
	}
	for _, line := range strings.Split(lan, "\n") {
		if strings.HasPrefix(line, "host ") && !strings.Contains(line, "127.0.0.1/32") && !strings.Contains(line, "::1/128") {
			t.Errorf("a plain connection from off this machine: %q", line)
		}
	}
	plain := clusterBundle(t, true, false).hbaConf()
	if strings.Contains(plain, "hostssl") || !strings.Contains(plain, "host    replication  +maktaba_replicas") {
		t.Fatalf("TLS off:\n%s", plain)
	}
}

func TestEnsureTLS(t *testing.T) {
	b := clusterBundle(t, true, true)
	now := time.Now()
	if b.TLSReady() {
		t.Fatal("ready before")
	}
	if err := b.ensureTLS(now); err != nil {
		t.Fatal(err)
	}
	if !b.TLSReady() {
		t.Fatal("no server certificate")
	}
	ca, err := readCert(b.CACert())
	if err != nil || !ca.IsCA {
		t.Fatalf("CA: %v %v", ca, err)
	}
	srv, err := readCert(filepath.Join(b.Data, serverCertFile))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := srv.Verify(x509.VerifyOptions{Roots: pool, DNSName: "localhost"}); err != nil {
		t.Fatalf("the server certificate does not verify with the CA: %v", err)
	}
	if runtime.GOOS != "windows" {
		for _, f := range []string{filepath.Join(b.Data, serverKeyFile), filepath.Join(b.Keys, caKeyFile)} {
			if info, err := os.Stat(f); err != nil || info.Mode().Perm() != 0o600 {
				t.Errorf("%s: %v %v, want 0600", f, info.Mode().Perm(), err)
			}
		}
	}

	// a later start keeps both; near its end the server's is issued again, same CA
	if err := b.ensureTLS(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if again, _ := readCert(filepath.Join(b.Data, serverCertFile)); again.SerialNumber.Cmp(srv.SerialNumber) != 0 {
		t.Fatal("a valid server certificate was replaced")
	}
	if err := b.ensureTLS(srv.NotAfter.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	renewed, _ := readCert(filepath.Join(b.Data, serverCertFile))
	if renewed.SerialNumber.Cmp(srv.SerialNumber) == 0 {
		t.Fatal("a server certificate near its end was kept")
	}
	if ca2, _ := readCert(b.CACert()); ca2.SerialNumber.Cmp(ca.SerialNumber) != 0 {
		t.Fatal("the CA changed: every machine would stop trusting the home")
	}
	if renewed.CheckSignatureFrom(ca) != nil {
		t.Fatal("the renewed certificate is not the CA's")
	}
}

func TestServerArgsOnTheLAN(t *testing.T) {
	b := clusterBundle(t, true, true)
	args := strings.Join(b.ServerArgs(), " ")
	if !strings.Contains(args, "listen_addresses=*") || strings.Contains(args, "ssl=on") {
		t.Fatalf("before the certificate: %s", args)
	}
	if err := b.ensureTLS(time.Now()); err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(b.ServerArgs(), " "); !strings.Contains(args, "ssl=on") {
		t.Fatalf("with the certificate: %s", args)
	}
	if args := strings.Join(b.serverArgs(false), " "); !strings.Contains(args, "listen_addresses=127.0.0.1") {
		t.Fatalf("Prepare's own server must stay on loopback: %s", args)
	}
	loop := clusterBundle(t, false, true)
	if args := strings.Join(loop.ServerArgs(), " "); !strings.Contains(args, "listen_addresses=127.0.0.1") {
		t.Fatalf("loopback: %s", args)
	}
}
