# Update attempts: every failure says why

> **Status:** built with the PR that adds this file. Owner's request (2026-10-24, after the Mac
> home's update from 0.3.0-rc.5 to 0.3.0 failed without a word): every update failure shows its
> reason, in plain words, where the person looks: the Updates page, and the desktop app.

## The record

An **attempt** is one try at bringing a machine to a release, whoever made it:

| source | who | what it covers |
|---|---|---|
| `app` | the desktop app's own updater (its badge, its menu, or the home's order) | check, download, signature verification, install, relaunch |
| `server` | a release's first start on a machine (the 38.12 database step, the pipeline's environment) | the dump, the migrations, the restore, `db_ahead`, a release older than the database, the environment |
| `run` | the cluster's runs (Epic 35, `update_runs` / `update_targets`) | as before; the page reads them from the database |

```json
{
  "id": "app-1792735800123",
  "at": "2026-10-24T05:30:00Z",
  "ended_at": "2026-10-24T05:31:12Z",
  "source": "app",
  "trigger": "badge",
  "from": "0.3.0-rc.5",
  "to": "0.3.0",
  "outcome": "failed",
  "step": "install",
  "code": "needs_admin",
  "detail": "the raw error, in English, as the updater or the server said it",
  "fix": "a command or an action that mends it (optional)"
}
```

- `id`: unique per attempt (`app-<unix ms>`; a release's start: `start-<version>`).
- `at`, `ended_at`: RFC 3339, UTC; `ended_at` is left out while the attempt runs.
- `trigger`: `badge`, `menu`, `order` (app); `start` (server); a run's trigger (run).
- `outcome`: `running`, `installed`, `failed`, `put_back` (the release failed to start and the
  database and the previous release are back), `rolled_back`, `cancelled` (the home took its
  order back before the install).
- `to`: the release tried; empty only when the app's own feed could not even be checked (the
  page then says "a new release").
- `step`: `check`, `download`, `verify`, `install`, `relaunch` (app); `dump`, `migrate`,
  `restore`, `venv` (server); a run's step (run).
- `code`: snake_case, from the lists below; the page says each in the reader's language
  (`updates.code.<code>`), and shows `detail` as well.

### Where they are kept

In the machine's updates folder, the folder of the app's order (`app-install.json`): macOS
`~/Library/Application Support/Maktaba/updates`, Windows `%LOCALAPPDATA%\Maktaba\updates`, a
plain server `<install>/updates`. Two files, one writer each, so neither overwrites the other:

- `app-attempts.json`: written by the desktop app;
- `server-attempts.json`: written by the server.

Each is `{"attempts": [...]}`, newest first, at most 20, written whole (a temporary file and a
rename).

### How the page gets them

- The server's update status (`/update/status` on the supervisor, `update.state` on the cluster
  channel) carries `attempts`: both files merged, newest first, at most 10. A node's machine
  agent sends its `update.state` again when either file changes.
- `GET /api/cluster/updates` gives each machine (the home and every node) `attempts`: its own,
  merged with its last finished run (`source: run`), newest first, at most 5.
- The Updates page shows, for each machine, its last attempt: when, from and to, the step it
  stopped at, the reason, the raw detail, and what to do next: Retry for a run, the command for
  a code that has one (`quarantined`, `login_item_unsigned`, held in the page, never a
  machine's text), the fix in the page's words, and the logs. A release that failed to start
  and was put back reads "0.3.0 failed to start: <reason>", then what runs now: the previous
  release again, or, in the desktop app (no release before it to run), its database put back
  as it was and its server stopped until a fix. An attempt at a release the machine has since
  reached (by any way: a later try, a disk image) no longer shows as its current failure.
- Every detail and fix is kept without URLs' queries (a signed download's signature) and cut to
  2 KB, on write and on read, by the server, the app and the home's api alike.
