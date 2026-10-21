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
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
)

// `maktaba-server setup` on a machine whose server runs from an app payload
// and has no window (Epic 39 story 39.6, specs/epics/39-linux-app/headless.md):
// the desktop app's setup screens as terminal prompts, in English or Arabic.
// Each screen is one `setup --json --step <name>` run as its own process,
// exactly as the app runs it (specs/epics/36-mac-app/contract-setup.md), so the
// terminal and the window share one implementation. With --answers the inputs
// come from a JSON object keyed by step instead of the prompts (automation, CI).
//
// On Linux the payload is first copied into the runtime (owner headless) and
// made `current`; every step after `check` runs from runtime/current, so the
// venv is built against the runtime's Python, never against the unpacked
// download, which is deleted afterwards.

// appSteps are the steps whose input --answers may give, in screen order
// (contract-setup.md).
var appSteps = []string{"check", "admin", "libraries", "models", "plex", "venv", "llm", "finish"}

// stepEvent is one NDJSON line of a step (setupEvent, plus progress fields).
type stepEvent struct {
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Field   string          `json:"field"`
	Params  map[string]any  `json:"params"`
	Stage   string          `json:"stage"`
	Done    int64           `json:"done"`
	Total   int64           `json:"total"`
}

// stepFailure is a step's error event (or a run that gave no result): its
// code, its English, the field to ask again, the params of its text.
type stepFailure struct {
	Code, Message, Field string
	Params               map[string]any
}

func (f *stepFailure) Error() string { return f.Code + ": " + f.Message }

// stepRunner runs one step with its input; progress gets each progress line.
type stepRunner func(ctx context.Context, step string, input any, progress func(stepEvent)) (json.RawMessage, error)

// execStepRunner runs `<server> setup --json --step <step>` with env added to
// this process's: the input on stdin (secrets never in argv or the
// environment), NDJSON on stdout.
func execStepRunner(server string, env ...string) stepRunner {
	return func(ctx context.Context, step string, input any, progress func(stepEvent)) (json.RawMessage, error) {
		in, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, server, "setup", "--json", "--step", step)
		cmd.Env = append(os.Environ(), env...)
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
				fail = &stepFailure{Code: ev.Code, Message: ev.Message, Field: ev.Field, Params: ev.Params}
			}
		}
	}
	return res, fail
}

// errCancelled: the terminal closed its input (Ctrl-D) at a question.
var errCancelled = errors.New("cancelled")

// asker is the wizard's terminal (tests replace it). Each question returns
// errCancelled when the input ends.
type asker interface {
	line(question, def string) (string, error)
	yesNo(question string, def bool) (bool, error)
	secret(question string) (string, error)
}

// termAsker asks on this terminal: stdin, echo off for secrets.
type termAsker struct {
	r   *bufio.Reader
	out io.Writer
	t   texts
}

func newTermAsker(t texts) *termAsker {
	return &termAsker{r: bufio.NewReader(os.Stdin), out: os.Stdout, t: t}
}

func (a *termAsker) read() (string, error) {
	s, err := a.r.ReadString('\n')
	if err != nil && strings.TrimSpace(s) == "" {
		return "", errCancelled
	}
	return strings.TrimSpace(s), nil
}

func (a *termAsker) line(question, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(a.out, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(a.out, "%s: ", question)
	}
	s, err := a.read()
	if err != nil {
		return "", err
	}
	if s == "" {
		return def, nil
	}
	return s, nil
}

func (a *termAsker) yesNo(question string, def bool) (bool, error) {
	y, n := a.t.get("terminal.yes"), a.t.get("terminal.no")
	if def {
		y = strings.ToUpper(y)
	} else {
		n = strings.ToUpper(n)
	}
	fmt.Fprintf(a.out, "%s [%s/%s]: ", question, y, n)
	s, err := a.read()
	if err != nil {
		return false, err
	}
	return yesNoAnswer(s, def), nil
}

// yesNoAnswer reads an answer in either language; anything else is def.
func yesNoAnswer(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "ن", "نعم":
		return true
	case "n", "no", "ل", "لا":
		return false
	}
	return def
}

func (a *termAsker) secret(question string) (string, error) {
	fmt.Fprintf(a.out, "%s: ", question)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(a.out)
		if err != nil {
			return "", errCancelled
		}
		return strings.TrimSpace(string(b)), nil
	}
	return a.read()
}

