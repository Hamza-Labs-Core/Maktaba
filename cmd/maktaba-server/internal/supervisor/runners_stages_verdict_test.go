package supervisor

// The verdict of the first-party stages' Windows end to end (runners_windows_stages_e2e_test.go):
// what proves that a job ran in its runner and not in the worker, and that the stage made
// the right output. Portable, so its own tests (below) run on every system: a fallback to
// the worker must fail the end to end, never pass it quietly.

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// stageReport is testdata/runner_stages_worker.py's record of one case.
type stageReport struct {
	Name        string         `json:"name"`
	Stage       string         `json:"stage"`
	JobID       int64          `json:"job_id"`
	Error       string         `json:"error"`
	Seconds     float64        `json:"seconds"`
	Job         map[string]any `json:"job"`
	Invocations int            `json:"invocations"`
	Choices     []struct {
		Stage string `json:"stage"`
		Impl  string `json:"impl"`
		Why   string `json:"why"`
	} `json:"choices"`
	Runs []struct {
		JobID   int64   `json:"job_id"`
		Status  string  `json:"status"`
		Error   string  `json:"error"`
		Seconds float64 `json:"seconds"`
	} `json:"runs"`
	Fallbacks map[string]int `json:"fallbacks"`
}

// summary is the job's output_summary (a JSON object, stored as JSON or as its text).
func (r stageReport) summary() map[string]any {
	switch v := r.Job["output_summary"].(type) {
	case map[string]any:
		return v
	case string:
		var m map[string]any
		_ = json.Unmarshal([]byte(v), &m)
		return m
	}
	return nil
}

// runnerJobLine is the runner's own record of a job (maktaba_plugin_runner.server.job_line).
type runnerJobLine struct {
	Event  string  `json:"event"`
	Group  string  `json:"group"`
	Stage  string  `json:"stage"`
	JobID  int64   `json:"job_id"`
	Status string  `json:"status"`
	MS     float64 `json:"ms"`
	// Broker: what the launcher's broker answered for the job (winfiles.take_counts:
	// "stat", "open", "stat_missing", "open_refused", "unrouted_refused", ...)
	Broker map[string]int `json:"broker"`
	// Missing: the first paths the job's look-ups found missing (winfiles.take_missing)
	Missing []string `json:"missing"`
}

// runnerJobLines are the job lines of a runner's log (its spool: the launcher's lines and
// the runner's, one JSON object or plain text per line).
func runnerJobLines(log string) []runnerJobLine {
	var out []runnerJobLine
	sc := bufio.NewScanner(strings.NewReader(log))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var l runnerJobLine
		if json.Unmarshal([]byte(line), &l) == nil && l.Event == "plugin_runner_job" {
			out = append(out, l)
		}
	}
	return out
}

// ranInRunner names every sign that the case's job did not run in its runner group: a
// choice other than the plugin (runner_not_serving_here, role_unusable, an in-process
// stage), a run_native that did not answer ok (sandbox_refused, unregistered, a lost
// runner), a fallback the worker counted, no token minted for it, a job that did not end
// done, and no line in the runner's own log saying it ran the job and it ended ok.
func ranInRunner(group string, r stageReport, runnerLog string) []string {
	var p []string
	if r.Error != "" {
		p = append(p, "the worker's driver failed: "+r.Error)
	}
	if r.JobID == 0 {
		return append(p, "no job ran")
	}
	if len(r.Choices) == 0 {
		p = append(p, "the worker never chose where the job runs (not a first-party stage here)")
	}
	for _, c := range r.Choices {
		if c.Impl != "plugin" {
			p = append(p, fmt.Sprintf("the worker chose %s (%s), not the runner", c.Impl, c.Why))
		}
	}
	switch {
	case len(r.Runs) == 0:
		p = append(p, "run_native was never called: the worker ran the job itself")
	case len(r.Runs) > 1:
		p = append(p, fmt.Sprintf("run_native was called %d times for one job", len(r.Runs)))
	}
	for _, run := range r.Runs {
		if run.Status != "ok" {
			p = append(p, fmt.Sprintf("run_native %s: %s (the worker then ran it itself)", run.Status, run.Error))
		}
	}
	for k, n := range r.Fallbacks {
		p = append(p, fmt.Sprintf("the worker counted a fallback: %s x%d", k, n))
	}
	if r.Invocations < 1 {
		p = append(p, "no plugin token was minted for the job")
	}
	if st, _ := r.Job["state"].(string); st != "done" {
		p = append(p, fmt.Sprintf("the job ended %q (error %v)", st, r.Job["error"]))
	}
	found := false
	for _, l := range runnerJobLines(runnerLog) {
		if l.JobID != r.JobID {
			continue
		}
		found = true
		if l.Group != group || l.Stage != r.Stage || l.Status != "ok" {
			p = append(p, fmt.Sprintf("the runner's log: group %s, stage %s, status %s; want %s, %s, ok",
				l.Group, l.Stage, l.Status, group, r.Stage))
		}
	}
	if !found {
		p = append(p, fmt.Sprintf("the %s runner's log never names job %d", group, r.JobID))
	}
	return p
}

