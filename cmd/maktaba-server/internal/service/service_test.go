package service

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

var update = flag.Bool("update", false, "rewrite the deploy/packaging unit files from the renderers")

// The unit files shipped in deploy/packaging are what the renderers produce
// for the package paths (story 35.2 TC7): one source of truth.
var goldens = []struct {
	file string
	spec Spec
}{
	{"systemd/maktaba-server.service", Spec{Kind: KindSystemd, Mode: layout.ModeSystem,
		Root: "/var/lib/maktaba", Launcher: "/usr/bin/maktaba-launcher", ConfigPath: "/etc/maktaba/server.toml"}},
	{"systemd/user/maktaba-server.service", Spec{Kind: KindSystemd, Mode: layout.ModeUser,
		Root: "%h/.local/share/maktaba", Home: "%h"}},
	{"launchd/com.maktaba.server.plist", Spec{Kind: KindLaunchd, Mode: layout.ModeSystem,
		Root: "/usr/local/var/maktaba", LogDir: "/usr/local/var/log/maktaba", ConfigPath: "/usr/local/etc/maktaba/server.toml"}},
	{"launchd/com.maktaba.server.agent.plist", Spec{Kind: KindLaunchd, Mode: layout.ModeUser,
		Root: "__HOME__/Library/Application Support/Maktaba/server", Home: "__HOME__",
		LogDir: "__HOME__/Library/Logs/Maktaba"}},
}

func packagingDir() string { return filepath.Join("..", "..", "..", "..", "deploy", "packaging") }

func TestDeployUnitsMatchRenderers(t *testing.T) {
	for _, g := range goldens {
		got, err := Render(g.spec)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(packagingDir(), filepath.FromSlash(g.file))
		if *update {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.ReplaceAll(string(b), "\r\n", "\n"); want != got {
			t.Errorf("%s differs from the renderer (go test ./internal/service -update):\n--- file\n%s\n--- rendered\n%s", g.file, want, got)
		}
	}
}

func TestSystemdUnitRender(t *testing.T) {
	sys, _ := Render(Spec{Kind: KindSystemd, Mode: layout.ModeSystem, Root: "/var/lib/maktaba"})
	for _, want := range []string{
		"ExecStart=/var/lib/maktaba/maktaba-launcher --root /var/lib/maktaba serve",
		"Restart=always", "RestartForceExitStatus=75", "KillMode=mixed",
		"User=maktaba", "ProtectSystem=strict", "ReadWritePaths=/var/lib/maktaba /var/log/maktaba",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("system unit lacks %q:\n%s", want, sys)
		}
	}
	if strings.Contains(sys, "Environment=MAKTABA_CONFIG") {
		t.Error("MAKTABA_CONFIG without a config path")
	}
	user, _ := Render(Spec{Kind: KindSystemd, Mode: layout.ModeUser, Root: "/home/u/.local/share/maktaba", Home: "/home/u"})
	for _, want := range []string{"RestartForceExitStatus=75", "KillMode=mixed", "WantedBy=default.target"} {
		if !strings.Contains(user, want) {
			t.Errorf("user unit lacks %q", want)
		}
	}
	if strings.Contains(user, "User=") || strings.Contains(user, "ProtectSystem") {
		t.Errorf("user unit carries system settings:\n%s", user)
	}
	s := Spec{Kind: KindSystemd, Mode: layout.ModeUser, Root: "/r", Home: "/home/u"}
	if p := s.UnitPath(); p != "/home/u/.config/systemd/user/maktaba-server.service" {
		t.Errorf("user unit path = %s", p)
	}
	steps, _ := Plan(s)
	if got := steps[len(steps)-1].String(); got != "run   loginctl enable-linger" {
		t.Errorf("user unit plan ends with %q", got)
	}
}

func TestLaunchdPlistRender(t *testing.T) {
	p, _ := Render(Spec{Kind: KindLaunchd, Mode: layout.ModeUser, Root: "/Users/u/Library/Application Support/Maktaba/server", Home: "/Users/u"})
	for _, want := range []string{
		"<string>/Users/u/Library/Application Support/Maktaba/server/maktaba-launcher</string>",
		"<string>--root</string>", "<string>serve</string>",
		"<key>KeepAlive</key>\n  <true/>", "<key>AbandonProcessGroup</key>\n  <true/>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
	if path := (Spec{Kind: KindLaunchd, Mode: layout.ModeUser, Home: "/Users/u"}).UnitPath(); path != "/Users/u/Library/LaunchAgents/com.maktaba.server.plist" {
		t.Errorf("agent path = %s", path)
	}
	if path := (Spec{Kind: KindLaunchd, Mode: layout.ModeSystem}).UnitPath(); path != "/Library/LaunchDaemons/com.maktaba.server.plist" {
		t.Errorf("daemon path = %s", path)
	}
	esc, _ := Render(Spec{Kind: KindLaunchd, Mode: layout.ModeUser, Root: "/a&b<c>", Home: "/h"})
	if !strings.Contains(esc, "/a&amp;b&lt;c&gt;") {
		t.Error("plist strings are not XML-escaped")
	}
}

func TestWindowsTaskRender(t *testing.T) {
	s := Spec{Kind: KindTask, Mode: layout.ModeUser, Root: `C:\Users\u\AppData\Local\Maktaba`}
	x, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<LogonTrigger>", "<LogonType>InteractiveToken</LogonType>",
		"<Interval>PT1M</Interval>", "<Count>3</Count>", "<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		`<Command>C:\Users\u\AppData\Local\Maktaba\maktaba-launcher.exe</Command>`,
		`<Arguments>--root C:\Users\u\AppData\Local\Maktaba --hide-console serve</Arguments>`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("task lacks %q", want)
		}
	}
	sp, _ := Render(Spec{Kind: KindTask, Mode: layout.ModeUser, Root: `C:\Program Data\M`})
	if !strings.Contains(sp, `--root &quot;C:\Program Data\M&quot;`) {
		t.Errorf("a root with a space is not quoted:\n%s", sp)
	}
	steps, _ := Plan(s)
	if !steps[0].UTF16 || !strings.Contains(steps[1].String(), "schtasks /Create /TN Maktaba /XML") {
		t.Errorf("task plan = %v", steps)
	}
	// schtasks wants UTF-16 with a BOM
	b := utf16LE("<Task>é")
	if b[0] != 0xFF || b[1] != 0xFE {
		t.Fatal("no BOM")
	}
	u := make([]uint16, 0, len(b)/2-1)
	for i := 2; i < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	if string(utf16.Decode(u)) != "<Task>é" {
		t.Fatal("utf16 round trip")
	}
}

