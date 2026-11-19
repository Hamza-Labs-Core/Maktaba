# Security policy

We want to hear about any vulnerability in Maktaba. This page says how to report one, how
quickly we answer, what is in scope, and how a fix and its advisory are published.

## Reporting a vulnerability

Please do not open a public issue, pull request or discussion for a vulnerability.

- **Preferred:** report it privately through GitHub:
  [Report a vulnerability](https://github.com/Hamza-Labs-Core/Maktaba/security/advisories/new)
  (the repository's **Security** tab, then **Report a vulnerability**). The report becomes a
  private draft advisory that only you and the maintainers can see.
- **Or by email:** security@maktaba.dev.

Include what you found, how to reproduce it, the version (Settings → About shows it, and
**Copy version info** copies it) and what it lets an attacker do. Reports in English or Arabic
are welcome. The machine-readable contact is
[`/.well-known/security.txt`](https://maktaba.dev/.well-known/security.txt) (RFC 9116).

## What we promise

| Step | When |
|---|---|
| We acknowledge the report | within **3 business days** |
| We confirm or rule it out, and agree a severity with you | as soon as we can reproduce it |
| A fix is released, or a disclosure is coordinated with you | within **90 days** of the acknowledgement |

We keep you informed while we work on it, and share the draft advisory with you before it is
published.

## Scope

In scope:

- The code in this repository: the home server (api, streaming, pipeline, the unified
  `maktaba-server`), the web app, the phone, tablet, TV and computer apps, the plugin host and
  SDKs, the cloud service and relay, and the release and update tooling.
- Official artifacts built from it: the GitHub releases and their signed archives and
  packages, the container images Maktaba publishes, the installers and store apps, and the
  sites Maktaba runs (maktaba.dev, maktaba.tv).

Out of scope:

- Vulnerabilities in third-party software Maktaba depends on: report them upstream. When one
  affects Maktaba, our advisory points to the upstream advisory and the fix is the
  dependency's update.
- Plugins published by others in the plugin store: report them to their authors (a plugin
  that escapes its sandbox is a Maktaba vulnerability, and in scope).
- Denial of service by volume, social engineering, and findings that need physical access to
  an unlocked machine.

Test only against your own installation. Do not access other people's data or degrade a
service for others.

## How a fix is published

1. Each accepted report is tracked in a private GitHub security advisory draft.
2. The fix ships as a new patch release. Maktaba is released continuously from `main`, so the
   newest release is the supported one; a critical fix for an older line is also released as
   a patch on its `release/*` branch.
3. The advisory is then published with its affected versions, the release that fixes it, the
   mitigation until you can update, and a CVE when one applies. The release notes link it.
4. Every Maktaba home reads the published advisories with its update check: Settings → About
   lists the ones that affect the running version, and the release that fixes them.

Published advisories: [github.com/Hamza-Labs-Core/Maktaba/security/advisories](https://github.com/Hamza-Labs-Core/Maktaba/security/advisories).

## Disclosure

The default is coordinated disclosure: we publish the advisory when the fix is released, and
at most 90 days after the acknowledgement. If you want to publish sooner, or the fix needs
longer, tell us and we agree a date case by case.

## Credit

We credit you in the advisory, under the name you choose. If you want to stay anonymous, the
advisory thanks "an anonymous researcher", and we do not share your identity.
