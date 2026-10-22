package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// firewallPC is this PC with its four inbound rules (TCP and UDP for the old
// maktaba-server and maktaba-streaming, every port, the Private and Public
// profiles, as Windows made them when they first listened), a disabled rule,
// a block rule and one for a program the app does not have; and the app's
// payload with its programs.
func firewallPC(t *testing.T) *thisPC {
	t.Helper()
	pc := newThisPC(t)
	payload := filepath.Join(filepath.Dir(pc.tgt.StateDir), "Programs", "Maktaba", "server")
	for _, exe := range []string{"maktaba-server.exe", "maktaba-streaming.exe", "maktaba-api.exe"} {
		write(t, filepath.Join(payload, "bin", exe), "the app's "+exe)
	}
	pc.tgt.AppPayload = payload
	bin := filepath.Join(pc.dir, "bin")
	rule := func(name, exe, proto string) FirewallRule {
		return FirewallRule{Name: name, Display: strings.TrimSuffix(exe, ".exe"), Program: filepath.Join(bin, exe),
			Protocol: proto, Ports: []string{"Any"}, Profiles: "Private, Public", Enabled: true, Allow: true, Inbound: true}
	}
	off := rule("TCP Query User{off}", "maktaba-server.exe", "TCP")
	off.Enabled, off.Ports = false, []string{"8780"}
	block := rule("Block{1}", "maktaba-streaming.exe", "TCP")
	block.Allow, block.Ports = false, []string{"8782"}
	pc.sys.rules = []FirewallRule{
		rule("TCP Query User{1}", "maktaba-server.exe", "TCP"), rule("UDP Query User{2}", "maktaba-server.exe", "UDP"),
		rule("TCP Query User{3}", "maktaba-streaming.exe", "TCP"), rule("UDP Query User{4}", "maktaba-streaming.exe", "UDP"),
		off, block, rule("TCP Query User{5}", "maktaba-tool.exe", "TCP"),
		// dizqueTV's own: never looked at
		{Name: "dizquetv", Program: `C:\Program Files\nodejs\node.exe`, Protocol: "TCP", Ports: []string{"8000"}, Profiles: "Any",
			Enabled: true, Allow: true, Inbound: true},
	}
	return pc
}

// TestThePlanCopiesTheOldInstallsFirewallRules: the rules that let other
// devices reach the old install's programs are copied for the app's, the same
// protocols, ports and profiles; the plan says Windows asks once, or refuses
// when this user cannot allow it.
func TestThePlanCopiesTheOldInstallsFirewallRules(t *testing.T) {
	pc := firewallPC(t)
	srcs, _ := Find(pc.sys, pc.tgt.AppPayload)
	plan, err := MakePlan(pc.sys, srcs[0], pc.tgt)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range plan.Firewall {
		if !strings.HasPrefix(r.Name, firewallPrefix) || !r.serves() || r.Profiles != "Private, Public" ||
			!reflect.DeepEqual(r.Ports, []string{"Any"}) || !within(r.Program, filepath.Join(pc.tgt.AppPayload, "bin")) {
			t.Errorf("copy %+v", r)
		}
		got = append(got, filepath.Base(r.Program)+" "+r.Protocol+" from "+r.From)
	}
	want := []string{"maktaba-server.exe TCP from TCP Query User{1}", "maktaba-server.exe UDP from UDP Query User{2}",
		"maktaba-streaming.exe TCP from TCP Query User{3}", "maktaba-streaming.exe UDP from UDP Query User{4}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("copies %q, want %q (not the disabled one, the block one, nor one for a program the app lacks)", got, want)
	}
	if len(plan.Problems) != 0 || len(plan.Warnings) != 1 || plan.Warnings[0].Code != "firewall_prompt" || plan.Warnings[0].Params["count"] != 4 {
		t.Errorf("problems %+v warnings %+v", plan.Problems, plan.Warnings)
	}
	// the rule of a program the app has none of is said, never dropped silently
	if n := codedNotes(plan.NotCarried, "firewall_rule"); len(n) != 1 || n[0].Params["rule"] != "maktaba-tool" {
		t.Errorf("not carried: %+v", plan.NotCarried)
	}

	// what the app has already is not asked again
	pc.sys.rules = append(pc.sys.rules, plan.Firewall[0])
	if again, _ := MakePlan(pc.sys, srcs[0], pc.tgt); len(again.Firewall) != 3 {
		t.Errorf("with one rule there already: %d copies", len(again.Firewall))
	}
	// a standard user cannot allow it: refused before anything changes
	pc.sys.noAdmin = true
	if p, _ := MakePlan(pc.sys, srcs[0], pc.tgt); len(p.Problems) != 1 || p.Problems[0].Code != "firewall_needs_admin" {
		t.Errorf("a standard user: %+v", p.Problems)
	}
	// rules that cannot be read: not "none"
	pc.sys.noAdmin, pc.sys.fwErr = false, errors.New("the NetSecurity module failed")
	if p, _ := MakePlan(pc.sys, srcs[0], pc.tgt); len(p.Problems) != 1 || p.Problems[0].Code != "firewall_unknown" {
		t.Errorf("unreadable rules: %+v", p.Problems)
	}
	// none to copy: no prompt
	pc.sys.fwErr, pc.sys.rules = nil, nil
	if p, _ := MakePlan(pc.sys, srcs[0], pc.tgt); len(p.Firewall) != 0 || len(p.Warnings) != 0 {
		t.Errorf("no rules: %+v %+v", p.Firewall, p.Warnings)
	}
}

