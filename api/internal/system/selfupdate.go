// Server self-update handler (Epic 28, Story 28.3).
//
// POST /api/admin/system/update downloads the correct release archive for
// this host from GitHub, verifies its SHA-256 against the release's
// checksums.txt, atomically swaps the running maktaba-server binary
// (keeping a .bak), and re-execs. Package-managed (.deb/.rpm) installs
// shell out to the package manager instead; Docker installs can't replace
// their own image and get a 409 with instructions.
//
// The swap strategy mirrors the proven CLI updater in
// cmd/maktaba-server/internal/selfupdate: write the new binary next to
// the target, rename the live one aside, rename the new one into place —
// each rename is atomic within a directory, and the .bak makes a
// mid-swap crash recoverable. Download/verify/extract are pure helpers so
// the only filesystem-mutating step is isolated and testable.
package system

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// installKind is how this maktaba-server was installed, which decides the
// update path.
type installKind int

const (
	installBinary installKind = iota // plain archive / Homebrew / manual
	installDocker                    // container — can't self-replace
	installDeb                       // apt-managed
	installRPM                       // dnf/yum-managed
)

// SelfUpdater wires the self-update handler to the update-check service
// (it reuses the Updater to resolve the target release and assets) and
// guards against concurrent updates with a process-wide mutex.
type SelfUpdater struct {
	u *Updater

	mu       sync.Mutex
	inFlight bool

	// injection points for tests
	httpc        *http.Client
	execSelf     func(path string) error // re-exec; default syscall.Exec
	detect       func(self string) installKind
	runPkgMgr    func(kind installKind, version string) ([]byte, error)
	selfPathFunc func() (string, error)
}

// NewSelfUpdater builds a SelfUpdater backed by u.
func NewSelfUpdater(u *Updater) *SelfUpdater {
	return &SelfUpdater{
		u:            u,
		httpc:        &http.Client{Timeout: 10 * time.Minute},
		execSelf:     reexecSelf,
		detect:       detectInstall,
		runPkgMgr:    runPackageManager,
		selfPathFunc: resolveSelf,
	}
}

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

	switch s.detect(self) {
	case installDocker:
		// A container can't replace its own image; tell the operator how.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(dockerResponse{
			Install:      "docker",
			Instructions: "docker compose pull && docker compose up -d",
			Image:        "ghcr.io/hamza-labs-core/maktaba-server:" + strings.TrimPrefix(status.LatestVersion, "v"),
		})
		return
	case installDeb:
		s.packageManagerUpdate(w, r, installDeb, status.LatestVersion)
		return
	case installRPM:
		s.packageManagerUpdate(w, r, installRPM, status.LatestVersion)
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
	// Best-effort re-exec; if it fails the .bak is still in place and the
	// operator/supervisor can recover.
	go func() {
		time.Sleep(500 * time.Millisecond) // let the response drain
		_ = s.execSelf(self)
	}()
}

// applyBinary performs the download/verify/swap. Errors are sentinel-wrapped
// so the handler can map them to HTTP statuses.
func (s *SelfUpdater) applyBinary(ctx context.Context, self string, status UpdateStatus) error {
	dir := filepath.Dir(self)
	if err := writableDir(dir); err != nil {
		return fmt.Errorf("%w: %s", errNotWritable, dir)
	}
	asset := matchServerAsset(status.Assets)
	if asset == nil {
		return fmt.Errorf("%w: no maktaba-server asset for %s", errNoAsset, PlatformToken())
	}
	if err := diskCheck(dir, 2*asset.Size); err != nil {
		return err
	}
	checksumsURL := findChecksumsURL(status.Assets)
	if checksumsURL == "" {
		return fmt.Errorf("%w: release has no checksums.txt", errVerify)
	}

	archive, err := s.download(ctx, asset.URL)
	if err != nil {
		return fmt.Errorf("%w: %v", errDownload, err)
	}
	sums, err := s.download(ctx, checksumsURL)
	if err != nil {
		return fmt.Errorf("%w: fetch checksums: %v", errDownload, err)
	}
	want := lookupChecksum(string(sums), asset.Name)
	if want == "" {
		return fmt.Errorf("%w: %s not in checksums.txt", errVerify, asset.Name)
	}
	got := sha256hex(archive)
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: got %s want %s", errChecksumMismatch, got, want)
	}

	bin, err := extractServerBinary(asset.Name, archive)
	if err != nil {
		return fmt.Errorf("%w: %v", errVerify, err)
	}
	if err := swapBinary(self, bin); err != nil {
		return fmt.Errorf("%w: %v", errSwap, err)
	}
	return nil
}

