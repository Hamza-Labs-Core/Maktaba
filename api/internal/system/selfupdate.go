// Server self-update handler (Epic 28, Story 28.3).
//
// POST /api/admin/system/update downloads the release for this host from
// the GitHub release of the pinned repository, verifies it with
// shared/go/releaseverify (Epic 35 story 35.1: the Sigstore bundle over
// checksums.txt against the pinned release-workflow identity and the Rekor
// inclusion proof, release.json, the archive's and the extracted binary's
// digests, and the no-downgrade watermark), atomically swaps the running
// maktaba-server binary (keeping a .bak), raises the watermark and re-execs. Package-managed (.deb/.rpm) installs
// shell out to the package manager instead; Docker installs can't replace
// their own image and get a 409 with instructions.
//
// The swap strategy mirrors the proven CLI updater in
// cmd/maktaba-server/internal/selfupdate: write the new binary next to
// the target, rename the live one aside, rename the new one into place —
// each rename is atomic within a directory, and the .bak makes a
// mid-swap crash recoverable. If the re-exec of the swapped binary fails
// (the new file cannot be executed), rollbackBinary puts the .bak back, the
// old process keeps serving, and GET /api/system/updates reports
// rolled_back so the About page says so. Download/verify/extract live in releaseverify
// (shared with `maktaba-server update` and the node installers) so the only
// filesystem-mutating step here is isolated and testable.
package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/install"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// installKind is how this maktaba-server was installed, which decides the
// update path.
type installKind int

const (
	installBinary  installKind = iota // plain archive / Homebrew / manual
	installDocker                     // container — can't self-replace
	installDeb                        // apt-managed
	installRPM                        // dnf/yum-managed
	installRuntime                    // the Linux app's runtime copy, updated by the machine (Epic 39)
)

// SelfUpdater wires the self-update handler to the update-check service
// (it reuses the Updater to resolve the target release and assets) and
// guards against concurrent updates with a process-wide mutex.
type SelfUpdater struct {
	u *Updater

	mu       sync.Mutex
	inFlight bool
	// rolledBack is the last update whose re-exec failed and was undone
	// (nil: none since this process started).
	rolledBack *RollbackInfo

	// injection points for tests
	httpc     *http.Client
	execSelf  func(path string) error // re-exec; default syscall.Exec
	detect    func(self string) installKind
	runPkgMgr func(kind installKind, version string) ([]byte, error)
	// runRuntime starts a runtime copy's update (default startRuntimeUpdate).
	runRuntime   func(server, version string) (unit string, out []byte, err error)
	selfPathFunc func() (string, error)
	// trusted returns the Sigstore trust root (default: the Updater's
	// RootStore, refreshed through TUF first, else the embedded root).
	trusted func(ctx context.Context) (root.TrustedMaterial, error)
	// platform is the release.json assets key ("" = this host).
	platform string
}

// NewSelfUpdater builds a SelfUpdater backed by u.
func NewSelfUpdater(u *Updater) *SelfUpdater {
	return &SelfUpdater{
		u:            u,
		httpc:        &http.Client{Timeout: 10 * time.Minute},
		execSelf:     reexecSelf,
		detect:       detectInstall,
		runPkgMgr:    runPackageManager,
		runRuntime:   startRuntimeUpdate,
		selfPathFunc: resolveSelf,
		trusted:      u.trustedMaterial,
	}
}

// WatermarkPath is where a flat (pre-35.2 layout) install keeps the highest
// version it ever installed: next to the binary, shared with
// `maktaba-server update`. Story 35.2 moves it to <install>/watermark.
func WatermarkPath(self string) string { return releaseverify.FlatWatermarkPath(self) }

type updateRequest struct {
	Version string `json:"version"` // empty ⇒ latest on the channel
	Confirm bool   `json:"confirm"`
}

type dockerResponse struct {
	Install      string `json:"install"`
	Instructions string `json:"instructions"`
	Image        string `json:"image"`
}

// Handler returns the POST /api/admin/system/update handler.
func (s *SelfUpdater) Handler() http.Handler {
	return http.HandlerFunc(s.handle)
}

