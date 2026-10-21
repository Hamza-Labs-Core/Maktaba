package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
)

// `maktaba-server setup` on a machine whose server runs from an app payload
// and has no window (Epic 39 story 39.6, specs/epics/39-linux-app/headless.md):
// the desktop app's setup screens as terminal prompts. Each screen is one
// `setup --json --step <name>` run as its own process, exactly as the app runs
// it (specs/epics/36-mac-app/contract-setup.md), so the terminal and the
// window share one implementation. With --answers the inputs come from a JSON
// object keyed by step instead of the prompts (automation, CI).

// appSteps are the setup steps in screen order (contract-setup.md).
var appSteps = []string{"check", "admin", "libraries", "models", "plex", "venv", "finish"}

// stepEvent is one NDJSON line of a step (setupEvent, plus progress fields).
type stepEvent struct {
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Field   string          `json:"field"`
	Stage   string          `json:"stage"`
	Done    int64           `json:"done"`
	Total   int64           `json:"total"`
}

// stepFailure is a step's error event (or a run that gave no result).
type stepFailure struct {
	Code, Message, Field string
}

func (f *stepFailure) Error() string { return f.Code + ": " + f.Message }

// stepRunner runs one step with its input; progress gets each progress line.
type stepRunner func(ctx context.Context, step string, input any, progress func(stepEvent)) (json.RawMessage, error)

// execStepRunner runs `<self> setup --json --step <step>`: the input on stdin
// (secrets never in argv or the environment), NDJSON on stdout.
func execStepRunner(self string) stepRunner {
	return func(ctx context.Context, step string, input any, progress func(stepEvent)) (json.RawMessage, error) {
		in, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, self, "setup", "--json", "--step", step)
		cmd.Stdin = strings.NewReader(string(in))
		cmd.Stderr = io.Discard
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		res, fail := readStepEvents(out, progress)
		werr := cmd.Wait()
		switch {
		case fail != nil:
			return nil, fail
		case res == nil || werr != nil:
			return nil, &stepFailure{Code: "no_result", Message: fmt.Sprintf("setup step %s gave no result (%v)", step, werr)}
		}
		return res, nil
	}
}

// readStepEvents reads a step's NDJSON: progress lines to progress, then its
// result data or its error. Lines that are not events are skipped.
func readStepEvents(r io.Reader, progress func(stepEvent)) (json.RawMessage, *stepFailure) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var res json.RawMessage
	var fail *stepFailure
	for sc.Scan() {
		var ev stepEvent
		if json.Unmarshal([]byte(strings.TrimSpace(sc.Text())), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "progress":
			if progress != nil {
				progress(ev)
			}
		case "result":
			if res == nil && fail == nil {
				res = ev.Data
				if res == nil {
					res = json.RawMessage("null")
				}
			}
		case "error":
			if res == nil && fail == nil {
				fail = &stepFailure{Code: ev.Code, Message: ev.Message, Field: ev.Field}
			}
		}
	}
	return res, fail
}

// asker is the wizard's terminal (tests replace it).
type asker interface {
	line(question, def string) string
	yesNo(question string, def bool) bool
	secret(question string) string
}

// appSetup is one run of the terminal wizard.
type appSetup struct {
	run     stepRunner
	ask     asker
	out     io.Writer
	answers map[string]json.RawMessage
	// afterFinish installs and starts the service (Linux: the systemd user unit).
	afterFinish func(ctx context.Context) error
	// pollEvery is how often a Plex PIN is checked.
	pollEvery time.Duration
}

func (a *appSetup) printf(format string, args ...any) { fmt.Fprintf(a.out, format, args...) }

// progress prints a step's progress lines, at most one every two seconds
// (and every new stage).
func (a *appSetup) progress() func(stepEvent) {
	var last time.Time
	var stage string
	return func(ev stepEvent) {
		if ev.Stage == stage && time.Since(last) < 2*time.Second {
			return
		}
		stage, last = ev.Stage, time.Now()
		if ev.Total > 0 {
			a.printf("  %s (%d%%)\n", ev.Message, ev.Done*100/ev.Total)
		} else {
			a.printf("  %s\n", ev.Message)
		}
	}
}

