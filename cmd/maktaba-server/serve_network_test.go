package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// Epic 59 story 59.08 (T04): on a Docker bridge mDNS is off with its own reason, the
// roles get discovery off (the API's mDNS, streaming's SSDP), and the log says how apps
// still connect, in both languages; on the host's network nothing changes.
func TestBridgeTurnsDiscoveryOff(t *testing.T) {
	t.Setenv(envDiscoveryMDNS, "")
	t.Setenv(envDiscoverySSDP, "")
	var log bytes.Buffer
	bridgeOff(hostinfo.Network{Mode: hostinfo.NetworkHost}, "http://192.168.1.50:8088", &log)
	if log.Len() != 0 || os.Getenv(envDiscoverySSDP) != "" {
		t.Fatalf("host network: %q", log.String())
	}
	bridgeOff(hostinfo.Network{Mode: hostinfo.NetworkBridge, Interface: "eth0", Driver: "veth"}, "http://192.168.1.50:8088", &log)
	if os.Getenv(envDiscoveryMDNS) != "0" || os.Getenv(envDiscoverySSDP) != "0" {
		t.Fatal("discovery not off for the roles")
	}
	out := log.String()
	for _, want := range []string{"network: bridge (eth0, veth)", "apps can't find Maktaba by themselves",
		"(http://192.168.1.50:8088)", "شبكة جسرية"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}

	old := onBridge
	t.Cleanup(func() { onBridge = old })
	onBridge = func() bool { return true }
	t.Setenv(envDiscoveryMDNS, "")
	if l := mdnsLoop(config.Defaults(), true, func(context.Context) {}); l.Forced != supervisor.ReasonBridgeNetwork || l.ForcedOn {
		t.Fatalf("bridge: %+v", l)
	}
}

// T03: a port another program holds (QNAP's UI on 8080) moves, the move names the holder
// in both languages, and a moved web port prints the new address in both.
func TestPortMoveNamesTheHolder(t *testing.T) {
	withProbe(t, takenProbe("8080", "8088"))
	oldHolder := portHolder
	t.Cleanup(func() { portHolder = oldHolder })
	portHolder = func(port int) string {
		if port == 8080 {
			return "thttpd"
		}
		return ""
	}
	cfg := config.Defaults()
	var out bytes.Buffer
	moved, err := reconcilePorts(&cfg, "", config.PortOptions{}, &out, false)
	if err != nil {
		t.Fatal(err)
	}
	var api, web config.PortChange
	for _, ch := range moved {
		switch ch.Key {
		case "listen":
			api = ch
		case "web_listen":
			web = ch
		}
	}
	if !api.Taken || api.OldPort != 8080 || api.NewPort == 8080 || api.Holder != "thttpd" {
		t.Fatalf("the cluster port: %+v", api)
	}
	if !web.Taken || web.Holder != "" {
		t.Fatalf("the web port: %+v", web)
	}
	log := out.String()
	for _, want := range []string{
		"Port 8080 is used by thttpd on this machine, so Maktaba uses port " + itoaPort(api.NewPort) + " instead.",
		"المنفذ 8080 يستخدمه thttpd",
		"Port 8088 is used by another program on this machine",
		"Open Maktaba at " + cfg.PublicWebURL() + ".",
		"افتح مكتبة على " + cfg.PublicWebURL(),
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}

	// Settings → This machine shows the moves
	moves := portMovesOf(moved)
	runningPortMoves.Store(&moves)
	t.Cleanup(func() { runningPortMoves.Store(nil) })
	b, _ := json.Marshal(machineDocOf(machineTier{Tier: hostinfo.TierStandard}, cfg, false))
	var doc struct {
		WebURL    string     `json:"web_url"`
		PortMoves []portMove `json:"port_moves"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.WebURL != cfg.PublicWebURL() || len(doc.PortMoves) != 2 {
		t.Fatalf("machine doc: %s", b)
	}
	for _, pm := range doc.PortMoves {
		if pm.Key == "listen" && (pm.Old != 8080 || pm.Holder != "thttpd" || !pm.Taken) {
			t.Fatalf("machine doc's move: %+v", pm)
		}
	}
}

// Setup's check says the bridge and each taken port.
func TestSetupCheckNetwork(t *testing.T) {
	oldMoves, oldHolder := setupPortMoves, portHolder
	t.Cleanup(func() { setupPortMoves, portHolder = oldMoves, oldHolder })
	setupPortMoves = func(config.Config) []config.PortChange {
		return []config.PortChange{{Key: "listen", OldPort: 8080, NewPort: 8782, Taken: true},
			{Key: "web_listen", OldPort: 8088, NewPort: 8780}}
	}
	portHolder = func(int) string { return "thttpd" }
	cfg := config.Defaults()
	cfg.Server.WebPublicURL = "http://192.168.1.50:8088"
	var res checkResult
	checkNetwork(cfg, hostinfo.Machine{Network: hostinfo.Network{Mode: hostinfo.NetworkBridge}}, &res)
	if len(res.Warnings) != 2 {
		t.Fatalf("warnings: %+v", res.Warnings)
	}
	b, _ := json.Marshal(res.Warnings)
	s := string(b)
	for _, want := range []string{`"code":"network_bridge"`, `"url":"http://192.168.1.50:8088"`, `"code":"port_moved"`, `"holder":"thttpd"`, `"new":8782`} {
		if !strings.Contains(s, want) {
			t.Errorf("warnings lack %s: %s", want, s)
		}
	}
	res = checkResult{}
	portHolder = func(int) string { return "" }
	checkNetwork(cfg, hostinfo.Machine{}, &res)
	if len(res.Warnings) != 1 || res.Warnings[0].Code != "port_moved_other" {
		t.Fatalf("host network, holder unseen: %+v", res.Warnings)
	}
}

func itoaPort(p int) string { b, _ := json.Marshal(p); return string(b) }
