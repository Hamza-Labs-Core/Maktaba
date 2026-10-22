package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/adopt"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// adoptFakeSys is a PC with one Maktaba node task, stopped already (nothing
// to end): the adopt step's moves are adopt's own tests'; this one checks the
// step's contract.
type adoptFakeSys struct {
	tasks   []adopt.Task
	env     map[string]string
	deleted []string
	removed []adopt.Task // what DeleteTask took: CreateTask (from its XML) brings it back
}

func (f *adoptFakeSys) Tasks() ([]adopt.Task, error) { return f.tasks, nil }
func (f *adoptFakeSys) TaskXML(string) ([]byte, error) {
	return []byte("<Task/>"), nil
}
func (f *adoptFakeSys) HasTask(name string) bool {
	for _, t := range f.tasks {
		if t.Name == name {
			return true
		}
	}
	return false
}
func (f *adoptFakeSys) EndTask(string) error { return nil }
func (f *adoptFakeSys) SetTaskEnabled(name string, on bool) error {
	for i := range f.tasks {
		if f.tasks[i].Name == name {
			f.tasks[i].Enabled = on
			return nil
		}
	}
	return errors.New("no task " + name)
}
func (f *adoptFakeSys) DeleteTask(name string) error {
	f.deleted = append(f.deleted, name)
	var keep []adopt.Task
	for _, t := range f.tasks {
		if t.Name == name {
			f.removed = append(f.removed, t)
		} else {
			keep = append(keep, t)
		}
	}
	f.tasks = keep
	return nil
}
func (f *adoptFakeSys) CreateTask(name, _ string) error {
	back := adopt.Task{Name: name}
	for _, t := range f.removed {
		if t.Name == name {
			back = t
		}
	}
	back.Enabled = true
	f.tasks = append(f.tasks, back)
	return nil
}
func (f *adoptFakeSys) RunTask(string) error                               { return nil }
func (f *adoptFakeSys) Processes() ([]adopt.Proc, error)                   { return nil, nil }
func (f *adoptFakeSys) Kill(int) error                                     { return nil }
func (f *adoptFakeSys) EndProcess(int) error                               { return nil }
func (f *adoptFakeSys) Services() ([]adopt.Service, error)                 { return nil, nil }
func (f *adoptFakeSys) FirewallRules(string) ([]adopt.FirewallRule, error) { return nil, nil }
func (f *adoptFakeSys) ActiveProfiles() ([]string, error)                  { return []string{"Private"}, nil }
func (f *adoptFakeSys) CanElevate() bool                                   { return true }
func (f *adoptFakeSys) AddFirewallRules([]adopt.FirewallRule) error        { return nil }
func (f *adoptFakeSys) RemoveFirewallRules([]string) error                 { return nil }
func (f *adoptFakeSys) ReadFile(path string) ([]byte, error)               { return os.ReadFile(path) }
func (f *adoptFakeSys) Getenv(name string) string                          { return f.env[name] }
func (f *adoptFakeSys) Listening(string) bool                              { return false }
func (f *adoptFakeSys) Listeners(string) ([]adopt.Listener, error)         { return nil, nil }
func (f *adoptFakeSys) StopAppServer() error                               { return nil }
func (f *adoptFakeSys) User() (adopt.User, error) {
	return adopt.User{SID: "S-1-5-21-1000-2000-3000-1001", Name: `PC\me`}, nil
}

