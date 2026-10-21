// The server at sign-in on Windows (Epic 38, story 38.3;
// specs/epics/38-windows-app/contract-agent.md).
//
// A sign-in task of the user, "\Maktaba Server", runs `Maktaba.exe
// --server-agent`. The agent is this GUI executable (nothing flashes); it
// starts <install>\server\bin\maktaba-launcher.exe with CREATE_NO_WINDOW,
// which gives the launcher a console without a window that the server and
// every role inherit, so the launcher's CTRL_BREAK reaches the server and
// the server's reaches its roles. The agent and the launcher both wait on
// the named event Local\Maktaba.Server.Stop: Pause sets it, the launcher
// stops the server cleanly and exits 0, the agent exits 0, and Task
// Scheduler leaves the task "Ready" until Resume (schtasks /Run) or the next
// sign-in. A launcher that fails is started again with backoff; an agent
// that dies takes its tree with it (a kill-on-close job) and Task Scheduler
// restarts it.
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

use windows_sys::Win32::Foundation::{
    CloseHandle, GetLastError, ERROR_ALREADY_EXISTS, HANDLE, WAIT_ABANDONED_0, WAIT_OBJECT_0,
};
use windows_sys::Win32::System::JobObjects::{
    AssignProcessToJobObject, CreateJobObjectW, JobObjectExtendedLimitInformation,
    SetInformationJobObject, JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JOB_OBJECT_LIMIT_BREAKAWAY_OK,
    JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
};
use windows_sys::Win32::System::Threading::{
    CreateEventW, CreateMutexW, GetCurrentProcess, OpenEventW, OpenMutexW, ReleaseMutex,
    ResetEvent, SetEvent, WaitForMultipleObjects, WaitForSingleObject, CREATE_NEW_PROCESS_GROUP,
    CREATE_NO_WINDOW, EVENT_MODIFY_STATE, INFINITE, SYNCHRONIZATION_SYNCHRONIZE,
};
use windows_sys::Win32::UI::Shell::ShellExecuteW;
use windows_sys::Win32::UI::WindowsAndMessaging::SW_SHOWNORMAL;

use super::{Action, BundlePaths, LoginItem};

/// The sign-in task (apart from `\Maktaba`, the task `maktaba-server setup
/// --service` writes; story 38.13 moves such an install into the app).
pub const TASK_NAME: &str = "Maktaba Server";
/// The launcher's stop event (cmd/maktaba-launcher/stopevent_windows.go).
const STOP_EVENT: &str = r"Local\Maktaba.Server.Stop";
/// Held by the running agent: one per session, and "is it running?".
const AGENT_MUTEX: &str = r"Local\Maktaba.Server.Agent";
/// How long a stop may take: the launcher's 60 s grace, then Postgres's fast
/// shutdown.
const STOP_WAIT: Duration = Duration::from_secs(90);
const BACKOFF_MIN: Duration = Duration::from_secs(1);
const BACKOFF_MAX: Duration = Duration::from_secs(30);
/// A launcher run this long resets the backoff.
const STABLE_AFTER: Duration = Duration::from_secs(60);

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
    (!h.is_null()).then_some(Owned(h))
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

/// The sign-in task's XML (contract-agent's table): `command args` at the
/// sign-in of `user` (DOMAIN\name; None: whoever registers it), 30 s late so
/// the session's mapped drives are back, as that user with least privilege,
/// one at a time, no time limit, restarted when it fails.
pub fn task_xml(command: &Path, args: &str, workdir: &Path, user: Option<&str>) -> String {
    let user_id = user
        .map(|u| format!("\n      <UserId>{}</UserId>", xml_escape(u)))
        .unwrap_or_default();
    let principal = user
        .map(|u| format!("\n      <UserId>{}</UserId>", xml_escape(u)))
        .unwrap_or_default();
    format!(
        r#"<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Maktaba: runs the Maktaba server at sign-in (the app's server agent). Pause and resume it from the Maktaba tray icon.</Description>
    <URI>\{name}</URI>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>{user_id}
      <Delay>PT30S</Delay>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">{principal}
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
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
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
        name = xml_escape(TASK_NAME),
        command = xml_escape(&command.display().to_string()),
        args = xml_escape(args),
        workdir = xml_escape(&workdir.display().to_string()),
    )
}

