package discovery

// Who answers the announcement. zeroconf answers it itself, and with it the address
// records of the host it names, flagged as its own. That host is the machine's own name,
// which the system's responder already answers for where there is one: two answerers for
// one name is a conflict, and the system settles it by renaming the machine ("…-2.local",
// and again for every Maktaba process that claims the new name). So where the system has
// a responder the announcement goes through it, which publishes the service alone and
// points it at the name the machine holds:
//
//   - macOS: mDNSResponder, through `dns-sd -R`;
//   - Linux (any Unix): Avahi when avahi-daemon runs, through `avahi-publish-service`;
//     when it runs without that tool (no avahi-utils), zeroconf announces under a host
//     name of its own ("<host>-maktaba"), so the two never claim one name;
//   - Linux: systemd-resolved when it is the mDNS responder (MulticastDNS=yes, globally
//     and on a link), through its D-Bus RegisterService (announce_resolved*.go); where
//     this process may not register there (polkit lets root, or whom a rule names), or
//     resolved has no RegisterService, zeroconf announces under "<host>-maktaba" too;
//   - Windows 10+: the DNS Client's responder, through DnsServiceRegister (dnsapi.dll,
//     announce_dnsapi.go and announce_windows.go).
//
// zeroconf answers alone only where nothing else does. cmd/maktaba-server/internal/mdns has
// the same files (another module).

