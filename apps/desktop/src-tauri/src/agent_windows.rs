// The server at sign-in on Windows (Epic 38, story 38.3;
// specs/epics/38-windows-app/contract-agent.md).
//
// A sign-in task of the user, "\Maktaba Server <the user's SID>", runs
// `Maktaba.exe --server-agent`. The agent is this GUI executable (nothing
// flashes); it starts <install>\server\bin\maktaba-launcher.exe with
// CREATE_NO_WINDOW, which gives the launcher a console without a window that
// the server and every role inherit, so the launcher's CTRL_BREAK reaches the
// server and the server's reaches its roles. The agent and the launcher both
// wait on the named event Local\Maktaba.Server.Stop: Pause sets it, the
// launcher stops the server cleanly and exits 0, the agent exits 0. A
// launcher that fails is started again with backoff; an agent that dies takes
// its tree with it (a kill-on-close job).
//
// Task Scheduler does not restart an action that exits or crashes (its
// "restart on failure" covers a task that cannot start), so the task has a
// watchdog: a trigger every WATCHDOG_EVERY, ignored while the agent runs
// (IgnoreNew), which starts an agent that died. An agent started while the
// server is paused, or stopped for an install, in this sign-in exits at once,
// so the watchdog never undoes either; the next sign-in ends both.
//
// Every program the shell runs here (schtasks.exe) is created with
// CREATE_NO_WINDOW. schtasks' localized table output is never parsed: only
// its exit code and its /XML output.
use std::ffi::{OsStr, OsString};
use std::io::Write;
use std::os::windows::ffi::OsStrExt;
use std::os::windows::io::AsRawHandle;
use std::os::windows::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use windows_sys::core::PWSTR;
use windows_sys::Win32::Foundation::{
    CloseHandle, GetLastError, LocalFree, ERROR_ACCESS_DENIED, ERROR_ALREADY_EXISTS,
    ERROR_FILE_NOT_FOUND, ERROR_SUCCESS, HANDLE, WAIT_ABANDONED_0, WAIT_OBJECT_0,
};
use windows_sys::Win32::Security::Authentication::Identity::{
    LsaFreeReturnBuffer, LsaGetLogonSessionData, SECURITY_LOGON_SESSION_DATA,
};
use windows_sys::Win32::Security::Authorization::ConvertSidToStringSidW;
use windows_sys::Win32::Security::{
    GetTokenInformation, TokenStatistics, TokenUser, TOKEN_QUERY, TOKEN_STATISTICS, TOKEN_USER,
};
use windows_sys::Win32::System::JobObjects::{
    AssignProcessToJobObject, CreateJobObjectW, JobObjectExtendedLimitInformation,
    SetInformationJobObject, JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JOB_OBJECT_LIMIT_BREAKAWAY_OK,
    JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
};
use windows_sys::Win32::System::Registry::{
    RegDeleteKeyValueW, RegGetValueW, RegSetKeyValueW, HKEY_CURRENT_USER, REG_SZ, RRF_RT_REG_SZ,
};
use windows_sys::Win32::System::Threading::{
    CreateEventW, CreateMutexW, GetCurrentProcess, OpenEventW, OpenMutexW, OpenProcessToken,
    ReleaseMutex, ResetEvent, SetEvent, WaitForMultipleObjects, WaitForSingleObject,
    CREATE_NEW_PROCESS_GROUP, CREATE_NO_WINDOW, EVENT_MODIFY_STATE, INFINITE,
    SYNCHRONIZATION_SYNCHRONIZE,
};
use windows_sys::Win32::UI::Shell::ShellExecuteW;
use windows_sys::Win32::UI::WindowsAndMessaging::SW_SHOWNORMAL;

use super::{Action, BundlePaths, LoginItem};
use crate::run_entry;

/// The sign-in task is named this, a space and the user's SID ("Maktaba
/// Server S-1-5-21-…"), in the root folder: one task per user (a second
/// user's `/Create /F` would otherwise be refused, or replace the first
/// user's task), stable across a rename of the user. Not a `\Maktaba\` folder:
/// Task Scheduler keeps a folder and a task of one name in one place, and
/// `\Maktaba` is the task `maktaba-server setup --service` writes (story 38.13
/// moves such an install into the app).
pub const TASK_PREFIX: &str = "Maktaba Server";
/// The one name every user's task had before: a task of this user under it is
/// moved to this user's name once (replace_old_task).
const OLD_TASK_NAME: &str = "Maktaba Server";
/// The launcher's stop event (cmd/maktaba-launcher/stopevent_windows.go).
const STOP_EVENT: &str = r"Local\Maktaba.Server.Stop";
/// Held by the running agent: one per session, and "is it running?".
const AGENT_MUTEX: &str = r"Local\Maktaba.Server.Agent";
/// How long the agent lets the launcher stop before it ends the launcher's
/// tree: the launcher's 60 s grace, then Postgres's fast shutdown.
const STOP_WAIT: Duration = Duration::from_secs(90);
/// What a stop's waiter allows beyond STOP_WAIT: the agent ending the
/// launcher's tree and exiting.
const EXIT_MARGIN: Duration = Duration::from_secs(10);
const BACKOFF_MIN: Duration = Duration::from_secs(1);
const BACKOFF_MAX: Duration = Duration::from_secs(30);
/// A launcher run this long resets the backoff.
const STABLE_AFTER: Duration = Duration::from_secs(60);
/// How long after a sign-in the server starts: the session's mapped drives
/// (the NAS) reconnect first. The sign-in trigger's delay, and what the agent
/// waits out when another start (the watchdog) comes sooner.
const SIGNIN_DELAY: Duration = Duration::from_secs(30);
/// The watchdog trigger's interval: an agent that died (a crash, or ended in
/// Task Manager) is started again within this. While the agent runs the
/// trigger does nothing (IgnoreNew); while the server is paused it costs one
/// short start of Maktaba.exe that exits at once. A shorter interval adds a
/// start, and a line in the task's history, every minute.
const WATCHDOG_EVERY: Duration = Duration::from_secs(5 * 60);
/// What the install hold says it is.
const HOLD_TEXT: &str = "stopped for an install (Maktaba.exe --server-stop, or an update)";
/// /Run right after a stop is tried this many times, this far apart, until a
/// new agent holds the mutex (run_after_stop).
const RUN_TRIES: u32 = 3;
const RUN_RETRY_AFTER: Duration = Duration::from_secs(2);

fn wide(s: &OsStr) -> Vec<u16> {
    s.encode_wide().chain(Some(0)).collect()
}

/// An owned kernel handle, closed on drop.
struct Owned(HANDLE);

impl Drop for Owned {
    fn drop(&mut self) {
        // SAFETY: the handle came from a successful Create*/Open* call and is
        // closed once.
        unsafe { CloseHandle(self.0) };
    }
}

fn open_event(name: &str, access: u32) -> Option<Owned> {
    let n = wide(OsStr::new(name));
    // SAFETY: n is a NUL-terminated UTF-16 string that outlives the call.
    let h = unsafe { OpenEventW(access, 0, n.as_ptr()) };
    if h.is_null() {
        None
    } else {
        Some(Owned(h))
    }
}

/// Set the stop event. Ok(false): nothing is running to ask.
fn signal_stop_event(name: &str) -> Result<bool, String> {
    let Some(ev) = open_event(name, EVENT_MODIFY_STATE) else {
        return Ok(false);
    };
    // SAFETY: ev is a live event handle opened with EVENT_MODIFY_STATE.
    if unsafe { SetEvent(ev.0) } == 0 {
        return Err(format!(
            "cannot ask the server to stop: {}",
            std::io::Error::last_os_error()
        ));
    }
    Ok(true)
}

/// Whether the stop event is set: a stop was asked and the agent that was
/// asked has not exited yet (the next agent resets it).
fn stop_event_set(name: &str) -> bool {
    let Some(ev) = open_event(name, SYNCHRONIZATION_SYNCHRONIZE) else {
        return false;
    };
    // SAFETY: ev is a live event handle opened with SYNCHRONIZE; a zero
    // timeout only reads its state.
    unsafe { WaitForSingleObject(ev.0, 0) == WAIT_OBJECT_0 }
}

/// Whether an agent of this session holds the mutex.
fn mutex_held(name: &str) -> bool {
    let n = wide(OsStr::new(name));
    // SAFETY: as in open_event.
    let h = unsafe { OpenMutexW(SYNCHRONIZATION_SYNCHRONIZE, 0, n.as_ptr()) };
    if h.is_null() {
        return false;
    }
    drop(Owned(h));
    true
}

/// Whether a mutex of this name exists in this session, even one this process may not open
/// (an installer run as an administrator made it).
fn mutex_exists(name: &str) -> bool {
    let n = wide(OsStr::new(name));
    // SAFETY: as in open_event.
    let h = unsafe { OpenMutexW(SYNCHRONIZATION_SYNCHRONIZE, 0, n.as_ptr()) };
    if h.is_null() {
        // SAFETY: reads this thread's last error, right after the call that set it.
        return unsafe { GetLastError() } == ERROR_ACCESS_DENIED;
    }
    drop(Owned(h));
    true
}

/// Wait until the agent of this session has exited (its mutex is released or
/// abandoned), up to `timeout`. True when no agent runs (any more).
fn wait_mutex_free(name: &str, timeout: Duration) -> bool {
    let n = wide(OsStr::new(name));
    // SAFETY: as in open_event.
    let h = unsafe { OpenMutexW(SYNCHRONIZATION_SYNCHRONIZE, 0, n.as_ptr()) };
    if h.is_null() {
        return true;
    }
    let m = Owned(h);
    let ms = u32::try_from(timeout.as_millis()).unwrap_or(u32::MAX - 1);
    // SAFETY: m is a live mutex handle opened with SYNCHRONIZE.
    let r = unsafe { WaitForSingleObject(m.0, ms) };
    if r == WAIT_OBJECT_0 || r == WAIT_ABANDONED_0 {
        // the wait made us its owner: give it back at once
        // SAFETY: this thread owns the mutex after the successful wait.
        unsafe { ReleaseMutex(m.0) };
        return true;
    }
    false
}

/// Whether the server agent runs in this session.
pub fn agent_alive() -> Option<bool> {
    Some(mutex_held(AGENT_MUTEX))
}

// --- this user and this sign-in ------------------------------------------------

/// This process's token, for reading.
fn process_token() -> Option<Owned> {
    let mut h: HANDLE = std::ptr::null_mut();
    // SAFETY: the pseudo handle of this process; h receives a new handle.
    if unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut h) } == 0 {
        return None;
    }
    Some(Owned(h))
}

