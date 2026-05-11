# Auto-update mechanism (Story 25.34)

## Cross-platform design

The cloud publishes signed release manifests at
`https://releases.maktaba.app/v1/channels/{channel}/manifest.json` for
each platform. A manifest looks like:

```json
{
  "channel": "stable",
  "version": "0.2.1",
  "released_at": "2026-04-30T10:14:00Z",
  "assets": {
    "linux-amd64-deb": {
      "url": "https://releases.maktaba.app/v1/files/maktaba-server-0.2.1-amd64.deb",
      "sha256": "abc123...",
      "size": 17839224,
      "min_supported": "0.1.0",
      "signature": "<ed25519 sig over (channel|version|sha256)>"
    }
    // ... other arch/format combinations
  },
  "key_fingerprint": "<truncated ed25519 pub fp>"
}
```

## Update workflow

The `maktaba-server update` subcommand on the on-prem binary:

1. Fetches the manifest for its compiled-in `channel`.
2. Verifies the manifest signature against the public key pinned at
   build time (the entitlement-signing key serves double duty here).
3. Refuses to upgrade past a `min_supported` step (forces an intermediate
   release first so the schema migration chain stays linear).
4. Downloads the asset for its arch/format.
5. Verifies the asset SHA-256 and signature.
6. Stages the new binary alongside the old.
7. Runs `--smoke-test` on the new binary (boot, ping /healthz, exit 0).
8. Atomically swaps the symlink at `/usr/bin/maktaba-server`.
9. Reloads the service (systemd, launchd, SCM).
10. Rolls back to the previous binary if any of steps 6–8 fail.

The cloud rejects requests for an unsupported version once it has been
end-of-lifed; the agent's offline grace period is 7 days.

## Channel matrix

| Channel | Update cadence | Audience |
|---|---|---|
| stable | quarterly + critical fixes | default for all installers |
| beta | weekly | opted in via wizard |
| nightly | per-commit | internal/dev only |

## Why we built our own

Sparkle / Squirrel / Omaha all exist, but none of them covers
Linux + NAS + RPi consistently. The cloud's manifest endpoint is dumb
enough (signed JSON over HTTPS) that each platform's installer can
implement step 8 in its native way (Sparkle XML feed on macOS, MSI
delta on Windows, apt repo on Debian) while sharing steps 1–6.
