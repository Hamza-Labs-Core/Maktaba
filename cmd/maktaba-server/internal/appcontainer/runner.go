package appcontainer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A first-party plugin runner on Windows (Epic 54, story 54.23): Maktaba's own stages, one
// Python process per runner group, inside the group's own AppContainer and a Job Object,
// the way the plugin host runs (#710). On macOS and Linux a runner applies its sandbox to
// itself (Seatbelt, Landlock and seccomp); a Windows process cannot put itself in an
// AppContainer, so `maktaba-server runner-sandbox-run` does it from outside:
//
//  1. it asks the runner for its policy (`<runner> --policy`: the union of the group's
//     [trusted] tables over this machine's areas, the same code the other systems apply;
//     the runner imports no plugin code for it);
//  2. it grants the group's profile (Maktaba.Runner.<group>, one per group, so a group's
//     folders are its own) read on the interpreter, the read areas and the tools' folders,
//     and full control on the run folder and the write areas, and takes back what an
//     earlier start granted that the policy no longer names (the grants file);
//  3. it starts the runner in that AppContainer, with internetClient only for a group with
//     net (no capability otherwise: the Windows Filtering Platform blocks every network
//     path, loopback included), in a Job Object that allows no child process unless the
//     group may run tools, and exits with its exit code.
//
// Any step that fails exits RunnerSandboxExit (no runner runs unsandboxed), except the
// runner's own refusals at step 1 (untrusted packages, a secret in its environment), which
// pass through.

// RunnerProfilePrefix names a group's AppContainer profile (RunnerProfile).
const RunnerProfilePrefix = "Maktaba.Runner."

// The runner's exit codes this launcher answers or passes through
// (maktaba_plugin_runner/__main__.py).
const (
	RunnerSandboxExit   = 70
	RunnerUntrustedExit = 71
	RunnerEnvExit       = 72
)

// CapInternetClient is the internetClient capability's SID: outbound internet, never
// loopback or the local network.
const CapInternetClient = "S-1-15-3-1"

// RunnerProfile is a runner group's AppContainer profile.
func RunnerProfile(group string) string { return RunnerProfilePrefix + group }

// RunnerPolicy is what `<runner> --policy` prints: the group's sandbox as data.
type RunnerPolicy struct {
	Read       []string `json:"read"`
	Write      []string `json:"write"`
	WriteNamed []string `json:"write_named"`
	Exec       []string `json:"exec"`
	Net        bool     `json:"net"`
	GPU        bool     `json:"gpu"`
	RunDir     string   `json:"run_dir"`
	// Python is what the interpreter reads: its installation, its packages, sys.path
	Python []string `json:"python"`
	// ToolDirs are the installations of the Exec tools (what they read to start)
	ToolDirs []string `json:"tool_dirs"`
	// Processes is the Job Object's process limit: the runner (and, from a venv, the
	// interpreter its launcher starts), plus room for the tools when Exec names any
	Processes int `json:"processes"`
}

// GrantEntry is one folder (or file) the profile is granted.
type GrantEntry struct {
	Path  string `json:"path"`
	Write bool   `json:"write"`
	// Create: made when missing (the run folder and write areas under the data folder);
	// otherwise a missing path is skipped (a library on a drive not mounted now)
	Create bool `json:"-"`
}

func pathKey(p string) string { return strings.ToLower(filepath.Clean(p)) }

func under(p, root string) bool {
	if root == "" {
		return false
	}
	p, root = pathKey(p), pathKey(root)
	return p == root || strings.HasPrefix(p, strings.TrimRight(root, `\/`)+string(filepath.Separator))
}