// TestTheMoveMakesTheRulesBeforeTheStop: one administrator's prompt, before
// anything of the old install stops, so a No changes nothing; the record keeps
// what was made; Verify checks the rules before the old task goes, and Undo
// removes the move's rules only.
func TestTheMoveMakesTheRulesBeforeTheStop(t *testing.T) {
	fastStop(t)
	pc := firewallPC(t)
	srcs, _ := Find(pc.sys, pc.tgt.AppPayload)
	plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt)
	old := append([]FirewallRule(nil), pc.sys.rules...)

	// a No, and a prompt not answered in time (withdrawn): a refusal each, with
	// its own words, and nothing changed
	for _, refusal := range []struct {
		err  error
		code string
	}{
		{ErrDeclined, "adopt_firewall_declined"},
		{&PromptTimeoutError{Wait: 3 * time.Minute}, "adopt_firewall_timeout"},
	} {
		pc.sys.promptErr = refusal.err
		_, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil)
		ae := codeOf(err)
		if ae == nil || ae.Code != refusal.code {
			t.Fatalf("%s: %v", refusal.code, err)
		}
		if refusal.code == "adopt_firewall_timeout" && ae.Params["minutes"] != 3 {
			t.Errorf("the timeout's params: %v", ae.Params)
		}
		if rec, _, _ := ReadRecord(pc.tgt.StateDir); rec.Stage != StageFound || len(rec.Firewall) != 0 || len(pc.sys.ended) != 0 || len(pc.sys.enabled) != 0 {
			t.Fatalf("after %s: stage %s, rules %d, ended %q, tasks changed %v", refusal.code, rec.Stage, len(rec.Firewall), pc.sys.ended, pc.sys.enabled)
		}
	}

	pc.sys.promptErr = nil
	var stages []string
	rec, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, func(stage, _ string) { stages = append(stages, stage) })
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.sys.added) != 1 || len(pc.sys.added[0]) != 4 || pc.sys.endedWhenRulesWereMade != 0 {
		t.Errorf("added %d times (%d rules), %d tasks ended before: one prompt, before the stop", len(pc.sys.added), len(pc.sys.added[0]), pc.sys.endedWhenRulesWereMade)
	}
	if !reflect.DeepEqual(stages, []string{"firewall", "stop", "copy", "settings"}) || len(rec.Firewall) != 4 {
		t.Errorf("stages %q, recorded rules %d", stages, len(rec.Firewall))
	}

	// Verify: the server answers, but a rule turned off keeps the old task
	procs := []supervisor.ChildStatus{{Role: supervisor.RoleAPI, Wanted: true, State: supervisor.StateRunning}}
	cfg := fakeServer(t, &procs)
	appServes(pc, "0.0.0.0")
	for i := range pc.sys.rules {
		if pc.sys.rules[i].Name == rec.Firewall[0].Name {
			pc.sys.rules[i].Enabled = false
		}
	}
	if err := Verify(context.Background(), pc.sys, pc.tgt, cfg, 0); err == nil || !strings.Contains(err.Error(), "off or changed") {
		t.Fatalf("a rule turned off: %v", err)
	}
	if ok, _ := Finish(pc.sys, pc.tgt.StateDir); ok || len(pc.sys.deleted) != 0 {
		t.Fatal("the old task went while a rule was off")
	}
	// one that no longer covers the network in use
	for i := range pc.sys.rules {
		if pc.sys.rules[i].Name == rec.Firewall[0].Name {
			pc.sys.rules[i].Enabled, pc.sys.rules[i].Profiles = true, "Public"
		}
	}
	if err := Verify(context.Background(), pc.sys, pc.tgt, cfg, 0); err == nil || !strings.Contains(err.Error(), "Private network") {
		t.Fatalf("a rule off the active profile: %v", err)
	}
	for i := range pc.sys.rules {
		if pc.sys.rules[i].Name == rec.Firewall[0].Name {
			pc.sys.rules[i].Profiles = "Private, Public"
		}
	}
	if err := Verify(context.Background(), pc.sys, pc.tgt, cfg, 0); err != nil {
		t.Fatal(err)
	}

	// Undo: the move's rules removed (it says Windows asks first), the old ones
	// untouched (they serve it again)
	stages = nil
	if err := Undo(pc.sys, pc.tgt, func(stage, _ string) { stages = append(stages, stage) }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stages, []string{"firewall"}) {
		t.Errorf("undo's stages %q", stages)
	}
	if len(pc.sys.removed) != 1 || len(pc.sys.removed[0]) != 4 || !strings.HasPrefix(pc.sys.removed[0][0], firewallPrefix) {
		t.Errorf("removed %q", pc.sys.removed)
	}
	if !reflect.DeepEqual(pc.sys.rules, old) {
		t.Errorf("the old rules changed: %+v", pc.sys.rules)
	}
	if rec, _, _ := ReadRecord(pc.tgt.StateDir); len(rec.Firewall) != 0 || len(rec.FirewallLeft) != 0 {
		t.Errorf("record after undo: %+v %+v", rec.Firewall, rec.FirewallLeft)
	}
}