func TestWindowsServicePlan(t *testing.T) {
	if k := DefaultKind("windows", layout.ModeUser); k != KindTask {
		t.Fatalf("Windows default = %s, want the logon task", k)
	}
	if k := DefaultKind("windows", layout.ModeSystem); k != KindWinService {
		t.Fatalf("Windows system = %s", k)
	}
	steps, err := Plan(Spec{Kind: KindWinService, Mode: layout.ModeSystem, Root: `C:\ProgramData\Maktaba`})
	if err != nil || len(steps) != 1 || !strings.Contains(steps[0].String(), `maktaba-launcher.exe --root C:\ProgramData\Maktaba serve`) {
		t.Fatalf("plan = %v, %v", steps, err)
	}
	if Supported(KindWinService, "linux") || !Supported(KindSystemd, "linux") || Supported(KindLaunchd, "windows") {
		t.Fatal("Supported")
	}
}

func TestInstallRunsPlan(t *testing.T) {
	if !Supported(DefaultKind(runtimeGOOS(), layout.ModeUser), runtimeGOOS()) {
		t.Skip("no service kind here")
	}
	kind := DefaultKind(runtimeGOOS(), layout.ModeUser)
	if kind == KindWinService {
		t.Skip()
	}
	home := t.TempDir()
	root := filepath.Join(home, "maktaba")
	var ran [][]string
	old := runCmd
	runCmd = func(argv []string) error { ran = append(ran, argv); return nil }
	t.Cleanup(func() { runCmd = old })
	s := Spec{Kind: kind, Mode: layout.ModeUser, Root: root, Home: home}
	if err := Install(s, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.UnitPath()); err != nil {
		t.Fatalf("unit not written: %v", err)
	}
	if len(ran) == 0 {
		t.Fatal("no service-manager command run")
	}
}

// A spec that names its server.toml writes the install's pointer to it first, for every
// service manager: a Windows logon task (or service) carries no MAKTABA_CONFIG, so the
// server its launcher starts reads the pointer (config.Path) instead of the defaults.
func TestPlanWritesTheConfigPointer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "maktaba")
	cfg := filepath.Join(t.TempDir(), "elsewhere", "server.toml")
	for _, kind := range []Kind{KindTask, KindWinService, KindSystemd, KindLaunchd} {
		steps, err := Plan(Spec{Kind: kind, Mode: layout.ModeUser, Root: root, Home: t.TempDir(), ConfigPath: cfg})
		if err != nil || len(steps) < 2 {
			t.Fatalf("%s: %v %v", kind, steps, err)
		}
		if steps[0].Path != filepath.Join(root, layout.ConfigPointerFile) || steps[0].Content != cfg+"\n" {
			t.Errorf("%s: first step %+v", kind, steps[0])
		}
	}
	// no server.toml named: no pointer (the server's own default)
	steps, err := Plan(Spec{Kind: KindTask, Mode: layout.ModeUser, Root: root})
	if err != nil || len(steps) != 2 || strings.Contains(steps[0].Path, layout.ConfigPointerFile) {
		t.Fatalf("without a config: %v %v", steps, err)
	}
	// a relative path is made absolute (the task starts in the install root)
	steps, _ = Plan(Spec{Kind: KindTask, Mode: layout.ModeUser, Root: root, ConfigPath: "server.toml"})
	if got := strings.TrimSpace(steps[0].Content); !filepath.IsAbs(got) {
		t.Fatalf("relative config: %q", got)
	}
}

// Installing writes the pointer where the launcher's server reads it (faked service
// manager: no task or unit is registered on this machine).
func TestInstallWritesTheConfigPointer(t *testing.T) {
	kind := DefaultKind(runtimeGOOS(), layout.ModeUser)
	if !Supported(kind, runtimeGOOS()) || kind == KindWinService {
		t.Skip("no user service kind here")
	}
	home := t.TempDir()
	root := filepath.Join(home, "maktaba")
	cfg := filepath.Join(home, "config", "server.toml")
	old := runCmd
	runCmd = func([]string) error { return nil }
	t.Cleanup(func() { runCmd = old })
	if err := Install(Spec{Kind: kind, Mode: layout.ModeUser, Root: root, Home: home, ConfigPath: cfg}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if got := layout.ReadConfigPointer(root); got != cfg {
		t.Fatalf("pointer %q, want %q", got, cfg)
	}
}
