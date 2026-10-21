package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// TestMain keeps the tests off this machine's own server.toml and keys (a
// source without MAKTABA_CONFIG reads the default ones).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "adopt-test-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "none", "server.toml"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fakeSys is a PC: its tasks, its processes (alive until their task is ended,
// or until killed when stubborn) and the ports they hold.
type fakeSys struct {
	mu       sync.Mutex
	tasks    []Task
	xml      map[string][]byte
	procs    []Proc
	taskOf   map[int]string // pid → the task whose end stops it
	stubborn map[int]bool   // pid → survives its task's end
	ports    map[string]int // port → pid listening
	env      map[string]string

	ended, killed, deleted, run []string
	endedProcs                  []int // EndProcess: one process, not its tree
	enabled                     map[string]bool
	created                     map[string]string
	appStopped                  int
	user                        User
	services                    []Service
	procErr                     error // the process list cannot be read
	// the firewall: its rules, the profiles in use, whether this user may
	// allow an administrator's change, what was asked of it
	rules                  []FirewallRule
	profiles               []string
	noAdmin, declines      bool
	fwErr                  error
	added                  [][]FirewallRule
	removed                [][]string
	endedWhenRulesWereMade int // how many tasks were ended when the rules were made
	// onList runs (locked) before each process list: a restart loop's server
	// coming back, say
	onList func(f *fakeSys)
}

// fakeUser is the fake PC's user: a SID of the form Windows gives, and the
// name Task Scheduler writes for them.
var fakeUser = User{SID: "S-1-5-21-1000-2000-3000-1001", Name: `PC\me`}

func newFakeSys(env map[string]string) *fakeSys {
	return &fakeSys{xml: map[string][]byte{}, taskOf: map[int]string{}, stubborn: map[int]bool{},
		ports: map[string]int{}, env: env, enabled: map[string]bool{}, created: map[string]string{}, user: fakeUser}
}

func (f *fakeSys) Tasks() ([]Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]Task(nil), f.tasks...)
	for i := range out {
		if on, ok := f.enabled[out[i].Name]; ok {
			out[i].Enabled = on
		}
	}
	return out, nil
}

func (f *fakeSys) TaskXML(name string) ([]byte, error) {
	if b, ok := f.xml[name]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("no task %s", name)
}

func (f *fakeSys) HasTask(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tasks {
		if t.Name == name {
			return true
		}
	}
	return false
}

func (f *fakeSys) EndTask(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, name)
	for pid, task := range f.taskOf {
		if task == name && !f.stubborn[pid] {
			f.drop(pid)
		}
	}
	return nil
}

func (f *fakeSys) drop(pid int) {
	var keep []Proc
	for _, p := range f.procs {
		if p.PID != pid {
			keep = append(keep, p)
		}
	}
	f.procs = keep
	for port, owner := range f.ports {
		if owner == pid {
			delete(f.ports, port)
		}
	}
}

func (f *fakeSys) SetTaskEnabled(name string, on bool) error {
	if !f.HasTask(name) {
		return fmt.Errorf("no task %s", name)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled[name] = on
	return nil
}

func (f *fakeSys) DeleteTask(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, name)
	var keep []Task
	for _, t := range f.tasks {
		if t.Name != name {
			keep = append(keep, t)
		}
	}
	f.tasks = keep
	return nil
}

func (f *fakeSys) CreateTask(name, xmlFile string) error {
	raw, err := os.ReadFile(xmlFile)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created[name] = string(raw)
	f.tasks = append(f.tasks, Task{Name: name, Enabled: true})
	return nil
}

func (f *fakeSys) RunTask(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.run = append(f.run, name)
	return nil
}

func (f *fakeSys) Processes() ([]Proc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.procErr != nil {
		return nil, f.procErr
	}
	if f.onList != nil {
		f.onList(f)
	}
	return append([]Proc(nil), f.procs...), nil
}

func (f *fakeSys) alive(pid int) bool {
	for _, p := range f.procs {
		if p.PID == pid {
			return true
		}
	}
	return false
}

func (f *fakeSys) EndProcess(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endedProcs = append(f.endedProcs, pid)
	f.drop(pid)
	return nil
}

func (f *fakeSys) Services() ([]Service, error) { return f.services, nil }

func (f *fakeSys) FirewallRules(dir string) ([]FirewallRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fwErr != nil {
		return nil, f.fwErr
	}
	var out []FirewallRule
	for _, r := range f.rules {
		if within(r.Program, dir) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeSys) ActiveProfiles() ([]string, error) {
	if f.profiles == nil {
		return []string{"Private"}, nil
	}
	return f.profiles, nil
}

func (f *fakeSys) CanElevate() bool { return !f.noAdmin }

func (f *fakeSys) AddFirewallRules(rules []FirewallRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.declines {
		return ErrDeclined
	}
	f.added = append(f.added, rules)
	f.endedWhenRulesWereMade = len(f.ended)
	f.rules = append(f.rules, rules...)
	return nil
}

func (f *fakeSys) RemoveFirewallRules(names []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.declines {
		return ErrDeclined
	}
	f.removed = append(f.removed, names)
	var keep []FirewallRule
	for _, r := range f.rules {
		if !slices.Contains(names, r.Name) {
			keep = append(keep, r)
		}
	}
	f.rules = keep
	return nil
}

func (f *fakeSys) Kill(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, fmt.Sprint(pid))
	f.drop(pid)
	return nil
}

func (f *fakeSys) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (f *fakeSys) Getenv(name string) string            { return f.env[name] }

func (f *fakeSys) Listening(port string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.ports[port]
	return ok
}

func (f *fakeSys) StopAppServer() error {
	f.appStopped++
	return nil
}

func (f *fakeSys) User() (User, error) { return f.user, nil }

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// tasksXML is `schtasks /Query /XML ONE` for these tasks: a comment with the
// name before each, an ANSI body under a UTF-16 declaration.
func tasksXML(tasks ...string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-16"?>` + "\r\n<Tasks>\r\n" + strings.Join(tasks, "\r\n") + "\r\n</Tasks>\r\n")
}

func taskEntry(name, command, args, workDir string, enabled bool) string {
	return fmt.Sprintf(`  <!-- %s -->
  <Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
    <RegistrationInfo><URI>%s</URI></RegistrationInfo>
    <Settings><Enabled>%t</Enabled><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings>
    <Triggers><LogonTrigger><Delay>PT30S</Delay></LogonTrigger></Triggers>
    <Actions Context="Author">
      <Exec>
        <Command>%s</Command>
        <Arguments>%s</Arguments>
        <WorkingDirectory>%s</WorkingDirectory>
      </Exec>
    </Actions>
  </Task>`, name, name, enabled, command, args, workDir)
}

