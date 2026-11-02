package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Line is one added or removed line of a diff: its number on its own side (the new file
// for an added line, the old file for a removed one) and its text.
type Line struct {
	N    int
	Text string
}

// Change is one file the range changed.
type Change struct {
	Path    string // the path after the change (a rename's new name)
	OldPath string // a rename's old name, else ""
	Status  byte   // 'A' added, 'M' modified, 'D' deleted, 'R' renamed (also 'C', 'T')
	Added   []Line
	Removed []Line
}

// Repo is the tree at the range's head: its file list and their contents.
type Repo interface {
	Files() []string
	Read(path string) ([]byte, error)
}

// Range is a git range as `git diff` takes it: "base...head" (from their merge base, the
// default) or "base..head".
type Range struct {
	Base, Head string
	ThreeDot   bool
}

func parseRange(s string) (Range, error) {
	if i := strings.Index(s, "..."); i >= 0 {
		r := Range{Base: s[:i], Head: s[i+3:], ThreeDot: true}
		if r.Head == "" {
			r.Head = "HEAD"
		}
		if r.Base == "" {
			return Range{}, fmt.Errorf("range %q has no base", s)
		}
		return r, nil
	}
	if i := strings.Index(s, ".."); i >= 0 {
		r := Range{Base: s[:i], Head: s[i+2:]}
		if r.Head == "" {
			r.Head = "HEAD"
		}
		if r.Base == "" {
			return Range{}, fmt.Errorf("range %q has no base", s)
		}
		return r, nil
	}
	return Range{}, fmt.Errorf("range %q: want base...head or base..head", s)
}

func (r Range) String() string {
	if r.ThreeDot {
		return r.Base + "..." + r.Head
	}
	return r.Base + ".." + r.Head
}

// LogRange is the commits the range brings: those in head and not in base.
func (r Range) LogRange() string { return r.Base + ".." + r.Head }

// git runs git in dir and returns stdout.
func git(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func repoRoot(dir string) (string, error) {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// changes lists what the range changed, with each file's added and removed lines.
func changes(root string, r Range) ([]Change, error) {
	out, err := git(root, "diff", "--no-color", "--no-ext-diff", "-M", "-U0", r.String())
	if err != nil {
		return nil, err
	}
	return parseDiff(out), nil
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseDiff reads `git diff -U0` output.
func parseDiff(out []byte) []Change {
	var res []Change
	var cur *Change
	var oldN, newN int
	flush := func() {
		if cur != nil {
			res = append(res, *cur)
			cur = nil
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	inHunk := false
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "diff --git "):
			flush()
			cur = &Change{Status: 'M'}
			// a/x b/x: a fallback for the path when there are no ---/+++ lines (binary, mode)
			if a, b, ok := splitDiffGitLine(l); ok {
				cur.OldPath, cur.Path = a, b
			}
			inHunk = false
		case cur == nil:
			continue
		case !inHunk && strings.HasPrefix(l, "new file mode"):
			cur.Status = 'A'
		case !inHunk && strings.HasPrefix(l, "deleted file mode"):
			cur.Status = 'D'
		case !inHunk && strings.HasPrefix(l, "rename from "):
			cur.Status = 'R'
			cur.OldPath = strings.TrimPrefix(l, "rename from ")
		case !inHunk && strings.HasPrefix(l, "rename to "):
			cur.Path = strings.TrimPrefix(l, "rename to ")
		case !inHunk && strings.HasPrefix(l, "--- "):
			if p := strings.TrimPrefix(l, "--- "); p != "/dev/null" {
				cur.OldPath = unquoteGitPath(strings.TrimPrefix(p, "a/"))
			}
		case !inHunk && strings.HasPrefix(l, "+++ "):
			if p := strings.TrimPrefix(l, "+++ "); p != "/dev/null" {
				cur.Path = unquoteGitPath(strings.TrimPrefix(p, "b/"))
			}
		case strings.HasPrefix(l, "@@"):
			m := hunkRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			inHunk = true
			oldN, _ = strconv.Atoi(m[1])
			newN, _ = strconv.Atoi(m[3])
		case inHunk && strings.HasPrefix(l, "+"):
			cur.Added = append(cur.Added, Line{N: newN, Text: l[1:]})
			newN++
		case inHunk && strings.HasPrefix(l, "-"):
			cur.Removed = append(cur.Removed, Line{N: oldN, Text: l[1:]})
			oldN++
		}
	}
	flush()
	for i := range res {
		c := &res[i]
		if c.Status == 'D' {
			c.Path = c.OldPath
		}
		if c.Status != 'R' {
			if c.Status != 'D' {
				c.OldPath = ""
			}
		}
	}
	return res
}

func splitDiffGitLine(l string) (string, string, bool) {
	rest := strings.TrimPrefix(l, "diff --git ")
	i := strings.Index(rest, " b/")
	if !strings.HasPrefix(rest, "a/") || i < 0 {
		return "", "", false
	}
	return rest[2:i], rest[i+3:], true
}

func unquoteGitPath(p string) string {
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			return strings.TrimPrefix(strings.TrimPrefix(u, "a/"), "b/")
		}
	}
	return p
}

// commitMessages returns the full message of every commit the range brings.
func commitMessages(root string, r Range) ([]string, error) {
	out, err := git(root, "log", "--format=%B%x00", r.LogRange())
	if err != nil {
		return nil, err
	}
	var msgs []string
	for _, m := range strings.Split(string(out), "\x00") {
		if m = strings.TrimSpace(m); m != "" {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

// gitTree is the Repo at a revision, read through one `git cat-file --batch`.
type gitTree struct {
	root, rev string
	files     []string

	mu    sync.Mutex
	cmd   *exec.Cmd
	in    io.WriteCloser
	out   *bufio.Reader
	cache map[string][]byte
}

func openTree(root, rev string) (*gitTree, error) {
	out, err := git(root, "ls-tree", "-r", "-z", "--name-only", rev)
	if err != nil {
		return nil, err
	}
	t := &gitTree{root: root, rev: rev, cache: map[string][]byte{}}
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			t.files = append(t.files, f)
		}
	}
	return t, nil
}

func (t *gitTree) Files() []string { return t.files }

func (t *gitTree) Read(path string) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if b, ok := t.cache[path]; ok {
		return b, nil
	}
	if t.cmd == nil {
		t.cmd = exec.Command("git", "cat-file", "--batch")
		t.cmd.Dir = t.root
		var err error
		if t.in, err = t.cmd.StdinPipe(); err != nil {
			return nil, err
		}
		stdout, err := t.cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		t.out = bufio.NewReaderSize(stdout, 1<<16)
		if err := t.cmd.Start(); err != nil {
			t.cmd = nil
			return nil, err
		}
	}
	if _, err := fmt.Fprintf(t.in, "%s:%s\n", t.rev, path); err != nil {
		return nil, err
	}
	header, err := t.out.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == "missing" {
		return nil, fmt.Errorf("%s: not in %s", path, t.rev)
	}
	if len(fields) != 3 {
		return nil, fmt.Errorf("cat-file: unexpected header %q", header)
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, err
	}
	buf := make([]byte, size+1) // the content and its trailing newline
	if _, err := io.ReadFull(t.out, buf); err != nil {
		return nil, err
	}
	b := buf[:size]
	t.cache[path] = b
	return b, nil
}

func (t *gitTree) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd != nil {
		_ = t.in.Close()
		_ = t.cmd.Wait()
		t.cmd = nil
	}
}

// memRepo is a Repo held in memory (tests, and nothing else).
type memRepo map[string]string

func (m memRepo) Files() []string {
	fs := make([]string, 0, len(m))
	for f := range m {
		fs = append(fs, f)
	}
	return fs
}

func (m memRepo) Read(path string) ([]byte, error) {
	s, ok := m[path]
	if !ok {
		return nil, fmt.Errorf("%s: no such file", path)
	}
	return []byte(s), nil
}
