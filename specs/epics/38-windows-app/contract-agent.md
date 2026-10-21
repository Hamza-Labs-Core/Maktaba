# Epic 38 — contract: the server at sign-in on Windows (stories 38.2, 38.3, 38.4)

> **Owners:** track C (the shell: `apps/desktop/src-tauri/src/agent.rs` and its Windows module),
> track A (the launcher: `cmd/maktaba-launcher`). **The Mac equivalent:** the LaunchAgent of
> story 36.5 (`apps/desktop/src-tauri/LaunchAgents/`).

On a Mac, launchd runs `Maktaba --server-agent` from a LaunchAgent registered with `SMAppService`.
On Windows the same executable runs from a **sign-in task** of the user. A Windows service is not
used: it would run in session 0, where the user's mapped NAS drives (`O:`, `Y:`) do not exist.

## The task

Registered by the shell when setup finishes (`schtasks /Create /TN "Maktaba Server <SID>" /XML <file>
/F`, the XML written as UTF-16 into `%LOCALAPPDATA%\Maktaba\updates\maktaba-server-task.xml`), removed
by `Maktaba.exe --server-remove` (the uninstaller's hook). `<SID>` is the string SID of the user the
app runs as (`S-1-5-21-…`): one task per user of the PC, the same after a rename of the user. A task
named `\Maktaba Server` (the name before) whose principal is this user is replaced once by this
user's, turned off if it was; another user's is left alone.

| Setting | Value | Why |
|---|---|---|
| Name | `\Maktaba Server <SID>`, in the root folder | one per user; apart from `\Maktaba`, the task `maktaba-server setup --service` makes (38.13 moves such an install into the app). A `\Maktaba\` folder cannot be used: Task Scheduler keeps a folder and a task of one name in one place |
| Trigger | at sign-in of this user, **delay 30 s** | the session's mapped drives reconnect first |
| Watchdog | a time trigger every 5 min, indefinitely | an agent that died is started again within 5 min; while one runs nothing happens (`IgnoreNew`) |
| Principal | this user, `InteractiveToken`, `LeastPrivilege` | the user's session and drives; never elevated (Postgres refuses an administrator) |
| Action | `"<install>\Maktaba.exe" --server-agent`, working directory `%LOCALAPPDATA%\Maktaba` | `Maktaba.exe` is a GUI program: nothing flashes |
| Instances | `IgnoreNew` | a second start while it runs does nothing |
| Time limit | none (`PT0S`) | it runs until sign-out |
| On batteries | starts and keeps running | a laptop is a valid home |
| Restart on failure | not set | Task Scheduler retries only a task that could not start, never an action that exits non-zero or crashes (Microsoft's answer on its forum, and CI shows it: a crashed stand-in with restart on failure set is not started again within 100 s, while the watchdog starts it within its interval). The watchdog covers a crash; the agent restarts the launcher on its own |

Whether it is registered and enabled is read from `schtasks /Query /TN "Maktaba Server <SID>" /XML`
(XML is not localized; the localized table output is never parsed): no task → `not_registered`;
`<Enabled>false</Enabled>` → `disabled` (the user turned it off in Task Scheduler; the tray offers to
turn it on again); else `enabled`.

## `Maktaba.exe --server-agent`

1. Takes the named mutex `Local\Maktaba.Server.Agent`. Already held (another agent runs in this
   session): exits 0 at once.
2. While the pause flag or the install hold holds in this sign-in (below), exits 0 and starts nothing:
   the watchdog never undoes a pause or starts a server whose files an install is replacing. A flag
   from an earlier sign-in is removed. Started less than 30 s after the sign-in, it waits until 30 s
   after it (a stop meanwhile ends it), so the mapped drives are back first.
3. Creates (or opens) the manual-reset event `Local\Maktaba.Server.Stop` and resets it, then checks
   the flags again (a pause set between the two).
4. Opens `%LOCALAPPDATA%\Maktaba\logs\server.log` for append (rotated to `server.log.1` past 50 MB).
5. Starts `<install>\server\bin\maktaba-launcher.exe --app-bundle <install>\server serve` with
   `CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP`, stdout and stderr to the log, in a kill-on-close job
   (when the agent is killed, its tree goes too, so a restarted agent never finds a server on its
   ports). `CREATE_NO_WINDOW` gives the launcher a console without a window, which the server and
   every role inherit: CTRL_BREAK works inside it (checked on Windows 11 26300 with a GUI parent).
6. Waits for the launcher to exit, or for the stop event.
   - The stop event: the launcher sees it too (below) and stops the server; the agent waits for the
     launcher to exit (the launcher's own grace is 60 s; after 90 s the agent ends the job) and
     **exits 0**. The task then shows "Ready"; the watchdog starts it again only once nothing holds
     it (a Pause and an install set their flag before the event). The job stays open until the agent
     exits.
   - The launcher exits 0 without the event (someone stopped it another way): the agent exits 0.
   - The launcher exits non-zero or cannot start: logged, started again after 1 s, doubling to 30 s
     (reset after a minute of running), as the launcher does for the server.

## The launcher's stop event (38.2)

In app-bundle mode on Windows the launcher opens `Local\Maktaba.Server.Stop` (creating it when the
agent did not) and treats it like CTRL_C: it writes its stop file, sends the server CTRL_BREAK, waits
the stop grace (60 s), then kills. Any process of the user's session can ask the server to stop
cleanly with one `SetEvent`; nothing needs a console.

App-bundle mode on Windows: `--app-bundle <dir>` (what the agent passes), else detected when the
launcher's directory is `<install>\server\bin`, `<install>\server\payload.json` exists and
`<install>\Maktaba.exe` exists. Its lock and `launcher.log` live in `%LOCALAPPDATA%\Maktaba\updates\`.

## What the shell does

| Action | How |
|---|---|
| Setup finished | write the task XML, `schtasks /Create … /F` (replacing this user's old-named task), then start it as Resume does |
| Resume / start | clear the pause flag and the install hold; the task disabled → `schtasks /Change /TN … /ENABLE`; an agent still stopping (its mutex held and the stop event set) → wait until its mutex is free (≤ 100 s); then `schtasks /Run`, again up to 3 times 2 s apart until a new agent holds the mutex (Task Scheduler ignores a `/Run` for a moment after the last run ends) |
| Pause | set the pause flag, `SetEvent(Local\Maktaba.Server.Stop)` |
| Restart (after an update, or on request) | the stop event, wait until the agent's mutex is free (≤ 100 s), then start as Resume does |
| Stop server and quit | Pause, then exit the shell |
| Quit (close the window) | exit the shell only; the server keeps running |
| Status | the supervisor's `GET http://127.0.0.1:9103/processes`, every 3 s (as on the Mac); "stopped" vs "paused" vs "starting" also uses the agent's mutex and the pause flag; "not answering" below |
| Open logs | Explorer on `%LOCALAPPDATA%\Maktaba\logs` |

The **pause flag** is the file `%LOCALAPPDATA%\Maktaba\state\server-paused`, holding the sign-in it
was set in (`sign-in <LogonTime>`, the LSA logon time in 100 ns units since 1601). It holds when it
names this sign-in, or was written after this sign-in began (a flag another program wrote, without
the line), or when the sign-in time cannot be read. A paused server stays paused when the window
restarts (an update relaunches it) and through the watchdog, until Resume or the next sign-in (the
agent removes a flag of an earlier sign-in), as `launchctl kill` + `KeepAlive {SuccessfulExit:
false}` behaves on the Mac.

The **install hold** is `%LOCALAPPDATA%\Maktaba\state\server-held`, in the same form: set by
`--server-stop` and before an update's install, so the watchdog cannot start the server while its
files are replaced; cleared by `--server-start`, Resume, setup and the window's start in server mode.

**Not answering.** While the agent's mutex is held but `/processes` has not answered for **3 minutes**,
the status is `not_answering`, with a reason from the end of `server.log` (the launcher's fatal line,
`maktaba-server`'s `Error:` line, a failed serve step or the agent's "cannot start", looking back over
the launcher's last two starts; else "the server does not answer"), cut to 100 characters for the
tray's tooltip, which says "Maktaba has a problem: …". The clock starts again at any answer, a start
or Resume from the app, and a new agent. 3 minutes is the longest a start takes before the
supervisor answers: the bundled Postgres's first-start bootstrap (2 minutes) and up to 10 s for busy
ports.

The **mode** (`mode.json`: server or client, Epic 36) lives in `%LOCALAPPDATA%\com.hamzalabs.maktaba`,
not in the roaming profile: a server is this PC's. One in `%APPDATA%` is moved once (copied, then
removed); a server mode only when this PC registered the task (its XML is in
`%LOCALAPPDATA%\Maktaba\updates`), so another PC of a roaming profile opens setup.

Every program the shell starts (`schtasks.exe`, `explorer.exe`, `maktaba-server.exe setup --json`)
is created with `CREATE_NO_WINDOW`, so no console flashes.

## Installer hooks (38.11)

The NSIS installer (`bundle.windows.nsis.installerHooks`) calls the installed `Maktaba.exe`:

| Hook | Runs | Why |
|---|---|---|
| `NSIS_HOOK_PREINSTALL` | `"$INSTDIR\Maktaba.exe" --server-stop` when it exists | the payload's files are in use while the server runs; an install over them would fail half way |
| `NSIS_HOOK_POSTINSTALL` | `"$INSTDIR\Maktaba.exe" --server-start` | runs the task when it is registered and the server is not paused (a first install does nothing: setup registers it) |
| `NSIS_HOOK_PREUNINSTALL` (not when updating) | `--server-stop`, then `--server-remove` | no task is left pointing at a removed program |

`--server-stop` sets the install hold, then the stop event, and waits for the agent's mutex
(≤ 100 s: the agent's 90 s, then 10 s to end the launcher and exit), exit 0 when the server is stopped
or was not running. `--server-start` clears the install hold, replaces this user's old-named task,
then starts the task as Resume does when it is registered, on and not paused. `--server-remove`
removes this user's task (and its old-named one) even when the stop timed out, then reports that
(exit 1). `--server-start` and `--server-remove` exit 0 when there is nothing to do. The state in
`%LOCALAPPDATA%\Maktaba` is never removed by the uninstaller.

**An older `Maktaba.exe`.** The client-only Windows app (the same `Maktaba.exe`, installed in the same
`%LOCALAPPDATA%\Maktaba`) does not know these flags: it ignores them and opens its window, so an
`ExecWait` on it would hold the install until the user closes it. A server build whose shell
predates the flags is the same. Each hook therefore asks only a `Maktaba.exe` known to take them:

- **The one the installer packs** is decided when the installer is compiled, from the shell's source
  in the same tree (`windows/knows-server-flags.ps1`: the quoted literals `"--server-start"`,
  `"--server-stop"` and `"--server-remove"` in the code, not a comment, of `src-tauri/src/*.rs`),
  which the same `tauri build` compiles into that `Maktaba.exe`. Not from the program: a release
  build need not hold the flags' text (LLVM compiles `arg == "--server-remove"` into a length and
  integer compares). A source that cannot be read fails the build.
- **The one already installed** (before an install, an update and an uninstall): an installer that
  put a flag-taking `Maktaba.exe` there records its size and file time as `MaktabaServerFlags` in the
  app's per-user uninstall key, and the hook asks it only while that still matches the file. A
  client-only install has no record; a client-only `Maktaba.exe` put over a server install no longer
  matches it; an installer whose program predates the flags drops it; the uninstaller deletes the
  key with the rest.

On any other the hook does nothing (there is no server to stop or start).
`tools/release/win-app/test-hooks.sh` compiles the real hooks around stand-in programs that log each
call, each kind of build with a stand-in source of its own (fresh install, reinstall, an update to a
build before the flags, an uninstall of that, a client-only app with no record, a client-only
`Maktaba.exe` over a server install, an update's uninstall, a real uninstall). The installer's check
(`check.sh --installer`) decides from the tree's source as the installer does and checks that against
the program (the installed `Maktaba.exe --server-start` exits by itself only when the source takes the
flags). It installs over a client-only layout whose stand-in ignores the flags and never exits, then
reinstalls and uninstalls, watching each run in a job object (`run-watched.ps1`) that reports every
process it starts. It fails when a hook is still running after 20 s; when the shell takes the flags but
the installer recorded nothing, or the hooks did not run `Maktaba.exe` with the flags each step calls
for; and when the shell takes none but a hook ran it.

## Sign-out and shutdown

At sign-out Windows ends the session's processes. The launcher and the roles get the console's
logoff/shutdown event (Go maps it to SIGTERM, which `serve` and the launcher drain on) but only a few
seconds: Postgres may be ended before its fast shutdown finishes and does crash recovery at the next
start (it is built for that; the nightly dumps stay). Nothing else is lost: the pipeline's claimed
jobs return to the queue when their lease ends.
