package discovery

// Who answers the announcement. zeroconf answers it itself, and with it the address
// records of the host it names, flagged as its own. On a Mac that host is the Mac's
// Bonjour name, which mDNSResponder already answers for: two answerers for one name is a
// conflict, and macOS settles it by renaming the Mac ("…-2.local", and again for every
// Maktaba process that claims the new name). So a Mac announces through mDNSResponder
// itself (`dns-sd -R`), which publishes the service alone and points it at the name the
// Mac holds. cmd/maktaba-server/internal/mdns has the same file (another module).

import (
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// announcement is one registered service: its TXT changes in place, Shutdown withdraws
// it. *zeroconf.Server is one.
type announcement interface {
	SetText(txt []string)
	Shutdown()
}

// systemResponder is the system's own announcer when it has one ("" for none): dns-sd
// on macOS, whose mDNSResponder answers for the host name.
func systemResponder() string {
	return systemResponderFor(runtime.GOOS, exec.LookPath)
}

func systemResponderFor(goos string, lookPath func(string) (string, error)) string {
	if goos != "darwin" {
		return ""
	}
	path, err := lookPath("dns-sd")
	if err != nil {
		return ""
	}
	return path
}

// dnssdScript runs dns-sd ($0, its arguments after) until its stdin closes: our end
// closes on Shutdown, and when this process ends however it ends, so no announcement
// outlives the server. It exits when dns-sd does. A shell gives a background job /dev/null
// for stdin, so the watcher reads a copy of it (fd 3).
// Every value (the home's name among them) is an argument, never part of the script.
const dnssdScript = `exec 3<&0; "$0" "$@" </dev/null & p=$!; (read _ <&3; kill $p 2>/dev/null) & wait $p`

// dnssdRestart is how long a dns-sd that ended by itself (mDNSResponder restarted) waits
// before announcing again.
var dnssdRestart = 2 * time.Second

// systemDNSSD announces through `dns-sd -R instance service domain port txt…`.
type systemDNSSD struct {
	path    string
	args    []string
	mu      sync.Mutex
	txt     []string
	run     *dnssdRun
	stopped bool
}

// dnssdRun is one dns-sd: its stdin (closing it ends it) and done, closed once it ended.
type dnssdRun struct {
	stdin io.WriteCloser
	done  chan struct{}
}

func startDNSSD(path, instance, service, domain string, port int, txt []string) (*systemDNSSD, error) {
	d := &systemDNSSD{path: path, args: []string{"-R", dnsLabel(instance, 63), service, domain, strconv.Itoa(port)}, txt: txtStrings(txt)}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.start(); err != nil {
		return nil, err
	}
	return d, nil
}

// start runs a dns-sd with the current TXT (d.mu held).
func (d *systemDNSSD) start() error {
	argv := append([]string{"-c", dnssdScript, d.path}, d.args...)
	cmd := exec.Command("/bin/sh", append(argv, d.txt...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	run := &dnssdRun{stdin: stdin, done: make(chan struct{})}
	d.run = run
	go func() {
		_ = cmd.Wait()
		close(run.done)
		d.ended(run)
	}()
	return nil
}

// ended announces again, after a moment, when run ended by itself.
func (d *systemDNSSD) ended(run *dnssdRun) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || d.run != run {
		return
	}
	d.run = nil
	time.AfterFunc(dnssdRestart, d.again)
}

func (d *systemDNSSD) again() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || d.run != nil {
		return
	}
	if err := d.start(); err != nil {
		time.AfterFunc(dnssdRestart, d.again)
	}
}

// end stops the running dns-sd and waits for it to go (d.mu held), so the same name can be
// announced again at once without a conflict with itself.
func (d *systemDNSSD) end() {
	run := d.run
	if run == nil {
		return
	}
	d.run = nil
	_ = run.stdin.Close()
	select {
	case <-run.done:
	case <-time.After(2 * time.Second):
	}
}

// SetText implements announcement: dns-sd cannot change a TXT, so it announces anew.
func (d *systemDNSSD) SetText(txt []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.txt = txtStrings(txt)
	if d.stopped {
		return
	}
	d.end()
	if err := d.start(); err != nil {
		time.AfterFunc(dnssdRestart, d.again)
	}
}

// Shutdown implements announcement.
func (d *systemDNSSD) Shutdown() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	d.end()
}

// dnsLabel is s as DNS-SD takes it: no control characters, at most max bytes, cut on a
// character boundary. A longer instance name (a long home name) makes dns-sd refuse it,
// and the announcement would be retried for ever.
func dnsLabel(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	for len(s) > max {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

// txtStrings are the TXT entries as DNS-SD takes them: each at most 255 bytes.
func txtStrings(txt []string) []string {
	out := make([]string, 0, len(txt))
	for _, t := range txt {
		out = append(out, dnsLabel(t, 255))
	}
	return out
}
