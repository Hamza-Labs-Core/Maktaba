package adopt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// FirewallRule is an inbound rule of Windows Defender Firewall that lets a
// program be reached (Get-NetFirewallRule with its application and port
// filters; the enums as their invariant names, never localized text).
type FirewallRule struct {
	Name        string   `json:"name"`    // the rule's id
	Display     string   `json:"display"` // its display name
	Description string   `json:"description,omitempty"`
	Group       string   `json:"group,omitempty"`
	Program     string   `json:"program"`  // the program it is bound to, whole
	Protocol    string   `json:"protocol"` // TCP, UDP, Any, or a protocol number
	Ports       []string `json:"ports"`    // local ports; ["Any"] for every port
	Profiles    string   `json:"profiles"` // "Any", or "Domain, Private, Public" in part
	Enabled     bool     `json:"enabled"`
	Allow       bool     `json:"allow"`
	Inbound     bool     `json:"inbound"`
	// From is the old install's rule this one copies (a rule the move made).
	From string `json:"from,omitempty"`
}

// The rules a move makes: their ids start with this, and only those are ever
// removed (the old install's rules are never touched: they serve it again on
// Undo).
const (
	firewallPrefix = "Maktaba-app-"
	firewallGroup  = "Maktaba app"
)

// serves reports whether r lets other devices reach its program.
func (r FirewallRule) serves() bool { return r.Enabled && r.Allow && r.Inbound }

// covers reports whether r applies on the network profile p (Domain,
// Private, Public).
func (r FirewallRule) covers(p string) bool {
	if strings.EqualFold(strings.TrimSpace(r.Profiles), "Any") || strings.TrimSpace(r.Profiles) == "" {
		return true
	}
	for _, x := range strings.Split(r.Profiles, ",") {
		if strings.EqualFold(strings.TrimSpace(x), p) {
			return true
		}
	}
	return false
}

// blocks reports whether r, an inbound block rule that is on, stops some of
// what the allow rule a lets through: the same program, on a protocol, a port
// and a profile of a's. Windows applies a block before any allow, so a's
// devices would not reach the program.
func (r FirewallRule) blocks(a FirewallRule) bool {
	if !r.Enabled || r.Allow || !r.Inbound || !samePath(r.Program, a.Program) {
		return false
	}
	if !anyValue(r.Protocol) && !anyValue(a.Protocol) && !strings.EqualFold(r.Protocol, a.Protocol) {
		return false
	}
	return portsMeet(r.Ports, a.Ports) && profilesMeet(r.Profiles, a.Profiles)
}

// anyValue: a rule's field that holds every value ("Any", or none given).
func anyValue(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || strings.EqualFold(v, "Any")
}

// portsMeet: two rules' local ports share one. A range ("5000-5010") holds
// its ports; a keyword (RPC, PlayToDiscovery, …) is taken to meet any.
func portsMeet(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			xl, xh, okx := portRange(x)
			yl, yh, oky := portRange(y)
			if anyValue(x) || anyValue(y) || !okx || !oky || (xl <= yh && yl <= xh) {
				return true
			}
		}
	}
	return false
}

func portRange(p string) (lo, hi int, ok bool) {
	from, to, isRange := strings.Cut(strings.TrimSpace(p), "-")
	lo, err := strconv.Atoi(strings.TrimSpace(from))
	if err != nil {
		return 0, 0, false
	}
	hi = lo
	if isRange {
		if hi, err = strconv.Atoi(strings.TrimSpace(to)); err != nil {
			return 0, 0, false
		}
	}
	return lo, hi, true
}

// profilesMeet: two rules apply on a network profile in common.
func profilesMeet(a, b string) bool {
	if anyValue(a) || anyValue(b) {
		return true
	}
	for _, p := range strings.Split(a, ",") {
		if (FirewallRule{Profiles: b}).covers(strings.TrimSpace(p)) {
			return true
		}
	}
	return false
}

// same reports whether o lets the same program be reached the same way as r.
func (r FirewallRule) same(o FirewallRule) bool {
	return samePath(r.Program, o.Program) && strings.EqualFold(r.Protocol, o.Protocol) &&
		strings.EqualFold(strings.Join(r.Ports, ","), strings.Join(o.Ports, ",")) &&
		strings.EqualFold(strings.ReplaceAll(r.Profiles, " ", ""), strings.ReplaceAll(o.Profiles, " ", ""))
}

// firewallCopies are the rules the app's programs need so that other devices
// reach them as they reached the old install's: one for each enabled inbound
// allow rule of one of the old install's programs whose program the app's
// payload has (bin\<the same name>), none the app has already.
func firewallCopies(old, app []FirewallRule, payload string) []FirewallRule {
	var out []FirewallRule
	for _, c := range firewallWanted(old, payload) {
		had := false
		for _, a := range app {
			had = had || (a.serves() && a.same(c))
		}
		for _, o := range out {
			had = had || o.Name == c.Name
		}
		if !had {
			out = append(out, c)
		}
	}
	return out
}

