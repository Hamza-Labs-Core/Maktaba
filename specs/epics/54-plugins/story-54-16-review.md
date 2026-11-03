# Story 54.16 — Automated checks and human review

> Epic 54 · Phase 2 · Depends on: 54.03, 54.12, 54.15 · Needed by: —

## Why

The sandbox makes a malicious plugin harmless to the network and the disk, but it can still
write wrong data, waste the server, mislead its users, or squat a name. Every version gets the
same automated checks the home runs and more; people review what machines cannot judge: first
submissions, new permissions, and anything flagged. Review must be fast enough that developers
stay (N11, N12).

## Description

### 1. The pipeline of checks

On upload, a check job runs in this order; any **block** stops the version. The checks run in
two places, both inside the store's own Cloudflare account and neither on a VPS (54.15):

- **Static checks (C1–C7 except the ClamAV scan, C9, C11)** run in the `store-checks` Queue
  consumer Worker: the package is streamed from R2, and the format, signature, manifest,
  import and capability audits use `mkp-check`, the SDK's Rust checker compiled to Wasm (the
  same code as `mkp check`, 54.12). YARA rules run through YARA-X compiled to Wasm. Each step
  stays far under the Worker CPU limit; a package too large for that is refused by C1's size
  limit first.
- **The sandbox run (C8), the ClamAV scan (C7) and the reproducible build (C10)** run in an
  **isolated runner**: a **Cloudflare Container** started per check with outbound internet
  off (`enableInternet = false`), no Worker secrets and no bindings except a single-use
  upload URL for its report; the image applies the same `plugins-sandbox` Linux profile
  (namespaces, seccomp, Landlock) as homes do. Containers' maturity, instance limits and
  whether that profile can be applied inside one are checked in a spike before this story is
  built (README §14 item 11).
- **Fallback, if Containers does not fit:** a GitHub Actions job (`store-check-runner.yml`)
  started by the store through `repository_dispatch` with only the submission id. Its first
  step downloads the package and source with single-use URLs; then the runner's egress is
  firewalled off (as `plugins-sandbox` does, §5.4) before any plugin code runs; it has no
  repository or org secrets; its report leaves as a job artifact that a separate job, which
  never runs plugin code, posts back with a single-use URL. The store's dispatch token can
  only start that one workflow.

Either way the runner can reach nothing but the store's own upload URL, and nothing it does can
touch the cloud.