// handedToWorker is the verdict for a job its runner's sandbox refuses by design (a share
// folder the group writes in full is never brokered, spec 54.23 §3): the worker chose the
// runner, the runner ran the stage and refused it (its own log line says sandbox_refused),
// run_native raised SandboxRefused once, the worker counted that one fallback and then ran
// the job itself to done. Anything else fails it: a job that never reached the runner, one
// the runner failed instead of refusing (a refusal hidden as a missing file), or one the
// runner ran on a share it must not reach.
func handedToWorker(group string, r stageReport, runnerLog string) []string {
	var p []string
	if r.Error != "" {
		p = append(p, "the worker's driver failed: "+r.Error)
	}
	if r.JobID == 0 {
		return append(p, "no job ran")
	}
	if len(r.Choices) != 1 || r.Choices[0].Impl != "plugin" {
		p = append(p, fmt.Sprintf("the worker's choices %v: want the runner, once", r.Choices))
	}
	if len(r.Runs) != 1 || r.Runs[0].Status != "raised" || !strings.HasPrefix(r.Runs[0].Error, "SandboxRefused:") {
		p = append(p, fmt.Sprintf("run_native %v: want one SandboxRefused", r.Runs))
	}
	want := r.Stage + ":sandbox_refused"
	if len(r.Fallbacks) != 1 || r.Fallbacks[want] != 1 {
		p = append(p, fmt.Sprintf("the worker's fallbacks %v: want %s x1", r.Fallbacks, want))
	}
	if r.Invocations < 1 {
		p = append(p, "no plugin token was minted for the job")
	}
	if st, _ := r.Job["state"].(string); st != "done" {
		p = append(p, fmt.Sprintf("the job ended %q (error %v)", st, r.Job["error"]))
	}
	found := false
	for _, l := range runnerJobLines(runnerLog) {
		if l.JobID != r.JobID {
			continue
		}
		found = true
		if l.Group != group || l.Stage != r.Stage || l.Status != "sandbox_refused" {
			p = append(p, fmt.Sprintf("the runner's log: group %s, stage %s, status %s; want %s, %s, sandbox_refused",
				l.Group, l.Stage, l.Status, group, r.Stage))
		}
	}
	if !found {
		p = append(p, fmt.Sprintf("the %s runner's log never names job %d", group, r.JobID))
	}
	return p
}

// brokerWants is what each stage's job on a brokered share must have had the broker
// answer at least (the runner's job line counts): thumb_small looks its source up and hands
// ffmpeg a handle; chapter_thumbs hands ffmpeg one handle per chapter; subtitle_gen looks
// the video up and writes and renames its two files by name; convert_mp4's dry run opens
// the file and hands ffprobe a handle. A stage not named wants a stat and an open.
var brokerWants = map[string]map[string]int{
	"thumb_small":    {"stat": 1, "open": 1},
	"chapter_thumbs": {"open": 2},
	"subtitle_gen":   {"stat": 1, "write": 2, "replace": 2},
	"convert_mp4":    {"open": 2},
}

// brokeredProblems checks a job on a brokered share read it through the launcher's broker:
// its runner's job line counts at least the answers want names (nil: a stat and an open),
// and no refusal of any kind.
func brokeredProblems(r stageReport, runnerLog string, want map[string]int) []string {
	if want == nil {
		want = map[string]int{"stat": 1, "open": 1}
	}
	for _, l := range runnerJobLines(runnerLog) {
		if l.JobID != r.JobID {
			continue
		}
		var p []string
		for op, n := range want {
			if l.Broker[op] < n {
				p = append(p, fmt.Sprintf("the share was not read through the broker: its answers %v (want %s x%d at least); looked for and missing: %q", l.Broker, op, n, l.Missing))
			}
		}
		for k, n := range l.Broker {
			if strings.HasSuffix(k, "_refused") {
				p = append(p, fmt.Sprintf("the broker refused %s x%d", strings.TrimSuffix(k, "_refused"), n))
			}
		}
		return p
	}
	return []string{fmt.Sprintf("no runner job line for job %d", r.JobID)}
}

// writeRefusedProblems checks a write the runner tried on a share its group reads only
// (the files group: the broker serves its share for reads): the launcher's broker refused
// the op on that path and said so (plugin_runner_broker_write_refused), and the runner's
// job line counts the refusal (<op>_refused).
func writeRefusedProblems(r stageReport, runnerLog, op, path string) []string {
	var p []string
	said := false
	for _, l := range strings.Split(runnerLog, "\n") {
		if !strings.Contains(l, `"plugin_runner_broker_write_refused"`) {
			continue
		}
		var ev struct {
			Op   string `json:"op"`
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(l)), &ev) == nil && ev.Op == op && strings.EqualFold(ev.Path, path) {
			said = true
		}
	}
	if path == "" || !said {
		p = append(p, fmt.Sprintf("the broker did not log refusing %s of %q (plugin_runner_broker_write_refused)", op, path))
	}
	counted := false
	for _, l := range runnerJobLines(runnerLog) {
		if l.JobID == r.JobID && l.Broker[op+"_refused"] >= 1 {
			counted = true
		}
	}
	if !counted {
		p = append(p, fmt.Sprintf("the runner's job line for job %d counts no %s_refused", r.JobID, op))
	}
	return p
}

