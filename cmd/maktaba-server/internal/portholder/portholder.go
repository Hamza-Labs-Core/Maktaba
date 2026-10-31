// Package portholder finds the process listening on a TCP port, as far as the OS
// lets us see: lsof on a Mac and on Linux (/proc when lsof is not there), netstat
// and tasklist on Windows. The supervisor names it when a role's port is taken
// (supervisor/portcheck.go), so a clash says who holds the port instead of
// showing as a role's opaque crash loop.
package portholder

import (
	"bufio"
	"context"
	"encoding/csv"
	"strconv"
	"strings"
	"time"
)

// Holder is the process listening on a port.
type Holder struct {
	PID     int
	Command string // its program's name ("maktaba-pipeline", "python3.12"); "" when unknown
}

// Timeout bounds one lookup: the commands it runs answer in well under it, and a
// start waits on it.
var Timeout = 3 * time.Second

// Find is the process listening on TCP port, ok false when none could be found
// (nothing listens, the OS keeps it from us, or the tool is missing).
func Find(port int) (Holder, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	return find(ctx, port)
}

// parseLsof reads `lsof -F pc` output: a "p<pid>" line, then its "c<command>".
func parseLsof(out string) (Holder, bool) {
	var h Holder
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			if h.PID != 0 {
				return h, true // the first listener's
			}
			h.PID, _ = strconv.Atoi(line[1:])
		case 'c':
			if h.PID != 0 && h.Command == "" {
				h.Command = line[1:]
			}
		}
	}
	return h, h.PID != 0
}

// parseNetstat finds the pid listening on port in `netstat -ano -p TCP` output
// (Windows): "  TCP    127.0.0.1:9111    0.0.0.0:0    LISTENING    4242".
func parseNetstat(out string, port int) (int, bool) {
	suffix := ":" + strconv.Itoa(port)
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || !strings.EqualFold(f[0], "TCP") || !strings.EqualFold(f[3], "LISTENING") {
			continue
		}
		if !strings.HasSuffix(f[1], suffix) {
			continue
		}
		if pid, err := strconv.Atoi(f[4]); err == nil && pid > 0 {
			return pid, true
		}
	}
	return 0, false
}

// parseTasklist reads `tasklist /FI "PID eq N" /FO CSV /NH`: "\"name.exe\",\"N\",...".
func parseTasklist(out string) string {
	r := csv.NewReader(strings.NewReader(strings.TrimSpace(out)))
	rec, err := r.Read()
	if err != nil || len(rec) < 2 {
		return ""
	}
	return rec[0]
}

// parseProcNetTCP finds the socket inode listening on port in /proc/net/tcp or
// tcp6 (Linux): local_address is hex "0100007F:23A7", st 0A is LISTEN.
func parseProcNetTCP(table string, port int) (string, bool) {
	want := strings.ToUpper(strconv.FormatInt(int64(port), 16))
	for len(want) < 4 {
		want = "0" + want
	}
	sc := bufio.NewScanner(strings.NewReader(table))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != "0A" {
			continue
		}
		if _, p, ok := strings.Cut(f[1], ":"); ok && strings.EqualFold(p, want) {
			return f[9], true
		}
	}
	return "", false
}