func serveOn(t *testing.T, h http.Handler) string {
	t.Helper()
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

func TestSetupAdopt(t *testing.T) {
	spawnsPrograms(t) // the venv step's stand-in Python
	e := newSetupEnv(t)
	fakePayload(t, e)
	// the machine's one node config, which the app reads where it is (never a copy)
	t.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(e.dir, "maktaba-local", "node.json"))
	t.Setenv("LOCALAPPDATA", filepath.Join(e.dir, "Local")) // the app window's mode.json (Windows)

	// the app's server as verify sees it once it runs with the carried settings
	api := serveOn(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	sup := serveOn(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"processes": []map[string]any{
			{"role": "api", "wanted": true, "state": "running"},
		}})
	}))

	// the old install: a node of a home, its script, its config and key
	old := filepath.Join(e.dir, "maktaba-local")
	script := filepath.Join(old, "start-maktaba-node.cmd")
	for name, body := range map[string]string{
		script: "cd /d \"%~dp0\"\r\nset MAKTABA_CONFIG=%~dp0server.toml\r\nset MAKTABA_PYTHON=C:\\venv\\python.exe\r\n" +
			"bin\\maktaba-server.exe serve --no-mdns --bin-dir bin > maktaba.log 2>&1\r\n",
		filepath.Join(old, "server.toml"): "[server]\nlisten = \"" + api + "\"\nweb_listen = \"0.0.0.0:8780\"\n" +
			"data_dir = '" + filepath.Join(old, "data") + "'\n[supervisor]\nadmin_addr = \"" + sup + "\"\n",
		filepath.Join(old, "stream-signing.key"): "the node's key",
		filepath.Join(old, "node.json"):          `{"node_id":"n-1","database_url":"postgres://node@home/maktaba","home":{"name":"Home"}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	name := `\Maktaba node (maktaba-local)`
	sys := &adoptFakeSys{
		tasks: []adopt.Task{{Name: name, Command: `C:\Windows\System32\conhost.exe`,
			Args: `--headless cmd.exe /c "` + script + `"`, WorkDir: old, Enabled: true}},
		env: map[string]string{"MAKTABA_NODE_CONFIG": filepath.Join(old, "node.json")},
	}
	prev := adoptSys
	adoptSys = func() adopt.Sys { return sys }
	t.Cleanup(func() { adoptSys = prev })

	// a fresh setup started first: the move drops what it collected
	if code, _, ev := runStep(t, "admin", map[string]string{"username": "admin", "password": "s3cret-long"}); code != 0 {
		t.Fatalf("admin = %d %v", code, ev)
	}
	// ... but beside a node of a home it cannot go on: the app would be a
	// second copy of that node (check says so, finish refuses)
	freshRefused(t, name)

	code, _, ev := runStep(t, "adopt", map[string]string{"action": "find"})
	if code != 0 {
		t.Fatalf("find = %d %v", code, ev)
	}
	srcs, _ := data(t, ev)["sources"].([]any)
	if len(srcs) != 1 || srcs[0].(map[string]any)["id"] != name || srcs[0].(map[string]any)["db"] != "node" {
		t.Fatalf("sources = %v", srcs)
	}

	code, _, ev = runStep(t, "adopt", map[string]string{"action": "plan", "source": name})
	if code != 0 {
		t.Fatalf("plan = %d %v", code, ev)
	}
	plan := data(t, ev)
	if items, _ := plan["items"].([]any); len(items) != 1 || items[0].(map[string]any)["code"] != "key" {
		t.Fatalf("items = %v", plan["items"])
	}
	// what is carried, in codes the screens translate
	carried, _ := plan["carried"].([]any)
	if len(carried) == 0 || carried[len(carried)-1].(map[string]any)["code"] != "no_mdns" {
		t.Fatalf("carried = %v", plan["carried"])
	}
	if code, _, ev := runStep(t, "adopt", map[string]string{"action": "plan", "source": `\Gone`}); code != 1 || ev["code"] != "adopt_gone" {
		t.Fatalf("an unknown source = %d %v", code, ev)
	}

	code, evs, ev := runStep(t, "adopt", map[string]string{"action": "move", "source": name})
	if code != 0 {
		t.Fatalf("move = %d %v", code, evs)
	}
	d := data(t, ev)
	if d["web_url"] != "http://127.0.0.1:8780" || d["supervisor_url"] != "http://"+sup {
		t.Fatalf("urls = %v %v", d["web_url"], d["supervisor_url"])
	}
	stages := map[string]bool{}
	for _, p := range evs[:len(evs)-1] {
		stages[p["stage"].(string)] = true
	}
	for _, s := range []string{"venv", "stop", "copy", "settings"} {
		if !stages[s] {
			t.Errorf("no %s progress in %v", s, evs)
		}
	}
	if _, err := os.Stat(filepath.Join(e.state, "state", "setup.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the fresh setup's pending state stays: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(filepath.Dir(e.config), "stream-signing.key")); string(b) != "the node's key" {
		t.Errorf("the key is not in the app: %q", b)
	}
	cfg, _, err := config.Load()
	if err != nil || cfg.Server.WebListen != "0.0.0.0:8780" || cfg.Server.MDNSOn() || cfg.Server.DataDir != e.state {
		t.Fatalf("the app's server.toml: %+v %v", cfg.Server, err)
	}
	if logged, _ := os.ReadFile(filepath.Join(e.logs, "adopt.log")); !strings.Contains(string(logged), "moving "+name) {
		t.Errorf("adopt.log = %q", logged)
	}
	// the app counts as set up: a node, the home's database
	if code, ev := setupStep(t, "status", "{}"); code != 0 || data(t, ev)["configured"] != true {
		t.Fatalf("status after the move = %d %v", code, ev)
	}
	// moved already: the step says so with its code and the source, for the
	// screens' own words, and its English for Details
	code, _, ev = runStep(t, "adopt", map[string]string{"action": "move", "source": name})
	if params, _ := ev["params"].(map[string]any); code != 1 || ev["code"] != "adopt_moved_already" || params["source"] != name ||
		!strings.Contains(ev["message"].(string), "moved into the app already") {
		t.Fatalf("a second move = %d %v", code, ev)
	}

	code, _, ev = runStep(t, "adopt", map[string]any{"action": "verify", "wait_sec": 1})
	if code != 0 {
		t.Fatalf("verify = %d %v", code, ev)
	}
	if rec := data(t, ev)["move"].(map[string]any); rec["stage"] != adopt.StageFinished || len(sys.deleted) != 1 {
		t.Fatalf("after verify: %v, deleted %q", rec["stage"], sys.deleted)
	}
	if code, _, ev := runStep(t, "adopt", map[string]string{"action": "nope"}); code != 1 || ev["code"] != "bad_input" {
		t.Fatalf("a bad action = %d %v", code, ev)
	}

	// undo (adopt --undo): the old task back, the app window out of server
	// mode, and setup's status no longer counts the app as set up, so the
	// window's setup does not register the app's server over the old install
	cfg, _, _ = config.Load()
	tgt := adoptTarget(cfg)
	if runtime.GOOS == "windows" {
		if want := filepath.Join(e.dir, "Local", "com.hamzalabs.maktaba", "mode.json"); tgt.ModeFile != want {
			t.Fatalf("mode.json at %q, want %q", tgt.ModeFile, want)
		}
		if err := os.MkdirAll(filepath.Dir(tgt.ModeFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tgt.ModeFile, []byte(`{"mode":"server","web_url":"http://127.0.0.1:8780"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, _, ev = runStep(t, "adopt", map[string]string{"action": "undo"})
	if code != 0 || data(t, ev)["move"].(map[string]any)["stage"] != adopt.StageUndone {
		t.Fatalf("undo = %d %v", code, ev)
	}
	if !sys.HasTask(name) {
		t.Error("the old task is not back")
	}
	if _, err := os.Stat(tgt.ModeFile); tgt.ModeFile != "" && !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the window stays in server mode: %v", err)
	}
	if code, ev := setupStep(t, "status", "{}"); code != 0 || data(t, ev)["configured"] != false {
		t.Fatalf("status after an undo = %d %v", code, ev)
	}
	// and a fresh setup after the undo is refused as before the move: the old
	// node runs again (with the machine's one node config, which the app reads
	// too)
	freshRefused(t, name)
	if code, _, ev := runStep(t, "adopt", map[string]string{"action": "undo"}); code != 1 || ev["code"] != "adopt_no_move" {
		t.Fatalf("undo twice = %d %v", code, ev)
	}
}

