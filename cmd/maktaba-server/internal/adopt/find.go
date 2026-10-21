package adopt

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// The kinds of install the app finds.
const (
	// KindLauncher: Maktaba's own sign-in task (maktaba-server setup --service):
	// maktaba-launcher --root <install> serve.
	KindLauncher = "launcher"
	// KindScript: a task that runs a batch file which runs maktaba-server serve
	// (directly, through cmd /c, or conhost --headless cmd /c).
	KindScript = "script"
	// KindNodeWorker: the node installer's worker tasks (run-node.ps1): the
	// pipeline alone, which the app's server replaces.
	KindNodeWorker = "node-worker"
	// KindProcess: a maktaba-server serve that no task explains (started by
	// hand).
	KindProcess = "process"
	// KindService: a Windows service that runs maktaba-launcher or
	// maktaba-server (setup --service --system): it runs as the system, so
	// only an administrator can stop it; a plan refuses it.
	KindService = "service"
)

// The database an install uses.
const (
	DBOwnPostgres = "own-postgres" // a Postgres on this machine (moved into the app's)
	DBPostgres    = "postgres"     // a Postgres elsewhere (kept: the app uses its url)
	DBSQLite      = "sqlite"       // the SQLite file (copied)
	DBNode        = "node"         // a full node: the home's database (nothing to move)
)

// Source is a Maktaba this machine runs outside the app.
type Source struct {
	ID          string `json:"id"` // the task's name, or pid:<n>
	Kind        string `json:"kind"`
	Task        string `json:"task,omitempty"`
	TaskEnabled bool   `json:"task_enabled"`
	// OtherTasks are the node installer's further worker tasks (maktaba-node@<n>):
	// one install, moved together.
	OtherTasks []string `json:"other_tasks,omitempty"`
	// Service is the Windows service's name (KindService).
	Service string `json:"service,omitempty"`
	Script  string `json:"script,omitempty"`
	// Loops: the script runs the server again when it exits, after
	// RestartDelay (what the stop waits out before it counts it stopped).
	Loops        bool          `json:"-"`
	RestartDelay time.Duration `json:"-"`
	Dir          string        `json:"dir"`
	Server       string        `json:"server,omitempty"` // the maktaba-server it runs
	Config       string        `json:"config,omitempty"` // its server.toml
	ConfigFound  bool          `json:"config_found"`
	ServeArgs    []string      `json:"serve_args,omitempty"`
	// Env is the script's set lines; it is never shown (a line may hold a secret).
	Env        map[string]string `json:"-"`
	EnvOrder   []string          `json:"-"`
	NodeConfig string            `json:"node_config,omitempty"`
	Home       string            `json:"home,omitempty"` // a node's home (its name or address)
	DB         string            `json:"db"`
	DBWhere    string            `json:"db_where,omitempty"` // host:port of its Postgres (no credentials)
	Ports      map[string]string `json:"ports"`
	Keys       []string          `json:"keys,omitempty"`
	PIDs       []int             `json:"pids,omitempty"`
}

// Tasks are every task that runs the source.
func (s Source) Tasks() []string {
	if s.Task == "" {
		return nil
	}
	return append([]string{s.Task}, s.OtherTasks...)
}

// owns: p is one of the source's programs (in its folder, its server, or a
// pipeline worker of the node installer's). A console is never one (runsScript
// is the script's).
func (s Source) owns(p Proc) bool {
	if p.Exe == "" || isConsole(p) {
		return false
	}
	return within(p.Exe, s.Dir) || (s.Server != "" && samePath(p.Exe, s.Server)) || (s.Kind == KindNodeWorker && isPipeline(p))
}

// HasProgram reports whether the program at exe is one of the source's (its
// folder's, or its server).
func (s Source) HasProgram(exe string) bool {
	return s.owns(Proc{Name: filepath.Base(exe), Exe: exe})
}

// runsScript: p is the cmd.exe (or the conhost.exe hosting it) that runs the
// source's script, its restart loop: the batch file its command line runs,
// whole (a relative one against the console's current directory, or a `cd /d`
// before it), is the script. Never a part of a name: restart.cmd is not
// start.cmd, nor another folder's start.cmd.
func (s Source) runsScript(p Proc) bool {
	if s.Script == "" || !isConsole(p) {
		return false
	}
	script := ConsoleScript(p.Cmdline, p.Cwd)
	return script != "" && samePath(script, s.Script)
}

// isConsole: cmd.exe or conhost.exe.
func isConsole(p Proc) bool {
	n := strings.ToLower(p.Name)
	return n == "cmd.exe" || n == "conhost.exe"
}

// self is the app's own side of a move: its install (the payload the setup
// step runs from) and this very process. Neither is ever the old install's,
// even where the old install's folder holds them: a user `setup --service`
// roots at %LOCALAPPDATA%\Maktaba, where the app is installed too.
type self struct {
	payload string
	venv    string // the app's pipeline venv (<state>\venv)
	pid     int
	exe     string
}