// Grants is the policy's folders as grants: write wins over read for the same path, and a
// write folder is made when missing only in the run folder or the data folder.
func (p RunnerPolicy) Grants(dataDir string) []GrantEntry {
	byKey := map[string]GrantEntry{}
	add := func(path string, write, create bool) {
		if strings.TrimSpace(path) == "" {
			return
		}
		k := pathKey(path)
		g, ok := byKey[k]
		if !ok {
			g = GrantEntry{Path: filepath.Clean(path)}
		}
		g.Write = g.Write || write
		g.Create = g.Create || create
		byKey[k] = g
	}
	for _, l := range [][]string{p.Python, p.Read, p.ToolDirs, p.Exec} {
		for _, x := range l {
			add(x, false, false)
		}
	}
	add(p.RunDir, true, true)
	for _, x := range p.Write {
		add(x, true, under(x, dataDir) || under(x, p.RunDir))
	}
	for _, x := range p.WriteNamed {
		// the library: written by subtitle name only (the runner's guard_names), never made
		add(x, true, false)
	}
	out := make([]GrantEntry, 0, len(byKey))
	for _, g := range byKey {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return pathKey(out[i].Path) < pathKey(out[j].Path) })
	return out
}

// Stale is what an earlier start granted that now is not granted the same way: taken back
// before the new grants (a library removed from the home, a group that lost a write area).
func Stale(before, now []GrantEntry) []GrantEntry {
	keep := map[string]bool{}
	for _, g := range now {
		keep[pathKey(g.Path)+fmt.Sprint(g.Write)] = true
	}
	var out []GrantEntry
	for _, g := range before {
		if !keep[pathKey(g.Path)+fmt.Sprint(g.Write)] {
			out = append(out, g)
		}
	}
	return out
}

// RunnerOptions is the runner's start inside its AppContainer.
func (p RunnerPolicy) RunnerOptions(env []string) Options {
	o := Options{Env: env, ProcessLimit: p.Processes}
	if o.ProcessLimit < 1 {
		o.ProcessLimit = 1
	}
	if p.Net {
		o.Capabilities = []string{CapInternetClient}
	}
	return o
}

// The launcher's seams (tests replace them): the policy run, and the Windows calls.
var (
	runnerPolicyTimeout = 2 * time.Minute
	askPolicy           = func(prog string, args []string, stderr io.Writer) ([]byte, int, error) {
		ctx, cancel := context.WithTimeout(context.Background(), runnerPolicyTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, prog, args...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out.Bytes(), exit.ExitCode(), nil
		}
		return out.Bytes(), 0, err
	}
	grantFn      = Grant
	grantFilesFn = GrantFiles
	revokeFn     = Revoke
	runFn        = Run
	// remoteFn: a folder on another machine (a share, a mapped drive), whose ACL the
	// launcher never changes; an AppContainer cannot reach SMB shares anyway
	remoteFn = remotePath
)

