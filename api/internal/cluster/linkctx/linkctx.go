// Package linkctx carries the cluster link's word on a request (Epic 35 story 35.16):
// which machine sent it over its mutual-TLS link, and the home's link mode. It has no
// dependencies, so the channel's hub, the delegation checks and the link itself all read
// it without importing each other.
package linkctx

import (
	"context"
	"sync/atomic"
)

// The link modes of design §18.7.
const (
	ModeOff     = "off"
	ModePrefer  = "prefer"
	ModeRequire = "require"
)

type nodeKey struct{}

// WithNode marks ctx as a request a machine sent over its link (its certificate named
// nodeID and the home checked it).
func WithNode(ctx context.Context, nodeID string) context.Context {
	return context.WithValue(ctx, nodeKey{}, nodeID)
}

// Node is the machine a request came from over its link ("" and false: not over the link).
func Node(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(nodeKey{}).(string)
	return id, ok && id != ""
}

var mode atomic.Value

// SetMode records the home's link mode (the link's settings loop sets it).
func SetMode(m string) { mode.Store(m) }

// Mode is the home's link mode ("prefer" until it is known: the default since the link).
func Mode() string {
	if m, ok := mode.Load().(string); ok && m != "" {
		return m
	}
	return ModePrefer
}

// Require reports whether the legacy paths are refused (mode require).
func Require() bool { return Mode() == ModeRequire }
