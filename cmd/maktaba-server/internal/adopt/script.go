package adopt

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Script is what a batch file that runs `maktaba-server serve` sets up for it,
// read (never run): the folder it runs in, its `set` lines, the server it runs
// and that server's flags. This PC's E:\tv\maktaba-local\start-maktaba-node.cmd
// is one: a restart loop around `bin\maktaba-server.exe serve`.
type Script struct {
	Path      string
	Dir       string            // the folder it changes to (cd /d), else its own
	Env       map[string]string // its `set NAME=VALUE` lines (expanded), by name
	EnvOrder  []string          // their names in order
	Server    string            // the maktaba-server it runs
	ServeArgs []string          // what follows `serve`, up to a redirection
	Log       string            // where its output goes (> file), when it says
	// Loops: it goes back (goto) to run the server again when it exits, after
	// RestartDelay (its `timeout /t N`, `ping -n N` or `choice /t N`; 0 when it
	// names none).
	Loops        bool
	RestartDelay time.Duration
}

var (
	cdLine  = regexp.MustCompile(`(?i)^cd(?:\s+/d)?\s+(.+)$`)
	setLine = regexp.MustCompile(`(?i)^set\s+"?([A-Za-z_][A-Za-z0-9_]*)=(.*?)"?$`)
	envRef  = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)
	// the waits a restart loop makes between two runs of the server
	timeoutLine = regexp.MustCompile(`(?i)^timeout(?:\.exe)?\s+(?:/t\s+)?(\d+)\b`)
	pingLine    = regexp.MustCompile(`(?i)^ping(?:\.exe)?\s.*-n\s+(\d+)\b`)
	choiceLine  = regexp.MustCompile(`(?i)^choice(?:\.exe)?\s.*/t\s+(\d+)\b`)
)

// waitOf is how long a line of a batch file waits (timeout, ping -n, choice /t).
func waitOf(line string) time.Duration {
	if m := timeoutLine.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[1])
		return time.Duration(n) * time.Second
	}
	if m := pingLine.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n > 1 {
			return time.Duration(n-1) * time.Second // ping -n N waits a second between its N echoes
		}
		return 0
	}
	if m := choiceLine.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[1])
		return time.Duration(n) * time.Second
	}
	return 0
}

