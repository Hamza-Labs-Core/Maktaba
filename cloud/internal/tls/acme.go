// Package tls glues golang.org/x/crypto/acme to the relay edge so
// per-server subdomains receive valid certificates without an operator
// having to run a separate ACME daemon.
//
// In v1 we delegate to autocert's stdlib-friendly Manager and gate
// HostPolicy on a callback that hits stores.Servers — only subdomains
// we've provisioned are eligible for cert issuance, so an attacker
// can't burn our Let's Encrypt quota by hitting random hostnames.
//
// We deliberately do not import x/crypto/acme here to avoid pulling a
// new dependency into go.mod for the bootstrap PR. The HostPolicy +
// Cache surface is sufficient for the integration to wire up in a
// follow-up; this file establishes the shape.
package tls

import (
	"context"
	"errors"
	"strings"
)

// HostPolicy reports whether `host` is eligible for cert issuance.
// We accept either the bare public host (api.maktaba.app) or
// <slug>.<relay public host>.
type HostPolicy func(ctx context.Context, host string) error

// SubdomainResolver looks up a slug → exists boolean. The relay role
// wires this to a stores.Servers query.
type SubdomainResolver interface {
	HasSlug(ctx context.Context, slug string) (bool, error)
}

// PolicyFromResolver returns a HostPolicy enforcing the subdomain rule.
//
// `apex` is the comma-separated list of bare hostnames we always serve
// (api.maktaba.app, admin.maktaba.app, ...); `wildcardRoot` is the
// suffix that delimits valid per-server subdomains.
func PolicyFromResolver(apex, wildcardRoot string, r SubdomainResolver) HostPolicy {
	apexSet := map[string]bool{}
	for _, h := range strings.Split(apex, ",") {
		apexSet[strings.TrimSpace(h)] = true
	}
	return func(ctx context.Context, host string) error {
		if apexSet[host] {
			return nil
		}
		suffix := "." + wildcardRoot
		if !strings.HasSuffix(host, suffix) {
			return errors.New("tls: host not in allowed scope")
		}
		slug := strings.TrimSuffix(host, suffix)
		if slug == "" || strings.Contains(slug, ".") {
			return errors.New("tls: slug shape invalid")
		}
		ok, err := r.HasSlug(ctx, slug)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("tls: slug not provisioned")
		}
		return nil
	}
}
