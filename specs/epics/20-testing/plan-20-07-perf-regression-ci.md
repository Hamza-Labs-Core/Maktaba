# Implementation Plan — Story 20.7 Performance Regression Tests in CI

> Companion to [story-20-07-perf-regression-ci.md](story-20-07-perf-regression-ci.md).
> Reduced perf suite per PR (≤ 5 min); full nightly; PR comment with deltas;
> auto-quarantine flapping budgets.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| `make perf-ci` | Subset of Story 18.1's harness — 9 endpoints, 3 trials each, warm only. |
| `make perf` | Full suite. |
| Budget source | `shared/perf_budgets.yaml` (canonical; see plan-18-01). All gating reads from this single file. |
| Storage | JSON-Lines per run pushed to `./.perf-history/{date}.jsonl` on a long-lived branch `perf-history` in the same repo (or Prometheus pushgateway if available). |
| PR comment | GitHub action posts deltas vs. `main` baseline (last green nightly). |
| Regression gate | Two-condition gate: `delta > 10% AND value > budget * 1.05`. The 10% bound is the soft warn-only delta vs. last-nightly baseline; the absolute `> budget * 1.05` clause is the hard breach against the budget itself. Both must hold to fail the build. |
| Quarantine | A budget that fails on **3 distinct calendar dates within a rolling 5-day window** on `main` is moved to `quarantined:` section of `perf_budgets.yaml`; auto-issue filed. (Distinct-dates rather than total occurrences avoids quarantining a budget after three failures inside a single hot-fix scramble.) |
| Out of scope | Cold-cache budgets (run weekly via separate target). |

## 1. Project layout

```
tests/perf/
├── ci_subset.go                    # filters budgets where ci_pr=true
├── delta_report.go                 # produces PR-comment markdown
├── publisher.go                    # writes to ./.perf-history
└── quarantine.go                   # rolling-window flap detector

.github/workflows/
├── perf-ci.yml
├── perf-nightly.yml
└── perf-quarantine.yml             # weekly housekeeping

scripts/perf/
├── post-pr-comment.sh
└── auto-file-issue.sh

docs/runbooks/
└── perf-regression.md
```

## 2. perf-ci subset

```yaml
# shared/perf_budgets.yaml (excerpt). The `ci_pr` field is added by plan-18-01
# to the canonical `Budget` struct; see cross-cutting §1.5.
endpoints:
  - name: api.libraries.list
    ci_pr: true
    cache: warm
    ...
  - name: streaming.segment.first_byte.cold
    ci_pr: false                     # full suite only
    cache: cold
```

```go
// tests/perf/ci_subset.go
//
// Field name alignment (cross-cutting §1.5): the YAML key is `ci_pr` and
// the Go struct field is `CIPR` (mapped via `yaml:"ci_pr"`). Both names
// must stay in lockstep with the canonical `Budget` struct in plan-18-01.
func PRSet(b *BudgetFile) []Budget {
    out := []Budget{}
    for _, e := range b.Endpoints {
        if e.CIPR && e.Cache == "warm" { out = append(out, e) }
    }
    return out
}
```

## 3. PR comment

```go
// tests/perf/delta_report.go
type Delta struct {
    Name     string
    Baseline float64
    Measured float64
    Pct      float64
    Breach   bool
    Quarantined bool
}

func Render(d []Delta) string {
    var b strings.Builder
    b.WriteString("### Perf budgets\n\n| Endpoint | Baseline p95 | This PR p95 | Δ | |\n|---|---:|---:|---:|---|\n")
    for _, x := range d {
        marker := ""
        if x.Breach { marker = "❌" }
        if x.Quarantined { marker = "🟡" }
        b.WriteString(fmt.Sprintf("| `%s` | %.1f ms | %.1f ms | %+.1f%% | %s |\n",
            x.Name, x.Baseline, x.Measured, x.Pct, marker))
    }
    return b.String()
}
```

```bash
# scripts/perf/post-pr-comment.sh
#!/usr/bin/env bash
set -euo pipefail
COMMENT=$(cat "$1")
gh pr comment "${PR_NUMBER}" --body "$COMMENT"
```

