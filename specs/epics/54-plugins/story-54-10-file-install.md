# Story 54.10 — Install from a file and the unreviewed opt-in

> Epic 54 · Phase 1 · Depends on: 54.07, 54.08, 54.38 · Needed by: 54.13

## Why

Homes are often offline, and developers need to try their own plugins before any store exists.
Plex's lesson (Epic 51 H9) is that "only from our servers" fails offline homes. But a file from
anywhere is exactly how bad code arrives. The answer: a file installs the same way as a store
download when it carries the store's signature; anything else needs a deliberate admin opt-in
and is marked "Unreviewed" wherever it appears. The sandbox, grants and quotas do not change.

## Description

### 1. Three kinds of file

| File carries | Phase | Installs | Badge |
|---|---|---|---|
| store signature + developer signature (54.14) | 2 | always, offline, same as the store | none (or "Reviewed") |
| developer signature only | 1 | only with **Allow unreviewed plugins** on | **Unreviewed** (red) |
| no signature | 1 | only with Allow unreviewed plugins on | **Unreviewed** (red) |
| an unpacked folder via developer mode | 1 | only with Allow unreviewed plugins on | **Development** (amber) |

Before Phase 2 ships, every file is unreviewed. The dialog says so.

### 2. The opt-in

- Settings → Plugins → **Allow unreviewed plugins**, off by default. Turning it on asks for the
  admin's password again (the existing re-auth used for sensitive admin actions) and shows what
  it means:
  - en: "Unreviewed plugins have not been checked by Maktaba. They still can't reach the
    internet or see your files, but they could write wrong data or slow your server. Only
    install plugins from people you trust."
  - ar: «الإضافات غير المراجَعة لم تفحصها مكتبة. لا تستطيع الوصول إلى الإنترنت ولا رؤية ملفاتك، لكنها قد تكتب بيانات خاطئة أو تُبطئ الجهاز الرئيسي. لا تثبّت إلا إضافات من أشخاص تثق بهم.»
- `PUT /api/plugins/policy {allow_unreviewed: true}` is admin-only, re-auth-checked, audited.
- **Turning it off** stops every unreviewed and development plugin, drained, data kept: they
  wait on the gate `unreviewed_disallowed` (54.37 §3), and the API also sets an Epic 51 hold
  on each (`hold_by = "unreviewed-policy"`, audit `component.hold`). Turning it on again lifts
  the gate but not the hold; the admin starts each (`POST /api/components/plugin.<id>/start`
  or the plugin route).

### 3. Installing an unreviewed file

1. Settings → Plugins → **Install from a file** (or `maktaba-server plugins install <file>`).
2. The package is validated (54.01) and audited (54.02) exactly as from the store.
3. The dialog shows the plugin's name, developer key fingerprint (if signed), version, rating
   (54.08), capabilities in Maktaba's words, libraries, and the red Unreviewed badge, and asks
   the admin to **type the plugin's name** to confirm.
4. Install (54.07). Every screen that names it (Plugins page, Queues tiles, logs, components,
   "Added by" labels) shows the badge.

### 4. Developer mode

`maktaba-server plugins dev <folder>` (CLI, admin token or the supervisor's local admin port)
installs from an unpacked folder, watches it, and on change re-validates, re-preloads and
reloads (drain, switch) within 5 s. Only with Allow unreviewed plugins on; marked Development;
never kept after the CLI stops unless `--keep`. The dev loop uses the same host and sandbox.

### 5. CLI parity

`maktaba-server plugins list|show|install|enable|disable|start|stop|update|rollback|remove|grants|dev|policy`
call the same admin routes, for headless homes, the way `maktaba-server components …` passes
through to `maktaba-api components …` (`api/components_cli.go`, Epic 51 F16): the same flags
(`--yes`, `--json`, `--dsn`), the preview and `Continue? [y/N]` before a write, and the same
exit codes (0 ok, 1 refusal, 2 a write with no terminal and no `--yes`, 3 no database).
`maktaba-server components enable plugin.<id>` works too.

## Acceptance criteria

- **Given** Allow unreviewed plugins off,
  **when** an admin installs an unsigned file,
  **then** it is refused with `plugin_unreviewed_disallowed` and the words explain how to turn
  the setting on.
- **Given** the setting on,
  **when** the admin installs it and types its name,
  **then** it installs with the Unreviewed badge on the Plugins page, its Queues tiles and its
  log lines.
- **Given** two unreviewed plugins running,
  **when** the setting is turned off,
  **then** both are held with `unreviewed_disallowed`, their running jobs drain, queues are
  kept.
- **Given** a viewer or a cloud-linked play sign-in,
  **when** it calls `PUT /api/plugins/policy`,
  **then** 403; an admin without re-auth gets `401 reauth_required`.
- **Given** developer mode on a folder,
  **when** the developer rebuilds `plugin.wasm`,
  **then** the new build serves the next job within 5 s.
- **Given** an air-gapped home with the setting on,
  **when** it installs a file,
  **then** no network request is made by the server at all.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (PG, go) | setting off | install unsigned | 400 `plugin_unreviewed_disallowed` |
| T02 | integration (PG, go) | setting on, name typed / wrong name | install | installed / 400 |
| T03 | integration (PG, go+py) | two unreviewed running | setting off | held, drained |
| T04 | role test (go) | policy route | no one / viewer / cloud-linked / admin no re-auth / admin | 401 / 403 / 403 / 401 / 200 |
| T05 | e2e | dev mode, change file | reload | ≤ 5 s |
| T06 | unit (web) | badge everywhere | render | badge on all listed screens, en + ar |
| T07 | integration (go) | air-gapped (no route out) | install | no outbound connection attempted (network namespace in test) |
| T08 | unit (go) | CLI | each subcommand | calls the matching route |

## Edge cases

- **A store-signed file after the developer key was revoked** (Phase 2): refused unless the
  revocation list the home has is older than the signature; then installs with a warning about
  the list's age.
- **The same plugin id installed unreviewed, then offered by the store**: the store version can
  replace it only if signed with the same developer key; otherwise the admin must uninstall
  first (`plugin_key_mismatch`).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/handlers/plugins/policy.go` (new) | §2. |
| `api/internal/plugins/install.go` | Review level from signatures; refuse when disallowed. |
| `api/plugins_cli.go` (new), `cmd/maktaba-server/` pass-through beside `components` | §4, §5. |
| `web/src/pages/Admin/Plugins/InstallFromFile.tsx` (new, with 54.13) | Dialog, typed confirmation. |
| `web/src/components/UnreviewedBadge.tsx` (new) | Badge used on every screen. |
| `web/src/i18n/{en,ar}.json` | Strings, together. |

### Tests

- Integration T01–T03, T07; role T04; e2e T05; unit T06, T08.

### Rollout

Ships with Phase 1. The setting is off on every home.

### Rollback

Release rollback; installed unreviewed plugins stay on disk, unused.

### Dependencies

54.07 (install), 54.08 (routes and roles), 54.38 (the files). Epic 51 51.08 (the CLI pattern).
