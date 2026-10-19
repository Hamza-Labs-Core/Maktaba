package config

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// fakeProbe: taken ports are busy, ours ports answer as a Maktaba.
func fakeProbe(taken []int, ours ...int) PortProbe {
	busy := map[int]bool{}
	for _, p := range taken {
		busy[p] = true
	}
	mine := map[int]bool{}
	for _, p := range ours {
		mine[p] = true
		busy[p] = true
	}
	return PortProbe{
		Free: func(addr string) bool {
			_, ps, _ := net.SplitHostPort(addr)
			n, _ := strconv.Atoi(ps)
			return !busy[n]
		},
		Ours: func(port int) bool { return mine[port] },
	}
}

func TestReconcilePortsKeepsFreePorts(t *testing.T) {
	c := Defaults()
	moved, err := c.ReconcilePorts(fakeProbe(nil), PortOptions{})
	if err != nil || len(moved) != 0 {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	if c.Server.WebListen != "0.0.0.0:8088" || c.Server.Listen != "0.0.0.0:8080" || c.Server.StreamAddr != ":8081" {
		t.Fatalf("changed: %+v", c.Server)
	}
}

// Splunk on 8088 and MiniTool ShadowMaker on 8080 (the Windows PC this was found on).
func TestReconcilePortsMovesTakenPorts(t *testing.T) {
	c := Defaults()
	moved, err := c.ReconcilePorts(fakeProbe([]int{8088, 8080}), PortOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 {
		t.Fatalf("moved %v", moved)
	}
	if c.Server.WebListen != "0.0.0.0:8780" {
		t.Errorf("web_listen = %q, want 0.0.0.0:8780", c.Server.WebListen)
	}
	if c.Server.Listen != "0.0.0.0:8782" {
		t.Errorf("listen = %q, want 0.0.0.0:8782", c.Server.Listen)
	}
	if c.Server.StreamAddr != ":8081" {
		t.Errorf("stream_addr = %q, want :8081 (free)", c.Server.StreamAddr)
	}
}

func TestReconcilePortsSkipsTakenFallbacks(t *testing.T) {
	c := Defaults()
	// 8780/8781 are another local install; 8782 is free
	moved, err := c.ReconcilePorts(fakeProbe([]int{8088, 8780, 8781}), PortOptions{})
	if err != nil || len(moved) != 1 {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	if c.Server.WebListen != "0.0.0.0:8782" {
		t.Errorf("web_listen = %q, want 0.0.0.0:8782", c.Server.WebListen)
	}
}

func TestReconcilePortsNeverPicksTheSamePortTwice(t *testing.T) {
	c := Defaults()
	_, err := c.ReconcilePorts(fakeProbe([]int{8088, 8080, 8081}), PortOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, a := range []string{c.Server.WebListen, c.Server.Listen, c.Server.StreamAddr} {
		_, p, _ := net.SplitHostPort(a)
		if seen[p] {
			t.Fatalf("port %s used twice: %+v", p, c.Server)
		}
		seen[p] = true
	}
}

func TestReconcilePortsRepointsPublicURLs(t *testing.T) {
	c := Defaults()
	c.Server.PublicURL = "http://192.168.1.20:8081"
	c.Server.WebPublicURL = "192.168.1.20:8088"
	if _, err := c.ReconcilePorts(fakeProbe([]int{8088, 8081}), PortOptions{}); err != nil {
		t.Fatal(err)
	}
	if c.Server.PublicURL != "http://192.168.1.20:8781" {
		t.Errorf("public_url = %q", c.Server.PublicURL)
	}
	if c.Server.WebPublicURL != "192.168.1.20:8780" {
		t.Errorf("web_public_url = %q", c.Server.WebPublicURL)
	}
}

func TestReconcilePortsPrefersWebPort(t *testing.T) {
	c := Defaults()
	moved, err := c.ReconcilePorts(fakeProbe(nil), PortOptions{PreferWeb: WindowsNodeWebPort})
	if err != nil || len(moved) != 1 || c.Server.WebListen != "0.0.0.0:8780" {
		t.Fatalf("moved=%v err=%v web=%q", moved, err, c.Server.WebListen)
	}
	// an operator's own web port is never moved for the preference
	c = Defaults()
	c.Server.WebListen = "0.0.0.0:9000"
	moved, _ = c.ReconcilePorts(fakeProbe(nil), PortOptions{PreferWeb: WindowsNodeWebPort})
	if len(moved) != 0 {
		t.Fatalf("moved an explicit port: %v", moved)
	}
	// preference taken: 8088 kept when it is free
	c = Defaults()
	moved, _ = c.ReconcilePorts(fakeProbe([]int{8780}), PortOptions{PreferWeb: WindowsNodeWebPort})
	if len(moved) != 0 || c.Server.WebListen != "0.0.0.0:8088" {
		t.Fatalf("moved=%v web=%q", moved, c.Server.WebListen)
	}
}

func TestReconcilePortsOurs(t *testing.T) {
	c := Defaults()
	if moved, err := c.ReconcilePorts(fakeProbe(nil, 8080), PortOptions{KeepOurs: true}); err != nil || len(moved) != 0 {
		t.Fatalf("KeepOurs: moved=%v err=%v", moved, err)
	}
	_, err := c.ReconcilePorts(fakeProbe(nil, 8080), PortOptions{})
	var ours ErrPortOurs
	if !errors.As(err, &ours) || ours.Key != "listen" {
		t.Fatalf("err = %v, want ErrPortOurs for listen", err)
	}
}

func TestReconcilePortsKeys(t *testing.T) {
	c := Defaults()
	moved, err := c.ReconcilePorts(fakeProbe([]int{8088, 8080}), PortOptions{Keys: []string{"listen"}})
	if err != nil || len(moved) != 1 || moved[0].Key != "listen" || c.Server.WebListen != "0.0.0.0:8088" {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
}

func TestReconcilePortsNoFreeFallback(t *testing.T) {
	taken := []int{8088}
	for p := FallbackPortLow; p <= FallbackPortHigh; p++ {
		taken = append(taken, p)
	}
	c := Defaults()
	if _, err := c.ReconcilePorts(fakeProbe(taken), PortOptions{}); err == nil {
		t.Fatal("want an error when the whole range is taken")
	}
}

// CanBind against a real listener on loopback.
func TestCanBindRealListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer func() { _ = ln.Close() }()
	if CanBind(ln.Addr().String()) {
		t.Fatalf("CanBind(%s) = true while it is listened on", ln.Addr())
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if CanBind(net.JoinHostPort("0.0.0.0", port)) {
		t.Fatalf("wildcard bind of a port held on 127.0.0.1 reported free")
	}
	_ = ln.Close()
	if !CanBind(ln.Addr().String()) {
		t.Fatalf("CanBind(%s) = false after close", ln.Addr())
	}
}

func TestMaktabaAnswersNothingListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if MaktabaAnswers(port) {
		t.Fatal("nothing listening answered as Maktaba")
	}
}

func TestMaktabaAnswersHealthz(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte(`{"status":"ok","service":"api"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	if !MaktabaAnswers(srv.Listener.Addr().(*net.TCPAddr).Port) {
		t.Fatal("a /healthz {status: ok} is a Maktaba")
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>Splunk</html>`))
	}))
	defer other.Close()
	if MaktabaAnswers(other.Listener.Addr().(*net.TCPAddr).Port) {
		t.Fatal("an HTML page is not a Maktaba")
	}
}