- **The logs.** "See the logs" opens the logs page on the attempt's window (`since`, `until`;
  a node's attempt with `node=<id>`). Beside the api's own lines it shows the machine's files,
  where an update's story is: `server.log` (the supervisor's database step and every role),
  `app.log` (the desktop app's updater) and `launcher.log` (the launcher's events).
  - The supervisor serves them over a window on its loopback admin (`GET /logs/files?files=&since=&until=&limit=`,
    the local token); the api at `GET /api/admin/logs/files` (admin), and a node's over the
    machine channel (`logs.files`, its own agent only, the answer bounded to fit a frame and
    noted in the hub's event log by its size only).
  - A line takes its time from itself (a JSON line's `ts`, an RFC 3339 time at its start,
    slog's `time=`) or from the line before it (the supervisor's `[serve]` lines carry none).
  - "Download the logs" is the admin diagnostics bundle (`/api/admin/logs/export`) of the same
    window, with those files (`files/*.log`; a node's under `node-<id>/`).

## Codes

The desktop app (`source: app`):

| code | when |
|---|---|
| `no_network` | the feed or the download could not be reached (no connection, a name that does not resolve, a timeout) |
| `feed_not_found` | the release's update feed is missing (HTTP 404) |
| `feed_invalid` | the feed answered but is not one the app can read |
| `asset_not_found` | the feed names a download that is missing (HTTP 404) |
| `signature_mismatch` | the download's signature does not match the app's key |
| `needs_admin` | the app's folder needs an administrator's password to change (macOS `/Applications` for a standard user) |
| `not_writable` | the app's files cannot be replaced (permissions) |
| `disk_full` | no room to download or unpack the update |
| `translocated` | macOS runs this copy of the app from a temporary read-only place (opened from Downloads or the disk image): move it to Applications. Checked first, before `needs_admin` |
| `app_management` | macOS refused the app changing its own bundle (System Settings › Privacy & Security › App Management), or the bundle is locked: an ordered install stops here, never at a password prompt |
| `local_build` | the app was built locally (the placeholder updater key): it can verify no release and updates nothing itself; install the release by hand once. No attempt counts against the release and nothing is blocked |
| `installer_not_finished` | Windows: the installer did not finish after the app closed (found at the next start; an unattended home's server is started again on the release it had after 15 minutes) |
| `quarantined` | macOS still marks the app as downloaded from the internet and unchecked |
| `installer_exit` | the Linux package installer (`.deb`, `.rpm`) failed. On Windows the updater starts the installer and exits, so an install that did not take shows at the next start as `relaunch_failed` |
| `server_stop_failed` | the server could not be stopped for the install (Windows) |
| `relaunch_failed` | the app came back on an older release, or did not come back, or (with the server on this machine) the server did not come back on the new release: 3 minutes with no answer or an older one, 30 while it is still starting |
| `login_item_unsigned` | macOS would not load the server's login item from an app that is not code-signed (found when the server does not come back after the relaunch) |
| `previous_app_not_restored` | the update failed and the updater could not put the previous app back in its folder: install again from the release's disk image |
| `check_failed`, `download_failed`, `install_failed` | anything else at that step (the detail says what) |

A release's start (`source: server`): `db_dump_failed`, `db_migrate_failed`, `db_restore_failed`,
`db_ahead`, `db_refused_older`, `venv_failed`, `venv_interrupted`; a rollback's reason when the
release before puts the database back (`start_timeout`, `boot_loop`, the launcher's
`launcher_boot_loop`, `launcher_pending_timeout`, `launcher_refused`, else `start_failed`); and
`start_interrupted`, a start that never finished (the server or the machine stopped, or the
launcher went back from it), settled at the next start: of another release, or of the same one
more than 30 minutes later. A first install that installs is no update and is not recorded.
Its step is `dump`, `migrate`, `venv`, or `start` (the release before put the database back).

A cluster run (`source: run`) carries its error's code, else the target's state
(`failed_prepare`, `needs_person`). Among its codes: `download_failed` (a download that failed:
HTTP, a timeout, the disk; retried, never a verification failure), `verify_*` (only a real
verification failure blocks the release), `older_build` (a release numbered higher but built
before the one running here, on the same major.minor line: passed by, nothing changed, no
notice) and `python_store_stub` (Windows: only the Microsoft Store's `python3` alias where the
pipeline needs a real Python 3.12).

## The desktop app

On a failure at any step the app says why: the menu (or tray) shows "Update to <v> failed:
<reason>" until the next attempt, an OS notification says the same, and, for an install the
person started there, the dialog shows the reason, the updater's own words and the fix (an
install the home ordered goes at the home's go, maybe long after the click: no dialog waits for
nobody, the notification and the menu say it). The order's own `error` keeps its `needs_admin: `
prefix (the server's run reads it); every code is in `app-attempts.json`. A check that finds
nothing newer leaves no attempt; a successful staging of an order neither (the attempt begins at
the home's go). Before it installs, the app records the attempt as running;
when it starts again it settles it: installed when it now is that release or a newer one and,
with the server on this machine, once that server answers on it (until then the attempt runs at
step `relaunch`); `relaunch_failed` or `login_item_unsigned` otherwise. The result of its
restart of the server after an install is noted on the attempt. The menu's failed line goes
once the app runs a newer release (or the same one, reached another way). Its own log, `app.log`, sits beside the server's `server.log` (macOS
`~/Library/Logs/Maktaba`, Windows `%LOCALAPPDATA%\Maktaba\logs`, Linux the app's logs folder).
