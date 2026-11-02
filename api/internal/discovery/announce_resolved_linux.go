//go:build linux

package discovery

// systemd-resolved over the system bus (org.freedesktop.resolve1, announce_resolved.go).
// The same file is in api/internal/discovery and cmd/maktaba-server/internal/mdns (two
// modules); tools/ci/mdns_copies.py checks they stay the same.

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	resolveName     = "org.freedesktop.resolve1"
	resolvePath     = dbus.ObjectPath("/org/freedesktop/resolve1")
	resolveManager  = "org.freedesktop.resolve1.Manager"
	resolveLink     = "org.freedesktop.resolve1.Link"
	resolveRegister = "org.freedesktop.resolve1.register-service" // the polkit action
	polkitName      = "org.freedesktop.PolicyKit1"
	polkitPath      = dbus.ObjectPath("/org/freedesktop/PolicyKit1/Authority")
)

// probeResolved asks the system bus about systemd-resolved; no bus, or no resolved on
// it, is a resolved that does not respond.
func probeResolved() resolvedState {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return resolvedState{}
	}
	defer func() { _ = conn.Close() }()
	return probeResolvedOn(ctx, conn, os.Geteuid() == 0)
}

func probeResolvedOn(ctx context.Context, conn *dbus.Conn, root bool) resolvedState {
	// running, not started for asking (a call to it would start it)
	var owner string
	if conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, resolveName).Store(&owner) != nil {
		return resolvedState{}
	}
	manager := conn.Object(resolveName, resolvePath)
	if !mdnsYes(ctx, manager, resolveManager) || !anyLinkMDNS(ctx, conn, manager) {
		return resolvedState{}
	}
	st := resolvedState{responds: true}
	if v, err := getProperty(ctx, manager, resolveManager+".LLMNRHostname"); err == nil {
		if host, _ := v.Value().(string); strings.Trim(strings.TrimSpace(host), ".") != "" {
			st.host = bareHost(host)
		}
	}
	st.canRegister = hasRegisterService(ctx, manager) && (root || polkitAllows(ctx, conn))
	return st
}

// mdnsYes is whether obj's MulticastDNS is "yes" (not "no", nor "resolve": resolving only,
// answering for nothing).
func mdnsYes(ctx context.Context, obj dbus.BusObject, iface string) bool {
	v, err := getProperty(ctx, obj, iface+".MulticastDNS")
	if err != nil {
		return false
	}
	s, _ := v.Value().(string)
	return s == "yes"
}

// anyLinkMDNS is whether resolved answers mDNS on a link: a link's own MulticastDNS
// ("no" unless networkd or NetworkManager set it) bounds the global one.
func anyLinkMDNS(ctx context.Context, conn *dbus.Conn, manager dbus.BusObject) bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		var path dbus.ObjectPath
		if manager.CallWithContext(ctx, resolveManager+".GetLink", 0, int32(ifi.Index)).Store(&path) != nil {
			continue
		}
		if mdnsYes(ctx, conn.Object(resolveName, path), resolveLink) {
			return true
		}
	}
	return false
}

// hasRegisterService is whether this resolved has RegisterService.
func hasRegisterService(ctx context.Context, manager dbus.BusObject) bool {
	var xml string
	if manager.CallWithContext(ctx, "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml) != nil {
		return false
	}
	return strings.Contains(xml, `name="RegisterService"`)
}

// polkitAllows is whether polkit lets this process register a service with resolved,
// without asking anyone (no polkit: it does not).
func polkitAllows(ctx context.Context, conn *dbus.Conn) bool {
	names := conn.Names()
	if len(names) == 0 {
		return false
	}
	subject := struct {
		Kind    string
		Details map[string]dbus.Variant
	}{"system-bus-name", map[string]dbus.Variant{"name": dbus.MakeVariant(names[0])}}
	var result struct {
		Authorized bool
		Challenge  bool
		Details    map[string]string
	}
	err := conn.Object(polkitName, polkitPath).CallWithContext(ctx,
		"org.freedesktop.PolicyKit1.Authority.CheckAuthorization", 0,
		subject, resolveRegister, map[string]string{}, uint32(0), "").Store(&result)
	return err == nil && result.Authorized
}

func getProperty(ctx context.Context, obj dbus.BusObject, name string) (dbus.Variant, error) {
	i := strings.LastIndexByte(name, '.')
	var v dbus.Variant
	err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, name[:i], name[i+1:]).Store(&v)
	return v, err
}

// dbusResolved is resolvedAPI over its own system-bus connection, which lives as long as
// the announcement does.
type dbusResolved struct {
	conn    *dbus.Conn
	signals chan *dbus.Signal
	starts  chan struct{}
}

func newResolvedAPI() (resolvedAPI, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	r := &dbusResolved{conn: conn, signals: make(chan *dbus.Signal, 8), starts: make(chan struct{}, 1)}
	if err := conn.AddMatchSignal(
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, resolveName),
	); err != nil {
		_ = conn.Close()
		return nil, err
	}
	conn.Signal(r.signals)
	go r.forward()
	return r, nil
}

// forward tells starts each time resolve1 has a new owner (resolved started again).
func (r *dbusResolved) forward() {
	defer close(r.starts)
	for sig := range r.signals {
		if sig.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(sig.Body) != 3 {
			continue
		}
		if owner, _ := sig.Body[2].(string); owner != "" {
			select {
			case r.starts <- struct{}{}:
			default:
			}
		}
	}
}

func (r *dbusResolved) register(svc resolvedService) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var path dbus.ObjectPath
	err := r.conn.Object(resolveName, resolvePath).CallWithContext(ctx, resolveManager+".RegisterService", 0,
		svc.id, svc.name, svc.service, uint16(svc.port), uint16(0), uint16(0), []map[string][]byte{svc.txt},
	).Store(&path)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", errors.New("RegisterService gave no path")
	}
	return string(path), nil
}

func (r *dbusResolved) unregister(path string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.conn.Object(resolveName, resolvePath).CallWithContext(ctx, resolveManager+".UnregisterService", 0,
		dbus.ObjectPath(path)).Err
}

func (r *dbusResolved) restarted() <-chan struct{} { return r.starts }

// close implements resolvedAPI; closing the connection closes signals too, which ends
// forward (and so restarted).
func (r *dbusResolved) close() { _ = r.conn.Close() }
