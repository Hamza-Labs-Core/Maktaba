# CVE gate suppressions

The CVE gate (story 23.7, `tools/security/cve_gate.py`, CI's `cve-gate` job) fails on a
called Go vulnerability and on a high or critical Python, production npm (`pnpm audit --prod`)
or Rust (`cargo-audit`) one. A vulnerability that does not affect Maktaba is let through by a
file here, named for the advisory's id (a CVE, GHSA, GO-, PYSEC- or RUSTSEC- id: any of the
vulnerability's aliases matches):

```markdown
---
id: GHSA-xxxx-xxxx-xxxx
expires: 2027-02-01
---
Why this does not affect Maktaba (what the advisory needs that Maktaba never does), and
what removes the suppression (the release that fixes it, the issue tracking it).
```

- `expires` is required and at most 180 days away. An expired suppression fails the gate,
  whether or not the vulnerability is still reported, so it is renewed with a new reason or
  removed.
- The text after the front matter is the reason; a file without one fails the gate.
- A suppression is a decision about one advisory, reviewed in its own pull request.

Run the gate locally: `make cve-gate` (needs govulncheck, uv, pnpm and cargo-audit).
