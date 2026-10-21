package devmigrate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// DefaultContainer is the compose Postgres's container (deploy/compose).
const DefaultContainer = "maktaba-postgres-1"

// Target is Maktaba.app's side.
type Target struct {
	Config     config.Config
	ConfigPath string
	Found      bool   // server.toml exists
	State      string // the state root (models/, chroma/, postgres/, backups/, venv/)
	Keys       string
	Bundle     *pgbundle.Bundle // nil: no bundled Postgres
	NoBundle   string           // why there is none
	Payload    string           // the app's server payload ("" unknown)
}

// Migration is one move.
type Migration struct {
	Sys       Sys
	Target    Target
	Container string
	Repo      string // the stack's checkout ("" detect)
	Out       io.Writer
	// Replace lets a run go ahead over an app install that already has a
	// server.toml or a database (they are replaced).
	Replace bool
	// StopWait bounds the wait for the stack's processes to exit.
	StopWait time.Duration
	// Ports must be free once the stack stopped (default StackPorts).
	Ports []string
}

func (m *Migration) ports() []string {
	if m.Ports != nil {
		return m.Ports
	}
	return StackPorts
}

// Marker records a move: <state>/migrate-from-dev.json.
type Marker struct {
	Stage       string           `json:"stage"` // migrated, verified, finalized
	MigratedAt  time.Time        `json:"migrated_at"`
	VerifiedAt  *time.Time       `json:"verified_at,omitempty"`
	FinalizedAt *time.Time       `json:"finalized_at,omitempty"`
	Repo        string           `json:"repo"`
	Container   string           `json:"container"`
	Volume      string           `json:"volume"`
	Dump        string           `json:"dump"`
	Workers     int              `json:"workers"`
	Counts      map[string]int64 `json:"counts"`
	HostID      string           `json:"host_id,omitempty"`
}

func (m *Migration) markerPath() string {
	return filepath.Join(m.Target.State, "migrate-from-dev.json")
}

func (m *Migration) readMarker() (Marker, error) {
	var mk Marker
	raw, err := os.ReadFile(m.markerPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mk, fmt.Errorf("no move from the developer stack is recorded (%s): run `maktaba-server migrate-from-dev` first", m.markerPath())
		}
		return mk, err
	}
	return mk, json.Unmarshal(raw, &mk)
}

func (m *Migration) writeMarker(mk Marker) error {
	raw, err := json.MarshalIndent(mk, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.markerPath() + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.markerPath())
}

func (m *Migration) say(format string, args ...any) { _, _ = fmt.Fprintf(m.Out, format+"\n", args...) }

// Plan is what a move would do, read without changing anything.
type Plan struct {
	Stack     Stack
	Settings  Settings
	Source    Source
	Container Container
	Facts     Facts
	FactsErr  error
	Items     []Item
	Problems  []string // a run refuses while any is left
	Warnings  []string
}

