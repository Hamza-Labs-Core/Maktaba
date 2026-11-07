// Package direct is Maktaba Cloud's side of direct remote access (Epic 50 story 50.15): each
// home's own HTTPS names under one of several direct-name domains (never maktaba.tv), the
// authoritative DNS responder that computes their answers, which domain and CA a home's
// certificate comes from, issuing it from the home's CSR by DNS-01, and the check from outside
// that the home is reachable. Direct remote is free on every plan: nothing here looks at a plan.
package direct

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"
)

// HomeID is a home's stable label in its direct names: the first 32 hex characters of
// SHA-256("direct-v1" ‖ server_id). It never changes (unlike the slug) and says nothing about
// the home (certificate transparency logs show it).
func HomeID(serverID string) string {
	sum := sha256.Sum256([]byte("direct-v1" + serverID))
	return hex.EncodeToString(sum[:])[:32]
}

// IsHomeID says whether s has a home ID's shape.
func IsHomeID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

// EncodeIP is an address as one DNS label: 81.2.3.4 is "81-2-3-4", 2001:db8::1 is "2001-db8--1".
func EncodeIP(ip netip.Addr) string {
	return strings.ReplaceAll(ip.Unmap().String(), map[bool]string{true: ".", false: ":"}[ip.Unmap().Is4()], "-")
}

// DecodeLabel reads a label EncodeIP wrote; anything else is refused.
func DecodeLabel(label string) (netip.Addr, error) {
	label = strings.ToLower(label)
	if label == "" || len(label) > 63 {
		return netip.Addr{}, fmt.Errorf("not an address label: %q", label)
	}
	for _, c := range label {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && c != '-' {
			return netip.Addr{}, fmt.Errorf("not an address label: %q", label)
		}
	}
	var text string
	if strings.Count(label, "-") == 3 && !strings.Contains(label, "--") && !strings.ContainsAny(label, "abcdef") {
		text = strings.ReplaceAll(label, "-", ".")
	} else {
		text = strings.ReplaceAll(label, "-", ":")
	}
	ip, err := netip.ParseAddr(text)
	if err != nil || ip.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("not an address label: %q", label)
	}
	// the canonical form only: one address, one name
	if EncodeIP(ip) != label {
		return netip.Addr{}, fmt.Errorf("not the canonical label for %s: %q", ip, label)
	}
	return ip, nil
}

// Name is the home's name for ip: <encoded ip>.<home id>.<domain>.
func Name(ip netip.Addr, homeID, domain string) string {
	return EncodeIP(ip) + "." + homeID + "." + domain
}

// Wildcard is the one name the home's certificate covers: *.<home id>.<domain>.
func Wildcard(homeID, domain string) string { return "*." + homeID + "." + domain }

// Parsed is a query name read against the configured domains.
type Parsed struct {
	Domain string
	HomeID string
	// Addr is set for <ip>.<home id>.<domain>; ACME is set for _acme-challenge.<home id>.<domain>.
	Addr netip.Addr
	ACME bool
	// Apex is the domain itself (its NS, SOA, CAA); NS is ns1. or ns2.<domain>; HomeOnly is
	// <home id>.<domain>, which exists with no records (a CA's CAA lookup climbs through it).
	Apex     bool
	NS       int
	HomeOnly bool
}

// Parse reads qname (with or without the final dot) against domains. ok is false for a name
// under none of them: the responder refuses it (it is no open resolver).
func Parse(qname string, domains []string) (p Parsed, inZone bool, err error) {
	name := strings.ToLower(strings.TrimSuffix(qname, "."))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSuffix(d, "."))
		if name != d && !strings.HasSuffix(name, "."+d) {
			continue
		}
		p.Domain = d
		if name == d {
			p.Apex = true
			return p, true, nil
		}
		rest := strings.Split(strings.TrimSuffix(name, "."+d), ".")
		switch {
		case len(rest) == 1 && (rest[0] == "ns1" || rest[0] == "ns2"):
			p.NS = int(rest[0][2] - '0')
			return p, true, nil
		case len(rest) == 1 && IsHomeID(rest[0]):
			p.HomeID, p.HomeOnly = rest[0], true
			return p, true, nil
		case len(rest) == 2 && rest[0] == "_acme-challenge" && IsHomeID(rest[1]):
			p.HomeID, p.ACME = rest[1], true
			return p, true, nil
		case len(rest) == 2 && IsHomeID(rest[1]):
			ip, err := DecodeLabel(rest[0])
			if err != nil {
				return p, true, err
			}
			p.HomeID, p.Addr = rest[1], ip
			return p, true, nil
		}
		return p, true, fmt.Errorf("no such name in %s", d)
	}
	return Parsed{}, false, nil
}
