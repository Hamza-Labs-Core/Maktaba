# Publishing

> **Status: Phase 2 is being built.** On `main`: signing (`mkp keys`, `mkp pack --key`, the
> home's checks of both signatures, [Signing and the chain of trust](trust.md)) and the store
> service itself: store accounts with email verification, MFA (an authenticator app or
> passkeys) and re-authentication, the developer console, signing keys, id prefixes,
> submissions, linking a home, store admin, the signed catalog and revocation list. The store
> is built but **not open yet**: sign-up and submissions are closed until its keys, hosting and
> reviewers are in place. The automated checks and the review queue are built too
> ([Review guidelines](guidelines.md)). The home's catalog and kill switch, and the store
> browser follow in the next changes of Phase 2; paid plugins are Phase 3. This page
> describes the design in
> [Epic 54](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/README.md)
> section 6, which can change.

## Where things live

The store is a service of its own, independent of Maktaba Cloud: its own accounts, database,
deploy, hostname and payments, all on Cloudflare. It shares no accounts, sessions or tables
with the cloud, and keeps working when the cloud is down.

- **The store: `plugins.maktaba.tv`.** The listing pages, buyer accounts, the developer
  console (your account, keys, submissions and payouts), the store API, linking a home
  (`/link`), and review and store admin (`/admin`).
- **Signed files: `plugins-cdn.maktaba.tv`.** Packages, icons, the catalog, the revocation list
  and the store's keys, served from the Cloudflare CDN.
- **Users** browse plugins on `plugins.maktaba.tv` and in their home's web app (Settings,
  Plugins, Browse), which shows only versions that fit their server and platform.
- **Developers** read these docs on maktaba.dev, and publish from the console on
  `plugins.maktaba.tv`.
- **Homes** fetch the catalog, packages and the revocation list straight from
  `plugins-cdn.maktaba.tv` over HTTPS, never through Maktaba Cloud. Signatures, not the
  channel, are what a home trusts. A home links to a store account only to buy paid plugins
  and to rate them, by a code its admin confirms on `plugins.maktaba.tv/link`.

## Your developer account

1. Sign up at `plugins.maktaba.tv` for a **store account** (not a Maktaba Cloud account) and
   verify your email.
2. Turn on multi-factor sign-in (an authenticator app or a passkey). It is required to
   publish, and sensitive actions (keys, submissions, payouts) ask you to sign in again.
3. Accept the developer agreement. A new version must be accepted before your next submission.
4. Choose an **id prefix**: prove a domain with a DNS TXT record
   `maktaba-developer=<value from the console>` (for the prefix `dev.example`, the record is on
   `example.dev`; the store re-checks it monthly, and new submissions under a prefix whose
   record is gone wait until it is back), or take a community prefix
   `tv.maktaba.community.<handle>`.

Sensitive actions (adding or revoking a key, submitting, withdrawing, changing your email,
password or factors, linking a home) need a factor confirmed in the last 5 minutes; otherwise
the API answers `401 reauth_required` and the console asks you to **Confirm it's you**. Five
failed sign-ins pause signing in for 15 minutes (doubling up to a day) and email you. Reviewers
and store admins sign in with a passkey.

### The store API you use

| Route | What |
|---|---|
| `POST /v1/developers {agreement_version}` | become a developer, or accept a new agreement |
| `POST /v1/developers/keys {id, public}` | the output of `mkp keys export-public` |
| `DELETE /v1/developers/keys/{id}` | revoke a key |
| `POST /v1/developers/prefixes {prefix}` or `{community}` | claim a prefix; `…/{prefix}/check` checks the TXT record |
| `POST /v1/submissions {plugin_id, version, package_sha256, package_size, source_size, notes_en, notes_ar}` | start a submission: answers upload URLs for the package and the source archive |
| `POST /v1/submissions/{id}/uploaded` | the uploads are checked (size and sha256) and the checks queued |
| `GET /v1/submissions/{id}` | its status and report |
| `POST /v1/plugins/{id}/withdraw` | delist your plugin |

A version is never submitted twice (`409 version_exists`: bump the version), and every
version needs its source archive.

## Signing keys

```sh
mkp keys new
mkp keys export-public
mkp pack package/ my.mkplugin --key <id>
```

Your key is Ed25519, and the private half never leaves your machine. Register the public half
in the console. You can rotate keys; old signatures stay valid until you revoke the old key.
[Signing and the chain of trust](trust.md) has the formats and every check a home makes.

A package carries two signatures over the same `files.json`:

| File | Signed by | Covers |
|---|---|---|
| `signatures/developer.sig` | you | plugin id, version, the hash of `files.json` |
| `signatures/store.sig` | the store, after checks | the above plus your key id, review level, review date and capabilities |

Because the store signature names your key, nobody else can re-sign your package and keep the
store's mark.

### Review levels a home records

| Level | Meaning |
|---|---|
| `none` | no signature |
| `developer` | your signature only |
| `automated` | store-signed after automated checks (an update that added nothing) |
| `reviewed` | store-signed after a person reviewed it |