func (s *SelfUpdater) handle(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	var req updateRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if !req.Confirm {
		httperror.Write(w, r, httperror.BadRequest("confirmation required: set \"confirm\": true"))
		return
	}

	// Single-flight: a second update while one is running gets 409.
	s.mu.Lock()
	if s.inFlight {
		s.mu.Unlock()
		httperror.Write(w, r, httperror.Conflict("update_in_progress", "an update is already in progress"))
		return
	}
	s.inFlight = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.inFlight = false; s.mu.Unlock() }()

	// Resolve the target release (latest on the channel; the explicit
	// version, if given, must be the resolved latest — we only update
	// forward to what the check service reports).
	status := s.u.Status(r.Context(), true)
	if status.Disabled {
		httperror.Write(w, r, httperror.Conflict("update_disabled", "update checks are disabled"))
		return
	}
	if req.Version != "" && compareSemver(req.Version, status.LatestVersion) != 0 {
		httperror.Write(w, r, httperror.BadRequest(
			fmt.Sprintf("requested %s but latest on channel %q is %s", req.Version, status.Channel, status.LatestVersion)))
		return
	}
	if !status.Available {
		httperror.Write(w, r, httperror.Conflict("already_current",
			"already running the latest version: "+status.CurrentVersion))
		return
	}

	self, err := s.selfPathFunc()
	if err != nil {
		httperror.Write(w, r, httperror.Internal("locate running binary"))
		return
	}

	// A self-managed install (Epic 35 story 35.2; package installs too): the
	// release is staged side by side in bin/releases/<v>/, becomes current
	// under the launcher's boot-loop guard, and the server restarts on it
	// (exit 75 through the launcher). The running binaries are never replaced.
	if l, ok := install.Root(self); ok {
		s.layoutUpdate(w, r, l, status)
		return
	}

	switch s.detect(self) {
	case installDocker:
		// A container can't replace its own image; tell the operator how.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(dockerResponse{
			Install:      "docker",
			Instructions: dockerInstructions,
			Image:        dockerImage(status.LatestVersion),
		})
		return
	case installDeb:
		s.packageManagerUpdate(w, r, installDeb, status.LatestVersion)
		return
	case installRPM:
		s.packageManagerUpdate(w, r, installRPM, status.LatestVersion)
		return
	case installRuntime:
		s.runtimeUpdate(w, r, self, status.LatestVersion)
		return
	}

	// Plain binary install: download → verify → swap → re-exec.
	if err := s.applyBinary(r.Context(), self, status); err != nil {
		s.writeApplyError(w, r, err)
		return
	}

	// The swap succeeded; report success, then re-exec so the new binary
	// takes over. The response is flushed before exec replaces the
	// process image.
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"updated":     true,
		"version":     status.LatestVersion,
		"restarting":  true,
		"rollback_to": filepath.Base(self) + ".bak",
	})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	// Re-exec; if the new binary cannot be executed the old one is put back
	// (restart).
	go func() {
		time.Sleep(500 * time.Millisecond) // let the response drain
		s.restart(self, status.LatestVersion)
	}()
}

// RollbackInfo is a self-update that was undone: the release that could
// not start, why, and when.
type RollbackInfo struct {
	Version string    `json:"version"`
	Reason  string    `json:"reason"`
	At      time.Time `json:"at"`
}

// errReexecUnsupported is the re-exec of a platform without syscall.Exec
// (Windows): the binary is swapped and a supervisor relaunches the
// process, so it is not a failure to roll back.
var errReexecUnsupported = errors.New("re-exec unsupported on this platform")

// restart re-execs the swapped binary at self. When the exec fails (the new
// file is not runnable here: a wrong architecture, a truncated file), the
// running process is still the old version, so the .bak is restored with
// rollbackBinary: the next start runs the old version too, and the failed
// release is reported as rolled back.
func (s *SelfUpdater) restart(self, target string) {
	err := s.execSelf(self)
	if err == nil || errors.Is(err, errReexecUnsupported) {
		return
	}
	if rbErr := rollbackBinary(self); rbErr != nil {
		s.u.log.Error("self-update: re-exec failed and the old binary could not be restored",
			"event", "update_rollback_error", "version", target, "err", err.Error(), "rollback_err", rbErr.Error())
		return
	}
	s.mu.Lock()
	s.rolledBack = &RollbackInfo{Version: target, Reason: err.Error(), At: time.Now().UTC()}
	s.mu.Unlock()
	s.u.log.Warn("self-update: re-exec failed; rolled back to the previous binary",
		"event", "update_rolled_back", "version", target, "err", err.Error())
}

