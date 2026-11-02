//go:build linux

package discovery

import (
	"context"
	"encoding/xml"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// On a Linux with systemd-resolved (the CI runner among them) the probe answers at once,
// and the RegisterService this binding calls has the arguments it sends:
// id, name template, type, port, priority, weight, TXT records (sssqqqaa{say}) → path (o).
func TestResolvedOnThisMachine(t *testing.T) {
	if _, err := os.Stat("/run/dbus/system_bus_socket"); err != nil {
		t.Skip("no system bus here")
	}
	start := time.Now()
	st := probeResolved()
	t.Logf("resolved: %+v (%v); responder %+v", st, time.Since(start), systemResponder())
	if time.Since(start) > 4*time.Second {
		t.Fatalf("the probe took %v", time.Since(start))
	}
	if st.canRegister && !st.responds {
		t.Fatalf("may register with a resolved that does not respond: %+v", st)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		t.Fatalf("system bus: %v", err)
	}
	defer func() { _ = conn.Close() }()
	var owner string
	if conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, resolveName).Store(&owner) != nil {
		t.Skip("systemd-resolved does not run here")
	}
	var raw string
	if err := conn.Object(resolveName, resolvePath).CallWithContext(ctx, "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&raw); err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var node struct {
		Interfaces []struct {
			Name    string `xml:"name,attr"`
			Methods []struct {
				Name string `xml:"name,attr"`
				Args []struct {
					Type      string `xml:"type,attr"`
					Direction string `xml:"direction,attr"`
				} `xml:"arg"`
			} `xml:"method"`
		} `xml:"interface"`
	}
	if err := xml.Unmarshal([]byte(raw), &node); err != nil {
		t.Fatalf("introspection: %v", err)
	}
	want := map[string]string{"RegisterService": "sssqqqaa{say}>o", "UnregisterService": "o>", "GetLink": "i>o"}
	for _, ifc := range node.Interfaces {
		if ifc.Name != resolveManager {
			continue
		}
		for _, m := range ifc.Methods {
			w, ok := want[m.Name]
			if !ok {
				continue
			}
			var in, out strings.Builder
			for _, a := range m.Args {
				if a.Direction == "out" {
					out.WriteString(a.Type)
				} else {
					in.WriteString(a.Type)
				}
			}
			if got := in.String() + ">" + out.String(); got != w {
				t.Errorf("%s(%s), the binding sends %s", m.Name, got, w)
			}
			delete(want, m.Name)
		}
	}
	if _, ok := want["GetLink"]; ok {
		t.Error("resolved has no GetLink")
	}
	if _, ok := want["RegisterService"]; ok && st.canRegister {
		t.Error("the probe saw a RegisterService introspection does not have")
	}
}