// TestUndoGoesOnWhenItCannotRemoveTheRules: a refused prompt at Undo leaves
// the app's rules (they only allow its programs, which no longer run) and says
// so in the record; the old install runs again all the same.
func TestUndoGoesOnWhenItCannotRemoveTheRules(t *testing.T) {
	fastStop(t)
	pc := firewallPC(t)
	srcs, _ := Find(pc.sys, pc.tgt.AppPayload)
	plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt)
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err != nil {
		t.Fatal(err)
	}
	pc.sys.declines = true
	if err := Undo(pc.sys, pc.tgt, nil); err != nil {
		t.Fatal(err)
	}
	rec, _, _ := ReadRecord(pc.tgt.StateDir)
	if rec.Stage != StageUndone || len(rec.FirewallLeft) != 4 || len(pc.sys.run) != 1 {
		t.Errorf("stage %s, left %q, run %q", rec.Stage, rec.FirewallLeft, pc.sys.run)
	}
}

// TestTheElevatedRunTakesOnlyTheAppsRules: the elevated helper's input is the
// user's file; it accepts only the move's own rules, allowing inbound
// connections to a program beside it, with plain values, and removes only the
// move's own.
func TestTheElevatedRunTakesOnlyTheAppsRules(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "server", "bin")
	good := FirewallRule{Name: firewallPrefix + "0123456789abcdef", Display: "Maktaba: maktaba-server (TCP)",
		Program: filepath.Join(bin, "maktaba-server.exe"), Protocol: "TCP", Ports: []string{"Any"},
		Profiles: "Private, Public", Enabled: true, Allow: true, Inbound: true}
	if err := validFirewallChange(firewallChange{Add: []FirewallRule{good}, Remove: []string{good.Name}}, bin); err != nil {
		t.Fatalf("the move's own change: %v", err)
	}
	bad := map[string]func(*FirewallRule){
		"another id":         func(r *FirewallRule) { r.Name = "TCP Query User{1}" },
		"a block rule":       func(r *FirewallRule) { r.Allow = false },
		"outbound":           func(r *FirewallRule) { r.Inbound = false },
		"another program":    func(r *FirewallRule) { r.Program = `C:\Windows\System32\cmd.exe` },
		"a program below":    func(r *FirewallRule) { r.Program = filepath.Join(bin, "sub", "x.exe") },
		"a port of words":    func(r *FirewallRule) { r.Ports = []string{"8780; Remove-Item"} },
		"a profile of words": func(r *FirewallRule) { r.Profiles = "Private; whoami" },
		"a protocol":         func(r *FirewallRule) { r.Protocol = "TCP'" },
		"a line break":       func(r *FirewallRule) { r.Display = "x\ny" },
	}
	for what, change := range bad {
		r := good
		change(&r)
		if err := validFirewallChange(firewallChange{Add: []FirewallRule{r}}, bin); err == nil {
			t.Errorf("%s accepted", what)
		}
	}
	if err := validFirewallChange(firewallChange{Remove: []string{"TCP Query User{1}"}}, bin); err == nil {
		t.Error("removing the old install's rule accepted")
	}
}

