# Signing and the chain of trust

> **Status: on `main` (Phase 2, story 54.14).** Signing with `mkp`, the home's checks of both
> signatures and the store's key list are built. The store that countersigns is built dark and
> not open yet: until it opens, and until the root keys exist, no file carries a store
> signature a home accepts, and every file you sign yourself is **Unreviewed** at level
> `developer`.

A home must be able to tell, offline, three things about a plugin file: who made it, that
Maktaba reviewed this exact file, and that Maktaba has not since withdrawn it. Plugins have
their own small chain of trust for that, separate from the one Maktaba's releases use.

## Two signatures over one file list

Every package carries `files.json`: the path, sha256 and size of every file in it,
`manifest.toml` included. Both signatures cover that list, so they cover every byte of the
package.

| File | Signed by | Signs |
|---|---|---|
| `signatures/developer.sig` | you, with your key | `{plugin_id, version, files_sha256}` |
| `signatures/store.sig` | the store, after its checks | `{plugin_id, version, files_sha256, developer_key_id, review_level, reviewed_at, capabilities_sha256}` |

`files_sha256` is the sha256 of the canonical bytes of `files.json` (below). The store's
signature names **your key id**, so nobody can re-sign your package with another key and keep
the store's mark, and it names the sha256 of the capabilities it reviewed.

A signature file is `{key_id, sig, signed}`. Yours also carries `public`, your public key: a
key id is the first 16 bytes of the sha256 of the raw public key, in hex, so the file proves
which key signed it. Each signature is Ed25519 over

```text
"maktaba-plugins/v1/" + kind + "\n" + canonical JSON of signed
```

where `kind` is `developer`, `store`, `store-keys`, `catalog`, `revoked`, `revoked-head` or
`entitlement`: a signature made for one kind of file is never valid for another.

## Making and using your key

```sh
mkp keys new                 # a new Ed25519 key; prints its id
mkp keys export-public       # {"id": "...", "public": "..."} for the developer console
mkp pack package/ my.mkplugin --key <id>
```

The private key is kept in `~/.maktaba/keys/<id>.key` (or `$MKP_KEYS_DIR`), readable by you
only, and is never printed. Keep a backup somewhere offline. If it is lost, make a new key and
register it; if it is stolen, revoke it in the console at once (the store then refuses new
uploads signed by it and adds a revocation entry for versions signed only by it).

## Review levels a home records

| Level | What the home found |
|---|---|
| `none` | no signature |
| `developer` | your signature only, valid |
| `automated` | the store's signature after automated checks (an update that added nothing) |
| `reviewed` | the store's signature after a person reviewed it |

`none` and `developer` are **Unreviewed**: a home installs them only with **Allow unreviewed
plugins** on, after the admin types the plugin's name. A file whose signature is present but
does not check out is refused, whatever the setting: `plugin_signature_invalid` names the step
that failed (`store_keys`, `store_sig`, `developer_sig` or `digest`), and
`developer_key_mismatch` says the store's signature names another key.

## The chain

1. **`plugins-root.json`**, the root, is built into every server binary. It lists two root
   keys the owner holds offline; either one can sign. Rotating a root key ships in a server
   release, and the old root signs the new one.
2. **`store-keys.json`** names the store's online keys, each with a `use` (`store` for
   packages and lists, `entitlement` for paid-plugin entitlements, Phase 3), a validity window
   and the list's own expiry (13 months). A root key signs it; a home keeps the newest version
   it has seen and refuses an older one.
3. **`store.sig`** must be made by a key listed for use `store`, valid at `reviewed_at`. A
   key whose window has since ended still vouches for what it signed inside it.
4. **`developer.sig`** must be by the key `store.sig` names.
5. **Every file** must match its sha256 and size in `files.json`.

A home checks this offline, at install. A store-signed file copied on a USB stick installs on
a home that never reached the store: each release carries the store's key list as of its
build.

## The revocation list

The store also signs `revoked.json` (expires 48 hours after it is issued) and a tiny
`revoked-head.json` homes poll every 5 minutes. Each entry names a plugin id, a version range,
optionally a developer key id, an action (`disable`, `warn` or `delist`) and a reason code. A
home refuses a list older than the newest it has seen (`revocation_rollback_refused`) or one
already expired, and uses the newer of the list it stored and the one its release carries. An
expired list never stops a plugin: the Plugins page warns, and installs from the store wait
until a fresh list arrives. Applying the entries (holding a plugin, the warnings) is the
catalog's job, described in [Publishing](publishing.md).

The same holds for `store-keys.json` and the catalog: a home refuses an older version
(`store_keys_rollback_refused`, `catalog_rollback_refused`), and also the newest version it has
seen when its bytes differ from the copy it accepted, since the store never signs two lists with
one version.

Time on a home is the newest of its clock and the newest issue time it has seen, so a clock
set in the past cannot make an old list look fresh.

## Canonical JSON

What every signature covers, byte for byte the same in the home (Go), the SDK (Rust) and the
store (TypeScript):

- object keys sorted by their UTF-8 bytes, never twice, no spaces;
- strings as they are, escaping only `"`, `\` and characters below U+0020 (`\b \f \n \r \t`,
  the rest `\u00xx`);
- integers only, within ±(2^53 − 1); a fraction, an exponent or `-0` is refused;
- a `\u` escape of a surrogate is refused, and so is text that is not UTF-8.

The three implementations are tested on the same vectors,
`shared/plugins/testdata/signatures/`, made by the store's signer.

## If a key is compromised

- **The store's online key:** the owner signs a new `store-keys.json` without it, and the
  store re-signs every live package with the new key in one batch. Homes pick both up on their
  next fetch; anything the removed key signed stops verifying. CI runs this drill with test
  keys on every change to the store.
- **Your key:** the store adds a revocation entry naming your key id (`warn` or `disable`,
  depending on the incident) and refuses new uploads signed by it.
- **A root key:** the other root key signs a new root without it, shipped in a server
  release.