/// The user this process runs as, as a SID string (S-1-5-21-…).
pub fn user_sid() -> Option<String> {
    let token = process_token()?;
    let mut len = 0u32;
    // SAFETY: a size query: no buffer, len receives the size needed.
    unsafe { GetTokenInformation(token.0, TokenUser, std::ptr::null_mut(), 0, &mut len) };
    if len == 0 {
        return None;
    }
    // u64s: TOKEN_USER holds a pointer, so the buffer is 8-aligned
    let mut buf = vec![0u64; (len as usize).div_ceil(8)];
    // SAFETY: buf holds at least len bytes.
    let ok =
        unsafe { GetTokenInformation(token.0, TokenUser, buf.as_mut_ptr().cast(), len, &mut len) };
    if ok == 0 {
        return None;
    }
    // SAFETY: the call filled buf with a TOKEN_USER whose Sid points into buf.
    let sid = unsafe { (*buf.as_ptr().cast::<TOKEN_USER>()).User.Sid };
    let mut s: PWSTR = std::ptr::null_mut();
    // SAFETY: sid is valid while buf lives; s receives a LocalAlloc'd string.
    if unsafe { ConvertSidToStringSidW(sid, &mut s) } == 0 || s.is_null() {
        return None;
    }
    // SAFETY: s is a NUL-terminated UTF-16 string, freed once after reading.
    let text = unsafe {
        let n = (0..).take_while(|&i| *s.add(i) != 0).count();
        let text = String::from_utf16_lossy(std::slice::from_raw_parts(s, n));
        LocalFree(s.cast());
        text
    };
    Some(text)
}

/// When this sign-in began, in 100 ns units since 1601 (its logon session's
/// LogonTime): what makes a pause "this sign-in's". None when Windows does not
/// say.
pub fn sign_in_time() -> Option<u64> {
    let token = process_token()?;
    let mut st = TOKEN_STATISTICS::default();
    let mut len = 0u32;
    // SAFETY: st is a TOKEN_STATISTICS of the size given.
    let ok = unsafe {
        GetTokenInformation(
            token.0,
            TokenStatistics,
            (&mut st as *mut TOKEN_STATISTICS).cast(),
            std::mem::size_of::<TOKEN_STATISTICS>() as u32,
            &mut len,
        )
    };
    if ok == 0 {
        return None;
    }
    let mut out = std::mem::MaybeUninit::<*mut SECURITY_LOGON_SESSION_DATA>::uninit();
    // SAFETY: the logon id is this process's own session (no privilege
    // needed); on success out receives an LSA buffer.
    if unsafe { LsaGetLogonSessionData(&st.AuthenticationId, out.as_mut_ptr()) } != 0 {
        return None;
    }
    // SAFETY: the call succeeded, so it wrote out.
    let data = std::ptr::NonNull::new(unsafe { out.assume_init() })?;
    // SAFETY: data is a live LSA buffer, freed once after reading.
    let logon = unsafe {
        let t = data.as_ref().LogonTime;
        LsaFreeReturnBuffer(data.as_ptr().cast());
        t
    };
    u64::try_from(logon).ok().filter(|&t| t > 0)
}

/// A time in 100 ns units since 1601 (a FILETIME, as LogonTime is).
fn filetime(t: SystemTime) -> Option<u64> {
    const UNIX_EPOCH_AS_FILETIME: u64 = 116_444_736_000_000_000;
    let d = t.duration_since(UNIX_EPOCH).ok()?;
    let ticks = d.as_secs() * 10_000_000 + u64::from(d.subsec_nanos() / 100);
    Some(UNIX_EPOCH_AS_FILETIME + ticks)
}

/// How long an agent started now must still wait for SIGNIN_DELAY after the
/// sign-in (zero when it is later, or when the sign-in's time is unknown).
fn signin_wait(signin: Option<u64>, now: Option<u64>, delay: Duration) -> Duration {
    let (Some(signin), Some(now)) = (signin, now) else {
        return Duration::ZERO;
    };
    let since = Duration::from_nanos(now.saturating_sub(signin).saturating_mul(100));
    delay.saturating_sub(since)
}

// --- the pause flag and the install hold ---------------------------------------

/// A flag's text: what set it, and the sign-in it belongs to.
pub fn flag_text(what: &str) -> String {
    flag_text_for(what, sign_in_time())
}

fn flag_text_for(what: &str, signin: Option<u64>) -> String {
    match signin {
        Some(t) => format!("{what}\nsign-in {t}\n"),
        None => format!("{what}\n"),
    }
}

/// The sign-in a flag's text names.
fn flag_sign_in(text: &str) -> Option<u64> {
    text.lines()
        .find_map(|l| l.strip_prefix("sign-in ")?.trim().parse().ok())
}

/// Whether a flag holds in the sign-in that began at `signin`: one set in
/// this sign-in (it names it, or was written after it began) holds until it is
/// cleared; one from an earlier sign-in was ended by this one. When the
/// sign-in is unknown it holds: a pause is never undone on a guess.
fn holds(recorded: Option<u64>, written: Option<u64>, signin: Option<u64>) -> bool {
    let Some(signin) = signin else {
        return true;
    };
    recorded == Some(signin) || written.is_some_and(|w| w >= signin)
}

/// Whether the flag file at `path` holds in this sign-in.
pub fn flag_holds(path: &Path) -> bool {
    flag_holds_in(path, sign_in_time())
}

fn flag_holds_in(path: &Path, signin: Option<u64>) -> bool {
    let text = match std::fs::read(path) {
        Ok(b) => String::from_utf8_lossy(&b).into_owned(),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return false,
        // there but unreadable: keep it
        Err(_) => return true,
    };
    let written = std::fs::metadata(path)
        .and_then(|m| m.modified())
        .ok()
        .and_then(filetime);
    holds(flag_sign_in(&text), written, signin)
}

/// The install hold: <state>\state\server-held while an install replaces the
/// payload (`--server-stop`, an update), so the watchdog does not start the
/// server in the middle. `--server-start`, Resume, setup and the window's
/// start end it, as does the next sign-in.
fn hold_path() -> Option<PathBuf> {
    super::state_root().map(|r| r.join("state").join("server-held"))
}

fn set_hold() {
    let Some(p) = hold_path() else { return };
    if let Some(dir) = p.parent() {
        let _ = std::fs::create_dir_all(dir);
    }
    let _ = std::fs::write(&p, flag_text(HOLD_TEXT));
}

fn clear_hold() {
    if let Some(p) = hold_path() {
        let _ = std::fs::remove_file(p);
    }
}

/// An install hold older than this, with no installer running, is an install that did not
/// finish: the installer the app started as it closed failed or was cancelled, so its
/// POSTINSTALL hook (`--server-start`) never ended the hold. The watchdog's next start
/// ends it itself.
const HOLD_STALE: Duration = Duration::from_secs(15 * 60);

/// An install hold this old ends even while an installer still runs (one that hangs).
const HOLD_MAX: Duration = Duration::from_secs(2 * 60 * 60);

/// The installer's mutex: installer-hooks.nsh's PREINSTALL makes it, and it lives as long
/// as the installer runs (its handle closes with the installer). A long install (a slow
/// disk, an antivirus scanning every file) keeps its hold while it runs.
const INSTALLER_MUTEX: &str = "MaktabaInstallerRunning";

/// Whether an install hold that holds in this sign-in, written `age` ago, is stale: older
/// than HOLD_STALE with no installer running, or older than HOLD_MAX whatever runs.
fn hold_stale(holds: bool, age: Option<Duration>, installer_runs: bool) -> bool {
    holds && age.is_some_and(|a| a >= HOLD_MAX || (a >= HOLD_STALE && !installer_runs))
}

/// The watchdog's start of a server an install left held for longer than HOLD_STALE (the
/// home would stay down until someone opened the window or signed in again): the hold
/// ends, the app's update attempt is recorded failed (installer_not_finished), the home's
/// order says so, and the server starts again on the release it had. Best effort.
fn release_stale_hold() {
    let Some(p) = hold_path() else { return };
    let age = std::fs::metadata(&p)
        .and_then(|m| m.modified())
        .ok()
        .and_then(|t| SystemTime::now().duration_since(t).ok());
    if !hold_stale(flag_holds(&p), age, mutex_exists(INSTALLER_MUTEX))
        || std::fs::remove_file(&p).is_err()
    {
        return;
    }
    let detail = format!(
        "the installer the app started as it closed did not finish within {} minutes (it \
         failed, or was cancelled): the server starts again on the release it had",
        HOLD_STALE.as_secs() / 60
    );
    crate::attempts::log(&format!("install hold ended: {detail}"));
    let Some(root) = super::state_root() else {
        return;
    };
    let rec = crate::attempts::path(&root);
    if let Some(a) = crate::attempts::newest(&rec).filter(|a| a.outcome == crate::attempts::RUNNING)
    {
        crate::attempts::fail(
            &rec,
            &a.id,
            &crate::attempts::Failure::new(
                crate::attempts::STEP_INSTALL,
                crate::attempts::INSTALLER_NOT_FINISHED,
                detail.clone(),
            ),
        );
    }
    let order = crate::autoinstall::request_path(&root);
    if let Some(r) = crate::autoinstall::read(&order).filter(|r| {
        matches!(
            r.state.as_str(),
            crate::autoinstall::GO | crate::autoinstall::INSTALLING
        )
    }) {
        let _ = crate::autoinstall::write_state(
            &order,
            &r,
            crate::autoinstall::FAILED,
            &crate::autoinstall::order_error(crate::attempts::INSTALLER_NOT_FINISHED, &detail),
        );
    }
}

// --- the task ------------------------------------------------------------------