func TestParseTasks(t *testing.T) {
	raw := tasksXML(
		taskEntry(`\Maktaba node (maktaba-local)`, `C:\Windows\System32\conhost.exe`,
			`--headless cmd.exe /c "E:\tv\maktaba-local\start-maktaba-node.cmd"`, `E:\tv\maktaba-local`, true),
		taskEntry(`\dizqueTV`, `C:\Program Files\nodejs\node.exe`, `index.js`, `E:\tv\dizquetv`, true),
		taskEntry(`\Maktaba\maktaba-node@2`, `powershell.exe`, `-File "C:\x\run-node.ps1" -Instance 2`, ``, false),
		`  <!-- \No actions -->
  <Task><RegistrationInfo><URI>\No actions</URI></RegistrationInfo></Task>`,
		// the app's task of before #175, as a user's: whom its principal runs as
		`  <!-- \Maktaba Server -->
  <Task><RegistrationInfo><URI>\Maktaba Server</URI></RegistrationInfo>
    <Principals><Principal id="Author"><UserId> `+fakeUser.SID+` </UserId><LogonType>InteractiveToken</LogonType></Principal></Principals>
    <Actions Context="Author"><Exec><Command>C:\Program Files\Maktaba\Maktaba.exe</Command><Arguments>--server-agent</Arguments></Exec></Actions>
  </Task>`,
	)
	tasks, err := ParseTasks(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []Task{
		{Name: `\Maktaba node (maktaba-local)`, Command: `C:\Windows\System32\conhost.exe`,
			Args: `--headless cmd.exe /c "E:\tv\maktaba-local\start-maktaba-node.cmd"`, WorkDir: `E:\tv\maktaba-local`, Enabled: true},
		{Name: `\dizqueTV`, Command: `C:\Program Files\nodejs\node.exe`, Args: "index.js", WorkDir: `E:\tv\dizquetv`, Enabled: true},
		{Name: `\Maktaba\maktaba-node@2`, Command: "powershell.exe", Args: `-File "C:\x\run-node.ps1" -Instance 2`},
		{Name: `\No actions`, Enabled: true},
		{Name: OldAppTask, Command: `C:\Program Files\Maktaba\Maktaba.exe`, Args: "--server-agent", Enabled: true, UserID: fakeUser.SID},
	}
	if !reflect.DeepEqual(tasks, want) {
		t.Fatalf("tasks:\n got %#v\nwant %#v", tasks, want)
	}
}

func TestSplitArgs(t *testing.T) {
	for in, want := range map[string][]string{
		`--headless cmd.exe /c "E:\tv\maktaba-local\start-maktaba-node.cmd"`: {"--headless", "cmd.exe", "/c", `E:\tv\maktaba-local\start-maktaba-node.cmd`},
		`--root "C:\Users\a b\Maktaba" serve`:                                {"--root", `C:\Users\a b\Maktaba`, "serve"},
		`say \"hi\"  ""`:                                                     {"say", `"hi"`, ""},
		``:                                                                   nil,
	} {
		if got := SplitArgs(in); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitArgs(%q) = %q, want %q", in, got, want)
		}
	}
}

// thisPCScript is E:\tv\maktaba-local\start-maktaba-node.cmd.
const thisPCScript = "@echo off\r\n" +
	"rem This PC as a full node of the Maktaba home (Mahmouds-Mac-Studio).\r\n" +
	"cd /d \"%~dp0\"\r\n" +
	"set MAKTABA_CONFIG=%~dp0server.toml\r\n" +
	"set MAKTABA_API_BIN=%~dp0bin\\maktaba-api.exe\r\n" +
	"set MAKTABA_STREAMING_BIN=%~dp0bin\\maktaba-streaming.exe\r\n" +
	"set MAKTABA_PYTHON=E:\\tv\\Maktaba\\pipeline\\.venv\\Scripts\\python.exe\r\n" +
	"set PYTHONIOENCODING=utf-8\r\n" +
	"set PYTHONUNBUFFERED=1\r\n" +
	":run\r\n" +
	"if exist maktaba.log move /y maktaba.log maktaba.prev.log >nul\r\n" +
	"bin\\maktaba-server.exe serve --no-mdns --bin-dir bin > maktaba.log 2>&1\r\n" +
	"echo %date% %time% maktaba node stopped (exit %errorlevel%), starting again >> restarts.log\r\n" +
	"timeout /t 10 /nobreak >nul\r\n" +
	"goto run\r\n"

func TestParseScript(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "maktaba-local")
	path := filepath.Join(dir, "start-maktaba-node.cmd")
	s, ok := ParseScript(path, thisPCScript, func(string) string { return "" })
	if !ok {
		t.Fatal("the script runs maktaba-server serve")
	}
	if s.Dir != dir {
		t.Errorf("dir %q, want %q", s.Dir, dir)
	}
	if got, want := s.Server, filepath.Join(dir, "bin", "maktaba-server.exe"); got != want {
		t.Errorf("server %q, want %q", got, want)
	}
	if want := []string{"--no-mdns", "--bin-dir", "bin"}; !reflect.DeepEqual(s.ServeArgs, want) {
		t.Errorf("serve args %q, want %q", s.ServeArgs, want)
	}
	if got, want := s.Log, filepath.Join(dir, "maktaba.log"); got != want {
		t.Errorf("log %q, want %q", got, want)
	}
	if got, want := s.Env["MAKTABA_CONFIG"], dir+string(filepath.Separator)+"server.toml"; got != want {
		t.Errorf("MAKTABA_CONFIG %q, want %q", got, want)
	}
	if want := []string{"MAKTABA_CONFIG", "MAKTABA_API_BIN", "MAKTABA_STREAMING_BIN", "MAKTABA_PYTHON", "PYTHONIOENCODING", "PYTHONUNBUFFERED"}; !reflect.DeepEqual(s.EnvOrder, want) {
		t.Errorf("env order %q, want %q", s.EnvOrder, want)
	}

	if _, ok := ParseScript(path, "@echo off\r\nnode index.js\r\n", func(string) string { return "" }); ok {
		t.Error("a script that runs no maktaba-server is not one")
	}
	// %VAR% from the user's environment, set lines seen before
	s, ok = ParseScript(path, "set ROOT=%BASE%\\mk\r\n\"%ROOT%\\maktaba-server\" serve --role api\r\n",
		func(k string) string { return map[string]string{"BASE": `C:\Apps`}[k] })
	if want := filepath.Clean(`C:\Apps\mk\maktaba-server`); !ok || s.Server != want {
		t.Errorf("server from %%ROOT%%: %q (ok %v)", s.Server, ok)
	}
	if want := []string{"--role", "api"}; !reflect.DeepEqual(s.ServeArgs, want) {
		t.Errorf("serve args %q, want %q", s.ServeArgs, want)
	}
}