## 4. Workflow — PR

```yaml
# .github/workflows/perf-ci.yml
name: perf-ci
on: [pull_request]
jobs:
  perf:
    # Runner tag aligned with plan-18-01's `darwin-arm64-16gb-m2` profile.
    runs-on: [self-hosted, perf-ci, darwin-arm64-16gb-m2]
    timeout-minutes: 8
    steps:
      - uses: actions/checkout@v4
      - run: make perf-ci > perf-ci.json
      - run: make perf-baseline-fetch > baseline.json   # last green nightly
      - run: |
          go run ./tests/perf/cmd/render-delta \
            -baseline baseline.json -current perf-ci.json -out delta.md
      - run: scripts/perf/post-pr-comment.sh delta.md
        env: { PR_NUMBER: ${{ github.event.pull_request.number }} }
      - run: |
          # Two-condition regression gate (see §0):
          #   delta > 10 %  AND  value > budget * 1.05
          # The 10 % delta is computed against the last green nightly; the
          # absolute `value > budget * 1.05` clause guards against drift
          # where the baseline itself has crept above budget.
          go run ./tests/perf/cmd/gate \
            -baseline baseline.json -current perf-ci.json \
            -delta-warn-pct 10 \
            -absolute-budget-tolerance-pct 5
```

## 5. Workflow — nightly

```yaml
# .github/workflows/perf-nightly.yml
name: perf-nightly
on: { schedule: [{ cron: '0 5 * * *' }] }
permissions:
  # Required to push to the long-lived `perf-history` branch from this
  # workflow. Without `contents: write`, the `git push` below fails with a
  # 403 on default-locked repos.
  contents: write
jobs:
  perf:
    runs-on: [self-hosted, perf-ci, darwin-arm64-16gb-m2]
    timeout-minutes: 45
    steps:
      - uses: actions/checkout@v4
      - run: make perf > nightly.jsonl
      - run: |
          # publish to history branch
          git fetch origin perf-history
          git worktree add /tmp/hist origin/perf-history
          mv nightly.jsonl /tmp/hist/$(date +%Y-%m-%d).jsonl
          (cd /tmp/hist && git add . && git commit -m "perf nightly $(date +%F)" && git push origin perf-history)
      - run: go run ./tests/perf/cmd/quarantine-scan history/
```

## 6. Quarantine logic

```go
// tests/perf/quarantine.go
type Window struct{ Days int; FailsThreshold int }

// ScanFlaps quarantines a budget that has breached on at least
// `w.FailsThreshold` **distinct calendar dates** within the rolling
// `w.Days` window. Counting distinct dates (rather than total breaches)
// avoids quarantining a budget after three failures inside a single
// hot-fix scramble — those represent one incident, not three.
func ScanFlaps(history []NightlyRun, w Window) []string {
    // budget name → set of calendar dates (UTC, YYYY-MM-DD) it breached on
    dates := map[string]map[string]struct{}{}
    cutoff := time.Now().AddDate(0, 0, -w.Days)
    for _, r := range history {
        if r.At.Before(cutoff) { continue }
        day := r.At.UTC().Format("2006-01-02")
        for _, b := range r.Breaches {
            if dates[b.Name] == nil { dates[b.Name] = map[string]struct{}{} }
            dates[b.Name][day] = struct{}{}
        }
    }
    quar := []string{}
    for name, days := range dates {
        if len(days) >= w.FailsThreshold { quar = append(quar, name) }
    }
    return quar
}
```

When the scanner finds new candidates, it:

1. Patches `perf_budgets.yaml` moving them to a `quarantined:` list.
2. Opens a GitHub issue `[perf-quarantine] api.search.warm` with logs attached.
3. Comments next nightly noting the quarantine.

```bash
# scripts/perf/auto-file-issue.sh
gh issue create -t "[perf-quarantine] $1" -b "Auto-quarantined after $FAILS in $WINDOW_DAYS days. See attached logs." -l perf,quarantined
```