// step runs one step: with the answers file's input when it has one (no
// retry), else with ask's input, asking again after an error the user can fix.
func (a *appSetup) step(ctx context.Context, name string, ask func(field string) any) (json.RawMessage, error) {
	if raw, ok := a.answers[name]; ok {
		res, err := a.run(ctx, name, raw, a.progress())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return res, nil
	}
	field := ""
	for attempt := 0; ; attempt++ {
		res, err := a.run(ctx, name, ask(field), a.progress())
		var f *stepFailure
		if err == nil {
			return res, nil
		}
		if !errors.As(err, &f) || f.Field == "" || attempt >= 4 {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		a.printf("  %s\n", f.Message)
		field = f.Field
	}
}

// Run walks the steps in screen order.
func (a *appSetup) Run(ctx context.Context) error {
	a.printf("Maktaba setup: this computer runs the server (no window needed).\n\n")
	if res, err := a.run(ctx, "status", map[string]any{}, nil); err == nil {
		var st struct {
			Configured bool   `json:"configured"`
			WebURL     string `json:"web_url"`
		}
		if json.Unmarshal(res, &st) == nil && st.Configured {
			a.printf("This computer is set up already (%s).\n", st.WebURL)
			if a.afterFinish != nil && (len(a.answers) > 0 || a.ask.yesNo("Install and start its service again", true)) {
				return a.afterFinish(ctx)
			}
			return nil
		}
	}

	res, err := a.step(ctx, "check", func(string) any { return map[string]any{} })
	if err != nil {
		return err
	}
	if !a.showCheck(res) {
		return errors.New("this computer cannot run the server (see above)")
	}

	if _, err := a.step(ctx, "admin", func(field string) any {
		user := a.ask.line("Administrator's user name", "admin")
		for {
			pw := a.ask.secret("Password")
			if pw == a.ask.secret("Password again") && pw != "" {
				return map[string]any{"username": user, "password": pw}
			}
			a.printf("  The passwords differ, or are empty.\n")
		}
	}); err != nil {
		return err
	}

	if _, err := a.step(ctx, "libraries", func(string) any {
		roots := splitList(a.ask.line("Library folders (comma-separated)", ""))
		a.printf("Paths the other machines report, as they are on this computer (e.g. /mnt/media => /srv/media).\n")
		var pm []map[string]string
		for _, pair := range splitList(a.ask.line("NAS path map (comma-separated, empty for none)", "")) {
			from, to, ok := strings.Cut(pair, "=>")
			if ok {
				pm = append(pm, map[string]string{"from": strings.TrimSpace(from), "to": strings.TrimSpace(to)})
			}
		}
		return map[string]any{"roots": roots, "path_map": pm}
	}); err != nil {
		return err
	}

	if _, err := a.step(ctx, "models", func(string) any {
		a.printf("The second listen uses a gated model: a Hugging Face token of an account that accepted its licence,\n" +
			"or the copy your home published on the share.\n")
		if tok := a.ask.secret("Hugging Face token (empty: use the share's copy)"); tok != "" {
			return map[string]any{"source": "huggingface", "hf_token": tok}
		}
		return map[string]any{"source": "nas", "path": a.ask.line("Folder of the share's models copy", "")}
	}); err != nil {
		return err
	}

	if err := a.plex(ctx); err != nil {
		return err
	}

	a.printf("Building the pipeline's Python environment (downloads a few GB the first time).\n")
	if _, err := a.step(ctx, "venv", func(string) any { return map[string]any{} }); err != nil {
		return err
	}
	res, err = a.step(ctx, "finish", func(string) any { return map[string]any{} })
	if err != nil {
		return err
	}
	var fin struct {
		WebURL string `json:"web_url"`
	}
	_ = json.Unmarshal(res, &fin)
	if a.afterFinish != nil {
		if err := a.afterFinish(ctx); err != nil {
			return err
		}
	}
	a.printf("\nMaktaba is set up. Open %s (or this computer's address on your network).\n", fin.WebURL)
	return nil
}

// showCheck prints what will run and the warnings; false when the computer
// cannot run the server.
func (a *appSetup) showCheck(res json.RawMessage) bool {
	var c struct {
		OK       bool     `json:"ok"`
		Warnings []string `json:"warnings"`
		Problems []string `json:"problems"`
		Runs     []struct {
			Role string `json:"role"`
			What string `json:"what"`
		} `json:"runs"`
		DataDir string `json:"data_dir"`
	}
	if json.Unmarshal(res, &c) != nil {
		return false
	}
	a.printf("What will run (data in %s):\n", c.DataDir)
	for _, r := range c.Runs {
		a.printf("  %-10s %s\n", r.Role, r.What)
	}
	for _, w := range c.Warnings {
		a.printf("  Note: %s\n", w)
	}
	for _, p := range c.Problems {
		a.printf("  Cannot run: %s\n", p)
	}
	a.printf("\n")
	return c.OK
}

// plex links Plex with the PIN flow (optional): the code is shown, then
// polled until it is entered at plex.tv/link or expires.
func (a *appSetup) plex(ctx context.Context) error {
	if raw, ok := a.answers["plex"]; ok {
		_, err := a.run(ctx, "plex", raw, nil)
		return wrapStep("plex", err)
	}
	if !a.ask.yesNo("Link Plex now (Live TV in Plex)", false) {
		_, err := a.run(ctx, "plex", map[string]any{"action": "skip"}, nil)
		return wrapStep("plex", err)
	}
	res, err := a.run(ctx, "plex", map[string]any{"action": "pin"}, nil)
	if err != nil {
		return wrapStep("plex", err)
	}
	var pin struct {
		PinID   int64  `json:"pin_id"`
		Code    string `json:"code"`
		LinkURL string `json:"link_url"`
	}
	if err := json.Unmarshal(res, &pin); err != nil {
		return fmt.Errorf("plex: %w", err)
	}
	a.printf("Open %s and enter the code %s (waiting for it).\n", pin.LinkURL, pin.Code)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(a.pollEvery):
		}
		res, err := a.run(ctx, "plex", map[string]any{"action": "poll", "pin_id": pin.PinID}, nil)
		if err != nil {
			return wrapStep("plex", err)
		}
		var st struct {
			Linked bool `json:"linked"`
		}
		if json.Unmarshal(res, &st) == nil && st.Linked {
			a.printf("  Plex is linked.\n")
			return nil
		}
	}
}

