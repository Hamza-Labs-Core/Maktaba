package config

import "github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"

// SupervisorSection is the [supervisor] table: how `serve` restarts and
// probes its child processes (Story 22.9). Durations are Go duration
// strings ("1s", "5m"); the supervisor package parses and validates
// them so a typo is reported with its key name at startup.
type SupervisorSection struct {
	// Restart is the policy: "always" (default), "on-failure" (a clean
	// exit 0 is left stopped) or "never" (any child exit stops the whole
	// stack, for installs where systemd/launchd/compose restarts it).
	Restart string `toml:"restart"`
	// BackoffInitial / BackoffMax bound the exponential restart delay.
	BackoffInitial string `toml:"backoff_initial"`
	BackoffMax     string `toml:"backoff_max"`
	// HealthyAfter is the uptime after which a child's backoff resets.
	HealthyAfter string `toml:"healthy_after"`
	// MaxRestarts restarts inside MaxRestartsWindow mark a child failed;
	// a failed child is retried every FailedRetry.
	MaxRestarts       int    `toml:"max_restarts"`
	MaxRestartsWindow string `toml:"max_restarts_window"`
	FailedRetry       string `toml:"failed_retry"`
	// Liveness probes: every ProbeInterval, each with ProbeTimeout;
	// ProbeFailures failures in a row kill and restart the child. No
	// failure counts before StartupGrace unless a probe has succeeded.
	ProbeInterval string `toml:"probe_interval"`
	ProbeTimeout  string `toml:"probe_timeout"`
	ProbeFailures int    `toml:"probe_failures"`
	StartupGrace  string `toml:"startup_grace"`
	// AdminAddr is the loopback address the supervisor serves its state
	// on (GET /processes); the api proxies it. Empty disables it.
	AdminAddr string `toml:"admin_addr"`
}

// DefaultSupervisor is the [supervisor] section a fresh install gets.
func DefaultSupervisor() SupervisorSection {
	return SupervisorSection{
		Restart:           "always",
		BackoffInitial:    "1s",
		BackoffMax:        "60s",
		HealthyAfter:      "5m",
		MaxRestarts:       5,
		MaxRestartsWindow: "10m",
		FailedRetry:       "5m",
		ProbeInterval:     "10s",
		ProbeTimeout:      "3s",
		ProbeFailures:     3,
		StartupGrace:      "60s",
		AdminAddr:         ports.Supervisor.Addr(),
	}
}