// chapterFramesProblems checks chapter_thumbs' output: one frame per chapter (want), each a
// JPEG in a video's folder of the thumbnail cache (root), never on the share.
func chapterFramesProblems(summary map[string]any, root string, want int) []string {
	var p []string
	if n, _ := summary["frames"].(float64); int(n) != want {
		p = append(p, fmt.Sprintf("the job made %v frames, want %d", summary["frames"], want))
	}
	frames, _ := filepath.Glob(filepath.Join(root, "*", "chapter-*.jpg"))
	if len(frames) < want {
		p = append(p, fmt.Sprintf("the thumbnail cache holds %d chapter frames, want %d", len(frames), want))
	}
	for _, f := range frames {
		if info, err := os.Stat(f); err != nil || info.Size() == 0 {
			p = append(p, fmt.Sprintf("%s: empty or unreadable (%v)", f, err))
		}
	}
	return p
}

// subtitleFilesProblems checks subtitle_gen's output: <stem>.<lang>.srt and .vtt written in
// the video's folder (folder: on the share, read outside the sandbox) with the transcript's
// sentences, the summary naming both, and no temporary file left.
func subtitleFilesProblems(summary map[string]any, folder, stem, lang string, texts []string) []string {
	var p []string
	written := map[string]bool{}
	if ws, ok := summary["written"].([]any); ok {
		for _, w := range ws {
			if s, ok := w.(string); ok {
				written[s] = true
			}
		}
	}
	for _, ext := range []string{"srt", "vtt"} {
		name := stem + "." + lang + "." + ext
		if !written[name] {
			p = append(p, fmt.Sprintf("the summary does not name %s as written: %v", name, summary["written"]))
		}
		b, err := os.ReadFile(filepath.Join(folder, name))
		if err != nil {
			p = append(p, fmt.Sprintf("%s is not in %s: %v", name, folder, err))
			continue
		}
		for _, t := range texts {
			if !strings.Contains(string(b), t) {
				p = append(p, fmt.Sprintf("%s lacks %q", name, t))
			}
		}
	}
	if left, _ := filepath.Glob(filepath.Join(folder, "*.maktaba-tmp")); len(left) > 0 {
		p = append(p, fmt.Sprintf("temporary files left: %v", left))
	}
	return p
}

// runnerIdentityProblems checks a runner's spool (the launcher's lines and the runner's)
// for the process the broker serves: the launcher started the venv's base interpreter
// itself (plugin_runner_venv_direct), the runner found itself in that venv (no
// plugin_runner_venv_refused), and the broker turned no client away
// (plugin_runner_broker_client_refused, plugin_runner_broker_refused). A brokered runner
// must also have had its hello answered (plugin_runner_files_brokered with hello).
func runnerIdentityProblems(runnerLog string, brokered bool) []string {
	var p []string
	if logLine(runnerLog, "plugin_runner_venv_direct") == "" {
		p = append(p, "the launcher did not start the venv's base interpreter (no plugin_runner_venv_direct)")
	}
	for _, bad := range []string{"plugin_runner_venv_refused", "plugin_runner_broker_client_refused", "plugin_runner_broker_refused"} {
		if l := logLine(runnerLog, bad); l != "" {
			p = append(p, "its log says "+l)
		}
	}
	if brokered {
		if l := logLine(runnerLog, "plugin_runner_files_brokered"); !strings.Contains(strings.ReplaceAll(l, " ", ""), `"hello":true`) {
			p = append(p, fmt.Sprintf("no broker hello answered: %q", l))
		}
	}
	return p
}

// guardedInWorker is the verdict for a job native.choose keeps in the worker on Windows
// while a library is on a share (#990's guard, MAKTABA_WINDOWS_SHARE_RUNNERS unset): one
// choice, in-process with why windows_share_library, that one fallback counted, no
// run_native, no line for it in the runner's log, and the job done (its output is judged
// apart).
func guardedInWorker(group string, r stageReport, runnerLog string) []string {
	const why = "windows_share_library"
	var p []string
	if r.Error != "" {
		p = append(p, "the worker's driver failed: "+r.Error)
	}
	if r.JobID == 0 {
		return append(p, "no job ran")
	}
	if len(r.Choices) != 1 || r.Choices[0].Impl != "inproc" || r.Choices[0].Why != why {
		p = append(p, fmt.Sprintf("the worker's choices %v: want inproc (%s), once", r.Choices, why))
	}
	if len(r.Runs) != 0 {
		p = append(p, fmt.Sprintf("run_native was called %v: the guard sends no job to the runner", r.Runs))
	}
	want := r.Stage + ":" + why
	if len(r.Fallbacks) != 1 || r.Fallbacks[want] != 1 {
		p = append(p, fmt.Sprintf("the worker's fallbacks %v: want %s x1", r.Fallbacks, want))
	}
	if st, _ := r.Job["state"].(string); st != "done" {
		p = append(p, fmt.Sprintf("the job ended %q (error %v)", st, r.Job["error"]))
	}
	for _, l := range runnerJobLines(runnerLog) {
		if l.JobID == r.JobID {
			p = append(p, fmt.Sprintf("the %s runner logged job %d (%s %s): the guard sends it no job", group, l.JobID, l.Stage, l.Status))
		}
	}
	return p
}