### The trust chain

Every server binary embeds the store's root public keys, held offline. They sign a
short-lived list of the store's online keys, which sign packages, the catalog and the
revocation list. A home refuses expired or older copies of these lists (anti-rollback).

## Submitting

```sh
mkp submit
```

Each submission is a package signed with your key, a **source archive** (required for every
version, kept private to reviewers) and release notes in English and Arabic. A listing needs
English **and** Arabic strings. Machine translation is allowed; say so in your notes and the
listing shows "Arabic machine translated".

## Automated checks

They report within about 30 minutes:

| Check | Blocks when |
|---|---|
| Package format and limits | invalid |
| Developer signature, active key, verified prefix | invalid |
| Manifest rules, `maktaba` upper bound present | invalid |
| Import audit | forbidden or undeclared import |
| Capability audit | warns on declared but unused capabilities, and medium-risk ones without a reason |
| Strings in both languages; reserved words in names | missing string or reserved word; URLs warn |
| Malware scan of package and source | a hit |
| Sandbox run of your fixtures and generated events with strict limits | a trap, memory over 80 % of declared, or outputs breaking the rules |
| Diff against your previous version | routes to human review on new capabilities, panels, exports or large jumps |
| Source archive present and, for reproducible toolchains (Rust, TinyGo), builds to the same hash | missing source blocks; otherwise a badge |

## Human review

A person reviews:

- every **first** version;
- any version that adds a capability, a panel or a `plugin-data.read`;
- any version the checks flag;
- any version of a plugin with an open malware or data-misuse report;
- a random 5 % of the rest.

Other versions are approved automatically. Reviewers see the manifest, your reasons for each
capability, the check report, the call trace, outputs on the sample set, the diff and your
source. They approve, reject with reasons from a fixed list, or ask you a question.

Targets: first response within 5 business days for a new plugin, 2 for an update.

### Guidelines

- Ask only for the capabilities you use, and say why for medium-risk ones.
- Do what your listing says.
- No hidden data stores or library fingerprinting.
- No illegal or hateful content.
- Names and icons must not imitate Maktaba or anyone else; `Maktaba`, its Arabic name and
  `official` are reserved.

## The catalog and updates

Homes fetch the catalog every 6 hours while plugins are installed, and when an admin opens
Browse on a catalog over an hour old. When a newer compatible
version appears, the admin sees Update. Auto-update is off by default per plugin; when on,
updates that add no capability apply at night, and updates that add capabilities always wait
for the admin.

## The kill switch

The store publishes a signed revocation list on `plugins-cdn.maktaba.tv`. Every home checks a
small signed head of it every 5 minutes and fetches the full list when it changes (and every
hour in any case), so a `disable` reaches every online home within about 7 minutes, linked
to a store account or not, with no Maktaba Cloud connection. Each server release also carries
the list as of its build.

| Action | On homes |
|---|---|
| `disable` | the plugin is drained and held, admins are told why, and it can only be removed; an admin cannot undo it |
| `warn` | it keeps running with a warning; the admin may disable it |
| `delist` | removed from the catalog; installed copies keep working, without updates |

An expired list does not stop plugins. It shows a warning and blocks new store installs until
a fresh list arrives. If your developer key is compromised, the store revokes it, refuses
uploads signed only by it, and re-reviews countersigned versions.

## Ratings and reports

A store account can rate your plugin once (1 to 5 stars, optional text) when it is linked to
a home that has had the plugin installed for at least 24 hours. You can reply once per review.
Moderation is done by the store's reviewers, on store accounts, never by home admins. Anyone can report a plugin for malware,
data misuse, not working, being misleading, offensive content or copyright. Malware and data
misuse reports are triaged within 24 hours.

Ratings show on your listing page once a plugin has 5 of them. Reviews are plain text: links
and markup show as typed, never as links. A report's reporter hears the outcome by email.
Your listing's words come from your package's string tables at countersign, the keys your
manifest names under `strings` (`name`, `summary`, `description`); when the version's release
notes say the Arabic is machine translated, the Arabic page shows a badge saying so.

## Paid plugins (Phase 3)

- Publishing free plugins is free. Selling needs a one-time $25 developer fee and active
  payouts.
- The developer gets 85 % of each sale and Maktaba keeps 15 %.
- Prices are one-time or yearly, from $1 to $200 or the equivalent. Buyers can get a refund
  within 14 days.
- A listed free plugin cannot become paid; publish a new plugin id instead.
- Plugins are bought on `plugins.maktaba.tv` by a home linked to a store account. Payments
  belong to the store, separate from Maktaba Cloud's billing. The payment provider and seller
  of record are not chosen yet, and it will never be Stripe.
- The home checks a signed entitlement offline and refreshes it daily. Without a valid one the
  plugin waits; its data is never deleted.
- Your plugin never handles payment: no capability touches money, and a plugin cannot show a
  price, paywall or link.
