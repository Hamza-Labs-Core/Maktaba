# Licensing (Epic 16)

How a Maktaba home learns what its plan allows, and how the operator runs it.

## The plans

| Home tier | Users | What it adds | Where it comes from |
|---|---|---|---|
| **Free** | 1 | the whole library, streaming, search and every client, on the home network | the default; no license |
| **Home** | 4 | remote access through Maktaba Cloud, cloud backup, analytics beyond 30 days with the per-user breakdown and export | the cloud's `pro` plan, or an offline license |
| **Pro** | unlimited | Home's, plus connecting other homes (federation) | the cloud's `family` plan, or an offline license |

The home enforces every gate in its API, whatever the client:

| Feature | Gate | Refusal |
|---|---|---|
| more than one user | `POST /api/users` and open registration (`POST /api/auth/register` after the first user) | `403 https://maktaba.dev/problems/seat-limit`, e.g. _This home's Free plan allows 1 user and it has 1. Adding users needs the Home plan (4 users) or Pro (unlimited): apply a license in Settings → Subscription._ |
| remote access | every request the cloud relay forwards (`X-Maktaba-Via`), health probes excepted | `403 https://maktaba.dev/problems/license-required` (`feature: cloud_relay`) |
| analytics | `/api/admin/analytics/*` with a range beyond 30 days, `/users`, `/export` | `403 license-required` (`feature: advanced_analytics`) |
| cloud backup, federation | `subscriptions.RequireFeature` (the routes do not exist yet: stories 24.5 / 15.3 mount behind it) | `403 license-required` |

Users that already exist keep working when the home has more users than its plan
allows (a home that went back to Free, or one whose users predate the gate); only
new users are refused, and Settings → Subscription warns the admin.

Every refusal carries `feature`, `tier` and `required_tier` next to a plain
`detail`; the web shows the detail as it is.

## One license format

```json
{"license": {"license_id": "…", "tier": "home|pro", "seats": 0,
             "issued_at": "…", "expires_at": "…",
             "features": [], "server_id": "…", "holder": "…"},
 "signature": "<base64 Ed25519 over the inner object as encoding/json writes it>"}
```

`seats` 0 means the tier's cap. `server_id` set means a **plan license**: Maktaba
Cloud issued it for one home from its owner's plan, and the home renews it. No
`server_id` means an **offline license**, valid on any home until it expires or is
revoked. The cloud (`cloud/internal/license`) signs and the home
(`api/internal/subscriptions`) verifies the same bytes; both pin one golden vector.

## The key

One Ed25519 key signs every license and the revocation list: Maktaba Cloud's
entitlement key.

- **Private half.** In the operator's macOS Keychain, item
  `maktaba-license-signing` (account `hamza-labs`), as a hex seed. Never printed,
  never committed, never written to a file. The cloud needs the same key as
  `entitlement.private_key_path` to issue plan licenses: installing it there is a
  deploy step for the cloud's host.
- **Public half.** The GitHub Actions variable `MAKTABA_LICENSE_PUBKEY` (PEM). Every
  release workflow (`release.yml`, `desktop-release.yml`) runs
  `tools/release/license-key.sh --require` before compiling, which writes it over
  the placeholder `api/internal/subscriptions/keys/license-server.pub.pem`; a
  release without it fails. A local app build embeds it when
  `MAKTABA_LICENSE_PUBKEY` is set. A build that keeps the placeholder cannot apply
  any license (the API answers 503 and Settings says so); the free tier is
  unaffected.

Print the public half again with

```sh
security find-generic-password -s maktaba-license-signing -w | maktaba-cloud license pubkey --key-stdin
```

## How a home stays in step

The home's API (`subscriptions.Refresher`, started in `api/license.go`):

- **Revocation list**, daily, from the license server: the cloud the home is claimed
  in, else `MAKTABA_LICENSE_SERVER` (default `https://api.maktaba.app`; `off` asks
  no one). It is signed; the home keeps the last one it verified (slot 0155,
  `license_state`) and uses it while offline. A listed license locks at once.
- **Plan license**, every minute, for a home whose relay agent claimed it in the
  cloud (`MAKTABA_CLOUD_IDENTITY`, `MAKTABA_CLOUDLINK_ADMIN`): through the agent to
  `GET /v1/servers/{id}/license`. A new license applies; `no_license` returns a plan
  license to Free (an offline license someone applied is left alone, and a plan
  license never replaces a higher offline one); `license_revoked` locks.
- **Grace.** A plan license lasts 31 days and is renewed daily, so a home cut off
  from the cloud keeps its plan for about a month. Any license keeps working 72 hours
  past `expires_at` (clock skew), then the home is on Free and Settings says
  _Reconnect to validate the license_.
- **Clock.** The home never goes back in time: it keeps a high-water mark of the
  latest time it saw (its own clock or the license server's `Date`), so setting the
  clock back does not revive an expired license; when the license server answers,
  its time wins.

Every change (applied, removed, revoked, plan ended) writes a security audit row.

## Admin surface on the home

`Settings → Subscription` (`/settings/subscription`; `/billing` redirects):

| Route | Who | What |
|---|---|---|
| `GET /api/entitlements` | any signed-in user | tier, `seats_allowed`, the feature map |
| `GET /api/admin/license` | admin | tier, features, seats used and allowed, status (`none`/`active`/`grace`/`expired`/`revoked`), the license's id, holder and dates (never its signature), the admin message, last refresh and its error, the cloud link, the billing page |
| `POST /api/admin/license` | admin | apply a pasted license → the view; `400 license-invalid` with a plain reason; `503` in a build without the key |
| `DELETE /api/admin/license` | admin | back to Free |
| `POST /api/admin/license/refresh` | admin | ask the license server now → the view |

A viewer gets 403 on every admin route (tests in
`api/internal/handlers/subscriptions/license_test.go`).

## Issuing a license by hand

For a home that is not linked to the cloud (or the owner's own):

```sh
security find-generic-password -s maktaba-license-signing -w \
  | maktaba-cloud license issue --key-stdin --tier pro --days 365 --holder "Name" > license.json
```

Paste the file's contents in the home's Settings → Subscription → License, then
delete the file. Revoke one with `maktaba-cloud license revoke --id lic_… --reason …`
(or the cloud admin API); homes lock at their next daily check.