// MakePlan looks at the stack and the app (read only: Docker and the database
// are only queried).
func (m *Migration) MakePlan(ctx context.Context) Plan {
	var p Plan
	p.Stack = FindStack(ctx, m.Sys, m.Repo)
	p.Settings = ReadSettings(m.Sys, p.Stack)
	src, err := NewSource(m.Sys, m.Container, p.Settings)
	if err != nil {
		p.Problems = append(p.Problems, err.Error())
	}
	p.Source = src
	p.Container = src.Inspect(ctx)
	switch {
	case !p.Container.Exists:
		p.Problems = append(p.Problems, fmt.Sprintf("no Docker container %s (is Docker running? --container names another)", m.Container))
	case !p.Container.Running:
		p.Problems = append(p.Problems, fmt.Sprintf("the container %s is stopped: start it (docker start %s)", m.Container, m.Container))
	default:
		p.Facts, p.FactsErr = ReadFacts(func(sql string) (string, error) { return src.Query(ctx, sql) })
		if p.FactsErr != nil {
			p.Problems = append(p.Problems, "read the database: "+p.FactsErr.Error())
		}
	}
	if p.Container.Exists && p.Container.Volume == "" {
		p.Warnings = append(p.Warnings, "the container keeps its data in no named volume: --finalize will only remove the container")
	}
	if len(p.Stack.Procs) > 0 && p.Stack.Script() == "" {
		p.Problems = append(p.Problems, "the stack runs but its checkout was not found: name it with --dev-repo")
	}
	if busy := PortsInUse(m.ports()); len(busy) > 0 && len(p.Stack.Procs) == 0 {
		p.Problems = append(p.Problems, "something listens on port "+strings.Join(busy, ", ")+" and it is not the stack (Maktaba.app's server?): stop it first")
	}
	if len(p.Stack.App) > 0 {
		p.Problems = append(p.Problems, "Maktaba.app's server is running: quit Maktaba (menu bar > Quit) first")
	}
	t := m.Target
	if t.Bundle == nil {
		p.Problems = append(p.Problems, "no bundled Postgres: "+t.NoBundle)
	} else if t.Bundle.Listening(ctx) {
		p.Problems = append(p.Problems, fmt.Sprintf("a Postgres already listens on %s (Maktaba.app's?): quit Maktaba first", t.Bundle.Addr()))
	}
	if !m.Replace {
		if t.Found {
			p.Problems = append(p.Problems, t.ConfigPath+" exists (this Mac has been set up): --replace overwrites that install")
		}
		if t.Bundle != nil && t.Bundle.Initialized() {
			p.Problems = append(p.Problems, "the bundled Postgres has a database already ("+t.Bundle.Data+"): --replace overwrites it")
		}
	}
	if mk, err := m.readMarker(); err == nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("a move was already made on %s (%s)", mk.MigratedAt.Local().Format(time.DateTime), mk.Stage))
	}
	if roles := p.Facts.MachineRoles(); len(roles) > 0 || len(p.Facts.Nodes) > 0 {
		who := ""
		if len(p.Facts.Nodes) > 0 {
			who = " (" + strings.Join(p.Facts.Nodes, ", ") + ")"
		}
		p.Warnings = append(p.Warnings, fmt.Sprintf("machines joined this home%s: they reach its database on port 5432, "+
			"and the bundled Postgres listens on this Mac only, so they stop working after the move until they can reach it again", who))
	}
	if p.Settings.EmbedModel != "" {
		p.Warnings = append(p.Warnings, "MAKTABA_EMBED_MODEL="+p.Settings.EmbedModel+" is not carried (server.toml has no setting): set it for the app's pipeline")
	}
	p.Items = Items(p.Settings, t.State, t.Keys)
	for _, it := range p.Items {
		if !it.Exists && it.Key && it.What != "Hugging Face token" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("no %s at %s (the app makes a new one)", it.What, it.From))
		}
	}
	if need, free := p.Need(), freeBytes(existingParent(t.State)); free >= 0 && need > free {
		p.Problems = append(p.Problems, fmt.Sprintf("needs about %s of disk, %s is free", human(need), human(free)))
	}
	return p
}

// Need estimates the disk a move takes: the dump and the restored database
// (each at most the database's size), and the copies that are not clones.
func (p Plan) Need() int64 {
	n := 2 * p.Facts.Size
	for _, it := range p.Items {
		if it.Exists && !it.Done && !it.Clone {
			n += it.Size
		}
	}
	return n
}

