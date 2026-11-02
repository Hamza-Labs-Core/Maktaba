package discovery

// The announcement through systemd-resolved, where it is the machine's mDNS responder
// (MulticastDNS=yes): its D-Bus RegisterService. Of resolved's two ways to publish a
// service this is the one a process can use without root: a .dnssd file goes in
// /etc/systemd/dnssd or /run/systemd/dnssd, which only root writes, and resolved reads
// them only when it starts or reloads. RegisterService is polkit's
// org.freedesktop.resolve1.register-service: root may, and whom a polkit rule names; the
// probe asks polkit first, and where it says no zeroconf announces under a name of its own
// instead (announce.go). resolved withdraws a registration once the connection that made
// it closes, so none outlives this process. The D-Bus binding is announce_resolved_linux.go;
// here is what is the same on every OS (so its tests run everywhere, with a fake).
// The same file is in api/internal/discovery and cmd/maktaba-server/internal/mdns (two
// modules); tools/ci/mdns_copies.py checks they stay the same.

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// resolvedState is what the probe found of systemd-resolved.
type resolvedState struct {
	// responds: resolved runs and answers mDNS (MulticastDNS=yes, globally and on a
	// link), and so for this machine's name
	responds bool
	// canRegister: it has RegisterService, and this process may call it
	canRegister bool
	// host is the name resolved answers for (its LLMNRHostname, "studio")
	host string
}

// resolvedService is one RegisterService call.
type resolvedService struct {
	// id is resolved's name for the registration, unique among its registrations
	id string
	// name is the instance name as resolved takes it (resolvedInstance): "Maktaba (studio)"
	name    string
	service string // "_maktaba._tcp"
	port    int
	txt     map[string][]byte // one TXT record
}

// resolvedAPI is systemd-resolved over one D-Bus connection.
type resolvedAPI interface {
	register(svc resolvedService) (path string, err error)
	unregister(path string)
	// restarted is told every time resolved starts again (its registrations are gone)
	restarted() <-chan struct{}
	// close closes the connection (resolved withdraws what it registered)
	close()
}

// resolvedIDs makes registration ids unique within this process.
var resolvedIDs atomic.Uint64

// systemResolved announces through a resolvedAPI. Every new TXT is a new registration
// (RegisterService cannot change one); one resolved refuses, and one a restarted resolved
// lost, is made again (after dnssdRestart when refused).
type systemResolved struct {
	api     resolvedAPI
	svc     resolvedService
	mu      sync.Mutex
	path    string
	retry   *time.Timer
	stopped bool
	done    chan struct{}
}

func startResolved(api resolvedAPI, instance, service, domain string, port int, txt []string) (*systemResolved, error) {
	if d := strings.TrimSuffix(domain, "."); d != "" && !strings.EqualFold(d, "local") {
		api.close()
		return nil, fmt.Errorf("systemd-resolved announces only in .local, not %q", domain)
	}
	d := &systemResolved{api: api, done: make(chan struct{}), svc: resolvedService{
		id:      fmt.Sprintf("maktaba-%d-%d", os.Getpid(), resolvedIDs.Add(1)),
		name:    resolvedInstance(instance),
		service: service,
		port:    port,
		txt:     resolvedTXT(txt),
	}}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.start(); err != nil {
		api.close()
		return nil, err
	}
	go d.watch()
	return d, nil
}

// start registers d.svc (d.mu held).
func (d *systemResolved) start() error {
	path, err := d.api.register(d.svc)
	if err != nil {
		return err
	}
	d.path = path
	return nil
}

// startOrRetry registers d.svc, and again after a moment while resolved refuses (d.mu
// held).
func (d *systemResolved) startOrRetry() {
	if d.retry != nil {
		d.retry.Stop()
		d.retry = nil
	}
	if err := d.start(); err != nil {
		d.retry = time.AfterFunc(dnssdRestart, d.again)
	}
}

func (d *systemResolved) again() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || d.path != "" {
		return
	}
	d.retry = nil
	d.startOrRetry()
}

// watch registers again whenever resolved restarts, until Shutdown.
func (d *systemResolved) watch() {
	restarted := d.api.restarted()
	for {
		select {
		case <-d.done:
			return
		case _, ok := <-restarted:
			if !ok {
				return
			}
			d.mu.Lock()
			if !d.stopped {
				d.path = "" // the restarted resolved has none of ours
				d.startOrRetry()
			}
			d.mu.Unlock()
		}
	}
}

// end withdraws the current registration (d.mu held).
func (d *systemResolved) end() {
	if d.retry != nil {
		d.retry.Stop()
		d.retry = nil
	}
	if d.path != "" {
		d.api.unregister(d.path)
		d.path = ""
	}
}

// SetText implements announcement.
func (d *systemResolved) SetText(txt []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.svc.txt = resolvedTXT(txt)
	if d.stopped {
		return
	}
	d.end()
	d.startOrRetry()
}

// Shutdown implements announcement.
func (d *systemResolved) Shutdown() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.stopped = true
	d.end()
	close(d.done)
	d.api.close()
}

// resolvedInstance is an instance name as RegisterService takes it. resolved expands "%"
// specifiers in it (a "%" is "%%"), then reads the result as a DNS name in presentation
// form when it joins it to the type (resolved-dnssd.c, dns_name_concat): a "." there splits
// the label ("Studio 1.2" was announced as "Studio 1" in a domain "2"), and a "\" starts an
// escape ("Back\slash" is one resolved cannot read, and resolved 255 aborts on it, an
// assertion in dns_zone_put, when it next refreshes its records). Both found by the real run
// in CI (mdns-linux). So "\" is "\\" and "." is "\.", and the escaped name, which resolved
// checks against the 63 bytes of a label, is cut on a character boundary to fit.
func resolvedInstance(s string) string {
	s = dnsLabel(s, 63)
	escape := strings.NewReplacer(`\`, `\\`, `.`, `\.`)
	esc := escape.Replace(s)
	for len(esc) > 63 {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
		esc = escape.Replace(s)
	}
	return strings.ReplaceAll(esc, "%", "%%")
}

// resolvedTXT is the TXT entries as one record of RegisterService's: key → value (an entry
// without "=" is a key with an empty value), each entry at most 255 bytes.
func resolvedTXT(txt []string) map[string][]byte {
	keys, values := txtPairs(txt)
	out := make(map[string][]byte, len(keys))
	for i, k := range keys {
		out[k] = []byte(values[i])
	}
	return out
}