// appSetup is one run of the terminal wizard.
type appSetup struct {
	run     stepRunner
	ask     asker
	out     io.Writer
	t       texts
	answers map[string]json.RawMessage
	// runtime copies the payload into the runtime and returns the runner of the
	// steps there (Linux; nil: the steps run from this payload).
	runtime func(ctx context.Context) (stepRunner, error)
	// afterFinish installs and starts the service (Linux: the systemd user unit).
	afterFinish func(ctx context.Context) error
	// pollEvery is how often a Plex PIN is checked.
	pollEvery time.Duration
}

func (a *appSetup) printf(format string, args ...any) { fmt.Fprintf(a.out, format, args...) }
func (a *appSetup) say(key string, pairs ...string)   { a.printf("%s\n", a.t.get(key, pairs...)) }

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
func (a *appSetup) step(ctx context.Context, name string, ask func(field string) (any, error)) (json.RawMessage, error) {
	if raw, ok := a.answers[name]; ok {
		res, err := a.run(ctx, name, raw, a.progress())
		return res, wrapStep(name, err)
	}
	field := ""
	for attempt := 0; ; attempt++ {
		in, err := ask(field)
		if err != nil {
			return nil, err
		}
		res, err := a.run(ctx, name, in, a.progress())
		var f *stepFailure
		if err == nil {
			return res, nil
		}
		if !errors.As(err, &f) || f.Field == "" || attempt >= 4 {
			return nil, wrapStep(name, err)
		}
		a.printf("  %s\n", a.t.stepError(f))
		field = f.Field
	}
}

// fixed runs a step whose input the user does not give (venv, the models'
// download, finish): the answers file's when it has one.
func (a *appSetup) fixed(ctx context.Context, name string, input any) (json.RawMessage, error) {
	if raw, ok := a.answers[name]; ok {
		input = raw
	}
	res, err := a.run(ctx, name, input, a.progress())
	return res, wrapStep(name, err)
}

// Run walks the steps in screen order.
func (a *appSetup) Run(ctx context.Context) error {
	a.say("terminal.intro")
	a.printf("\n")
	if res, err := a.run(ctx, "status", map[string]any{}, nil); err == nil {
		var st struct {
			Configured bool   `json:"configured"`
			WebURL     string `json:"web_url"`
		}
		if json.Unmarshal(res, &st) == nil && st.Configured {
			a.say("terminal.already", "url", st.WebURL)
			if a.afterFinish == nil {
				return nil
			}
			again := len(a.answers) > 0
			if !again {
				if again, err = a.ask.yesNo(a.t.get("terminal.serviceAgain"), true); err != nil {
					return err
				}
			}
			if !again {
				return nil
			}
			if err := a.toRuntime(ctx); err != nil {
				return err
			}
			return a.afterFinish(ctx)
		}
	}

	res, err := a.fixed(ctx, "check", map[string]any{})
	if err != nil {
		return err
	}
	if !a.showCheck(res) {
		return errors.New(a.t.get("terminal.blockedEnd"))
	}
	if err := a.toRuntime(ctx); err != nil {
		return err
	}

	a.printf("\n%s\n", a.t.get("setup.admin.title"))
	a.say("setup.admin.intro")
	if _, err := a.step(ctx, "admin", a.askAdmin); err != nil {
		return err
	}

	a.printf("\n%s\n", a.t.get("setup.libraries.title"))
	a.say("setup.libraries.intro")
	if _, err := a.step(ctx, "libraries", a.askLibraries); err != nil {
		return err
	}

	a.printf("\n%s\n", a.t.get("setup.models.title"))
	a.say("setup.models.intro", "machine", a.t.get("setup.machine.linux"))
	a.say("setup.models.hfNote")
	if _, err := a.step(ctx, "models", a.askModels); err != nil {
		return err
	}

	a.printf("\n%s\n", a.t.get("setup.plex.title"))
	if err := a.plex(ctx); err != nil {
		return err
	}

	// what the desktop's last screen runs, in its order (SetupWizard.tsx)
	a.printf("\n%s\n", a.t.get("setup.install.title"))
	a.say("setup.install.venv")
	if _, err := a.fixed(ctx, "venv", map[string]any{}); err != nil {
		return err
	}
	a.say("setup.install.models")
	if _, err := a.run(ctx, "models", map[string]any{"action": "download"}, a.progress()); err != nil {
		return wrapStep("models", err)
	}
	// only too little disk stops setup: the server downloads a missing model
	a.say("setup.install.llm")
	if _, err := a.fixed(ctx, "llm", map[string]any{}); err != nil {
		var f *stepFailure
		if errors.As(err, &f) && f.Code == "disk_full" {
			return err
		}
		a.say("terminal.llmLater", "err", err.Error())
	}
	a.say("setup.install.finishing")
	res, err = a.fixed(ctx, "finish", map[string]any{})
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
	a.printf("\n")
	a.say("terminal.done", "url", fin.WebURL)
	return nil
}

