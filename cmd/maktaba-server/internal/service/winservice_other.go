//go:build !windows

package service

func installWindowsService(Spec) error { return ErrUnsupported }
