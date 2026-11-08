package appcontainer

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// A runner's library on a share (story 54.23 on Windows). An AppContainer with no
// capabilities cannot reach SMB at all, and the capabilities that would let it
// (privateNetworkClientServer with enterpriseAuthentication) open every share and host the
// user can reach. So the launcher, which runs outside the sandbox and waits on the runner
// anyway, brokers the group's share folders itself: the runner asks over a pipe only it can
// use (its AppContainer's SID on the pipe's DACL, its process id checked on each
// connection) for one file of a brokered folder, and the launcher opens that file, after
// the checks below, and duplicates the handle into the runner's process with read access:
// reads then go through the handle straight to the SMB redirector, no byte passes through
// the launcher. A subtitle (small) is written by the launcher itself from the bytes the
// runner sends, for a subtitle name in a folder the group writes by name
// (library.subtitles): the runner never holds a handle it could write with (nor turn into
// a link). A folder the group writes in full (files.write) on a share is never brokered:
// its jobs run in the worker, as before.

// EnvFilesPipe and EnvFilesRoots tell the runner its broker (winfiles.py).
const (
	EnvFilesPipe  = "MAKTABA_RUNNER_FILES_PIPE"
	EnvFilesRoots = "MAKTABA_RUNNER_FILES_ROOTS"
)

// BrokerRoot is one share folder a runner's broker serves.
type BrokerRoot struct {
	Path string `json:"path"`
	// WriteNamed: subtitle names may be written, replaced and removed in it
	WriteNamed bool `json:"write_named"`
}

// BrokerRequest is one line a runner sends its broker.
type BrokerRequest struct {
	// Op is open (to read), stat, list, write, replace or remove
	Op   string `json:"op"`
	Path string `json:"path"`
	To   string `json:"to,omitempty"`
	// Data is a write's whole file (at most MaxBrokeredWrite bytes)
	Data []byte `json:"data,omitempty"`
}

// MaxBrokeredWrite caps a subtitle the broker writes.
const MaxBrokeredWrite = 16 << 20

// BrokerReply is the broker's answer line.
type BrokerReply struct {
	Handle  uint64   `json:"handle,omitempty"`
	Size    int64    `json:"size,omitempty"`
	MtimeNs int64    `json:"mtime_ns,omitempty"`
	Dir     bool     `json:"dir,omitempty"`
	Names   []string `json:"names,omitempty"`
	Error   string   `json:"error,omitempty"`
	// Errno is 2 (no such file) or 13 (refused: the runner raises PermissionError, and a
	// job refused so runs in the worker, sandbox_refused)
	Errno int `json:"errno,omitempty"`
}

// errRefused: outside the brokered folders, or a name the group may not write.
var errRefused = errors.New("refused")

// subtitleName is the runner's SUBTITLE_NAME (sandbox.py), a name only (no folder).
var subtitleName = regexp.MustCompile(`^[^/\\]+\.[-A-Za-z0-9_]{1,35}\.(srt|vtt)(\.maktaba-tmp)?$`)

// shortAlias is an 8.3 short name (SUBTIT~1.SRT): another spelling of a long name, which
// would let a write by a subtitle-looking alias reach a name that is not one.
var shortAlias = regexp.MustCompile(`^[^.~]{1,6}~[0-9]{1,6}(\.[^.]{0,3})?$`)

// BrokerRoots are a group's brokered share folders.
type BrokerRoots []BrokerRoot

