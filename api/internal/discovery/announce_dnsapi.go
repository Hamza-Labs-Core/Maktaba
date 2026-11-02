package discovery

// The announcement through Windows' own responder (DnsServiceRegister, Windows 10+). The
// registration is asynchronous: Windows answers later, through a callback, whether it
// took it. The binding to dnsapi.dll is announce_windows.go; here is what is the same on
// every OS (so its tests run everywhere, with a fake dnsAPI).
// cmd/maktaba-server/internal/mdns has the same file (another module).

import (
	"net"
	"strings"
	"sync"
	"time"
)

// dnsAPI registers services with the system's responder (dnsapi.dll on Windows).
type dnsAPI interface {
	// register asks for svc; done is called once, later, with the outcome (nil: it is
	// announced). An error is a request Windows did not take at all.
	register(svc winService, done func(error)) (winRegistration, error)
}

// winRegistration is one registration; deregister withdraws it.
type winRegistration interface {
	deregister()
}

// winService is a service as DnsServiceConstructInstance takes it.
type winService struct {
	// instance is the full name: "Maktaba (studio)._maktaba._tcp.local"
	instance string
	// host is the target, "<host>.local", and ip4/ip6 its addresses ("" for none)
	host     string
	ip4, ip6 string
	port     int
	// keys and values are the TXT entries, split at their first "="
	keys, values []string
}

// systemDNSAPI announces through a dnsAPI. Every new TXT is a new registration (Windows
// cannot change one in place); one Windows refuses is tried again after dnssdRestart.
type systemDNSAPI struct {
	api     dnsAPI
	svc     winService
	mu      sync.Mutex
	reg     winRegistration
	gen     int
	stopped bool
}

func startDNSAPI(api dnsAPI, instance, service, domain, host string, port int, ips, txt []string) (*systemDNSAPI, error) {
	d := &systemDNSAPI{api: api, svc: winService{
		// the whole name: a "." of the instance's would split it (announce.go)
		instance: dnsLabel(windowsInstance(instance), 63) + "." + service + "." + strings.TrimSuffix(domain, "."),
		host:     host,
		port:     port,
	}}
	d.svc.ip4, d.svc.ip6 = firstIPs(ips)
	d.svc.keys, d.svc.values = txtPairs(txt)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.start(); err != nil {
		return nil, err
	}
	return d, nil
}

// start registers d.svc (d.mu held).
func (d *systemDNSAPI) start() error {
	d.gen++
	gen := d.gen
	// done may come at once, from register itself: it must not wait for d.mu here
	reg, err := d.api.register(d.svc, func(err error) { go d.completed(gen, err) })
	if err != nil {
		return err
	}
	d.reg = reg
	return nil
}

// completed is Windows' answer to registration gen: refused, it is tried again.
func (d *systemDNSAPI) completed(gen int, err error) {
	if err == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || d.gen != gen {
		return
	}
	d.end()
	time.AfterFunc(dnssdRestart, func() { d.again(gen) })
}

// again registers anew when nothing has since (registration gen is still the last).
func (d *systemDNSAPI) again(gen int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || d.gen != gen || d.reg != nil {
		return
	}
	if err := d.start(); err != nil {
		retry := d.gen
		time.AfterFunc(dnssdRestart, func() { d.again(retry) })
	}
}

// end withdraws the current registration (d.mu held).
func (d *systemDNSAPI) end() {
	if d.reg != nil {
		d.reg.deregister()
		d.reg = nil
	}
}

// SetText implements announcement.
func (d *systemDNSAPI) SetText(txt []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.svc.keys, d.svc.values = txtPairs(txt)
	if d.stopped {
		return
	}
	d.end()
	if err := d.start(); err != nil {
		gen := d.gen
		time.AfterFunc(dnssdRestart, func() { d.again(gen) })
	}
}

// Shutdown implements announcement.
func (d *systemDNSAPI) Shutdown() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	d.end()
}

// txtPairs are the TXT entries as keys and values (an entry without "=" is a key alone),
// each entry at most 255 bytes.
func txtPairs(txt []string) (keys, values []string) {
	for _, t := range txtStrings(txt) {
		k, v, _ := strings.Cut(t, "=")
		keys = append(keys, k)
		values = append(values, v)
	}
	return keys, values
}

// firstIPs are the first IPv4 and the first IPv6 of ips ("" for none).
func firstIPs(ips []string) (ip4, ip6 string) {
	for _, s := range ips {
		ip := net.ParseIP(s)
		switch {
		case ip == nil:
		case ip.To4() != nil:
			if ip4 == "" {
				ip4 = s
			}
		case ip6 == "":
			ip6 = s
		}
	}
	return ip4, ip6
}