fn xml_escape(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

fn xml_unescape(s: &str) -> String {
    s.replace("&lt;", "<")
        .replace("&gt;", ">")
        .replace("&quot;", "\"")
        .replace("&apos;", "'")
        .replace("&amp;", "&")
}

/// An XML duration (PT5M, PT30S).
fn xml_duration(d: Duration) -> String {
    let s = d.as_secs();
    if s > 0 && s % 60 == 0 {
        format!("PT{}M", s / 60)
    } else {
        format!("PT{s}S")
    }
}

/// This user's task name.
fn task_name_for(sid: &str) -> String {
    format!("{TASK_PREFIX} {sid}")
}

/// This user's task name: "Maktaba Server <SID>".
fn task_name() -> Result<String, String> {
    user_sid()
        .map(|sid| task_name_for(&sid))
        .ok_or_else(|| "cannot read this user's SID".to_string())
}

/// The sign-in task's XML (contract-agent's table): `command args` at the
/// sign-in of `user` (DOMAIN\name; None: whoever registers it), SIGNIN_DELAY
/// late so the session's mapped drives are back, and again every `watchdog`
/// (the watchdog trigger: ignored while it runs), as that user with least
/// privilege, one at a time, no time limit.
pub fn task_xml(
    name: &str,
    command: &Path,
    args: &str,
    workdir: &Path,
    user: Option<&str>,
    watchdog: Duration,
) -> String {
    let user_id = user
        .map(|u| format!("\n      <UserId>{}</UserId>", xml_escape(u)))
        .unwrap_or_default();
    format!(
        r#"<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Maktaba: runs the Maktaba server at sign-in (the app's server agent), and starts it again within {every} minutes when it stopped without a pause. Pause and resume it from the Maktaba tray icon.</Description>
    <URI>\{name}</URI>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>{user_id}
      <Delay>{delay}</Delay>
    </LogonTrigger>
    <TimeTrigger>
      <Repetition>
        <Interval>{watchdog}</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
      <StartBoundary>2000-01-01T00:00:00</StartBoundary>
      <Enabled>true</Enabled>
    </TimeTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">{user_id}
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>{command}</Command>
      <Arguments>{args}</Arguments>
      <WorkingDirectory>{workdir}</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
"#,
        every = WATCHDOG_EVERY.as_secs() / 60,
        name = xml_escape(name),
        delay = xml_duration(SIGNIN_DELAY),
        watchdog = xml_duration(watchdog),
        command = xml_escape(&command.display().to_string()),
        args = xml_escape(args),
        workdir = xml_escape(&workdir.display().to_string()),
    )
}

/// The text of the first `<tag>…</tag>` (or `<tag attr="…">…</tag>`) inside
/// `xml`.
fn element<'a>(xml: &'a str, tag: &str) -> Option<&'a str> {
    let open = format!("<{tag}");
    let close = format!("</{tag}>");
    let mut from = 0;
    loop {
        let at = from + xml[from..].find(&open)?;
        let after = at + open.len();
        // `<Settings>` or `<Principal id="…">`, not `<SettingsX>` or `<Tag/>`
        match xml[after..].chars().next()? {
            '>' | ' ' | '\t' | '\r' | '\n' => {
                let start = after + xml[after..].find('>')? + 1;
                let end = start + xml[start..].find(&close)?;
                return Some(&xml[start..end]);
            }
            _ => from = after,
        }
    }
}

/// What `schtasks /Query /TN … /XML` says about the task, for this app at
/// `exe`: a task that runs another executable (an install moved or left
/// behind) is not this app's login item (register writes it again).
pub fn parse_task(xml: &str, exe: &Path) -> LoginItem {
    let command = element(xml, "Exec")
        .and_then(|e| element(e, "Command"))
        .map(|c| xml_unescape(c.trim()).trim_matches('"').to_string());
    let ours = command.is_some_and(|c| c.eq_ignore_ascii_case(&exe.display().to_string()));
    if !ours {
        return LoginItem::NotRegistered;
    }
    if task_enabled(xml) == Some(false) {
        LoginItem::RequiresApproval
    } else {
        LoginItem::Enabled
    }
}

/// The task's own Enabled (turned off in Task Scheduler), not a trigger's.
fn task_enabled(xml: &str) -> Option<bool> {
    let v = element(xml, "Settings").and_then(|s| element(s, "Enabled"))?;
    Some(!v.trim().eq_ignore_ascii_case("false"))
}

/// Whether a task's XML runs as this user: its principal's UserId is the SID,
/// DOMAIN\name or the bare name.
fn runs_as(xml: &str, sid: &str, user: Option<&str>) -> bool {
    let Some(id) = element(xml, "Principal").and_then(|p| element(p, "UserId")) else {
        return false;
    };
    let id = xml_unescape(id.trim());
    id.eq_ignore_ascii_case(sid)
        || user.is_some_and(|u| {
            id.eq_ignore_ascii_case(u)
                || u.rsplit('\\')
                    .next()
                    .is_some_and(|n| id.eq_ignore_ascii_case(n))
        })
}

/// UTF-16LE with a byte-order mark (what Task Scheduler reads from /XML).
fn utf16_file(s: &str) -> Vec<u8> {
    let mut out = vec![0xFF, 0xFE];
    for u in s.encode_utf16() {
        out.extend_from_slice(&u.to_le_bytes());
    }
    out
}

fn schtasks(args: &[&OsStr]) -> Result<std::process::Output, String> {
    let system = std::env::var_os("SystemRoot").unwrap_or_else(|| OsString::from(r"C:\Windows"));
    Command::new(Path::new(&system).join("System32").join("schtasks.exe"))
        .args(args)
        .stdin(Stdio::null())
        .creation_flags(CREATE_NO_WINDOW)
        .output()
        .map_err(|e| format!("schtasks: {e}"))
}

fn schtasks_ok(args: &[&OsStr]) -> Result<(), String> {
    let out = schtasks(args)?;
    if out.status.success() {
        return Ok(());
    }
    let msg = String::from_utf8_lossy(&out.stderr);
    let msg = if msg.trim().is_empty() {
        String::from_utf8_lossy(&out.stdout)
    } else {
        msg
    };
    Err(format!("schtasks: {}", msg.trim()))
}

fn os(s: &str) -> &OsStr {
    OsStr::new(s)
}

fn current_exe() -> Result<PathBuf, String> {
    std::env::current_exe().map_err(|e| format!("cannot find the app executable: {e}"))
}

/// A task's XML, when it is there (and this user may read it).
fn query_xml(name: &str) -> Option<String> {
    match schtasks(&[os("/Query"), os("/TN"), os(name), os("/XML")]) {
        Ok(out) if out.status.success() => Some(String::from_utf8_lossy(&out.stdout).into_owned()),
        _ => None,
    }
}

/// The task's state for this app (registered, turned off, not there).
pub fn login_item() -> LoginItem {
    let (Ok(exe), Ok(name)) = (current_exe(), task_name()) else {
        return LoginItem::NotRegistered;
    };
    query_xml(&name)
        .map(|xml| parse_task(&xml, &exe))
        .unwrap_or(LoginItem::NotRegistered)
}

/// This user, as Task Scheduler names them (DOMAIN\name).
fn task_user() -> Option<String> {
    let name = std::env::var("USERNAME").ok().filter(|n| !n.is_empty())?;
    Some(match std::env::var("USERDOMAIN") {
        Ok(d) if !d.is_empty() => format!("{d}\\{name}"),
        _ => name,
    })
}

/// Where register writes the task's XML.
fn task_file(root: &Path) -> PathBuf {
    root.join("updates").join("maktaba-server-task.xml")
}

/// Whether this PC registered the server's task for this user (setup ran
/// here): the XML register writes into %LOCALAPPDATA%, which a roaming
/// profile does not carry.
pub fn registered_here() -> bool {
    super::state_root().is_some_and(|r| task_file(&r).is_file())
}

/// Write the task `name` for this app (/F: over one that runs another
/// executable, or turned off).
fn create_task(name: &str) -> Result<(), String> {
    let exe = current_exe()?;
    let root = super::state_root().ok_or("cannot find %LOCALAPPDATA%")?;
    let file = task_file(&root);
    if let Some(dir) = file.parent() {
        std::fs::create_dir_all(dir)
            .map_err(|e| format!("cannot create {}: {e}", dir.display()))?;
    }
    let xml = task_xml(
        name,
        &exe,
        super::AGENT_FLAG,
        &root,
        task_user().as_deref(),
        WATCHDOG_EVERY,
    );
    std::fs::write(&file, utf16_file(&xml))
        .map_err(|e| format!("cannot write {}: {e}", file.display()))?;
    schtasks_ok(&[
        os("/Create"),
        os("/TN"),
        os(name),
        os("/XML"),
        file.as_os_str(),
        os("/F"),
    ])
}

/// Remove a task; one that is not there is not an error.
fn delete_task(name: &str) -> Result<(), String> {
    match schtasks_ok(&[os("/Delete"), os("/TN"), os(name), os("/F")]) {
        Err(e) if query_xml(name).is_some() => Err(e),
        _ => Ok(()),
    }
}

/// This user's task under the old shared name, when there is one: its XML.
/// Another user's task of that name is theirs.
fn old_task_of_this_user(sid: &str) -> Option<String> {
    query_xml(OLD_TASK_NAME).filter(|xml| runs_as(xml, sid, task_user().as_deref()))
}

/// A task of this user under the old shared name `\Maktaba Server` (before
/// each user had their own): register this user's task in its place (turned
/// off if that one was) and remove it. Once: afterwards there is none. True
/// when it replaced one.
pub fn replace_old_task() -> Result<bool, String> {
    let sid = user_sid().ok_or("cannot read this user's SID")?;
    let Some(old) = old_task_of_this_user(&sid) else {
        return Ok(false);
    };
    let name = task_name_for(&sid);
    if query_xml(&name).is_none() {
        create_task(&name)?;
        if task_enabled(&old) == Some(false) {
            schtasks_ok(&[os("/Change"), os("/TN"), os(&name), os("/DISABLE")])?;
        }
    }
    delete_task(OLD_TASK_NAME)?;
    Ok(true)
}

/// Register the sign-in task for this app (again: /F replaces one that runs
/// another executable) and start the server.
pub fn register() -> Result<(), String> {
    let name = task_name()?;
    // this user's task of the old name would run a second set of triggers;
    // one that cannot be moved is no reason not to register this one
    let _ = replace_old_task();
    create_task(&name)?;
    // the tray at sign-in too; one that cannot be written is no reason not to
    // run the server (the app's next start tries again)
    if let Err(e) = sync_autostart() {
        crate::attempts::log(&format!("the tray at sign-in: {e}"));
    }
    clear_hold();
    start_task(&name, AGENT_MUTEX, STOP_EVENT)
}

fn run_task(name: &str) -> Result<(), String> {
    schtasks_ok(&[os("/Run"), os("/TN"), os(name)])
}