// Print writes the plan.
func (m *Migration) Print(p Plan) {
	t := m.Target
	m.say("Developer stack")
	if p.Stack.Repo != "" {
		m.say("  checkout     %s", p.Stack.Repo)
	}
	m.say("  running      %s", p.Stack.Summary())
	db := fmt.Sprintf("Docker container %s", m.Container)
	if p.Container.Exists {
		db += fmt.Sprintf(" (%s, volume %s)", p.Container.Image, orDefault(p.Container.Volume, "none"))
	}
	if p.Facts.Size > 0 {
		db += ", " + human(p.Facts.Size)
	}
	m.say("  database     %s", db)
	if len(p.Facts.Counts) > 0 {
		m.say("  holds        %s", countsLine(p.Facts.Counts))
	}
	m.say("")
	m.say("Maktaba.app")
	m.say("  state        %s", t.State)
	m.say("  server.toml  %s%s", t.ConfigPath, map[bool]string{true: " (exists)", false: ""}[t.Found])
	if t.Bundle != nil {
		m.say("  postgres     %s (port %d, cluster %s)", t.Bundle.Home, t.Bundle.Port, t.Bundle.Data)
	} else {
		m.say("  postgres     none: %s", t.NoBundle)
	}
	m.say("")
	m.say("Steps")
	venv := "skipped: no Python in the payload"
	if m.hasPython() {
		venv = "build " + filepath.Join(t.State, "venv") + " from the payload, while the stack runs (minutes the first time)"
	}
	m.say("  1. pipeline venv            %s", venv)
	stop := "nothing runs"
	if len(p.Stack.Procs) > 0 {
		stop = "zsh " + p.Stack.Script() + " stop"
	}
	m.say("  2. stop the stack           %s", stop)
	m.say("  3. dump the database        docker exec %s pg_dump -Fc -> %s (at most %s)", m.Container,
		filepath.Join(t.State, "backups", "from-dev-<time>.dump"), human(p.Facts.Size))
	m.say("  4. stop the container       docker stop %s (its volume %s is kept)", m.Container, orDefault(p.Container.Volume, "-"))
	m.say("  5. restore                  into the bundled Postgres (%s), then check the counts above", human(p.Facts.Size))
	m.say("  6. copy                     (clones on this volume take no space)")
	for _, it := range p.Items {
		state := human(it.Size)
		switch {
		case !it.Exists:
			state = "none"
		case it.Done:
			state += ", there already"
		case it.Clone:
			state += ", clone"
		}
		m.say("     %s (%s)", it.What, state)
		if it.Exists {
			m.say("        %s -> %s", it.From, it.To)
		}
	}
	m.say("  7. server.toml              %s", t.ConfigPath)
	for _, l := range p.Settings.TOMLLines() {
		m.say("       %s", l)
	}
	if free := freeBytes(existingParent(t.State)); free >= 0 {
		m.say("")
		m.say("Disk: needs about %s, %s free.", human(p.Need()), human(free))
	}
	for _, w := range p.Warnings {
		m.say("Note: %s", w)
	}
	for _, pr := range p.Problems {
		m.say("Cannot go ahead: %s", pr)
	}
}

func countsLine(c map[string]int64) string {
	var parts []string
	for _, k := range append(append([]string{}, CheckTables...), "goose") {
		if v, ok := c[k]; ok {
			name := strings.ReplaceAll(k, "_", " ")
			if k == "goose" {
				name = "migration version"
			}
			parts = append(parts, fmt.Sprintf("%s %d", name, v))
		}
	}
	return strings.Join(parts, ", ")
}

