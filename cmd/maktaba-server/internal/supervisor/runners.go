package supervisor

// maktaba-plugin-runner (Epic 54, story 54.23): Maktaba's own stages as first-party
// plugins, one Python process per runner group with no database login and no secrets.
//
// The shared groups (light, net, service, fp) are instances of the core component
// core.plugin_runners (on_demand: they run while a component with a first-party plugin is
// enabled); ml-* and index come with transcription and semantic search (wave 3). Only the
// groups the release's first-party plugins name are built (shared/plugins FirstPartyGroups):
// a release with none runs no runner.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	sharedplugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
)

// RolePluginRunner is a first-party plugin runner's role.
const RolePluginRunner Role = "plugin-runner"

// PluginRunnersComponent is the catalog's core component of the shared runner groups.
const PluginRunnersComponent = "core.plugin_runners"

// RunnerGroups are core.plugin_runners' instances (the catalog's processes).
var RunnerGroups = []string{"light", "net", "service", "fp"}

// The runner groups of their own components (story 54.30): ml, one per transcription
// worker (transcription's plugin-runner/ml-*), and index (search.semantic's).
const (
	GroupML    = "ml"
	GroupIndex = "index"
)

// The runner's exit codes that are not a crash (maktaba_plugin_runner/__main__.py).
const (
	RunnerSandboxExit   = 70
	RunnerUntrustedExit = 71
	RunnerEnvExit       = 72
)

// Reasons a runner does not run (Epic 51 reason codes, story 54.37 §3).
const (
	ReasonTrustedUnsigned = "plugin_trusted_unsigned"
	ReasonRunnerEnv       = "runner_env"
)

// RunnersHere reports whether this machine runs plugin runners: a Postgres home or node
// (the plugin API's statements run under a Postgres role), not Windows (asyncio opens no
// unix socket there; its stages run in the worker, as before).
func RunnersHere(cfg config.Config) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	u := strings.ToLower(strings.TrimSpace(cfg.Database.URL))
	if !strings.HasPrefix(u, "postgres://") && !strings.HasPrefix(u, "postgresql://") {
		return false
	}
	return len(sharedplugins.FirstPartyGroups()) > 0
}

// RunnerDir is the runners' sockets' folder (MAKTABA_RUNNER_DIR for the worker and each
// runner): <data>/run/runners, or a short folder under /tmp when that is too long for a
// unix socket (as the plugin host's).
func RunnerDir(cfg config.Config) string {
	d := filepath.Join(cfg.Server.DataDir, "run", "runners")
	if len(filepath.Join(d, "runner-service.sock")) > maxSocketPath {
		sum := sha256.Sum256([]byte(filepath.Clean(cfg.Server.DataDir)))
		d = filepath.Join("/tmp", "maktaba-r-"+hex.EncodeToString(sum[:6]))
	}
	return d
}

// runnerEnvDrop is what a runner's environment never has (a test asserts the child's).
func runnerEnvDrop(k string) bool {
	switch k {
	case "DATABASE_URL", "MAKTABA_DATABASE_URL", "MAKTABA_HOME_SECRETS", "MAKTABA_SUPERVISOR_TOKEN_FILE",
		"PGPASSWORD", "PGPASSFILE", "PGHOST", "PGUSER", "PGDATABASE":
		return true
	}
	u := strings.ToUpper(k)
	return strings.Contains(u, "SECRET") || strings.Contains(u, "TOKEN") || strings.Contains(u, "PASSWORD") ||
		strings.HasPrefix(u, "MAKTABA_CLOUD") || strings.HasPrefix(u, "MAKTABA_PLEX") || strings.HasPrefix(u, "MAKTABA_DIZQUE") ||
		strings.HasSuffix(u, "_DSN") || strings.HasPrefix(u, "AWS_") || strings.HasPrefix(u, "MAKTABA_CLUSTER_PG")
}

// RunnerEnv is a runner's environment: the supervisor's own without any database
// address, secret or token, plus where its sockets and areas are.
func RunnerEnv(cfg config.Config, base []string) []string {
	out := make([]string, 0, len(base)+6)
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !runnerEnvDrop(k) {
			out = append(out, kv)
		}
	}
	areas, _ := json.Marshal(RunnerAreas(cfg))
	out = setEnv(out, "MAKTABA_RUNNER_DIR", RunnerDir(cfg))
	out = setEnv(out, EnvPluginAPISocket, PluginsPaths(cfg).API)
	out = setEnv(out, "MAKTABA_DATA_DIR", cfg.Server.DataDir)
	out = setEnv(out, "MAKTABA_RUNNER_AREAS", string(areas))
	// a runner has no network for a model (its sandbox refuses it): it loads what the
	// cache has, and a stage whose model is missing runs in the worker, which fetches it
	out = setEnv(out, "HF_HUB_OFFLINE", "1")
	out = setEnv(out, "TRANSFORMERS_OFFLINE", "1")
	return out
}