/// Run the task once the agent that is stopping (a Pause a moment ago) has
/// exited: while it runs, Task Scheduler ignores /Run (IgnoreNew), and it
/// would then exit and leave the server stopped. With no agent at all the
/// /Run is asked again too: one that exited a moment ago (its mutex free, its
/// process not yet gone) makes Task Scheduler ignore the first.
fn start_task(name: &str, mutex: &str, stop: &str) -> Result<(), String> {
    if !mutex_held(mutex) {
        return run_after_stop(name, mutex);
    }
    if !stop_event_set(stop) {
        return run_task(name); // an agent runs: nothing to start (IgnoreNew)
    }
    let limit = STOP_WAIT + EXIT_MARGIN;
    if !wait_mutex_free(mutex, limit) {
        return Err(format!(
            "the server is still stopping after {} s",
            limit.as_secs()
        ));
    }
    run_after_stop(name, mutex)
}

/// /Run right after an agent stopped. Its mutex is free a moment before its
/// process has gone, and until then Task Scheduler still ignores /Run, so it
/// is asked again until a new agent holds the mutex (RUN_TRIES times,
/// RUN_RETRY_AFTER apart: a start of Maktaba.exe takes well under that).
fn run_after_stop(name: &str, mutex: &str) -> Result<(), String> {
    run_task(name)?;
    for _ in 1..RUN_TRIES {
        if wait_mutex_held(mutex, RUN_RETRY_AFTER) {
            return Ok(());
        }
        // the first /Run was accepted; this one may meet an agent still
        // starting, which is no failure
        let _ = run_task(name);
    }
    Ok(())
}

/// Wait up to `d` for an agent to hold the mutex.
fn wait_mutex_held(name: &str, d: Duration) -> bool {
    let deadline = Instant::now() + d;
    loop {
        if mutex_held(name) {
            return true;
        }
        if Instant::now() >= deadline {
            return false;
        }
        std::thread::sleep(Duration::from_millis(100));
    }
}

/// Stop the server and remove the sign-in task and the tray's Run value
/// (client mode, the uninstaller). The task is removed even when the server
/// does not stop in time (it would run a program that is about to be
/// removed); that error is reported after.
pub fn unregister() -> Result<(), String> {
    let stopped = stop_and_wait();
    let removed = (|| {
        let unrun = remove_autostart();
        let sid = user_sid().ok_or("cannot read this user's SID")?;
        delete_task(&task_name_for(&sid))?;
        if old_task_of_this_user(&sid).is_some() {
            delete_task(OLD_TASK_NAME)?;
        }
        if let Some(root) = super::state_root() {
            let _ = std::fs::remove_file(task_file(&root));
        }
        unrun
    })();
    match (stopped, removed) {
        (Err(s), Err(r)) => Err(format!("{s}; {r}")),
        (Err(e), Ok(())) | (Ok(()), Err(e)) => Err(e),
        (Ok(()), Ok(())) => Ok(()),
    }
}

/// The task turned off in Task Scheduler: turn it on and start the server.
pub fn open_login_items_settings() {
    let _ = control(Action::Resume);
}

/// Ask the server to stop and wait until the agent has exited.
fn stop_and_wait() -> Result<(), String> {
    signal_stop_event(STOP_EVENT)?;
    let limit = STOP_WAIT + EXIT_MARGIN;
    if wait_mutex_free(AGENT_MUTEX, limit) {
        Ok(())
    } else {
        Err(format!(
            "the server did not stop within {} s",
            limit.as_secs()
        ))
    }
}

/// Stop the server for an install (an update, the installer's PREINSTALL
/// hook) and wait until the agent has exited: the payload's files are in use
/// while it runs. The install hold keeps the watchdog from starting it until
/// the install is over (`--server-start`, the window's next start, Resume).
pub fn stop_for_install() -> Result<(), String> {
    set_hold();
    stop_and_wait()
}

/// Pause, resume or restart the server (contract-agent's table). The pause
/// flag is the shell's to keep (it decides when to set it).
pub fn control(action: Action) -> Result<(), String> {
    match action {
        Action::Pause => signal_stop_event(STOP_EVENT).map(|_| ()),
        Action::Resume => {
            let name = task_name()?;
            clear_hold();
            if login_item() == LoginItem::RequiresApproval {
                schtasks_ok(&[os("/Change"), os("/TN"), os(&name), os("/ENABLE")])?;
            }
            start_task(&name, AGENT_MUTEX, STOP_EVENT)
        }
        Action::Restart => {
            let name = task_name()?;
            clear_hold();
            stop_and_wait()?;
            run_after_stop(&name, AGENT_MUTEX)
        }
    }
}

/// The installer's hooks (`--server-start`, `--server-stop`,
/// `--server-remove`): an exit code for NSIS; problems go to the server log.
pub fn installer_hook(flag: &str) -> i32 {
    let mut problems = Vec::new();
    let result = match flag {
        super::START_FLAG => {
            // the install is over
            clear_hold();
            if let Err(e) = replace_old_task() {
                problems.push(format!("cannot move the old sign-in task: {e}"));
            }
            // an update of a PC set up to run the server: the tray at sign-in (an
            // update from a release before it, which no window opens after)
            if login_item() != LoginItem::NotRegistered {
                if let Err(e) = sync_autostart() {
                    problems.push(format!("the tray at sign-in: {e}"));
                }
            }
            if super::paused_flag() || login_item() != LoginItem::Enabled {
                Ok(()) // a first install (setup registers it) or a paused server
            } else {
                task_name().and_then(|name| start_task(&name, AGENT_MUTEX, STOP_EVENT))
            }
        }
        super::STOP_FLAG => stop_for_install(),
        super::REMOVE_FLAG => unregister(),
        _ => Err(format!("unknown flag {flag}")),
    };
    let code = match result {
        Ok(()) => 0,
        Err(e) => {
            problems.push(e);
            1
        }
    };
    if !problems.is_empty() {
        if let Ok((mut log, _)) = super::open_server_log() {
            for p in problems {
                let _ = writeln!(log, "{} [app] {flag}: {p}", utc_now());
            }
        }
    }
    code
}

// --- the tray at sign-in (run_entry.rs) -------------------------------------------

/// This user's registry (HKEY_CURRENT_USER).
struct UserRegistry;

fn reg_error(what: &str, key: &str, name: &str, code: u32) -> String {
    format!(
        "cannot {what} HKCU\\{key}\\{name}: {}",
        std::io::Error::from_raw_os_error(code as i32)
    )
}

impl run_entry::Values for UserRegistry {
    fn get(&self, key: &str, name: &str) -> Option<String> {
        let (k, n) = (wide(OsStr::new(key)), wide(OsStr::new(name)));
        let mut size: u32 = 0;
        // SAFETY: k and n are NUL-terminated UTF-16 that outlive the call; a null
        // buffer asks only for the size.
        let r = unsafe {
            RegGetValueW(
                HKEY_CURRENT_USER,
                k.as_ptr(),
                n.as_ptr(),
                RRF_RT_REG_SZ,
                std::ptr::null_mut(),
                std::ptr::null_mut(),
                &mut size,
            )
        };
        if r != ERROR_SUCCESS {
            return None;
        }
        let mut buf = vec![0u16; (size as usize).div_ceil(2) + 1];
        let mut size = (buf.len() * 2) as u32;
        // SAFETY: buf holds `size` bytes and outlives the call.
        let r = unsafe {
            RegGetValueW(
                HKEY_CURRENT_USER,
                k.as_ptr(),
                n.as_ptr(),
                RRF_RT_REG_SZ,
                std::ptr::null_mut(),
                buf.as_mut_ptr().cast(),
                &mut size,
            )
        };
        if r != ERROR_SUCCESS {
            return None;
        }
        let len = buf.iter().position(|&c| c == 0).unwrap_or(buf.len());
        Some(String::from_utf16_lossy(&buf[..len]))
    }

    fn set(&self, key: &str, name: &str, data: &str) -> Result<(), String> {
        let (k, n, d) = (
            wide(OsStr::new(key)),
            wide(OsStr::new(name)),
            wide(OsStr::new(data)),
        );
        // SAFETY: all three are NUL-terminated UTF-16 that outlive the call; the data's
        // size counts its terminator, as REG_SZ wants.
        let r = unsafe {
            RegSetKeyValueW(
                HKEY_CURRENT_USER,
                k.as_ptr(),
                n.as_ptr(),
                REG_SZ,
                d.as_ptr().cast(),
                (d.len() * 2) as u32,
            )
        };
        if r == ERROR_SUCCESS {
            Ok(())
        } else {
            Err(reg_error("write", key, name, r))
        }
    }

    fn delete(&self, key: &str, name: &str) -> Result<(), String> {
        let (k, n) = (wide(OsStr::new(key)), wide(OsStr::new(name)));
        // SAFETY: k and n are NUL-terminated UTF-16 that outlive the call.
        let r = unsafe { RegDeleteKeyValueW(HKEY_CURRENT_USER, k.as_ptr(), n.as_ptr()) };
        if r == ERROR_SUCCESS || r == ERROR_FILE_NOT_FOUND {
            Ok(())
        } else {
            Err(reg_error("remove", key, name, r))
        }
    }
}

/// <state>\state\autostart-written: the Run value was written once (run_entry.rs).
fn autostart_written_path() -> Option<PathBuf> {
    super::state_root().map(|r| r.join("state").join("autostart-written"))
}

/// The tray at sign-in: the Run value for this program, when run_entry::decision says so.
pub fn sync_autostart() -> Result<(), String> {
    let written = autostart_written_path().ok_or("cannot find %LOCALAPPDATA%")?;
    run_entry::sync(&UserRegistry, &written, &current_exe()?).map(|_| ())
}

/// The Run value removed (when it is ours), and its marker: a later setup writes it again.
pub fn remove_autostart() -> Result<(), String> {
    let written = autostart_written_path().ok_or("cannot find %LOCALAPPDATA%")?;
    run_entry::remove(&UserRegistry, &written)
}

/// Open a folder or an address with its handler (Explorer, the browser).
pub fn shell_open(target: &OsStr) -> Result<(), String> {
    let file = wide(target);
    let verb = wide(OsStr::new("open"));
    // SAFETY: both strings are NUL-terminated UTF-16 that outlive the call;
    // null window, parameters and directory are allowed.
    let r = unsafe {
        ShellExecuteW(
            std::ptr::null_mut(),
            verb.as_ptr(),
            file.as_ptr(),
            std::ptr::null(),
            std::ptr::null(),
            SW_SHOWNORMAL,
        )
    };
    if r as isize > 32 {
        Ok(())
    } else {
        Err(format!(
            "cannot open {}: ShellExecute error {}",
            target.to_string_lossy(),
            r as isize
        ))
    }
}

