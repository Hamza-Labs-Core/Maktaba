package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// selfPath is this launcher's executable, links resolved.
func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

// takeReplaceRequest consumes the server's request to hand over to a new launcher
// (layout.ReplaceLauncherFile: the launcher to run instead, put in place already); ""
// when there is none, or it names nothing runnable.
func (ln *launcher) takeReplaceRequest() string {
	path := filepath.Join(layout.New(ln.cfg.Root).UpdatesDir(), layout.ReplaceLauncherFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_ = os.Remove(path)
	next := strings.TrimSpace(string(b))
	if next == "" || !filepath.IsAbs(next) {
		return ""
	}
	if info, err := os.Stat(next); err != nil || info.IsDir() {
		return ""
	}
	return next
}