// TestTheScriptsConsoleIsKnownByItsWholePath: a console runs the old install's
// script only when the batch file on its command line, resolved against its
// directory (or a cd /d before it), is the script; never by part of a name.
func TestTheScriptsConsoleIsKnownByItsWholePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "maktaba-local")
	other := filepath.Join(t.TempDir(), "elsewhere")
	script := filepath.Join(dir, "start.cmd")
	src := Source{Script: script}
	for _, c := range []struct {
		cmdline, cwd string
		want         bool
	}{
		{`cmd.exe /c start.cmd`, dir, true},
		{`C:\Windows\system32\cmd.exe /c ""` + script + `" "`, "", true},
		{`C:\Windows\System32\conhost.exe --headless cmd.exe /c "` + script + `"`, "", true},
		{`cmd /c "cd /d ` + dir + ` && start.cmd"`, "", true},
		{`cmd /c "cd /d ` + other + ` && start.cmd"`, dir, false},
		{`cmd.exe /c restart.cmd`, dir, false},
		{`cmd.exe /c start.cmd`, other, false},
		{`cmd.exe /c "` + filepath.Join(other, "start.cmd") + `"`, dir, false},
		{`cmd.exe /c start.cmd`, "", false}, // no directory to tell by
		{`cmd.exe`, dir, false},
	} {
		if got := src.runsScript(Proc{Name: "cmd.exe", Cmdline: c.cmdline, Cwd: c.cwd}); got != c.want {
			t.Errorf("%q in %q: %v, want %v", c.cmdline, c.cwd, got, c.want)
		}
	}
	// a path with a space, quoted once
	spaced := Source{Script: filepath.Join(dir, "old install", "start.cmd")}
	if !spaced.runsScript(Proc{Name: "cmd.exe", Cmdline: `cmd.exe /c "` + spaced.Script + `"`}) {
		t.Error("a quoted path with a space")
	}
}

// TestALateRunFindsTheChangeWithdrawn: a move that stopped waiting for the
// prompt withdraws its change, and the elevated run a late Yes starts cannot
// take it: it changes nothing. A change the run took first stays its own.
func TestALateRunFindsTheChangeWithdrawn(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "change.json"), filepath.Join(dir, "result.txt")
	// a change the run would refuse anyway: nothing here can reach the firewall
	change := []byte(`{"remove":["not-the-apps"]}`)
	if err := os.WriteFile(in, change, 0o600); err != nil {
		t.Fatal(err)
	}
	if !withdraw(in) {
		t.Fatal("the change was not withdrawn")
	}
	if code := FirewallHelper([]string{in, out}); code != 1 {
		t.Errorf("the late run: exit %d", code)
	}
	if msg, _ := os.ReadFile(out); !strings.Contains(string(msg), "withdrawn") {
		t.Errorf("the late run: %q", msg)
	}
	if err := os.WriteFile(in, change, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(in, in+takenSuffix); err != nil {
		t.Fatal(err)
	}
	if withdraw(in) {
		t.Error("withdrew a change the run had taken")
	}
	if got := (&PromptTimeoutError{Wait: 150 * time.Second}).Minutes(); got != 3 {
		t.Errorf("2.5 minutes are %d in the screen's words", got)
	}
}

// codedNotes are the notes with code.
func codedNotes(notes []Note, code string) []Note {
	var out []Note
	for _, n := range notes {
		if n.Code == code {
			out = append(out, n)
		}
	}
	return out
}

// TestRulesOutsideTheFolderAreRead: the old install's programs outside its
// folder (the server its script runs from elsewhere, a pipeline worker of a
// venv elsewhere) have their rules read too: copied for the app's program of
// the same name, and said not carried when the app has none.
func TestRulesOutsideTheFolderAreRead(t *testing.T) {
	pc := firewallPC(t)
	srcs, _ := Find(pc.sys, pc.tgt.AppPayload)
	src := srcs[0]
	elsewhere := filepath.Join(filepath.Dir(pc.dir), "Maktaba Server")
	src.Server = filepath.Join(elsewhere, "maktaba-server.exe")
	python := filepath.Join(filepath.Dir(pc.dir), "venv", "Scripts", "python.exe")
	pc.sys.procs = append(pc.sys.procs, Proc{PID: 150, Name: "python.exe", Exe: python, Cmdline: "python.exe -m pipeline"})
	src.PIDs = append(src.PIDs, 150)
	pc.sys.rules = append(pc.sys.rules,
		FirewallRule{Name: "Outside{1}", Display: "maktaba-server (elsewhere)", Program: src.Server, Protocol: "TCP",
			Ports: []string{"8780"}, Profiles: "Private", Enabled: true, Allow: true, Inbound: true},
		FirewallRule{Name: "Python{1}", Display: "python", Program: python, Protocol: "TCP", Ports: []string{"9090"},
			Profiles: "Private", Enabled: true, Allow: true, Inbound: true},
		// another program in the server's folder: not the old install's
		FirewallRule{Name: "Other{1}", Display: "other", Program: filepath.Join(elsewhere, "other.exe"), Protocol: "TCP",
			Ports: []string{"1"}, Profiles: "Any", Enabled: true, Allow: true, Inbound: true})
	plan, err := MakePlan(pc.sys, src, pc.tgt)
	if err != nil {
		t.Fatal(err)
	}
	var from []string
	for _, r := range plan.Firewall {
		from = append(from, r.From)
	}
	if !slices.Contains(from, "Outside{1}") || len(plan.Firewall) != 5 {
		t.Errorf("copies from %q: the server's rule outside the folder is copied too", from)
	}
	var rules []any
	for _, n := range codedNotes(plan.NotCarried, "firewall_rule") {
		rules = append(rules, n.Params["rule"])
	}
	if !reflect.DeepEqual(rules, []any{"maktaba-tool", "python"}) {
		t.Errorf("not carried %v: the rules of programs the app has none of", rules)
	}
}