// thisPC is a PC laid out like this one: a node in a folder of its own,
// started by a script task under conhost, its node config in the roaming
// profile, dizqueTV beside it; and the app's place, empty.
type thisPC struct {
	sys      *fakeSys
	dir      string
	nodeJSON string
	tgt      Target
}

const thisPCToml = `# Maktaba on this PC
[server]
listen = "127.0.0.1:8781"
data_dir = "%s"
web_listen = "0.0.0.0:8780"
admin_addr = "127.0.0.1:9100"
stream_addr = "0.0.0.0:8782"

[database]
url = "postgres://maktaba:maktaba@127.0.0.1:5432/maktaba?sslmode=disable"

[media]
roots = ["Y:/", "O:/"]

[transcription]
backend = "faster-whisper"
model = "large-v3"
`

func newThisPC(t *testing.T) *thisPC {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "maktaba-local")
	script := filepath.Join(dir, "start-maktaba-node.cmd")
	write(t, script, thisPCScript)
	write(t, filepath.Join(dir, "server.toml"), fmt.Sprintf(thisPCToml, filepath.ToSlash(filepath.Join(dir, "data"))))
	write(t, filepath.Join(dir, "stream-signing.key"), "the node's stream key")
	write(t, filepath.Join(dir, "data", "cache", "streaming", "seg-1.ts"), "a cached segment")
	nodeJSON := filepath.Join(root, "Roaming", "Maktaba", "node.json")
	write(t, nodeJSON, `{"node_id":"n-1","name":"MEYWD-HOME","database_url":"postgres://node@home:5432/maktaba",`+
		`"home":{"name":"Mahmouds-Mac-Studio","url":"http://home:8080","instance_id":"h-1"}}`)

	sys := newFakeSys(map[string]string{"MAKTABA_NODE_CONFIG": nodeJSON})
	name := `\Maktaba node (maktaba-local)`
	sys.tasks, _ = ParseTasks(tasksXML(
		taskEntry(name, `C:\Windows\System32\conhost.exe`, `--headless cmd.exe /c "`+script+`"`, dir, true),
		taskEntry(`\dizqueTV`, `C:\Program Files\nodejs\node.exe`, `index.js`, filepath.Join(root, "dizquetv"), true),
	))
	sys.xml[name] = []byte("<Task>the old task</Task>")
	bin := filepath.Join(dir, "bin")
	for i, exe := range []string{"maktaba-server", "maktaba-api", "maktaba-streaming"} {
		pid := 100 + i
		sys.procs = append(sys.procs, Proc{PID: pid, Name: exeName(exe), Exe: filepath.Join(bin, exeName(exe)),
			Cmdline: exeName(exe) + " serve --no-mdns --bin-dir bin"})
		sys.taskOf[pid] = name
	}
	sys.ports["8780"], sys.ports["8781"], sys.ports["8782"] = 100, 101, 102

	app := filepath.Join(root, "Local", "Maktaba")
	return &thisPC{sys: sys, dir: dir, nodeJSON: nodeJSON, tgt: Target{
		ConfigPath: filepath.Join(app, "server.toml"), StateDir: app,
		KeysDir: filepath.Join(app, "keys"), NodeConfig: filepath.Join(app, "node.json"),
	}}
}

func fastStop(t *testing.T) {
	t.Helper()
	grace, wait, poll, settle := stopGrace, stopWait, stopPoll, settleFor
	stopGrace, stopWait, stopPoll = 100*time.Millisecond, 5*time.Second, 10*time.Millisecond
	settleFor = func(Source) time.Duration { return 30 * time.Millisecond }
	t.Cleanup(func() { stopGrace, stopWait, stopPoll, settleFor = grace, wait, poll, settle })
}

// messages are the notes' English.
func messages(notes []Note) []string {
	out := make([]string, len(notes))
	for i, n := range notes {
		out[i] = n.Message
	}
	return out
}

func TestFindThisPC(t *testing.T) {
	pc := newThisPC(t)
	// a server someone started by hand from a download, and the app's own
	pc.sys.procs = append(pc.sys.procs,
		Proc{PID: 300, Name: exeName("maktaba-server"), Exe: filepath.Join(t.TempDir(), exeName("maktaba-server")), Cmdline: "maktaba-server serve"},
		Proc{PID: 400, Name: exeName("maktaba-server"), Exe: filepath.Join(pc.tgt.StateDir, "server", "bin", exeName("maktaba-server")), Cmdline: "maktaba-server serve"},
	)
	srcs, err := Find(pc.sys, filepath.Join(pc.tgt.StateDir, "server"))
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 2 {
		t.Fatalf("found %d sources, want the task and the server started by hand: %+v", len(srcs), srcs)
	}
	hand, s := srcs[0], srcs[1]
	if hand.Kind != KindProcess || hand.ID != "pid:300" || !reflect.DeepEqual(hand.PIDs, []int{300}) {
		t.Errorf("by hand: %+v", hand)
	}
	if s.Kind != KindScript || s.ID != `\Maktaba node (maktaba-local)` || !s.TaskEnabled {
		t.Errorf("kind %s id %s enabled %v", s.Kind, s.ID, s.TaskEnabled)
	}
	if s.Dir != pc.dir || s.Config != filepath.Join(pc.dir, "server.toml") || !s.ConfigFound {
		t.Errorf("dir %q config %q (%v)", s.Dir, s.Config, s.ConfigFound)
	}
	if s.DB != DBNode || s.Home != "Mahmouds-Mac-Studio" || s.NodeConfig != pc.nodeJSON {
		t.Errorf("db %s home %q node config %q", s.DB, s.Home, s.NodeConfig)
	}
	if want := map[string]string{"web": "0.0.0.0:8780", "api": "127.0.0.1:8781", "stream": "0.0.0.0:8782",
		"admin": "127.0.0.1:9100", "supervisor": "127.0.0.1:9103", "pipeline": "127.0.0.1:9090"}; !reflect.DeepEqual(s.Ports, want) {
		t.Errorf("ports %v", s.Ports)
	}
	if want := []string{filepath.Join(pc.dir, "stream-signing.key")}; !reflect.DeepEqual(s.Keys, want) {
		t.Errorf("keys %v", s.Keys)
	}
	if !reflect.DeepEqual(s.PIDs, []int{100, 101, 102}) {
		t.Errorf("pids %v", s.PIDs)
	}
	// what the app shows never holds the script's set lines
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), "MAKTABA_PYTHON") {
		t.Errorf("the source's JSON shows the script's environment: %s", raw)
	}
}