| # | Check | Block when |
|---|---|---|
| C1 | Package format and limits (54.01) | invalid |
| C2 | Developer signature (54.14), key active, id under a verified prefix | invalid |
| C3 | Manifest schema and rules (54.01); `maktaba` upper bound present | invalid |
| C4 | **Import audit** (54.02): imports ⊆ allowed; every gated interface covered by a declared capability | forbidden or undeclared import |
| C5 | **Capability audit**: declared but not imported → warn; `medium` capabilities without a stated reason in the notes → warn | — (warn) |
| C6 | Strings: every key in `en` and `ar`; no URLs, emails or phone numbers in user-visible strings; reserved words (`Maktaba`, «مكتبة», `official`) not in names | missing string or reserved word; URLs → warn |
| C7 | Malware scan of the package and source archive (ClamAV signatures, YARA rules for known Wasm cryptominer patterns) | hit |
| C8 | **Sandbox run**: the plugin's fixtures plus generated events (50 videos, random metadata, the sample media set) through the harness (54.12) with `--limits strict`, recording calls, outputs and resources | any trap on fixtures; p95 memory > 80 % of declared; outputs violating rules |
| C9 | **Diff vs previous version**: new capabilities, new panels, new exports, size jump > 3×, call pattern change > 5× | — (routes to human review) |
| C10 | Source archive present (**required for every version**, kept private to reviewers; decided 2026-11-03; missing → blocked) and builds (`mkp build` in the language's container) to a component whose sha256 matches, when the language toolchain is reproducible (Rust, TinyGo); otherwise "not reproducible" noted | — (badge only) |
| C11 | Rating (54.08's shared function) | — |

Target: report ≤ 30 minutes p95 (N11). The report is shown to the developer and reviewers.

### 2. When a human reviews

- every **first** version of a plugin;
- any version adding a **capability**, a **panel** (panels are shown to every user of the
  home, viewers included, so a person always reviews them), or a **`plugin-data.read`**;
- any version C5, C6, C9 or C10 flagged;
- any version of a plugin with an open malware or data-misuse report;
- a random 5 % sample of the rest.

Other versions are approved automatically and countersigned with review level `automated`.

### 3. The review queue

`plugins.maktaba.tv/admin/queue`, the store's own admin pages (54.15 §7; reviewers sign in
with a store account, passkey required): oldest first, first submissions before
updates, flagged ones marked. A reviewer sees the manifest, capabilities and their stated
reasons, the check report, the call trace (which API calls with which arguments, summarised),
outputs on the sample set, the diff, and the source (browsable). Decisions: **approve**
(countersign `reviewed`), **reject** (reasons from a fixed list: misleading, does not work,
asks for more than it needs, harmful outputs, malware, policy, strings; plus notes), **ask**
(question to the developer; the clock pauses).

### 4. Guidelines

`docs/plugins/guidelines.md` (maktaba.dev/plugins/guidelines, en + ar), the checklist reviewers
use: ask only for what you use; do what the listing says; no tracking or fingerprinting of the
library for later exfiltration (moot without network, but no hidden data stores either); no
content that is illegal or hateful; names and icons not imitating Maktaba or others; Arabic
strings may be machine translated but must be labelled so in the notes, and the listing
then shows "Arabic machine translated" / «ترجمة آلية» (decided 2026-11-03). A listing
without both English and Arabic is refused (C6).

### 5. SLAs and staffing

Published on maktaba.dev: first response ≤ 5 business days for new plugins, ≤ 2 for updates
needing review (N12); malware and data-misuse reports triaged ≤ 24 h (54.18). A dashboard
shows queue age; a queue older than the SLA alerts the store on-call through the store's own
alert (a Cron Trigger checks queue age and sends `store_review_backlog` by email to the
`store_admin`s; no cloud monitoring is used).

## Acceptance criteria

- **Given** a package importing `wasi:http/outgoing-handler`,
  **when** submitted,
  **then** C4 blocks it within minutes with `plugin_forbidden_import`.
- **Given** an update that adds `transcript.read`,
  **when** checks pass,
  **then** it goes to human review, not auto-approval.
- **Given** an update that only changes code, with no flags,
  **when** checks pass,
  **then** it is approved automatically with level `automated` (unless sampled).
- **Given** a plugin whose name contains «مكتبة»,
  **when** checked,
  **then** C6 blocks it.
- **Given** a plugin that traps on its own fixtures,
  **when** C8 runs,
  **then** it is blocked with the trap's backtrace in the report.
- **Given** a reviewer approving,
  **when** the decision is saved,
  **then** `store.sig` is made with level `reviewed`, the catalog regenerates, and the audit
  row names the reviewer.
- **Given** a reviewer whose last passkey check was 6 minutes ago,
  **when** they approve,
  **then** `401 reauth_required` («أكّد أنّك أنت»), and nothing is signed.
- **Given** a plugin whose C8 run tries to open a socket or resolve a name,
  **when** the isolated runner runs it,
  **then** the attempt fails inside the sandbox and the report records it; the runner's
  egress log shows no connection.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (go + rust) | corpus of bad packages, one per block | check | blocked at the right check |
| T02 | integration | good package; update adding a capability; plain update | check | approve path per §2 |
| T03 | unit (go) | string checker | URLs, reserved words in en/ar | warn / block |
| T04 | integration | C8 with a memory-hungry plugin | run | flagged at 80 % |
| T05 | integration | C10 reproducible Rust build | build | sha matches → badge |
| T06 | role test (ts) | review routes | no one / buyer / developer / reviewer (TOTP only) / reviewer (passkey) / store admin | 401 / 403 / 403 / 403 / 200 / 200 |
| T08 | integration | isolated runner image (Container or the CI fallback) with an escape-probe plugin | run C8 | every probe fails; zero connections at the canary; no secret in the environment |
| T07 | CI (`store`) | timing | full pipeline on the corpus | p95 ≤ 30 min (scaled) |

## Edge cases

- **A plugin whose fixtures are empty**: C8 uses only generated events; the report says so.
- **The isolated runner is unavailable** (Containers capacity, or the CI fallback queued):
  submissions wait in `checking`; the SLA clock starts at report time, not upload.
- **A developer resubmits the same version**: refused (`version_exists`); bump the version.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `store/checks/` (new) | C1–C11 orchestration in the `store-checks` Queue consumer; `mkp-check` (Wasm) and YARA-X (Wasm). |
| `store/check-runner/` (new) | Container image for C7 (ClamAV), C8 and C10, with the `plugins-sandbox` profile. |
| `.github/workflows/store-check-runner.yml` (new) | The CI fallback runner (no secrets, egress firewalled). |
| `store/review/` (new) | Queue, decisions, countersigning call. |
| `store/web/` admin pages | Review pages at `/admin`, en + ar. |
| `docs/plugins/guidelines.md` (new) | Guidelines, en + ar. |
| `store/checks/testdata/` | Bad-package corpus. |

### Tests

- Integration T01, T02, T04, T05, T08; unit T03; role T06; CI T07.

### Rollout

With 54.15, dark. Reviewers named by the owner before opening submissions; the Containers
spike (or the choice of the CI fallback) done first.

### Rollback

Disable submissions (store admin switch); existing approved versions stay listed. The
runner can switch between Containers and the CI fallback by configuration.

### Dependencies

54.03 (sandbox profile reused by the check runner), 54.12 (harness), 54.15 (store).
