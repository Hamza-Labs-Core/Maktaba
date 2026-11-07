package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// RolePlugins is the plugin host (Epic 54, stories 54.02, 54.03, 54.37 §4): one
// maktaba-plugind per home, the catalog's plugins.host. It runs third-party plugin code in
// WebAssembly inside its OS sandbox, and only while a plugin is enabled (on_demand): the
// reconciler opens its gate then.
const RolePlugins Role = "plugins"

// PluginsSandboxExit is maktaba-plugind's exit when its sandbox cannot be applied (no
// plugin ran): not restarted, reported sandbox_unavailable.
const PluginsSandboxExit = 78

// ReasonSandboxUnavailable is the reason of a host that exited PluginsSandboxExit.
const ReasonSandboxUnavailable = "sandbox_unavailable"

// EnvPluginAPISocket names the plugin API's unix socket for the API (served only there,
// never on TCP: story 54.04 §1); EnvPluginDSocket the host's dispatch socket for the
// pipeline's dispatcher (story 54.06 §3).
const (
	EnvPluginAPISocket = "MAKTABA_PLUGIN_API_SOCKET"
	EnvPluginDSocket   = "MAKTABA_PLUGIND_SOCKET"
	// EnvPluginsRoot is a joined machine's plugins folder (its copies of the packages, 54.42)
	EnvPluginsRoot = "MAKTABA_PLUGINS_ROOT"
)

// PluginPaths are the plugin host's folders and sockets on this machine.
type PluginPaths struct {
	// Root is <state>/plugins: one folder per plugin (story 54.38), the packs layout.
	Root string
	// Cache is the compile cache (<state>/plugins/.cache: hidden from the layout).
	Cache string
	// Run is <data>/run/plugins, the host's run folder; Dispatch its socket there.
	Run      string
	Dispatch string
	// API is the plugin API's socket (<data>/run/plugin-api.sock).
	API string
	// Sockets, when set, is the short folder the two sockets moved to (PluginsPaths).
	Sockets string
}

// maxSocketPath is the longest unix socket path used as it is (sun_path holds 104 bytes
// on macOS, 108 on Linux, with its NUL).
const maxSocketPath = 100

// PluginsPaths are cfg's plugin paths. The two sockets are in <data>/run, or, when that
// path is too long for a unix socket, in /tmp/maktaba-<12 hex of the data folder>/ (a
// folder of this user only, made by the plugins role's start).
func PluginsPaths(cfg config.Config) PluginPaths {
	root := filepath.Join(cfg.StateDir(), "plugins")
	run := filepath.Join(cfg.Server.DataDir, "run", "plugins")
	p := PluginPaths{Root: root, Cache: filepath.Join(root, ".cache"), Run: run,
		Dispatch: filepath.Join(run, "plugind.sock"), API: filepath.Join(cfg.Server.DataDir, "run", "plugin-api.sock")}
	if len(p.Dispatch) > maxSocketPath || len(p.API) > maxSocketPath {
		sum := sha256.Sum256([]byte(filepath.Clean(cfg.Server.DataDir)))
		dir := filepath.Join("/tmp", "maktaba-"+hex.EncodeToString(sum[:6]))
		p.Sockets = dir
		p.Dispatch, p.API = filepath.Join(dir, "plugind.sock"), filepath.Join(dir, "plugin-api.sock")
	}
	return p
}

// PluginsHere reports whether this machine runs plugins: a home on Postgres (the plugin
// tables and job_stages' longer names are Postgres's, #693), or a full node of one (a
// plugin placed any or gpu runs there, its writes sent to the home: story 54.42), and not
// Windows yet (the pipeline's dispatcher reaches the host on a unix socket, which asyncio
// cannot open there).
func PluginsHere(cfg config.Config) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	u := strings.ToLower(strings.TrimSpace(cfg.Database.URL))
	if n, err := NodeOf(cfg); err == nil && NodeMode(cfg, n) {
		u = strings.ToLower(HomeDSN(n))
	}
	return strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://")
}

// PluginsPool is the host's pool: the plugins pool's size, a quarter of the cores (at
// least 1, at most 8: README §4.7).
func PluginsPool() int {
	n := runtime.NumCPU() / 4
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	return n
}