// LastRollback is the last undone self-update (nil: none).
func (s *SelfUpdater) LastRollback() *RollbackInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rolledBack == nil {
		return nil
	}
	cp := *s.rolledBack
	return &cp
}

// InstallName is how this server was installed, as GET
// /api/system/updates reports it: "managed" (the self-managed layout,
// Epic 35.2), "docker", "deb", "rpm" or "binary".
func (s *SelfUpdater) InstallName() string {
	self, err := s.selfPathFunc()
	if err != nil {
		return "binary"
	}
	if _, ok := install.Root(self); ok {
		return "managed"
	}
	switch s.detect(self) {
	case installDocker:
		return "docker"
	case installDeb:
		return "deb"
	case installRPM:
		return "rpm"
	case installRuntime:
		return "runtime"
	}
	return "binary"
}

// dockerInstructions is what an operator runs to update a container.
const dockerInstructions = "docker compose pull && docker compose up -d"

func dockerImage(latest string) string {
	return "ghcr.io/hamza-labs-core/maktaba-server:" + strings.TrimPrefix(latest, "v")
}

// StatusHandler decorates GET /api/system/updates with how this server
// updates, so the About page can show the Docker instructions up front
// instead of after a failed click: install, and for docker the
// instructions and image; plus rolled_back when the last self-update was
// undone.
func (s *SelfUpdater) StatusHandler(base http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusCapture{header: http.Header{}}
		base.ServeHTTP(rec, r)
		var doc map[string]any
		if (rec.code != 0 && rec.code != http.StatusOK) || json.Unmarshal(rec.body, &doc) != nil {
			for k, v := range rec.header {
				w.Header()[k] = v
			}
			if rec.code == 0 {
				rec.code = http.StatusOK
			}
			w.WriteHeader(rec.code)
			_, _ = w.Write(rec.body)
			return
		}
		kind := s.InstallName()
		doc["install"] = kind
		if kind == "docker" {
			doc["instructions"] = dockerInstructions
			if v, _ := doc["latest_version"].(string); v != "" {
				doc["image"] = dockerImage(v)
			}
		}
		if rb := s.LastRollback(); rb != nil {
			doc["rolled_back"] = rb
		}
		common.WriteJSON(w, r, http.StatusOK, doc)
	})
}

// statusCapture buffers the decorated handler's response.
type statusCapture struct {
	header http.Header
	code   int
	body   []byte
}

func (c *statusCapture) Header() http.Header { return c.header }
func (c *statusCapture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}
func (c *statusCapture) Write(b []byte) (int, error) {
	c.body = append(c.body, b...)
	return len(b), nil
}

// applyBinary performs the download/verify/swap. Errors are sentinel-wrapped
// so the handler can map them to HTTP statuses. Nothing touches the disk
// until releaseverify accepted the release and the watermark allows it.
func (s *SelfUpdater) applyBinary(ctx context.Context, self string, status UpdateStatus) error {
	dir := filepath.Dir(self)
	if err := writableDir(dir); err != nil {
		return fmt.Errorf("%w: %s", errNotWritable, dir)
	}
	id, err := s.u.Identity()
	if err != nil {
		return fmt.Errorf("%w: %v", errVerify, err)
	}
	tm, err := s.trusted(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", errVerify, err)
	}
	assets := make(map[string]string, len(status.Assets))
	for _, a := range status.Assets {
		assets[a.Name] = a.URL
	}
	up, err := releaseverify.FetchServer(ctx, releaseverify.HTTPDownloader(s.httpc),
		releaseverify.Input{Tag: status.LatestVersion, Identity: id, TrustedMaterial: tm}, assets, s.platform)
	if err != nil {
		var rvErr *releaseverify.Error
		switch {
		case errors.Is(err, releaseverify.ErrNotFound):
			return fmt.Errorf("%w: %v", errNoAsset, err)
		case errors.As(err, &rvErr):
			return fmt.Errorf("%w: %v", errVerify, err)
		default:
			return fmt.Errorf("%w: %v", errDownload, err)
		}
	}
	wm := releaseverify.WatermarkFile{Path: WatermarkPath(self)}
	cur, err := wm.Load()
	if err != nil {
		return fmt.Errorf("%w: read watermark: %v", errVerify, err)
	}
	if err := releaseverify.CheckMonotonic(cur, up.Result); err != nil {
		return fmt.Errorf("%w: %v", errDowngrade, err)
	}
	if err := diskCheck(dir, 2*int64(len(up.Binary))); err != nil {
		return err
	}
	if err := swapBinary(self, up.Binary); err != nil {
		return fmt.Errorf("%w: %v", errSwap, err)
	}
	if err := wm.Commit(up.Result); err != nil {
		// The new binary is in place and verified; a watermark that could not
		// be raised only weakens the downgrade guard until the next update.
		s.u.log.Warn("self-update: watermark not raised", "event", "update_watermark_error", "err", err.Error())
	}
	return nil
}

