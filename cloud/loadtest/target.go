// Package loadtest measures what one relay carries (Epic 50 story 50.14): simulated homes and
// players against a relay, ramped until a stop rule trips; the regression check CI runs on every
// relay change; and the sizing table the staging runs fill. It never targets a user-facing name.
package loadtest

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// CheckTarget refuses any target but the rehearsal's containers, this machine and the staging
// names under maktaba.dev (story 50.14: never maktaba.tv, never a user-facing name).
func CheckTarget(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("target %q: want http(s)://host", raw)
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	switch {
	case h == "maktaba.tv" || strings.HasSuffix(h, ".maktaba.tv"):
		return fmt.Errorf("target %s: never a maktaba.tv name (users are there); staging is under maktaba.dev", h)
	case h == "maktaba.dev" || strings.HasSuffix(h, ".maktaba.dev"):
		return nil
	case h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".test"):
		return nil
	case !strings.Contains(h, "."):
		return nil // a container's name on the rehearsal's network
	}
	if ip := net.ParseIP(h); ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
		return nil
	}
	return fmt.Errorf("target %s: only the rehearsal's containers, this machine or *.maktaba.dev", h)
}
