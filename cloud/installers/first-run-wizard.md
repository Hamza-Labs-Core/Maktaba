# First-run setup wizard (Story 25.35)

When `maktaba-server` boots without a server-secret file present, it
launches the wizard. The wizard runs on:

- **TTY** (headless boxes: Linux, RPi) — text-mode prompts.
- **A locally-bound web UI on port 8080** (desktops, NAS) — opened
  automatically in the user's browser, served only from `127.0.0.1`
  until claim succeeds, then bound to all interfaces.

## Steps

1. **Welcome / Telemetry consent** — single screen. Default opt-in for
   crash reports, opt-out for usage analytics.
2. **Hostname / display name** — friendly name shown in the cloud UI.
3. **Claim code** — 8-char base32. Field shows progress as the user types
   (validates against `^[A-Z2-7]{8}$`).
4. **Cloud account verification** — calls `POST /v1/servers/claims/redeem`
   with `{code, name, slug: <generated>, version, public_key_pem}`.
5. **Secret persistence** — server secret returned by the cloud is
   stored in the OS keychain (when present) or at
   `/var/lib/maktaba/secret` mode `0600`.
6. **Storage location** — where to keep the library. Default
   `/var/lib/maktaba/library` (or `~/Maktaba` on desktops).
7. **Done** — service starts, browser navigates to `https://app.maktaba.app`.

## Error handling

| Failure | Wizard behaviour |
|---|---|
| Claim code expired | Returns to step 3 with banner; user fetches a fresh code. |
| Network unreachable | Retry loop with 30s backoff; user can skip to local-only mode. |
| Disk perms | Inform the user of the failing path; offer to retry as root. |
| Cloud says slug taken | Auto-append `-1`, `-2`, ... until accepted. |

## CLI fallback

```sh
sudo maktaba-server setup \
    --claim-code ABCD1234 \
    --name "Living Room" \
    --library /srv/library
```

Useful for scripted deploys (Ansible, NixOS).