func TestMoveThisPC(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	srcs, err := Find(pc.sys, "")
	if err != nil || len(srcs) != 1 {
		t.Fatalf("find: %v %+v", err, srcs)
	}
	plan, err := MakePlan(pc.sys, srcs[0], pc.tgt)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Problems) > 0 {
		t.Fatalf("problems: %v", plan.Problems)
	}
	var whats []string
	for _, it := range plan.Items {
		whats = append(whats, it.What)
	}
	if want := []string{"stream-signing.key", "node config", "streaming cache"}; !reflect.DeepEqual(whats, want) {
		t.Errorf("items %q, want %q", whats, want)
	}
	if plan.Need != int64(len("the node's stream key")+len("a cached segment")+len(read(t, pc.nodeJSON))) {
		t.Errorf("need %d", plan.Need)
	}
	for _, n := range []string{"[server]", "[media]", `--no-mdns as [server] mdns = "off"`} {
		if !contains(messages(plan.Carried), n) {
			t.Errorf("carried %q lacks %q", plan.Carried, n)
		}
	}
	if contains(messages(plan.Carried), "[database] url") {
		t.Error("a node's own database url is not carried: it reads the home's")
	}
	// the script's set lines only located its programs: only its own Python is
	// said (the app runs its own)
	if len(plan.NotCarried) != 1 || plan.NotCarried[0].Code != "app_python" || plan.NotCarried[0].Params["name"] != "MAKTABA_PYTHON" {
		t.Errorf("not carried %+v: the script's set lines only located its programs", plan.NotCarried)
	}

	venvBuilt := false
	var stages []string
	rec, err := Move(context.Background(), pc.sys, plan, pc.tgt, func(context.Context) error {
		if len(pc.sys.ended) > 0 {
			t.Error("the venv is built while the old install still runs")
		}
		venvBuilt = true
		return nil
	}, func(stage, _ string) { stages = append(stages, stage) })
	if err != nil {
		t.Fatal(err)
	}
	if !venvBuilt || !reflect.DeepEqual(stages, []string{"venv", "stop", "copy", "settings"}) {
		t.Errorf("venv %v, stages %q", venvBuilt, stages)
	}
	if rec.Stage != StageMoved || rec.MovedAt == nil {
		t.Errorf("record %+v", rec)
	}
	name := `\Maktaba node (maktaba-local)`
	if !reflect.DeepEqual(pc.sys.ended, []string{name}) || pc.sys.enabled[name] {
		t.Errorf("ended %q, enabled %v: the old task is ended and disabled", pc.sys.ended, pc.sys.enabled)
	}
	if len(pc.sys.killed) != 0 || len(pc.sys.deleted) != 0 {
		t.Errorf("killed %q deleted %q: it stopped with its task, and the task stays until verified", pc.sys.killed, pc.sys.deleted)
	}
	if len(rec.Saved) != 1 || rec.Saved[0].Task != name || read(t, rec.Saved[0].XML) != "<Task>the old task</Task>" {
		t.Errorf("the old task's definition: %+v", rec.Saved)
	}
	if read(t, filepath.Join(pc.tgt.KeysDir, "stream-signing.key")) != "the node's stream key" ||
		read(t, pc.tgt.NodeConfig) != read(t, pc.nodeJSON) ||
		read(t, filepath.Join(pc.tgt.StateDir, "cache", "streaming", "seg-1.ts")) != "a cached segment" {
		t.Error("the keys, node config and cache are not in the app")
	}
	cfg, found, err := config.LoadFrom(pc.tgt.ConfigPath)
	if err != nil || !found {
		t.Fatalf("the app's server.toml: %v %v", found, err)
	}
	if cfg.Server.WebListen != "0.0.0.0:8780" || cfg.Server.Listen != "127.0.0.1:8781" || cfg.Server.StreamAddr != "0.0.0.0:8782" ||
		cfg.Server.MDNSOn() || !reflect.DeepEqual(cfg.Media.Roots, []string{"Y:/", "O:/"}) || cfg.Transcription.Model != "large-v3" {
		t.Errorf("settings not carried: %+v %+v", cfg.Server, cfg.Media)
	}
	if cfg.Server.DataDir == filepath.Join(pc.dir, "data") || strings.Contains(cfg.Database.URL, "5432") {
		t.Errorf("data_dir %q / database %q are the old install's", cfg.Server.DataDir, cfg.Database.URL)
	}
	// the old folder is kept as it was
	if _, err := os.Stat(filepath.Join(pc.dir, "stream-signing.key")); err != nil {
		t.Error("the old folder lost its key")
	}
	if _, err := os.Stat(pc.nodeJSON); err != nil {
		t.Error("the old node config went: undo needs it")
	}

	// a second move is refused: this install is in the app
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err == nil {
		t.Error("a moved install moved again")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestMoveGoesOnAfterAFailure(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	// a file where the app keeps its caches: the copy fails after the stop
	write(t, filepath.Join(pc.tgt.StateDir, "cache"), "in the way")
	srcs, _ := Find(pc.sys, "")
	plan, err := MakePlan(pc.sys, srcs[0], pc.tgt)
	if err != nil || len(plan.Problems) > 0 {
		t.Fatal(err, plan.Problems)
	}
	if _, err = Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err == nil {
		t.Fatal("the copy went through a file")
	}
	rec, _, _ := ReadRecord(pc.tgt.StateDir)
	if rec.Stage != StageStopped || len(rec.Saved) != 1 {
		t.Fatalf("stage %s, saved %+v; want stopped, the old task's definition kept", rec.Stage, rec.Saved)
	}

	// put right, the move goes on from the copy: the old install is not stopped again
	if err := os.Remove(filepath.Join(pc.tgt.StateDir, "cache")); err != nil {
		t.Fatal(err)
	}
	pc.sys.xml = map[string][]byte{} // reading the task again would fail
	srcs, _ = Find(pc.sys, "")
	if srcs[0].TaskEnabled {
		t.Error("the old task is still enabled")
	}
	plan, _ = MakePlan(pc.sys, srcs[0], pc.tgt)
	rec, err = Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Stage != StageMoved || len(pc.sys.ended) != 1 {
		t.Errorf("stage %s, ended %q", rec.Stage, pc.sys.ended)
	}
	if read(t, filepath.Join(pc.tgt.StateDir, "cache", "streaming", "seg-1.ts")) != "a cached segment" {
		t.Error("the cache is not in the app")
	}

	// another install cannot start while one is under way
	write(t, RecordPath(pc.tgt.StateDir), `{"stage":"stopped","source":{"id":"\\Other"}}`)
	_, err = Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil)
	if ae := codeOf(err); ae == nil || ae.Code != "adopt_under_way" || ae.Params["source"] != `\Other` || !strings.Contains(err.Error(), "under way") {
		t.Errorf("a second source during a move: %v", err)
	}
}