// Run makes the move the plan describes (MakePlan, with no Problems left).
// Nothing of the stack is deleted.
func (m *Migration) Run(ctx context.Context, p Plan) error {
	if len(p.Problems) > 0 {
		return errors.New("nothing was changed")
	}
	t := m.Target
	m.say("")

	// 1. the pipeline's venv, while the stack still runs (minutes the first time)
	if m.hasPython() {
		m.say("==> building the pipeline's Python environment")
		if err := m.buildVenv(ctx); err != nil {
			return err
		}
	}

	// 2. stop the stack
	if len(p.Stack.Procs) > 0 {
		m.say("==> stopping the developer stack")
		cmd := m.Sys.Command(ctx, "zsh", p.Stack.Script(), "stop")
		cmd.Stdout, cmd.Stderr = m.Out, m.Out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("stack.sh stop: %w", err)
		}
		if err := m.waitStopped(ctx); err != nil {
			return err
		}
	}

	// 3. dump at one snapshot, counted in it
	m.say("==> dumping the database (%s)", human(p.Facts.Size))
	if err := os.MkdirAll(filepath.Join(t.State, "backups"), 0o700); err != nil {
		return err
	}
	dump := filepath.Join(t.State, "backups", "from-dev-"+time.Now().Format("20060102-150405")+".dump")
	sn, err := p.Source.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("open a snapshot of the database: %w", err)
	}
	facts, err := ReadFacts(sn.Query)
	if err == nil {
		err = p.Source.Dump(ctx, sn.ID, dump)
	}
	if cerr := sn.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	m.say("    %s (%s): %s", dump, human(fileSize(dump)), countsLine(facts.Counts))

	// 4. stop the container: machines that joined must not write to it any more
	m.say("==> stopping the container %s (the volume stays)", m.Container)
	if out, err := m.Sys.Command(ctx, "docker", "stop", m.Container).CombinedOutput(); err != nil {
		return fmt.Errorf("docker stop %s: %w: %s", m.Container, err, strings.TrimSpace(string(out)))
	}
	mk := Marker{Stage: "dumped", MigratedAt: time.Now().UTC(), Repo: p.Stack.Repo, Container: m.Container,
		Volume: p.Container.Volume, Dump: dump, Workers: p.Stack.Workers(), Counts: facts.Counts, HostID: facts.HostID}
	if err := m.writeMarker(mk); err != nil {
		return err
	}

	// 5. restore and check
	m.say("==> restoring into the bundled Postgres")
	logf, closeLog := m.restoreLog()
	defer closeLog()
	opts := pgbundle.RestoreOptions{Roles: facts.Roles, Members: facts.Members}
	err = t.Bundle.Restore(ctx, dump, opts, logf, func(q pgbundle.Querier) error {
		got, err := ReadFacts(func(sql string) (string, error) { return firstRow(q.Query(ctx, pgbundle.AppDatabase, sql)) })
		if err != nil {
			return err
		}
		if err := Compare(facts.Counts, got.Counts, true); err != nil {
			return fmt.Errorf("the restored database differs: %w", err)
		}
		m.say("    the counts match: %s", countsLine(got.Counts))
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w (the restore's log: %s)\n%s", err, m.restoreLogPath(), m.Undo(mk))
	}

	// 6. the files (and a look at the models the venv finds)
	m.say("==> copying")
	for _, it := range p.Items {
		if !it.Exists {
			continue
		}
		if err := it.Copy(); err != nil {
			return fmt.Errorf("%w\n%s", err, m.Undo(mk))
		}
		m.say("    %-24s %s", it.What, it.To)
	}

	if m.hasPython() {
		m.checkModels(ctx)
	}

	// 7. server.toml
	m.say("==> writing %s", t.ConfigPath)
	cfg := t.Config
	p.Settings.Apply(&cfg)
	if err := cfg.SaveTo(t.ConfigPath); err != nil {
		return err
	}
	mk.Stage = "migrated"
	if err := m.writeMarker(mk); err != nil {
		return err
	}
	m.say("")
	m.say("Moved. Next:")
	m.say("  1. open Maktaba (it starts the server from %s)", t.State)
	m.say("  2. maktaba-server migrate-from-dev --verify   (the library, transcripts and the Live TV host check in)")
	m.say("  3. maktaba-server migrate-from-dev --finalize (retires the Docker database; only once you are sure)")
	m.say("")
	m.say("%s", m.Undo(mk))
	return nil
}

func firstRow(rows []string, err error) (string, error) {
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0], nil
}

