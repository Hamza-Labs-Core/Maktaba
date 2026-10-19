//go:build windows

package service

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc/mgr"
)

// installWindowsService registers (or updates) the optional Windows service:
// the launcher with --root <install> serve, automatic start, SCM recovery
// restart after 5 s with the failure count reset after a day (design §2.3).
func installWindowsService(s Spec) error {
	return InstallWindowsService(WindowsName, s.Launcher, []string{"--root", s.Root, "serve"})
}

// InstallWindowsService creates or reconfigures a service named name.
func InstallWindowsService(name, exe string, args []string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (run as administrator): %w", err)
	}
	defer func() { _ = m.Disconnect() }()
	cfg := mgr.Config{
		DisplayName: "Maktaba",
		Description: "Maktaba: runs maktaba-launcher, which starts the current release of maktaba-server.",
		StartType:   mgr.StartAutomatic,
	}
	s, err := m.OpenService(name)
	if err == nil {
		defer s.Close()
		cur, err := s.Config()
		if err != nil {
			return err
		}
		cur.DisplayName, cur.Description, cur.StartType = cfg.DisplayName, cfg.Description, cfg.StartType
		cur.BinaryPathName = quoteCmd(exe, args)
		if err := s.UpdateConfig(cur); err != nil {
			return err
		}
	} else {
		s, err = m.CreateService(name, exe, cfg, args...)
		if err != nil {
			return fmt.Errorf("create service %s: %w", name, err)
		}
		defer s.Close()
	}
	return s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
	}, uint32((24 * time.Hour).Seconds()))
}

// RemoveWindowsService deletes a service (tests, uninstall).
func RemoveWindowsService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Delete()
}

func quoteCmd(exe string, args []string) string {
	out := winArg(exe)
	for _, a := range args {
		out += " " + winArg(a)
	}
	return out
}