// TestADifferentKeyIsRefusedBeforeTheStop: a key (or node config) the app has
// already, and different, is never replaced: the plan refuses it, and nothing
// of the old install is stopped.
func TestADifferentKeyIsRefusedBeforeTheStop(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	key := filepath.Join(pc.tgt.KeysDir, "stream-signing.key")
	write(t, key, "another key")
	write(t, pc.tgt.NodeConfig, "another node")
	srcs, _ := Find(pc.sys, "")
	plan, err := MakePlan(pc.sys, srcs[0], pc.tgt)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, n := range plan.Problems {
		codes = append(codes, n.Code+" "+fmt.Sprint(n.Params["path"]))
	}
	if want := []string{"key_differs " + key, "key_differs " + pc.tgt.NodeConfig}; !reflect.DeepEqual(codes, want) {
		t.Fatalf("problems %q, want %q", codes, want)
	}
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err == nil {
		t.Fatal("moved over a different key")
	}
	if len(pc.sys.ended) != 0 || len(pc.sys.enabled) != 0 || len(pc.sys.killed) != 0 {
		t.Errorf("ended %q, tasks changed %v, killed %q: nothing is stopped", pc.sys.ended, pc.sys.enabled, pc.sys.killed)
	}
	if _, ok, _ := ReadRecord(pc.tgt.StateDir); ok {
		t.Error("a refused plan left a record")
	}

	// the same key and node config: left as they are, no problem
	write(t, key, "the node's stream key")
	write(t, pc.tgt.NodeConfig, read(t, pc.nodeJSON))
	if plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt); len(plan.Problems) != 0 {
		t.Errorf("the same key: %+v", plan.Problems)
	}
}

// codeOf is err's code and params, when it has them.
func codeOf(err error) *Error {
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

func TestStopEndsWhatOutlivesItsTask(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	pc.sys.stubborn[100] = true // the old maktaba-server ignores its console going away
	pc.sys.procs = append(pc.sys.procs, Proc{PID: 900, Name: "node.exe", Exe: filepath.Join(t.TempDir(), "node.exe")})
	srcs, _ := Find(pc.sys, "")
	if err := stopSource(context.Background(), pc.sys, srcs[0], pc.tgt.self()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pc.sys.killed, []string{"100"}) {
		t.Errorf("killed %q, want the maktaba-server left after the grace", pc.sys.killed)
	}
	if pc.sys.Listening("8780") {
		t.Error("its port is still held")
	}
	procs, _ := pc.sys.Processes()
	if len(procs) != 1 || procs[0].PID != 900 {
		t.Errorf("left %+v: only what is not Maktaba's stays", procs)
	}

	// a port something else keeps: the stop fails rather than start on top of it
	stopWait = 200 * time.Millisecond
	pc = newThisPC(t)
	pc.sys.ports["8782"] = 999
	srcs, _ = Find(pc.sys, "")
	err := stopSource(context.Background(), pc.sys, srcs[0], pc.tgt.self())
	if ae := codeOf(err); ae == nil || ae.Code != "adopt_port_busy" || ae.Params["port"] != "8782" || !strings.Contains(err.Error(), "8782") {
		t.Errorf("a port still in use: %v", err)
	}
}

// TestStopSaysWhatStillRuns: a process of the old install that outlives the
// wait, killed or not, stops the move with its pid.
func TestStopSaysWhatStillRuns(t *testing.T) {
	fastStop(t)
	stopGrace, stopWait = 10*time.Millisecond, 50*time.Millisecond
	pc := newThisPC(t)
	srcs, _ := Find(pc.sys, "")
	src := srcs[0]
	pc.sys.procs = append(pc.sys.procs, Proc{PID: 777, Name: "python.exe", Exe: filepath.Join(pc.dir, "venv", "python.exe")})
	src.PIDs = append(src.PIDs, 777)
	err := stopSource(context.Background(), &undying{fakeSys: pc.sys, pid: 777}, src, pc.tgt.self())
	if ae := codeOf(err); ae == nil || ae.Code != "adopt_still_running" || ae.Params["pids"] != "777" {
		t.Errorf("a process that never ends: %v (%+v)", err, ae)
	}
}

// undying is a PC where one process survives every kill.
type undying struct {
	*fakeSys
	pid int
}

func (u *undying) Kill(pid int) error {
	if pid == u.pid {
		return nil
	}
	return u.fakeSys.Kill(pid)
}

func TestMoveAHomeOnSQLite(t *testing.T) {
	fastStop(t)
	root := t.TempDir()
	dir := filepath.Join(root, "Maktaba")
	data := filepath.Join(dir, "data")
	write(t, filepath.Join(dir, "server.toml"), fmt.Sprintf(`[server]
listen = "0.0.0.0:8080"
data_dir = %q
[database]
url = "sqlite://data/maktaba.db"
[media]
roots = ["D:/Lectures"]
[pipeline]
python = "C:/venv/python.exe"
`, filepath.ToSlash(data)))
	write(t, filepath.Join(data, "maktaba.db"), "sqlite database")
	write(t, filepath.Join(data, "maktaba.db-wal"), "its wal")
	write(t, filepath.Join(data, "chroma", "index.bin"), "the search index")
	write(t, filepath.Join(data, "models", "huggingface", "hub", "m.bin"), "a model")
	write(t, filepath.Join(dir, "secrets.key"), "the home's secrets key")
	hfHome := filepath.Join(root, "hf")
	write(t, filepath.Join(hfHome, "token"), "hf_xxx")
	script := filepath.Join(dir, "run.cmd")
	write(t, script, "cd /d \"%~dp0\"\r\nset MAKTABA_CONFIG=%~dp0server.toml\r\nset MAKTABA_PATH_MAP=/Volumes/Media/=D:/\r\n"+
		"set MAKTABA_STT_REFINE=0\r\nset MAKTABA_FANCY=1\r\nset HF_HOME="+hfHome+"\r\n"+
		"maktaba-server.exe serve --grace 30s --role all\r\n")
	sys := newFakeSys(map[string]string{"MAKTABA_NODE_CONFIG": filepath.Join(root, "none.json")})
	sys.tasks = []Task{{Name: `\Maktaba home`, Command: script, Enabled: true}}
	sys.xml[`\Maktaba home`] = []byte("<Task/>")
	app := filepath.Join(root, "app")
	tgt := Target{ConfigPath: filepath.Join(app, "server.toml"), StateDir: app, KeysDir: filepath.Join(app, "keys"),
		NodeConfig: filepath.Join(app, "node.json")}
	// the app's own defaults: its data folder inside its state root
	write(t, tgt.ConfigPath, fmt.Sprintf("[server]\ndata_dir = %q\n", filepath.ToSlash(filepath.Join(app, "data"))))

	srcs, err := Find(sys, "")
	if err != nil || len(srcs) != 1 {
		t.Fatalf("find: %v %+v", err, srcs)
	}
	if srcs[0].DB != DBSQLite {
		t.Fatalf("db %s", srcs[0].DB)
	}
	plan, err := MakePlan(sys, srcs[0], tgt)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"MAKTABA_PATH_MAP", "MAKTABA_STT_REFINE"} {
		if !contains(messages(plan.Carried), n) {
			t.Errorf("carried %q lacks %s", plan.Carried, n)
		}
	}
	for _, n := range []string{"MAKTABA_FANCY (no server.toml setting)", "serve --role all", "[pipeline] python (the app runs its own)"} {
		if !contains(messages(plan.NotCarried), n) {
			t.Errorf("not carried %q lacks %q", plan.NotCarried, n)
		}
	}
	if _, err := Move(context.Background(), sys, plan, tgt, nil, nil); err != nil {
		t.Fatal(err)
	}
	for from, to := range map[string]string{
		filepath.Join(data, "maktaba.db"):                            filepath.Join(app, "data", "maktaba.db"),
		filepath.Join(data, "maktaba.db-wal"):                        filepath.Join(app, "data", "maktaba.db-wal"),
		filepath.Join(data, "chroma", "index.bin"):                   filepath.Join(app, "chroma", "index.bin"),
		filepath.Join(data, "models", "huggingface", "hub", "m.bin"): filepath.Join(app, "models", "huggingface", "hub", "m.bin"),
		filepath.Join(dir, "secrets.key"):                            filepath.Join(app, "keys", "secrets.key"),
		filepath.Join(hfHome, "token"):                               filepath.Join(app, "models", "huggingface", "token"),
	} {
		if read(t, to) != read(t, from) {
			t.Errorf("%s is not in the app at %s", from, to)
		}
	}
	cfg, _, _ := config.LoadFrom(tgt.ConfigPath)
	if want := "sqlite://" + filepath.ToSlash(filepath.Join(app, "data", "maktaba.db")); cfg.Database.URL != want {
		t.Errorf("database %q, want %q", cfg.Database.URL, want)
	}
	if cfg.Server.DataDir != filepath.Join(app, "data") && cfg.Server.DataDir != filepath.ToSlash(filepath.Join(app, "data")) {
		t.Errorf("data_dir %q", cfg.Server.DataDir)
	}
	if cfg.Media.PathMap != "/Volumes/Media/=D:/" || cfg.Transcription.Refine != "off" || cfg.Pipeline.Python != "" ||
		cfg.Server.Listen != "0.0.0.0:8080" || !cfg.Server.MDNSOn() {
		t.Errorf("settings: map %q refine %q python %q listen %q", cfg.Media.PathMap, cfg.Transcription.Refine, cfg.Pipeline.Python, cfg.Server.Listen)
	}
}

