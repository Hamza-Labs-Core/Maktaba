# Plan 35.2 — Side-by-side layout and the launcher — implementation

> Implementation plan for [story-35-02-layout-launcher](story-35-02-layout-launcher.md). Design: [design.md](design.md) §2.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Launcher is its own module, standard library + `golang.org/x/sys` only, < 1 000 lines. | It is replaced rarely (35.7); small means little to get wrong. |
| D2 | Pointer files, not symlinks. | Windows symlinks need privileges; a text file is atomic everywhere with rename. |
| D3 | Exit 75 is the only "restart me" signal. | Works the same under systemd, launchd, SCM and a logon task. |
| D4 | The guard is driven by `pending`, removed by the supervisor at commit. | Survives power loss: no memory needed to decide. |
| D5 | Package managers own the launcher, the updater owns releases. | `apt upgrade` and the updater never fight over one file. |

## 1. Files

**Create**
- `cmd/maktaba-launcher/main.go`, `run.go` (child loop, backoff, signals), `guard.go`, `pointer.go`,
  `service_windows.go` (`svc.Handler`), `service_other.go`, `status.go`, `replace.go`
- `cmd/maktaba-launcher/*_test.go` with a fake child binary
- `cmd/maktaba-server/internal/layout/layout.go` — paths per OS, pointer read/write, prune, `.verified`
- `cmd/maktaba-server/cmd_setup_service.go` — `setup --service`, `setup --self-managed`
- `deploy/packaging/launchd/com.maktaba.server.agent.plist` (node/user variant)
- `deploy/packaging/systemd/user/maktaba-server.service`

**Modify**
- `cmd/maktaba-server/internal/supervisor/supervisor.go` — `Options.ReleaseDir`; `Build` resolves role
  executables from it (pipeline: `ReleaseDir/venv`)
- `cmd/maktaba-server/internal/supervisor/release.go` — `binRoot` from the layout
- `cmd/maktaba-server/cmd_serve.go` — read `MAKTABA_RELEASE_DIR`; refuse to run outside a layout only
  when `MAKTABA_LAUNCHER=1`
- `deploy/packaging/systemd/maktaba-server.service` — `ExecStart=/var/lib/maktaba/maktaba-launcher serve`,
  `Restart=always`, `RestartForceExitStatus=75`, `KillMode=mixed`
- `deploy/packaging/launchd/com.maktaba.server.plist` — Program = launcher, `AbandonProcessGroup`
- `deploy/packaging/nfpm.yaml`, `scripts/postinstall.sh` — launcher in `/usr/bin`, release dir under
  `/var/lib/maktaba`, pointer only when newer than `watermark`
- `deploy/packaging/homebrew/maktaba.rb` — caveat text pointing at `setup --self-managed`
- `.github/workflows/release.yml` — build `maktaba-launcher` into every archive

## 2. Steps

1. `layout` package and pointer atomicity (POSIX and Windows).
2. Launcher child loop and exit-code handling; guard; `--status`, `--verify`.
3. Windows service handler; logon-task mode unchanged in behaviour.
4. Supervisor `ReleaseDir`; serve under the launcher.
5. `setup --service` renderers; `setup --self-managed` conversion; prune.
6. Packages: nfpm layout, postinstall, unit files; Homebrew caveat.

## 3. Tests

Story TCs; a `windows-latest` and `macos-latest` job runs the launcher tests; an nfpm dry-run job checks
the package file list.

## 4. Rollout

Phase A. Existing installs keep running as they are until `setup --self-managed` (or the next package)
runs; the Updates page (35.13) shows which machines are not self-managed.
