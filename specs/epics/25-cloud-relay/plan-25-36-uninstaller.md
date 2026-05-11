# Implementation Plan — Story 25.36 Cross-platform uninstaller

> Companion to [story-25-36-uninstaller.md](story-25-36-uninstaller.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Universal CLI | `maktaba uninstall` — cross-platform fallback; per-platform installers wrap their native uninstall around it. |
| Always-removed | Binaries, auto-start, firewall, tray, service. |
| Prompted | DB, cache, logs, config. |
| Never touched | Library files. |
| Notify cloud | Best-effort `DELETE /api/servers/{id}` with 10s timeout. |
| Out of scope | Telemetry on uninstall; auto-link to cloud account delete (we link, never auto-trigger). |

## 1. CLI

```
maktaba uninstall [--data=keep|remove|prompt] [--dry-run] [--cloud-unlink=on|off]
```

`cli/maktaba/uninstall.go`:

```go
type UninstallPlan struct {
    Remove        []string
    Prompt        []string
    Skip          []string
    CloudUnlink   bool
    DryRun        bool
}

func BuildPlan(env Env) UninstallPlan {
    base := UninstallPlan{ CloudUnlink: true }
    switch env.OS {
    case "darwin":
        base.Remove = append(base.Remove,
            "/Applications/Maktaba.app",
            home(env, "Library/LaunchAgents/app.maktaba.server.plist"),
            home(env, "Library/Logs/Maktaba"))
        base.Prompt = append(base.Prompt,
            home(env, "Library/Application Support/Maktaba"),
            home(env, "Library/Caches/io.maktaba.*"))
    case "linux":
        base.Remove = append(base.Remove,
            "/usr/bin/maktaba",
            "/lib/systemd/system/maktaba.service")
        base.Prompt = append(base.Prompt, "/var/lib/maktaba", "/var/log/maktaba", "/etc/maktaba")
    case "windows":
        base.Remove = append(base.Remove,
            `%PROGRAMFILES%\Maktaba`,
            `%LOCALAPPDATA%\Maktaba`,
            "MaktabaServer service entry",
            "Maktaba firewall rules",
            `%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\MaktabaTray.lnk`)
        base.Prompt = append(base.Prompt, `%PROGRAMDATA%\Maktaba`)
    }
    return base
}

func Apply(ctx context.Context, plan UninstallPlan, prompt PromptFn) error {
    if plan.CloudUnlink { _ = notifyCloudUnlink(ctx, 10*time.Second) }
    // 1. Stop services first
    stopServices(plan)
    // 2. Always-remove
    for _, p := range plan.Remove { removeIdempotent(p) }
    // 3. Prompt
    for _, p := range plan.Prompt {
        action := prompt(p)   // "remove" | "keep"
        if action == "remove" { removeIdempotent(p) }
    }
    return nil
}
```

`removeIdempotent` is best-effort: a not-found path is a no-op; permission errors are logged but don't abort.

## 2. Cloud unlink

```go
func notifyCloudUnlink(ctx context.Context, timeout time.Duration) error {
    creds, err := loadCloudCreds(); if err != nil { return err }
    ctx, cancel := context.WithTimeout(ctx, timeout); defer cancel()
    req, _ := http.NewRequestWithContext(ctx, "DELETE",
        creds.CloudAPI+"/api/servers/"+creds.ServerID, nil)
    req.Header.Set("X-Server-Token", creds.Bearer)
    _, _ = http.DefaultClient.Do(req)   // best-effort; ignore errors
    return nil
}
```

If offline, cloud-side reaper (25.16) eventually marks the server stale; user can also unlink from the cloud dashboard.

## 3. Per-platform integration

### 3.1 macOS

`desktop/macos/uninstall.sh` runs as a post-Trash hook (triggered by Sparkle's relaunch helper or invoked by Homebrew cask zap).

```bash
#!/bin/sh
# Stop subprocesses
/Applications/Maktaba.app/Contents/MacOS/maktaba-cloudlink --signal-quit || true
launchctl unload ~/Library/LaunchAgents/app.maktaba.server.plist 2>/dev/null || true
rm -f ~/Library/LaunchAgents/app.maktaba.server.plist
# Hand off to CLI
/Applications/Maktaba.app/Contents/Resources/ServerBinaries/maktaba uninstall --data=prompt
rm -rf /Applications/Maktaba.app
```

Homebrew cask `zap` lists the data paths so `brew uninstall --cask --zap` also clears them.

### 3.2 Windows

`UninstallActions.cs` (WiX 4) custom actions run on uninstall:

```cs
[CustomAction]
public static ActionResult UninstallSteps(Session s) {
    StopService("MaktabaServer");
    RemoveFirewallRule("Maktaba API");
    RemoveFirewallRule("Maktaba Streaming");
    DeleteService("MaktabaServer");
    File.Delete(StartupShortcutPath());
    if (s["REMOVE_DATA"] == "1") DirectoryRemove(@"%PROGRAMDATA%\Maktaba");
    return ActionResult.Success;
}
```

MSI exposes the "Also remove data" checkbox via `REMOVE_DATA` property in the UI sequence.

### 3.3 Linux (deb/rpm)

`debian/postrm`:

```bash
#!/bin/sh
set -e
case "$1" in
  remove)
    systemctl disable --now maktaba.service || true
    rm -f /lib/systemd/system/maktaba.service
    ;;
  purge)
    deluser --quiet --system maktaba 2>/dev/null || true
    rm -rf /var/lib/maktaba /var/log/maktaba /etc/maktaba
    ;;
esac
```

`rpm.spec` `%preun`/`%postun` mirror the same.

### 3.4 Docker

Documentation only:

```
docker compose down      # stops; data volumes preserved
docker compose down -v   # also removes named volumes (db, data, cache); media bind mount untouched
```

### 3.5 NAS

Vendor wizard "Also delete data?" toggle — implemented in `packaging/synology/scripts/postuninst` (etc.) calling the same CLI.

### 3.6 AppImage

`packaging/appimage/uninstall.sh` (documented):

```bash
rm -f ~/.local/bin/Maktaba-*.AppImage
rm -f ~/.config/systemd/user/maktaba.service
systemctl --user disable --now maktaba.service || true
echo "If you want to wipe data: rm -rf ~/.local/share/Maktaba"
```

## 4. In-app uninstall preview

Settings → Advanced → Uninstall page:

```tsx
function UninstallPreview() {
    const plan = useQuery(["uninstall-plan"], fetchUninstallPlan);
    return (
      <div>
        <h2>What will be removed</h2>
        <Section title="Always removed"><ul>{plan.Remove.map(p=><li>{p}</li>)}</ul></Section>
        <Section title="Prompted (your choice)"><ul>{plan.Prompt.map(p=><li>{p}</li>)}</ul></Section>
        <Button onClick={launchPlatformUninstaller}>Open uninstaller…</Button>
      </div>
    );
}
```

`launchPlatformUninstaller` opens the platform's native control panel:

- macOS: opens "Move Maktaba.app to Trash" dialog.
- Windows: launches `appwiz.cpl` with Maktaba selected.
- Linux: copies `sudo apt remove maktaba` to clipboard with toast.

## 5. Test plan

### 5.1 Manual

| Test | Pins |
|---|---|
| macOS uninstall (keep data) | drag → data preserved. |
| macOS uninstall (remove all) | data dirs gone. |
| Windows MSI uninstall | service + firewall clean. |
| Debian remove vs purge | clear difference. |
| Docker `down -v` | volumes deleted; media bind untouched. |
| NAS Synology uninstall | clean. |
| Uninstall during scan | scan stops cleanly. |
| AppImage left-overs | per-user systemd unit removed via doc script. |

### 5.2 Unit

| Test | Pins |
|---|---|
| `TestBuildPlanByOS` | Returns expected lists per OS. |
| `TestRemoveIdempotentMissing` | Missing path → no error. |
| `TestPermissionErrorContinues` | Locked file logs warning; continues. |
| `TestDryRunListsOnly` | --dry-run prints; removes nothing. |
| `TestCloudUnlinkTimeout` | Cloud unreachable → returns within 10s. |

## 6. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Library files inside install dir | Never follow symlinks. | Implementation. |
| Open file handles (Windows) | Restart Manager closes. | Cross-25.28. |
| Ongoing transcribes | Pipeline killed; resume info preserved. | Cross-Epic 03. |
| Cloud unlink offline | Best-effort; cloud reaper covers. | Spec. |
| Kernel modules | None. | Spec. |
| Docker without compose | Doc: stop + rm + volume cleanup. | Doc. |
| NAS shared folders | Never touch. | Spec. |
| Permission errors | Logged; continue. | `TestPermissionErrorContinues`. |
| Reinstall over leftover state | Wizard (25.35) detects + offers use/wipe. | Cross-25.35. |
| Dry-run | Lists; takes no action. | `TestDryRunListsOnly`. |
| Account-delete link | Secondary link to cloud `DELETE /api/me`; never auto. | UX. |

## 7. Dependencies

- 25.27, 25.28, 25.29, 25.30, 25.31 (each invokes the CLI from its native uninstaller).
- 25.16 (cloud unlink).
- 25.35 (re-install detection).

## 8. Acceptance checklist

- [ ] `maktaba uninstall` CLI cross-platform.
- [ ] Per-platform integration hooks call CLI.
- [ ] Always-remove vs prompt-remove categories enforced.
- [ ] Library files never touched.
- [ ] Cloud unlink best-effort with 10s timeout.
- [ ] In-app uninstall preview implemented.
- [ ] Tests in §5 pass.