func TestPlanProblems(t *testing.T) {
	pc := newThisPC(t)
	srcs, _ := Find(pc.sys, "")
	src := srcs[0]

	// a home whose Postgres on this PC is down cannot move
	home := src
	home.DB, home.DBWhere = DBOwnPostgres, "127.0.0.1:5432"
	p, err := MakePlan(pc.sys, home, pc.tgt)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Problems) != 1 || p.Problems[0].Code != "postgres_down" || p.Problems[0].Params["where"] != "127.0.0.1:5432" ||
		!strings.Contains(p.Problems[0].Message, "not running") || len(p.Warnings) != 1 || p.Warnings[0].Code != "postgres_stays" {
		t.Errorf("problems %+v warnings %+v", p.Problems, p.Warnings)
	}
	// the plan's JSON: each finding a code, its params and its English
	raw, _ := json.Marshal(p)
	if !strings.Contains(string(raw), `"problems":[{"code":"postgres_down","params":{"where":"127.0.0.1:5432"},"message":"its library database`) {
		t.Errorf("the plan's findings: %s", raw)
	}
	if _, err := Move(context.Background(), pc.sys, p, pc.tgt, nil, nil); err == nil || len(pc.sys.ended) != 0 {
		t.Errorf("a plan with problems moved: %v (ended %q)", err, pc.sys.ended)
	}
	pc.sys.ports["5432"] = 1
	if p, _ = MakePlan(pc.sys, home, pc.tgt); len(p.Problems) != 0 || !contains(messages(p.Carried), "[database] url") {
		t.Errorf("its Postgres running: problems %q, carried %q", p.Problems, p.Carried)
	}

	// a worker that never joined a home
	worker := Source{ID: `\Maktaba\maktaba-node`, Kind: KindNodeWorker, Task: `\Maktaba\maktaba-node`, DB: DBNode, NodeConfig: "x"}
	if p, _ := MakePlan(pc.sys, worker, pc.tgt); len(p.Problems) != 1 || p.Problems[0].Code != "worker_not_joined" || p.Problems[0].Params["path"] != "x" {
		t.Errorf("an unjoined worker: %+v", p.Problems)
	}

	// a server started by hand: the move ends it
	byHand := Source{ID: "pid:300", Kind: KindProcess, Dir: pc.dir, DB: DBSQLite}
	if p, _ := MakePlan(pc.sys, byHand, pc.tgt); len(p.Warnings) != 1 || p.Warnings[0].Code != "started_by_hand" {
		t.Errorf("by hand: %+v", p.Warnings)
	}
}

// fakeServer is the app's server as Verify sees it: its API's /healthz and its
// supervisor's /processes.
func fakeServer(t *testing.T, procs *[]supervisor.ChildStatus) config.Config {
	t.Helper()
	listen := func(h http.Handler) string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewUnstartedServer(h)
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
		return l.Addr().String()
	}
	cfg := config.Defaults()
	cfg.Server.Listen = listen(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
		}
	}))
	cfg.Supervisor.AdminAddr = listen(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"processes": *procs})
	}))
	return cfg
}