func wrapStep(name string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// readAnswers reads --answers (a file) or --answers-fd (an open descriptor:
// secrets never written to disk): a JSON object of step inputs.
func readAnswers(file string, fd int) (map[string]json.RawMessage, error) {
	var r io.Reader
	switch {
	case file != "":
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	case fd > 0:
		r = os.NewFile(uintptr(fd), "answers")
	default:
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return nil, fmt.Errorf("answers: %w", err)
	}
	for k := range m {
		if k != "plex" && !contains(appSteps, k) {
			return nil, fmt.Errorf("answers: no setup step %q (%s)", k, strings.Join(appSteps, ", "))
		}
	}
	return m, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// runAppSetupCmd is `setup` run from an app payload: the terminal wizard over
// this binary's own setup steps, then (Linux) the runtime copy and the user
// service, which the desktop app does itself.
func runAppSetupCmd(answersFile string, answersFD int) error {
	answers, err := readAnswers(answersFile, answersFD)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	a := &appSetup{run: execStepRunner(self), ask: newPrompter(), out: os.Stdout, answers: answers,
		pollEvery: 2 * time.Second}
	if runtimeGOOS == "linux" {
		a.afterFinish = func(ctx context.Context) error { return headlessService(ctx, os.Stdout) }
	}
	return a.Run(context.Background())
}

// runtimeGOOS is runtime.GOOS (tests replace it).
var runtimeGOOS = goruntime.GOOS

// headlessService puts this payload in the runtime (owner: the machine
// itself, it updates through its releases), points `current` at it, and
// installs and starts the systemd user unit with linger, asking for the
// password on this terminal where polkit wants one.
func headlessService(ctx context.Context, out io.Writer) error {
	payload := payloadOfThisBinary()
	if payload == "" {
		return errors.New("this maktaba-server is not in a payload")
	}
	d := newRuntimeDeps()
	fmt.Fprintf(out, "Copying the server into %s\n", d.rt.Root)
	dir, _, err := d.rt.Install(ctx, payload, nil)
	if err != nil {
		return err
	}
	if err := d.rt.SetOwner("headless"); err != nil {
		return err
	}
	if _, err := d.rt.Activate(ctx, filepath.Base(dir), d.service); err != nil {
		return err
	}
	sd := newServiceDeps()
	if err := sd.manager.Install(ctx, sd.unit, true, false); err != nil {
		return err
	}
	fmt.Fprintf(out, "Started the service %s.\n", sd.unit.Path())
	if err := sd.manager.Linger(ctx, true); err != nil {
		fmt.Fprintf(out, "It runs while you are logged in. To start it at boot, an administrator can run: loginctl enable-linger %s (%v)\n",
			os.Getenv("USER"), err)
	} else {
		fmt.Fprintf(out, "It starts at boot (linger).\n")
	}
	return nil
}
