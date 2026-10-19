# Plan 35.7 — Supervisor and launcher self-update — implementation

> Implementation plan for [story-35-07-supervisor-self-update](story-35-07-supervisor-self-update.md). Design: [design.md](design.md) §8.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Handover by file (`updates/handover.json`) plus adoption by probe and polling. | The new process is not the parent and cannot `wait`; a file survives the exec boundary and a power loss is detectable (boot id). |
| D2 | Pid identity = pid + start time (`/proc/<pid>/stat`, `proc_pidinfo`, `GetProcessTimes`). | Prevents adopting a reused pid. |
| D3 | Windows: named job, handle duplicated into the launcher. | `KILL_ON_JOB_CLOSE` would kill slots with the supervisor; the launcher outlives it. |
| D4 | Degrade to ordered restart when the Windows handle duplication fails. | Same as story 33.14 EC2; never silently break TV outside the viewer rule. |
| D5 | Launcher self-replacement is a full restart, gated like a front-door restart. | Rare; keeps the launcher tiny with no adoption logic of its own. |

## 1. Files

**Create**
- `cmd/maktaba-server/internal/supervisor/handover.go` — write/read `handover.json`, boot id.
- `cmd/maktaba-server/internal/supervisor/adopt.go` — adopted-child monitor (probe + liveness poll).
- `cmd/maktaba-server/internal/supervisor/adopt_unix.go`, `adopt_windows.go` — pid start time, group
  signalling, `OpenJobObject` + `QueryInformationJobObject`.
- `cmd/maktaba-launcher/replace.go` — `--replace-self`.
- Tests: `handover_test.go`, `adopt_test.go`, `adopt_windows_test.go`, `cmd/maktaba-launcher/replace_test.go`.

**Modify**
- `cmd/maktaba-server/internal/supervisor/proctree_windows.go` — create survivors in the named job
  `Local\Maktaba-tv-<boot id>`; `DuplicateHandle` into `MAKTABA_LAUNCHER_PID`.
- `cmd/maktaba-server/internal/supervisor/proctree_unix.go` — no group kill on exit 75.
- `cmd/maktaba-server/internal/supervisor/run.go` — `ExitForUpdate()` path; adopt on start.
- `cmd/maktaba-server/cmd_serve.go` — exit code 75 after handover; read handover before `Build`.
- `cmd/maktaba-server/internal/updater/step_supervisor.go` — step 12, launcher replacement gate.
- `deploy/packaging/systemd/maktaba-server.service` — `ExecStart` launcher, `KillMode=mixed`,
  `RestartForceExitStatus=75`, `Restart=always`.
- `deploy/packaging/launchd/com.maktaba.server.plist` — launcher, `AbandonProcessGroup`.

## 2. Steps

1. Pid identity helpers per OS and their tests.
2. Handover writer and the exit-75 path in `cmd_serve.go`.
3. Adoption monitor; replace-on-death; service-stop signalling from the launcher (TC1, TC2, TC5).
4. Windows named job + handle duplication; spike first on a Windows runner (TC4), with the degrade
   path wired before the happy path.
5. Launcher guard integration (TC3, built in 35.2) with the journal's `rolling_back`.
6. `--replace-self` and the gate in the orchestrator (TC6).
7. Cross-OS integration with a live HLS client (TC7).

## 3. Tests

Story TCs; a chaos test killing the new supervisor between adoption of the front door and the slots
(the remaining slot entries must still be adopted or restarted on the next start).

## 4. Rollout

Phase B. Until it lands, step 12 restarts every child in order in the window under the viewer rule
(the degrade path), which is what a Windows home keeps doing if the job handover spike fails.
