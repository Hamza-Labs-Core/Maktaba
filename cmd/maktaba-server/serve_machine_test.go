package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/plugins"
)

func fakeMachineOf(t *testing.T, gib uint64) {
	t.Helper()
	prev := detectMachine
	prevRun := runningTier.Load()
	t.Cleanup(func() { detectMachine = prev; runningTier.Store(prevRun) })
	detectMachine = func() hostinfo.Machine {
		return hostinfo.Machine{Platform: hostinfo.PlatformDSM, Arch: "amd64",
			Memory: hostinfo.Memory{LimitBytes: gib << 30, TotalBytes: 32 << 30}}
	}
	runningTier.Store(nil)
}

func tomlWith(t *testing.T, server string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server.toml")
	body := "[server]\ndata_dir = " + strconvQuote(filepath.Join(filepath.Dir(p), "data")) + "\n" + server
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

// The tier at start: detected from the memory limit and written to server.toml; memory
// added moves it up and says so; an admin's pick stays whatever the memory.
func TestResolveTier(t *testing.T) {
	fakeMachineOf(t, 6)
	path := tomlWith(t, "")
	cfg, _, _ := config.LoadFrom(path)
	var log bytes.Buffer
	mt := resolveTier(&cfg, path, &log)
	if mt.Tier != hostinfo.TierStandard || cfg.Server.Tier != "standard" || mt.MovedUp || mt.Chosen {
		t.Fatalf("6 GiB: %+v", mt)
	}
	if saved, _, _ := config.LoadFrom(path); saved.Server.Tier != "standard" || saved.Server.TierChosen != "" {
		t.Fatalf("saved %+v", saved.Server)
	}
	if !strings.Contains(log.String(), "hardware tier standard") {
		t.Fatalf("log: %s", log.String())
	}

	// memory added: Strong, said once
	fakeMachineOf(t, 32)
	cfg, _, _ = config.LoadFrom(path)
	log.Reset()
	if mt := resolveTier(&cfg, path, &log); mt.Tier != hostinfo.TierStrong || !mt.MovedUp ||
		!strings.Contains(log.String(), "moved up to the strong tier") {
		t.Fatalf("moved up: %+v %s", mt, log.String())
	}
	cfg, _, _ = config.LoadFrom(path)
	if mt := resolveTier(&cfg, path, &log); mt.MovedUp {
		t.Fatal("the move up is said again at the next start")
	}

	// an admin's pick stands
	path = tomlWith(t, "tier = \"light\"\ntier_chosen = \"admin\"\n")
	cfg, _, _ = config.LoadFrom(path)
	if mt := resolveTier(&cfg, path, &log); mt.Tier != hostinfo.TierLight || !mt.Chosen || mt.MovedUp {
		t.Fatalf("picked: %+v", mt)
	}
}

func machineCall(h http.Handler, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/config/machine", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// GET /config/machine says the tier and why; PUT saves an admin's pick for the next start
// (auto detects again); a non-tier is refused; no token, no answer.
func TestConfigMachineRoutes(t *testing.T) {
	fakeMachineOf(t, 8)
	path := tomlWith(t, "")
	cfg, _, _ := config.LoadFrom(path)
	mt := resolveTier(&cfg, path, &bytes.Buffer{})
	runningTier.Store(&mt)
	c := &configAdmin{path: path, token: "tok"}
	h := c.adminHandler(http.NotFoundHandler())

	req := httptest.NewRequest("GET", "/config/machine", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no token: %d", rec.Code)
	}

	rec = machineCall(h, "GET", "")
	var doc machineDoc
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil {
		t.Fatalf("GET %d %s", rec.Code, rec.Body)
	}
	if doc.Tier != hostinfo.TierStandard || doc.Saved != "auto" || doc.RestartNeeded || !doc.NAS ||
		doc.Reason.String() != "Standard: 8 GB, no GPU for video" || doc.Defaults.PipelineWorkers != 2 {
		t.Fatalf("doc %+v", doc)
	}

	rec = machineCall(h, "PUT", `{"tier":"huge"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a non-tier: %d %s", rec.Code, rec.Body)
	}

	rec = machineCall(h, "PUT", `{"tier":"light"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc.Saved != "light" || !doc.RestartNeeded ||
		doc.Tier != hostinfo.TierStandard {
		t.Fatalf("pick light: %d %s", rec.Code, rec.Body)
	}
	if saved, _, _ := config.LoadFrom(path); saved.Server.Tier != "light" || saved.Server.TierChosen != TierChosenAdmin {
		t.Fatalf("saved %+v", saved.Server)
	}

	rec = machineCall(h, "PUT", `{"tier":"auto"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc.Saved != "auto" || doc.RestartNeeded {
		t.Fatalf("auto: %d %s", rec.Code, rec.Body)
	}
	if saved, _, _ := config.LoadFrom(path); saved.Server.Tier != "standard" || saved.Server.TierChosen != "" {
		t.Fatalf("saved %+v", saved.Server)
	}
	if rec := machineCall(h, "DELETE", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE: %d", rec.Code)
	}
}

// Epic 59 story 59.02: the quiet hours and the data disk's answer are saved from the web;
// a window that does not parse is refused; null goes back to the tier's default.
func TestConfigMachineQuietHoursAndDisk(t *testing.T) {
	fakeMachineOf(t, 8)
	path := tomlWith(t, "")
	cfg, _, _ := config.LoadFrom(path)
	mt := resolveTier(&cfg, path, &bytes.Buffer{})
	runningTier.Store(&mt)
	quiet := "01:00-06:00"
	runningQuiet.Store(&quiet)
	t.Cleanup(func() { runningQuiet.Store(nil) })
	h := (&configAdmin{path: path, token: "tok"}).adminHandler(http.NotFoundHandler())

	var doc machineDoc
	rec := machineCall(h, "GET", "")
	if json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc.QuietHours != "01:00-06:00" || doc.QuietSet || doc.RestartNeeded {
		t.Fatalf("GET %s", rec.Body)
	}
	if rec := machineCall(h, "PUT", `{"quiet_hours":"25:00-06:00"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a bad window: %d %s", rec.Code, rec.Body)
	}
	rec = machineCall(h, "PUT", `{"quiet_hours":"22:00-06:00","data_disk":"ssd"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc.QuietHours != "22:00-06:00" || !doc.QuietSet ||
		!doc.RestartNeeded || doc.DiskHow != "asked" || doc.Rotational {
		t.Fatalf("PUT %d %s", rec.Code, rec.Body)
	}
	saved, _, _ := config.LoadFrom(path)
	if saved.Schedule.QuietHours == nil || *saved.Schedule.QuietHours != "22:00-06:00" || saved.Server.DataDisk != "ssd" {
		t.Fatalf("saved %+v %+v", saved.Schedule, saved.Server)
	}
	rec = machineCall(h, "PUT", `{"quiet_hours":""}`)
	if json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc.QuietHours != "" || !doc.QuietSet {
		t.Fatalf("none: %s", rec.Body)
	}
	rec = machineCall(h, "PUT", `{"quiet_default":true,"data_disk":"auto"}`)
	if json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc.QuietHours != "01:00-06:00" || doc.QuietSet || doc.DiskHow == "asked" {
		t.Fatalf("back to the default: %s", rec.Body)
	}
}

// Epic 59 story 59.07: GET /config/machine says the plugin isolation level, from the
// kernel's facts before the plugins runner's first pass: Container on Linux without
// Landlock or user namespaces, Full with both (and on macOS and Windows).
func TestConfigMachinePluginIsolation(t *testing.T) {
	for _, c := range []struct {
		name   string
		kernel hostinfo.Kernel
		want   string
	}{
		{"every layer", hostinfo.Kernel{Landlock: 4, UserNS: boolPtr(true)}, plugins.LevelFull},
		{"a NAS kernel", hostinfo.Kernel{Landlock: 0, UserNS: boolPtr(false)}, map[bool]string{true: plugins.LevelContainer, false: plugins.LevelFull}[runtime.GOOS == "linux"]},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeMachineOf(t, 8)
			prev := detectMachine
			detectMachine = func() hostinfo.Machine { m := prev(); m.Kernel = c.kernel; return m }
			path := tomlWith(t, "")
			cfg, _, _ := config.LoadFrom(path)
			mt := resolveTier(&cfg, path, &bytes.Buffer{})
			runningTier.Store(&mt)
			h := (&configAdmin{path: path, token: "tok"}).adminHandler(http.NotFoundHandler())
			var doc machineDoc
			if rec := machineCall(h, "GET", ""); rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil {
				t.Fatalf("GET %d %s", rec.Code, rec.Body)
			}
			if doc.PluginIsolation.Level != c.want {
				t.Fatalf("plugin_isolation %+v, want %s", doc.PluginIsolation, c.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
