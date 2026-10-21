package appruntime

import (
	"context"
	"errors"
)

// ErrLocked: another install or activation of this runtime kept its lock.
var ErrLocked = errors.New("locked")

// The runtime copy is the Linux app's (the Windows app stops its server before
// its installer runs, Epic 38).
func (r Runtime) lock(context.Context) (func(), error) {
	return nil, errors.New("the runtime copy is the Linux app's")
}

func freeBytes(string) (uint64, error) { return 0, errors.ErrUnsupported }

func syncAll() {}
