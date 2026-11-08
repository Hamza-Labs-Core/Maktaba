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
// the checks below, and duplicates the handle into the runner's process with read access,
// or write access for a subtitle name in a folder the group writes by name
// (library.subtitles). Reads and writes then go through the handle straight to the SMB
// redirector: no byte passes through the launcher. A folder the group writes in full
// (files.write) on a share is never brokered: its jobs run in the worker, as before.

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
	// Op is open, stat, list, replace or remove
	Op    string `json:"op"`
	Path  string `json:"path"`
	To    string `json:"to,omitempty"`
	Write bool   `json:"write,omitempty"`
}

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
		rk := strings.ToLower(strings.TrimRight(strings.ReplaceAll(r.Path, "/", `\`), `\`))
		k := strings.ToLower(p)
		if (k == rk || strings.HasPrefix(k, rk+`\`)) && (best < 0 || len(rk) > len(strings.TrimRight(rs[best].Path, `\`))) {
			best = i
		}
	}
	if best < 0 {
		return BrokerRoot{}, "", fmt.Errorf("%s: not in a folder this runner reads: %w", path, errRefused)
	}
	root := rs[best]
	if write {
		base := parts[len(parts)-1]
		if !root.WriteNamed || strings.EqualFold(strings.TrimRight(root.Path, `\`), p) || !subtitleName.MatchString(base) {
			return BrokerRoot{}, "", fmt.Errorf("%s: only subtitle files are written here: %w", path, errRefused)
		}
	}
	return root, p, nil
}

// brokerRootsOf is the policy's share folders a broker serves: its read areas and the
// folders it writes by name; a share in a folder written in full is not brokered.
func brokerRootsOf(p RunnerPolicy, remote func(string) bool) BrokerRoots {
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
		if seen[k] || full[k] || !remote(x) {
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