// asciiFold compares as Windows does for ASCII letters only: any other character must be
// the same (Go's Unicode folding and Windows' upcase table disagree, as for U+0130 and the
// Kelvin sign, which would let a request name a sibling folder as the root).
func asciiFold(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// underRoot reports p at or under root, compared by asciiFold.
func underRoot(p, root string) bool {
	k, rk := asciiFold(p), asciiFold(strings.TrimRight(root, `\`))
	return k == rk || strings.HasPrefix(k, rk+`\`)
}

// Confine answers the brokered folder a requested path is in, and the path as compared:
// a \\server\share path, spelled one way only (no . or .., no empty part, no stream, no
// device name, no trailing dot or space, no 8.3 alias), at or under one of the roots (any
// case, as Windows compares). For a write it must be a subtitle name directly or deeper in
// a folder the group writes by name, never the folder itself.
func (rs BrokerRoots) Confine(path string, write bool) (BrokerRoot, string, error) {
	p := strings.ReplaceAll(path, "/", `\`)
	if !uncPath(p) {
		return BrokerRoot{}, "", fmt.Errorf("%s: not on a share: %w", path, errRefused)
	}
	if badSpelling(p) {
		return BrokerRoot{}, "", fmt.Errorf("%s: a path spelled to be read otherwise: %w", path, errRefused)
	}
	parts := strings.Split(p[2:], `\`)
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || shortAlias.MatchString(part) {
			return BrokerRoot{}, "", fmt.Errorf("%s: a path spelled to be read otherwise: %w", path, errRefused)
		}
	}
	best := -1
	for i, r := range rs {
		if underRoot(p, strings.ReplaceAll(r.Path, "/", `\`)) && (best < 0 || len(r.Path) > len(rs[best].Path)) {
			best = i
		}
	}
	if best < 0 {
		return BrokerRoot{}, "", fmt.Errorf("%s: not in a folder this runner reads: %w", path, errRefused)
	}
	root := rs[best]
	if write {
		base := parts[len(parts)-1]
		if !root.WriteNamed || asciiFold(strings.TrimRight(root.Path, `\`)) == asciiFold(p) || !subtitleName.MatchString(base) {
			return BrokerRoot{}, "", fmt.Errorf("%s: only subtitle files are written here: %w", path, errRefused)
		}
	}
	return root, p, nil
}

// brokerRootsOf is the policy's share folders a broker serves (shared: a folder named by
// its \\server\share path): its read areas and the folders it writes by name; a share in
// a folder written in full is not brokered, nor a mapped drive (Confine serves only
// \\server\share paths).
func brokerRootsOf(p RunnerPolicy, shared func(string) bool) BrokerRoots {
	named := map[string]bool{}
	for _, x := range p.WriteNamed {
		named[pathKey(x)] = true
	}
	full := map[string]bool{}
	for _, x := range p.Write {
		full[pathKey(x)] = true
	}
	seen := map[string]bool{}
	var out BrokerRoots
	for _, x := range append(append([]string{}, p.Read...), p.WriteNamed...) {
		k := pathKey(x)
		if seen[k] || full[k] || !shared(x) {
			continue
		}
		seen[k] = true
		out = append(out, BrokerRoot{Path: x, WriteNamed: named[k]})
	}
	return out
}

// brokered reports whether a policy path is served by the broker.
func (rs BrokerRoots) brokered(path string) bool {
	for _, r := range rs {
		if pathKey(r.Path) == pathKey(path) {
			return true
		}
	}
	return false
}

// adminShare: a drive's or the system's administrative share (C$, ADMIN$, IPC$), which
// exposes a whole drive: a library there is never brokered.
func adminShare(root string) bool {
	parts := strings.Split(strings.TrimLeft(strings.ReplaceAll(root, "/", `\`), `\`), `\`)
	if len(parts) < 2 {
		return true
	}
	name := strings.ToUpper(parts[1])
	return name == "ADMIN$" || name == "IPC$" || name == "PRINT$" || (len(name) == 2 && name[1] == '$')
}

// check refuses a brokered folder on an administrative share, and one on a share this
// machine serves whose folder holds or is inside a protected folder (as CheckGrants
// refuses the same folder by its local path): local maps such a share to its folder.
func (rs BrokerRoots) check(local func(string) (string, bool, error), protect []string, runDir string) error {
	for _, r := range rs {
		if adminShare(r.Path) {
			return fmt.Errorf("%s: an administrative share is never brokered", r.Path)
		}
		p, ok, err := local(r.Path)
		if err != nil {
			return fmt.Errorf("%s: %w", r.Path, err)
		}
		if !ok {
			continue
		}
		c, err := canonical(p)
		if err != nil {
			return err
		}
		if err := CheckGrants([]GrantEntry{{Path: c}}, protect, runDir); err != nil {
			return fmt.Errorf("%s (this machine's %s): %w", r.Path, c, err)
		}
	}
	return nil
}