// within reports whether path is dir or inside it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// smallThumbnailProblems checks thumb_small's output: a JPEG of the wanted size made from
// the video's own thumbnail (source), written in the thumbnail cache (root) and never on
// the library's share or folder (library), with the bytes the summary says.
func smallThumbnailProblems(summary map[string]any, root, library, source string, width, height int) []string {
	var p []string
	small, _ := summary["small"].(string)
	if small == "" {
		return []string{fmt.Sprintf("no small thumbnail: %v", summary)}
	}
	if !within(root, small) {
		p = append(p, fmt.Sprintf("%s is not in the thumbnail cache %s", small, root))
	}
	if within(library, small) {
		p = append(p, fmt.Sprintf("%s was written in the library %s", small, library))
	}
	if got, _ := summary["source"].(string); got != source {
		p = append(p, fmt.Sprintf("made from %q, want %q", got, source))
	}
	f, err := os.Open(small)
	if err != nil {
		return append(p, err.Error())
	}
	defer f.Close()
	info, _ := f.Stat()
	if n, ok := summary["bytes"].(float64); !ok || info == nil || int64(n) != info.Size() {
		p = append(p, fmt.Sprintf("the summary says %v bytes, the file has %d", summary["bytes"], info.Size()))
	}
	cfg, format, err := image.DecodeConfig(f)
	if err != nil || format != "jpeg" {
		return append(p, fmt.Sprintf("not a JPEG: %s %v", format, err))
	}
	if cfg.Width != width || cfg.Height != height {
		p = append(p, fmt.Sprintf("%dx%d, want %dx%d", cfg.Width, cfg.Height, width, height))
	}
	return p
}

// convertPlanProblems checks convert_mp4's dry run of a video against the plan wanted:
// what ffprobe read of the file (its container, codecs, subtitle streams and a duration)
// and where its index is, without changing the file.
func convertPlanProblems(summary map[string]any, action string, subtitles []map[string]any) []string {
	var p []string
	if summary == nil {
		return []string{"no summary"}
	}
	if summary["dry_run"] != true || summary["result"] != "skipped" {
		p = append(p, fmt.Sprintf("not a dry run that changed nothing: %v", summary))
	}
	if summary["action"] != action || summary["reason"] != nil {
		p = append(p, fmt.Sprintf("planned %v (%v), want %s", summary["action"], summary["reason"], action))
	}
	got, _ := json.Marshal(summary["subtitles"])
	want, _ := json.Marshal(subtitles)
	if string(got) != string(want) {
		p = append(p, fmt.Sprintf("subtitles %s, want %s", got, want))
	}
	return p
}

// moovFirst reports whether an MP4's top-level moov box comes before its mdat (the
// fixture must have its index at its end).
func moovFirst(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var head [16]byte
	for {
		if _, err := io.ReadFull(f, head[:8]); err != nil {
			return false, fmt.Errorf("%s: no moov or mdat box", path)
		}
		size := int64(binary.BigEndian.Uint32(head[:4]))
		switch string(head[4:8]) {
		case "moov":
			return true, nil
		case "mdat":
			return false, nil
		}
		hdr := int64(8)
		if size == 1 {
			if _, err := io.ReadFull(f, head[8:16]); err != nil {
				return false, err
			}
			size, hdr = int64(binary.BigEndian.Uint64(head[8:16])), 16
		}
		if size < hdr {
			return false, fmt.Errorf("%s: a box of %d bytes", path, size)
		}
		if _, err := f.Seek(size-hdr, io.SeekCurrent); err != nil {
			return false, err
		}
	}
}

// ---- the verdict's own tests ----------------------------------------------------------

func goodStageReport() stageReport {
	r := stageReport{Name: "thumb_small/local", Stage: "thumb_small", JobID: 7, Invocations: 1,
		Job: map[string]any{"state": "done"}, Fallbacks: map[string]int{}}
	r.Choices = append(r.Choices, struct {
		Stage string `json:"stage"`
		Impl  string `json:"impl"`
		Why   string `json:"why"`
	}{"thumb_small", "plugin", ""})
	r.Runs = append(r.Runs, struct {
		JobID   int64   `json:"job_id"`
		Status  string  `json:"status"`
		Error   string  `json:"error"`
		Seconds float64 `json:"seconds"`
	}{7, "ok", "", 0.4})
	return r
}

