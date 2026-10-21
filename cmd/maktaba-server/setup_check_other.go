//go:build !linux

package main

import "context"

// platformCheck: the Mac and Windows apps have nothing to add to `check`
// beyond what every platform reports.
func platformCheck(context.Context, *checkResult) {}