// RunnerAreas maps the [trusted] file areas to this machine's folders (story 54.23 §3):
// the defaults. The worker writes the folders it really uses (<runner dir>/areas.json: the
// library roots through this machine's share-path mapping, the thumbnail cache), which a
// runner reads over these (story 54.29).
func RunnerAreas(cfg config.Config) map[string]string {
	data := cfg.Server.DataDir
	return map[string]string{
		"data.thumbnails": filepath.Join(data, "thumbnails"),
		"data.index":      filepath.Join(data, "index"),
		"data.review":     filepath.Join(data, "review"),
		"data.channels":   filepath.Join(data, "channels"),
		"data.tv-slots":   filepath.Join(data, "tv-slots"),
		"data.cache":      filepath.Join(data, "cache"),
		"models":          filepath.Join(cfg.StateDir(), "models"),
	}
}

// BuildPluginRunners is one dormant process per runner group the release needs, built
// only on a machine that runs runners (RunnersHere).
func BuildPluginRunners(opts Options) ([]Process, error) {
	if !RunnersHere(opts.Config) {
		return nil, nil
	}
	base, err := ChildEnv(RolePipeline, opts.Config)
	if err != nil {
		return nil, err
	}
	env := RunnerEnv(opts.Config, append(os.Environ(), base...))
	dir := RunnerDir(opts.Config)
	api := PluginsPaths(opts.Config).API
	var out []Process
	runner := func(instance string, env []string, dormant bool) Process {
		sock := filepath.Join(dir, "runner-"+instance+".sock")
		cmd := exec.Command(pythonFor(opts), "-m", "maktaba_plugin_runner", "--group", instance, "--api-socket", api, "--run-dir", dir)
		cmd.Env = env
		wire(cmd)
		return Process{
			Role: RolePluginRunner, Instance: instance, Cmd: cmd, Dormant: dormant,
			Probe: "unix:" + sock, ProbeTimeout: PythonProbeTimeout,
			Check: func(ctx context.Context) error { return askRunnerHello(ctx, sock) },
			BeforeStart: func() {
				privateDir(dir)
			},
			FatalExits: map[int]string{RunnerSandboxExit: ReasonSandboxUnavailable,
				RunnerUntrustedExit: ReasonTrustedUnsigned, RunnerEnvExit: ReasonRunnerEnv},
		}
	}
	for _, g := range sharedplugins.FirstPartyGroups() {
		switch {
		case containsString(RunnerGroups, g):
			// the shared groups: core.plugin_runners, started while a component they serve
			// is on (dormant until then)
			out = append(out, runner(g, env, true))
		case g == GroupML:
			// wave 3 (story 54.30): one ml runner beside each transcription worker, with its
			// knobs and its slot (the worker pipeline/stt-<i> asks ml-<i>); with none, the
			// main worker transcribes and asks ml. The transcription component's processes.
			plan := PlanFor(opts)
			if plan.Workers <= 0 {
				out = append(out, runner(GroupML, append(append([]string{}, env...), plan.knobEnv()...), false))
				continue
			}
			for i := 1; i <= plan.Workers && i <= MaxPipelineInstances; i++ {
				e := append(append([]string{}, env...), plan.knobEnv()...)
				e = setEnv(e, "MAKTABA_STT_SLOT", "stt-"+strconv.Itoa(i))
				out = append(out, runner(GroupML+"-"+strconv.Itoa(i), e, false))
			}
		case g == GroupIndex:
			// the search index's stages (search.semantic's process beside pipeline/index,
			// which keeps the query server)
			out = append(out, runner(GroupIndex, env, false))
		}
	}
	return out, nil
}

func containsString(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// askRunnerHello is a runner's health: it answers hello on its socket.
func askRunnerHello(ctx context.Context, socket string) error {
	var raw json.RawMessage
	if err := HostCall(ctx, socket, map[string]any{"type": "hello"}, &raw); err != nil {
		return err
	}
	var frame struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		return err
	}
	if frame.Type != "hello" {
		return errors.New("the plugin runner answered " + frame.Type)
	}
	return nil
}