// waitStopped waits for the stack's processes to be gone.
func (m *Migration) waitStopped(ctx context.Context) error {
	wait := m.StopWait
	if wait <= 0 {
		wait = 2 * time.Minute
	}
	deadline := time.Now().Add(wait)
	for {
		st := FindStack(ctx, m.Sys, m.Repo)
		busy := PortsInUse(m.ports())
		if len(st.Procs) == 0 && len(busy) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			if len(st.Procs) == 0 {
				return fmt.Errorf("the stack stopped but something still listens on port %s", strings.Join(busy, ", "))
			}
			return fmt.Errorf("the stack is still running after %s: %s", wait, st.Summary())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (m *Migration) restoreLogPath() string {
	return filepath.Join(config.LogsDir(), "migrate-from-dev.log")
}

func (m *Migration) restoreLog() (io.Writer, func()) {
	_ = os.MkdirAll(config.LogsDir(), 0o755)
	f, err := os.OpenFile(m.restoreLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return io.Discard, func() {}
	}
	return f, func() { _ = f.Close() }
}

// Undo is how to go back to the stack.
func (m *Migration) Undo(mk Marker) string {
	repo := orDefault(mk.Repo, "<the checkout>")
	workers := mk.Workers
	if workers <= 0 {
		workers = 2
	}
	return strings.Join([]string{
		"To go back to the developer stack (nothing of it was deleted):",
		"  1. quit Maktaba (menu bar > Quit), or: launchctl bootout gui/$(id -u)/com.hamzalabs.maktaba.server",
		"  2. docker start " + mk.Container,
		"  3. cd " + repo + " && CGO_ENABLED=0 zsh tools/dev/stack.sh start " + strconv.Itoa(workers),
		"  What changed in Maktaba.app since the move stays in " + m.Target.State + " (not in the stack's database).",
	}, "\n")
}

// payloadPython is the payload's Python and the pipeline wheel folder.
func (m *Migration) payloadPython() (py, pipeline string) {
	if m.Target.Payload == "" {
		return "", ""
	}
	return filepath.Join(m.Target.Payload, "python", "bin", "python3"), filepath.Join(m.Target.Payload, "pipeline")
}

func (m *Migration) hasPython() bool {
	py, pl := m.payloadPython()
	if py == "" {
		return false
	}
	whl, _ := filepath.Glob(filepath.Join(pl, "maktaba_pipeline-*.whl"))
	_, err := os.Stat(py)
	return err == nil && len(whl) == 1
}

// buildVenv runs the pipeline's venv bootstrap from the payload (contract-venv.md).
func (m *Migration) buildVenv(ctx context.Context) error {
	py, pl := m.payloadPython()
	whl, _ := filepath.Glob(filepath.Join(pl, "maktaba_pipeline-*.whl"))
	cmd := m.Sys.Command(ctx, py, "-m", "maktaba_pipeline.venv_bootstrap", "--payload", pl,
		"--venv", filepath.Join(m.Target.State, "venv"))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+whl[0])
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = m.Out
	if err := cmd.Start(); err != nil {
		return err
	}
	last := ""
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		var ev struct{ Step, Message string }
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Step != last {
			last = ev.Step
			m.say("    %s: %s", ev.Step, ev.Message)
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("the pipeline's venv: %w (its log: %s)", err, filepath.Join(m.Target.State, "venv-status.json"))
	}
	return nil
}

// checkModels asks the new venv which models it finds in the app's model folder.
func (m *Migration) checkModels(ctx context.Context) {
	bin := filepath.Join(m.Target.State, "venv", "bin", "maktaba-pipeline")
	cmd := m.Sys.Command(ctx, bin, "models", "status")
	cmd.Env = append(os.Environ(), "MAKTABA_MODEL_CACHE_DIR="+filepath.Join(m.Target.State, "models"))
	out, err := cmd.CombinedOutput()
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			m.say("    %s", l)
		}
	}
	if err != nil {
		m.say("    (some models are missing: the app's setup downloads them, or `maktaba-server models`)")
	}
}

