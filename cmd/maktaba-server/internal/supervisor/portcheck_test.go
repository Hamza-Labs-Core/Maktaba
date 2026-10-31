package supervisor

import (
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/portholder"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// defaultBinds are every process's default addresses: the roles, the index worker,
// every transcription worker, the TV slots, the relay agent and serve's own.
func defaultBinds() []ports.Bind {
	cfg := config.Defaults()
	var out []ports.Bind
	for _, r := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		for _, b := range roleBinds(r, cfg) {
			b.Owner = string(r)
			out = append(out, b)
		}
	}
	out = append(out, ports.Bind{Addr: pipelineSearch(cfg), Owner: "pipeline/index", What: "search port"},
		ports.Bind{Addr: pipelineIndexLogs(cfg), Owner: "pipeline/index", What: "logs port"},
		ports.Bind{Addr: RelayAdminAddr(cfg), Owner: "relay", What: "status port"},
		ports.Bind{Addr: cfg.Server.WebListen, Owner: "serve", What: "web port"},
		ports.Bind{Addr: config.DefaultSupervisor().AdminAddr, Owner: "serve", What: "supervisor port"})
	for i := 1; i <= MaxPipelineInstances; i++ {
		out = append(out, ports.Bind{Addr: PipelineInstanceLogsAddr(i), Owner: "pipeline/stt-" + strconv.Itoa(i), What: "logs port"})
	}
	for _, s := range SlotNames {
		out = append(out, ports.Bind{Addr: SlotAddr(s), Owner: "tv-slot/" + s, What: "channel port"},
			ports.Bind{Addr: SlotAdminAddr(s), Owner: "tv-slot/" + s, What: "admin port"})
	}
	return out
}

// TestDefaultsAreTheRegistrys: every default address the supervisor gives a process
// is a port of the registry (shared/go/ports) owned by that process, and none
// clashes with another (the registry's own test covers every entry; this one, that
// the supervisor takes its ports from it). It replaces the list this package kept
// by hand, which had the transcription workers on the TV slots' admin ports.
func TestDefaultsAreTheRegistrys(t *testing.T) {
	binds := defaultBinds()
	for _, b := range binds {
		e, _, ok := ports.Lookup(b.Port())
		if !ok || e.Foreign {
			t.Errorf("%s is no port of the registry", b)
			continue
		}
		if owner := strings.SplitN(b.Owner, "/", 2)[0]; !strings.HasPrefix(e.Owner, owner) {
			t.Errorf("%s is the registry's %s, %s's", b, e.Name, e.Owner)
		}
	}
	if err := CheckBinds(nil, binds...); err != nil {
		t.Error(err)
	}
	if len(SlotNames) != ports.TVSlots || MaxPipelineInstances != ports.TranscriptionLogs.Len() {
		t.Error("the slots or the transcription workers are not as many as their ports")
	}
}

// TestCheckBindsNamesBothOwners: server.toml giving the pipeline's logs the TV blue
// slot's admin port is refused, the message naming both and where to change it.
func TestCheckBindsNamesBothOwners(t *testing.T) {
	slot := Process{Role: RoleTVSlot, Instance: "blue", Binds: []ports.Bind{{Addr: SlotAdminAddr("blue"), What: "admin port"}}}
	pipe := Process{Role: RolePipeline, Binds: []ports.Bind{{Addr: ":9111", What: "logs port"}}}
	err := CheckBinds([]Process{slot, pipe})
	if err == nil {
		t.Fatal("a clash was not refused")
	}
	for _, want := range []string{"port 9111", "tv-slot/blue's admin port (127.0.0.1:9111)", "pipeline's logs port (:9111)", "server.toml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
	if err := CheckBinds([]Process{slot}, ports.Bind{Addr: "127.0.0.1:9112", Owner: "serve", What: "web port"}); err != nil {
		t.Errorf("no clash, yet: %v", err)
	}
}

// fakeHolder makes portHolder answer h (ok false: unnamed) for the test.
func fakeHolder(t *testing.T, h portholder.Holder, ok bool) {
	t.Helper()
	old := portHolder
	portHolder = func(int) (portholder.Holder, bool) { return h, ok }
	t.Cleanup(func() { portHolder = old })
}

// heldPort listens on a loopback port for the test, as another program would.
func heldPort(t *testing.T) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, ln.Addr().String()
}

