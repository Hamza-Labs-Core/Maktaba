# Implementation Plan — Story 25.34 Auto-update mechanism

> Companion to [story-25-34-auto-update.md](story-25-34-auto-update.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Manifest | `https://releases.maktaba.app/manifest.json`, EdDSA-signed (`manifest.json.sig`), CDN-cached. |
| Channels | `stable`, `beta`, `nightly` (internal only). |
| Per-platform | macOS: Sparkle 2; Windows: small `MaktabaUpdater.exe`; Linux deb/rpm: apt/dnf; Docker: `docker compose pull`; AppImage: AppImageUpdate; NAS: vendor handles. |
| Server-side updater shim | `internal/updater/check.go` shared across platforms; surfaces "Update available" in the local UI. |
| Rollback | Pre-update DB dump + binary copy in `.previous/`; auto-revert on post-update health-check failure. |
| Out of scope | Delta updates. Staged rollouts. |

## 1. Manifest publishing

```
packaging/updater/
  manifest_template.json
  publisher.go              # CI step: assemble manifest from artifacts; sign; upload to R2
  edDSA_keys/
```

`manifest_template.json` (server-side fills in `releases`):

```json
{
  "channels": {
    "stable": { "version": "1.4.2", "min_supported": "1.0.0" },
    "beta":   { "version": "1.5.0-rc.1", "min_supported": "1.0.0" }
  },
  "releases": []
}
```

`publisher.go`:

```go
func PublishRelease(ctx context.Context, r Release) error {
    m := loadCurrentManifest()
    m.Releases = append(m.Releases, r)
    m.Channels[r.Channel].Version = r.Version
    raw, _ := json.MarshalIndent(m, "", "  ")
    sig := ed25519.Sign(privateKey, raw)
    uploadR2(ctx, "manifest.json", raw)
    uploadR2(ctx, "manifest.json.sig", sig)
    return nil
}
```

Public key bundled in every server binary at build time at `keys/maktaba-release.pub`. Rotation requires a release using both keys for one cycle.

## 2. Server-side updater shim

```go
// internal/updater/check.go
type Updater struct {
    pubkey  ed25519.PublicKey
    channel string
    httpc   *http.Client
}

type Manifest struct {
    Channels map[string]ChannelState `json:"channels"`
    Releases []Release `json:"releases"`
}

func (u *Updater) Check(ctx context.Context, currentVersion string) (*Release, error) {
    raw, err := u.fetch(ctx, "manifest.json"); if err != nil { return nil, err }
    sig, err := u.fetch(ctx, "manifest.json.sig"); if err != nil { return nil, err }
    if !ed25519.Verify(u.pubkey, raw, sig) { return nil, ErrBadSignature }
    var m Manifest; json.Unmarshal(raw, &m)
    target, ok := m.Channels[u.channel]
    if !ok { return nil, ErrUnknownChannel }
    if !semverGT(target.Version, currentVersion) { return nil, nil }
    for _, rel := range m.Releases {
        if rel.Version == target.Version { return &rel, nil }
    }
    return nil, ErrReleaseNotFound
}
```

The local API exposes `GET /admin/update` returning the result; UI surfaces the badge.

## 3. Apply

```go
// internal/updater/apply.go
func (u *Updater) Apply(ctx context.Context, r *Release, platform string) error {
    if err := u.diskCheck(2 * r.Artifacts[platform].Size); err != nil { return err }
    if err := u.batteryCheck(); err != nil { return err }
    if r.Breaking { if !u.confirmedBreaking { return ErrUserConfirmationRequired } }
    artURL := r.Artifacts[platform].URL
    art, err := u.downloadVerified(ctx, artURL, r.Artifacts[platform].SHA256)
    if err != nil { return err }
    if err := u.preBackup(ctx); err != nil { return err }
    if err := u.platformApply(ctx, platform, art); err != nil {
        u.rollback(ctx); return err
    }
    if err := u.postHealthCheck(ctx); err != nil {
        u.rollback(ctx); return err
    }
    u.audit("update.applied", r.Version)
    return nil
}
```

`diskCheck` refuses if free < 2× artifact size.

`postHealthCheck` retries `GET /healthz` 3 times with 30s intervals.

## 4. Per-platform apply

### 4.1 macOS (Sparkle)

`SparkleHandler.swift` (25.27) drives via Sparkle. Manifest is converted to appcast XML by a small CI step:

```go
func WriteAppcast(m Manifest) []byte { /* RSS items per release; EdDSA in <enclosure sparkle:edSignature="..."/> */ }
```

### 4.2 Windows (Updater service)

`MaktabaUpdater.exe`:

```cs
[Service]
public class UpdaterService : BackgroundService {
    protected override async Task ExecuteAsync(CancellationToken ct) {
        while (!ct.IsCancellationRequested) {
            await Task.Delay(TimeSpan.FromHours(24), ct);
            await CheckAndApply();
        }
    }
    async Task CheckAndApply() {
        var rel = await u.Check();
        if (rel == null) return;
        await u.Download();
        // Use Windows Restart Manager to stop service then run new MSI
        Process.Start("msiexec", $"/i {downloadedMSI} /quiet /norestart");
    }
}
```

Registered as a scheduled task by the MSI (25.28).

### 4.3 Linux deb/rpm

Apt/dnf handles updates; the local API simply surfaces availability by reading our published manifest. Operator runs `apt upgrade`. Optionally enable `unattended-upgrades` per docs.

### 4.4 Docker

Recommend `docker compose pull && docker compose up -d`. Surfaced in UI as instruction; no auto-apply for Docker by default (Watchtower opt-in documented).

### 4.5 AppImage

`AppRun-update.sh`: invokes `appimageupdatetool --check` and `--update`.

### 4.6 NAS

Surface "Update available" only; user installs via NAS package manager.

## 5. Rollback

`preBackup`:

```go
func (u *Updater) preBackup(ctx context.Context) error {
    dst := u.cfg.DataDir + "/.previous/" + time.Now().UTC().Format("20060102-150405")
    os.MkdirAll(dst, 0700)
    // Copy binaries
    for _, b := range u.cfg.Binaries { copyFile(b, filepath.Join(dst, filepath.Base(b))) }
    // DB dump
    if u.cfg.DBDriver == "postgres" {
        cmd := exec.Command("pg_dump", "--format=custom", u.cfg.DBUrl, "-f", filepath.Join(dst, "db.dump"))
        if err := cmd.Run(); err != nil { return err }
    } else {
        copyFile(u.cfg.SQLitePath, filepath.Join(dst, "maktaba.db"))
    }
    // Symlink "current"
    os.RemoveAll(u.cfg.DataDir + "/.previous-current")
    os.Symlink(dst, u.cfg.DataDir + "/.previous-current")
    return nil
}
```

`rollback`:

```go
func (u *Updater) rollback(ctx context.Context) error {
    p, _ := os.Readlink(u.cfg.DataDir + "/.previous-current")
    for _, b := range u.cfg.Binaries { copyFile(filepath.Join(p, filepath.Base(b)), b) }
    // DB restore: only on schema-failure path (binary restore alone usually suffices).
    return nil
}
```

`maktaba rollback` CLI uses the same code path on demand.

## 6. UI surface

`GET /admin/update` returns `{available: bool, version, breaking, notes_url}`. UI renders the badge + dialog. For breaking releases the dialog requires the user to type the new version into a confirm input.

## 7. Test plan

### 7.1 Unit

| Test | Pins |
|---|---|
| `TestVerifyManifestSig` | Flipped byte → reject. |
| `TestSemverCompareLeading10` | `1.10.0 > 1.9.9` true. |
| `TestBreakingDialogRequiresConfirmation` | Without confirm flag → ErrUserConfirmationRequired. |
| `TestDiskCheckRefusesIfLow` | freeBytes < 2*size → error. |
| `TestBatteryCheck` | On battery + low charge → defer. |

### 7.2 Integration

| Test | Pins |
|---|---|
| `TestSparkleApplyOnQuit` | macOS subprocess updates on quit. |
| `TestWindowsServiceRestartUnder5s` | Service down < 5s during update. |
| `TestLinuxAptUpgradeNoDowntime` | dpkg upgrade preserves service. |
| `TestRollbackOnHealthFail` | Inject failing healthz → reverts. |
| `TestStaleManifestCacheRefresh` | After 5min cache, refresh. |
| `TestClockSkew60s` | Manifest "issued_at" within 60s tolerance accepted. |

## 8. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Update during scan | Pipeline drains; durable queue preserves state. | Spec. |
| DB migration failure | Revert binaries; DB stays on last good schema. | Spec. |
| Version skew between services | All updated atomically via the apply step. | Spec. |
| Disk-space pre-check | Refuse. | `TestDiskCheckRefusesIfLow`. |
| Cosmic ray bit-flip | SHA-256 catches. | Spec. |
| Battery / laptop | Defer when low. | `TestBatteryCheck`. |
| Cluster of servers | Per-server update. | Spec. |
| Channel switch beta→stable | No auto-downgrade; wait. | Spec. |
| GitHub Releases mirror | Same artifacts; canonical is `releases.maktaba.app`. | Doc. |
| Manifest signature key rotation | New key signs alongside old for one release; servers accept either. | Doc. |
| Cache staleness | CDN max-age=300; clients additionally honor cache headers. | Doc. |

## 9. Dependencies

- 25.27, 25.28, 25.29, 25.30 (per-platform artifacts).
- 25.16 (UI surface "update available").

## 10. Acceptance checklist

- [ ] Manifest published with EdDSA signature.
- [ ] Per-platform apply paths wired.
- [ ] Pre-update backup; rollback on health-check fail.
- [ ] Server-side `Updater.Check` + `Apply`.
- [ ] CLI `maktaba rollback`.
- [ ] Channels: stable, beta, nightly.
- [ ] Tests in §7 pass.