const goodRunnerLog = `{"event": "plugin_runner_serving", "group": "light", "sandbox": "full"}
plain text from the launcher
{"event": "plugin_runner_job", "service": "plugin-runner", "group": "light", "stage": "thumb_small", "job_id": 7, "status": "ok", "ms": 412.5}
`

func TestTheStageVerdictAcceptsAJobItsRunnerRan(t *testing.T) {
	if p := ranInRunner("light", goodStageReport(), goodRunnerLog); len(p) != 0 {
		t.Fatalf("a job the runner ran: %v", p)
	}
}

// Every way a job can end up in the worker fails the verdict: the end to end can pass only
// when the job really ran in the runner.
func TestTheStageVerdictFailsEveryWorkerFallback(t *testing.T) {
	cases := map[string]struct {
		edit func(*stageReport)
		log  string
		want string
	}{
		"the runner not serving": {func(r *stageReport) {
			r.Choices[0].Impl, r.Choices[0].Why = "inproc", "runner_not_serving_here"
			r.Runs = nil
			r.Fallbacks = map[string]int{"thumb_small:runner_not_serving_here": 1}
		}, goodRunnerLog, "chose inproc (runner_not_serving_here)"},
		"an in-process stage": {func(r *stageReport) {
			r.Choices = nil
			r.Runs = nil
		}, goodRunnerLog, "never chose"},
		"the sandbox refused it": {func(r *stageReport) {
			r.Runs[0].Status, r.Runs[0].Error = "raised", "SandboxRefused: [WinError 5] Access is denied"
			r.Fallbacks = map[string]int{"thumb_small:sandbox_refused": 1}
		}, strings.Replace(goodRunnerLog, `"status": "ok"`, `"status": "sandbox_refused"`, 1), "sandbox_refused"},
		"no token":               {func(r *stageReport) { r.Invocations = 0 }, goodRunnerLog, "no plugin token"},
		"a failed job":           {func(r *stageReport) { r.Job["state"] = "failed" }, goodRunnerLog, `ended "failed"`},
		"no runner log line":     {func(*stageReport) {}, `{"event": "plugin_runner_serving"}`, "never names job 7"},
		"another group's runner": {func(*stageReport) {}, strings.Replace(goodRunnerLog, `"group": "light", "stage"`, `"group": "files", "stage"`, 1), "group files"},
		"the driver failed":      {func(r *stageReport) { r.JobID, r.Error = 0, "Traceback ..." }, goodRunnerLog, "driver failed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := goodStageReport()
			r.Job = map[string]any{"state": "done"}
			c.edit(&r)
			p := ranInRunner("light", r, c.log)
			if !strings.Contains(strings.Join(p, "\n"), c.want) {
				t.Fatalf("problems %q, want one saying %q", p, c.want)
			}
		})
	}
}

// A job the files runner must refuse (a share it writes in full) passes only as the
// designed hand-off: refused in the runner, then run by the worker to done.
func TestTheHandOffVerdict(t *testing.T) {
	refused := func() stageReport {
		r := goodStageReport()
		r.Stage = "convert_mp4"
		r.Job = map[string]any{"state": "done"}
		r.Runs[0].Status, r.Runs[0].Error = "raised", `SandboxRefused: [WinError 5] Access is denied: '\\nas\m\a.mp4'`
		r.Fallbacks = map[string]int{"convert_mp4:sandbox_refused": 1}
		return r
	}
	log := `{"event": "plugin_runner_job", "service": "plugin-runner", "group": "files", "stage": "convert_mp4", "job_id": 7, "status": "sandbox_refused", "ms": 9.0}`
	if p := handedToWorker("files", refused(), log); len(p) != 0 {
		t.Fatalf("a designed hand-off has problems: %q", p)
	}
	cases := map[string]struct {
		edit func(*stageReport)
		log  string
		want string
	}{
		"run in the runner": {func(r *stageReport) {
			r.Runs[0].Status, r.Runs[0].Error = "ok", ""
			r.Fallbacks = nil
		}, strings.Replace(log, "sandbox_refused", "ok", 1), "want one SandboxRefused"},
		"a refusal hidden as a missing file": {func(r *stageReport) {
			r.Runs[0].Error = "StageFailure: file_missing: cannot open"
			r.Fallbacks = nil
			r.Job["state"] = "failed"
		}, strings.Replace(log, "sandbox_refused", "failure", 1), "status failure"},
		"never sent to the runner": {func(r *stageReport) {
			r.Choices[0].Impl, r.Choices[0].Why = "inproc", "runner_not_serving_here"
			r.Runs = nil
		}, log, "want the runner, once"},
		"the worker's run failed": {func(r *stageReport) { r.Job["state"] = "failed" }, log, `ended "failed"`},
		"no runner log line":      {func(*stageReport) {}, `{"event": "plugin_runner_serving"}`, "never names job 7"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := refused()
			c.edit(&r)
			p := handedToWorker("files", r, c.log)
			if !strings.Contains(strings.Join(p, "\n"), c.want) {
				t.Fatalf("problems %q, want one saying %q", p, c.want)
			}
		})
	}
}