// uncPath reports a \\server\share path (not a \\?\ or \\.\ local device path).
func uncPath(p string) bool {
	if !strings.HasPrefix(p, `\\`) && !strings.HasPrefix(p, "//") {
		return false
	}
	return !strings.HasPrefix(p, `\\?\`) && !strings.HasPrefix(p, `\\.\`)
}

// LaunchRunner is `maktaba-server runner-sandbox-run` (and aclaunch's runner-sandbox-run):
//
//	runner-sandbox-run -group name -run-dir dir [-areas-dir dir] [-protect dir]... [-grants file] [-memory bytes] \
//	    -- python -m maktaba_plugin_runner args...
//
// What the supervisor names on this command line is trusted; what the runner's policy names
// is not: its areas come from areas.json, so a path in it is never granted when it holds or
// is inside a -protect folder (the supervisor's token, the keys, the run folders), except
// the group's own -run-dir, which replaces the policy's run folder. -areas-dir (where the
// worker writes areas.json) is granted read on its own files only.
func LaunchRunner(args []string, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "runner-sandbox-run" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("runner-sandbox-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var protect list
	group := fs.String("group", "", "the runner group")
	runDir := fs.String("run-dir", "", "the group's own run folder (the only one it writes there)")
	areasDir := fs.String("areas-dir", "", "the folder of the worker's areas.json (read, its files only)")
	grantsFile := fs.String("grants", "", "where the profile's grants are kept between starts")
	memory := fs.Uint64("memory", 0, "the Job Object's memory cap in bytes (0: none)")
	fs.Var(&protect, "protect", "a folder no grant may hold or be inside (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 || !validGroup(*group) || *runDir == "" {
		fmt.Fprintln(stderr, "runner-sandbox-run: -group, -run-dir and a program to run after -- are required")
		return 2
	}
	fail := func(step string, err error) int {
		fmt.Fprintf(stderr, `{"event":"plugin_runner_sandbox_unavailable","group":%q,"step":%q,"detail":%q}`+"\n",
			*group, step, err.Error())
		return RunnerSandboxExit
	}
	raw, code, err := askPolicy(rest[0], append(append([]string{}, rest[1:]...), "--policy"), stderr)
	if err != nil {
		return fail("policy", err)
	}
	if code == RunnerUntrustedExit || code == RunnerEnvExit {
		return code
	}
	if code != 0 {
		return fail("policy", fmt.Errorf("the runner's --policy exited %d", code))
	}
	var p RunnerPolicy
	if err := json.Unmarshal(lastLine(raw), &p); err != nil {
		return fail("policy", err)
	}
	// compared and granted as the filesystem names them (links, junctions and short names
	// resolved), so no spelling of a path slips past the protected folders
	if err := canonicalPolicy(&p); err != nil {
		return fail("policy_path", err)
	}
	own, err := canonical(*runDir)
	if err != nil {
		return fail("policy_path", err)
	}
	p.RunDir = own
	var guarded []string
	for _, pr := range protect {
		c, err := canonical(pr)
		if err != nil {
			return fail("policy_path", err)
		}
		guarded = append(guarded, c)
	}
	data, err := canonical(os.Getenv("MAKTABA_DATA_DIR"))
	if err != nil {
		return fail("policy_path", err)
	}
	now := p.Grants(data)
	if err := CheckGrants(now, guarded, own); err != nil {
		return fail("policy_path", err)
	}
	profile := RunnerProfile(*group)
	var before []GrantEntry
	if *grantsFile != "" {
		b, err := os.ReadFile(*grantsFile)
		if err != nil && !os.IsNotExist(err) {
			return fail("grants_file", err)
		}
		if err == nil {
			if err := json.Unmarshal(b, &before); err != nil {
				return fail("grants_file", err)
			}
		}
	}
	// what this start leaves granted, kept before any grant is made: the new grants, and
	// any stale one that could not be taken back now (a folder not mounted: next time)
	keep := []GrantEntry{}
	for _, g := range now {
		if !remoteFn(g.Path) {
			keep = append(keep, GrantEntry{Path: g.Path, Write: g.Write})
		}
	}
	stale := Stale(before, now)
	var revoke []GrantEntry
	for _, g := range stale {
		if _, err := os.Stat(g.Path); err != nil {
			keep = append(keep, g)
			continue
		}
		if !remoteFn(g.Path) {
			revoke = append(revoke, g)
		}
	}
	if *grantsFile != "" {
		// the revocations below are recorded as kept until they are done
		b, _ := json.Marshal(append(append([]GrantEntry{}, keep...), revoke...))
		if err := writeFileAtomic(*grantsFile, b); err != nil {
			return fail("grants_file", err)
		}
	}
	for _, g := range revoke {
		if err := revokeFn(profile, g.Path); err != nil {
			return fail("revoke", err)
		}
	}
	for _, g := range now {
		if g.Create {
			if err := os.MkdirAll(g.Path, 0o700); err != nil {
				return fail("grant", err)
			}
		}
		if _, err := os.Stat(g.Path); err != nil {
			continue // not on this machine now
		}
		if remoteFn(g.Path) {
			// another machine's folder: its ACL is not ours to change, and the AppContainer
			// cannot reach a share; a job refused there runs in the worker (sandbox_refused)
			fmt.Fprintf(stderr, `{"event":"plugin_runner_area_remote","group":%q,"path":%q}`+"\n", *group, g.Path)
			continue
		}
		// resolved again just before the grant: a link made since the check is refused
		if again, err := canonical(g.Path); err != nil || pathKey(again) != pathKey(g.Path) {
			return fail("policy_path", fmt.Errorf("%s changed after it was checked", g.Path))
		}
		if err := grantFn(profile, g.Path, g.Write); err != nil {
			return fail("grant", err)
		}
	}
	if *areasDir != "" {
		if err := os.MkdirAll(*areasDir, 0o700); err != nil {
			return fail("grant", err)
		}
		if err := grantFilesFn(profile, *areasDir); err != nil {
			return fail("grant", err)
		}
	}
	if *grantsFile != "" {
		b, _ := json.Marshal(keep)
		if err := writeFileAtomic(*grantsFile, b); err != nil {
			return fail("grants_file", err)
		}
	}
	o := p.RunnerOptions(os.Environ())
	o.MemoryBytes = *memory
	code, err = runFn(profile, rest[0], rest[1:], o)
	if err != nil {
		return fail("start", err)
	}
	return code
}

// dosDevices are names Windows opens as a device in any folder, with any extension.
var dosDevices = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true, "conin$": true, "conout$": true}

func dosDevice(part string) bool {
	base := strings.ToLower(part)
	if k := strings.IndexByte(base, '.'); k >= 0 {
		base = base[:k]
	}
	if dosDevices[base] {
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) &&
		base[3] >= '0' && base[3] <= '9'
}

// badSpelling: a path the Windows APIs read otherwise than it is written: a device or
// verbatim prefix (\\?\, \\.\), a colon past the drive letter (a stream), a device name
// (CON, NUL, COM1 …), or a name ending in a dot or a space (Windows drops it).
func badSpelling(p string) bool {
	if strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `\\.\`) || strings.HasPrefix(p, `//?/`) || strings.HasPrefix(p, `//./`) {
		return true
	}
	rest := p
	if len(rest) >= 2 && rest[1] == ':' {
		rest = rest[2:]
	}
	if strings.Contains(rest, ":") {
		return true
	}
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '\\' || r == '/' }) {
		if part == "." || part == ".." || (len(part) == 2 && part[1] == ':') {
			continue
		}
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || dosDevice(part) {
			return true
		}
	}
	return false
}