// firewallWanted are the old install's serving rules as the app's programs'
// (firewallCopies, before the ones the app has are left out).
func firewallWanted(old []FirewallRule, payload string) []FirewallRule {
	var out []FirewallRule
	for _, r := range old {
		prog, ok := appProgram(r, payload)
		if !r.serves() || !ok {
			continue
		}
		c := FirewallRule{Program: prog, Protocol: r.Protocol, Ports: r.Ports, Profiles: r.Profiles,
			Enabled: true, Allow: true, Inbound: true, Group: firewallGroup, From: r.Name}
		c.Name = firewallPrefix + ruleID(c)
		c.Display = fmt.Sprintf("Maktaba: %s (%s)", strings.TrimSuffix(filepath.Base(prog), filepath.Ext(prog)), r.Protocol)
		c.Description = "Lets other devices reach the Maktaba app's " + filepath.Base(prog) +
			", as the rule " + r.Display + " let them reach the install it took over. Restore the old install removes it."
		out = append(out, c)
	}
	return out
}

// appProgram is the app's program for the old rule r's (bin\<the same name>),
// when the app's payload has one.
func appProgram(r FirewallRule, payload string) (string, bool) {
	prog := filepath.Join(payload, "bin", filepath.Base(winSlash(r.Program)))
	return prog, payload != "" && isFile(prog)
}

// firewallBlocks are the app's block rules that beat a rule its programs need
// (wanted): Windows applies a block before any allow, so other devices would
// not reach the app while one is on.
func firewallBlocks(app, wanted []FirewallRule) []FirewallRule {
	var out []FirewallRule
	for _, b := range app {
		for _, w := range wanted {
			if b.blocks(w) && !slices.ContainsFunc(out, func(o FirewallRule) bool { return o.Name == b.Name }) {
				out = append(out, b)
			}
		}
	}
	return out
}

func ruleID(r FirewallRule) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(r.Program)) + "|" + strings.ToUpper(r.Protocol) + "|" +
		strings.ToLower(strings.Join(r.Ports, ",")) + "|" + strings.ToLower(strings.ReplaceAll(r.Profiles, " ", ""))))
	return hex.EncodeToString(sum[:8])
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// checkFirewall: the rules a move made are there, on, inbound, allowing the
// app's programs as recorded, on every network profile in use that the old
// rule covered. (A connection from this PC to its own LAN address never passes
// the inbound filter, so it would prove nothing: the rules themselves are read.)
func checkFirewall(sys Sys, made []FirewallRule) error {
	if len(made) == 0 {
		return nil
	}
	var now []FirewallRule
	read := map[string]bool{}
	for _, r := range made {
		dir := strings.ToLower(filepath.Dir(r.Program))
		if read[dir] {
			continue
		}
		read[dir] = true
		rules, err := sys.FirewallRules(filepath.Dir(r.Program))
		if err != nil {
			return fmt.Errorf("read the firewall rules: %w", err)
		}
		now = append(now, rules...)
	}
	active, err := sys.ActiveProfiles()
	if err != nil {
		return fmt.Errorf("read the network's profile: %w", err)
	}
	for _, want := range made {
		var got *FirewallRule
		for i := range now {
			if strings.EqualFold(now[i].Name, want.Name) {
				got = &now[i]
			}
		}
		switch {
		case got == nil:
			return fmt.Errorf("the firewall rule %s for %s is missing", want.Name, want.Program)
		case !got.serves() || !samePath(got.Program, want.Program) || !strings.EqualFold(got.Protocol, want.Protocol):
			return fmt.Errorf("the firewall rule %s for %s is off or changed", want.Name, want.Program)
		}
		for _, p := range active {
			if want.covers(p) && !got.covers(p) {
				return fmt.Errorf("the firewall rule %s for %s no longer covers the %s network", want.Name, want.Program, p)
			}
			// a block applies before any allow
			for _, b := range now {
				if b.blocks(want) && want.covers(p) && b.covers(p) {
					return fmt.Errorf("the firewall rule %s blocks %s on the %s network", b.Display, want.Program, p)
				}
			}
		}
	}
	return nil
}

// FirewallHelperArg is the first argument of maktaba-server run elevated to
// change the firewall (once per move or Undo, through one prompt): what it
// adds and removes is in a file, checked here before anything is changed.
const FirewallHelperArg = "__adopt-firewall"

// firewallChange is the elevated helper's input.
type firewallChange struct {
	Add    []FirewallRule `json:"add"`
	Remove []string       `json:"remove"`
}