// A job on a brokered share passes only when its runner's line shows the broker answered
// its look-up and its tool's open, with nothing refused.
func TestTheBrokeredShareVerdict(t *testing.T) {
	r := goodStageReport()
	line := func(broker string) string {
		return `{"event": "plugin_runner_job", "group": "light", "stage": "thumb_small", "job_id": 7, "status": "ok", "ms": 90.0` + broker + "}"
	}
	if p := brokeredProblems(r, line(`, "broker": {"stat": 2, "stat_missing": 1, "open": 1}`), nil); len(p) != 0 {
		t.Fatalf("a share read through the broker has problems: %q", p)
	}
	for name, c := range map[string]struct{ log, want string }{
		"no broker at all":     {line(""), "not read through the broker"},
		"only missing answers": {line(`, "broker": {"stat_missing": 8}`), "not read through the broker"},
		"no tool open":         {line(`, "broker": {"stat": 1}`), "not read through the broker"},
		"an unrouted call":     {line(`, "broker": {"stat": 1, "open": 1, "unrouted_refused": 1}`), "refused unrouted x1"},
		"no line for the job":  {goodRunnerLog[:strings.Index(goodRunnerLog, "\n")], "no runner job line"},
	} {
		t.Run(name, func(t *testing.T) {
			p := brokeredProblems(r, c.log, nil)
			if !strings.Contains(strings.Join(p, "\n"), c.want) {
				t.Fatalf("problems %q, want one saying %q", p, c.want)
			}
		})
	}
}

// A stage's own wants: subtitle_gen must have written and renamed both files by name.
func TestTheBrokeredShareVerdictTakesTheStagesWants(t *testing.T) {
	r := goodStageReport()
	line := func(broker string) string {
		return `{"event": "plugin_runner_job", "group": "light", "stage": "subtitle_gen", "job_id": 7, "status": "ok", "ms": 9.0, "broker": {` + broker + "}}"
	}
	want := brokerWants["subtitle_gen"]
	if p := brokeredProblems(r, line(`"stat": 1, "open_missing": 2, "write": 2, "replace": 2`), want); len(p) != 0 {
		t.Fatalf("both subtitles written: %q", p)
	}
	if p := brokeredProblems(r, line(`"stat": 1, "write": 1, "replace": 1`), want); len(p) == 0 {
		t.Fatal("one subtitle written passed")
	}
	if p := brokeredProblems(r, line(`"stat": 1, "write": 2, "replace": 2, "write_refused": 1`), want); len(p) == 0 {
		t.Fatal("a refused write passed")
	}
}

// A write the files runner tried on its read-only share passes only when the broker
// refused it and said so, and the runner counted the refusal.
func TestTheWriteRefusedVerdict(t *testing.T) {
	r := goodStageReport()
	const path = `\\localhost\mkstages\stages-e2e\.maktaba\trash\e2e\moov_end.mp4`
	said := `{"event":"plugin_runner_broker_write_refused","op":"remove","path":"\\\\localhost\\mkstages\\stages-e2e\\.maktaba\\trash\\e2e\\moov_end.mp4","detail":"refused"}`
	counted := `{"event": "plugin_runner_job", "group": "files", "stage": "trash_purge", "job_id": 7, "status": "sandbox_refused", "ms": 9.0, "broker": {"stat": 2, "remove_refused": 1}}`
	if p := writeRefusedProblems(r, said+"\n"+counted, "remove", path); len(p) != 0 {
		t.Fatalf("a refused, logged removal: %q", p)
	}
	for name, log := range map[string]string{
		"not logged by the broker":  counted,
		"not counted by the runner": said + "\n" + strings.Replace(counted, `"remove_refused": 1`, `"remove": 1`, 1),
		"another path":              strings.Replace(said, "moov_end", "other", 1) + "\n" + counted,
		"another op":                strings.Replace(said, `"op":"remove"`, `"op":"write"`, 1) + "\n" + counted,
	} {
		if p := writeRefusedProblems(r, log, "remove", path); len(p) == 0 {
			t.Errorf("%s: passed", name)
		}
	}
}