// Verify checks the app after it started: the API answers, the library and the
// transcripts are all there, and the Live TV host checked in after the move.
func (m *Migration) Verify(ctx context.Context, wait time.Duration) error {
	mk, err := m.readMarker()
	if err != nil {
		return err
	}
	if mk.Stage == "dumped" {
		return errors.New("the move did not finish: run `maktaba-server migrate-from-dev --replace` again")
	}
	t := m.Target
	if t.Bundle == nil {
		return errors.New("no bundled Postgres: " + t.NoBundle)
	}
	deadline := time.Now().Add(wait)
	var last error
	for {
		last = m.verifyOnce(ctx, mk)
		if last == nil || time.Now().After(deadline) {
			break
		}
		m.say("    waiting: %v", last)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	if last != nil {
		return fmt.Errorf("not yet: %w", last)
	}
	now := time.Now().UTC()
	mk.Stage, mk.VerifiedAt = "verified", &now
	if err := m.writeMarker(mk); err != nil {
		return err
	}
	m.say("Verified. Once you are sure, retire the Docker database: maktaba-server migrate-from-dev --finalize")
	return nil
}

func (m *Migration) verifyOnce(ctx context.Context, mk Marker) error {
	t := m.Target
	api := "http://" + supervisor.LocalAddr(t.Config.Server.Listen, "8080") + "/healthz"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("the API (%s) does not answer: %w", api, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the API (%s) answers %s", api, resp.Status)
	}
	q, err := t.Bundle.NewQuerier()
	if err != nil {
		return err
	}
	query := func(sql string) (string, error) { return firstRow(q.Query(ctx, pgbundle.AppDatabase, sql)) }
	got, err := ReadFacts(query)
	if err != nil {
		return err
	}
	if err := Compare(mk.Counts, got.Counts, false); err != nil {
		return fmt.Errorf("rows are missing: %w", err)
	}
	if mk.HostID != "" {
		row, err := query("SELECT coalesce((SELECT (heartbeat_at > '" + mk.MigratedAt.Format(time.RFC3339Nano) +
			"'::timestamptz)::text FROM streaming_hosts WHERE host_id = '" + strings.ReplaceAll(mk.HostID, "'", "") + "'), 'none')")
		if err != nil {
			return err
		}
		if row != "true" {
			return fmt.Errorf("the Live TV host %s has not checked in since the move", mk.HostID)
		}
	}
	m.say("    the API answers; %s; the Live TV host checked in", countsLine(got.Counts))
	return nil
}

// Finalize retires the stack's Docker database once a verified move is
// confirmed: it removes the stopped container and its volume. The dump stays
// in backups/.
func (m *Migration) Finalize(ctx context.Context, confirm func(prompt string) bool) error {
	mk, err := m.readMarker()
	if err != nil {
		return err
	}
	if mk.Stage == "finalized" {
		m.say("Already retired on %s.", mk.FinalizedAt.Local().Format(time.DateTime))
		return nil
	}
	if mk.Stage != "verified" {
		return errors.New("the move is not verified: open Maktaba, then run `maktaba-server migrate-from-dev --verify`")
	}
	src := Source{sys: m.Sys, Container: mk.Container}
	c := src.Inspect(ctx)
	if c.Running {
		return fmt.Errorf("the container %s runs again (did you go back to the stack?): stop it first", mk.Container)
	}
	what := "the container " + mk.Container
	if mk.Volume != "" {
		what += " and its volume " + mk.Volume
	}
	m.say("This deletes %s for good. The dump %s stays.", what, mk.Dump)
	if !confirm("Type retire to go ahead: ") {
		return errors.New("nothing was deleted")
	}
	if c.Exists {
		if out, err := m.Sys.Command(ctx, "docker", "rm", mk.Container).CombinedOutput(); err != nil {
			return fmt.Errorf("docker rm %s: %w: %s", mk.Container, err, strings.TrimSpace(string(out)))
		}
	}
	if mk.Volume != "" {
		if out, err := m.Sys.Command(ctx, "docker", "volume", "rm", mk.Volume).CombinedOutput(); err != nil {
			return fmt.Errorf("docker volume rm %s: %w: %s", mk.Volume, err, strings.TrimSpace(string(out)))
		}
	}
	now := time.Now().UTC()
	mk.Stage, mk.FinalizedAt = "finalized", &now
	if err := m.writeMarker(mk); err != nil {
		return err
	}
	m.say("Retired %s.", what)
	m.say("The stack's own files were copied, not moved; delete them by hand when you no longer need them:")
	for _, it := range Items(ReadSettings(m.Sys, Stack{}), m.Target.State, m.Target.Keys) {
		if it.Exists && !it.Key {
			m.say("  %s (%s)", it.From, human(it.Size))
		}
	}
	return nil
}

func fileSize(p string) int64 {
	if info, err := os.Stat(p); err == nil {
		return info.Size()
	}
	return 0
}

// human is a size in the units Finder uses.
func human(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
