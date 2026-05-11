# Implementation Plan — Story 25.28 Windows installer

> Companion to [story-25-28-windows-installer.md](story-25-28-windows-installer.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Primary installer | MSI built with WiX 4. Two artifacts: `Maktaba-<v>-x64.msi`, `Maktaba-<v>-arm64.msi`. |
| Secondary | NSIS EXE mirror for environments that block MSI. |
| Service | `MaktabaServer` Windows Service registered via WiX `ServiceInstall` element; runs as `LocalService`; recovery policy 60s/120s/no-action. |
| Tray | `MaktabaTray.exe` (WinUI 3) launched from Startup folder; talks to service over named pipe `\\.\pipe\maktaba`. |
| Per-user fallback | If UAC denied, NSIS installer offers per-user install to `%LOCALAPPDATA%\Programs\Maktaba`. |
| Code signing | EV cert; `signtool /td sha256 /fd sha256 /tr http://timestamp.digicert.com`. |
| Out of scope | MSIX / Microsoft Store. |

## 1. WiX project

```
desktop/windows/installer/
  Product.wxs                 # main MSI definition
  Components/
    Server.wxs                # api.exe, streaming.exe, etc.
    Service.wxs               # ServiceInstall, ServiceControl
    Firewall.wxs              # Windows Defender rules
    Tray.wxs                  # Startup folder shortcut
  CustomActions/
    Pre.cs                    # detect existing install, stop service
    Post.cs                   # create maktaba data dirs
    Uninstall.cs              # purge if requested
  UI/
    en-US.wxl                 # localized strings
```

`Product.wxs` excerpt:

```xml
<Package Name="Maktaba Media Server" Manufacturer="HamzaLabs"
         Version="$(env.MAKTABA_VERSION)" UpgradeCode="...">
  <MediaTemplate EmbedCab="yes"/>
  <Property Id="ALLUSERS" Value="1"/>   <!-- per-machine default -->
  <Feature Id="Core" Title="Maktaba Server" Level="1">
    <ComponentGroupRef Id="Server"/>
    <ComponentGroupRef Id="Service"/>
    <ComponentGroupRef Id="Firewall"/>
    <ComponentGroupRef Id="Tray"/>
  </Feature>
  <CustomAction Id="StopServicePreInstall" BinaryRef="CustomActionsDll" DllEntry="StopService" Execute="deferred"/>
</Package>
```

Service component:

```xml
<File Id="ApiExe" Source="staging/api.exe" KeyPath="yes"/>
<ServiceInstall Id="MaktabaServerInstall" Name="MaktabaServer"
                DisplayName="Maktaba Media Server"
                Type="ownProcess" Start="auto" ErrorControl="normal"
                Account="NT AUTHORITY\\LocalService"
                Arguments="serve --config &quot;[ProgramData64Folder]Maktaba\config.toml&quot;">
  <ServiceConfig DelayedAutoStart="yes"/>
  <ServiceDependency Id="Tcpip"/>
</ServiceInstall>
<ServiceControl Id="MaktabaServerStart" Name="MaktabaServer" Start="install" Stop="both" Remove="uninstall" Wait="yes"/>
```

Firewall component:

```xml
<fw:FirewallException Id="API" Name="Maktaba API" Port="8080" Protocol="tcp" Profile="private" Scope="localSubnet"/>
<fw:FirewallException Id="Streaming" Name="Maktaba Streaming" Port="8081" Protocol="tcp" Profile="private" Scope="localSubnet"/>
```

## 2. NSIS

`desktop/windows/installer/maktaba.nsi`:

```nsis
!define APP "Maktaba"
!include "MUI2.nsh"
Section "Install"
  IfErrors RequestUAC TryUserScope
  SetOutPath "$PROGRAMFILES64\Maktaba"
  File /r "staging\*"
  CreateShortcut "$SMSTARTUP\Maktaba Tray.lnk" "$PROGRAMFILES64\Maktaba\MaktabaTray.exe"
  ExecWait '"$PROGRAMFILES64\Maktaba\nssm.exe" install MaktabaServer "$PROGRAMFILES64\Maktaba\api.exe" serve --config "$PROGRAMDATA\Maktaba\config.toml"'
SectionEnd
```

NSIS uses NSSM to register the service for fallback; MSI is preferred.

## 3. Tray app

`desktop/windows/MaktabaTray/` is a WinUI 3 project:

```cs
public partial class TrayApp : Application
{
    NamedPipeClientStream pipe;
    public TrayApp() {
        var icon = new TaskbarIcon();
        icon.Icon = LoadIcon();
        icon.ContextFlyout = BuildMenu();
        Connect();
    }
    void Connect() {
        pipe = new NamedPipeClientStream(".", "maktaba", PipeDirection.InOut);
        pipe.Connect(5000);
    }
}
```

Menu items: status, open browser (default browser to localhost:8080), pause indexing (POST to local API), open config folder, show logs, help.

Named-pipe ACL: only `LocalService` and `Authenticated Users` may connect. Service ensures pipe is read-only for clients.

## 4. Signing pipeline

`desktop/windows/scripts/sign.ps1`:

```powershell
$files = @(
  "staging\api.exe",
  "staging\streaming.exe",
  "staging\pipeline-launcher.exe",
  "staging\MaktabaTray.exe",
  "staging\Maktaba-x64.msi",
  "staging\Maktaba-x64-installer.exe"
)
foreach ($f in $files) {
  signtool sign /td sha256 /fd sha256 /tr http://timestamp.digicert.com `
    /sha1 $env:EV_CERT_THUMB $f
}
```

EV cert kept on Yubikey in CI; KSP handling documented in runbook.

## 5. Storage layout

```
%PROGRAMDATA%\Maktaba\          # service-owned data (db, cache, logs)
%PROGRAMFILES%\Maktaba\         # read-only binaries
%LOCALAPPDATA%\Maktaba\         # tray per-user state
```

Service ACL: `LocalService` read-write on `ProgramData\Maktaba\`. Tray accesses via named pipe; never writes shared data.

## 6. Custom actions

```cs
// Pre.cs
[CustomAction]
public static ActionResult StopService(Session session) {
    using var sc = new ServiceController("MaktabaServer");
    if (sc.Status != ServiceControllerStatus.Stopped) {
        sc.Stop(); sc.WaitForStatus(ServiceControllerStatus.Stopped, TimeSpan.FromSeconds(30));
    }
    return ActionResult.Success;
}
```

Long path support via `app.manifest`:

```xml
<longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
```

## 7. Per-user fallback (NSIS only)

If UAC denied:

```nsis
SetShellVarContext current
SetOutPath "$LOCALAPPDATA\Programs\Maktaba"
File /r "staging\*"
; No service; rely on Startup-folder Tray that spawns API on launch.
CreateShortcut "$SMSTARTUP\Maktaba Tray.lnk" "$LOCALAPPDATA\Programs\Maktaba\MaktabaTray.exe"
```

In per-user mode, no firewall rules can be added; we surface a dialog with instructions.

## 8. Test plan

### 8.1 Manual

| Test | Pins |
|---|---|
| Win 11 fresh | MSI install + service start + healthz 200. |
| Win 10 22H2 | Same. |
| Windows on ARM | ARM64 MSI on Surface Pro X. |
| Group Policy MSI deny | NSIS fallback works. |
| Defender quarantine | FFmpeg unaffected (submitted to MS). |
| Uninstall (Programs) | Service removed, firewall removed, tray entry removed. |
| Reboot + autostart | Service up. |
| Per-user install | No service; Startup tray spawns. |

### 8.2 Unit / integration

| Test | Pins |
|---|---|
| `TestPipeACL` | Authenticated Users can connect; SYSTEM-only restricted. |
| `TestPostInstallDataDirsExist` | `%PROGRAMDATA%\Maktaba\` and subdirs created. |
| `TestUninstallIdempotent` | Re-run uninstall on partial state → succeeds. |
| `TestFirewallRulesAreScoped` | Profile=Private only. |
| `TestLongPathSupport` | 280-char path works in Python venv. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Antivirus FP on FFmpeg | Submission to vendors; not packed. | Doc. |
| Locked binaries on update | Stop service before file replace; reboot+RunOnce fallback. | Implementation. |
| Long paths | Enabled in manifest. | `TestLongPathSupport`. |
| PATH pollution | Avoid; use absolute paths. | Spec. |
| Locale | English in v1; WiX MUI v2. | Spec. |
| AppLocker rules | Path rules documented. | Doc. |
| SMB share access | Service account swap documented. | Doc. |
| Sleep/wake | Service handles; watchdog re-registers post-wake. | Implementation. |
| 64-bit-only | x86 unsupported. | Spec. |

## 10. Dependencies

- Local API/Streaming/Pipeline binaries.
- 25.34 (manifest for updater).

## 11. Acceptance checklist

- [ ] x64 + ARM64 MSI artifacts.
- [ ] NSIS fallback for restricted environments.
- [ ] Service registered + auto-start + recovery.
- [ ] Tray with named-pipe IPC.
- [ ] Firewall rules added (private only).
- [ ] EV-signed all binaries + installers.
- [ ] Uninstall removes binaries, service, fw rules; offers data removal.
- [ ] Tests in §8 pass.