func TestVerifyFinishUndo(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	srcs, _ := Find(pc.sys, "")
	plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt)
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err != nil {
		t.Fatal(err)
	}
	name := `\Maktaba node (maktaba-local)`

	procs := []supervisor.ChildStatus{
		{Role: supervisor.RoleAPI, Wanted: true, State: supervisor.StateRunning},
		{Role: supervisor.RolePipeline, Wanted: true, State: supervisor.StateBackoff},
		{Role: supervisor.RoleTVSlot, Instance: "b", State: supervisor.StateStopped}, // a standby nobody asked for
	}
	cfg := fakeServer(t, &procs)
	err := Verify(context.Background(), pc.sys, pc.tgt, cfg, 0)
	if err == nil || !strings.Contains(err.Error(), "pipeline (backoff)") {
		t.Fatalf("a process down: %v", err)
	}
	if ok, err := Finish(pc.sys, pc.tgt.StateDir); ok || err != nil || len(pc.sys.deleted) != 0 {
		t.Fatalf("finished before verified: %v %v", ok, err)
	}
	procs[1].State = supervisor.StateRunning
	if err := Verify(context.Background(), pc.sys, pc.tgt, cfg, 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := Finish(pc.sys, pc.tgt.StateDir); !ok || err != nil {
		t.Fatalf("finish: %v %v", ok, err)
	}
	if !reflect.DeepEqual(pc.sys.deleted, []string{name}) || pc.sys.HasTask(name) {
		t.Errorf("deleted %q", pc.sys.deleted)
	}
	rec, _, _ := ReadRecord(pc.tgt.StateDir)
	if rec.Stage != StageFinished || rec.VerifiedAt == nil || rec.FinishedAt == nil {
		t.Errorf("record %+v", rec)
	}
	if err := Verify(context.Background(), pc.sys, pc.tgt, cfg, 0); err != nil {
		t.Errorf("verifying a finished move again: %v", err)
	}

	// undo: the app's server paused and stopped, this user's app tasks off
	// (theirs, and theirs of the old shared name), the window out of server
	// mode, the old task back from its XML
	app := AppTask(pc.sys.user)
	pc.sys.tasks = append(pc.sys.tasks, Task{Name: app, Enabled: true}, Task{Name: OldAppTask, Enabled: true, UserID: pc.sys.user.Name})
	pc.tgt.ModeFile = filepath.Join(t.TempDir(), "com.hamzalabs.maktaba", "mode.json")
	write(t, pc.tgt.ModeFile, `{"mode":"server","web_url":"http://127.0.0.1:8780","supervisor_url":"http://127.0.0.1:9100"}`)
	if UndoneHere(pc.tgt.StateDir) {
		t.Error("undone before the undo")
	}
	if err := Undo(pc.sys, pc.tgt); err != nil {
		t.Fatal(err)
	}
	if pc.sys.appStopped != 1 || pc.sys.enabled[app] || pc.sys.enabled[OldAppTask] {
		t.Errorf("the app's server: stopped %d, tasks enabled %v", pc.sys.appStopped, pc.sys.enabled)
	}
	if !strings.HasPrefix(app, `\Maktaba Server S-1-`) {
		t.Errorf("the app's task %q is not named by the user's SID", app)
	}
	if _, err := os.Stat(filepath.Join(pc.tgt.StateDir, "state", "server-paused")); err != nil {
		t.Error("the app's server is not paused: the app would start it over the old install")
	}
	if _, err := os.Stat(pc.tgt.ModeFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the window stays in server mode: %v", err)
	}
	if !UndoneHere(pc.tgt.StateDir) {
		t.Error("the undo is not recorded: setup's status would take the server back")
	}
	if pc.sys.created[name] != "<Task>the old task</Task>" || !pc.sys.enabled[name] || !reflect.DeepEqual(pc.sys.run, []string{name}) {
		t.Errorf("the old task: created %q enabled %v run %q", pc.sys.created[name], pc.sys.enabled[name], pc.sys.run)
	}
	if rec, _, _ := ReadRecord(pc.tgt.StateDir); rec.Stage != StageUndone {
		t.Errorf("stage %s", rec.Stage)
	}
	if err := Undo(pc.sys, pc.tgt); err == nil {
		t.Error("undone twice")
	}
}

func TestUndoBeforeFinish(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	srcs, _ := Find(pc.sys, "")
	plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt)
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err != nil {
		t.Fatal(err)
	}
	name := `\Maktaba node (maktaba-local)`
	// no app task yet (setup did not register it): nothing to disable
	if err := Undo(pc.sys, pc.tgt); err != nil {
		t.Fatal(err)
	}
	if len(pc.sys.created) != 0 || !pc.sys.enabled[name] || !reflect.DeepEqual(pc.sys.run, []string{name}) {
		t.Errorf("created %v enabled %v run %q: the disabled task comes back on", pc.sys.created, pc.sys.enabled, pc.sys.run)
	}

	// after an undo the same install can move again
	srcs, _ = Find(pc.sys, "")
	plan, _ = MakePlan(pc.sys, srcs[0], pc.tgt)
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err != nil {
		t.Errorf("moving again after an undo: %v", err)
	}
	if UndoneHere(pc.tgt.StateDir) {
		t.Error("moved again, the record still says undone")
	}
}

// TestUndoTouchesOnlyThisUsersApp: another user's task of the old shared name
// is theirs, and a window in client mode stays so. A fresh setup afterwards
// makes the app this PC's server again.
func TestUndoTouchesOnlyThisUsersApp(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	srcs, _ := Find(pc.sys, "")
	plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt)
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err != nil {
		t.Fatal(err)
	}
	pc.sys.tasks = append(pc.sys.tasks, Task{Name: OldAppTask, Enabled: true, UserID: `PC\you`})
	pc.tgt.ModeFile = filepath.Join(t.TempDir(), "mode.json")
	client := `{"mode":"client","server_url":"https://maktaba.home"}`
	write(t, pc.tgt.ModeFile, client)
	if err := Undo(pc.sys, pc.tgt); err != nil {
		t.Fatal(err)
	}
	if on, set := pc.sys.enabled[OldAppTask]; set && !on {
		t.Error("another user's app task was turned off")
	}
	if read(t, pc.tgt.ModeFile) != client {
		t.Error("a client mode was changed")
	}
	if err := ForgetUndone(pc.tgt.StateDir); err != nil || UndoneHere(pc.tgt.StateDir) {
		t.Errorf("a fresh setup after the undo: %v, undone %v", err, UndoneHere(pc.tgt.StateDir))
	}
	if err := ForgetUndone(pc.tgt.StateDir); err != nil {
		t.Errorf("nothing to forget: %v", err)
	}
}

