//go:build linux

package mdns

// The host's Avahi over a mounted system bus (Epic 59 story 59.08). A container has no
// avahi-daemon socket of the host's; given the host's /var/run/dbus/system_bus_socket it
// can still publish through the host's avahi-daemon, which avahi-publish-service reaches
// over D-Bus. Asked only in a container (announce.go).
// The same file is in api/internal/discovery and cmd/maktaba-server/internal/mdns (two
// modules); tools/ci/mdns_copies.py checks they stay the same.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

const avahiBusName = "org.freedesktop.Avahi"

// avahiOnBus is whether the system bus answers and Avahi owns its name there (running:
// GetNameOwner never starts it). No socket, no bus or no Avahi is false.
func avahiOnBus() bool { return avahiBusCheck() == nil }

// avahiBusCheck is avahiOnBus's answer with why not, for the log (Responder).
func avahiBusCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("no connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var owner string
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, avahiBusName).Store(&owner); err != nil {
		return fmt.Errorf("avahi not on the bus: %w", err)
	}
	if owner == "" {
		return errors.New("avahi not on the bus")
	}
	return nil
}