// BuildPlugins is the plugin host's process: maktaba-plugind with the plugin folders and
// both sockets named, built dormant (its gate opens while a plugin is on). A binary that
// is not in this payload leaves it not_in_payload.
func BuildPlugins(opts Options) (Process, *PluginsHealth, error) {
	bin, err := PlugindBin(opts)
	if err != nil {
		return Process{}, nil, err
	}
	p := PluginsPaths(opts.Config)
	args := []string{"--data", opts.Config.Server.DataDir, "--plugins-dir", p.Root, "--cache-dir", p.Cache,
		"--run-dir", p.Run, "--api-socket", p.API, "--dispatch-socket", p.Dispatch, "--pool", strconv.Itoa(PluginsPool())}
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	wire(cmd)
	h := &PluginsHealth{Socket: p.Dispatch}
	return Process{
		Role: RolePlugins, Cmd: cmd, Dormant: true,
		Probe: "unix:" + p.Dispatch, Check: h.Check,
		BeforeStart: func() {
			_ = os.MkdirAll(p.Root, 0o700)
			_ = os.MkdirAll(p.Run, 0o700)
			if p.Sockets != "" {
				privateDir(p.Sockets)
			}
			_ = os.Remove(SandboxStepFile(p.Run))
		},
		FatalExits: map[int]string{PluginsSandboxExit: ReasonSandboxUnavailable},
		Present:    func() bool { return isExecutable(bin) },
	}, h, nil
}

// PlugindBin is maktaba-plugind in this payload (MAKTABA_PLUGIND_BIN, the release
// directory, beside this binary); its bare name when it is not there (Present false).
func PlugindBin(opts Options) (string, error) {
	bin, err := opts.resolve("maktaba-plugind", "MAKTABA_PLUGIND_BIN")
	if err != nil {
		if !errors.Is(err, ErrBinaryNotFound) {
			return "", err
		}
		bin = "maktaba-plugind"
	}
	return bin, nil
}

// PreloadArgs are maktaba-plugind's arguments to check a package folder (--preload): the
// host's own folders, so the compile cache is the one its jobs read.
func PreloadArgs(cfg config.Config) func(dir, id, version string) []string {
	p := PluginsPaths(cfg)
	return func(dir, id, version string) []string {
		return []string{"--data", cfg.Server.DataDir, "--plugins-dir", p.Root, "--cache-dir", p.Cache,
			"--run-dir", filepath.Join(p.Run, "preload"), "--api-socket", p.API,
			"--preload", dir, "--plugin-id", id, "--plugin-version", version}
	}
}

// SandboxStepFile is where maktaba-plugind writes the sandbox step that failed before it
// exits PluginsSandboxExit.
func SandboxStepFile(run string) string { return filepath.Join(run, "sandbox_unavailable") }

// PluginsHealth checks the host over its dispatch socket (a health frame, story 54.02 §2)
// and keeps the last answer: its sandbox level (full, or partial with what is missing).
type PluginsHealth struct {
	Socket string
	mu     sync.Mutex
	last   HostHealth
	at     time.Time
}

// HostHealth is the host's health frame.
type HostHealth struct {
	OK             bool     `json:"ok"`
	Ready          bool     `json:"ready"`
	Wasmtime       string   `json:"wasmtime"`
	PluginAPI      string   `json:"plugin_api"`
	Sandbox        string   `json:"sandbox"`
	SandboxMissing []string `json:"sandbox_missing"`
	Loaded         int      `json:"loaded"`
	Running        int      `json:"running"`
}

// Check asks the host for its health: an error when it does not answer ok.
func (h *PluginsHealth) Check(ctx context.Context) error {
	hh, err := AskHostHealth(ctx, h.Socket)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.last, h.at = hh, time.Now()
	h.mu.Unlock()
	if !hh.OK {
		return errors.New("the plugin host answered not ok")
	}
	return nil
}

// Last is the last health answer (ok false: none yet).
func (h *PluginsHealth) Last() (HostHealth, time.Time, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last, h.at, !h.at.IsZero()
}

// AskHostHealth sends one health frame on the host's dispatch socket.
func AskHostHealth(ctx context.Context, socket string) (HostHealth, error) {
	var out HostHealth
	var raw json.RawMessage
	err := HostCall(ctx, socket, map[string]any{"type": "health"}, &raw)
	if err != nil {
		return out, err
	}
	var frame struct {
		Type string `json:"type"`
		HostHealth
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		return out, err
	}
	if frame.Type != "health" {
		return out, fmt.Errorf("the plugin host answered %q", frame.Type)
	}
	return frame.HostHealth, nil
}

// maxHostFrame is the largest frame the host sends (plugind's protocol.rs MAX_FRAME).
const maxHostFrame = 4 << 20

// HostCall sends req as one frame (a 4-byte big-endian length and JSON) on the host's
// socket and reads one answer frame into out.
func HostCall(ctx context.Context, socket string, req any, out *json.RawMessage) error {
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := d.DialContext(dctx, "unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	head := make([]byte, 4)
	binary.BigEndian.PutUint32(head, uint32(len(body)))
	if _, err := conn.Write(append(head, body...)); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(head)
	if n > maxHostFrame {
		return fmt.Errorf("the plugin host's answer is %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	*out = buf
	return nil
}

// privateDir makes dir this user's own folder (0700): a link or another user's file in its
// place in /tmp is removed first.
func privateDir(dir string) {
	if info, err := os.Lstat(dir); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		_ = os.Remove(dir)
	}
	_ = os.MkdirAll(dir, 0o700)
	_ = os.Chmod(dir, 0o700)
}
