package components

import (
	"os"
	"slices"
	"strings"
)

// Safe mode (Epic 53 story 53.05): one start with the least that keeps the home
// reachable and fixable. Only these components run; every other one, the always-on ones
// included, is off for this boot with reason boot_flag ({"flag": "--safe-mode"}). The
// desired state is never written: the next normal start runs what it says.
var SafeModeComponents = []string{"core.postgres", "core.api", "core.updates", "logs.index"}

// EnvSafeMode is what the supervisor gives every child in safe mode ("1").
const EnvSafeMode = "MAKTABA_COMPONENTS_SAFE_MODE"

// SafeModeFlag is the boot flag's params on a component kept off.
const SafeModeFlag = "--safe-mode"

// SafeModeOn reports whether this process runs in safe mode.
func SafeModeOn() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvSafeMode))) {
	case "1", "on", "true", "yes":
		return true
	}
	return false
}

// SafeModeAllows reports whether component id runs in safe mode.
func SafeModeAllows(id string) bool { return slices.Contains(SafeModeComponents, id) }
