# Story 23.8 — Coordinated disclosure and security response

A self-hosted product needs a way for security researchers to
report. We're tiny; our process is small but real.

## Acceptance criteria

- AC1. `SECURITY.md` documents the disclosure address (a dedicated
  email or GitHub Security Advisories), the response SLA (3 business
  days to ack, 90 days to fix or coordinated disclosure), and the
  scope (in-tree code, official artifacts).
- AC2. Reported vulnerabilities are tracked in a private repo or
  GHSA draft; once fixed, an advisory is published with CVE if
  applicable, mitigation, and affected versions.
- AC3. Critical fixes are released as patch versions on supported
  branches; the release notes link the GHSA.
- AC4. The web client surfaces a one-click "What version am I
  running?" with a link to known advisories for that version.

## Test cases

- TC1. Security workflow drill: a synthetic report is filed against
  the documented address; an acknowledgement is recorded within
  the SLA in a tabletop exercise.
- TC2. Advisory link: a versioned client renders advisories
  matching its `version` field; an intentionally outdated client
  renders the upgrade prompt.
- TC3. Patch release: a synthetic CVE produces a patch tag, an
  artifact rebuild, an SBOM update, and an advisory notification
  end-to-end.

## Edge cases

- EC1. Reporter requests anonymity — supported; published advisory
  thanks "an anonymous researcher" by default.
- EC2. Vulnerability in an upstream dep — Maktaba's advisory points
  to the upstream and ships the dep bump as the fix.
- EC3. Disclosure conflict (researcher wants to publish before fix)
  — `SECURITY.md` documents the 90-day default; deviations are
  coordinated case-by-case.
