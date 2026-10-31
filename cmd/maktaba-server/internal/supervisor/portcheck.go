package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/portholder"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// The ports a process listens on (Process.Binds) are checked twice. At serve's
// start CheckBinds refuses addresses in effect that clash among themselves (two
// of server.toml's, or one of them and a fixed one). Before each start the
// supervisor checks they are free (portTaken): a port another program holds
// leaves the process not started, its detail saying which port and who holds it,
// and the start is tried again with the crash loop's backoff. Before this a
// clash showed only as the role's crash loop on "bind: address already in use":
// on the owner's Mac the blue and green TV slots restarted 1,329 times on the
// ports two transcription workers held, and nothing said why.

// DetailPortTaken is the detail code of a process not started because one of its
// ports is held (detail_codes.json): its params are the port, the address, the
// holder's pid and program when the OS names them, the Maktaba process it is when
// it is one of this supervisor's, and the English.
const DetailPortTaken = "port_taken"

// portFree checks addr can be bound: the same bind the child is about to make
// (tests replace it).
var portFree = func(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}

// portHolder names who listens on a port (tests replace it).
var portHolder = portholder.Find

// binds is p's Binds with their Owner, the process's name.
func (p Process) binds() []ports.Bind {
	out := make([]ports.Bind, 0, len(p.Binds))
	for _, b := range p.Binds {
		if strings.TrimSpace(b.Addr) == "" {
			continue
		}
		if b.Owner == "" {
			b.Owner = p.Name()
		}
		out = append(out, b)
	}
	return out
}

// CheckBinds refuses binds that clash among themselves: every process's (procs)
// and serve's own (extra: the web port, the supervisor's). The error names both
// owners of each clashing port and says where to change it.
func CheckBinds(procs []Process, extra ...ports.Bind) error {
	all := append([]ports.Bind(nil), extra...)
	for _, p := range procs {
		all = append(all, p.binds()...)
	}
	clashes := ports.Clashes(all)
	if len(clashes) == 0 {
		return nil
	}
	lines := make([]string, 0, len(clashes))
	for _, c := range clashes {
		lines = append(lines, c.String())
	}
	return errors.New("parts of Maktaba are set to listen on one port, and they cannot both run: " +
		strings.Join(lines, "; ") + ". Set one of them to another port in server.toml ([server], [supervisor], [cloud], [postgres] or [llm])")
}

// portTaken checks c's ports before a start: nil when every one is free, else the
// Detail of the first one held, naming its holder as far as the OS lets us see.
func (s *Supervisor) portTaken(c *child) *Detail {
	for _, b := range c.proc.binds() {
		err := portFree(b.Addr)
		if err == nil || !addrInUse(err) {
			// a bind refused for another reason (no such address) is the child's to
			// report: it is started and says so itself
			continue
		}
		port := b.Port()
		params := map[string]any{"port": port, "addr": b.Addr}
		text := fmt.Sprintf("%s is not started: port %d (%s, its %s) is held by ", c.proc.Name(), port, b.Addr, b.What)
		h, ok := portHolder(port)
		switch {
		case !ok:
			text += "another program, which this machine does not name"
		default:
			params["holder_pid"], params["holder_command"] = h.PID, h.Command
			who := fmt.Sprintf("pid %d", h.PID)
			if h.Command != "" {
				who = fmt.Sprintf("%s (pid %d)", h.Command, h.PID)
			}
			if name := s.childWithPID(h.PID); name != "" {
				params["holder_role"] = name
				text += fmt.Sprintf("Maktaba's %s, %s", name, who)
				if e, _, ok := ports.Lookup(port); ok && !e.Foreign {
					text += fmt.Sprintf(" (the port is %s's: %s)", e.Owner, e.Name)
				}
			} else if strings.Contains(strings.ToLower(h.Command), "maktaba") {
				text += who + ", a Maktaba process this server did not start (one an earlier start left running?)"
			} else {
				text += "another program: " + who
			}
		}
		text += "; it starts once the port is free"
		params["error"] = text
		return &Detail{Code: DetailPortTaken, Params: params, Text: text}
	}
	return nil
}

// wantedNow reports whether c is to run now.
func (s *Supervisor) wantedNow(c *child) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return c.wanted && !s.stopping
}

// notStarted records a start not made because a port is held (d) as a failed start:
// d is the process's detail until it starts, and the next try waits as after any
// failed start (afterExit: the backoff, then the crash loop's failed_retry), so a
// port held for good is tried every few minutes, not in a spin. true: the loop ends.
func (s *Supervisor) notStarted(ctx context.Context, c *child, d Detail, backoff *time.Duration) bool {
	info := ExitInfo{At: s.now(), Code: -1, Reason: ReasonStartFailed, Error: d.Text}
	s.mu.Lock()
	c.status.Detail, c.status.DetailCode, c.status.DetailParams = d.Text, d.Code, d.Params
	s.mu.Unlock()
	s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventExited, Reason: ReasonStartFailed, Message: d.Text})
	s.errorf("%s", d.Text)
	return s.afterExit(ctx, c, info, backoff)
}

// clearPortTaken drops a port_taken detail once it no longer holds (the process
// started, or is no longer wanted). Caller holds s.mu.
func clearPortTaken(c *child) {
	if c.status.DetailCode == DetailPortTaken {
		c.status.Detail, c.status.DetailCode, c.status.DetailParams = "", "", nil
	}
}

// childWithPID is the name of the running child with pid, "" when none is.
func (s *Supervisor) childWithPID(pid int) string {
	if pid <= 0 {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.status.PID == pid {
			return c.proc.Name()
		}
	}
	return ""
}

// addrInUse reports whether a bind failed because the address is held: EADDRINUSE,
// or Windows' WSAEADDRINUSE, which only its words say.
func addrInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "address already in use") || strings.Contains(m, "only one usage of each socket address")
}