// TestABlockRuleOfTheAppsIsWarnedOf: a block rule on one of the app's programs
// (Windows makes one when its prompt to allow a program is cancelled) beats the
// copied allow rules, as a block applies before any allow: the plan warns of
// it, and Verify does not pass while it is on where the copies apply.
func TestABlockRuleOfTheAppsIsWarnedOf(t *testing.T) {
	fastStop(t)
	pc := firewallPC(t)
	app := filepath.Join(pc.tgt.AppPayload, "bin", "maktaba-server.exe")
	elsewhere := FirewallRule{Name: "TCP Query User{8}", Display: "maktaba-server (domain)", Program: app, Protocol: "TCP",
		Ports: []string{"Any"}, Profiles: "Domain", Enabled: true, Inbound: true}
	pc.sys.rules = append(pc.sys.rules, elsewhere)
	srcs, _ := Find(pc.sys, pc.tgt.AppPayload)
	if plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt); len(codedNotes(plan.Warnings, "firewall_blocked")) != 0 {
		t.Errorf("a block on a network the copies do not cover: %+v", plan.Warnings)
	}
	block := FirewallRule{Name: "TCP Query User{9}", Display: "maktaba-server", Program: app, Protocol: "TCP",
		Ports: []string{"Any"}, Profiles: "Public", Enabled: true, Inbound: true}
	pc.sys.rules = append(pc.sys.rules, block)
	plan, _ := MakePlan(pc.sys, srcs[0], pc.tgt)
	if n := codedNotes(plan.Warnings, "firewall_blocked"); len(n) != 1 || n[0].Params["rule"] != "maktaba-server" ||
		n[0].Params["program"] != "maktaba-server.exe" {
		t.Fatalf("warnings %+v", plan.Warnings)
	}
	if _, err := Move(context.Background(), pc.sys, plan, pc.tgt, nil, nil); err != nil {
		t.Fatal(err)
	}
	procs := []supervisor.ChildStatus{{Role: supervisor.RoleAPI, Wanted: true, State: supervisor.StateRunning}}
	cfg := fakeServer(t, &procs)
	appServes(pc, "0.0.0.0")
	pc.sys.profiles = []string{"Public"}
	if err := Verify(context.Background(), pc.sys, pc.tgt, cfg, 0); err == nil || !strings.Contains(err.Error(), "blocks") {
		t.Fatalf("a block on the network in use: %v", err)
	}
	pc.sys.profiles = []string{"Private"}
	if err := Verify(context.Background(), pc.sys, pc.tgt, cfg, 0); err != nil {
		t.Fatalf("the block is on another network: %v", err)
	}
}

func TestRulesMeet(t *testing.T) {
	for _, c := range []struct {
		a, b []string
		want bool
	}{
		{[]string{"Any"}, []string{"8780"}, true},
		{[]string{"8700-8800"}, []string{"8780"}, true},
		{[]string{"8000-8100"}, []string{"8780"}, false},
		{[]string{"8781", "8782"}, []string{"8780"}, false},
		{[]string{"RPC"}, []string{"8780"}, true},
	} {
		if got := portsMeet(c.a, c.b); got != c.want {
			t.Errorf("ports %q and %q: %v", c.a, c.b, got)
		}
	}
	if profilesMeet("Domain", "Private, Public") || !profilesMeet("Public", "Private, Public") || !profilesMeet("Any", "Domain") {
		t.Error("profiles")
	}
}
