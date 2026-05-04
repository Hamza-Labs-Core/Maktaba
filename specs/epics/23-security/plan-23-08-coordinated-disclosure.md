# Implementation Plan — Story 23.8 Coordinated disclosure and security response

> Companion to [story-23-08-coordinated-disclosure.md](story-23-08-coordinated-disclosure.md).
> Story states *what* and *why*; this plan states *how*.
> Builds on the SBOM/CVE plumbing from
> [Story 23.7](plan-23-07-supply-chain-security.md) and the release
> flow from [Story 22.5](plan-22-05-release-management.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Disclosure address | GitHub Security Advisories (GHSA) primary; backup email `security@maktaba.io`. |
| SECURITY.md | Top-level repo file; documents address, SLA (3 business days ack, 90 days fix-or-coordinated-disclosure), scope. |
| Tracking | Private GHSA drafts during embargo; published advisory on fix. |
| CVE assignment | **GHSA is the canonical CVE assignment route via [GitHub Security Advisories](https://docs.github.com/en/code-security/security-advisories/repository-security-advisories/about-repository-security-advisories).** GitHub is a CVE Numbering Authority (CNA); publishing a GHSA from the repo automatically requests a CVE through GitHub's CNA workflow. We do not file CVEs through MITRE directly. |
| Patch release | Re-uses the release workflow from [plan-22-05](../22-devops-delivery/plan-22-05-release-management.md); tagged on a `release/v*.x` branch when the fix is on a non-main branch. The release workflow's `guard` job already accepts `release/*` tags (plan-22-05 §). |
| Client surface | "What version am I running?" button in the web client wires to `/api/system/version` (Story 22.5) and renders advisories pulled from a static feed published with each release. |
| Out of scope | The actual CVE fixes (per-incident); SBOM mechanics (23.7); legal/policy beyond what fits in SECURITY.md. |

## 1. Architecture diagram

```
   security@maktaba.io / GHSA report
                │
                ▼
      ┌──────────────────────┐
      │ private GHSA draft   │  (3 days to ack)
      │  triage + repro      │
      └──────────┬───────────┘
                 │ patch ready
                 ▼
      ┌──────────────────────┐
      │ release/v1.x branch  │  (Story 22.5 hotfix path)
      │  cherry-pick + tag   │
      └──────────┬───────────┘
                 │
                 ▼
      ┌──────────────────────┐
      │ publish GHSA + CVE   │
      │ update advisories.json│
      │ release notes link   │
      └──────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `SECURITY.md` | Disclosure policy. |
| `docs/security/incident-runbook.md` | Internal runbook for the maintainer-on-call. |
| `docs/security/postmortem-template.md` | Template for non-public retrospectives. |
| `security/age-pubkey.txt` | The maintainers' age public key (referenced from SECURITY.md for encrypted email reports). |
| `advisories.schema.json` | JSON Schema validating the structure of `advisories.json` (used by `tools/publish-advisory.sh` and `TestAdvisoryJsonSchema`). |
| `web/src/lib/advisories.ts` | Fetches `advisories.json`; matches by version range. |
| `web/src/components/AboutDialog.tsx` | "What version am I running?" + advisory list. |
| `tools/publish-advisory.sh` | Adds an entry to `advisories.json`, signs, opens a PR. |
| `advisories.json` | Static feed shipped with each release: `[{ id, severity, fixed_in, affects, mitigations, link }]`. |
| Tests — `tests/security/advisory_match_test.ts`, `tests/security/disclosure_drill_test.sh`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `.github/workflows/release.yml` | Embeds the current `advisories.json` into the GitHub release; the web bundle ships it for offline access. |
| `web/src/routes/settings.tsx` | Renders the About dialog; "Check for advisories" button. |
| `RELEASING.md` | Documents the patch-release flow. |

### 2.3 SECURITY.md

```
# Security Policy

We take security seriously and welcome reports from researchers.

## Reporting a vulnerability

Preferred: open a GitHub Security Advisory at
https://github.com/maktaba/maktaba/security/advisories/new

Backup: email security@maktaba.io. Encrypt with the maintainers' age
public key (see `docs/security/age-pubkey.txt`) when the report contains
exploit details. We accept English and Arabic.

We will:

- Acknowledge receipt within 3 business days.
- Aim to resolve and ship a fix within 90 days; coordinate a public
  disclosure date with you.
- Credit you in the published advisory unless you ask to remain
  anonymous.

## Scope

In scope:

- Code in this repository.
- Official artifacts: ghcr.io/maktaba/* containers, Homebrew tap,
  released binaries, mobile/desktop installers.

Out of scope:

- Third-party services or dependencies (please report to upstream).
- Social-engineering or denial-of-service against the public website.

## Process

Each accepted report becomes a private GHSA draft. We will share the
draft with you for review before publication. Critical fixes ship as
patch versions on supported branches; the release notes link to the
GHSA.

Public disclosure default: 90 days from the acknowledgement, or
sooner if the fix is shipped and validated. Researchers may request
earlier coordinated disclosure; we'll coordinate case-by-case.

## Supported versions

| Version | Supported |
|---------|-----------|
| 1.x     | Yes       |
| 0.x     | No (pre-release) |

The current branch (`main`) and the most recent two minor versions
receive security fixes.
```

### 2.4 Incident runbook (sketch)

`docs/security/incident-runbook.md` is a checklist for the
on-call maintainer:

```
1. Acknowledge.
   - Reply within 3 business days.
   - If GHSA: add reporter to the draft; thank them.
   - If email: open a GHSA draft and link the email thread.

2. Triage.
   - Reproduce on the affected version(s).
   - Severity (CVSS v3.1).
   - Assign owner.

3. Fix.
   - Branch from main (or release/v1.x for older minors).
   - Write the test first (regression).
   - Land in a non-public branch; do not push to main until coordinated.

4. Coordinate.
   - Choose a disclosure date.
   - Pre-warn integrators if needed (private mailing list).

5. Release.
   - Cherry-pick into release/v1.x branches as needed.
   - Tag patch versions (Story 22.5 hotfix path).
   - Publish GHSA → CVE assignment via GitHub.

6. Post-release.
   - Update advisories.json (tools/publish-advisory.sh).
   - Web client surfaces the advisory next to the version banner.
   - Public retrospective in the changelog (without exploit detail).
```

### 2.5 Advisory feed format

`advisories.json` (committed and shipped with each release):

```json
{
  "version": 1,
  "published": "2026-04-12T10:00:00Z",
  "advisories": [
    {
      "id": "GHSA-1234-5678-90ab",
      "cve": "CVE-2026-12345",
      "severity": "high",
      "title": "Path traversal in subtitle ingest",
      "summary": "An operator-supplied SRT path could escape the library root via NUL-byte injection.",
      "affects": ">=1.0.0 <1.0.5",
      "fixed_in": "1.0.5",
      "mitigations": "Disable sidecar ingest until upgrading.",
      "link": "https://github.com/maktaba/maktaba/security/advisories/GHSA-1234-5678-90ab"
    }
  ]
}
```

`tools/publish-advisory.sh` appends to the array, validates the schema
against `advisories.schema.json`, signs the file with minisign, and
opens a PR.

### 2.6 Web client integration

`web/src/lib/advisories.ts`:

```ts
import { satisfies } from 'semver';
import { version as currentVersion } from './version';

export type Advisory = {
  id: string;
  cve?: string;
  severity: 'low' | 'medium' | 'high' | 'critical';
  title: string;
  summary: string;
  affects: string;     // semver range
  fixed_in: string;
  mitigations?: string;
  link: string;
};

export async function fetchAdvisories(): Promise<Advisory[]> {
  // Try the shipped, offline copy first; fall back to network.
  try {
    const res = await fetch('/advisories.json', { cache: 'no-store' });
    if (res.ok) return (await res.json()).advisories;
  } catch {}
  const res = await fetch('https://maktaba.io/advisories.json', { cache: 'no-store' });
  return (await res.json()).advisories;
}

export function applicable(advisories: Advisory[], v: string = currentVersion): Advisory[] {
  return advisories.filter(a => satisfies(v, a.affects));
}
```

`AboutDialog.tsx` renders:

```tsx
<section>
  <h2>You are running Maktaba {version}</h2>
  <button onClick={loadAdvisories}>Check for advisories</button>
  {applicable.length > 0 && (
    <Banner severity={maxSeverity(applicable)}>
      {applicable.length} advisory(ies) affect this version.
      <a href="https://github.com/maktaba/maktaba/security/advisories">See advisories</a>
    </Banner>
  )}
</section>
```

### 2.7 Patch-release wiring

The patch-release flow re-uses the release workflow defined in
[plan-22-05 — release management](../22-devops-delivery/plan-22-05-release-management.md).
This plan adds only the coordination layer (advisory publication
timing); the actual release mechanics (branch protection rules,
required-status-checks, tag → release.yml dispatch, multi-arch image
build, signing) are owned by plan-22-05.

`RELEASING.md` adds the patch-release flow:

```
1. From the affected minor, create a release branch:
     git checkout v1.0.4 -b release/v1.0.x
2. Cherry-pick the security fix and the regression test.
3. Bump VERSION; run tools/bump-version.sh.
4. Push the branch; tag v1.0.5.
5. Wait for release.yml (plan-22-05) to publish images, binaries, and the GH release.
6. Publish the GHSA from draft (this triggers the GHSA→CVE assignment via GitHub's CNA workflow).
7. Run tools/publish-advisory.sh to add to advisories.json on main.
```

The release workflow's `guard` job (plan-22-05) accepts tags on
`release/*` branches.

## 3. Test plan

### 3.1 Disclosure drill (TC1)

| Test | What it pins |
|---|---|
| `TestDisclosureAckSla` | A synthetic GHSA report filed at T=0 is acknowledged in the on-call's docket within 3 business days, asserted by a tabletop exercise (manual). The runbook's checklist is the artifact. |
| `TestDisclosureRunbookExists` | `docs/security/incident-runbook.md` is present, has the six numbered sections, and is referenced from `SECURITY.md`. |

### 3.2 Advisory link (TC2)

| Test | What it pins |
|---|---|
| `TestAdvisoryAppliesToCurrentVersion` | A fixture `advisories.json` with `affects: ">=1.0.0 <1.0.5"` and the client at `1.0.4` returns one applicable advisory; at `1.0.5` returns zero. |
| `TestAdvisoryFetchOfflineFallsBackToNetwork` | Mock `/advisories.json` to 404; the client falls back to the published feed. |
| `TestAdvisoryRenderingShowsLink` | Component test asserts the advisory link is rendered with `target="_blank"` and `rel="noopener noreferrer"`. |

### 3.3 Patch release (TC3)

| Test | What it pins |
|---|---|
| `TestPatchReleaseEndToEnd` | A synthetic CVE on `release/v1.0.x`: cherry-pick → tag → release.yml runs → SBOM updated → advisory published; the test traces each step. |
| `TestAdvisoryJsonSchema` | `advisories.json` validates against `advisories.schema.json`; missing `id` or `affects` fails CI. |
| `TestSignedAdvisoryFeed` | `tools/publish-advisory.sh` signs the file; `minisign -V` succeeds. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Reporter wants anonymity (EC1) | The runbook's "credit" step records the request. Published advisory thanks "an anonymous researcher" by default. | `TestAdvisoryAnonymousCredit` |
| Vulnerability is in an upstream dep (EC2) | Maktaba's advisory points to the upstream CVE/GHSA and ships the dep bump as the fix. The advisory's `mitigations` field tells operators how to mitigate before upgrading. | `TestUpstreamAdvisoryFlow` |
| Researcher wants to publish before fix (EC3) | The runbook documents the 90-day default and the "case-by-case" coordination clause. The on-call may shorten the embargo if the issue is being actively exploited. | runbook |
| Multiple supported branches need fixes | Cherry-pick into each `release/v1.x` branch; tag separately. The advisory's `affects` carries the OR of impacted ranges. | `TestMultipleBranchesPatchRelease` |
| GHSA tooling outage | Maintainers fall back to the email channel; the runbook's escalation step documents this. | n/a |
| Embargo leak before disclosure | The on-call accelerates the release and notifies the reporter. The retrospective documents the leak source. | runbook |
| Severity disputed | The runbook's triage step records both the maintainer and reporter assessments; CVSS v3.1 is the tiebreaker. | n/a |
| Fix breaks API compatibility | Documented in the release notes and advisory; the upgrade path is the same as the normal hotfix flow. | `TestBreakingFixDocumented` |
| Old supported version receives EOL | The supported-versions table in SECURITY.md is updated; advisories targeting EOL versions still publish but `mitigations` recommends upgrading. | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| GitHub Security Advisories | n/a (platform) | Disclosure tracking. |
| `semver` (npm) | latest | Range matching in the web client. |
| `minisign` | already (Story 22.2) | Signing the advisory feed. |
| `age` | latest | Encrypted email reports (optional). |

## 6. Acceptance checklist

**Policy**
- [ ] `SECURITY.md` documents the address, SLA, and scope.
- [ ] Supported-versions table maintained.

**Process**
- [ ] Incident runbook exists and is linked.
- [ ] Postmortem template present.

**Advisory feed**
- [ ] `advisories.json` shipped with every release.
- [ ] Schema validated; signed via minisign.

**Client**
- [ ] About dialog shows current version.
- [ ] Applicable advisories surface as a banner.
- [ ] Offline-first fetch with network fallback.

**Patch release**
- [ ] `release/v*.x` branch path documented in RELEASING.md.
- [ ] Re-uses Story 22.5 release workflow.
