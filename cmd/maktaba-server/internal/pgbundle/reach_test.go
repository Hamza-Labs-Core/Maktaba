package pgbundle

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Until a machine may connect (one approved, or joins open), the server listens on this
// machine only and pg_hba.conf lets no machine in; then it listens beyond it, and lets
// the machines in from their networks and, by default, each one's own address.
func TestTheMachinesReachOnlyWhatTheClusterAsks(t *testing.T) {
	dir := t.TempDir()
	b := Bundle{Data: filepath.Join(dir, "postgres"), Keys: filepath.Join(dir, "keys"), Port: DefaultPort, LAN: true, TLS: true}
	if err := os.MkdirAll(b.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	listen := func() string {
		for _, a := range b.ServerArgs() {
			if v, ok := strings.CutPrefix(a, "listen_addresses="); ok {
				return v
			}
		}
		return ""
	}
	if got := listen(); got != "127.0.0.1" {
		t.Errorf("no machine yet: listens on %q", got)
	}
	if hba := b.hbaConf(); strings.Contains(hba, "maktaba_worker") {
		t.Errorf("no machine yet, but pg_hba.conf lets them in:\n%s", hba)
	}

	r := Reach{Open: true, Addrs: []string{"100.64.1.2", "192.168.1.7", "127.0.0.1", "bogus", "fd7a::5", "100.64.1.2"}}
	if err := b.KeepReach(r); err != nil {
		t.Fatal(err)
	}
	if got := b.LastReach(); !got.Open || len(got.Addrs) != len(r.Addrs) {
		t.Fatalf("kept %+v, read back %+v", r, got)
	}
	if got := listen(); got != "*" {
		t.Errorf("a machine may connect: listens on %q", got)
	}
	hba := b.hbaConf()
	for _, want := range []string{
		"hostssl maktaba      +maktaba_worker  samenet",
		"hostssl maktaba      +maktaba_replicas samenet",
		"hostssl maktaba      +maktaba_worker  100.64.1.2/32",
		"hostssl maktaba      +maktaba_replicas 192.168.1.7/32",
		"hostssl maktaba      +maktaba_worker  fd7a::5/128",
	} {
		if !strings.Contains(hba, want) {
			t.Errorf("pg_hba.conf lacks %q:\n%s", want, hba)
		}
	}
	if strings.Count(hba, "100.64.1.2/32") != 2 || strings.Contains(hba, "bogus") ||
		strings.Contains(hba, "maktaba_worker  127.0.0.1") {
		t.Errorf("one line per group and machine address, never this machine's own:\n%s", hba)
	}

	// networks the admin named: those only
	b.MachinesFrom = "10.8.0.0/16"
	if hba := b.hbaConf(); strings.Contains(hba, "100.64.1.2") || !strings.Contains(hba, "10.8.0.0/16") {
		t.Errorf("machines_from named networks, yet:\n%s", hba)
	}
	b.MachinesFrom = ""

	// [postgres] listen = "loopback": never beyond this machine
	b.LAN = false
	if got := listen(); got != "127.0.0.1" {
		t.Errorf("listen = loopback: listens on %q", got)
	}
	if hba := b.hbaConf(); strings.Contains(hba, "maktaba_worker") {
		t.Errorf("listen = loopback, but pg_hba.conf lets machines in:\n%s", hba)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b.WatchReach(ctx, time.Millisecond, func() error { t.Error("listen = loopback, yet started again"); return nil }, io.Discard)
}

// Once the home requires the cluster link (story 35.16 AC12), the role of each machine
// that presents its client certificate from the cluster CA (Reach.CertRoles; the server is
// started with the CA's file) needs it too, on its own line before the group's; every
// other machine's role keeps its password alone (one whose processes present none, an
// older release, is never cut off its database), and so do the copies' replication roles.
// Frozen lets no machine role in; TLS off asks no certificate.
func TestRequiredLinkAsksForClientCertificates(t *testing.T) {
	const (
		presents = "maktaba_node_0b6f3c1e2a4d4e8f9c715d2a8b3e6f10"
		another  = "maktaba_node_1c7a4d2f3b5e4f90ad826e3b9c4f7a21"
	)
	dir := t.TempDir()
	b := Bundle{Data: filepath.Join(dir, "postgres"), Keys: filepath.Join(dir, "keys"), Port: DefaultPort, LAN: true, TLS: true}
	for _, d := range []string{b.Data, b.Keys} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	keep := func(r Reach) {
		t.Helper()
		if err := b.KeepReach(r); err != nil {
			t.Fatal(err)
		}
	}
	keep(Reach{Open: true, Require: true, Addrs: []string{"192.168.1.7"}, CertRoles: []string{presents}})
	if hba := b.hbaConf(); strings.Contains(hba, "clientcert") {
		t.Fatalf("no cluster CA yet, but certificates asked:\n%s", hba)
	}
	if err := os.WriteFile(filepath.Join(b.Keys, ClusterCAFile), []byte("CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	hba := b.hbaConf()
	for _, want := range []string{
		"hostssl maktaba      " + presents + " samenet         scram-sha-256 clientcert=verify-full",
		"hostssl maktaba      " + presents + " 192.168.1.7/32  scram-sha-256 clientcert=verify-full",
		"hostssl maktaba      +maktaba_worker  samenet         scram-sha-256\n",
		"hostssl maktaba      +maktaba_worker  192.168.1.7/32  scram-sha-256\n",
	} {
		if !strings.Contains(hba, want) {
			t.Fatalf("required, %s presenting its certificate: lacks %q:\n%s", presents, want, hba)
		}
	}
	if strings.Count(hba, "clientcert") != 2 || strings.Contains(hba, another) ||
		strings.Contains(hba, "+"+ReplicaGroup+" samenet         scram-sha-256 clientcert") {
		t.Fatalf("only the machine that presents its certificate needs it:\n%s", hba)
	}
	// the first line that matches decides: the machine's own lines come before the group's
	if strings.LastIndex(hba, presents) > strings.Index(hba, "+"+WorkerGroup) {
		t.Fatalf("a machine's own line after the group's (the group's would decide):\n%s", hba)
	}

	// required, and no machine says it presents one: none is asked for one
	keep(Reach{Open: true, Require: true})
	if hba := b.hbaConf(); strings.Contains(hba, "clientcert") || !strings.Contains(hba, "+"+WorkerGroup) {
		t.Fatalf("required, but no machine presents a certificate:\n%s", hba)
	}
	// a name that is not a machine's role never goes in
	keep(Reach{Open: true, Require: true, CertRoles: []string{"postgres", "all", "+maktaba_worker"}})
	if hba := b.hbaConf(); strings.Contains(hba, "clientcert") {
		t.Fatalf("a name that is no machine's role:\n%s", hba)
	}
	// frozen: no machine role at all, the copies' only
	keep(Reach{Open: true, Require: true, CertRoles: []string{presents}})
	if err := b.SetFrozen(true); err != nil {
		t.Fatal(err)
	}
	if hba := b.hbaConf(); strings.Contains(hba, presents) || strings.Contains(hba, "+"+WorkerGroup) ||
		!strings.Contains(hba, "+"+ReplicaGroup) {
		t.Fatalf("frozen:\n%s", hba)
	}
	if err := b.SetFrozen(false); err != nil {
		t.Fatal(err)
	}
	// TLS off ([postgres] tls = "off"): no certificate can be asked
	b.TLS = false
	if hba := b.hbaConf(); strings.Contains(hba, "clientcert") || strings.Contains(hba, presents) {
		t.Fatalf("TLS off, but certificates asked:\n%s", hba)
	}
	b.TLS = true
	// prefer: none
	keep(Reach{Open: true, CertRoles: []string{presents}})
	if hba := b.hbaConf(); strings.Contains(hba, "clientcert") {
		t.Fatalf("prefer, but certificates asked:\n%s", hba)
	}
}

// The server takes the cluster CA (verifying every certificate a client offers) only
// while the link is required: in prefer a machine of a home whose CA it does not hold
// (just after a switch) still logs in with its password.
func TestClusterCAOnlyWhileTheLinkIsRequired(t *testing.T) {
	dir := t.TempDir()
	b := Bundle{Data: filepath.Join(dir, "postgres"), Keys: filepath.Join(dir, "keys"), Port: DefaultPort, LAN: true, TLS: true}
	for _, d := range []string{b.Data, b.Keys} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{serverCertFile, serverKeyFile, ClusterCAFile} {
		if err := os.WriteFile(filepath.Join(b.Keys, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hasCA := func() bool {
		for _, a := range b.ServerArgs() {
			if strings.HasPrefix(a, "ssl_ca_file=") {
				return true
			}
		}
		return false
	}
	if err := b.KeepReach(Reach{Open: true}); err != nil {
		t.Fatal(err)
	}
	if hasCA() {
		t.Fatal("prefer: the server verifies client certificates")
	}
	if err := b.KeepReach(Reach{Open: true, Require: true}); err != nil {
		t.Fatal(err)
	}
	if !hasCA() {
		t.Fatal("require: the server does not take the cluster CA")
	}
}
