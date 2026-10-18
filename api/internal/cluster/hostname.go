package cluster

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// StableHostName is the machine's stable host name: on macOS the Bonjour
// LocalHostName (`scutil --get LocalHostName`, which does not change with
// the network the way `hostname` can), elsewhere os.Hostname. The domain
// part and a trailing ".local" are dropped.
func StableHostName() string {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "scutil", "--get", "LocalHostName").Output(); err == nil {
			if n := cleanHost(string(out)); n != "" {
				return n
			}
		}
	}
	if h, err := os.Hostname(); err == nil {
		if n := cleanHost(h); n != "" {
			return n
		}
	}
	return "maktaba"
}

func cleanHost(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".local")
	if i := strings.IndexByte(s, '.'); i > 0 {
		s = s[:i]
	}
	return s
}
