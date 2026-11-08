//go:build windows

package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbin"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// stageE2ECase is one job of the end to end: a first-party stage on the fixture video of
// one library (a local folder or an SMB share).
type stageE2ECase struct {
	Name    string         `json:"name"`
	Stage   string         `json:"stage"`
	Library string         `json:"library"`
	File    string         `json:"file"`
	Payload map[string]any `json:"payload,omitempty"`
	group   string
}

// Story 54.23 on Windows, end to end with Maktaba's own stages (CI's runner-stages job,
// never a developer's or the owner's machine). Everything but the worker's loop is the
// release's: the supervisor (this package's Supervisor) runs the bundled Postgres
// (BuildPostgres, a restricted token) and the shared runner groups as it builds them
// (BuildPluginRunners: on demand, through the launcher's runner-sandbox-run, each in its
// group's AppContainer and Job Object with its grants and its share broker, reached through
// the supervisor's pipes); the plugin API's statements and tokens are api/tools/plugin-api-dev
// (the API's executor, the plugins' Postgres roles); the worker is the pipeline's own
// dispatch (testdata/runner_stages_worker.py: dispatch_job, native.choose, native.run_native).
//
// Two stages that capture a tool's output, each on a local library and on one on a share:
// thumb_small (ffmpeg, the JPEG on its stdout) and convert_mp4's dry run (ffprobe's JSON on
// its stdout, of an mp4 whose index is at its end). Each job must have run in its runner
// (ranInRunner: the plugin chosen, run_native ok, no fallback, a token, the runner's own
// log line) and made the right output (smallThumbnailProblems, convertPlanProblems).
func TestFirstPartyStagesRunInTheirRunnersOnWindows(t *testing.T) {
	env := map[string]string{}
	var missing []string
	for _, k := range []string{"MAKTABA_STAGES_E2E_PYTHON", "MAKTABA_PLUGINS_SANDBOX_RUN", "MAKTABA_PLUGIN_API_DEV",
		"MAKTABA_TEST_PG_DOWNLOAD", "MAKTABA_STAGES_E2E_SHARE", "MAKTABA_STAGES_E2E_SHARE_DIR",
		"MAKTABA_STAGES_E2E_FFMPEG", "MAKTABA_STAGES_E2E_FFPROBE"} {
		if env[k] = os.Getenv(k); env[k] == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		if os.Getenv("MAKTABA_STAGES_E2E_REQUIRE") == "1" {
			t.Fatalf("CI must run this test: %v not set", missing)
		}
		t.Skipf("the Windows runner-stages end to end (CI): %v not set", missing)
	}
	python, ffmpeg, ffprobe := env["MAKTABA_STAGES_E2E_PYTHON"], env["MAKTABA_STAGES_E2E_FFMPEG"], env["MAKTABA_STAGES_E2E_FFPROBE"]
	repo, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	logs := os.Getenv("MAKTABA_STAGES_E2E_LOGS")
	if logs == "" {
		logs = t.TempDir()
	}
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "mks")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// the libraries: a local folder and a folder on the share, the same fixture in each
	share := strings.TrimRight(env["MAKTABA_STAGES_E2E_SHARE"], `\`) + `\stages-e2e`
	shareDir := filepath.Join(env["MAKTABA_STAGES_E2E_SHARE_DIR"], "stages-e2e")
	libraries := map[string]string{"local": filepath.Join(dir, "library"), "share": share}
	for _, d := range []string{filepath.Join(dir, "library", "show"), filepath.Join(shareDir, "show")} {
		makeStageFixture(t, ffmpeg, d)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shareDir) })
	if first, err := moovFirst(filepath.Join(shareDir, "show", "moov_end.mp4")); err != nil || first {
		t.Fatalf("the fixture's index must be at its end: moov first %v, %v", first, err)
	}
	var cases []stageE2ECase
	for _, lib := range []string{"local", "share"} {
		cases = append(cases,
			stageE2ECase{Name: "thumb_small/" + lib, Stage: "thumb_small", Library: lib, File: `show\moov_end.mp4`, group: "light"},
			stageE2ECase{Name: "convert_mp4/" + lib, Stage: "convert_mp4", Library: lib, File: `show\moov_end.mp4`,
				Payload: map[string]any{"dry_run": true}, group: "files"})
	}

	// the folders the stages and the runners' areas name, all in this test's folder
	thumbs := filepath.Join(dir, "cache", "thumbnails")
	for k, v := range map[string]string{
		"MAKTABA_PYTHON":         python,
		"MAKTABA_FFMPEG":         ffmpeg,
		"MAKTABA_FFPROBE":        ffprobe,
		"MAKTABA_THUMBNAIL_ROOT": thumbs,
		"MAKTABA_YT_WORK":        filepath.Join(dir, "cache", "yt"),
		"MAKTABA_CHROMA_DIR":     filepath.Join(dir, "cache", "chroma"),
		"HF_HOME":                filepath.Join(dir, "cache", "hf"),
		"MAKTABA_CONFIG":         filepath.Join(dir, "config", "config.toml"),
	} {
		t.Setenv(k, v)
		if strings.HasPrefix(v, dir) && !strings.HasSuffix(v, ".toml") {
			_ = os.MkdirAll(v, 0o700)
		}
	}
	_ = os.MkdirAll(filepath.Join(dir, "config"), 0o700)

	// the bundled Postgres, as the supervisor runs it
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pin, ok := pgbin.Here()
	if !ok {
		t.Fatal("no pinned Postgres for windows/amd64")
	}
	home, err := pgbin.Ensure(ctx, env["MAKTABA_TEST_PG_DOWNLOAD"], pin, pgbin.HTTPFetch(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	b := pgbundle.Bundle{Home: home, Data: filepath.Join(dir, "pg"), Keys: filepath.Join(dir, "keys"),
		Backups: filepath.Join(dir, "backups"), Port: freePort(t)}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	dsn, err := b.URL() // the app role's, as every child gets it (never logged)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{}
	cfg.Server.DataDir = filepath.Join(dir, "data")
	cfg.Database.URL = dsn
	pg, err := BuildPostgres(b)
	if err != nil {
		t.Fatal(err)
	}
	built, err := BuildPluginRunners(Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	procs := []Process{pg}
	for _, p := range built {
		if containsString(RunnerGroups, p.Instance) { // the shared groups, on demand
			procs = append(procs, p)
		}
	}
	supLog, err := os.Create(filepath.Join(logs, "supervisor.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer supLog.Close()
	spool := func(role Role, instance string) (*os.File, error) {
		name := string(role)
		if instance != "" {
			name += "-" + instance
		}
		return os.OpenFile(filepath.Join(logs, name+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	}
	s := New(procs, DefaultPolicy(), WithLog(supLog), WithSpool(spool))
	// the components reconciler's part (core.plugin_runners is on while a component with a
	// first-party plugin is): each shared group may start, and starts when the worker asks
	for _, p := range procs[1:] {
		if err := s.Allow(RolePluginRunner, p.Instance, true); err != nil {
			t.Fatal(err)
		}
	}
	stopped := make(chan error, 1)
	go func() { stopped <- s.Run(ctx, 30*time.Second) }()
	defer func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(60 * time.Second):
			t.Log("the supervisor did not stop within a minute")
		}
	}()
	defer func() {
		if t.Failed() {
			tailLogs(t, logs)
		}
	}()
	waitReady(t, b)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// the plugin API's trusted routes and token mint, on the runners' API socket
	apiLog, err := os.Create(filepath.Join(logs, "plugin-api.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiLog.Close()
	api := exec.CommandContext(ctx, env["MAKTABA_PLUGIN_API_DEV"], "-dsn", dsn, "-socket", PluginsPaths(cfg).API, //nolint:gosec // CI's build
		"-bundle", filepath.Join(repo, "pipeline", "src", "maktaba_pipeline", "plugins", "data", "firstparty.json"))
	api.Stderr = apiLog
	apiOut, _ := api.StdoutPipe()
	if err := api.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = api.Process.Kill(); _ = api.Wait() }()
	ready, err := bufio.NewReader(apiOut).ReadString('\n')
	if err != nil || !strings.HasPrefix(ready, "ready ") {
		t.Fatalf("plugin-api-dev: %q %v", ready, err)
	}
	mint := strings.TrimSpace(strings.TrimPrefix(ready, "ready "))

	// the worker
	report := filepath.Join(logs, "report.json")
	conf, _ := json.Marshal(map[string]any{
		"dsn": dsn, "pipeline": filepath.Join(repo, "pipeline"), "out": report,
		"groups": []string{"light", "files"}, "wait_sec": 300, "libraries": libraries, "cases": cases,
	})
	confPath := filepath.Join(dir, "worker.json")
	if err := os.WriteFile(confPath, conf, 0o600); err != nil {
		t.Fatal(err)
	}
	workerLog, err := os.Create(filepath.Join(logs, "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer workerLog.Close()
	wctx, wcancel := context.WithTimeout(ctx, 15*time.Minute)
	defer wcancel()
	worker := exec.CommandContext(wctx, python, filepath.Join(repo, "cmd", "maktaba-server", "internal", "supervisor", "testdata", "runner_stages_worker.py"), confPath) //nolint:gosec // CI's interpreter
	worker.Env = append(os.Environ(),
		"MAKTABA_API_URL=http://"+mint,
		"MAKTABA_SUPERVISOR_URL="+srv.URL,
		EnvRunnerPipes+"="+RunnerPipePrefix(),
		"MAKTABA_RUNNER_DIR="+RunnerDir(cfg),
		"MAKTABA_DATA_DIR="+cfg.Server.DataDir,
		"PYTHONUTF8=1",
	)
	worker.Stdout, worker.Stderr = workerLog, workerLog
	if err := worker.Run(); err != nil {
		t.Fatalf("the worker: %v (worker.log)", err)
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Cases   []stageReport `json:"cases"`
		Runners map[string]struct {
			Hello      map[string]any `json:"hello"`
			Seconds    float64        `json:"seconds"`
			AskRefused bool           `json:"ask_refused"`
		} `json:"runners"`
		MigrationProblems []string `json:"migration_problems"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("the worker's report: %v", err)
	}
	if len(rep.MigrationProblems) > 0 {
		t.Logf("migrations (tolerated, before slot 87): %v", rep.MigrationProblems)
	}
	for g, r := range rep.Runners {
		if r.Hello == nil {
			why := "never answered the worker"
			if r.AskRefused {
				why = "was refused by the supervisor when the worker asked for it (POST /runners/" + g + "/start)"
			}
			t.Fatalf("the %s runner %s after %.0fs; the supervisor sees %s (plugin-runner-%s.log)",
				g, why, r.Seconds, runnerStates(s), g)
		}
		t.Logf("runner %s up in %.1fs: pid %v, stages served %v", g, r.Seconds, r.Hello["pid"], r.Hello["stages"])
	}
	runnerLogs := map[string]string{}
	for _, g := range []string{"light", "files"} {
		b, _ := os.ReadFile(filepath.Join(logs, "plugin-runner-"+g+".log"))
		runnerLogs[g] = string(b)
		if !strings.Contains(runnerLogs[g], `"sandbox": "full"`) {
			t.Errorf("the %s runner did not report its full sandbox", g)
		}
		if !strings.Contains(runnerLogs[g], "plugin_runner_files_brokered") {
			t.Errorf("the %s runner's share was not brokered", g)
		}
	}
	byName := map[string]stageReport{}
	for _, r := range rep.Cases {
		byName[r.Name] = r
	}
	for _, c := range cases {
		r, ok := byName[c.Name]
		if !ok {
			t.Errorf("%s: not in the worker's report", c.Name)
			continue
		}
		problems := ranInRunner(c.group, r, runnerLogs[c.group])
		summary := r.summary()
		switch c.Stage {
		case "thumb_small":
			lib := libraries[c.Library]
			if c.Library == "share" {
				lib = shareDir // the share's own folder: nothing may land there either
			}
			problems = append(problems, smallThumbnailProblems(summary, thumbs, lib, "moov_end.jpg", 320, 180)...)
		case "convert_mp4":
			problems = append(problems, convertPlanProblems(summary, "remux",
				[]map[string]any{{"lang": "ara", "codec": "mov_text"}})...)
		}
		for _, p := range problems {
			t.Errorf("%s (job %d): %s", c.Name, r.JobID, p)
		}
		line := ""
		for _, l := range runnerJobLines(runnerLogs[c.group]) {
			if l.JobID == r.JobID {
				line = fmt.Sprintf("runner %s logged job %d %s %s in %.0f ms", l.Group, l.JobID, l.Stage, l.Status, l.MS)
			}
		}
		out, _ := json.Marshal(summary)
		t.Logf("case %s: job %d, choice plugin, run_native %v, fallbacks %v, tokens %d, state %v; %s; output %s",
			c.Name, r.JobID, runStatuses(r), r.Fallbacks, r.Invocations, r.Job["state"], line, out)
	}
}