func (s *SelfUpdater) packageManagerUpdate(w http.ResponseWriter, r *http.Request, kind installKind, version string) {
	out, err := s.runPkgMgr(kind, version)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("package manager update failed: "+strings.TrimSpace(string(out))))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"updated": true,
		"via":     pkgMgrName(kind),
		"version": version,
		"output":  string(out),
	})
}

// --- error sentinels → HTTP mapping ---

var (
	errNotWritable = errors.New("binary directory not writable")
	errNoAsset     = errors.New("no platform asset")
	errDownload    = errors.New("download failed")
	errVerify      = errors.New("verification failed")
	errDowngrade   = errors.New("downgrade or replay refused")
	errSwap        = errors.New("binary swap failed")
	errDiskSpace   = errors.New("insufficient disk space")
)

func (s *SelfUpdater) writeApplyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errNotWritable):
		httperror.Write(w, r, httperror.Forbidden("not_writable",
			"the binary's directory is not writable by this process — use the package manager or run as the install owner"))
	case errors.Is(err, errNoAsset):
		httperror.Write(w, r, httperror.NotFound(err.Error()))
	case errors.Is(err, errDiskSpace):
		httperror.Write(w, r, &httperror.Error{
			Type:   "about:blank#insufficient-storage",
			Title:  "insufficient storage",
			Status: http.StatusInsufficientStorage,
			Detail: err.Error(),
		})
	case errors.Is(err, errDowngrade):
		httperror.Write(w, r, httperror.Conflict("downgrade_refused", err.Error()))
	case errors.Is(err, errDownload), errors.Is(err, errVerify):
		httperror.Write(w, r, httperror.BadGateway(err.Error()))
	default:
		httperror.Write(w, r, httperror.Internal(err.Error()))
	}
}

// --- pure helpers (unit-tested) ---