// canonical is a path as the filesystem names it: absolute, links and junctions resolved
// (and on Windows short names expanded, as filepath.EvalSymlinks does). A path not made
// yet is resolved up to its nearest existing folder, but only past parts that really are
// missing: a link that does not resolve (dangling, or unreadable) is an error.
func canonical(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return p, nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rest := ""
	for cur := abs; ; {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		info, lerr := os.Lstat(cur)
		switch {
		case lerr == nil && info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0:
			return "", fmt.Errorf("%s: a link that does not resolve", cur)
		case lerr == nil:
			return "", fmt.Errorf("%s: %w", cur, err)
		case !os.IsNotExist(lerr):
			return "", fmt.Errorf("%s: %w", cur, lerr)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// canonicalPolicy refuses a policy path spelled to be read otherwise, and resolves the rest.
func canonicalPolicy(p *RunnerPolicy) error {
	for _, l := range []*[]string{&p.Read, &p.Write, &p.WriteNamed, &p.Exec, &p.Python, &p.ToolDirs} {
		for i, x := range *l {
			if badSpelling(x) {
				return fmt.Errorf("%s: a path spelled to be read otherwise", x)
			}
			c, err := canonical(x)
			if err != nil {
				return err
			}
			(*l)[i] = c
		}
	}
	return nil
}

// CheckGrants refuses a grant that would expose a protected folder: one that holds it (or
// is it), or one inside it other than the group's own run folder; and a filesystem root.
func CheckGrants(gs []GrantEntry, protect []string, runDir string) error {
	for _, g := range gs {
		clean := filepath.Clean(g.Path)
		if filepath.Dir(clean) == clean {
			return fmt.Errorf("%s: a filesystem root is never granted", g.Path)
		}
		for _, pr := range protect {
			if strings.TrimSpace(pr) == "" {
				continue
			}
			if under(pr, g.Path) {
				return fmt.Errorf("%s holds %s, which a runner never reaches", g.Path, pr)
			}
			if under(g.Path, pr) && !under(g.Path, runDir) {
				return fmt.Errorf("%s is inside %s, which a runner never reaches", g.Path, pr)
			}
		}
	}
	return nil
}

func validGroup(g string) bool {
	if g == "" || len(g) > 32 {
		return false
	}
	for _, r := range g {
		lower, digit := r >= 'a' && r <= 'z', r >= '0' && r <= '9'
		if !lower && !digit && r != '-' {
			return false
		}
	}
	return true
}

// lastLine is the last non-empty line (the policy; a log line before it is not JSON).
func lastLine(b []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	return bytes.TrimSpace(lines[len(lines)-1])
}

func writeFileAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