// --- the agent -----------------------------------------------------------------

/// Now as RFC 3339 UTC, for the agent's own log lines.
fn utc_now() -> String {
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    let (days, rem) = (secs / 86_400, secs % 86_400);
    // days since 1970-01-01 → civil date (Howard Hinnant's algorithm)
    let z = days as i64 + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z",
        rem / 3600,
        rem / 60 % 60,
        rem % 60
    )
}

/// A job that ends every process in it when its last handle closes (the
/// agent dying), with this process in it so the launcher and its whole tree
/// are too. BREAKAWAY_OK as the launcher's own job: the supervisor's per-child
/// jobs nest inside.
fn join_kill_on_close_job() -> Result<Owned, String> {
    // SAFETY: plain Win32 calls with valid (or null) arguments; the info
    // struct is zeroed then filled, and outlives the call.
    unsafe {
        let job = CreateJobObjectW(std::ptr::null(), std::ptr::null());
        if job.is_null() {
            return Err(std::io::Error::last_os_error().to_string());
        }
        let job = Owned(job);
        let mut info: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = std::mem::zeroed();
        info.BasicLimitInformation.LimitFlags =
            JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | JOB_OBJECT_LIMIT_BREAKAWAY_OK;
        if SetInformationJobObject(
            job.0,
            JobObjectExtendedLimitInformation,
            (&info as *const JOBOBJECT_EXTENDED_LIMIT_INFORMATION).cast(),
            std::mem::size_of::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
        ) == 0
            || AssignProcessToJobObject(job.0, GetCurrentProcess()) == 0
        {
            return Err(std::io::Error::last_os_error().to_string());
        }
        Ok(job)
    }
}

/// What ended one wait of the agent.
enum Woke {
    Stop,
    Exited,
}

/// Wait for the launcher to exit or the stop event to be set.
fn wait_launcher(launcher: HANDLE, stop: HANDLE) -> Woke {
    let hs = [launcher, stop];
    // SAFETY: both handles are live for the duration of the call.
    let r = unsafe { WaitForMultipleObjects(2, hs.as_ptr(), 0, INFINITE) };
    if r == WAIT_OBJECT_0 + 1 {
        Woke::Stop
    } else {
        Woke::Exited
    }
}

/// Wait on the stop event for `d`; true when it was set.
fn stop_set_within(stop: HANDLE, d: Duration) -> bool {
    let ms = u32::try_from(d.as_millis()).unwrap_or(u32::MAX - 1);
    // SAFETY: stop is a live event handle.
    unsafe { WaitForSingleObject(stop, ms) == WAIT_OBJECT_0 }
}

/// `Maktaba.exe --server-agent` (contract-agent): run the launcher until a
/// stop is asked, restarting it when it fails. Ok(exit code for Task
/// Scheduler): 0 after a clean stop, when another agent runs already, or when
/// the server is paused (or held for an install) in this sign-in.
pub fn run_server_agent() -> Result<i32, String> {
    // an install that never finished ended (and said so): the server starts again
    release_stale_hold();
    let exe = current_exe()?;
    let paths = BundlePaths::from_exe(&exe)
        .filter(BundlePaths::payload_present)
        .ok_or_else(|| format!("no server payload beside {}", exe.display()))?;
    let root = super::state_root().ok_or("cannot find %LOCALAPPDATA%")?;
    std::fs::create_dir_all(&root).map_err(|e| format!("cannot create {}: {e}", root.display()))?;
    let (_, log_path) = super::open_server_log()?;
    run_agent(&AgentRun {
        program: paths.launcher(),
        args: vec![
            OsString::from("--app-bundle"),
            paths.server_dir.into_os_string(),
            OsString::from("serve"),
        ],
        workdir: root,
        log: log_path,
        mutex: AGENT_MUTEX.into(),
        stop: STOP_EVENT.into(),
        flags: [super::pause_flag_path(), hold_path()]
            .into_iter()
            .flatten()
            .collect(),
        signin: sign_in_time(),
        signin_delay: SIGNIN_DELAY,
        job: true,
        backoff_min: BACKOFF_MIN,
    })
}

/// One agent's inputs (tests run the loop with a stand-in launcher and names
/// of their own).
#[derive(Clone)]
struct AgentRun {
    program: PathBuf,
    args: Vec<OsString>,
    workdir: PathBuf,
    log: PathBuf,
    mutex: String,
    stop: String,
    /// the pause flag and the install hold: either set in this sign-in keeps
    /// the server stopped; one from an earlier sign-in is removed
    flags: Vec<PathBuf>,
    /// when this sign-in began (sign_in_time)
    signin: Option<u64>,
    /// the launcher starts no sooner than this after the sign-in
    signin_delay: Duration,
    /// put itself (so the launcher's whole tree) in a kill-on-close job
    job: bool,
    backoff_min: Duration,
}

impl AgentRun {
    fn held(&self) -> bool {
        self.flags.iter().any(|f| flag_holds_in(f, self.signin))
    }
}

/// The agent's mutex, owned by this thread: released (not left to be
/// abandoned) before it is closed, so a waiter sees a clean exit.
struct AgentMutex(Owned);

impl Drop for AgentMutex {
    fn drop(&mut self) {
        // SAFETY: this thread owns the mutex since CreateMutexW in run_agent.
        unsafe { ReleaseMutex(self.0 .0) };
    }
}

fn run_agent(run: &AgentRun) -> Result<i32, String> {
    // paused or held in this sign-in: the watchdog's start (or a stray /Run)
    // does not undo it
    if run.held() {
        return Ok(0);
    }
    // one agent per session
    let mutex_name = wide(OsStr::new(&run.mutex));
    // SAFETY: a valid name; the handle is kept (and the mutex owned) until exit.
    let mutex = unsafe { CreateMutexW(std::ptr::null(), 1, mutex_name.as_ptr()) };
    if mutex.is_null() {
        return Err(format!(
            "cannot create the agent's mutex: {}",
            std::io::Error::last_os_error()
        ));
    }
    // SAFETY: read right after the call that set it.
    if unsafe { GetLastError() } == ERROR_ALREADY_EXISTS {
        drop(Owned(mutex));
        return Ok(0);
    }
    let _mutex = AgentMutex(Owned(mutex));

    let stop_name = wide(OsStr::new(&run.stop));
    // SAFETY: a valid name; manual reset, not set; kept until exit.
    let stop = unsafe { CreateEventW(std::ptr::null(), 1, 0, stop_name.as_ptr()) };
    if stop.is_null() {
        return Err(format!(
            "cannot create the stop event: {}",
            std::io::Error::last_os_error()
        ));
    }
    let stop = Owned(stop);
    // nothing asked this agent to stop yet
    // SAFETY: a live event handle.
    unsafe { ResetEvent(stop.0) };
    // a Pause since the check above: its flag is written before its event is set
    if run.held() {
        return Ok(0);
    }

    let open_log = || {
        std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(&run.log)
    };
    let mut log = open_log().map_err(|e| format!("cannot open {}: {e}", run.log.display()))?;
    let mut say = |msg: &str| {
        let _ = writeln!(log, "{} [agent] {msg}", utc_now());
    };
    // a pause or a hold from an earlier sign-in: this sign-in ended it (each
    // looked at again: a Pause may have come since)
    for flag in &run.flags {
        if flag.exists() && !flag_holds_in(flag, run.signin) && std::fs::remove_file(flag).is_ok() {
            say(&format!(
                "{} was from an earlier sign-in: removed",
                flag.display()
            ));
        }
    }
    if run.job {
        match join_kill_on_close_job() {
            // kept open until this process exits: closing it ends every
            // process in the job, this one included
            Ok(job) => std::mem::forget(job),
            Err(e) => say(&format!(
                "no job for the server's tree ({e}): it may outlive a killed agent"
            )),
        }
    }
    let early = signin_wait(run.signin, filetime(SystemTime::now()), run.signin_delay);
    if !early.is_zero() {
        say(&format!(
            "waiting {} s after the sign-in (the mapped drives)",
            early.as_secs()
        ));
        if stop_set_within(stop.0, early) {
            say("stopped (asked to)");
            return Ok(0);
        }
    }

    let mut backoff = run.backoff_min;
    let code = loop {
        let started = Instant::now();
        let child = open_log()
            .and_then(|f| Ok((f.try_clone()?, f)))
            .and_then(|(out, err)| {
                Command::new(&run.program)
                    .args(&run.args)
                    .current_dir(&run.workdir)
                    .stdin(Stdio::null())
                    .stdout(out)
                    .stderr(err)
                    .creation_flags(CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP)
                    .spawn()
            });
        match child {
            Err(e) => say(&format!("cannot start {}: {e}", run.program.display())),
            Ok(mut child) => {
                say(&format!("started the launcher (pid {})", child.id()));
                let h = child.as_raw_handle() as HANDLE;
                if let Woke::Stop = wait_launcher(h, stop.0) {
                    // the launcher sees the same event and stops the server
                    let ms = u32::try_from(STOP_WAIT.as_millis()).unwrap_or(u32::MAX - 1);
                    // SAFETY: the child's handle is live until wait() below.
                    if unsafe { WaitForSingleObject(h, ms) } != WAIT_OBJECT_0 {
                        say("the launcher did not stop in time: ending it");
                        let _ = child.kill();
                    }
                    let _ = child.wait();
                    say("stopped (asked to)");
                    break 0;
                }
                match child.wait() {
                    Ok(st) if st.success() => {
                        say("the launcher exited 0: stopped");
                        break 0;
                    }
                    Ok(st) => say(&format!("the launcher exited ({st})")),
                    Err(e) => say(&format!("cannot wait for the launcher: {e}")),
                }
            }
        }
        if started.elapsed() >= STABLE_AFTER {
            backoff = run.backoff_min;
        }
        say(&format!("starting it again in {} ms", backoff.as_millis()));
        if stop_set_within(stop.0, backoff) {
            say("stopped (asked to)");
            break 0;
        }
        backoff = (backoff * 2).min(BACKOFF_MAX);
    };
    Ok(code)
}

#[cfg(test)]
mod tests {
    use super::*;

    const ME: &str = r"C:\Users\me\AppData\Local\Maktaba";

    fn our_xml(exe: &Path) -> String {
        task_xml(
            "Maktaba Server S-1-5-21-1-2-3-1001",
            exe,
            "--server-agent",
            Path::new(r"C:\x"),
            None,
            WATCHDOG_EVERY,
        )
    }