// chapter_thumbs and subtitle_gen's outputs.
func TestTheChapterFramesAndSubtitleVerdicts(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "abc"), 0o700)
	if p := chapterFramesProblems(map[string]any{"frames": 2.0}, root, 2); len(p) == 0 {
		t.Fatal("no frames on disk passed")
	}
	for _, n := range []string{"chapter-1.jpg", "chapter-2.jpg"} {
		_ = os.WriteFile(filepath.Join(root, "abc", n), []byte("jpeg"), 0o600)
	}
	if p := chapterFramesProblems(map[string]any{"frames": 2.0}, root, 2); len(p) != 0 {
		t.Fatalf("two frames: %q", p)
	}
	if p := chapterFramesProblems(map[string]any{"frames": 1.0}, root, 2); len(p) == 0 {
		t.Fatal("a summary of one frame passed")
	}
	folder := t.TempDir()
	sum := map[string]any{"written": []any{"v.ar.srt", "v.ar.vtt"}}
	texts := []string{"بسم الله"}
	if p := subtitleFilesProblems(sum, folder, "v", "ar", texts); len(p) == 0 {
		t.Fatal("no files passed")
	}
	for _, ext := range []string{"srt", "vtt"} {
		_ = os.WriteFile(filepath.Join(folder, "v.ar."+ext), []byte("1\nبسم الله\n"), 0o600)
	}
	if p := subtitleFilesProblems(sum, folder, "v", "ar", texts); len(p) != 0 {
		t.Fatalf("both files: %q", p)
	}
	_ = os.WriteFile(filepath.Join(folder, "v.ar.srt.maktaba-tmp"), nil, 0o600)
	if p := subtitleFilesProblems(sum, folder, "v", "ar", texts); len(p) == 0 {
		t.Fatal("a temporary file left passed")
	}
}

// A job the Windows share-library guard keeps in the worker passes only as that: chosen
// in-process with its why, no runner run, no runner line, done.
func TestTheGuardedVerdict(t *testing.T) {
	guarded := func() stageReport {
		r := goodStageReport()
		r.Job = map[string]any{"state": "done"}
		r.Choices[0].Impl, r.Choices[0].Why = "inproc", "windows_share_library"
		r.Runs = nil
		r.Fallbacks = map[string]int{"thumb_small:windows_share_library": 1}
		return r
	}
	other := `{"event": "plugin_runner_job", "group": "light", "stage": "thumb_small", "job_id": 1, "status": "ok", "ms": 9.0}`
	if p := guardedInWorker("light", guarded(), other); len(p) != 0 {
		t.Fatalf("a guarded job has problems: %q", p)
	}
	for name, c := range map[string]struct {
		edit func(*stageReport)
		log  string
		want string
	}{
		"sent to the runner": {func(r *stageReport) {
			*r = goodStageReport()
			r.Job = map[string]any{"state": "done"}
		}, goodRunnerLog, "want inproc (windows_share_library)"},
		"another why": {func(r *stageReport) {
			r.Choices[0].Why = "runner_not_serving_here"
			r.Fallbacks = map[string]int{"thumb_small:runner_not_serving_here": 1}
		}, other, "want inproc (windows_share_library)"},
		"a runner line for it": {func(*stageReport) {}, goodRunnerLog, "the guard sends it no job"},
		"not done":             {func(r *stageReport) { r.Job["state"] = "failed" }, other, `ended "failed"`},
	} {
		t.Run(name, func(t *testing.T) {
			r := guarded()
			c.edit(&r)
			p := guardedInWorker("light", r, c.log)
			if !strings.Contains(strings.Join(p, "\n"), c.want) {
				t.Fatalf("problems %q, want one saying %q", p, c.want)
			}
		})
	}
}

func writeJPEG(t *testing.T, path string, w, h int) int64 {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, 0, color.RGBA{R: 200, A: 255})
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
	info, _ := f.Stat()
	_ = f.Close()
	return info.Size()
}

func TestTheSmallThumbnailVerdict(t *testing.T) {
	dir := t.TempDir()
	root, lib := filepath.Join(dir, "thumbs"), filepath.Join(dir, "lib")
	good := filepath.Join(root, "abc", "small.jpg")
	n := writeJPEG(t, good, 320, 180)
	sum := func(path string, bytes int64) map[string]any {
		return map[string]any{"small": path, "source": "moov_end.jpg", "bytes": float64(bytes)}
	}
	if p := smallThumbnailProblems(sum(good, n), root, lib, "moov_end.jpg", 320, 180); len(p) != 0 {
		t.Fatalf("a right thumbnail: %v", p)
	}
	wrong := filepath.Join(root, "def", "small.jpg")
	m := writeJPEG(t, wrong, 640, 360)
	onShare := filepath.Join(lib, "show", "small.jpg")
	k := writeJPEG(t, onShare, 320, 180)
	notJPEG := filepath.Join(root, "x", "small.jpg")
	_ = os.MkdirAll(filepath.Dir(notJPEG), 0o700)
	_ = os.WriteFile(notJPEG, []byte("not an image"), 0o600)
	for name, c := range map[string]struct {
		s    map[string]any
		want string
	}{
		"the wrong size":     {sum(wrong, m), "640x360, want 320x180"},
		"in the library":     {sum(onShare, k), "written in the library"},
		"not a JPEG":         {sum(notJPEG, 12), "not a JPEG"},
		"none":               {map[string]any{"small": nil, "reason": "no_thumbnail"}, "no small thumbnail"},
		"another source":     {map[string]any{"small": good, "source": "poster.jpg", "bytes": float64(n)}, `made from "poster.jpg"`},
		"the bytes disagree": {sum(good, n+1), "bytes, the file has"},
	} {
		if p := smallThumbnailProblems(c.s, root, lib, "moov_end.jpg", 320, 180); !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: problems %q, want one saying %q", name, p, c.want)
		}
	}
}

