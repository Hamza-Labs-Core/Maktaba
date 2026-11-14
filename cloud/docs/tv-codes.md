# TV codes on Maktaba Cloud

Epic 58 story 58.03. A TV that cannot find its home (a Samsung or LG at a relative's house, a
Roku on another network) signs in with a code: RFC 8628's device authorization, adapted. The
cloud has no OpenAPI document, so its routes are written up here; the home's half
(`POST /api/pairing/cloud-approve`, `POST /api/pairing/cloud-assertion`,
`GET /api/pairing/cloud-code/{code}`, `POST /api/pairing/device/token`) is in
`shared/api/openapi.yaml`.

The code is approved on a device signed in to the home, or on maktaba.tv by a cloud account that
a home linked to one of its users. Either way the home decides: it makes a single-use pairing
grant for the approver and claims the code, and the TV redeems that grant on the home, where
the home's own sign-in rules apply. A TV signed in by a cloud code is restricted: it never acts
as an admin and mints no credential of its own (the token's `rst` claim,
`principal.ApplyRestricted`). The cloud keeps no grant after the TV's poll.

Code: `cloud/internal/handlers/tvcodes` (`handler.go`, `approve.go`), the home's agent routes in
`cloud/internal/cloudlink/tvcodes.go`.

## Codes

Eight characters of `23456789ABCDEFGHJKMNPQRSTVWXYZ` (story 15.06's pairing alphabet without 0
and 1, which read like O and I on a TV), shown `ABCD-2345`. A code can be approved for 10
minutes; the TV polls every 5 seconds. One address holds at most 10 open codes.

The TV draws the QR code itself from `verify_url`; the cloud serves no QR image.

## Routes

| Route | Caller | Answers |
|---|---|---|
| `POST /v1/tv/codes` | no one; limited per address (10 at once) | `201 {code, poll_secret, expires_in, interval, verify_url}`; 400 `invalid_body`; 429 |
| `POST /v1/tv/codes/poll` | no one, with the code's `poll_secret`; limited per address | 202 `pending`; 429 `slow_down`; 200 the payload `{status, home_id, home_name, connections, grant}` once; then 410 `expired_token` |
| `GET /v1/tv/codes/{code}` | a signed-in cloud account | 200 `{code, platform, app_version, country, expires_at}` for an open code; 401; 404 `not_found`; 429 `rate_limited` |
| `POST /v1/tv/codes/{code}/approve` | a signed-in cloud account, `{server_id}` of a home linked to it | 200 `{status: approved, platform, country, home_name}`; 400 `invalid_body`; 401; 403 `not_linked`; 404 `not_found`; 409 `home_offline`, `already_approved`; 503 `home_unreachable`, `approve_unavailable` |
| `GET /v1/homes/{id}/tv-codes/{code}` | a home: HTTP basic auth with its id and server secret (its relay agent calls it) | 200 what asked with an open code, as above; 401 |
| `POST /v1/homes/{id}/tv-codes/{code}/claim` | a home, with its server secret, `{grant}` | the code is that home's; the grant goes to the TV's next poll |

## Who may do what

| Caller | Ask for a code | Poll | Look a code up | Approve | Claim |
|---|---|---|---|---|---|
| no one (a TV) | yes | with its poll secret only | 401 | 401 | refused |
| a signed-in cloud account | yes | with the poll secret only | yes | only for a home linked to the account (403 `not_linked` otherwise) | refused |
| a home (server secret) | — | — | its agent's lookup | through its own cloud-approve route | yes, with its own secret only |

Approving on maktaba.tv approves nothing in the cloud: the cloud signs a one-use assertion
naming the account, the home and the code (purpose `tvcode`) and posts it to the home through
the relay (`POST /api/pairing/cloud-assertion`). The home finds the user it linked to that
account and approves as them; an assertion that is not this cloud's, not for this home or this
code, already used, or for an account no longer linked approves nothing.

## Tests

- `cloud/internal/handlers/tvcodes`: `TestCodeLifecycle`, `TestCodeExpires`, `TestCodesPerAddress`,
  `TestAHomesLookupsAreCapped`, `TestApproveOnMaktabaTV`, `TestApproveOnMaktabaTVRefusals`,
  `TestAnAccountsLookupsAreCapped`, `TestRelayCallerReachesTheHome`.
- The cloud rehearsal's `tv-code` (`cloud/rehearsal/tvcode_test.go`, `cloud-rehearsal.yml`): a
  home online on the relay claims a code, the TV polls the payload once and redeems the grant on
  the home through the edge (story 58.17 T04).
- The home's half: the `tv-clients` rehearsal (`api/internal/router/p58_tvclients_pg_test.go`,
  the `tv-clients` job of `_integration.yml`) redeems the grant over the LAN and through the relay
  path with each TV's header, and the Epic 58 role table
  (`api/internal/router/p58_roles_pg_test.go`).