// notThere is a command no start reaches while its port is held (and that fails at
// once when one does).
func notThere(t *testing.T) *exec.Cmd {
	return exec.Command(filepath.Join(t.TempDir(), "no-such-binary"))
}

// TestHeldPortLeavesItUnstarted: a slot whose admin port a transcription worker of
// this supervisor holds is not started; its detail names the port and the worker,
// it retries with the backoff, and once the port is free it is started (here: a
// start that fails, which clears the detail).
func TestHeldPortLeavesItUnstarted(t *testing.T) {
	ln, addr := heldPort(t)
	fakeHolder(t, portholder.Holder{PID: 4242, Command: "maktaba-pipeline"}, true)
	slot := Process{Role: RoleTVSlot, Instance: "blue", Cmd: notThere(t),
		Binds: []ports.Bind{{Addr: addr, What: "admin port"}}}
	worker := Process{Role: RolePipeline, Instance: "stt-1", Cmd: notThere(t), OnDemand: true}
	s := New([]Process{slot, worker}, testPolicy(), WithLog(io.Discard))
	s.mu.Lock()
	s.children[1].status.PID = 4242 // the worker, as if it ran
	s.mu.Unlock()
	running(t, s)

	snap := waitFor(t, s, "the slot's port_taken", func(sn Snapshot) bool {
		return sn.Processes[0].DetailCode == DetailPortTaken && sn.Processes[0].Restarts >= 1
	})
	st := snap.Processes[0]
	if st.PID != 0 || st.LastExit == nil || st.LastExit.Reason != ReasonStartFailed {
		t.Errorf("the slot ran, or its last exit is not a failed start: %+v", st)
	}
	port, _ := strconv.Atoi(addr[strings.LastIndexByte(addr, ':')+1:])
	if st.DetailParams["port"] != port || st.DetailParams["holder_pid"] != 4242 ||
		st.DetailParams["holder_command"] != "maktaba-pipeline" || st.DetailParams["holder_role"] != "pipeline/stt-1" {
		t.Errorf("params %v", st.DetailParams)
	}
	for _, want := range []string{"tv-slot/blue is not started", addr, "its admin port", "Maktaba's pipeline/stt-1, maktaba-pipeline (pid 4242)"} {
		if !strings.Contains(st.Detail, want) {
			t.Errorf("detail %q does not say %q", st.Detail, want)
		}
	}
	if st.LastExit.Error != st.Detail {
		t.Errorf("last exit %q, detail %q", st.LastExit.Error, st.Detail)
	}

	_ = ln.Close()
	waitFor(t, s, "the start once the port is free", func(sn Snapshot) bool {
		p := sn.Processes[0]
		return p.DetailCode == "" && p.LastExit != nil && !strings.Contains(p.LastExit.Error, "is not started")
	})
}

// TestHeldPortHolderUnnamed: a holder the OS does not name, a Maktaba process this
// supervisor did not start, and another program are each said as such.
func TestHeldPortHolderUnnamed(t *testing.T) {
	_, addr := heldPort(t)
	s := New(nil, testPolicy(), WithLog(io.Discard))
	c := &child{proc: Process{Role: RoleAPI, Binds: []ports.Bind{{Addr: addr, What: "admin port"}}}}
	cases := []struct {
		h    portholder.Holder
		ok   bool
		want string
	}{
		{portholder.Holder{}, false, "held by another program, which this machine does not name"},
		{portholder.Holder{PID: 77, Command: "maktaba-streaming"}, true, "maktaba-streaming (pid 77), a Maktaba process this server did not start"},
		{portholder.Holder{PID: 78, Command: "Splunkd.exe"}, true, "held by another program: Splunkd.exe (pid 78)"},
		{portholder.Holder{PID: 79}, true, "held by another program: pid 79"},
	}
	for _, tc := range cases {
		fakeHolder(t, tc.h, tc.ok)
		d := s.portTaken(c)
		if d == nil || d.Code != DetailPortTaken || !strings.Contains(d.Text, tc.want) {
			t.Errorf("%+v: %+v, want %q", tc.h, d, tc.want)
		}
	}
	free := &child{proc: Process{Role: RoleAPI, Binds: []ports.Bind{{Addr: freeAddr(t), What: "admin port"}}}}
	if d := s.portTaken(free); d != nil {
		t.Errorf("a free port reported: %+v", d)
	}
}