// TestUndoNeedsThisUsersSID: without the SID the app's task has no name, and
// nothing is changed.
func TestUndoNeedsThisUsersSID(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	srcs, _ := Find(pc.sys, "")
	plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt)
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err != nil {
		t.Fatal(err)
	}
	pc.sys.user = User{}
	if err := Undo(pc.sys, pc.tgt); err == nil || !strings.Contains(err.Error(), "SID") {
		t.Errorf("undo without a SID: %v", err)
	}
	if pc.sys.appStopped != 0 || len(pc.sys.run) != 0 {
		t.Errorf("changed: stopped %d, run %q", pc.sys.appStopped, pc.sys.run)
	}
	if _, err := os.Stat(filepath.Join(pc.tgt.StateDir, "state", "server-paused")); !errors.Is(err, os.ErrNotExist) {
		t.Error("paused before it could undo")
	}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// TestTheOldAppTaskIsThisUsersBySIDOrName is the shell's runs_as rule.
func TestTheOldAppTaskIsThisUsersBySIDOrName(t *testing.T) {
	u := User{SID: fakeUser.SID, Name: `PC\me`}
	another := strings.TrimSuffix(fakeUser.SID, "1001") + "1002" // another user of the same PC
	for id, want := range map[string]bool{
		fakeUser.SID:                  true,
		strings.ToLower(fakeUser.SID): true,
		`pc\ME`:                       true,
		"me":                          true,
		another:                       false,
		`PC\you`:                      false,
		`OTHER\me`:                    false,
		"":                            false,
	} {
		if got := u.runsAs(id); got != want {
			t.Errorf("runsAs(%q) = %v, want %v", id, got, want)
		}
	}
	if (User{SID: fakeUser.SID}).runsAs("me") {
		t.Error("a bare name matched a user without a name")
	}
	if got := taskUser(envOf(map[string]string{"USERNAME": "me", "USERDOMAIN": "PC"})); got != `PC\me` {
		t.Errorf("taskUser = %q", got)
	}
	if got := taskUser(envOf(map[string]string{"USERNAME": "me"})); got != "me" {
		t.Errorf("taskUser without a domain = %q", got)
	}
	if got := taskUser(envOf(map[string]string{"USERDOMAIN": "PC"})); got != "" {
		t.Errorf("taskUser without a name = %q", got)
	}
}

func TestRecordNeverHoldsTheScriptEnv(t *testing.T) {
	fastStop(t)
	pc := newThisPC(t)
	srcs, _ := Find(pc.sys, "")
	plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt)
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err != nil {
		t.Fatal(err)
	}
	if raw := read(t, RecordPath(pc.tgt.StateDir)); strings.Contains(raw, "PYTHONIOENCODING") {
		t.Errorf("move.json holds the script's set lines: %s", raw)
	}
}

// TestMoveNodeWorkers: the node installer's worker tasks (one per pipeline
// instance) are one install: all of them stopped, kept, removed and brought
// back together; the app's server runs as many workers.
func TestMoveNodeWorkers(t *testing.T) {
	fastStop(t)
	root := t.TempDir()
	nodeJSON := filepath.Join(root, "node.json")
	write(t, nodeJSON, `{"node_id":"n-2","database_url":"postgres://node@home/maktaba","home":{"name":"Home"}}`)
	sys := newFakeSys(map[string]string{"MAKTABA_NODE_CONFIG": nodeJSON})
	venvPy := filepath.Join(root, "node", "venv", "Scripts", "python.exe")
	for i, name := range []string{`\Maktaba\maktaba-node`, `\Maktaba\maktaba-node@2`} {
		sys.tasks = append(sys.tasks, Task{Name: name, Command: "powershell.exe", Enabled: true,
			Args: fmt.Sprintf(`-NoProfile -File "%s" -Instance %d`, filepath.Join(root, "node", "run-node.ps1"), i+1)})
		sys.xml[name] = []byte("<Task>" + name + "</Task>")
		pid := 500 + i
		sys.procs = append(sys.procs, Proc{PID: pid, Name: "python.exe", Exe: venvPy,
			Cmdline: venvPy + " -m maktaba_pipeline worker --instance " + fmt.Sprint(i+1)})
		sys.taskOf[pid] = name
	}
	// this PC's own checkout runs a pipeline too: not the installer's, never touched
	sys.procs = append(sys.procs, Proc{PID: 900, Name: "python.exe", Exe: filepath.Join(root, "checkout", "python.exe"), Cmdline: "python -m pytest"})
	app := filepath.Join(root, "app")
	tgt := Target{ConfigPath: filepath.Join(app, "server.toml"), StateDir: app, KeysDir: filepath.Join(app, "keys"),
		NodeConfig: filepath.Join(app, "node.json")}

	srcs, err := Find(sys, "")
	if err != nil || len(srcs) != 1 {
		t.Fatalf("find: %v %+v", err, srcs)
	}
	src := srcs[0]
	if src.Kind != KindNodeWorker || src.Task != `\Maktaba\maktaba-node` || !reflect.DeepEqual(src.OtherTasks, []string{`\Maktaba\maktaba-node@2`}) ||
		!reflect.DeepEqual(src.PIDs, []int{500, 501}) || src.DB != DBNode || src.Home != "Home" {
		t.Fatalf("source %+v", src)
	}
	plan, err := MakePlan(sys, src, tgt)
	if err != nil || len(plan.Problems) > 0 {
		t.Fatalf("plan: %v %v", err, plan.Problems)
	}
	rec, err := Move(context.Background(), sys, plan, tgt, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sys.ended) != 2 || sys.enabled[src.Task] || sys.enabled[src.OtherTasks[0]] || len(rec.Saved) != 2 {
		t.Errorf("ended %q enabled %v saved %+v", sys.ended, sys.enabled, rec.Saved)
	}
	procs, _ := sys.Processes()
	if len(procs) != 1 || procs[0].PID != 900 {
		t.Errorf("left running %+v", procs)
	}
	cfg, found, _ := config.LoadFrom(tgt.ConfigPath)
	if !found || cfg.Pipeline.Instances != 2 || !cfg.PipelineInstancesSet {
		t.Errorf("the app's server.toml: found %v, instances %d", found, cfg.Pipeline.Instances)
	}
	if read(t, tgt.NodeConfig) != read(t, nodeJSON) {
		t.Error("the node config is not in the app")
	}

	rec.Stage = StageVerified
	if err := writeRecord(tgt.StateDir, rec); err != nil {
		t.Fatal(err)
	}
	if ok, err := Finish(sys, tgt.StateDir); !ok || err != nil || len(sys.deleted) != 2 {
		t.Fatalf("finish: %v %v, deleted %q", ok, err, sys.deleted)
	}
	if err := Undo(sys, tgt); err != nil {
		t.Fatal(err)
	}
	if len(sys.created) != 2 || len(sys.run) != 2 || sys.created[`\Maktaba\maktaba-node@2`] != `<Task>\Maktaba\maktaba-node@2</Task>` {
		t.Errorf("created %v run %q", sys.created, sys.run)
	}
}
