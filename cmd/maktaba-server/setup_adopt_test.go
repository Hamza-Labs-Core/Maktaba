package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/adopt"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// adoptFakeSys is a PC with one Maktaba node task, stopped already (nothing
// to end): the adopt step's moves are adopt's own tests'; this one checks the
// step's contract.
type adoptFakeSys struct {
	tasks   []adopt.Task
	env     map[string]string
	deleted []string
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
	f.tasks = nil
	return nil
}
func (f *adoptFakeSys) CreateTask(string, string) error      { return nil }
func (f *adoptFakeSys) RunTask(string) error                 { return nil }
func (f *adoptFakeSys) Processes() ([]adopt.Proc, error)     { return nil, nil }
func (f *adoptFakeSys) Kill(int) error                       { return nil }
func (f *adoptFakeSys) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (f *adoptFakeSys) Getenv(name string) string            { return f.env[name] }
func (f *adoptFakeSys) Listening(string) bool                { return false }
func (f *adoptFakeSys) StopAppServer() error                 { return nil }

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
	e := newSetupEnv(t)
	fakePayload(t, e)
	t.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(e.dir, "app-node.json"))

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
	if items, _ := plan["items"].([]any); len(items) != 2 {
		t.Fatalf("items = %v", plan["items"])
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
}