    #[test]
    fn the_task_runs_the_agent_at_this_users_sign_in_and_after_it_died() {
        let xml = task_xml(
            "Maktaba Server S-1-5-21-1-2-3-1001",
            &Path::new(ME).join("Maktaba.exe"),
            "--server-agent",
            Path::new(ME),
            Some(r"PC\me & co"),
            WATCHDOG_EVERY,
        );
        assert!(xml.contains(r"<Command>C:\Users\me\AppData\Local\Maktaba\Maktaba.exe</Command>"));
        assert!(xml.contains("<Arguments>--server-agent</Arguments>"));
        assert!(
            xml.contains(r"<WorkingDirectory>C:\Users\me\AppData\Local\Maktaba</WorkingDirectory>")
        );
        // the trigger and the principal are this user, escaped
        assert_eq!(xml.matches(r"<UserId>PC\me &amp; co</UserId>").count(), 2);
        assert!(xml.contains("<Delay>PT30S</Delay>"));
        // the watchdog: every 5 minutes for ever, ignored while it runs
        assert!(xml.contains(
            "<Interval>PT5M</Interval>\n        <StopAtDurationEnd>false</StopAtDurationEnd>"
        ));
        assert!(!xml.contains("<Duration>"));
        assert!(xml.contains("<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>"));
        // Task Scheduler's restart on failure does not cover a crash: not used
        assert!(!xml.contains("RestartOnFailure"));
        assert!(xml.contains("<LogonType>InteractiveToken</LogonType>"));
        assert!(xml.contains("<RunLevel>LeastPrivilege</RunLevel>"));
        assert!(xml.contains("<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>"));
        assert!(xml.contains("<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>"));
        assert!(xml.contains(r"<URI>\Maktaba Server S-1-5-21-1-2-3-1001</URI>"));
        assert!(xml.contains("within 5 minutes"));
    }

    #[test]
    fn durations_are_xml_durations() {
        assert_eq!(xml_duration(Duration::from_secs(30)), "PT30S");
        assert_eq!(xml_duration(Duration::from_secs(300)), "PT5M");
        assert_eq!(xml_duration(Duration::from_secs(90)), "PT90S");
    }

    #[test]
    fn each_user_has_a_task_named_by_their_sid() {
        assert_eq!(
            task_name_for("S-1-5-21-1004336348-1177238915-682003330-1001"),
            "Maktaba Server S-1-5-21-1004336348-1177238915-682003330-1001"
        );
        let sid = user_sid().expect("this user's SID");
        assert!(sid.starts_with("S-1-"), "{sid}");
        assert_eq!(task_name(), Ok(task_name_for(&sid)));
    }

