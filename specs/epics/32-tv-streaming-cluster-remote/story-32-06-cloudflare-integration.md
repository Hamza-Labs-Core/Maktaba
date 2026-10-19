# Story 32.6 — Cloudflare Stream integration

## Description

*As the admin, I want to connect my own Cloudflare account to Maktaba once — account id and an API
token — and see a clear "ready" or "what is missing", before remote viewing can be turned on.*

The credentials are the most sensitive secret in this epic (a Stream Write token can create and
delete every video of the account). They live on the home only, encrypted, and never reach node
roles. This story also carries the **spike** that verifies the Cloudflare behaviours the design
assumes (README "not documented").

Design: [design.md](design.md) §7.1, §7.6; README "Security", "Cloudflare facts".

## Acceptance criteria

- **AC1** Slot 0119 creates `home_secrets` (name, ciphertext, nonce, key_id, updated_at,
  updated_by), revoked from `maktaba_worker`. Values are sealed with AES-256-GCM using a 32-byte key
  file in the home's config directory (`secrets.key`, mode 0600, created on first use); the key
  never enters the database.
- **AC2** Admin → Integrations → **Cloudflare Stream** (an entry of the extensions list, like
  Plex): account id, API token (secret), "Test connection". Saving stores the token in
  `home_secrets` (`cf_api_token`); the API returns it masked; the extensions table holds no secret.
- **AC3** Test connection checks, and reports each line as ok / failed with the reason:
  token valid and active; the account has Stream (list live inputs); storage capacity and use;
  Analytics read (optional — without it the cost estimate uses session minutes only);
  creates the signing key if none (`POST /stream/keys`, stores `id` + `pem` in `home_secrets`);
  learns the customer playback domain; each online streaming host's `can_push` for the chosen
  ingest.
- **AC4** `tv_remote.enabled` can only be switched on after a green test less than 24 h old; the
  status shows the last test time and result.
- **AC5** Removing the integration disables remote, stops pushes, deletes Maktaba's live inputs and
  signing key at Cloudflare (with confirmation), and deletes the secrets.
- **AC6** Cloudflare errors are shown with their code and message; the token is never logged.
- **AC6a (README Q7, accepted default)** Every extension's `secret` setting field — the Plex token
  in `extensions.settings` today, and any future one — is stored in `home_secrets` instead. On the
  home API's first start after this story, existing secret values are moved out of
  `extensions.settings` into `home_secrets` (idempotent) and returned masked by the API. Code that
  needs a secret reads it through the secrets store on the home; a job that needs one on a node
  gets it per job from the home, never by reading a table its role can see.
- **AC7 (spike, documented in the runbook)** With a real account: (a) a token whose `sub` is the
  in-progress video id plays the live broadcast; (b) whether `exp` is enforced per segment;
  (c) glass-to-glass latency of standard HLS and of LL-HLS with the channel encoder settings;
  (d) a reconnect within `timeoutSeconds` keeps the same video id; (e) deleting the recording right
  after a broadcast frees storage; (f) concurrent live input limits on the account; (g) whether a live output reports its
  destination's state, and whether outputs survive a reconnect and a new broadcast (32.13, README Q13). Findings update
  the design before 32.7 is merged.

## Test cases

- **TC1** `TestSecretsSealOpen` — round trip; wrong key → error; nonce unique.
- **TC2** `TestSecretsNotReadableByWorker` (Postgres) — `maktaba_worker` SELECT → permission denied.
- **TC3** `TestCFTestConnection` — fake Cloudflare server: each failing check reported; signing key
  created once.
- **TC4** `TestRemoteEnableNeedsGreenTest`.
- **TC5** `TestTokenMaskedInAPI`.
- **TC6** `TestExtensionSecretsMovedToHomeSecrets` — a Plex token in `extensions.settings` moves
  to `home_secrets` once, is masked in the API, and `maktaba_worker` cannot read it.

## Edge cases

- **EC1** The token is replaced: the next test re-checks; existing inputs keep working (same account).
- **EC2** The account id is changed: refused while inputs exist ("remove the integration first").
- **EC3** `secrets.key` lost (disk replaced): secrets cannot be opened → the integration shows
  "re-enter the token"; the signing key is recreated.

## Dependencies

None for the integration itself; 32.7 and 32.8 depend on it.