// toRuntime moves the remaining steps into the runtime copy (Linux).
func (a *appSetup) toRuntime(ctx context.Context) error {
	if a.runtime == nil {
		return nil
	}
	run, err := a.runtime(ctx)
	if err != nil {
		return err
	}
	a.run = run
	return nil
}

func (a *appSetup) askAdmin(string) (any, error) {
	user, err := a.ask.line(a.t.get("login.username"), "admin")
	if err != nil {
		return nil, err
	}
	for {
		pw, err := a.ask.secret(a.t.get("login.password"))
		if err != nil {
			return nil, err
		}
		again, err := a.ask.secret(a.t.get("setup.admin.confirm"))
		if err != nil {
			return nil, err
		}
		switch {
		case pw == "":
			a.printf("  %s\n", a.t.get("terminal.passwordEmpty"))
		case pw != again:
			a.printf("  %s\n", a.t.get("setup.admin.mismatch"))
		default:
			return map[string]any{"username": user, "password": pw}, nil
		}
	}
}

func (a *appSetup) askLibraries(string) (any, error) {
	line, err := a.ask.line(a.t.get("terminal.roots"), "")
	if err != nil {
		return nil, err
	}
	roots := splitList(line)
	a.say("setup.libraries.mapNote")
	for {
		line, err := a.ask.line(a.t.get("terminal.map"), "")
		if err != nil {
			return nil, err
		}
		pm, ok := parsePathMap(line)
		if ok {
			return map[string]any{"roots": roots, "path_map": pm}, nil
		}
		a.printf("  %s\n", a.t.get("setup.libraries.mapBad"))
	}
}

// parsePathMap reads "their path => the path here" entries separated by
// commas; false when one lacks either side.
func parsePathMap(line string) ([]map[string]string, bool) {
	pm := []map[string]string{}
	for _, pair := range splitList(line) {
		from, to, ok := strings.Cut(pair, "=>")
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if !ok || from == "" || to == "" {
			return nil, false
		}
		pm = append(pm, map[string]string{"from": from, "to": to})
	}
	return pm, true
}