    #[test]
    fn a_query_tells_ours_off_and_someone_elses() {
        let exe = Path::new(r"C:\Users\me\AppData\Local\Maktaba\Maktaba.exe");
        let ours = our_xml(exe);
        assert_eq!(parse_task(&ours, exe), LoginItem::Enabled);
        // Task Scheduler gives the path back in its own case, maybe quoted
        let quoted = ours
            .replace(r"<Command>C:\Users\me", r#"<Command>"c:\users\ME"#)
            .replace(r"Maktaba.exe</Command>", r#"Maktaba.exe"</Command>"#);
        assert_eq!(parse_task(&quoted, exe), LoginItem::Enabled);
        // turned off in Task Scheduler: Settings' Enabled, not a trigger's
        let off = ours.replace(
            "    <Enabled>true</Enabled>\n    <Hidden>",
            "    <Enabled>false</Enabled>\n    <Hidden>",
        );
        assert_ne!(off, ours);
        assert_eq!(parse_task(&off, exe), LoginItem::RequiresApproval);
        assert_eq!(task_enabled(&off), Some(false));
        let trigger_off = ours.replacen("<Enabled>true</Enabled>", "<Enabled>false</Enabled>", 1);
        assert_eq!(parse_task(&trigger_off, exe), LoginItem::Enabled);
        // another install's task: register writes ours over it
        assert_eq!(
            parse_task(&ours, Path::new(r"D:\Apps\Maktaba\Maktaba.exe")),
            LoginItem::NotRegistered
        );
        assert_eq!(parse_task("garbage", exe), LoginItem::NotRegistered);
    }

    #[test]
    fn elements_may_carry_attributes() {
        let xml = r#"<A><Principal id="Author"><UserId>S-1</UserId></Principal><PrincipalX>no</PrincipalX></A>"#;
        assert_eq!(element(xml, "Principal"), Some("<UserId>S-1</UserId>"));
        assert_eq!(element(xml, "PrincipalX"), Some("no"));
        assert_eq!(element("<Tag/><Tag>x</Tag>", "Tag"), Some("x"));
        assert_eq!(element("<Tag>x", "Tag"), None);
    }

    #[test]
    fn the_old_task_is_this_users_by_sid_or_name() {
        let with = |id: &str| {
            format!(
                "<Task><Principals><Principal id=\"Author\"><UserId>{id}</UserId></Principal></Principals></Task>"
            )
        };
        let sid = "S-1-5-21-1-2-3-1001";
        assert!(runs_as(&with(sid), sid, None));
        assert!(runs_as(&with("s-1-5-21-1-2-3-1001"), sid, None));
        assert!(runs_as(&with(r"PC\me"), sid, Some(r"pc\ME")));
        assert!(runs_as(&with("me"), sid, Some(r"PC\me")));
        assert!(!runs_as(&with("S-1-5-21-1-2-3-1002"), sid, Some(r"PC\me")));
        assert!(!runs_as(&with(r"PC\you"), sid, Some(r"PC\me")));
        assert!(!runs_as("<Task/>", sid, Some(r"PC\me")));
    }

    #[test]
    fn the_task_file_is_utf16_with_a_bom() {
        let b = utf16_file("<a>é</a>");
        assert_eq!(&b[..2], &[0xFF, 0xFE]);
        assert_eq!(&b[2..4], &[b'<', 0]);
        assert_eq!(&b[8..10], &0xE9u16.to_le_bytes());
    }

    #[test]
    fn utc_now_is_rfc3339() {
        let t = utc_now();
        assert_eq!(t.len(), 20, "{t}");
        assert!(
            t.starts_with("20") && t.ends_with('Z') && &t[10..11] == "T",
            "{t}"
        );
    }

    #[test]
    fn this_sign_in_has_a_time() {
        assert_eq!(filetime(UNIX_EPOCH), Some(116_444_736_000_000_000));
        assert_eq!(
            filetime(UNIX_EPOCH + Duration::from_micros(1)),
            Some(116_444_736_000_000_010)
        );
        let signin = sign_in_time().expect("this sign-in's logon time");
        assert!(signin < filetime(SystemTime::now()).unwrap());
    }

    #[test]
    fn an_agent_soon_after_the_sign_in_waits_for_the_drives() {
        let s = 1_000_000_000u64;
        let d = Duration::from_secs(30);
        let ticks = |secs: u64| secs * 10_000_000;
        assert_eq!(
            signin_wait(Some(s), Some(s + ticks(10)), d),
            Duration::from_secs(20)
        );
        assert_eq!(signin_wait(Some(s), Some(s + ticks(31)), d), Duration::ZERO);
        // a clock behind the sign-in: the whole delay
        assert_eq!(signin_wait(Some(s), Some(s - 1), d), d);
        assert_eq!(signin_wait(None, Some(s), d), Duration::ZERO);
    }

    /// An install hold ends by itself once it is older than HOLD_STALE with no installer
    /// running (it failed or was cancelled after the app closed), or HOLD_MAX whatever runs;
    /// one that does not hold, or is young, or whose installer still runs, stays.
    #[test]
    fn a_stale_install_hold_ends() {
        assert!(hold_stale(true, Some(HOLD_STALE), false));
        assert!(!hold_stale(
            true,
            Some(HOLD_STALE - Duration::from_secs(1)),
            false
        ));
        assert!(!hold_stale(false, Some(HOLD_STALE * 2), false));
        assert!(!hold_stale(true, None, false));
        // a long install: its installer still runs
        assert!(!hold_stale(true, Some(HOLD_STALE * 3), true));
        assert!(hold_stale(true, Some(HOLD_MAX), true));
    }

    /// The installer's mutex is seen while it exists, as the installer's hook makes it.
    #[test]
    fn the_installer_s_mutex_is_seen() {
        let name = format!("MaktabaTestInstaller{}", std::process::id());
        assert!(!mutex_exists(&name));
        let n = wide(OsStr::new(&name));
        // SAFETY: a NUL-terminated name; default security, not owned.
        let h = unsafe { CreateMutexW(std::ptr::null(), 0, n.as_ptr()) };
        assert!(!h.is_null());
        assert!(mutex_exists(&name));
        drop(Owned(h));
        assert!(!mutex_exists(&name));
    }

    #[test]
    fn a_flag_holds_in_its_own_sign_in_only() {
        let (s1, s2) = (100u64, 200u64);
        // set in this sign-in: it names it, or was written after it began
        assert!(holds(Some(s2), Some(250), Some(s2)));
        assert!(holds(Some(s2), Some(150), Some(s2)), "a clock moved back");
        assert!(
            holds(None, Some(250), Some(s2)),
            "written by an older build"
        );
        assert!(
            holds(Some(s1), Some(250), Some(s2)),
            "UAC's other logon session"
        );
        // from an earlier sign-in: ended
        assert!(!holds(Some(s1), Some(150), Some(s2)));
        assert!(!holds(None, Some(150), Some(s2)));
        assert!(!holds(Some(s1), None, Some(s2)));
        // this sign-in unknown: a pause holds
        assert!(holds(Some(s1), Some(150), None));

        assert_eq!(
            flag_text_for("paused from the Maktaba app", Some(42)),
            "paused from the Maktaba app\nsign-in 42\n"
        );
        assert_eq!(flag_sign_in("paused\nsign-in 42\n"), Some(42));
        assert_eq!(flag_sign_in("paused from the Maktaba app\n"), None);
    }

    #[test]
    fn a_flag_file_from_an_earlier_sign_in_is_stale() {
        let tmp = tempfile::tempdir().unwrap();
        let flag = tmp.path().join("server-paused");
        assert!(!flag_holds_in(&flag, Some(1)), "no file");
        let now = filetime(SystemTime::now()).unwrap();
        // this sign-in began a minute ago and the flag names it
        let signin = now - 600_000_000;
        std::fs::write(&flag, flag_text_for("paused", Some(signin))).unwrap();
        assert!(flag_holds_in(&flag, Some(signin)));
        // the next sign-in, an hour on
        assert!(!flag_holds_in(&flag, Some(now + 36_000_000_000)));
        // written by an older build, after this sign-in began
        std::fs::write(&flag, "paused from the Maktaba app\n").unwrap();
        assert!(flag_holds_in(&flag, Some(signin)));
        // …and an hour before it
        let f = std::fs::File::options().write(true).open(&flag).unwrap();
        f.set_modified(SystemTime::now() - Duration::from_secs(3600))
            .unwrap();
        drop(f);
        assert!(!flag_holds_in(&flag, Some(signin)));
        assert!(flag_holds_in(&flag, None), "no sign-in to tell by");
    }

    /// A unique name, so the tests never meet the Maktaba this PC may run.
    fn unique(name: &str) -> String {
        format!(
            r"Local\Maktaba.Test.{name}.{}.{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        )
    }

    #[test]
    fn stop_event_reaches_a_waiter_and_is_a_no_op_without_one() {
        let name = unique("Stop");
        assert_eq!(signal_stop_event(&name), Ok(false), "nothing running");
        assert!(!stop_event_set(&name));
        let n = wide(OsStr::new(&name));
        // SAFETY: test-only, a valid name.
        let ev = Owned(unsafe { CreateEventW(std::ptr::null(), 1, 0, n.as_ptr()) });
        assert!(!stop_set_within(ev.0, Duration::from_millis(10)));
        assert!(!stop_event_set(&name));
        assert_eq!(signal_stop_event(&name), Ok(true));
        assert!(stop_set_within(ev.0, Duration::from_millis(10)));
        assert!(stop_event_set(&name), "set until the next agent resets it");
    }

    #[test]
    fn waiting_for_the_agent_ends_when_its_mutex_is_released() {
        let name = unique("Agent");
        assert!(!mutex_held(&name));
        assert!(!wait_mutex_held(&name, Duration::from_millis(150)));
        assert!(
            wait_mutex_free(&name, Duration::from_millis(10)),
            "no agent"
        );
        let (held_tx, held_rx) = std::sync::mpsc::channel();
        let (go_tx, go_rx) = std::sync::mpsc::channel::<()>();
        let owner_name = name.clone();
        let owner = std::thread::spawn(move || {
            let n = wide(OsStr::new(&owner_name));
            // SAFETY: test-only, a valid name; owned by this thread.
            let m = Owned(unsafe { CreateMutexW(std::ptr::null(), 1, n.as_ptr()) });
            held_tx.send(()).unwrap();
            go_rx.recv().unwrap();
            // SAFETY: this thread owns it.
            unsafe { ReleaseMutex(m.0) };
        });
        held_rx.recv().unwrap();
        assert!(mutex_held(&name));
        assert!(wait_mutex_held(&name, Duration::ZERO));
        assert!(
            !wait_mutex_free(&name, Duration::from_millis(50)),
            "still held"
        );
        go_tx.send(()).unwrap();
        assert!(wait_mutex_free(&name, Duration::from_secs(5)));
        owner.join().unwrap();
    }

    fn stand_in(tmp: &Path, script: &str) -> AgentRun {
        AgentRun {
            program: PathBuf::from("powershell.exe"),
            args: ["-NoProfile", "-NonInteractive", "-Command", script]
                .iter()
                .map(OsString::from)
                .collect(),
            workdir: tmp.to_path_buf(),
            log: tmp.join("server.log"),
            mutex: unique("AgentLoop"),
            stop: unique("StopLoop"),
            flags: Vec::new(),
            signin: None,
            signin_delay: Duration::ZERO,
            job: false,
            backoff_min: Duration::from_millis(50),
        }
    }

    /// The agent's loop with a stand-in launcher (PowerShell): it fails twice
    /// (the agent starts it again with backoff), then waits on the stop event
    /// like the real launcher; setting the event ends both, cleanly, and the
    /// agent's mutex is free afterwards. A second agent meanwhile exits 0 at
    /// once (one per session). The tray reads why it kept failing from the log.
    #[test]
    fn the_agent_restarts_a_failing_launcher_and_stops_on_the_event() {
        let tmp = tempfile::tempdir().unwrap();
        let count = tmp.path().join("runs");
        let mut run = stand_in(tmp.path(), "");
        let script = format!(
            "$n = 0; if (Test-Path '{c}') {{ $n = [int](Get-Content '{c}') }}; $n++; Set-Content '{c}' $n; \
             if ($n -lt 3) {{ Write-Output \"maktaba-launcher: run $n fails\"; exit 1 }}; \
             Write-Output 'run 3 waits'; \
             $e = [System.Threading.EventWaitHandle]::OpenExisting('{s}'); [void]$e.WaitOne(); \
             Write-Output 'asked to stop'; exit 0",
            c = count.display(),
            s = run.stop
        );
        run.args[3] = OsString::from(script);
        let agent = std::thread::scope(|scope| {
            let agent = scope.spawn(|| run_agent(&run));
            // the third run is the one that waits
            let deadline = Instant::now() + Duration::from_secs(60);
            while std::fs::read_to_string(tmp.path().join("server.log"))
                .map(|l| !l.contains("run 3 waits"))
                .unwrap_or(true)
            {
                assert!(
                    Instant::now() < deadline,
                    "the launcher never reached its third run"
                );
                std::thread::sleep(Duration::from_millis(50));
            }
            assert!(
                mutex_held(&run.mutex),
                "the agent holds its mutex while it runs"
            );
            let second = AgentRun {
                log: tmp.path().join("second.log"),
                ..run.clone()
            };
            assert_eq!(run_agent(&second), Ok(0), "a second agent leaves at once");
            assert_eq!(signal_stop_event(&run.stop), Ok(true));
            agent.join().unwrap()
        });
        assert_eq!(agent, Ok(0));
        assert!(wait_mutex_free(&run.mutex, Duration::from_secs(1)));
        let log = std::fs::read_to_string(tmp.path().join("server.log")).unwrap();
        assert_eq!(log.matches("starting it again").count(), 2, "{log}");
        assert!(
            log.contains("run 1 fails") && log.contains("run 2 fails"),
            "{log}"
        );
        assert!(
            log.contains("asked to stop") && log.contains("stopped (asked to)"),
            "{log}"
        );
        assert!(
            !tmp.path().join("second.log").exists(),
            "the second agent started nothing"
        );
        // the agent's own lines, as the tray reads them
        assert_eq!(
            crate::status::failure_in_log(&log).as_deref(),
            Some("run 2 fails")
        );
    }

    /// An agent started while the server is paused (or held for an install)
    /// in this sign-in exits 0 and starts nothing: the watchdog never undoes a
    /// pause. A pause from an earlier sign-in is removed and the server starts.
    #[test]
    fn a_pause_of_this_sign_in_keeps_the_agent_from_starting_the_server() {
        let tmp = tempfile::tempdir().unwrap();
        let started = tmp.path().join("started");
        let flag = tmp.path().join("server-paused");
        let mut run = stand_in(tmp.path(), "");
        run.args[3] = OsString::from(format!(
            "Set-Content '{}' started; $e = [System.Threading.EventWaitHandle]::OpenExisting('{}'); [void]$e.WaitOne(); exit 0",
            started.display(),
            run.stop
        ));
        let now = filetime(SystemTime::now()).unwrap();
        let signin = now - 600_000_000;
        run.signin = Some(signin);
        run.flags = vec![tmp.path().join("server-held"), flag.clone()];

        std::fs::write(&flag, flag_text_for("paused", Some(signin))).unwrap();
        assert_eq!(run_agent(&run), Ok(0));
        assert!(!started.exists(), "a paused server stays stopped");
        assert!(flag.exists(), "the pause is kept");
        assert!(!mutex_held(&run.mutex));

        // the next sign-in: the old pause is over
        run.signin = Some(now + 36_000_000_000);
        let agent = std::thread::scope(|scope| {
            let agent = scope.spawn(|| run_agent(&run));
            let deadline = Instant::now() + Duration::from_secs(60);
            while !started.exists() {
                assert!(Instant::now() < deadline, "the server never started");
                std::thread::sleep(Duration::from_millis(50));
            }
            assert_eq!(signal_stop_event(&run.stop), Ok(true));
            agent.join().unwrap()
        });
        assert_eq!(agent, Ok(0));
        assert!(!flag.exists(), "the earlier sign-in's pause is removed");
        let log = std::fs::read_to_string(&run.log).unwrap();
        assert!(log.contains("was from an earlier sign-in"), "{log}");
    }

    #[test]
    fn a_stop_during_the_sign_in_delay_starts_nothing() {
        let tmp = tempfile::tempdir().unwrap();
        let started = tmp.path().join("started");
        let mut run = stand_in(
            tmp.path(),
            &format!(
                "Set-Content '{}' started",
                tmp.path().join("started").display()
            ),
        );
        // signed in a moment ago: the agent waits out the delay first
        run.signin = filetime(SystemTime::now());
        run.signin_delay = Duration::from_secs(60);
        let agent = std::thread::scope(|scope| {
            let agent = scope.spawn(|| run_agent(&run));
            let deadline = Instant::now() + Duration::from_secs(30);
            while !std::fs::read_to_string(&run.log)
                .unwrap_or_default()
                .contains("after the sign-in")
            {
                assert!(Instant::now() < deadline, "the agent did not wait");
                std::thread::sleep(Duration::from_millis(20));
            }
            assert_eq!(signal_stop_event(&run.stop), Ok(true));
            agent.join().unwrap()
        });
        assert_eq!(agent, Ok(0));
        assert!(!started.exists());
    }

    // --- real tasks (CI only) ------------------------------------------------------

    /// These register real tasks, so they run only where
    /// MAKTABA_TEST_WINDOWS_TASK=1 (the windows-latest CI runner).
    fn real_tasks() -> bool {
        std::env::var("MAKTABA_TEST_WINDOWS_TASK").as_deref() == Ok("1")
    }

    /// A task of the tests, ended and removed when dropped.
    struct TestTask(String);

    impl TestTask {
        fn register(name: String, xml: &str, dir: &Path) -> Self {
            let file = dir.join("task.xml");
            std::fs::write(&file, utf16_file(xml)).unwrap();
            schtasks_ok(&[
                os("/Create"),
                os("/TN"),
                os(&name),
                os("/XML"),
                file.as_os_str(),
                os("/F"),
            ])
            .unwrap();
            TestTask(name)
        }
    }

    impl Drop for TestTask {
        fn drop(&mut self) {
            let _ = schtasks(&[os("/End"), os("/TN"), os(&self.0)]);
            let _ = schtasks(&[os("/Delete"), os("/TN"), os(&self.0), os("/F")]);
        }
    }

    fn powershell() -> PathBuf {
        let system =
            std::env::var_os("SystemRoot").unwrap_or_else(|| OsString::from(r"C:\Windows"));
        Path::new(&system).join(r"System32\WindowsPowerShell\v1.0\powershell.exe")
    }

    /// The task XML the app writes, running a stand-in agent (a script).
    fn stand_in_task(name: &str, script: &Path, dir: &Path, watchdog: Duration) -> String {
        task_xml(
            name,
            &powershell(),
            &format!(
                r#"-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "{}""#,
                script.display()
            ),
            dir,
            task_user().as_deref(),
            watchdog,
        )
    }

    /// How many times a stand-in ran (one line each).
    fn runs(file: &Path) -> usize {
        std::fs::read_to_string(file)
            .map(|s| s.lines().count())
            .unwrap_or(0)
    }

    fn wait_for(limit: Duration, what: &str, mut done: impl FnMut() -> bool) {
        let deadline = Instant::now() + limit;
        while !done() {
            assert!(Instant::now() < deadline, "{what}");
            std::thread::sleep(Duration::from_millis(250));
        }
    }

    fn test_name(what: &str) -> String {
        format!(
            "Maktaba Test {what} {}.{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_millis()
        )
    }

    /// A stand-in agent that crashes: it notes the run and exits with an
    /// access violation's code.
    fn crashing_agent(dir: &Path) -> (PathBuf, PathBuf) {
        let runs = dir.join("runs");
        let script = dir.join("crash.ps1");
        std::fs::write(
            &script,
            format!(
                "Add-Content -LiteralPath '{}' -Value run\r\nexit -1073741819\r\n",
                runs.display()
            ),
        )
        .unwrap();
        (script, runs)
    }

    /// Registers the XML the app writes (under the app's naming rule, with a
    /// test suffix), reads it back and removes it.
    #[test]
    fn task_scheduler_accepts_the_task() {
        if !real_tasks() {
            return;
        }
        let tmp = tempfile::tempdir().unwrap();
        let name = format!("{} test {}", task_name().unwrap(), std::process::id());
        let exe = std::env::current_exe().unwrap();
        let xml = task_xml(
            &name,
            &exe,
            "--help",
            tmp.path(),
            task_user().as_deref(),
            WATCHDOG_EVERY,
        );
        let task = TestTask::register(name.clone(), &xml, tmp.path());
        let back = query_xml(&name).expect("the task reads back");
        drop(task);
        assert_eq!(parse_task(&back, &exe), LoginItem::Enabled, "{back}");
        assert!(
            back.contains("PT30S") && back.contains("PT5M") && back.contains("IgnoreNew"),
            "{back}"
        );
        assert!(
            runs_as(&back, &user_sid().unwrap(), task_user().as_deref()),
            "{back}"
        );
    }

    /// The watchdog: the task's action crashes, and the next minute's trigger
    /// starts it again (5 minutes in the app; 1 here, the shortest Task
    /// Scheduler allows).
    #[test]
    fn the_watchdog_starts_an_agent_that_crashed_again() {
        if !real_tasks() {
            return;
        }
        let tmp = tempfile::tempdir().unwrap();
        let (script, runs_file) = crashing_agent(tmp.path());
        let name = test_name("watchdog");
        let xml = stand_in_task(&name, &script, tmp.path(), Duration::from_secs(60));
        let _task = TestTask::register(name.clone(), &xml, tmp.path());
        run_task(&name).unwrap();
        wait_for(Duration::from_secs(30), "the task did not run", || {
            runs(&runs_file) >= 1
        });
        wait_for(
            Duration::from_secs(90),
            "the watchdog trigger did not start the crashed agent again",
            || runs(&runs_file) >= 2,
        );
    }

    /// What contract-agent relied on before: Task Scheduler's restart on
    /// failure. It does not start an action that crashed again (it covers a
    /// task that cannot start), so the app has the watchdog instead.
    #[test]
    fn restart_on_failure_does_not_start_an_agent_that_crashed() {
        if !real_tasks() {
            return;
        }
        let tmp = tempfile::tempdir().unwrap();
        let (script, runs_file) = crashing_agent(tmp.path());
        let name = test_name("restart-on-failure");
        let mut xml = stand_in_task(&name, &script, tmp.path(), Duration::from_secs(60));
        let start = xml.find("    <TimeTrigger>").unwrap();
        let end = xml.find("</TimeTrigger>\n").unwrap() + "</TimeTrigger>\n".len();
        xml.replace_range(start..end, "");
        let xml = xml.replace(
            "  </Settings>",
            "    <RestartOnFailure>\n      <Interval>PT1M</Interval>\n      <Count>3</Count>\n    </RestartOnFailure>\n  </Settings>",
        );
        let _task = TestTask::register(name.clone(), &xml, tmp.path());
        run_task(&name).unwrap();
        wait_for(Duration::from_secs(30), "the task did not run", || {
            runs(&runs_file) >= 1
        });
        std::thread::sleep(Duration::from_secs(100));
        assert_eq!(
            runs(&runs_file),
            1,
            "Task Scheduler restarted the crashed action: RestartOnFailure covers a crash \
             after all (contract-agent may say so; the watchdog stays)"
        );
    }

    /// A stand-in agent that runs until its stop event, holding a mutex like
    /// the real one, and takes 3 s to stop (a server draining).
    fn waiting_agent(dir: &Path, mutex: &str, stop: &str) -> (PathBuf, PathBuf) {
        let runs = dir.join("runs");
        let script = dir.join("agent.ps1");
        std::fs::write(
            &script,
            format!(
                "$m = [System.Threading.Mutex]::new($true, '{mutex}')\r\n\
                 $e = [System.Threading.EventWaitHandle]::new($false, [System.Threading.EventResetMode]::ManualReset, '{stop}')\r\n\
                 Add-Content -LiteralPath '{runs}' -Value run\r\n\
                 [void]$e.WaitOne(300000)\r\n\
                 Start-Sleep -Seconds 3\r\n\
                 $m.ReleaseMutex()\r\n\
                 exit 0\r\n",
                runs = runs.display()
            ),
        )
        .unwrap();
        (script, runs)
    }

    /// While the agent runs, neither the watchdog nor /Run starts a second
    /// one (IgnoreNew); so Resume while it is still stopping must wait for it
    /// (start_task), or the /Run is lost and the server stays stopped.
    #[test]
    fn a_running_agent_is_not_started_twice_and_resume_waits_for_one_stopping() {
        if !real_tasks() {
            return;
        }
        let tmp = tempfile::tempdir().unwrap();
        let (mutex, stop) = (unique("TaskAgent"), unique("TaskStop"));
        let (script, runs_file) = waiting_agent(tmp.path(), &mutex, &stop);
        let name = test_name("resume");
        let xml = stand_in_task(&name, &script, tmp.path(), Duration::from_secs(60));
        let _task = TestTask::register(name.clone(), &xml, tmp.path());
        run_task(&name).unwrap();
        wait_for(
            Duration::from_secs(30),
            "the task did not run in this session",
            || runs(&runs_file) == 1 && mutex_held(&mutex),
        );
        // a watchdog minute and a /Run while it runs: nothing new
        std::thread::sleep(Duration::from_secs(65));
        let _ = run_task(&name);
        std::thread::sleep(Duration::from_secs(5));
        assert_eq!(runs(&runs_file), 1, "a second agent started");
        // Pause, then Resume at once, while it is still stopping
        assert_eq!(signal_stop_event(&stop), Ok(true));
        assert!(stop_event_set(&stop));
        start_task(&name, &mutex, &stop).unwrap();
        wait_for(
            Duration::from_secs(20),
            "Resume did not start the agent again after the stop",
            || runs(&runs_file) == 2,
        );
        // end the second run
        wait_for(Duration::from_secs(30), "the second run", || {
            mutex_held(&mutex)
        });
        let _ = signal_stop_event(&stop);
        assert!(wait_mutex_free(&mutex, Duration::from_secs(30)));
        // Resume right after it exited, no stop pending (as an agent leaves it):
        // its process may still be going, and Task Scheduler then ignores a /Run
        reset_event(&stop);
        assert!(!stop_event_set(&stop));
        start_task(&name, &mutex, &stop).unwrap();
        wait_for(
            Duration::from_secs(20),
            "Resume right after an agent exited did not start it again",
            || runs(&runs_file) == 3,
        );
        wait_for(Duration::from_secs(30), "the third run", || {
            mutex_held(&mutex)
        });
        let _ = signal_stop_event(&stop);
        assert!(wait_mutex_free(&mutex, Duration::from_secs(30)));
    }

    fn reset_event(name: &str) {
        let ev = open_event(name, EVENT_MODIFY_STATE).expect("the stop event");
        // SAFETY: ev is a live event handle opened with EVENT_MODIFY_STATE.
        assert_ne!(unsafe { ResetEvent(ev.0) }, 0, "ResetEvent");
    }

    /// This user's task of the old shared name is replaced by this user's own
    /// (kept turned off), once.
    #[test]
    fn an_old_named_task_of_this_user_is_replaced_once() {
        if !real_tasks() {
            return;
        }
        // never on a PC that runs Maktaba: it would replace the real task
        assert!(
            query_xml(OLD_TASK_NAME).is_none() && query_xml(&task_name().unwrap()).is_none(),
            "MAKTABA_TEST_WINDOWS_TASK=1 is for a PC without a Maktaba sign-in task"
        );
        let tmp = tempfile::tempdir().unwrap();
        let exe = std::env::current_exe().unwrap();
        let xml = task_xml(
            OLD_TASK_NAME,
            &exe,
            "--help",
            tmp.path(),
            task_user().as_deref(),
            WATCHDOG_EVERY,
        );
        let _old = TestTask::register(OLD_TASK_NAME.into(), &xml, tmp.path());
        let _new = TestTask(task_name().unwrap());
        schtasks_ok(&[os("/Change"), os("/TN"), os(OLD_TASK_NAME), os("/DISABLE")]).unwrap();
        assert_eq!(replace_old_task(), Ok(true));
        assert!(query_xml(OLD_TASK_NAME).is_none(), "the old task is gone");
        assert_eq!(login_item(), LoginItem::RequiresApproval, "kept turned off");
        assert_eq!(replace_old_task(), Ok(false), "once");
        assert!(registered_here());
    }
}