// ParseScript reads a batch file's text. ok false when it never runs
// maktaba-server serve.
func ParseScript(path, text string, getenv func(string) string) (Script, bool) {
	own := filepath.Dir(path)
	s := Script{Path: path, Dir: own, Env: map[string]string{}}
	expand := func(v string) string {
		v = strings.ReplaceAll(v, "%~dp0", own+string(filepath.Separator))
		return envRef.ReplaceAllStringFunc(v, func(m string) string {
			name := m[1 : len(m)-1]
			if val, ok := s.Env[strings.ToUpper(name)]; ok {
				return val
			}
			if val := getenv(name); val != "" {
				return val
			}
			return m
		})
	}
	abs := func(p string) string {
		p = strings.Trim(p, `"`)
		switch {
		case p == "":
			return p
		case filepath.IsAbs(p) || isWinAbs(p):
			return filepath.Clean(p)
		}
		return filepath.Join(s.Dir, winSlash(p))
	}
	found := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		line = strings.TrimPrefix(line, "@")
		low := strings.ToLower(line)
		if strings.HasPrefix(low, "goto ") && found {
			s.Loops = true // back to run the server again
		}
		if d := waitOf(line); d > s.RestartDelay {
			s.RestartDelay = d
		}
		switch {
		case line == "", strings.HasPrefix(low, "rem ") || low == "rem", strings.HasPrefix(line, "::"),
			strings.HasPrefix(line, ":"), strings.HasPrefix(low, "echo "), strings.HasPrefix(low, "goto "):
			continue
		}
		if m := cdLine.FindStringSubmatch(line); m != nil {
			s.Dir = abs(expand(strings.TrimSpace(m[1])))
			continue
		}
		if m := setLine.FindStringSubmatch(line); m != nil {
			name := strings.ToUpper(m[1])
			if _, seen := s.Env[name]; !seen {
				s.EnvOrder = append(s.EnvOrder, name)
			}
			s.Env[name] = expand(m[2])
			continue
		}
		args := SplitArgs(expand(line))
		for i := 0; i+1 < len(args); i++ {
			base := strings.ToLower(filepath.Base(strings.ReplaceAll(args[i], `\`, "/")))
			if (base != "maktaba-server.exe" && base != "maktaba-server") || args[i+1] != "serve" {
				continue
			}
			s.Server = abs(args[i])
			s.ServeArgs = nil
			for j := i + 2; j < len(args); j++ {
				a := args[j]
				if strings.HasPrefix(a, ">") || strings.HasPrefix(a, "1>") || strings.HasPrefix(a, "2>") ||
					a == "|" || a == "&" || a == "&&" || a == "||" {
					if (a == ">" || a == "1>") && j+1 < len(args) {
						s.Log = abs(args[j+1])
					} else if strings.HasPrefix(a, ">") && len(a) > 1 && a != ">>" {
						s.Log = abs(a[1:])
					}
					break
				}
				s.ServeArgs = append(s.ServeArgs, a)
			}
			found = true
			break
		}
	}
	if !s.Loops {
		s.RestartDelay = 0
	}
	return s, found
}

var cmdSwitch = regexp.MustCompile(`(?i)(?:^|\s)/[ck](?:\s+|$)`)

// ConsoleScript is the batch file a console's command line runs (cmd.exe /c
// or /k, conhost.exe ... cmd.exe /c), as a whole path: a relative one against
// a `cd /d` before it in the same line, else against cwd, the process's
// current directory. "" when it runs none, or when a relative one cannot be
// placed (no directory to resolve it against).
func ConsoleScript(cmdline, cwd string) string {
	loc := cmdSwitch.FindStringIndex(cmdline)
	if loc == nil {
		return ""
	}
	rest := strings.TrimSpace(cmdline[loc[1]:])
	// cmd's own rule: unless the line is exactly one quoted name (two quotes,
	// no special character between them), a leading quote and the last one are
	// taken off
	one := strings.Count(rest, `"`) == 2 && strings.HasSuffix(rest, `"`) && !strings.ContainsAny(strings.Trim(rest, `"`), "&<>()@^|")
	if strings.HasPrefix(rest, `"`) && !one {
		rest = rest[1:]
		if i := strings.LastIndexByte(rest, '"'); i >= 0 {
			rest = rest[:i] + rest[i+1:]
		}
	}
	dir := cwd
	for _, part := range splitCommands(rest) {
		part = strings.TrimSpace(part)
		if m := cdLine.FindStringSubmatch(part); m != nil {
			d := strings.Trim(strings.TrimSpace(m[1]), `"`)
			switch {
			case filepath.IsAbs(d) || isWinAbs(d):
				dir = filepath.Clean(winSlash(d))
			case dir != "":
				dir = filepath.Join(dir, winSlash(d))
			}
			continue
		}
		args := SplitArgs(part)
		if len(args) > 1 && strings.EqualFold(args[0], "call") {
			args = args[1:]
		}
		if len(args) == 0 {
			continue
		}
		name := strings.Trim(strings.TrimSpace(args[0]), `"`)
		if !isBatch(name) {
			continue
		}
		switch {
		case filepath.IsAbs(name) || isWinAbs(name):
			return filepath.Clean(winSlash(name))
		case dir != "":
			return filepath.Join(dir, winSlash(name))
		}
		return ""
	}
	return ""
}

// splitCommands splits a cmd line at & and && (and ||) outside quotes.
func splitCommands(line string) []string {
	var out []string
	in, start := false, 0
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '"':
			in = !in
		case !in && (c == '&' || c == '|'):
			out = append(out, line[start:i])
			for i+1 < len(line) && (line[i+1] == '&' || line[i+1] == '|') {
				i++
			}
			start = i + 1
		}
	}
	return append(out, line[start:])
}

// isWinAbs: a drive-letter or UNC path (on every OS, for the tests).
func isWinAbs(p string) bool {
	return (len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')) || strings.HasPrefix(p, `\\`)
}