func TestTheConvertPlanVerdict(t *testing.T) {
	subs := []map[string]any{{"lang": "ara", "codec": "mov_text"}}
	good := map[string]any{"dry_run": true, "result": "skipped", "action": "remux", "reason": nil,
		"subtitles": []any{map[string]any{"lang": "ara", "codec": "mov_text"}}}
	if p := convertPlanProblems(good, "remux", subs); len(p) != 0 {
		t.Fatalf("the right plan: %v", p)
	}
	bad := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range good {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for name, s := range map[string]map[string]any{
		"unreadable":        bad("reason", "unreadable"),
		"already faststart": bad("action", "skip"),
		"no subtitles":      bad("subtitles", []any{}),
		"not a dry run":     bad("dry_run", false),
		"no summary":        nil,
	} {
		if p := convertPlanProblems(s, "remux", subs); len(p) == 0 {
			t.Errorf("%s: no problem found", name)
		}
	}
}

func TestMoovFirstReadsTheTopLevelBoxes(t *testing.T) {
	box := func(kind string, body int) []byte {
		b := make([]byte, 8+body)
		binary.BigEndian.PutUint32(b, uint32(8+body))
		copy(b[4:], kind)
		return b
	}
	dir := t.TempDir()
	for name, c := range map[string]struct {
		boxes [][]byte
		first bool
	}{
		"index at the end":   {[][]byte{box("ftyp", 16), box("mdat", 100), box("moov", 20)}, false},
		"index at the start": {[][]byte{box("ftyp", 16), box("moov", 20), box("mdat", 100)}, true},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".mp4")
		var all []byte
		for _, b := range c.boxes {
			all = append(all, b...)
		}
		_ = os.WriteFile(path, all, 0o600)
		got, err := moovFirst(path)
		if err != nil || got != c.first {
			t.Errorf("%s: moov first %v (%v), want %v", name, got, err, c.first)
		}
	}
	if !reflect.DeepEqual(runnerJobLines("no json\n"), []runnerJobLine(nil)) {
		t.Error("a log with no job line has job lines")
	}
}

// The job's output_summary comes back as JSON or as its text (a column stored as text):
// both read the same, and anything else reads as none.
func TestTheStageReportReadsItsSummaryEitherWay(t *testing.T) {
	want := map[string]any{"small": `C:\t\small.jpg`, "bytes": float64(9)}
	for name, v := range map[string]any{
		"json": map[string]any{"small": `C:\t\small.jpg`, "bytes": float64(9)},
		"text": `{"small": "C:\\t\\small.jpg", "bytes": 9}`,
	} {
		r := stageReport{Job: map[string]any{"output_summary": v}}
		if got := r.summary(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	if got := (stageReport{Job: map[string]any{"output_summary": nil}}).summary(); got != nil {
		t.Errorf("no summary: %v", got)
	}
}

// The launcher's identity verdict: the base interpreter started directly and served by the
// broker passes; a launcher's child the broker turned away, a runner outside its venv, or
// no direct start fails.
func TestTheRunnerIdentityVerdict(t *testing.T) {
	good := `{"event":"plugin_runner_venv_direct","group":"light","python":"v\\Scripts\\python.exe","base":"b\\python.exe","venv":"v"}
{"event": "plugin_runner_files_brokered", "service": "plugin-runner", "group": "light", "hello": true}`
	if p := runnerIdentityProblems(good, true); len(p) > 0 {
		t.Fatalf("a good start: %v", p)
	}
	for name, log := range map[string]string{
		"no direct start":     `{"event": "plugin_runner_files_brokered", "group": "light", "hello": true}`,
		"a child turned away": good + "\n" + `{"event":"plugin_runner_broker_client_refused","client":12,"runner":10}`,
		"refused hello":       `{"event":"plugin_runner_venv_direct"}` + "\n" + `{"event": "plugin_runner_broker_refused", "detail": "x"}`,
		"outside its venv":    good + "\n" + `{"event": "plugin_runner_venv_refused", "detail": "x"}`,
		"no hello":            `{"event":"plugin_runner_venv_direct"}` + "\n" + `{"event": "plugin_runner_files_brokered", "group": "light"}`,
	} {
		if p := runnerIdentityProblems(log, true); len(p) == 0 {
			t.Errorf("%s: passed", name)
		}
	}
	if p := runnerIdentityProblems(`{"event":"plugin_runner_venv_direct"}`, false); len(p) > 0 {
		t.Errorf("an unbrokered runner needs no hello: %v", p)
	}
}

// logLine is the first line of log that names event ("" when none does).
func logLine(log, event string) string {
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, `"`+event+`"`) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}
