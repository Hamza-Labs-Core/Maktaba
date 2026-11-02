package mdns

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeResolved is systemd-resolved over D-Bus: it records registrations (the first refuse
// are refused) and withdrawals, and restarts on demand.
type fakeResolved struct {
	mu      sync.Mutex
	regs    []resolvedService
	live    map[string]bool // path → registered
	refuse  int
	starts  chan struct{}
	closed  bool
	nextReg int
}

func newFakeResolved() *fakeResolved {
	return &fakeResolved{live: map[string]bool{}, starts: make(chan struct{}, 1)}
}

func (f *fakeResolved) register(svc resolvedService) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.regs = append(f.regs, svc)
	if f.refuse > 0 {
		f.refuse--
		return "", errors.New("refused")
	}
	f.nextReg++
	path := "/org/freedesktop/resolve1/dnssd/" + svc.id + "/" + string(rune('0'+f.nextReg))
	f.live[path] = true
	return path, nil
}

func (f *fakeResolved) unregister(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, path)
}

func (f *fakeResolved) restarted() <-chan struct{} { return f.starts }

func (f *fakeResolved) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

// restart is resolved starting again: what it held is gone.
func (f *fakeResolved) restart() {
	f.mu.Lock()
	f.live = map[string]bool{}
	f.mu.Unlock()
	f.starts <- struct{}{}
}

func (f *fakeResolved) state() (regs []resolvedService, live int, closed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]resolvedService(nil), f.regs...), len(f.live), f.closed
}

// registrations waits up to a few seconds for at least n RegisterService calls.
func (f *fakeResolved) registrations(t *testing.T, n int) []resolvedService {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		regs, _, _ := f.state()
		if len(regs) >= n {
			return regs
		}
		if time.Now().After(deadline) {
			t.Fatalf("wanted %d registrations, have %d", n, len(regs))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// resolved gets the instance name alone ("%" doubled: resolved expands specifiers; a "."
// as it is: resolved escapes the label itself), the type, port and TXT as one record; a
// new TXT withdraws that registration and makes one with it; a restarted resolved is
// given it again; Shutdown withdraws it and closes the connection, and nothing registers
// again.
func TestResolvedAnnouncesChangesRestartsAndWithdraws(t *testing.T) {
	f := newFakeResolved()
	a, err := startResolved(f, "Maktaba (100% v1.2)\n", "_maktaba._tcp", "local.", 8080, []string{"role=home", "web=8088", "flag"})
	if err != nil {
		t.Fatal(err)
	}
	first := f.registrations(t, 1)[0]
	if first.name != "Maktaba (100%% v1.2)" || first.service != "_maktaba._tcp" || first.port != 8080 {
		t.Fatalf("registered %+v", first)
	}
	if !strings.HasPrefix(first.id, "maktaba-") {
		t.Fatalf("id %q", first.id)
	}
	wantTXT := map[string][]byte{"role": []byte("home"), "web": []byte("8088"), "flag": {}}
	if !reflect.DeepEqual(first.txt, wantTXT) {
		t.Fatalf("txt %q", first.txt)
	}

	a.SetText([]string{"role=home", "web=8090"})
	regs := f.registrations(t, 2)
	if _, live, _ := f.state(); live != 1 {
		t.Fatalf("after SetText %d registered, want 1", live)
	}
	if string(regs[1].txt["web"]) != "8090" || regs[1].id != first.id || regs[1].name != first.name {
		t.Fatalf("after SetText: %+v", regs[1])
	}

	f.restart()
	regs = f.registrations(t, 3)
	if string(regs[2].txt["web"]) != "8090" {
		t.Fatalf("after a restart: %+v", regs[2])
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, live, _ := f.state(); live == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not registered again after resolved restarted")
		}
		time.Sleep(10 * time.Millisecond)
	}

	a.Shutdown()
	if _, live, closed := f.state(); live != 0 || !closed {
		t.Fatalf("after Shutdown: %d registered, connection closed %v", live, closed)
	}
	a.SetText([]string{"role=home"})
	a.Shutdown()
	time.Sleep(50 * time.Millisecond)
	if regs, _, _ := f.state(); len(regs) != 3 {
		t.Fatalf("registered again after Shutdown: %d", len(regs))
	}
}

// Two announcements of one process are two registrations to resolved, never one id.
func TestResolvedIDsAreUnique(t *testing.T) {
	t.Parallel()
	f, g := newFakeResolved(), newFakeResolved()
	a, err := startResolved(f, "Maktaba", "_maktaba._tcp", "local.", 8080, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown()
	b, err := startResolved(g, "Maktaba", "_maktaba._tcp", "local.", 8081, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Shutdown()
	if f.registrations(t, 1)[0].id == g.registrations(t, 1)[0].id {
		t.Fatal("same id twice")
	}
}

// A new TXT resolved refuses is made again, after a moment, until it is taken.
func TestResolvedRegistersAgainWhenRefused(t *testing.T) {
	was := dnssdRestart
	dnssdRestart = 20 * time.Millisecond
	t.Cleanup(func() { dnssdRestart = was })

	f := newFakeResolved()
	a, err := startResolved(f, "Maktaba", "_maktaba._tcp", "local.", 8080, []string{"web=8088"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown()
	f.mu.Lock()
	f.refuse = 2
	f.mu.Unlock()
	a.SetText([]string{"web=8090"})
	f.registrations(t, 4)
	time.Sleep(100 * time.Millisecond)
	regs, live, _ := f.state()
	if len(regs) != 4 || live != 1 {
		t.Fatalf("%d registrations, %d registered; once taken it should stay", len(regs), live)
	}
}

// One resolved refuses at once fails the announcement (the caller logs it) and closes
// its connection; so does a domain other than .local, which resolved does not announce in.
func TestResolvedReportsARefusal(t *testing.T) {
	t.Parallel()
	f := newFakeResolved()
	f.refuse = 1
	if _, err := startResolved(f, "Maktaba", "_maktaba._tcp", "local.", 8080, nil); err == nil {
		t.Fatal("no error")
	}
	if _, _, closed := f.state(); !closed {
		t.Fatal("connection left open")
	}
	g := newFakeResolved()
	if _, err := startResolved(g, "Maktaba", "_maktaba._tcp", "example.org.", 8080, nil); err == nil {
		t.Fatal("no error for example.org")
	}
	if regs, _, closed := g.state(); len(regs) != 0 || !closed {
		t.Fatalf("example.org: %d registrations, closed %v", len(regs), closed)
	}
}

// Shutdown while a refused registration waits to be made again: it is not made.
func TestResolvedShutdownStopsRetries(t *testing.T) {
	was := dnssdRestart
	dnssdRestart = 30 * time.Millisecond
	t.Cleanup(func() { dnssdRestart = was })

	f := newFakeResolved()
	a, err := startResolved(f, "Maktaba", "_maktaba._tcp", "local.", 8080, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.refuse = 100
	f.mu.Unlock()
	a.SetText([]string{"web=8090"})
	a.Shutdown()
	time.Sleep(150 * time.Millisecond)
	if regs, _, _ := f.state(); len(regs) > 2 {
		t.Fatalf("registered %d times after Shutdown", len(regs))
	}
}
