# Implementation Plan — Story 25.35 First-run setup wizard

> Companion to [story-25-35-first-run-setup-wizard.md](story-25-35-first-run-setup-wizard.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Front-end | React multi-step pages under `web/src/pages/Wizard/`. Resumable state in `localStorage` keyed by server id. |
| Back-end | `internal/setup/` (in local API repo): hardware probe, profile applier, wizard state machine, REST endpoints `/api/setup/*`. |
| Trigger | Local API boots in "needs-setup" mode if `setup_completed=false` in `settings`; redirect web UI to `/setup`. |
| Resume | Each step persists to `cloud_setup_state` (local SQLite/PG) so closing+reopening reopens at last step. |
| Bilingual | EN + AR via i18n. RTL flips layout. |
| Out of scope | Telemetry of step completions (Epic 16.5 opt-in). |

## 1. Endpoints

```
GET    /api/setup/state                          # current step + saved values
POST   /api/setup/step                           # advance with body {step, payload}
POST   /api/setup/finish                         # mark complete
POST   /api/setup/probe                          # run hardware probe; returns recommended profile
GET    /api/setup/folder-check?path=...          # validates a library folder
POST   /api/setup/cloud-link/start               # initiates 25.6 claim
```

## 2. Wizard state machine

```go
// internal/setup/wizard.go
type Step string
const (
    StepWelcome      Step = "welcome"
    StepHardware     Step = "hardware"
    StepLibraries    Step = "libraries"
    StepTranscription Step = "transcription"
    StepStorage      Step = "storage"
    StepCloud        Step = "cloud"
    StepDone         Step = "done"
)

type State struct {
    Step          Step
    Locale        string
    Profile       string
    Libraries     []LibraryRoot
    Transcribe    TranscribeChoice
    Storage       StorageChoice
    CloudLinked   bool
    UpdatedAt     time.Time
}

func (w *Wizard) Advance(ctx context.Context, payload AdvancePayload) (State, error) {
    cur, _ := w.repo.Get(ctx)
    if err := validate(cur.Step, payload); err != nil { return cur, err }
    next := nextStep(cur.Step)
    new := applyPayload(cur, payload)
    new.Step = next
    new.UpdatedAt = time.Now()
    if next == StepDone { /* apply profile + write settings.setup_completed=true */ }
    return new, w.repo.Save(ctx, new)
}
```

## 3. Hardware probe

`internal/setup/probe.go` (cross-platform):

```go
type Probe struct {
    OS, Arch         string
    Model            string
    CPUCores         int
    RAMGB            int
    GPU              string
    DiskFreeGB       int
    RootIsSD         bool
}

func Detect() Probe {
    p := Probe{ OS: runtime.GOOS, Arch: runtime.GOARCH, CPUCores: runtime.NumCPU() }
    v, _ := mem.VirtualMemory(); p.RAMGB = int(v.Total / 1<<30)
    p.GPU = detectGPU()
    p.Model = readModel()
    p.DiskFreeGB = freeBytes(rootDir())
    p.RootIsSD = detectSD(rootDir())
    return p
}

func RecommendProfile(p Probe) string {
    switch {
    case p.OS == "darwin" && strings.Contains(p.GPU, "apple"): return "mac-mini"
    case strings.Contains(p.Model, "Raspberry Pi 5"):          return "pi5-default"
    case strings.Contains(p.Model, "Raspberry Pi"):            return "pi-default"
    case strings.Contains(p.GPU, "nvidia") && p.RAMGB >= 16:   return "pc-desktop"
    case p.RAMGB >= 32:                                         return "vps-large"
    case p.RAMGB >= 8:                                          return "nas-bay"
    default:                                                    return "vps-small"
    }
}
```

## 4. Folder check

```go
// GET /api/setup/folder-check?path=/Users/x/Movies
func folderCheck(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        path := r.URL.Query().Get("path")
        fi, err := os.Stat(path)
        if err != nil { writeJSON(w, 200, map[string]any{"ok": false, "reason": "not_found"}); return }
        if !fi.IsDir()  { writeJSON(w, 200, map[string]any{"ok": false, "reason": "not_dir"}); return }
        if !readable(path) { writeJSON(w, 200, map[string]any{"ok": false, "reason": "unreadable"}); return }
        size, count := sampleSize(path)
        // Snap-permission hint
        snapHint := isSnap() && !insideSnapPaths(path)
        writeJSON(w, 200, map[string]any{
            "ok": true, "approx_bytes": size, "approx_files": count,
            "snap_hint": snapHint,
        })
    }
}
```

## 5. UI

`web/src/pages/Wizard/Welcome.tsx`, `Hardware.tsx`, `Libraries.tsx`, `Transcription.tsx`, `Storage.tsx`, `Cloud.tsx`, `Done.tsx`.

State stored both server-side (source of truth) and `localStorage` (offline-resume safety). On mount, `GET /api/setup/state` rehydrates.

## 6. Model warning

For Whisper `large` on a 4 GB machine, the Transcription step shows:

> "This model needs > 8 GB RAM; consider `base` instead."

With "I understand" checkbox before allowing Next.

## 7. CLI mode

```go
// maktaba wizard --json --apply config.json
func wizardCLI(args []string) {
    var cfg State
    raw, _ := os.ReadFile(args[1])
    json.Unmarshal(raw, &cfg)
    // Validate; if ok, apply directly bypassing UI.
    _ = svc.Wizard.ApplyFromCLI(context.Background(), cfg)
}
```

## 8. Test plan

### 8.1 Snapshot

| Test | Pins |
|---|---|
| `Welcome_AR.snap` | RTL layout valid. |
| `HardwareAppleM2.snap` | profile=mac-mini, engine=mlx, model=small.en. |
| `LibrariesEmpty.snap` | continue disabled. |

### 8.2 Unit / integration

| Test | Pins |
|---|---|
| `TestDisplayNameNotInvolved` | (separator) |
| `TestRecommendProfileMatrix` | various probes → expected profile. |
| `TestFolderCheckNotFound` | nonexistent → reason=not_found. |
| `TestFolderCheckSnapHint` | running under Snap with path outside `home` → hint=true. |
| `TestModelWarningOn4GBLarge` | 4 GB + large → requires confirm. |
| `TestSkipCloudFinishes` | finish without cloud-link → dashboard loads. |
| `TestResumeAtStep4` | close at step 4 → reopen → state restored. |
| `TestKeyboardOnlyFlow` | all steps tab-reachable. |
| `TestBilingualMidFlow` | toggle ar → remaining steps in ar. |
| `TestInvalidPathInline` | submit nonexistent → inline error, Next disabled. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Empty library | Step 7 explains "drop videos in [path]". | Spec. |
| Read-only library mount | Step 3 refuses; surfaces error. | Spec. |
| No internet at cloud step | "couldn't reach Maktaba Cloud — connect later". | Spec. |
| Re-run wizard | Settings entry; no data loss. | Spec. |
| Multi-user host Mac | Per-user wizard. | Spec. |
| Mobile form factor | Discouraged but supported. | Doc. |
| Power-user CLI | `maktaba wizard --apply config.json`. | Implementation. |
| Telemetry | Opt-in via Epic 16.5. | Spec. |
| Permissions on shared folders | mac: bookmarks; Linux: portals. | Spec. |
| Probe inaccuracy on VMs | User override. | Spec. |

## 10. Dependencies

- Local API config + library_mgmt (Epic 09).
- 25.6 (cloud claim).
- 25.27/25.28/25.29/25.30 (Probe runs in each environment).
- Epic 17 (theme + i18n).

## 11. Acceptance checklist

- [ ] `internal/setup/{probe,wizard,profile}.go` implemented.
- [ ] REST endpoints + state persisted.
- [ ] Resumable mid-flow.
- [ ] Bilingual EN/AR + RTL.
- [ ] Model warning on under-spec hardware.
- [ ] Cloud-link optional.
- [ ] `maktaba wizard --apply` for CLI.
- [ ] Tests in §8 pass.