var (
	firewallName  = regexp.MustCompile(`^` + firewallPrefix + `[0-9a-f]{16}$`)
	firewallProto = regexp.MustCompile(`^(?i:TCP|UDP|Any|[0-9]{1,3})$`)
	firewallPort  = regexp.MustCompile(`^(?i:Any|[0-9]{1,5}(-[0-9]{1,5})?)$`)
	firewallProf  = regexp.MustCompile(`^(?i:Any|(Domain|Private|Public)(, ?(Domain|Private|Public))*)$`)
)

// validFirewallChange is what the elevated helper accepts: only rules of the
// move's own (their id), allowing inbound connections to a program beside
// the helper (the app's own bin folder), with plain protocols, ports and
// profiles; only the move's own rules removed. The file is the user's: the
// elevated side trusts nothing else in it.
func validFirewallChange(c firewallChange, binDir string) error {
	for _, r := range c.Add {
		switch {
		case !firewallName.MatchString(r.Name):
			return fmt.Errorf("not a rule of the app's: %q", r.Name)
		case !r.Enabled || !r.Allow || !r.Inbound:
			return fmt.Errorf("%s: only an enabled inbound allow rule is made", r.Name)
		case binDir == "" || !samePath(filepath.Dir(r.Program), binDir):
			return fmt.Errorf("%s: %s is not one of the app's programs (%s)", r.Name, r.Program, binDir)
		case !firewallProto.MatchString(r.Protocol):
			return fmt.Errorf("%s: protocol %q", r.Name, r.Protocol)
		case !firewallProf.MatchString(strings.TrimSpace(r.Profiles)):
			return fmt.Errorf("%s: profiles %q", r.Name, r.Profiles)
		case strings.ContainsAny(r.Display+r.Description, "\r\n"):
			return fmt.Errorf("%s: a line break in its text", r.Name)
		}
		for _, p := range r.Ports {
			if !firewallPort.MatchString(p) {
				return fmt.Errorf("%s: port %q", r.Name, p)
			}
		}
	}
	for _, n := range c.Remove {
		if !firewallName.MatchString(n) {
			return fmt.Errorf("not a rule of the app's: %q", n)
		}
	}
	return nil
}

// PromptTimeoutError: Windows's consent prompt was not answered within Wait
// (Windows can open it late, or leave it in the taskbar). The change was
// withdrawn: nothing was changed, and a run Windows starts later does nothing.
type PromptTimeoutError struct{ Wait time.Duration }

func (e *PromptTimeoutError) Error() string {
	return fmt.Sprintf("Windows asked for permission and no answer came within %s: nothing was changed", e.Wait)
}

// Minutes is Wait in whole minutes, rounded up (the screen's words).
func (e *PromptTimeoutError) Minutes() int {
	return max(1, int((e.Wait+time.Minute-1)/time.Minute))
}

// The change file is the elevated run's once it took it (renamed to
// in+takenSuffix); the move that asked withdraws it (renamed to
// in+withdrawnSuffix) when the prompt was not answered in time. Only one
// rename can win, so a late run never makes a change the move gave up on.
const (
	takenSuffix     = ".taken"
	withdrawnSuffix = ".withdrawn"
)

// withdraw takes the change in back from the elevated run: true when it is
// withdrawn, false when the run has taken it (it is under way).
func withdraw(in string) bool {
	for try := 0; ; try++ {
		if os.Rename(in, in+withdrawnSuffix) == nil {
			return true
		}
		if _, err := os.Stat(in + takenSuffix); err == nil || try == 4 {
			return false
		}
		time.Sleep(100 * time.Millisecond) // a scanner holding the file a moment
	}
}

// FirewallHelper is the elevated run: `maktaba-server __adopt-firewall <in>
// <out>` takes the change in <in> (unless it was withdrawn), checks it, makes
// it, and writes what went wrong (if anything) to <out>. Its exit code is 0
// when the change is made.
func FirewallHelper(args []string) int {
	if len(args) != 2 {
		return 2
	}
	fail := func(err error) int {
		_ = os.WriteFile(args[1], []byte(err.Error()), 0o600)
		return 1
	}
	taken := args[0] + takenSuffix
	if err := os.Rename(args[0], taken); err != nil {
		return fail(fmt.Errorf("the change was withdrawn (the prompt was answered too late), nothing was changed: %w", err))
	}
	raw, err := os.ReadFile(taken)
	if err != nil {
		return fail(err)
	}
	var c firewallChange
	if err := json.Unmarshal(raw, &c); err != nil {
		return fail(err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	if err := validFirewallChange(c, filepath.Dir(exe)); err != nil {
		return fail(err)
	}
	// what was checked is what is made: the change reaches PowerShell on its
	// stdin, never by reading the file again
	if err := applyFirewall(c); err != nil {
		return fail(err)
	}
	_ = os.WriteFile(args[1], nil, 0o600)
	return 0
}