func (s *SelfUpdater) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// Cap at 512 MiB so a hostile/buggy feed can't OOM us.
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
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
	errNotWritable      = errors.New("binary directory not writable")
	errNoAsset          = errors.New("no platform asset")
	errDownload         = errors.New("download failed")
	errVerify           = errors.New("verification failed")
	errChecksumMismatch = errors.New("checksum mismatch")
	errSwap             = errors.New("binary swap failed")
	errDiskSpace        = errors.New("insufficient disk space")
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
	case errors.Is(err, errChecksumMismatch), errors.Is(err, errDownload), errors.Is(err, errVerify):
		httperror.Write(w, r, httperror.BadGateway(err.Error()))
	default:
		httperror.Write(w, r, httperror.Internal(err.Error()))
	}
}

// --- pure helpers (unit-tested) ---

// matchServerAsset picks the maktaba-server archive for this host. Release
// archives are named maktaba-server-<ver>-<os>-<arch>.{tar.gz,zip}; we
// match on the "<os>-<arch>" token plus the platform-appropriate
// extension.
func matchServerAsset(assets []Asset) *Asset {
	token := PlatformToken()
	wantExt := ".tar.gz"
	if runtime.GOOS == "windows" {
		wantExt = ".zip"
	}
	for i := range assets {
		n := assets[i].Name
		if strings.HasPrefix(n, "maktaba-server-") &&
			strings.Contains(n, token) &&
			strings.HasSuffix(n, wantExt) {
			return &assets[i]
		}
	}
	return nil
}

func findChecksumsURL(assets []Asset) string {
	for _, a := range assets {
		if a.Name == "checksums.txt" {
			return a.URL
		}
	}
	return ""
}

// lookupChecksum finds the hex digest for name in a `sha256sum`-format
// manifest. Each line is "<hex>  <path>"; the path may carry a leading
// "./" so we match on the basename.
func lookupChecksum(manifest, name string) string {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if filepath.Base(fields[len(fields)-1]) == name {
			return fields[0]
		}
	}
	return ""
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// extractServerBinary returns the maktaba-server executable bytes from a
// .tar.gz or .zip archive.
func extractServerBinary(name string, archive []byte) ([]byte, error) {
	switch {
	case strings.HasSuffix(name, ".zip"):
		return extractFromZip(archive)
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return extractFromTarGz(archive)
	default:
		return nil, fmt.Errorf("unsupported archive: %s", name)
	}
}

func extractFromTarGz(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if isServerBinaryName(filepath.Base(hdr.Name)) && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 512<<20))
		}
	}
	return nil, errors.New("maktaba-server not found in archive")
}

func extractFromZip(archive []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if isServerBinaryName(filepath.Base(f.Name)) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = rc.Close() }()
			return io.ReadAll(io.LimitReader(rc, 512<<20))
		}
	}
	return nil, errors.New("maktaba-server not found in archive")
}

func isServerBinaryName(base string) bool {
	return base == "maktaba-server" || base == "maktaba-server.exe"
}

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

// rollbackBinary restores target from target+".bak" (used by the
// supervisor's post-restart health check on the unified server, and
// available to tests).
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
