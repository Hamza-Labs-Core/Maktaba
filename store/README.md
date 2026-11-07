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
npm run vectors:check && npm run typecheck && npm test
```

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

The rest of the go-live list (the Cloudflare account and resources, DNS for
`plugins.maktaba.tv` and `plugins-cdn.maktaba.tv`, email sending, the secrets' names, the first
store admin) is added with the store service (story 54.15).
