//go:build windows

package adopt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// sharedPC is a Windows install from before Epic 38 on the default paths:
// its server.toml and secrets key in %APPDATA%\Maktaba, its database, search
// index and models in the default data folder %LOCALAPPDATA%\Maktaba\data,
// which is the app's own data folder too; a server started by hand from a
// release folder. With copies, a newer command has copied its server.toml and
// key into %LOCALAPPDATA%\Maktaba (story 38.1), where the app keeps its own.
func sharedPC(t *testing.T, copies bool) (*fakeSys, Target, string, string) {
	t.Helper()
	root := t.TempDir()
	local, roaming := filepath.Join(root, "Local"), filepath.Join(root, "Roaming")
	t.Setenv("MAKTABA_CONFIG", "")
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", roaming)
	t.Setenv("MAKTABA_NODE_CONFIG", "")
	app := filepath.Join(local, "Maktaba")
	data := filepath.Join(app, "data")
	toml := fmt.Sprintf("[server]\ndata_dir = %q\n[database]\nurl = %q\n", filepath.ToSlash(data), "sqlite://"+filepath.ToSlash(filepath.Join(data, "maktaba.db")))
	write(t, filepath.Join(roaming, "Maktaba", "server.toml"), toml)
	write(t, filepath.Join(roaming, "Maktaba", "secrets.key"), "the home's secrets key")
	if copies {
		write(t, filepath.Join(app, "server.toml"), toml)
		write(t, filepath.Join(app, "keys", "secrets.key"), "the home's secrets key")
	}
	write(t, filepath.Join(data, "maktaba.db"), "the library")
	write(t, filepath.Join(data, "chroma", "index.bin"), "the search index")
	write(t, filepath.Join(data, "models", "m.bin"), "a model")

	// the old install's environment is the user's: its folders (and no node config)
	sys := newFakeSys(map[string]string{"LOCALAPPDATA": local, "APPDATA": roaming,
		"MAKTABA_NODE_CONFIG": filepath.Join(root, "none", "node.json")})
	server := filepath.Join(root, "release", "maktaba-server.exe")
	sys.procs = []Proc{{PID: 300, Name: "maktaba-server.exe", Exe: server, Cmdline: "maktaba-server.exe serve"}}
	tgt := Target{ConfigPath: config.Path(), StateDir: app, KeysDir: filepath.Join(app, "keys"), NodeConfig: filepath.Join(app, "node.json")}
	return sys, tgt, roaming, data
}

// TestAnOldInstallInTheAppsFolders: its original and its copy are one install
// (the copy wins, as on its own reads); nothing is copied onto itself (its key
// and database are the app's files already); its state is read with its own
// rule (its default data folder holds it), copied beside it into the app's
// root, and kept where it was.
func TestAnOldInstallInTheAppsFolders(t *testing.T) {
	fastStop(t)
	sys, tgt, _, data := sharedPC(t, true)
	srcs, err := Find(sys, "")
	if err != nil || len(srcs) != 1 {
		t.Fatalf("find: %v %+v (one install, not two)", err, srcs)
	}
	src := srcs[0]
	if !samePath(src.Config, tgt.ConfigPath) || !src.ConfigFound || src.DB != DBSQLite ||
		!reflect.DeepEqual(src.Keys, []string{filepath.Join(tgt.KeysDir, "secrets.key")}) {
		t.Fatalf("source %+v", src)
	}
	plan, err := MakePlan(sys, src, tgt)
	if err != nil || len(plan.Problems) != 0 {
		t.Fatalf("plan: %v %+v", err, plan.Problems)
	}
	var items []string
	for _, it := range plan.Items {
		items = append(items, it.Code)
		if samePath(it.From, it.To) {
			t.Errorf("%s copied onto itself", it.What)
		}
	}
	if want := []string{"search_index", "models"}; !reflect.DeepEqual(items, want) {
		t.Fatalf("items %q, want %q: the state from the old data folder, never the key or database in place", items, want)
	}
	if _, err := Move(context.Background(), sys, plan, tgt, nil, nil); err != nil {
		t.Fatal(err)
	}
	for from, to := range map[string]string{
		filepath.Join(data, "chroma", "index.bin"): filepath.Join(tgt.StateDir, "chroma", "index.bin"),
		filepath.Join(data, "models", "m.bin"):     filepath.Join(tgt.StateDir, "models", "m.bin"),
	} {
		if read(t, to) != read(t, from) {
			t.Errorf("%s is not in the app at %s", from, to)
		}
	}
	if read(t, filepath.Join(data, "maktaba.db")) != "the library" {
		t.Error("the library database is lost")
	}
	cfg, _, _ := config.LoadFrom(tgt.ConfigPath)
	if want := "sqlite://" + filepath.ToSlash(filepath.Join(data, "maktaba.db")); cfg.Database.URL != want {
		t.Errorf("the app's database %q, want the same file %q", cfg.Database.URL, want)
	}
}

// TestAnOldInstallNotCopiedYet: no command copied it: its originals in the
// roaming profile are what it runs with, and the move copies them in.
func TestAnOldInstallNotCopiedYet(t *testing.T) {
	sys, tgt, roaming, _ := sharedPC(t, false)
	srcs, _ := Find(sys, "")
	if len(srcs) != 1 || !samePath(srcs[0].Config, filepath.Join(roaming, "Maktaba", "server.toml")) || !srcs[0].ConfigFound ||
		!reflect.DeepEqual(srcs[0].Keys, []string{filepath.Join(roaming, "Maktaba", "secrets.key")}) {
		t.Fatalf("source %+v", srcs)
	}
	plan, _ := MakePlan(sys, srcs[0], tgt)
	key := false
	for _, it := range plan.Items {
		key = key || (it.Code == "key" && samePath(it.To, filepath.Join(tgt.KeysDir, "secrets.key")))
	}
	if !key {
		t.Errorf("the original key is not copied into the app: %+v", plan.Items)
	}
	if _, err := os.Stat(tgt.ConfigPath); err == nil {
		t.Error("a plan wrote the app's server.toml")
	}
}
