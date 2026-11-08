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
