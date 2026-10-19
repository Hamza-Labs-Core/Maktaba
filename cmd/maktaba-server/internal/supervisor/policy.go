package supervisor

import (
	"fmt"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// RestartMode is the [supervisor] restart policy.
type RestartMode string

const (
	// RestartAlways restarts a child whenever it exits.
	RestartAlways RestartMode = "always"
	// RestartOnFailure restarts a child unless it exited with code 0.
	RestartOnFailure RestartMode = "on-failure"
	// RestartNever stops the whole stack when any child exits (the
	// behaviour before Story 22.9, for an external service manager).
	RestartNever RestartMode = "never"
)

// Policy is the parsed [supervisor] section.
type Policy struct {
	Restart        RestartMode
	BackoffInitial time.Duration
	BackoffMax     time.Duration
	HealthyAfter   time.Duration
	MaxRestarts    int
	Window         time.Duration
	FailedRetry    time.Duration
	ProbeInterval  time.Duration
	ProbeTimeout   time.Duration
	ProbeFailures  int
	StartupGrace   time.Duration
	AdminAddr      string
}

// DefaultPolicy is the policy a fresh install runs with.
func DefaultPolicy() Policy {
	p, err := PolicyFromConfig(config.DefaultSupervisor())
	if err != nil {
		panic(err) // the defaults are constants; a failure is a programming error
	}
	return p
}

// PolicyFromConfig parses and validates a [supervisor] section. Empty
// keys take the default; a bad value is reported with its key name.
func PolicyFromConfig(s config.SupervisorSection) (Policy, error) {
	def := config.DefaultSupervisor()
	pick := func(v, d string) string {
		if strings.TrimSpace(v) == "" {
			return d
		}
		return strings.TrimSpace(v)
	}
	var p Policy
	var errs []string
	dur := func(key, v, d string, minimum time.Duration) time.Duration {
		raw := pick(v, d)
		out, err := time.ParseDuration(raw)
		if err != nil {
			errs = append(errs, fmt.Sprintf("supervisor.%s: %q is not a duration", key, raw))
			return 0
		}
		if out < minimum {
			errs = append(errs, fmt.Sprintf("supervisor.%s: %s is below the minimum %s", key, out, minimum))
		}
		return out
	}
	switch RestartMode(strings.ToLower(pick(s.Restart, def.Restart))) {
	case RestartAlways:
		p.Restart = RestartAlways
	case RestartOnFailure:
		p.Restart = RestartOnFailure
	case RestartNever:
		p.Restart = RestartNever
	default:
		errs = append(errs, fmt.Sprintf("supervisor.restart: %q is not always, on-failure or never", s.Restart))
	}
	p.BackoffInitial = dur("backoff_initial", s.BackoffInitial, def.BackoffInitial, time.Millisecond)
	p.BackoffMax = dur("backoff_max", s.BackoffMax, def.BackoffMax, time.Millisecond)
	p.HealthyAfter = dur("healthy_after", s.HealthyAfter, def.HealthyAfter, 0)
	p.Window = dur("max_restarts_window", s.MaxRestartsWindow, def.MaxRestartsWindow, time.Millisecond)
	p.FailedRetry = dur("failed_retry", s.FailedRetry, def.FailedRetry, time.Millisecond)
	p.ProbeInterval = dur("probe_interval", s.ProbeInterval, def.ProbeInterval, time.Millisecond)
	p.ProbeTimeout = dur("probe_timeout", s.ProbeTimeout, def.ProbeTimeout, time.Millisecond)
	p.StartupGrace = dur("startup_grace", s.StartupGrace, def.StartupGrace, 0)
	p.MaxRestarts = s.MaxRestarts
	if p.MaxRestarts == 0 {
		p.MaxRestarts = def.MaxRestarts
	}
	if p.MaxRestarts < 1 {
		errs = append(errs, fmt.Sprintf("supervisor.max_restarts: %d must be at least 1", s.MaxRestarts))
	}
	p.ProbeFailures = s.ProbeFailures
	if p.ProbeFailures == 0 {
		p.ProbeFailures = def.ProbeFailures
	}
	if p.ProbeFailures < 1 {
		errs = append(errs, fmt.Sprintf("supervisor.probe_failures: %d must be at least 1", s.ProbeFailures))
	}
	if p.BackoffMax < p.BackoffInitial {
		errs = append(errs, "supervisor.backoff_max must not be below backoff_initial")
	}
	// admin_addr keeps an explicit empty string ("disabled") only via
	// "off"; an unset key gets the default.
	switch strings.ToLower(strings.TrimSpace(s.AdminAddr)) {
	case "":
		p.AdminAddr = def.AdminAddr
	case "off", "none", "disabled":
		p.AdminAddr = ""
	default:
		p.AdminAddr = strings.TrimSpace(s.AdminAddr)
	}
	if len(errs) > 0 {
		return Policy{}, fmt.Errorf("server.toml: %s", strings.Join(errs, "; "))
	}
	return p, nil
}

// nextBackoff doubles d, capped at ceiling.
func nextBackoff(d, ceiling time.Duration) time.Duration {
	d *= 2
	if d > ceiling || d <= 0 {
		return ceiling
	}
	return d
}