func selfOf(payload, venv string) self {
	exe, _ := os.Executable()
	return self{payload: payload, venv: venv, pid: os.Getpid(), exe: exe}
}

func (m self) is(p Proc) bool {
	if p.PID == m.pid {
		return true
	}
	if p.Exe == "" {
		return false
	}
	return (m.payload != "" && within(p.Exe, m.payload)) || (m.venv != "" && within(p.Exe, m.venv)) ||
		(m.exe != "" && samePath(p.Exe, m.exe))
}

// getenv is the source's environment: its script's set lines over the user's.
func (s Source) getenv(sys Sys) func(string) string {
	return func(k string) string {
		if v, ok := s.Env[strings.ToUpper(k)]; ok {
			return v
		}
		return sys.Getenv(k)
	}
}

// Find lists the Maktaba installs this machine runs outside the app at
// appPayload (whose own programs, and this process, are left out). Nothing is
// changed. What it cannot read (the tasks, the processes, the services) is an
// error: an install it would miss could run beside the app.
func Find(sys Sys, appPayload string) ([]Source, error) {
	tasks, err := sys.Tasks()
	if err != nil {
		return nil, err
	}
	procs, err := sys.Processes()
	if err != nil {
		return nil, fmt.Errorf("list the running programs: %w", err)
	}
	svcs, err := sys.Services()
	if err != nil {
		return nil, fmt.Errorf("list the services: %w", err)
	}
	me := selfOf(appPayload, "")
	var out []Source
	worker := -1 // the node installer's tasks are one install
	for _, t := range tasks {
		s, ok := classify(sys, t)
		switch {
		case !ok:
		case s.Kind == KindNodeWorker && worker >= 0:
			out[worker].OtherTasks = append(out[worker].OtherTasks, s.Task)
			out[worker].TaskEnabled = out[worker].TaskEnabled || s.TaskEnabled
		default:
			if s.Kind == KindNodeWorker {
				worker = len(out)
			}
			out = append(out, s)
		}
	}
	// every running Maktaba program belongs to a source when its exe is in the
	// source's folder; one that does not is a server started by hand
	for _, p := range procs {
		exe := filepath.Clean(p.Exe)
		if p.Exe == "" || me.is(p) || isConsole(p) {
			continue
		}
		owned := false
		for i := range out {
			if out[i].owns(p) {
				out[i].PIDs = append(out[i].PIDs, p.PID)
				owned = true
			}
		}
		if owned || !strings.EqualFold(p.Name, exeName("maktaba-server")) || !strings.Contains(p.Cmdline, " serve") {
			continue
		}
		out = append(out, Source{ID: "pid:" + itoa(p.PID), Kind: KindProcess, Dir: filepath.Dir(exe), Server: exe,
			PIDs: []int{p.PID}})
	}
	for i := range out {
		describe(sys, &out[i])
	}
	// a service install (its processes are the system's: no path to tell)
	for _, sv := range svcs {
		if s, ok := classifyService(sv); ok {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out, nil
}

// classifyService reads a service: ok when its program is maktaba-launcher
// (its --root is the install) or maktaba-server.
func classifyService(sv Service) (Source, bool) {
	args := SplitArgs(sv.PathName)
	if len(args) == 0 {
		return Source{}, false
	}
	exe := winSlash(args[0])
	switch strings.ToLower(filepath.Base(exe)) {
	case "maktaba-launcher.exe", "maktaba-launcher", "maktaba-server.exe", "maktaba-server":
	default:
		return Source{}, false
	}
	s := Source{ID: "service:" + sv.Name, Kind: KindService, Service: sv.Name, Dir: filepath.Dir(exe), Server: exe}
	for i, a := range args {
		if a == "--root" && i+1 < len(args) {
			s.Dir = winSlash(args[i+1])
		}
	}
	return s, true
}

// classify reads one task: ok when it runs a Maktaba of its own.
func classify(sys Sys, t Task) (Source, bool) {
	base := strings.ToLower(filepath.Base(winSlash(t.Command)))
	args := SplitArgs(t.Args)
	s := Source{ID: t.Name, Task: t.Name, TaskEnabled: t.Enabled}
	switch {
	case base == "maktaba-launcher.exe" || base == "maktaba-launcher":
		s.Kind = KindLauncher
		s.Dir = filepath.Dir(t.Command)
		for i, a := range args {
			if a == "--root" && i+1 < len(args) {
				s.Dir = args[i+1]
			}
		}
		return s, true
	case strings.Contains(strings.ToLower(t.Args), "run-node.ps1") ||
		strings.HasPrefix(strings.ToLower(t.Name[strings.LastIndexByte(t.Name, '\\')+1:]), "maktaba-node"):
		s.Kind = KindNodeWorker
		s.Dir = t.WorkDir
		return s, true
	}
	// a batch file: the command itself, or what cmd /c (under conhost or not) runs
	script := ""
	if isBatch(t.Command) {
		script = t.Command
	} else if base == "cmd.exe" || base == "conhost.exe" || base == "cmd" {
		for _, a := range args {
			if isBatch(a) {
				script = a
				break
			}
		}
	}
	if script == "" {
		return s, false
	}
	if !filepath.IsAbs(script) && !isWinAbs(script) && t.WorkDir != "" {
		script = filepath.Join(t.WorkDir, script)
	}
	raw, err := sys.ReadFile(script)
	if err != nil {
		return s, false
	}
	sc, ok := ParseScript(script, string(raw), sys.Getenv)
	if !ok {
		return s, false
	}
	s.Kind, s.Script, s.Dir, s.Server, s.ServeArgs = KindScript, script, sc.Dir, sc.Server, sc.ServeArgs
	s.Env, s.EnvOrder = sc.Env, sc.EnvOrder
	s.Loops, s.RestartDelay = sc.Loops, sc.RestartDelay
	return s, true
}

// describe fills in what the source's config says: its server.toml, node
// config, database, ports and keys.
func describe(sys Sys, s *Source) {
	getenv := s.getenv(sys)
	home, _ := os.UserHomeDir()
	if p := strings.TrimSpace(getenv("MAKTABA_CONFIG")); p != "" {
		s.Config = p
	} else if s.Kind != KindNodeWorker {
		s.Config = config.Path()
	}
	cfg := config.Defaults()
	if s.Config != "" {
		if c, found, err := config.LoadFrom(s.Config); err == nil && found {
			cfg, s.ConfigFound = c, true
		}
	}
	s.NodeConfig = config.NodeConfigPath(getenv, runtime.GOOS, home)
	n := config.ReadNodeConfig(getenv, runtime.GOOS, home, sys.ReadFile)
	switch {
	case n.Joined() && !strings.EqualFold(strings.TrimSpace(cfg.Server.Role), "home"):
		// a node (an install whose server.toml says role = "home" is the home,
		// whatever node config lies about)
		s.DB = DBNode
		s.Home = n.Home.Name
		if s.Home == "" {
			s.Home = n.HomeURL()
		}
	case cfg.DatabaseURLSet && strings.HasPrefix(cfg.Database.URL, "postgres"):
		s.DB, s.DBWhere = DBPostgres, pgHost(cfg.Database.URL)
		if h, _, _ := net.SplitHostPort(s.DBWhere); h == "127.0.0.1" || h == "localhost" || h == "::1" {
			s.DB = DBOwnPostgres
		}
	default:
		s.DB = DBSQLite
	}
	if s.Kind == KindNodeWorker {
		return
	}
	// the ports the app's server takes over: free before it starts
	s.Ports = map[string]string{"web": cfg.Server.WebListen, "api": cfg.Server.Listen, "stream": cfg.Server.StreamAddr,
		"admin": cfg.Server.AdminAddr, "supervisor": cfg.Supervisor.AdminAddr,
		"pipeline": orDefault(cfg.Server.PipelineGRPCAddr, "127.0.0.1:9090")}
	for k, v := range s.Ports {
		if strings.TrimSpace(v) == "" {
			delete(s.Ports, k)
		}
	}
	keys := filepath.Dir(s.Config)
	if strings.TrimSpace(getenv("MAKTABA_CONFIG")) == "" {
		keys = config.KeysDir()
	}
	for _, k := range []string{"secrets.key", "stream-signing.key"} {
		if _, err := sys.ReadFile(filepath.Join(keys, k)); err == nil {
			s.Keys = append(s.Keys, filepath.Join(keys, k))
		}
	}
}

// pgHost is a Postgres url's host:port, never its credentials.
func pgHost(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Port() == "" {
		return net.JoinHostPort(u.Hostname(), "5432")
	}
	return u.Host
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// isPipeline: a pipeline worker (python -m maktaba_pipeline, maktaba-pipeline.exe).
func isPipeline(p Proc) bool {
	low := strings.ToLower(p.Cmdline)
	return strings.Contains(low, "maktaba_pipeline") || strings.Contains(low, "maktaba-pipeline")
}

func isBatch(p string) bool {
	low := strings.ToLower(strings.Trim(p, `"`))
	return strings.HasSuffix(low, ".cmd") || strings.HasSuffix(low, ".bat")
}

func winSlash(p string) string { return strings.ReplaceAll(p, `\`, string(filepath.Separator)) }

func exeName(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

// within: p is dir or inside it (case-insensitive, as Windows paths are).
func within(p, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(strings.ToLower(filepath.Clean(dir)), strings.ToLower(filepath.Clean(p)))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func itoa(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%10]}, b...)
		n /= 10
	}
	return string(b)
}