func (a *appSetup) askModels(string) (any, error) {
	tok, err := a.ask.secret(a.t.get("terminal.hfOrShare"))
	if err != nil {
		return nil, err
	}
	if tok != "" {
		return map[string]any{"source": "huggingface", "hf_token": tok}, nil
	}
	path, err := a.ask.line(a.t.get("setup.models.nasPath"), "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"source": "nas", "path": path}, nil
}

// showCheck prints what will run, the problems and the warnings
// (checkResult's notes), in the screens' words; false when the computer cannot
// run the server.
func (a *appSetup) showCheck(res json.RawMessage) bool {
	var c struct {
		OK       bool        `json:"ok"`
		Problems []checkNote `json:"problems"`
		Warnings []checkNote `json:"warnings"`
		Runs     []setupRole `json:"runs"`
		DataDir  string      `json:"data_dir"`
	}
	if json.Unmarshal(res, &c) != nil {
		return false
	}
	a.printf("%s (%s: %s)\n", a.t.get("setup.check.title"), a.t.get("setup.check.dataDir"), c.DataDir)
	for _, r := range c.Runs {
		a.printf("  - %s\n", a.t.runText(r.Role, r.What, r.Params))
	}
	if !c.OK {
		a.say("terminal.blocked")
		for _, p := range c.Problems {
			a.printf("  - %s\n", a.t.coded("setup.check.note", p.Code, p.Params, p.Message))
		}
	}
	for _, w := range c.Warnings {
		a.printf("  %s\n", a.t.get("terminal.note", "text", a.t.coded("setup.check.note", w.Code, w.Params, w.Message)))
	}
	return c.OK
}

// plex links Plex with the PIN flow (optional): the code is shown, then
// polled until it is entered at plex.tv/link or expires.
func (a *appSetup) plex(ctx context.Context) error {
	if raw, ok := a.answers["plex"]; ok {
		_, err := a.run(ctx, "plex", raw, nil)
		return wrapStep("plex", err)
	}
	a.say("setup.plex.intro")
	link, err := a.ask.yesNo(a.t.get("terminal.plexAsk"), false)
	if err != nil {
		return err
	}
	if !link {
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
	a.printf("%s %s\n", a.t.get("setup.plex.enterCode", "url", pin.LinkURL), pin.Code)
	a.say("setup.plex.waiting")
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
			a.printf("  %s\n", a.t.get("terminal.plexLinked"))
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
		if !slices.Contains(appSteps, k) {
			return nil, fmt.Errorf("answers: no setup step %q (%s)", k, strings.Join(appSteps, ", "))
		}
	}
	return m, nil
}

// runAppSetupCmd is `setup` run from an app payload: the terminal wizard over
// this binary's own setup steps; on Linux the runtime copy (before the steps
// that build on it) and the user service, which the desktop app does itself.
func runAppSetupCmd(answersFile string, answersFD int, lang string) error {
	l, err := setupLang(lang, os.Getenv)
	if err != nil {
		return err
	}
	t := textsFor(l)
	if runtimeGOOS == "linux" && geteuid() == 0 {
		return errors.New(t.get("terminal.root"))
	}
	answers, err := readAnswers(answersFile, answersFD)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	a := &appSetup{run: execStepRunner(self), ask: newTermAsker(t), out: os.Stdout, t: t, answers: answers,
		pollEvery: 2 * time.Second}
	if runtimeGOOS == "linux" {
		a.runtime = func(ctx context.Context) (stepRunner, error) { return headlessRuntime(ctx, t, os.Stdout) }
		a.afterFinish = func(ctx context.Context) error { return headlessService(ctx, t, os.Stdout) }
	}
	err = a.Run(context.Background())
	if errors.Is(err, errCancelled) {
		return errors.New(t.get("terminal.cancelled"))
	}
	return err
}

// runtimeGOOS is runtime.GOOS and geteuid os.Geteuid (tests replace them).
var (
	runtimeGOOS = goruntime.GOOS
	geteuid     = os.Geteuid
)

// headlessRuntime puts this payload in the runtime (owner: the machine itself,
// it updates through its releases) and makes it `current`, then returns the
// runner of the steps in runtime/current, MAKTABA_APP_BUNDLE pointing there:
// the venv is built against runtime/current/python.
func headlessRuntime(ctx context.Context, t texts, out io.Writer) (stepRunner, error) {
	payload := payloadOfThisBinary()
	if payload == "" {
		return nil, errors.New("this maktaba-server is not in a payload")
	}
	d := newRuntimeDeps()
	fmt.Fprintln(out, t.get("terminal.copying", "dir", d.rt.Root))
	var last time.Time
	res, err := d.rt.Install(ctx, payload, func(done, total int64) {
		if done < total && time.Since(last) < 2*time.Second {
			return
		}
		last = time.Now()
		fmt.Fprintf(out, "  %d%%\n", done*100/max(total, 1))
	}, appruntime.Options{})
	if err != nil {
		return nil, err
	}
	if err := d.rt.SetOwner(appruntime.OwnerHeadless); err != nil {
		return nil, err
	}
	if cur, _ := d.rt.Current(); cur != filepath.Base(res.Dir) {
		if _, err := d.rt.Activate(ctx, filepath.Base(res.Dir), d.service, appruntime.Options{}); err != nil {
			return nil, err
		}
	}
	current := filepath.Join(d.rt.Root, appruntime.CurrentLink)
	return execStepRunner(filepath.Join(current, "bin", "maktaba-server"), "MAKTABA_APP_BUNDLE="+current), nil
}

// headlessService installs and starts the systemd user unit that runs
// runtime/current, with linger, asking for the password on this terminal where
// polkit wants one.
func headlessService(ctx context.Context, t texts, out io.Writer) error {
	sd := newServiceDeps()
	if err := sd.manager.Install(ctx, sd.unit, true, false); err != nil {
		return err
	}
	fmt.Fprintln(out, t.get("terminal.started", "unit", sd.unit.Path()))
	if sd.manager.Status(ctx, sd.unit).Linger { // nothing to ask an administrator for
		fmt.Fprintln(out, t.get("terminal.linger"))
		return nil
	}
	if err := sd.manager.Linger(ctx, true); err != nil {
		fmt.Fprintln(out, t.get("terminal.noLinger", "user", os.Getenv("USER"), "err", err.Error()))
	} else {
		fmt.Fprintln(out, t.get("terminal.linger"))
	}
	return nil
}
