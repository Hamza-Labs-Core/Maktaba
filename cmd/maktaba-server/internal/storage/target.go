package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// TargetError is a target folder refused before anything looks at it.
type TargetError struct{ Note Note }

func (e *TargetError) Error() string { return e.Note.Message }

// systemDirs are folders Maktaba's data never goes in (nor under).
func systemDirs() []string {
	if runtime.GOOS == "windows" {
		win := os.Getenv("SystemRoot")
		if win == "" {
			win = `C:\Windows`
		}
		return []string{win, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramData")}
	}
	return []string{"/System", "/usr", "/bin", "/sbin", "/etc", "/dev", "/proc", "/sys", "/boot", "/lib", "/lib64",
		"/private/etc", "/private/var/db", "/Library", "/Applications", "/opt", "/run/systemd", "/var/lib/dpkg"}
}

// CheckTarget is the one way a folder a user typed becomes a move's or setup's
// target: it is trimmed, must be a whole path with no ".." in it, is cleaned and
// resolved through the links of its folders that exist, must not be a system
// folder (nor the top of a disk), and, when roots are given (the mount points of
// the disks the server itself listed), must be on one of them. It returns the
// checked path; nothing else looks at what the user typed.
func CheckTarget(raw string, roots []string) (string, error) {
	t := strings.TrimSpace(raw)
	if t == "" || !filepath.IsAbs(t) {
		return "", &TargetError{note("target_relative", "the folder must be a whole path (one that starts at the top of a disk)")}
	}
	for _, part := range strings.FieldsFunc(t, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", &TargetError{note("target_relative", "the folder must not go up with \"..\"")}
		}
	}
	t = filepath.Clean(t)
	if strings.Contains(t, "..") {
		return "", &TargetError{note("target_relative", "the folder must not go up with \"..\"")}
	}
	resolved, err := resolve(t)
	if err != nil {
		return "", &TargetError{note("target_unwritable", "cannot read "+t+": "+err.Error(), "error", err.Error())}
	}
	if resolved == filepath.Dir(resolved) {
		return "", &TargetError{note("target_system", resolved+" is the top of a disk: choose a folder on it")}
	}
	for _, s := range systemDirs() {
		if s != "" && withinFold(resolved, s) {
			return "", &TargetError{note("target_system", resolved+" is a system folder: choose a folder of your own",
				"path", resolved)}
		}
	}
	if len(roots) > 0 {
		on := false
		for _, r := range roots {
			on = on || (r != "" && withinFold(resolved, r))
		}
		if !on {
			return "", &TargetError{note("target_not_on_disk", resolved+" is not on a disk of this computer",
				"path", resolved)}
		}
	}
	return resolved, nil
}

// resolve is p with the links of its folders that exist resolved (the rest, not
// made yet, joined back).
func resolve(p string) (string, error) {
	var rest []string
	cur := p
	for {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(append([]string{r}, rest...)...), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		up := filepath.Dir(cur)
		if up == cur {
			return p, nil
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = up
	}
}

// withinFold is within, without regard to case on Windows and macOS (their disks'
// usual file systems).
func withinFold(p, root string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		p, root = strings.ToLower(p), strings.ToLower(root)
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
