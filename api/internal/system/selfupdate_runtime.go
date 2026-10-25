package system

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// The Linux app's runtime copy (Epic 39 stories 39.3, 39.9): the launcher sets
// MAKTABA_RUNTIME_OWNER for the server and every role. With owner headless the
// machine updates itself: `maktaba-server update` verifies the release, installs
// its payload as a new runtime directory and switches the service to it. The
// running binaries are never replaced.
const (
	EnvRuntimeOwner      = "MAKTABA_RUNTIME_OWNER"
	RuntimeOwnerHeadless = "headless"
)

// EnvContainer names the container the server runs in, set by its image (the Docker
// install, deploy/docker/server: "docker"). Its runtime is on the image's volume and the
// server updates it by itself, as a headless install's: the orchestrator does (Epic 35),
// never `docker compose pull`.
const EnvContainer = "MAKTABA_CONTAINER"

// runtimeUpdate starts the update of a runtime copy and answers 202: it runs
// on its own, since moving `current` stops the service this API runs in.
func (s *SelfUpdater) runtimeUpdate(w http.ResponseWriter, r *http.Request, self, version string) {
	server := filepath.Join(filepath.Dir(self), "maktaba-server") // bin/ of the same runtime directory
	unit, out, err := s.runRuntime(server, version)
	if err != nil {
		httperror.Write(w, r, httperror.Internal(fmt.Sprintf("start the runtime update: %v: %s", err, strings.TrimSpace(string(out)))))
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{
		"updated":    false,
		"updating":   true,
		"via":        "runtime",
		"version":    version,
		"restarting": true,
		"unit":       unit,
		"log":        "journalctl --user -u " + unit,
	})
}

// runtimeUpdateEnv is what the update takes from this process's environment: a
// transient unit starts with the user manager's, and the service pins the
// XDG directories in its own unit.
var runtimeUpdateEnv = []string{"MAKTABA_UPDATE_REPO", "MAKTABA_GITHUB_TOKEN",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"}

// runtimeUpdateArgs is the systemd-run command line of an update to version.
func runtimeUpdateArgs(unit, server, version string, lookup func(string) (string, bool)) []string {
	args := []string{"--user", "--collect", "--quiet", "--unit=" + unit}
	for _, k := range runtimeUpdateEnv {
		if _, ok := lookup(k); ok {
			args = append(args, "--setenv="+k) // the value from systemd-run's environment, not argv
		}
	}
	return append(args, "--", server, "update", "--version", version)
}

// startRuntimeUpdate runs `maktaba-server update --version <v>` as a transient
// systemd user unit, outside the service's cgroup (stopping the service would
// stop a child of this process with it). Its output is in the user journal.
func startRuntimeUpdate(server, version string) (string, []byte, error) {
	unit := "maktaba-update-" + strconv.FormatInt(time.Now().Unix(), 10)
	out, err := exec.Command("systemd-run", runtimeUpdateArgs(unit, server, version, os.LookupEnv)...).CombinedOutput()
	return unit, out, err
}