// freshRefused: check reports node_here (a problem: Next is blocked) and
// finish refuses it, while a node of a home (the task source) runs here.
func freshRefused(t *testing.T, source string) {
	t.Helper()
	prevFacts, prevGPUs := hostFacts, hostGPUs
	t.Cleanup(func() { hostFacts, hostGPUs = prevFacts, prevGPUs })
	hostFacts = func(string) (uint64, uint64) { return 32 << 30, 500 << 30 }
	hostGPUs = func() []hostinfo.GPU { return nil }
	_, _, ev := runStep(t, "check", map[string]any{})
	d := data(t, ev)
	problems, _ := d["problems"].([]any)
	found := false
	for _, p := range problems {
		n := p.(map[string]any)
		params, _ := n["params"].(map[string]any)
		found = found || (n["code"] == "node_here" && params["home"] == "Home" && params["source"] == source)
	}
	if d["ok"] != false || !found {
		t.Fatalf("check beside a node = %v", d)
	}
	if code, _, ev := runStep(t, "finish", map[string]any{}); code != 1 || ev["code"] != "node_here" {
		t.Fatalf("finish beside a node = %d %v", code, ev)
	}
}

// TestTheMoveKnowsAnotherPCsNodeConfig: the app's side of a move carries the
// roaming node config another PC claimed (the plan says the app is not that
// node), as setup's check reads it.
func TestTheMoveKnowsAnotherPCsNodeConfig(t *testing.T) {
	foreignNodeConfig = func() (string, string) { return `C:\Users\me\AppData\Roaming\Maktaba\node.json`, "other-pc" }
	t.Cleanup(func() { foreignNodeConfig = config.LocalForeignNodeConfig })
	tgt := adoptTarget(config.Defaults())
	if tgt.ForeignNode != `C:\Users\me\AppData\Roaming\Maktaba\node.json` || tgt.ForeignNodeOwner != "other-pc" {
		t.Errorf("target %+v", tgt)
	}
}
