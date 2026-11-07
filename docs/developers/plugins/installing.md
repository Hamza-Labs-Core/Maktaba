# Installing and managing plugins

> **Status: on `main`.** Everything on this page is built. Plugins run on a home with
> Postgres, on macOS and Linux; Windows is not supported yet. A home checks a file's signatures
> ([Signing and the chain of trust](trust.md)); until the store opens, every file is
> **unreviewed**.

This is what the admin of a home does with your plugin, and what your plugin sees of it.

## Install from a file

1. **Allow unreviewed plugins.** Settings, Plugins, at the bottom of the page. Turning it on
   asks for the admin's password again (`PUT /api/plugins/policy`, `401 reauth_required`
   without it). Turning it off later stops every unreviewed plugin: they wait with
   `unreviewed_disallowed` and are held until the admin starts each one again.
2. **Install from a file.** The admin chooses the `.mkplugin` (at most 50 MB). The home unpacks
   it with its safe reader, validates the manifest and the settings form, and checks the
   component in the sandboxed host (the import audit and the compile) before anything is
   written. A refusal names its code: `manifest_*`, `package_*`, `plugin_forbidden_import`,
   `settings_secret_forbidden` and the others in the [plugin reference](reference.md).
3. **Type the plugin's name.** An unreviewed plugin installs only after the admin types its
   name, in English or Arabic, as your `i18n` files give it.
4. **Grant and turn on.** The plugin installs off. On its page the admin ticks the capabilities
   it may use and the libraries it may see, then turns it on.

The same steps from a script: `POST /api/plugins/install` (multipart: `grants`, `libraries`,
`all_libraries`, `enable`, `confirm_name`, then `file` last), admin-only.

## Grants and libraries

The admin sees each capability in Maktaba's own words, never yours. **Required** ones must
all be granted before the plugin can be turned on (`400 plugin_required_capability`);
optional ones may stay off. **No library is ticked by default**: until one is, every
library-scoped call answers `plugin_scope`. "All libraries, including new ones" covers
libraries added later. A change applies to your next call: a job that started with older
grants gets `plugin_grants_changed`.

Each plugin shows a **rating** from 1 to 6, advice for the admin: it starts at 6 and drops by
one for each medium-risk capability, by one with no store review, and by one more for reading
another plugin's data.

## Turning it on and off

| Action | What happens to your plugin |
|---|---|
| Turn on | Its stages take jobs. The host process starts if it was idle. |
| Turn off | It stops taking jobs within seconds; running jobs finish or are cancelled with their checkpoint within the drain (at most your `timeout_s`, capped at 120 s); queued jobs stay. **It stays off** through updates, reinstalls, server updates and restores until an admin turns it on. |
| Stop / Start | A hold: the same drain, until Start. |
| Stopped itself | The circuit breaker held it (below). Start lifts it. |

The Components page lists every plugin too, under Plugins, and its switches do the same.

## Updates and rollback

An update is the same install with a newer file of the same plugin id. The previous version
is kept for a rollback. An update that adds a required capability needs the admin to grant it
in the same step. Rolling back to a version with a lower `data_version` than the data's is
refused (`plugin_data_newer`) unless the admin chooses to clear the plugin's data; tags,
chapters and subtitles it wrote are kept.

## Removing it

Remove drops your plugin's queued jobs (the dialog says how many), deletes its files, and
keeps its data, what it wrote and its settings. **Remove and clear what it wrote** also
deletes its data, artifacts and settings, and undoes each change it made to the library where
no one changed that field since.

## Settings

If your manifest names `ui/settings.schema.json`, the plugin's page draws the form from it,
with your words in the admin's language. The home checks every value on save. See
[Settings](queue-and-api.md#settings) for the schema.

## Health and logs

The plugin's page shows its phase and why, jobs done and failed, its last error, its data use
against its quota, and each stage's queue. Your log lines (`log.write`) are kept in a log
segment of their own and open from **Its logs**. Your plugin's queues are the **Plugins** lane
on the Queues page.

The **circuit breaker** stops a plugin that keeps failing: 5 traps, timeouts or memory caps in
10 minutes, more than half of its last 20 jobs failing, or 50 refused API calls in 10 minutes.
It is held, the admins get one notice, and its jobs stay queued until an admin presses Start.

## What it wrote

Every change your plugin makes to the library (a field, a tag, chapters, subtitles, artwork) is
recorded with the value before and after. The admin can select changes and revert them; a
change someone made after your plugin wins.

## Linking a home to a store account

A home needs a **plugin store account** only to rate plugins (and, in Phase 3, to buy them).
Free plugins install without one. The store is its own service on `plugins.maktaba.tv`, with
its own accounts, not Maktaba Cloud's.

On Settings, Plugins, **Store account**, the admin chooses **Link this home**. The home makes
an Ed25519 *home store key* (kept sealed in its secrets store), asks the store for a code and
shows it with a QR code for `plugins.maktaba.tv/link` (the code is never in the link). The admin
signs in to their store account on any device, opens `/link`, types the code, sees the home's
name, version and platform, and confirms. The home polls every 5 seconds and is linked within
seconds; the code lasts 10 minutes, and five wrong entries end it. Afterwards the home signs
each store request with its key (time-limited, each nonce once): there is no password or token
to leak. An account links up to 10 homes; **Unlink** works from either side.

The routes, admin only: `GET /api/plugins/store-account`, `POST …/link`, `POST …/poll`,
`DELETE /api/plugins/store-account`.

An offline home (`maktaba-server serve --offline`, `MAKTABA_OFFLINE=1`) never reaches the
store: linking and unlinking answer `409 store_offline` before any request is made.

## The admin's API

All admin-only (no sign-in 401; a viewer or a cloud-linked play sign-in 403):

| Route | What |
|---|---|
| `GET /api/plugins`, `GET /api/plugins/{id}` | the plugins, one plugin's detail |
| `POST /api/plugins/install`, `POST /api/plugins/{id}/update` | install or update from a file |
| `PUT /api/plugins/{id}/grants` | capabilities, libraries, all libraries |
| `POST /api/plugins/{id}/enable`, `/disable`, `/start`, `/stop` | the switches |
| `POST /api/plugins/{id}/rollback` | to the kept version (`?clear_data=true`) |
| `DELETE /api/plugins/{id}` | remove (`?purge=true`: and clear what it wrote) |
| `GET /api/plugins/{id}/writes`, `POST …/writes/revert` | what it wrote |
| `GET`, `PUT /api/plugins/{id}/settings` | its settings |
| `GET`, `PUT /api/plugins/policy` | allow unreviewed plugins |
