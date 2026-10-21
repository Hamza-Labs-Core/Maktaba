//go:build !linux

package main

// quietPlatformCheck: nothing to quiet off Linux (TestMain).
func quietPlatformCheck() {}