/// The text of the first `<tag>…</tag>` inside `xml`.
fn element<'a>(xml: &'a str, tag: &str) -> Option<&'a str> {
    let open = format!("<{tag}>");
    let close = format!("</{tag}>");
    let start = xml.find(&open)? + open.len();
    let end = start + xml[start..].find(&close)?;
    Some(&xml[start..end])
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
    match element(xml, "Settings").and_then(|s| element(s, "Enabled")) {
        Some(v) if v.trim().eq_ignore_ascii_case("false") => LoginItem::RequiresApproval,
        _ => LoginItem::Enabled,
    }
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

/// The task's state for this app (registered, turned off, not there).
pub fn login_item() -> LoginItem {
    let Ok(exe) = current_exe() else {
        return LoginItem::NotRegistered;
    };
    match schtasks(&[os("/Query"), os("/TN"), os(TASK_NAME), os("/XML")]) {
        Ok(out) if out.status.success() => parse_task(&String::from_utf8_lossy(&out.stdout), &exe),
        _ => LoginItem::NotRegistered,
    }
}

/// This user, as Task Scheduler names them (DOMAIN\name).
fn task_user() -> Option<String> {
    let name = std::env::var("USERNAME").ok().filter(|n| !n.is_empty())?;
    Some(match std::env::var("USERDOMAIN") {
        Ok(d) if !d.is_empty() => format!("{d}\\{name}"),
        _ => name,
    })
}

/// Register the sign-in task for this app (again: /F replaces one that runs
/// another executable) and start the server.
pub fn register() -> Result<(), String> {
    let exe = current_exe()?;
    let root = super::state_root().ok_or("cannot find %LOCALAPPDATA%")?;
    let dir = root.join("updates");
    std::fs::create_dir_all(&dir).map_err(|e| format!("cannot create {}: {e}", dir.display()))?;
    let file = dir.join("maktaba-server-task.xml");
    let xml = task_xml(&exe, super::AGENT_FLAG, &root, task_user().as_deref());
    std::fs::write(&file, utf16_file(&xml))
        .map_err(|e| format!("cannot write {}: {e}", file.display()))?;
    schtasks_ok(&[
        os("/Create"),
        os("/TN"),
        os(TASK_NAME),
        os("/XML"),
        file.as_os_str(),
        os("/F"),
    ])?;
    run_task()
}

fn run_task() -> Result<(), String> {
    schtasks_ok(&[os("/Run"), os("/TN"), os(TASK_NAME)])
}

/// Stop the server and remove the sign-in task (client mode, the uninstaller).
pub fn unregister() -> Result<(), String> {
    stop_and_wait()?;
    // its exit code is not checked: a task that was never there is not an error
    schtasks(&[os("/Delete"), os("/TN"), os(TASK_NAME), os("/F")]).map(|_| ())
}

/// The task turned off in Task Scheduler: turn it on and start the server.
pub fn open_login_items_settings() {
    let _ = control(Action::Resume);
}

/// Ask the server to stop and wait until the agent has exited (before an
/// update replaces the payload's files, which are in use while it runs).
pub fn stop_and_wait() -> Result<(), String> {
    signal_stop_event(STOP_EVENT)?;
    if wait_mutex_free(AGENT_MUTEX, STOP_WAIT) {
        Ok(())
    } else {
        Err(format!(
            "the server did not stop within {} s",
            STOP_WAIT.as_secs()
        ))
    }
}

/// Pause, resume or restart the server (contract-agent's table). The pause
/// flag is the shell's to keep (it decides when to set it).
pub fn control(action: Action) -> Result<(), String> {
    match action {
        Action::Pause => signal_stop_event(STOP_EVENT).map(|_| ()),
        Action::Resume => {
            if login_item() == LoginItem::RequiresApproval {
                schtasks_ok(&[os("/Change"), os("/TN"), os(TASK_NAME), os("/ENABLE")])?;
            }
            run_task()
        }
        Action::Restart => {
            stop_and_wait()?;
            run_task()
        }
    }
}