import (
	"fmt"
	"io"
	"os"
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

// responderKind is who announces the service.
type responderKind int

const (
	viaZeroconf        responderKind = iota // zeroconf, for the service and the host name
	viaDNSSD                                // macOS: dns-sd -R
	viaAvahi                                // avahi-publish-service
	viaZeroconfOwnHost                      // zeroconf, under a host name Avahi does not hold
	viaDNSAPI                               // Windows: DnsServiceRegister
	viaResolved                             // systemd-resolved: RegisterService (D-Bus)
)

// responder is the system's own announcer, and the command that reaches it (dns-sd,
// avahi-publish-service) or the host name it answers for (systemd-resolved's).
type responder struct {
	kind responderKind
	path string
	host string
}

// responderEnv is what choosing a responder looks at (fakes in tests).
type responderEnv struct {
	goos     string
	lookPath func(string) (string, error)
	exists   func(string) bool
	dnsapi   func() bool
	resolved func() resolvedState
}

// avahiSockets are where a running avahi-daemon listens.
var avahiSockets = []string{"/run/avahi-daemon/socket", "/var/run/avahi-daemon/socket"}

// systemResponder is this machine's announcer.
func systemResponder() responder {
	return systemResponderFor(responderEnv{
		goos: runtime.GOOS, lookPath: exec.LookPath, exists: fileExists, dnsapi: dnsapiAvailable, resolved: probeResolved,
	})
}

func systemResponderFor(env responderEnv) responder {
	switch env.goos {
	case "darwin":
		if path, err := env.lookPath("dns-sd"); err == nil {
			return responder{kind: viaDNSSD, path: path}
		}
	case "windows":
		if env.dnsapi() {
			return responder{kind: viaDNSAPI}
		}
	default:
		if avahiRunning(env.exists) {
			if path, err := env.lookPath("avahi-publish-service"); err == nil {
				return responder{kind: viaAvahi, path: path}
			}
			return responder{kind: viaZeroconfOwnHost}
		}
		if env.goos != "linux" {
			break
		}
		if st := env.resolved(); st.responds {
			if st.canRegister && st.host != "" {
				return responder{kind: viaResolved, host: st.host}
			}
			return responder{kind: viaZeroconfOwnHost}
		}
	}
	return responder{kind: viaZeroconf}
}

// avahiRunning is whether avahi-daemon runs here (and so answers for the host name).
func avahiRunning(exists func(string) bool) bool {
	for _, s := range avahiSockets {
		if exists(s) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dnssdScript runs the responder's command (dns-sd or avahi-publish-service: $0, its
// arguments after) until its stdin closes: our end closes on Shutdown, and when this
// process ends however it ends, so no announcement outlives the server. It exits when the
// command does. A shell gives a background job /dev/null for stdin, so the watcher reads
// a copy of it (fd 3).
// Every value (the home's name among them) is an argument, never part of the script.
const dnssdScript = `exec 3<&0; "$0" "$@" </dev/null & p=$!; (read _ <&3; kill $p 2>/dev/null) & wait $p`

// dnssdRestart is how long an announcement that ended by itself (mDNSResponder or
// avahi-daemon restarted, a registration Windows refused) waits before announcing again.
var dnssdRestart = 2 * time.Second

// systemDNSSD announces through a command that announces until it is ended:
// `dns-sd -R instance service domain port txt…` or
// `avahi-publish-service -- instance service port txt…`.
type systemDNSSD struct {
	path    string
	args    []string
	mu      sync.Mutex
	txt     []string
	run     *dnssdRun
	stopped bool
}

// dnssdRun is one command: its stdin (closing it ends it) and done, closed once it ended.
type dnssdRun struct {
	stdin io.WriteCloser
	done  chan struct{}
}

func startDNSSD(path, instance, service, domain string, port int, txt []string) (*systemDNSSD, error) {
	return startCommand(path, []string{"-R", dnsLabel(instance, 63), service, domain, strconv.Itoa(port)}, txt)
}

// startAvahi announces through avahi-publish-service, which avahi-daemon answers for. A
// name already taken there Avahi renames itself; "--" keeps a name that starts with "-"
// from reading as an option.
func startAvahi(path, instance, service, domain string, port int, txt []string) (*systemDNSSD, error) {
	var args []string
	if d := strings.TrimSuffix(domain, "."); d != "" && !strings.EqualFold(d, "local") {
		args = append(args, "-d", d)
	}
	args = append(args, "--", dnsLabel(instance, 63), service, strconv.Itoa(port))
	return startCommand(path, args, txt)
}

func startCommand(path string, args, txt []string) (*systemDNSSD, error) {
	d := &systemDNSSD{path: path, args: args, txt: txtStrings(txt)}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.start(); err != nil {
		return nil, err
	}
	return d, nil
}

// start runs the command with the current TXT (d.mu held).
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

// end stops the running command and waits for it to go (d.mu held), so the same name can
// be announced again at once without a conflict with itself.
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

// SetText implements announcement: neither command can change a TXT, so it announces
// anew.
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

// dnsLabel is s as DNS-SD takes it: no control characters, at most limit bytes, cut on a
// character boundary. A longer instance name (a long home name) makes the responder refuse
// it, and the announcement would be retried for ever.
func dnsLabel(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	for len(s) > limit {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

// windowsInstance is an instance name as DnsServiceConstructInstance can take it.
// Windows takes the whole name ("<instance>._maktaba._tcp.local") and splits it at every
// ".", escaped or not: RFC 6763's "\." reaches the wire as a label ending in "\" and the
// rest as another (read off the adapter in CI), so a home named "Studio 1.2" would be
// announced as "Studio 1\" in a domain "2". No escape keeps a "." in the label there, so
// a "." is U+2024 ONE DOT LEADER, which reads the same. A "\" Windows keeps as it is. Not
// for dns-sd -R, avahi-publish-service and RegisterService: they take the instance name
// alone, a "." in it included, and escape it themselves (dns-sd checked on macOS).
func windowsInstance(s string) string {
	return strings.ReplaceAll(s, ".", "\u2024")
}

// zeroconfInstance is an instance name as zeroconf takes it: the label as the DNS library
// under it writes one (github.com/miekg/dns: a backslash before ".", "\" and its other
// special characters, "\DDD" for a byte outside printable ASCII), so the name it answers
// with is one label, and a query for it, which the library writes the same way, matches
// it. zeroconf trims dots at the ends of the name: a last "." is "\046".
func zeroconfInstance(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.' && i == len(s)-1:
			b.WriteString(`\046`)
		case strings.IndexByte(`. '@;()"\`, c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < ' ' || c > '~':
			fmt.Fprintf(&b, "\\%03d", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// txtStrings are the TXT entries as DNS-SD takes them: each at most 255 bytes.
func txtStrings(txt []string) []string {
	out := make([]string, 0, len(txt))
	for _, t := range txt {
		out = append(out, dnsLabel(t, 255))
	}
	return out
}
