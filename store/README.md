# The Maktaba plugin store

Epic 54 Phase 2 (`specs/epics/54-plugins/`, stories 54.14–54.18). The store is **its own
service, independent of Maktaba Cloud** (README §6.0): its own accounts, database, deploy and
hostname, all on Cloudflare, no VPS. Nothing here imports `cloud/` or calls `api.maktaba.tv`
(a test and a CI step check it).

| Folder                      | What                                                                                                                                                                                                     |
| --------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `signing/`                  | The signer and verifier (story 54.14): canonical JSON, Ed25519 on WebCrypto, the chain of trust. The same rules as the home's Go (`shared/plugins/pluginsig`) and the SDK's Rust (`plugind/crates/sig`). |
| `scripts/gen-vectors.ts`    | Writes `shared/plugins/testdata/signatures/`, the vectors all three check (`npm run vectors`; CI runs `--check`).                                                                                           |
| `scripts/drill-rotation.ts` | The online key rotation drill (54.14 §5, T06), run by the `store` CI job against the Go verifier.                                                                                                        |

```sh
npm ci
npm run build    # the WebAssembly (needs rustup's wasm32-unknown-unknown target) and the console
npm test         # vectors current, typecheck, vitest (Node, jsdom, and Miniflare for the Worker)
```

No test calls Cloudflare, DNS, email or a payment provider: Miniflare and fakes only.

Every key the tests and vectors use is a **test key** made from a public label: anyone can
recompute it, and no real build trusts one (`TestTheCommittedRootListsNoTestKey`).

## Going live: what the owner does

Nothing below has been done. The code ships dark: the committed root
(`shared/plugins/plugins-root.json`) holds no keys, so no home accepts any store signature, and
every plugin file is Unreviewed until these steps are done.

### 1. The two plugin root keys (offline, once)

The root keys sign only `store-keys.json`, a few times a year. They never touch a network.
`scripts/keys.ts` (Node 20 or newer, `npm ci` done beforehand) makes and uses them.

1. On an offline machine (networking off, ideally booted from a live USB), with two encrypted
   USB sticks mounted:

   ```sh
   npx tsx scripts/keys.ts new /Volumes/KEY-A r1
   npx tsx scripts/keys.ts new /Volumes/KEY-B r2
   npx tsx scripts/keys.ts root --version 1 --key /Volumes/KEY-A/r1.key --key /Volumes/KEY-B/r2.key > plugins-root.json
   ```

2. Keep the two sticks **in two different places** (for example a home safe and a bank deposit
   box). Either key can sign; losing both means a server release with a new root.
3. Commit `plugins-root.json` (public keys only) as `shared/plugins/plugins-root.json` in a pull
   request; it ships in the next server release (a test refuses a root that lists one of the
   vectors' test keys). **Never commit, paste or upload a `.key` file.**

### 2. The store's online key S (yearly)

1. On the same offline machine, make S on a third encrypted stick (its offline copy) and sign
   the key list with either root key:

   ```sh
   npx tsx scripts/keys.ts new /Volumes/KEY-S s-2027
   npx tsx scripts/keys.ts store-keys --version 1 --root /Volumes/KEY-A/r1.key \
       --store /Volumes/KEY-S/s-2027.pub --months 13 > store-keys.json
   ```

   (With Phase 3, add `--entitlement E.pub` for the entitlement key.)

2. Set S's seed (the one line in `s-2027.key`) as the Worker secret **`STORE_SIGNING_KEY`** of
   the store's Workers (story 54.15): `wrangler secret put STORE_SIGNING_KEY`, typed or pasted
   by the owner at the prompt, never in a file, a shell history or a log. The entitlement key
   (Phase 3) is a separate secret, `STORE_ENTITLEMENT_KEY`.
3. Upload `store-keys.json` to the CDN bucket (story 54.15) and copy it to
   `shared/plugins/store-keys-embedded.json` so each release carries it.
4. **Each year**: make the next S, sign a store-keys.json (version + 1) listing both while
   the store re-signs every live package with the new key, then one more without the old key.

### 3. If a key is compromised

- **S:** sign a new `store-keys.json` without it (R1 or R2), upload it, and run the store's
  re-sign job with the new S. CI runs this drill on every change (`store` job).
- **R1 or R2:** the other root key signs `plugins-root.json` version N+1 without it; ship it in
  a server release.

### 4. Cloudflare: the account and the resources

All on the owner's Cloudflare account (Workers Paid, $5 a month, shared with any other Worker
on it). Create, then put each id in `store/wrangler.jsonc` in a pull request:

| Resource            | Name                                                                                                 | Notes                                                                                                                       |
| ------------------- | ---------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| D1 database         | `store-accounts`, `store-catalog`, `store-installs`                                                  | `wrangler d1 create <name>`; their ids replace the `00000000-…` placeholders (the deploy refuses to run with a placeholder) |
| R2 bucket           | `maktaba-plugins`                                                                                    | public, through the custom domain below; turn on object versioning                                                          |
| R2 bucket           | `maktaba-plugins-private`                                                                            | never public; a lifecycle rule deleting `exports/` after 90 days                                                            |
| Queues              | `store-checks`, `store-catalog`, `store-mail`                                                        | `wrangler queues create <name>`                                                                                             |
| Durable Objects     | `AccountGuard`, `IpBucket`, `PairingCode`, `CatalogWriter`                                           | made by the first deploy (the `migrations` block)                                                                           |
| R2 API token        | read and write on `maktaba-plugins-private` only                                                     | for the presigned upload URLs (secrets below)                                                                               |
| API token for CI    | Workers Scripts, D1, R2, Queues, Durable Objects edit, on this account only; **no** DNS or zone edit | the org secret `CLOUDFLARE_API_TOKEN`, with `CLOUDFLARE_ACCOUNT_ID`                                                         |
| WAF rate-limit rule | on `plugins.maktaba.tv/admin*` and `/v1/admin*`                                                      | 60 requests a minute per IP (54.15 §7)                                                                                      |

### 5. DNS

`maktaba.tv` is on Cloudflare. Both names are proxied (orange cloud); neither points at a
server of ours:

- **`plugins.maktaba.tv`**: the Worker `store-web`'s custom domain (`routes` in
  `wrangler.jsonc`; the first deploy creates the record).
- **`plugins-cdn.maktaba.tv`**: the R2 bucket `maktaba-plugins`'s custom domain (R2, the bucket,
  Settings, Custom Domains). Cache everything; the store sets `Cache-Control` on each object
  (signed lists 60 s, packages a year, immutable).
- Optional: a zone API token with Cache Purge only, as the Worker secrets `CF_ZONE_ID` and
  `CF_PURGE_TOKEN`, so a new revocation list is purged from the edge at once (otherwise homes
  see it within the 60 s cache).

### 6. Email

Every message is sent in English and Arabic from the `store-mail` queue. The code speaks to
Postmark's HTTP API (`MAIL_PROVIDER = "postmark"`); another provider is a small change in
`web/mail.ts`. Choose one (README §14 item 10), verify `plugins.maktaba.tv` as a sending domain
(SPF, DKIM and DMARC records it gives), and set its server token as `MAIL_API_KEY`. Until then
`MAIL_PROVIDER = "outbox"` keeps messages in the private bucket and sends nothing.

### 7. Worker secrets

Each typed by the owner at `npx wrangler secret put <NAME>` in `store/`. None is ever in a
file, a log, a commit or a chat.

| Secret                                                      | What                                                                                                                                                                                                                                        |
| ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `STORE_SIGNING_KEY`                                         | the online key S's seed (step 2)                                                                                                                                                                                                            |
| `MFA_ENC_KEY`                                               | 32 random bytes, base64 (`openssl rand -base64 32`): encrypts TOTP secrets at rest                                                                                                                                                          |
| `BOOTSTRAP_ADMIN_TOKEN`                                     | a long random string, used once (step 8), then deleted (`wrangler secret delete`)                                                                                                                                                           |
| `MAIL_API_KEY`                                              | the email provider's token                                                                                                                                                                                                                  |
| `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_ACCOUNT_ID` | the R2 token of step 4 (presigned uploads)                                                                                                                                                                                                  |
| `EXPORT_PUBLIC_KEY`                                         | the public half (SPKI, base64) of an RSA-3072 key the owner keeps offline: `openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out export-private.pem` then `openssl pkey -in export-private.pem -pubout -outform DER \| base64` |
| `CF_ZONE_ID`, `CF_PURGE_TOKEN`                              | optional, step 5                                                                                                                                                                                                                            |

### 8. Opening

The store deploys dark: `SIGNUP_OPEN` and `SUBMISSIONS_OPEN` are `"false"` and the catalog is
empty. To open it:

1. Push a tag `store-v0.1.0` on main: `store-deploy.yml` runs the store's CI, applies the D1
   migrations and moves traffic to the new version gradually.
2. Upload `store-keys.json` (step 2) to `maktaba-plugins`.
3. Set `SIGNUP_OPEN = "true"` (a pull request to `wrangler.jsonc`), sign up, verify the email,
   add a passkey, and call `POST /v1/admin/bootstrap {token}` once with `BOOTSTRAP_ADMIN_TOKEN`:
   that account is the first `store_admin`. Delete the secret afterwards.
4. Grant `store_reviewer` to the reviewers (each with a passkey), then set
   `SUBMISSIONS_OPEN = "true"`. The isolated runner is the GitHub Actions fallback
   (`RUNNER = "github"`); Cloudflare Containers (`check-runner/Dockerfile`) needs the spike of
   README §14 item 11 first, and is not wired.
5. Once the root keys are in a server release (step 1), homes accept the store's signatures.

### Rollback

`npx wrangler rollback` returns to the previous Worker version (migrations are additive);
D1 Time Travel restores data (`docs/runbooks/store-restore.md`). Delisting everything empties
the catalog; homes keep their installed plugins.