/// The installer's hooks (`--server-start`, `--server-stop`,
/// `--server-remove`): an exit code for NSIS; problems go to the server log.
pub fn installer_hook(flag: &str) -> i32 {
    let result = match flag {
        super::START_FLAG => {
            if super::paused_flag() || login_item() != LoginItem::Enabled {
                Ok(()) // a first install (setup registers it) or a paused server
            } else {
                run_task()
            }
        }
        super::STOP_FLAG => stop_and_wait(),
        super::REMOVE_FLAG => unregister(),
        _ => Err(format!("unknown flag {flag}")),
    };
    match result {
        Ok(()) => 0,
        Err(e) => {
            if let Ok((mut log, _)) = super::open_server_log() {
                let _ = writeln!(log, "{} [app] {flag}: {e}", utc_now());
            }
            1
        }
    }
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
/// Scheduler): 0 after a clean stop or when another agent runs already.
pub fn run_server_agent() -> Result<i32, String> {
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
        session: true,
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
    /// the session's real agent: it clears the pause flag and puts itself
    /// (so the launcher's whole tree) in a kill-on-close job
    session: bool,
    backoff_min: Duration,
}

fn run_agent(run: &AgentRun) -> Result<i32, String> {
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
    let mutex = Owned(mutex);

    // the agent started (a sign-in or Resume): no longer paused, nothing asked to stop
    if run.session {
        super::set_paused_flag(false);
    }
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
    // SAFETY: a live event handle.
    unsafe { ResetEvent(stop.0) };

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
    let _job = if run.session {
        match join_kill_on_close_job() {
            Ok(j) => Some(j),
            Err(e) => {
                say(&format!(
                    "no job for the server's tree ({e}): it may outlive a killed agent"
                ));
                None
            }
        }
    } else {
        None
    };

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
    // the mutex is released (not left to be abandoned) before the stop is
    // reported done to whoever waits on it
    // SAFETY: this thread owns the mutex since CreateMutexW above.
    unsafe { ReleaseMutex(mutex.0) };
    Ok(code)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_task_runs_the_agent_at_this_users_sign_in() {
        let xml = task_xml(
            Path::new(r"C:\Users\me\AppData\Local\Maktaba\Maktaba.exe"),
            "--server-agent",
            Path::new(r"C:\Users\me\AppData\Local\Maktaba"),
            Some(r"PC\me & co"),
        );
        assert!(xml.contains(r"<Command>C:\Users\me\AppData\Local\Maktaba\Maktaba.exe</Command>"));
        assert!(xml.contains("<Arguments>--server-agent</Arguments>"));
        assert!(
            xml.contains(r"<WorkingDirectory>C:\Users\me\AppData\Local\Maktaba</WorkingDirectory>")
        );
        // the trigger and the principal are this user, escaped
        assert_eq!(xml.matches(r"<UserId>PC\me &amp; co</UserId>").count(), 2);
        assert!(xml.contains("<Delay>PT30S</Delay>"));
        assert!(xml.contains("<LogonType>InteractiveToken</LogonType>"));
        assert!(xml.contains("<RunLevel>LeastPrivilege</RunLevel>"));
        assert!(xml.contains("<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>"));
        assert!(xml.contains("<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>"));
        assert!(xml.contains("<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>"));
        assert!(xml.contains("<Count>999</Count>"));
        assert!(xml.contains(r"<URI>\Maktaba Server</URI>"));
    }

    #[test]
    fn a_query_tells_ours_off_and_someone_elses() {
        let exe = Path::new(r"C:\Users\me\AppData\Local\Maktaba\Maktaba.exe");
        let ours = task_xml(exe, "--server-agent", Path::new(r"C:\x"), None);
        assert_eq!(parse_task(&ours, exe), LoginItem::Enabled);
        // Task Scheduler gives the path back in its own case, maybe quoted
        let quoted = ours
            .replace(r"<Command>C:\Users\me", r#"<Command>"c:\users\ME"#)
            .replace(r"Maktaba.exe</Command>", r#"Maktaba.exe"</Command>"#);
        assert_eq!(parse_task(&quoted, exe), LoginItem::Enabled);
        // turned off in Task Scheduler: Settings' Enabled, not the trigger's
        let off = ours.replace(
            "    <Enabled>true</Enabled>\n    <Hidden>",
            "    <Enabled>false</Enabled>\n    <Hidden>",
        );
        assert_ne!(off, ours);
        assert_eq!(parse_task(&off, exe), LoginItem::RequiresApproval);
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
        let n = wide(OsStr::new(&name));
        // SAFETY: test-only, a valid name.
        let ev = Owned(unsafe { CreateEventW(std::ptr::null(), 1, 0, n.as_ptr()) });
        assert!(!stop_set_within(ev.0, Duration::from_millis(10)));
        assert_eq!(signal_stop_event(&name), Ok(true));
        assert!(stop_set_within(ev.0, Duration::from_millis(10)));
    }

    #[test]
    fn waiting_for_the_agent_ends_when_its_mutex_is_released() {
        let name = unique("Agent");
        assert!(!mutex_held(&name));
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
        assert!(
            !wait_mutex_free(&name, Duration::from_millis(50)),
            "still held"
        );
        go_tx.send(()).unwrap();
        assert!(wait_mutex_free(&name, Duration::from_secs(5)));
        owner.join().unwrap();
    }

    /// The agent's loop with a stand-in launcher (PowerShell): it fails twice
    /// (the agent starts it again with backoff), then waits on the stop event
    /// like the real launcher; setting the event ends both, cleanly, and the
    /// agent's mutex is free afterwards. A second agent meanwhile exits 0 at
    /// once (one per session).
    #[test]
    fn the_agent_restarts_a_failing_launcher_and_stops_on_the_event() {
        let tmp = tempfile::tempdir().unwrap();
        let mutex = unique("AgentLoop");
        let stop = unique("StopLoop");
        let count = tmp.path().join("runs");
        let script = format!(
            "$n = 0; if (Test-Path '{c}') {{ $n = [int](Get-Content '{c}') }}; $n++; Set-Content '{c}' $n; \
             if ($n -lt 3) {{ Write-Output \"run $n fails\"; exit 1 }}; \
             Write-Output 'run 3 waits'; \
             $e = [System.Threading.EventWaitHandle]::OpenExisting('{s}'); [void]$e.WaitOne(); \
             Write-Output 'asked to stop'; exit 0",
            c = count.display(),
            s = stop
        );
        let run = AgentRun {
            program: PathBuf::from("powershell.exe"),
            args: ["-NoProfile", "-NonInteractive", "-Command", &script]
                .iter()
                .map(OsString::from)
                .collect(),
            workdir: tmp.path().to_path_buf(),
            log: tmp.path().join("server.log"),
            mutex: mutex.clone(),
            stop: stop.clone(),
            session: false,
            backoff_min: Duration::from_millis(50),
        };
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
                mutex_held(&mutex),
                "the agent holds its mutex while it runs"
            );
            let second = AgentRun {
                log: tmp.path().join("second.log"),
                ..run.clone()
            };
            assert_eq!(run_agent(&second), Ok(0), "a second agent leaves at once");
            assert_eq!(signal_stop_event(&stop), Ok(true));
            agent.join().unwrap()
        });
        assert_eq!(agent, Ok(0));
        assert!(wait_mutex_free(&mutex, Duration::from_secs(1)));
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
    }

    /// Registers a real sign-in task with the XML the app writes, reads it
    /// back and removes it. It changes the machine's tasks, so it runs only
    /// where MAKTABA_TEST_WINDOWS_TASK=1 (the windows-latest CI runner).
    #[test]
    fn task_scheduler_accepts_the_task() {
        if std::env::var("MAKTABA_TEST_WINDOWS_TASK").as_deref() != Ok("1") {
            return;
        }
        let tmp = tempfile::tempdir().unwrap();
        let name = format!("Maktaba Test {}", std::process::id());
        let exe = std::env::current_exe().unwrap();
        let xml = task_xml(&exe, "--help", tmp.path(), task_user().as_deref()).replace(
            r"<URI>\Maktaba Server</URI>",
            &format!(r"<URI>\{name}</URI>"),
        );
        let file = tmp.path().join("task.xml");
        std::fs::write(&file, utf16_file(&xml)).unwrap();
        schtasks_ok(&[
            os("/Create"),
            os("/TN"),
            os(&name),
            os("/XML"),
            file.as_os_str(),
            os("/F"),
        ])
        .unwrap();
        let out = schtasks(&[os("/Query"), os("/TN"), os(&name), os("/XML")]).unwrap();
        let back = String::from_utf8_lossy(&out.stdout).to_string();
        let _ = schtasks(&[os("/Delete"), os("/TN"), os(&name), os("/F")]);
        assert!(out.status.success(), "{back}");
        assert_eq!(parse_task(&back, &exe), LoginItem::Enabled, "{back}");
        assert!(
            back.contains("PT30S") && back.contains("IgnoreNew"),
            "{back}"
        );
    }
}