// runnerStates is how the supervisor sees each runner group (the first thing to read when
// one never came up: blocked by its component, never wanted, crashed, restarting).
func runnerStates(s *Supervisor) string {
	var out []string
	for _, c := range s.Snapshot().Processes {
		if c.Role == RolePluginRunner {
			out = append(out, fmt.Sprintf("%s: %s pid %d wanted %v blocked %v", c.Instance, c.State, c.PID, c.Wanted, c.Blocked))
		}
	}
	return strings.Join(out, "; ")
}

func runStatuses(r stageReport) []string {
	var out []string
	for _, x := range r.Runs {
		out = append(out, fmt.Sprintf("%s in %.2fs", x.Status, x.Seconds))
	}
	return out
}

// makeStageFixture writes show\moov_end.mp4 (H.264, AAC and an Arabic mov_text subtitle,
// 12 s, its index at its end: ffmpeg's default) and its downloaded thumbnail beside it,
// show\moov_end.jpg (1280x720), into folder.
func makeStageFixture(t *testing.T, ffmpeg, folder string) {
	t.Helper()
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	srt := filepath.Join(folder, "..", "sub.srt")
	_ = os.WriteFile(srt, []byte("1\r\n00:00:01,000 --> 00:00:04,000\r\nبسم الله\r\n"), 0o600)
	defer os.Remove(srt)
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(ffmpeg, append([]string{"-v", "error", "-y"}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // CI's ffmpeg on the test's files
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
		}
	}
	run("-f", "lavfi", "-i", "testsrc=duration=12:size=640x360:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:duration=12",
		"-i", srt, "-map", "0:v", "-map", "1:a", "-map", "2:s", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac",
		"-c:s", "mov_text", "-metadata:s:s:0", "language=ara", filepath.Join(folder, "moov_end.mp4"))
	run("-f", "lavfi", "-i", "testsrc=size=1280x720", "-frames:v", "1", "-update", "1", filepath.Join(folder, "moov_end.jpg"))
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitReady(t *testing.T, b pgbundle.Bundle) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		err := pgbundle.Ready(context.Background(), b.Addr(), pgbundle.AppRole, pgbundle.AppDatabase)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the bundled Postgres never answered: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// tailLogs prints the end of every log the end to end kept (the CI artifact has them all).
func tailLogs(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if len(lines) > 60 {
			lines = lines[len(lines)-60:]
		}
		t.Logf("---- %s (last %d lines) ----\n%s", e.Name(), len(lines), strings.Join(lines, "\n"))
	}
}
