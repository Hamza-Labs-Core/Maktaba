# Implementation Plan — Story 28.3 Server self-update

> Companion to [story-28-03-server-self-update.md](story-28-03-server-self-update.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `api/internal/system/selfupdate.go`. |
| Route | `POST /api/admin/system/update`, admin-gated in-handler. |
| Asset match | `maktaba-server-<ver>-<goos>-<goarch>.(tar.gz|zip)`. |
| Integrity | SHA-256 vs the release's `checksums.txt`. |
| Swap | temp → rename live to `.bak` → rename new into place; re-exec. |
| Install type | docker → 409; deb/rpm → package manager; else binary swap. |
| Concurrency | process-wide mutex; second request 409. |

## 1. Request / response

```go
type updateRequest struct {
    Version string `json:"version"` // empty = latest on channel
    Confirm bool   `json:"confirm"`
}
```

Handler order: admin check → `Confirm` check → mutex `TryLock` (else 409
"in progress") → resolve target via the 28.2 `Updater` → install-type
branch.

## 2. Install-type detection

```go
type installKind int
const ( installBinary installKind = iota; installDocker; installDeb; installRPM )

func detectInstall(selfPath string) installKind {
    if fileExists("/.dockerenv") || cgroupMentionsDocker() { return installDocker }
    if strings.HasPrefix(selfPath, "/usr/") {
        if fileExists("/var/lib/dpkg/status") { return installDeb }
        if dirExists("/var/lib/rpm") { return installRPM }
    }
    return installBinary
}
```

- **docker** → `409 Conflict` + body
  `{"install":"docker","instructions":"docker compose pull && docker compose up -d","image":"ghcr.io/hamza-labs-core/maktaba-server:<ver>"}`.
- **deb** → `exec.Command("apt-get","install","--only-upgrade","-y","maktaba-server")`; **rpm** → `dnf upgrade -y maktaba-server`. Return combined output; non-zero ⇒ 500 with stderr.
- **binary** → §3.

## 3. Binary path: download → verify → swap → re-exec

```go
func (s *selfUpdater) applyBinary(ctx context.Context, rel target) error {
    self, _ := os.Executable(); self, _ = filepath.EvalSymlinks(self)
    if err := writableDir(filepath.Dir(self)); err != nil { return errPermission }
    asset := matchAsset(rel.Assets) // GOOS/GOARCH
    if asset == nil { return errNoAsset }
    if err := diskCheck(filepath.Dir(self), 2*asset.Size); err != nil { return err }

    archive, err := download(ctx, asset.URL); if err != nil { return err }
    sums, err := download(ctx, rel.ChecksumsURL); if err != nil { return err }
    want := lookupSum(sums, asset.Name)
    if got := sha256hex(archive); !strings.EqualFold(got, want) {
        return fmt.Errorf("checksum mismatch: got %s want %s", got, want)
    }
    bin, err := extractServerBinary(archive, asset.Name) // tar.gz or zip
    if err != nil { return err }
    if err := swap(self, bin); err != nil { return err } // .bak kept
    // record a sentinel so the relaunched process can health-check + rollback
    writeUpdateSentinel(self+".bak", rel.Version)
    return reexec(self) // syscall.Exec on unix; supervisor relaunch on windows
}
```

`swap` mirrors the proven CLI updater
(`cmd/maktaba-server/internal/selfupdate`): write temp in the same dir,
chmod 0755, `rename(self, self+".bak")`, `rename(tmp, self)`, rollback
the rename on failure.

## 4. Rollback (post-restart)

On boot, if an update sentinel exists, the supervisor runs a health probe
(`GET /healthz`, 3×, 10s). On failure: restore `self.bak` → `self`,
relaunch, audit `update.rolled_back`, delete the sentinel. On success:
delete `.bak` + sentinel, audit `update.applied`. (The probe/rollback
hook lives in `cmd/maktaba-server/internal/supervisor`; the api handler
only writes the sentinel + triggers the swap.)

For the api-only deployment (no supervisor), the handler does the probe
inline before returning success and rolls back synchronously.

## 5. Guards / errors → HTTP

| Condition | Status |
|---|---|
| not admin | 403 |
| `confirm:false` | 400 |
| already current | 409 |
| update in progress | 409 |
| docker install | 409 (+instructions) |
| no asset for platform | 404 |
| dir not writable | 403 (+ "use package manager / run as owner") |
| disk too low | 507 |
| checksum mismatch / download fail | 502 |
| pkg-manager non-zero | 500 |

All via `httperror` so the response is problem+json like the rest.

## 6. Test plan

| Test | Pins |
|---|---|
| `TestMatchAssetByPlatform` | T01 |
| `TestVerifyChecksumOK` / `Mismatch` | T02/T03 |
| `TestSwapKeepsBak` / `TestRollbackRestoresBak` (temp dir) | T04/T05 |
| `TestDetectInstallDocker/Deb` (faked markers) | T06/T07 |
| `TestDiskCheckRefusesLow` | edge |
| `TestHandlerNonAdmin403` / `MissingConfirm400` / `AlreadyCurrent409` / `Docker409` | T08–T11 |

## 7. Acceptance checklist

- [ ] Admin + confirm gating; in-progress mutex.
- [ ] Platform asset match; checksum verify before any write.
- [ ] Atomic swap with `.bak`; re-exec.
- [ ] Rollback on failed health check.
- [ ] Docker 409 + instructions; deb/rpm via package manager.
- [ ] Disk/permission preflight; problem+json errors.
- [ ] Tests green.
