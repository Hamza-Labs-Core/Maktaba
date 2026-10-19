//go:build !windows

package main

import "errors"

// isService: systemd and launchd run the launcher as a plain process.
func isService() bool { return false }

func runService(Config) error { return errors.New("not a Windows service") }