// swapBinary atomically replaces target with body, keeping target+".bak".
func swapBinary(target string, body []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".maktaba-server.new.*")
	if err != nil {
		return fmt.Errorf("temp binary: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close new binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("chmod new binary: %w", err)
	}
	bak := target + ".bak"
	_ = os.Remove(bak)
	// Move the live binary aside first (Windows can rename a running .exe
	// but not delete it), then move the new one into place.
	if err := os.Rename(target, bak); err != nil {
		return fmt.Errorf("move current binary aside: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Rename(bak, target) // roll back so we don't leave no binary
		return fmt.Errorf("move new binary into place: %w", err)
	}
	return nil
}

// rollbackBinary restores target from target+".bak": restart calls it
// when the swapped binary cannot be executed.
func rollbackBinary(target string) error {
	bak := target + ".bak"
	if _, err := os.Stat(bak); err != nil {
		return fmt.Errorf("no rollback binary: %w", err)
	}
	tmp := target + ".rollback.tmp"
	if err := copyFileMode(bak, tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func copyFileMode(src, dst string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, mode)
}

// writableDir reports whether the process can create files in dir.
func writableDir(dir string) error {
	f, err := os.CreateTemp(dir, ".maktaba-write-probe.*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// diskCheck refuses the update if free space is below need bytes.
func diskCheck(dir string, need int64) error {
	free, err := DiskFreeBytes(dir)
	if err != nil {
		return nil // best-effort: a stat failure shouldn't block the update
	}
	if int64(free) < need {
		return fmt.Errorf("%w: need %d bytes free in %s, have %d", errDiskSpace, need, dir, free)
	}
	return nil
}

// --- install detection + package managers ---

// layoutUpdate is the self-update of a self-managed install.
func (s *SelfUpdater) layoutUpdate(w http.ResponseWriter, r *http.Request, l layout.Layout, status UpdateStatus) {
	st, err := s.stageLayout(r.Context(), l, status)
	if err != nil {
		s.writeApplyError(w, r, err)
		return
	}
	if err := st.Activate(l, "api"); err != nil {
		s.writeApplyError(w, r, fmt.Errorf("%w: %v", errSwap, err))
		return
	}
	if err := l.RequestRestart(); err != nil {
		s.u.log.Warn("self-update: restart request not written", "event", "update_restart_request_error", "err", err.Error())
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"updated":     true,
		"version":     status.LatestVersion,
		"restarting":  true,
		"layout":      true,
		"rollback_to": func() string { p, _ := l.Previous(); return p }(),
	})
}

// stageLayout downloads, verifies and stages the release (the shared install
// flow, releaseverify/install). Errors carry the handler's sentinels.
func (s *SelfUpdater) stageLayout(ctx context.Context, l layout.Layout, status UpdateStatus) (*install.Staged, error) {
	id, err := s.u.Identity()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errVerify, err)
	}
	tm, err := s.trusted(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errVerify, err)
	}
	if err := writableDir(l.ReleasesDir()); err != nil {
		return nil, fmt.Errorf("%w: %s", errNotWritable, l.ReleasesDir())
	}
	assets := make(map[string]string, len(status.Assets))
	for _, a := range status.Assets {
		assets[a.Name] = a.URL
	}
	st, err := install.Stage(ctx, l, releaseverify.HTTPDownloader(s.httpc),
		releaseverify.Input{Tag: status.LatestVersion, Identity: id, TrustedMaterial: tm}, assets, s.platform)
	if err != nil {
		var rvErr *releaseverify.Error
		switch {
		case errors.As(err, &rvErr) && (errors.Is(rvErr.Kind, releaseverify.ErrDowngrade) || errors.Is(rvErr.Kind, releaseverify.ErrReplay)):
			return nil, fmt.Errorf("%w: %v", errDowngrade, err)
		case errors.Is(err, releaseverify.ErrNotFound):
			return nil, fmt.Errorf("%w: %v", errNoAsset, err)
		case errors.As(err, &rvErr):
			return nil, fmt.Errorf("%w: %v", errVerify, err)
		default:
			return nil, fmt.Errorf("%w: %v", errDownload, err)
		}
	}
	return st, nil
}

func resolveSelf() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		return resolved, nil
	}
	return self, nil
}

func detectInstall(self string) installKind {
	if os.Getenv(EnvRuntimeOwner) == RuntimeOwnerHeadless { // set by the runtime's launcher
		return installRuntime
	}
	if fileExists("/.dockerenv") || cgroupMentionsContainer() {
		return installDocker
	}
	if strings.HasPrefix(self, "/usr/") {
		if fileExists("/var/lib/dpkg/status") {
			return installDeb
		}
		if dirExists("/var/lib/rpm") {
			return installRPM
		}
	}
	return installBinary
}

func cgroupMentionsContainer() bool {
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd") || strings.Contains(s, "kubepods")
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func pkgMgrName(kind installKind) string {
	if kind == installRPM {
		return "dnf"
	}
	return "apt-get"
}

// runPackageManager upgrades the maktaba-server package via the native
// package manager so its state stays consistent (a raw binary swap would
// leave dpkg/rpm thinking the old version is installed). The target
// version is informational — the package manager pulls the newest the
// configured repo offers.
func runPackageManager(kind installKind, _ string) ([]byte, error) {
	var cmd *exec.Cmd
	switch kind {
	case installRPM:
		cmd = exec.Command("dnf", "upgrade", "-y", "maktaba-server")
	default:
		cmd = exec.Command("apt-get", "install", "-y", "--only-upgrade", "maktaba-server")
	}
	return cmd.CombinedOutput()
}