## 7. Test cases

### TC1 — PR delta detection
PR injects a 50 ms artificial slowdown into `videos.GetByID`. `make perf-ci` captures p95 +50 ms vs baseline. Comment gets posted. Gate fires on `>10%` rule (assuming baseline is ~30 ms, +50 ms = +166 %).

### TC2 — Nightly publish
Run `perf-nightly` workflow on schedule. After the run, `perf-history` branch contains a new file `.perf-history/YYYY-MM-DD.jsonl`. Static dashboard (`scripts/perf/dashboard.html`) reads the last 30 files and renders a chart.

### TC3 — Quarantine
Inject 3 nightly fails for `api.search.warm` within 5 days. The next quarantine scan moves it to `quarantined:` and creates `[perf-quarantine] api.search.warm` issue. Subsequent runs skip its gate.

## 8. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 runner contention | story | Tagged self-hosted runners only; PR queues if no available runner. |
| EC2 cold vs warm | story | perf-ci is warm-only; weekly cold suite separate. |
| EC3 cross-OS variance | story | Per-OS reports, never averaged; baseline is per-runner-tag. |
| Baseline missing on first run after merge | impl | If no baseline, gate skips and posts "first run; no baseline". |
| Push to perf-history branch races | impl | Always git pull --rebase before push; one writer (nightly cron). |

## 9. Configuration

```yaml
perf_ci:
  pr_p95_tolerance_pct: 10
  trials_per_endpoint: 3
  warm_only: true
  baseline_branch: perf-history
  baseline_lookup_days: 7
  flap_window:
    days: 5
    fails_threshold: 3
  runner_tags:
    - mac-m2-8gb
    - linux-x86-16gb
```

## 10. Make targets

```makefile
.PHONY: perf-ci perf perf-baseline-fetch

perf-ci:
	go run ./tests/perf/cmd/runner -mode=ci

perf:
	go run ./tests/perf/cmd/runner -mode=full

# Fetch the last green nightly's JSONL from the perf-history branch and emit
# it to stdout. Used by perf-ci.yml to build `baseline.json`.
#
# Auth: on private repos, raw.githubusercontent.com requires a token. Pass
# the workflow's GITHUB_TOKEN (or a fine-grained PAT with `Contents: read`
# on the repo) via $GITHUB_TOKEN.
perf-baseline-fetch:
	@bash -c 'set -euo pipefail; \
	  hdr=""; \
	  if [ -n "$${GITHUB_TOKEN:-}" ]; then hdr="-H \"Authorization: Bearer $$GITHUB_TOKEN\""; fi; \
	  latest=$$(eval curl -fsSL $$hdr "https://api.github.com/repos/$$GITHUB_REPOSITORY/contents/.perf-history?ref=perf-history" \
	    | jq -r ".[].name" | grep -E "^[0-9]{4}-[0-9]{2}-[0-9]{2}\\.jsonl$$" | sort | tail -1); \
	  eval curl -fsSL $$hdr "https://raw.githubusercontent.com/$$GITHUB_REPOSITORY/perf-history/.perf-history/$$latest"'
```

## 11. Dashboard

`docs/dashboards/perf.html` — static page that fetches the last N files
from `perf-history` via raw GitHub URL and plots p95 over time per endpoint
with Chart.js. Linked from `docs/runbooks/perf-regression.md`.

On **private** repos, `raw.githubusercontent.com` requires authentication.
The dashboard either (a) is rendered server-side by a workflow that pushes
the rendered HTML to the same `perf-history` branch (preferred — no
client-side token), or (b) is loaded with a short-lived fine-grained PAT
(`Contents: read` on this repo only) injected at fetch time. Document
whichever path the team adopts in
`docs/runbooks/perf-regression.md`. Never ship a long-lived PAT in the
HTML.

## 12. Dependencies

- Story 18.1 (budgets file, harness).
- Story 20.1 (test tiers; perf is its own tier).
- Story 20.8 (similar quarantine model).
- Epic 22 (self-hosted runners).
