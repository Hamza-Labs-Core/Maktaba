package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// fakeDisk makes every folder's disk one local APFS volume with free bytes free.
func fakeDisk(t *testing.T, free uint64, fsType string, local bool) {
	t.Helper()
	prev := statVolume
	statVolume = func(string) (volume, error) {
		return volume{mount: "/fake", name: "Fake", fsType: fsType, free: free, total: free * 2, local: local}, nil
	}
	t.Cleanup(func() { statVolume = prev })
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
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

const tomlBody = `# my Maktaba
[server]
# where the data lives
listen = ":8080"

[media]
roots = ["/Volumes/Media"]
`

// macInstall is an install with the Mac's default layout in a temp dir: the root
// holds server.toml, data/ and the state folders beside it.
func macInstall(t *testing.T) (Install, string) {
	t.Helper()
	base := realTemp(t)
	root := filepath.Join(base, "Application Support", "Maktaba")
	write(t, filepath.Join(root, "server.toml"), tomlBody)
	write(t, filepath.Join(root, "data", "supervisor", "events.jsonl"), "{}\n")
	write(t, filepath.Join(root, "postgres", "PG_VERSION"), "17\n")
	write(t, filepath.Join(root, "postgres", "base", "1", "1259"), strings.Repeat("x", 9000))
	write(t, filepath.Join(root, "backups", "maktaba-2026-10-25.dump"), "dump")
	write(t, filepath.Join(root, "models", "whisper", "model.bin"), strings.Repeat("m", 5000))
	write(t, filepath.Join(root, "state", "database.json"), `{"version":"1.0"}`)
	write(t, filepath.Join(root, "state", "server-paused"), "app's own")
	write(t, filepath.Join(root, "keys", "secrets.key"), "secret")
	if runtime.GOOS != "windows" {
		if err := os.Symlink("python3.12", filepath.Join(root, "models", "link")); err != nil {
			t.Fatal(err)
		}
	}
	in := Install{
		ConfigDir:  root,
		ConfigPath: filepath.Join(root, "server.toml"),
		From:       Layout{Data: filepath.Join(root, "data"), Root: root},
		ToLayout:   func(target string) Layout { return Layout{Data: target, Root: target} },
	}
	return in, base
}

func TestMoveMacLayoutToAnotherDisk(t *testing.T) {
	fakeDisk(t, 100<<30, "apfs", true)
	in, base := macInstall(t)
	target := filepath.Join(base, "Big", "Maktaba")
	verified := ""
	var last uint64
	res, err := Move(context.Background(), in, target, Options{
		VerifyPostgres: func(_ context.Context, root string) error { verified = root; return nil },
		Progress:       func(done, _ uint64) { last = done },
		Now:            func() time.Time { return time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if verified != target {
		t.Errorf("the database was checked in %q, want %q", verified, target)
	}
	if last != res.Bytes || res.Bytes == 0 {
		t.Errorf("progress ended at %d of %d", last, res.Bytes)
	}
	// every folder in its new place: data's contents and the state folders together
	for _, f := range []string{"supervisor/events.jsonl", "postgres/PG_VERSION", "postgres/base/1/1259",
		"backups/maktaba-2026-10-25.dump", "models/whisper/model.bin", "state/database.json", config.StorageMarkerFile} {
		if !exists(filepath.Join(target, f)) {
			t.Errorf("%s is not in the new place", f)
		}
	}
	if runtime.GOOS != "windows" {
		if l, err := os.Readlink(filepath.Join(target, "models", "link")); err != nil || l != "python3.12" {
			t.Errorf("the link was not made again: %q %v", l, err)
		}
	}
	// server.toml points there, its comments kept
	got := read(t, in.ConfigPath)
	if !strings.Contains(got, "# where the data lives") || !strings.Contains(got, `data_dir = "`+target+`"`) {
		t.Errorf("server.toml:\n%s", got)
	}
	cfg, _, err := config.LoadFrom(in.ConfigPath)
	if err != nil || cfg.Server.DataDir != target {
		t.Errorf("server.toml reads data_dir %q (%v)", cfg.Server.DataDir, err)
	}
	// the old folders kept aside, never removed; state/ stays (the app's files)
	stamp := ".moved-20261026-093000"
	for _, n := range []string{"data", "postgres", "backups", "models"} {
		if exists(filepath.Join(in.From.Root, n)) || !exists(filepath.Join(in.From.Root, n+stamp)) {
			t.Errorf("%s was not kept aside as %s", n, n+stamp)
		}
	}
	if !exists(filepath.Join(in.From.Root, "state", "server-paused")) || !exists(filepath.Join(in.From.Root, "keys", "secrets.key")) {
		t.Error("state/ or keys/ left the old root")
	}
	if len(res.Old) != 4 {
		t.Errorf("old copies %v", res.Old)
	}
	if exists(filepath.Join(in.ConfigDir, JournalFile)) {
		t.Error("the journal is still there")
	}
	if r, ok := ReadResult(in.ConfigDir); !ok || !r.OK || r.Target != target {
		t.Errorf("result %+v %v", r, ok)
	}
	if p, missing := config.StorageMissing(in.ConfigDir, target); missing {
		t.Errorf("the new place reads as missing (%s)", p)
	}
	doc := Report(InstallFrom(in, target), "")
	if doc.Move.State != "done" || len(doc.Move.Old) != 4 {
		t.Errorf("report's move %+v", doc.Move)
	}
}

// InstallFrom is in after its data moved to target.
func InstallFrom(in Install, target string) Install {
	in.From = Layout{Data: target, Root: target}
	return in
}

func TestMoveBetweenTwoChosenFolders(t *testing.T) {
	fakeDisk(t, 100<<30, "apfs", true)
	base := realTemp(t)
	from := filepath.Join(base, "One")
	write(t, filepath.Join(base, "cfg", "server.toml"), "[server]\ndata_dir = \""+from+"\" # chosen\n")
	write(t, filepath.Join(from, "postgres", "PG_VERSION"), "17\n")
	write(t, filepath.Join(from, "node", "state.json"), "{}")
	write(t, filepath.Join(from, "state", "database.json"), "{}")
	in := Install{ConfigDir: filepath.Join(base, "cfg"), ConfigPath: filepath.Join(base, "cfg", "server.toml"),
		From: Layout{Data: from, Root: from}, ToLayout: func(t string) Layout { return Layout{Data: t, Root: t} }}
	target := filepath.Join(base, "Two")
	res, err := Move(context.Background(), in, target, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"postgres/PG_VERSION", "node/state.json", "state/database.json"} {
		if !exists(filepath.Join(target, f)) {
			t.Errorf("%s missing", f)
		}
	}
	if exists(from) || len(res.Old) != 1 || !strings.HasPrefix(res.Old[0], from+".moved-") {
		t.Errorf("the old folder was not kept aside whole: %v", res.Old)
	}
	if got := read(t, in.ConfigPath); got != "[server]\ndata_dir = \""+target+"\" # chosen\n" {
		t.Errorf("server.toml:\n%s", got)
	}
}

func TestMoveRefusesTooLittleSpace(t *testing.T) {
	fakeDisk(t, 4096, "apfs", true)
	in, base := macInstall(t)
	target := filepath.Join(base, "Small")
	_, err := Move(context.Background(), in, target, Options{})
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Plan.Problems[0].Code != "target_too_small" {
		t.Fatalf("err %v", err)
	}
	if exists(target) || exists(filepath.Join(in.ConfigDir, JournalFile)) {
		t.Error("a refused move wrote something")
	}
	if read(t, in.ConfigPath) != tomlBody {
		t.Error("server.toml changed")
	}
}

func TestPlanRefusals(t *testing.T) {
	in, base := macInstall(t)
	write(t, filepath.Join(base, "Full", "something"), "x")
	cases := []struct {
		name, target, fs string
		local            bool
		code             string
	}{
		{"relative", "Big/Maktaba", "apfs", true, "target_relative"},
		{"same", in.From.Data, "apfs", true, "target_same"},
		{"inside", filepath.Join(in.From.Root, "elsewhere"), "apfs", true, "target_inside_current"},
		{"around", filepath.Dir(in.From.Root), "apfs", true, "current_inside_target"},
		{"not empty", filepath.Join(base, "Full"), "apfs", true, "target_not_empty"},
		{"exfat", filepath.Join(base, "Stick"), "exfat", true, "target_not_local"},
		{"network", filepath.Join(base, "NAS"), "smbfs", false, "target_not_local"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeDisk(t, 100<<30, c.fs, c.local)
			p := PlanMove(in, c.target)
			if p.OK || p.Problems[0].Code != c.code {
				t.Errorf("plan %+v, want %s", p.Problems, c.code)
			}
		})
	}
	// a tight disk is said, not refused
	fakeDisk(t, 2<<30, "apfs", true)
	p := PlanMove(in, filepath.Join(base, "Tight"))
	if !p.OK || len(p.Warnings) != 1 || p.Warnings[0].Code != "disk_tight" {
		t.Errorf("tight plan %+v %+v", p.Problems, p.Warnings)
	}
}

func TestMoveCutShortResumes(t *testing.T) {
	fakeDisk(t, 100<<30, "apfs", true)
	in, base := macInstall(t)
	target := filepath.Join(base, "Big", "Maktaba")
	ctx, cancel := context.WithCancel(context.Background())
	_, err := Move(ctx, in, target, Options{Progress: func(done, _ uint64) {
		if done > 0 {
			cancel() // the power goes after the first file
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if read(t, in.ConfigPath) != tomlBody {
		t.Fatal("server.toml changed before the copy was whole")
	}
	if got, phase, ok := ReadJournal(in.ConfigDir); !ok || got != target || phase != phaseCopying {
		t.Fatalf("journal %q %q %v", got, phase, ok)
	}
	// meanwhile the server ran from the old place: a file went, one changed
	if err := os.Remove(filepath.Join(in.From.Root, "backups", "maktaba-2026-10-25.dump")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(target, "backups", "maktaba-2026-10-25.dump"), "stale copy")
	write(t, filepath.Join(target, "postgres", "base", "1", "1259"+partSuffix), "half")
	// another target may replace a copy nothing uses yet: the half copy is said
	if p := PlanMove(in, filepath.Join(base, "Other")); !p.OK || len(p.Warnings) == 0 || p.Warnings[0].Code != "partial_copy_left" {
		t.Errorf("another target: %+v %+v", p.Problems, p.Warnings)
	}
	res, err := Move(context.Background(), in, target, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(target, "backups", "maktaba-2026-10-25.dump")) {
		t.Error("a file the source no longer has stayed in the copy")
	}
	if exists(filepath.Join(target, "postgres", "base", "1", "1259"+partSuffix)) {
		t.Error("a part file stayed")
	}
	if read(t, filepath.Join(target, "postgres", "base", "1", "1259")) != strings.Repeat("x", 9000) {
		t.Error("the database file is not whole")
	}
	if !res.OK || !strings.Contains(read(t, in.ConfigPath), target) {
		t.Errorf("not finished: %+v", res)
	}
}

func TestMoveFinishesAfterTheSwitch(t *testing.T) {
	fakeDisk(t, 100<<30, "apfs", true)
	in, base := macInstall(t)
	target := filepath.Join(base, "Big", "Maktaba")
	// the copy is verified and server.toml switched, then the run is cut before the
	// old folders were kept aside
	dataStays := filepath.Join(in.From.Root, "data")
	_, err := Move(context.Background(), in, target, Options{Busy: func(Layout) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	// act out the cut: put one old folder back and the journal at "switched"
	old := ""
	for _, o := range mustResult(t, in).Old {
		if strings.HasPrefix(o, dataStays) {
			old = o
		}
	}
	if err := os.Rename(old, dataStays); err != nil {
		t.Fatal(err)
	}
	j := journal{Target: target, From: in.From, To: Layout{Data: target, Root: target}, Stamp: "20261026-093000",
		Phase: phaseSwitched}
	if err := writeJSON(filepath.Join(in.ConfigDir, JournalFile), j); err != nil {
		t.Fatal(err)
	}
	// once server.toml may point at it, no other target is taken until it is finished
	if p := PlanMove(InstallFrom(in, target), filepath.Join(base, "Other")); p.OK || p.Problems[0].Code != "move_unfinished" {
		t.Errorf("another target after the switch: %+v", p.Problems)
	}
	// run again with the install as server.toml now says it is
	res, err := Move(context.Background(), InstallFrom(in, target), target, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if exists(dataStays) || !exists(dataStays+".moved-20261026-093000") || len(res.Old) == 0 {
		t.Errorf("the old data was not kept aside: %v", res.Old)
	}
}

// realTemp is a temp dir with its links resolved (macOS's /var is /private/var): a
// move's target is resolved so.
func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustResult(t *testing.T, in Install) Result {
	t.Helper()
	r, ok := ReadResult(in.ConfigDir)
	if !ok {
		t.Fatal("no result")
	}
	return r
}

func TestMoveStopsWhenTheDatabaseDoesNotStartOnTheCopy(t *testing.T) {
	fakeDisk(t, 100<<30, "apfs", true)
	in, base := macInstall(t)
	target := filepath.Join(base, "Big", "Maktaba")
	_, err := Move(context.Background(), in, target, Options{
		VerifyPostgres: func(context.Context, string) error { return errors.New("postgres: bad checkpoint") }})
	if err == nil || !strings.Contains(err.Error(), "bad checkpoint") {
		t.Fatalf("err %v", err)
	}
	if read(t, in.ConfigPath) != tomlBody || !exists(filepath.Join(in.From.Root, "postgres", "PG_VERSION")) {
		t.Error("the install changed although its copy failed")
	}
	// fixed (a retry): the move goes on from the copy it made
	if _, err := Move(context.Background(), in, target, Options{
		VerifyPostgres: func(context.Context, string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
}

func TestMoveRefusesWhileTheServerRuns(t *testing.T) {
	fakeDisk(t, 100<<30, "apfs", true)
	in, base := macInstall(t)
	target := filepath.Join(base, "Big", "Maktaba")
	_, err := Move(context.Background(), in, target, Options{Busy: func(Layout) error { return errors.New("Maktaba runs") }})
	if err == nil || exists(filepath.Join(in.ConfigDir, JournalFile)) || exists(target) {
		t.Fatalf("err %v", err)
	}
}

func TestSuggestTakesTheRoomiestSuitableDisk(t *testing.T) {
	disks := []Disk{
		{Mount: "/", FreeBytes: 50 << 30, Suitable: true, SuggestedDir: "/home/Maktaba"},
		{Mount: "/Volumes/Big", FreeBytes: 900 << 30, Suitable: true, SuggestedDir: "/Volumes/Big/Maktaba"},
		{Mount: "/Volumes/Stick", FreeBytes: 2000 << 30, Suitable: false, SuggestedDir: "/Volumes/Stick/Maktaba"},
	}
	if got := Suggest(disks, "/default"); got != "/Volumes/Big/Maktaba" {
		t.Errorf("suggested %s", got)
	}
	if got := Suggest(nil, "/default"); got != "/default" {
		t.Errorf("suggested %s with no disk", got)
	}
}
