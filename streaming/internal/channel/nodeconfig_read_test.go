package channel

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A node config that is there but cannot be read, or is not one (broken, or an ANSI
// file with a byte that is not UTF-8, as the pipeline reads it), is never the home: the
// role is an error, and this host starts as neither.
func TestDetectRoleNeverTakesAnUnreadableNodeConfigForTheHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.json")
	getenv := func(k string) string {
		if k == "MAKTABA_NODE_CONFIG" {
			return path
		}
		return ""
	}
	for name, body := range map[string]string{
		"broken":          `{"node_id":"n1","private_key":"k","data`,
		"ANSI, not UTF-8": "{\"node_id\":\"n1\",\"name\":\"H\xe9l\xe8ne\",\"private_key\":\"k\",\"database_url\":\"postgres://h\"}",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if role, _, err := DetectRole(getenv, "windows", "", os.ReadFile); err == nil {
			t.Errorf("%s: role %q, want an error", name, role)
		}
	}
	denied := func(string) ([]byte, error) { return nil, fs.ErrPermission }
	if role, _, err := DetectRole(getenv, "windows", "", denied); err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("unreadable: role %q, err %v", role, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if role, _, err := DetectRole(getenv, "windows", "", os.ReadFile); err != nil || role != RoleHome {
		t.Errorf("none at all: %q %v, want the home", role, err)
	}
}

// While a write replaces node.json (its lock held, node.json under its backup name for a
// moment, as ReplaceFileW does), a reader waits it out: in many reads, never the home
// and never an error.
func TestDetectRoleWaitsOutAWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node.json")
	getenv := func(k string) string {
		if k == "MAKTABA_NODE_CONFIG" {
			return path
		}
		return ""
	}
	body := []byte(`{"node_id":"n1","private_key":"k","database_url":"postgres://h"}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // the writer: lock, node.json aside, the new one in, the backup gone
		defer wg.Done()
		lock := filepath.Join(dir, ".node.json.lock")
		for i := 0; !stop.Load(); i++ {
			_ = os.WriteFile(lock, nil, 0o600)
			tmp := filepath.Join(dir, ".node.json-1792600000-4242-0000000"+string(rune('0'+i%10))+".tmp")
			bak := filepath.Join(dir, ".node.json-1792600000-4242-0000000"+string(rune('0'+i%10))+".bak")
			_ = os.WriteFile(tmp, body, 0o600)
			_ = os.Rename(path, bak)
			time.Sleep(200 * time.Microsecond) // the gap
			_ = os.Rename(tmp, path)
			_ = os.Remove(bak)
			_ = os.Remove(lock)
		}
	}()
	deadline := time.Now().Add(time.Second)
	reads, wrong := 0, 0
	for time.Now().Before(deadline) {
		reads++
		if role, id, err := DetectRole(getenv, "windows", "", os.ReadFile); err != nil || role != RoleNode || id != "n1" {
			wrong++
			t.Logf("read %d: %q %q %v", reads, role, id, err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if wrong != 0 {
		t.Fatalf("%d of %d reads during writes were not the node", wrong, reads)
	}
}

// While a write replaces the roaming node.json of a machine that joined before Epic 38,
// the path resolves to it every time: never to %LOCALAPPDATA%, where nothing is joined.
func TestNodeConfigPathWaitsOutAWrite(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{
		"APPDATA":            filepath.Join(root, "Roaming"),
		"LOCALAPPDATA":       filepath.Join(root, "Local"),
		"MAKTABA_MACHINE_ID": "pc-a",
	}
	getenv := func(k string) string { return env[k] }
	dir := filepath.Join(root, "Roaming", "Maktaba")
	path := filepath.Join(dir, "node.json")
	body := []byte(`{"node_id":"n1","private_key":"k","database_url":"postgres://h","machine_id":"pc-a"}`)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // the writer: lock, node.json aside, the new one in, the backup gone
		defer wg.Done()
		lock := filepath.Join(dir, ".node.json.lock")
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		for i := 0; !stop.Load(); i++ {
			_ = os.WriteFile(lock, nil, 0o600)
			name := filepath.Join(dir, ".node.json-"+stamp+"-4242-0000000"+string(rune('0'+i%10)))
			_ = os.WriteFile(name+".tmp", body, 0o600)
			_ = os.Rename(path, name+".bak")
			time.Sleep(200 * time.Microsecond) // the gap
			_ = os.Rename(name+".tmp", path)
			_ = os.Remove(name + ".bak")
			_ = os.Remove(lock)
		}
	}()
	deadline := time.Now().Add(time.Second)
	reads, elsewhere := 0, 0
	for time.Now().Before(deadline) {
		reads++
		if got := NodeConfigPath(getenv, "windows", root); got != path {
			elsewhere++
			t.Logf("read %d: %s", reads, got)
		}
	}
	stop.Store(true)
	wg.Wait()
	if elsewhere != 0 {
		t.Fatalf("%d of %d resolutions during writes were not the roaming node config", elsewhere, reads)
	}
}
