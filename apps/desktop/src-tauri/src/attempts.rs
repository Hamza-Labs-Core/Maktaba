// Every update this app tries, with its reason when it fails (the owner's request after the
// Mac's 0.3.0-rc.5 to 0.3.0 update failed without a word; specs/epics/35-cluster-full-
// nodes-and-updates/update-attempts.md). The record is app-attempts.json beside the home's
// order (app-install.json): newest first, at most KEEP, written whole. The server reads it
// (its update status carries it to the Updates page); this app reads it for the menu's
// line about the last failure, and settles at its start the attempt its own restart ended.
// classify turns the updater's and the OS's errors into the spec's codes; lang.rs says each
// in the tray's language. app.log beside server.log keeps one line per event.
use std::io::Write as _;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Mutex;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};

/// The record's file name, in the folder of the home's order.
pub const FILE: &str = "app-attempts.json";
/// How many attempts the record keeps.
pub const KEEP: usize = 20;

/// The source of every attempt this app records.
pub const SOURCE_APP: &str = "app";

/// What started an attempt: the update item the tray's badge announces (the app's own
/// feed, the Linux app catching up with its server), or the home's order.
pub const TRIGGER_BADGE: &str = "badge";
pub const TRIGGER_ORDER: &str = "order";

/// Outcomes.
pub const RUNNING: &str = "running";
pub const INSTALLED: &str = "installed";
pub const FAILED: &str = "failed";

/// Steps.
pub const STEP_CHECK: &str = "check";
pub const STEP_DOWNLOAD: &str = "download";
pub const STEP_VERIFY: &str = "verify";
pub const STEP_INSTALL: &str = "install";
pub const STEP_RELAUNCH: &str = "relaunch";

/// The reasons (codes) an attempt of this app fails with.
pub const NO_NETWORK: &str = "no_network";
pub const FEED_NOT_FOUND: &str = "feed_not_found";
pub const FEED_INVALID: &str = "feed_invalid";
pub const ASSET_NOT_FOUND: &str = "asset_not_found";
pub const SIGNATURE_MISMATCH: &str = "signature_mismatch";
pub const NEEDS_ADMIN: &str = "needs_admin";
pub const NOT_WRITABLE: &str = "not_writable";
pub const DISK_FULL: &str = "disk_full";
pub const TRANSLOCATED: &str = "translocated";
pub const QUARANTINED: &str = "quarantined";
pub const INSTALLER_EXIT: &str = "installer_exit";
#[cfg_attr(not(windows), allow(dead_code))]
pub const SERVER_STOP_FAILED: &str = "server_stop_failed";
pub const RELAUNCH_FAILED: &str = "relaunch_failed";
pub const LOGIN_ITEM_UNSIGNED: &str = "login_item_unsigned";
pub const CHECK_FAILED: &str = "check_failed";
pub const DOWNLOAD_FAILED: &str = "download_failed";
pub const INSTALL_FAILED: &str = "install_failed";

/// Every code, for the words' tests (lang::update_reason has each).
#[cfg(test)]
pub const CODES: &[&str] = &[
    NO_NETWORK,
    FEED_NOT_FOUND,
    FEED_INVALID,
    ASSET_NOT_FOUND,
    SIGNATURE_MISMATCH,
    NEEDS_ADMIN,
    NOT_WRITABLE,
    DISK_FULL,
    TRANSLOCATED,
    QUARANTINED,
    INSTALLER_EXIT,
    SERVER_STOP_FAILED,
    RELAUNCH_FAILED,
    LOGIN_ITEM_UNSIGNED,
    CHECK_FAILED,
    DOWNLOAD_FAILED,
    INSTALL_FAILED,
];

/// One attempt, exactly as the spec's record has it (the server and the page read it).
#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
pub struct Attempt {
    pub id: String,
    pub at: String,
    /// Left out while the attempt runs.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub ended_at: String,
    pub source: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub trigger: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub from: String,
    /// The release tried (empty only when the app's own feed could not even be checked).
    #[serde(default)]
    pub to: String,
    pub outcome: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub step: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub code: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub detail: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub fix: String,
}

#[derive(Debug, Default, Serialize, Deserialize)]
struct Record {
    #[serde(default)]
    attempts: Vec<Attempt>,
}

/// The record's path in the state folder (the folder of the home's order).
pub fn path(state_root: &Path) -> PathBuf {
    state_root.join("updates").join(FILE)
}

/// The record's attempts, newest first (none when there is no record, or it cannot be read).
pub fn read(path: &Path) -> Vec<Attempt> {
    std::fs::read(path)
        .ok()
        .and_then(|raw| serde_json::from_slice::<Record>(&raw).ok())
        .map(|r| r.attempts)
        .unwrap_or_default()
}

/// The newest attempt.
pub fn newest(path: &Path) -> Option<Attempt> {
    read(path).into_iter().next()
}

/// How often write tries to put the record in place, and how long it waits between tries
/// (the server reading it can hold the file for a moment on Windows).
const WRITE_TRIES: u32 = 5;
const WRITE_PAUSE: Duration = Duration::from_millis(40);

/// Writes the record whole (a temporary file of this write's own, then a rename tried
/// again a few times), keeping the newest KEEP.
fn write(path: &Path, attempts: &[Attempt]) -> std::io::Result<()> {
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir)?;
    }
    let rec = Record {
        attempts: attempts.iter().take(KEEP).cloned().collect(),
    };
    let raw = serde_json::to_vec_pretty(&rec).map_err(std::io::Error::other)?;
    static SEQ: AtomicU64 = AtomicU64::new(0);
    let tmp = path.with_extension(format!(
        "json.{}-{}.tmp",
        std::process::id(),
        SEQ.fetch_add(1, Ordering::Relaxed)
    ));
    std::fs::write(&tmp, raw)?;
    let mut last = None;
    for attempt in 0..WRITE_TRIES {
        if attempt > 0 {
            std::thread::sleep(WRITE_PAUSE);
        }
        match std::fs::rename(&tmp, path) {
            Ok(()) => return Ok(()),
            Err(e) => last = Some(e),
        }
    }
    let _ = std::fs::remove_file(&tmp);
    Err(last.unwrap_or_else(|| std::io::Error::other("the attempts could not be written")))
}

/// One change of the record at a time in this app (the poller, a tray click, a staging).
static CHANGE: Mutex<()> = Mutex::new(());

/// Changes the record: f gets the attempts, newest first.
fn change(path: &Path, f: impl FnOnce(&mut Vec<Attempt>)) -> std::io::Result<()> {
    let _one = CHANGE.lock().unwrap_or_else(|e| e.into_inner());
    let mut attempts = read(path);
    f(&mut attempts);
    write(path, &attempts)
}

/// A new attempt id (unique in the record: the time in milliseconds, made later than the
/// last one handed out by this app).
fn new_id(now: SystemTime) -> String {
    static LAST: AtomicU64 = AtomicU64::new(0);
    let ms = now
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0);
    let prev = LAST.fetch_max(ms, Ordering::SeqCst);
    let id = if prev >= ms {
        LAST.fetch_add(1, Ordering::SeqCst) + 1
    } else {
        ms
    };
    format!("app-{id}")
}

/// Starts an attempt (running, at the check) and returns its id.
pub fn begin(path: &Path, trigger: &str, from: &str, to: &str) -> String {
    let now = SystemTime::now();
    let a = Attempt {
        id: new_id(now),
        at: rfc3339(now),
        source: SOURCE_APP.into(),
        trigger: trigger.into(),
        from: plain(from),
        to: plain(to),
        outcome: RUNNING.into(),
        step: STEP_CHECK.into(),
        ..Attempt::default()
    };
    let id = a.id.clone();
    log(&format!(
        "update {id}: begin ({trigger}) {} -> {}",
        or_dash(&a.from),
        or_dash(&a.to)
    ));
    if let Err(e) = change(path, |all| all.insert(0, a)) {
        log(&format!(
            "update {id}: the record could not be written: {e}"
        ));
    }
    id
}

/// A failed attempt in one go (the home's order could not be staged).
pub fn record_failed(path: &Path, trigger: &str, from: &str, to: &str, failure: &Failure) {
    let id = begin(path, trigger, from, to);
    fail(path, &id, failure);
}

fn update(path: &Path, id: &str, f: impl FnOnce(&mut Attempt)) {
    let res = change(path, |all| {
        if let Some(a) = all.iter_mut().find(|a| a.id == id) {
            f(a);
        }
    });
    if let Err(e) = res {
        log(&format!(
            "update {id}: the record could not be written: {e}"
        ));
    }
}

/// The release the attempt brings (the app's own feed: known once checked).
pub fn set_to(path: &Path, id: &str, to: &str) {
    let to = plain(to);
    update(path, id, |a| a.to = to);
}

/// The step the attempt is at now.
pub fn set_step(path: &Path, id: &str, step: &str) {
    log(&format!("update {id}: {step}"));
    update(path, id, |a| a.step = step.into());
}

/// Why an attempt failed: where, the code, the updater's (or the OS's) own words, and what
/// mends it.
#[derive(Debug, Clone, PartialEq)]
pub struct Failure {
    pub step: &'static str,
    pub code: &'static str,
    pub detail: String,
    pub fix: String,
}

impl Failure {
    pub fn new(step: &'static str, code: &'static str, detail: impl Into<String>) -> Failure {
        Failure {
            step,
            code,
            detail: detail.into(),
            fix: fix_for(code),
        }
    }
}

/// The attempt failed.
pub fn fail(path: &Path, id: &str, f: &Failure) {
    log(&format!(
        "update {id}: failed at {} ({}): {}",
        f.step, f.code, f.detail
    ));
    let now = rfc3339(SystemTime::now());
    update(path, id, |a| {
        a.outcome = FAILED.into();
        a.step = f.step.into();
        a.code = f.code.into();
        a.detail = f.detail.clone();
        a.fix = f.fix.clone();
        a.ended_at = now;
    });
}

/// The attempt was called off before anything was installed (the home's order withdrawn).
pub fn cancel(path: &Path, id: &str) {
    log(&format!("update {id}: cancelled"));
    let now = rfc3339(SystemTime::now());
    update(path, id, |a| {
        a.outcome = "cancelled".into();
        a.ended_at = now;
    });
}

/// The attempt found nothing to install (the app's own feed offers nothing newer): it is
/// taken out of the record.
pub fn discard(path: &Path, id: &str) {
    log(&format!("update {id}: nothing newer"));
    if let Err(e) = change(path, |all| all.retain(|a| a.id != id)) {
        log(&format!(
            "update {id}: the record could not be written: {e}"
        ));
    }
}

/// An error with its causes ("error sending request for url (…): dns error: …"): the
/// updater's own words leave the cause out, which says why.
pub fn error_chain(e: &dyn std::error::Error) -> String {
    let mut out = e.to_string();
    let mut cause = e.source();
    while let Some(c) = cause {
        let s = c.to_string();
        if !out.contains(&s) {
            out.push_str(": ");
            out.push_str(&s);
        }
        cause = c.source();
    }
    out
}

/// A Failure of an updater error at a step (classify_updater, with its causes).
pub fn of_updater(step: &'static str, e: &tauri_plugin_updater::Error, ctx: &Ctx) -> Failure {
    let detail = error_chain(e);
    let mut code = classify_updater(step, e, ctx);
    if code == fallback(step) {
        // the causes may say more than the error itself (a dns error under a request error)
        code = classify_text(step, &detail, ctx);
    }
    Failure::new(step, code, detail)
}

/// A Failure of an error known by its words at a step.
pub fn of_text(step: &'static str, msg: impl Into<String>, ctx: &Ctx) -> Failure {
    let msg = msg.into();
    Failure::new(step, classify_text(step, &msg, ctx), msg)
}

/// An attempt this app's last run left running, settled now that the app runs again as
/// `own` (None: nothing to settle). One at the install or the relaunch installed its
/// release when the app now is it, and failed (relaunch_failed) otherwise: the swap did
/// not take, or a restart brought the old app back. One at an earlier step ended with the
/// app (it quit, or crashed, mid-download): failed at that step.
pub fn settle(a: &Attempt, own: &str, ctx: &Ctx, now: &str) -> Option<Attempt> {
    if a.outcome != RUNNING {
        return None;
    }
    let mut out = a.clone();
    out.ended_at = now.to_string();
    let own = plain(own);
    match a.step.as_str() {
        STEP_INSTALL | STEP_RELAUNCH => {
            if !a.to.is_empty() && own == a.to {
                out.outcome = INSTALLED.into();
                out.code.clear();
                return Some(out);
            }
            out.outcome = FAILED.into();
            out.step = STEP_RELAUNCH.into();
            // what keeps macOS from running the new app, when it says
            out.code = if ctx.translocated {
                TRANSLOCATED
            } else if ctx.quarantined {
                QUARANTINED
            } else {
                RELAUNCH_FAILED
            }
            .into();
            out.detail = format!(
                "the app started on {}, not {}",
                or_dash(&own),
                or_dash(&a.to)
            );
        }
        step => {
            out.outcome = FAILED.into();
            out.code = fallback(step).into();
            out.detail = format!("the app stopped during the {step} (it quit or crashed)");
        }
    }
    out.fix = fix_for(&out.code);
    Some(out)
}

/// Settles the newest attempt (settle) in the record; the attempt when it now failed.
pub fn settle_newest(path: &Path, own: &str, ctx: &Ctx) -> Option<Attempt> {
    let newest = newest(path)?;
    let settled = settle(&newest, own, ctx, &rfc3339(SystemTime::now()))?;
    let id = settled.id.clone();
    let put = settled.clone();
    if let Err(e) = change(path, |all| {
        if let Some(a) = all.iter_mut().find(|a| a.id == id) {
            *a = put;
        }
    }) {
        log(&format!(
            "update {id}: the record could not be written: {e}"
        ));
    }
    if settled.outcome == FAILED {
        log(&format!(
            "update {id}: failed at {} ({}): {}",
            settled.step, settled.code, settled.detail
        ));
        Some(settled)
    } else {
        log(&format!("update {id}: installed (the app runs {own})"));
        None
    }
}

// ---------------------------------------------------------------------------
// Why: the updater's and the OS's errors as the spec's codes
// ---------------------------------------------------------------------------

/// What the OS says of this app that explains a failure (macOS): run from a temporary
/// read-only place (App Translocation), still marked as downloaded and unchecked, or in a
/// folder this user cannot change.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Ctx {
    pub translocated: bool,
    pub quarantined: bool,
    pub needs_admin: bool,
}

impl Ctx {
    /// This app's, now.
    pub fn here() -> Ctx {
        #[cfg(target_os = "macos")]
        {
            let exe = std::env::current_exe().ok();
            let translocated = exe.as_deref().is_some_and(is_translocated);
            let bundle = exe.as_deref().and_then(crate::autoinstall::app_bundle_of);
            let quarantined = bundle.as_deref().is_some_and(has_quarantine);
            let needs_admin = bundle
                .as_deref()
                .and_then(|b| crate::autoinstall::needs_admin(b, crate::autoinstall::writable))
                .is_some();
            Ctx {
                translocated,
                quarantined,
                needs_admin,
            }
        }
        #[cfg(not(target_os = "macos"))]
        Ctx::default()
    }
}

/// macOS runs this copy from a temporary read-only place (App Translocation: an app opened
/// from Downloads or a disk image while still quarantined).
#[cfg(any(target_os = "macos", test))]
pub fn is_translocated(exe: &Path) -> bool {
    exe.to_string_lossy().contains("/AppTranslocation/")
}

/// The bundle still carries com.apple.quarantine (downloaded, not yet checked).
#[cfg(target_os = "macos")]
fn has_quarantine(bundle: &Path) -> bool {
    use std::os::unix::ffi::OsStrExt;
    let (Ok(p), Ok(name)) = (
        std::ffi::CString::new(bundle.as_os_str().as_bytes()),
        std::ffi::CString::new("com.apple.quarantine"),
    ) else {
        return false;
    };
    // SAFETY: both are NUL-terminated strings that outlive the call; a null buffer of size 0
    // asks only the attribute's size (or -1 when it is not there)
    unsafe { libc::getxattr(p.as_ptr(), name.as_ptr(), std::ptr::null_mut(), 0, 0, 0) >= 0 }
}

/// The step's own code, when nothing better says why.
pub fn fallback(step: &str) -> &'static str {
    match step {
        STEP_CHECK => CHECK_FAILED,
        STEP_DOWNLOAD | STEP_VERIFY => DOWNLOAD_FAILED,
        _ => INSTALL_FAILED,
    }
}

/// The code of an updater error at a step.
pub fn classify_updater(
    step: &'static str,
    e: &tauri_plugin_updater::Error,
    ctx: &Ctx,
) -> &'static str {
    use tauri_plugin_updater::Error as E;
    match e {
        E::Reqwest(r) => {
            if r.status().is_some_and(|s| s.as_u16() == 404) {
                return if step == STEP_CHECK {
                    FEED_NOT_FOUND
                } else {
                    ASSET_NOT_FOUND
                };
            }
            if r.is_connect() || r.is_timeout() {
                return NO_NETWORK;
            }
            if r.is_decode() && step == STEP_CHECK {
                return FEED_INVALID;
            }
            classify_text(step, &format!("{r:?}"), ctx)
        }
        // no endpoint answered with a release (a 404 says nothing more to the updater)
        E::ReleaseNotFound => FEED_NOT_FOUND,
        E::Serialization(_)
        | E::Semver(_)
        | E::UrlParse(_)
        | E::EmptyEndpoints
        | E::InsecureTransportProtocol => FEED_INVALID,
        // the feed names no download for this machine
        E::TargetNotFound(_) | E::TargetsNotFound(_) => ASSET_NOT_FOUND,
        E::Minisign(_)
        | E::Base64(_)
        | E::SignatureUtf8(_)
        | E::SignedVersionMismatch { .. }
        | E::MissingSignedVersion => SIGNATURE_MISMATCH,
        E::Io(io) => classify_io(step, io, ctx),
        E::TempDirNotOnSameMountPoint if ctx.translocated => TRANSLOCATED,
        E::TempDirNotOnSameMountPoint => NOT_WRITABLE,
        // the Linux package's password prompt, dismissed or failed
        E::AuthenticationFailed => NEEDS_ADMIN,
        E::Network(msg) => classify_text(step, msg, ctx),
        other => classify_text(step, &other.to_string(), ctx),
    }
}

/// The OS's "disk full" (ENOSPC; Windows ERROR_DISK_FULL, ERROR_HANDLE_DISK_FULL).
fn os_disk_full(code: i32) -> bool {
    if cfg!(windows) {
        code == 112 || code == 39
    } else {
        code == 28
    }
}

/// The OS's "read-only file system" (EROFS; none on Windows).
fn os_read_only(code: i32) -> bool {
    cfg!(unix) && code == 30
}

/// The code of an OS error at a step.
pub fn classify_io(step: &'static str, e: &std::io::Error, ctx: &Ctx) -> &'static str {
    if let Some(code) = e.raw_os_error() {
        if os_disk_full(code) {
            return DISK_FULL;
        }
        if os_read_only(code) {
            return if ctx.translocated {
                TRANSLOCATED
            } else {
                NOT_WRITABLE
            };
        }
    }
    if e.kind() == std::io::ErrorKind::PermissionDenied {
        return denied(ctx);
    }
    classify_text(step, &e.to_string(), ctx)
}

/// A permission refused: the app run from a read-only place, a folder that needs an
/// administrator, or files this user may not replace.
fn denied(ctx: &Ctx) -> &'static str {
    if ctx.translocated {
        TRANSLOCATED
    } else if ctx.needs_admin {
        NEEDS_ADMIN
    } else {
        NOT_WRITABLE
    }
}

/// The code of an error known only by its words (the updater's network message, our own
/// errors, an error of another kind): what they say, else the step's own code.
pub fn classify_text(step: &'static str, msg: &str, ctx: &Ctx) -> &'static str {
    let m = msg.to_ascii_lowercase();
    let has = |words: &[&str]| words.iter().any(|w| m.contains(w));
    if m.starts_with("needs_admin:") {
        return NEEDS_ADMIN;
    }
    if m.starts_with("login_item_unsigned:") {
        return LOGIN_ITEM_UNSIGNED;
    }
    if has(&["apptranslocation"]) {
        return TRANSLOCATED;
    }
    if has(&["quarantine"]) {
        return QUARANTINED;
    }
    if has(&[
        "no space left",
        "disk full",
        "not enough space",
        "disk is full",
    ]) || (cfg!(unix) && m.contains("os error 28"))
        || (cfg!(windows) && (m.contains("os error 112") || m.contains("os error 39")))
    {
        return DISK_FULL;
    }
    if has(&["read-only file system"]) || (cfg!(unix) && m.contains("os error 30")) {
        return if ctx.translocated {
            TRANSLOCATED
        } else {
            NOT_WRITABLE
        };
    }
    if has(&[
        "permission denied",
        "operation not permitted",
        "access is denied",
        "os error 13)",
        "os error 5)",
    ]) {
        return denied(ctx);
    }
    if has(&["signature", "minisign", "public key", "pubkey"]) {
        return SIGNATURE_MISMATCH;
    }
    if has(&["status: 404", "404 not found"]) {
        return if step == STEP_CHECK {
            FEED_NOT_FOUND
        } else {
            ASSET_NOT_FOUND
        };
    }
    if has(&["could not fetch a valid release json"]) {
        return FEED_NOT_FOUND;
    }
    if has(&[
        "dns error",
        "failed to lookup address",
        "error sending request",
        "error trying to connect",
        "tcp connect error",
        "connection refused",
        "connection reset",
        "timed out",
        "network is unreachable",
        "no route to host",
        "could not resolve host",
    ]) {
        return NO_NETWORK;
    }
    if has(&["exit code", "exited with"]) {
        return INSTALLER_EXIT;
    }
    fallback(step)
}

/// What mends a failure with this code (English, for the record's `fix`; "" when the
/// detail says it all).
pub fn fix_for(code: &str) -> String {
    match code {
        NO_NETWORK => "check this machine's internet connection (or its proxy), then update again",
        DISK_FULL => "free some space on the disk that holds Maktaba, then update again",
        NEEDS_ADMIN => {
            "move Maktaba.app to a folder this user can change (Applications in the home folder), \
             or have an administrator install the update"
        }
        NOT_WRITABLE => "give this user the right to change Maktaba's folder, or reinstall Maktaba",
        TRANSLOCATED => "move Maktaba.app to the Applications folder, open it from there, then update again",
        QUARANTINED => "xattr -dr com.apple.quarantine /Applications/Maktaba.app",
        LOGIN_ITEM_UNSIGNED => {
            "codesign --force --deep -s - --identifier com.hamzalabs.maktaba /Applications/Maktaba.app"
        }
        SIGNATURE_MISMATCH => "install the update by hand from the release page; tell the Maktaba team",
        FEED_NOT_FOUND | ASSET_NOT_FOUND => "try again later, or install the update by hand from the release page",
        RELAUNCH_FAILED => "open Maktaba again; if it still runs the old release, install the update by hand",
        _ => "",
    }
    .to_string()
}

// ---------------------------------------------------------------------------
// Time, and the app's own log
// ---------------------------------------------------------------------------

/// RFC 3339 in UTC, to the second (the record's times).
pub fn rfc3339(t: SystemTime) -> String {
    let secs = t
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    let (days, rem) = (secs / 86_400, secs % 86_400);
    let (y, m, d) = civil_from_days(days as i64);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z",
        rem / 3600,
        (rem % 3600) / 60,
        rem % 60
    )
}

/// The date of a day count since 1970-01-01 (Howard Hinnant's civil_from_days).
fn civil_from_days(z: i64) -> (i64, u32, u32) {
    let z = z + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    (if m <= 2 { y + 1 } else { y }, m, d)
}

/// A time of the record (RFC 3339: ours is "2026-10-24T05:30:00Z"; a fraction, or an offset
/// like +03:00, is read too). None when it is not one.
pub fn parse_rfc3339(s: &str) -> Option<SystemTime> {
    let s = s.trim();
    let num = |a: usize, b: usize| -> Option<i64> {
        let part = s.get(a..b)?;
        part.bytes()
            .all(|c| c.is_ascii_digit())
            .then(|| part.parse().ok())?
    };
    let (y, mo, d) = (num(0, 4)?, num(5, 7)?, num(8, 10)?);
    let (h, mi, sec) = (num(11, 13)?, num(14, 16)?, num(17, 19)?);
    if s.get(4..5)? != "-" || s.get(7..8)? != "-" || !matches!(s.get(10..11)?, "T" | "t" | " ") {
        return None;
    }
    if !(1..=12).contains(&mo) || !(1..=31).contains(&d) || h > 23 || mi > 59 || sec > 60 {
        return None;
    }
    let mut rest = s.get(19..)?;
    if let Some(frac) = rest.strip_prefix('.') {
        let digits = frac.bytes().take_while(u8::is_ascii_digit).count();
        rest = &frac[digits..];
    }
    let offset = match rest {
        "Z" | "z" => 0,
        _ => {
            let sign = match rest.get(0..1)? {
                "+" => 1,
                "-" => -1,
                _ => return None,
            };
            let (oh, om) = (
                rest.get(1..3)?.parse::<i64>().ok()?,
                rest.get(4..6)?.parse::<i64>().ok()?,
            );
            if rest.len() != 6 || rest.get(3..4)? != ":" {
                return None;
            }
            sign * (oh * 3600 + om * 60)
        }
    };
    let secs = days_from_civil(y, mo as u32, d as u32) * 86_400 + h * 3600 + mi * 60 + sec - offset;
    u64::try_from(secs)
        .ok()
        .map(|s| UNIX_EPOCH + Duration::from_secs(s))
}

/// The day count since 1970-01-01 of a date (Howard Hinnant's days_from_civil).
fn days_from_civil(y: i64, m: u32, d: u32) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = y.div_euclid(400);
    let yoe = y.rem_euclid(400);
    let mp = i64::from(if m > 2 { m - 3 } else { m + 9 });
    let doy = (153 * mp + 2) / 5 + i64::from(d) - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

/// How far before and after an attempt the logs page shows.
pub const LOGS_MARGIN: Duration = Duration::from_secs(120);

/// The web logs page of this machine's server for an attempt ("See the logs"): its time,
/// two minutes on either side (since the attempt began, until it ended, or began when it
/// has not), and the release it tried as the search when known.
/// <web_url>/admin/logs?since=<ISO>&until=<ISO>&q=<to>
pub fn logs_url(web_url: &str, a: &Attempt) -> String {
    let base = web_url.trim_end_matches('/');
    let mut query: Vec<(&str, String)> = Vec::new();
    if let Some(at) = parse_rfc3339(&a.at) {
        query.push((
            "since",
            rfc3339(at.checked_sub(LOGS_MARGIN).unwrap_or(UNIX_EPOCH)),
        ));
        let end = parse_rfc3339(&a.ended_at).unwrap_or(at);
        query.push(("until", rfc3339(end + LOGS_MARGIN)));
    }
    if !a.to.is_empty() {
        query.push(("q", a.to.clone()));
    }
    let query: Vec<String> = query
        .iter()
        .map(|(k, v)| format!("{k}={}", percent_encode(v)))
        .collect();
    if query.is_empty() {
        format!("{base}/admin/logs")
    } else {
        format!("{base}/admin/logs?{}", query.join("&"))
    }
}

/// A query value with everything but RFC 3986's unreserved characters percent-encoded.
fn percent_encode(v: &str) -> String {
    let mut out = String::with_capacity(v.len());
    for b in v.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => {
                out.push(b as char)
            }
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

/// app.log rotates past this size (one old copy, app.log.1).
const LOG_ROTATE_BYTES: u64 = 1 << 20;

/// One line in app.log beside server.log (best effort: a log that cannot be written is
/// not an error of the update's).
pub fn log(line: &str) {
    let Some(dir) = crate::agent::logs_dir() else {
        return;
    };
    let _ = append_log(&dir, line, SystemTime::now());
}

fn append_log(dir: &Path, line: &str, now: SystemTime) -> std::io::Result<()> {
    std::fs::create_dir_all(dir)?;
    let path = dir.join("app.log");
    if std::fs::metadata(&path).is_ok_and(|m| m.len() > LOG_ROTATE_BYTES) {
        let _ = std::fs::rename(&path, path.with_extension("log.1"));
    }
    let mut f = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&path)?;
    writeln!(f, "{} {}", rfc3339(now), line.replace('\n', " "))
}

/// A version without its v.
fn plain(v: &str) -> String {
    v.strip_prefix('v').unwrap_or(v).to_string()
}

fn or_dash(s: &str) -> &str {
    if s.is_empty() {
        "-"
    } else {
        s
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const SPEC_EXAMPLE: &str = r#"{
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
    }"#;

    #[test]
    fn the_spec_example_reads_and_writes_back_the_same() {
        let a: Attempt = serde_json::from_str(SPEC_EXAMPLE).unwrap();
        assert_eq!(
            (
                a.source.as_str(),
                a.trigger.as_str(),
                a.outcome.as_str(),
                a.step.as_str(),
                a.code.as_str()
            ),
            ("app", "badge", "failed", "install", "needs_admin")
        );
        let back: serde_json::Value = serde_json::to_value(&a).unwrap();
        let want: serde_json::Value = serde_json::from_str(SPEC_EXAMPLE).unwrap();
        assert_eq!(back, want);
        // a running attempt leaves ended_at (and the other empty fields) out
        let running = Attempt {
            id: "app-1".into(),
            at: "2026-10-24T05:30:00Z".into(),
            source: "app".into(),
            to: "0.3.0".into(),
            outcome: RUNNING.into(),
            ..Attempt::default()
        };
        let v = serde_json::to_value(&running).unwrap();
        let keys: Vec<&String> = v.as_object().unwrap().keys().collect();
        assert_eq!(keys, ["at", "id", "outcome", "source", "to"]);
    }

    #[test]
    fn the_record_keeps_the_newest_twenty_first() {
        let dir = tempfile::tempdir().unwrap();
        let p = path(dir.path());
        let mut ids = Vec::new();
        for i in 0..25 {
            ids.push(begin(&p, TRIGGER_BADGE, "0.3.0-rc.5", &format!("0.3.{i}")));
        }
        let all = read(&p);
        assert_eq!(all.len(), KEEP);
        assert_eq!(all[0].to, "0.3.24");
        assert_eq!(all[KEEP - 1].to, "0.3.5");
        let unique: std::collections::HashSet<&String> = all.iter().map(|a| &a.id).collect();
        assert_eq!(unique.len(), KEEP, "ids are unique");
        assert_eq!(all[0].id, ids[24]);
        // whole writes: no temporary file left beside it
        let left: Vec<String> = std::fs::read_dir(p.parent().unwrap())
            .unwrap()
            .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
            .collect();
        assert_eq!(left, [FILE]);
    }

    #[test]
    fn an_attempt_moves_through_its_steps_and_ends() {
        let dir = tempfile::tempdir().unwrap();
        let p = path(dir.path());
        let other = begin(&p, TRIGGER_ORDER, "1.0.0", "1.1.0");
        let id = begin(&p, TRIGGER_BADGE, "v1.0.0", "");
        set_to(&p, &id, "v1.2.0");
        set_step(&p, &id, STEP_DOWNLOAD);
        let a = newest(&p).unwrap();
        assert_eq!(
            (
                a.to.as_str(),
                a.from.as_str(),
                a.step.as_str(),
                a.outcome.as_str()
            ),
            ("1.2.0", "1.0.0", "download", "running")
        );
        assert!(a.ended_at.is_empty());
        fail(
            &p,
            &id,
            &Failure::new(STEP_INSTALL, NEEDS_ADMIN, "needs_admin: /Applications"),
        );
        let a = newest(&p).unwrap();
        assert_eq!(
            (a.outcome.as_str(), a.step.as_str(), a.code.as_str()),
            ("failed", "install", "needs_admin")
        );
        assert!(!a.ended_at.is_empty() && !a.fix.is_empty());
        cancel(&p, &other);
        let all = read(&p);
        assert_eq!(
            (all[1].outcome.as_str(), all[0].id.as_str()),
            ("cancelled", id.as_str())
        );
        // a bad record reads as none, and is replaced by the next write
        std::fs::write(&p, b"{not json").unwrap();
        assert!(read(&p).is_empty());
        record_failed(
            &p,
            TRIGGER_ORDER,
            "1.0.0",
            "1.3.0",
            &Failure::new(STEP_DOWNLOAD, NO_NETWORK, "dns error"),
        );
        let a = newest(&p).unwrap();
        assert_eq!(
            (a.outcome.as_str(), a.code.as_str(), a.to.as_str()),
            ("failed", "no_network", "1.3.0")
        );
    }

    fn running(step: &str, to: &str) -> Attempt {
        Attempt {
            id: "app-1".into(),
            at: "2026-10-24T05:30:00Z".into(),
            source: "app".into(),
            from: "0.3.0-rc.5".into(),
            to: to.into(),
            outcome: RUNNING.into(),
            step: step.into(),
            ..Attempt::default()
        }
    }

    #[test]
    fn the_restart_settles_the_attempt_it_ended() {
        let now = "2026-10-24T05:35:00Z";
        let ok = settle(
            &running(STEP_RELAUNCH, "0.3.0"),
            "0.3.0",
            &Ctx::default(),
            now,
        )
        .unwrap();
        assert_eq!(
            (ok.outcome.as_str(), ok.ended_at.as_str()),
            ("installed", now)
        );
        // Windows: the installer ended the app at the install step
        let ok = settle(
            &running(STEP_INSTALL, "0.3.0"),
            "v0.3.0",
            &Ctx::default(),
            now,
        )
        .unwrap();
        assert_eq!(ok.outcome, "installed");
        let bad = settle(
            &running(STEP_RELAUNCH, "0.3.0"),
            "0.3.0-rc.5",
            &Ctx::default(),
            now,
        )
        .unwrap();
        assert_eq!(
            (bad.outcome.as_str(), bad.step.as_str(), bad.code.as_str()),
            ("failed", "relaunch", "relaunch_failed")
        );
        assert_eq!(bad.detail, "the app started on 0.3.0-rc.5, not 0.3.0");
        assert!(!bad.fix.is_empty());
        let mac = Ctx {
            quarantined: true,
            ..Ctx::default()
        };
        assert_eq!(
            settle(&running(STEP_INSTALL, "0.3.0"), "0.3.0-rc.5", &mac, now)
                .unwrap()
                .code,
            "quarantined"
        );
        let moved = Ctx {
            translocated: true,
            quarantined: true,
            ..Ctx::default()
        };
        assert_eq!(
            settle(&running(STEP_INSTALL, "0.3.0"), "0.3.0-rc.5", &moved, now)
                .unwrap()
                .code,
            "translocated"
        );
        // the app quit mid-download
        let quit = settle(
            &running(STEP_DOWNLOAD, "0.3.0"),
            "0.3.0-rc.5",
            &Ctx::default(),
            now,
        )
        .unwrap();
        assert_eq!(
            (quit.outcome.as_str(), quit.code.as_str()),
            ("failed", "download_failed")
        );
        // over already: nothing to settle
        let mut done = running(STEP_INSTALL, "0.3.0");
        done.outcome = FAILED.into();
        assert!(settle(&done, "0.3.0", &Ctx::default(), now).is_none());
    }

    #[test]
    fn settle_newest_writes_the_record() {
        let dir = tempfile::tempdir().unwrap();
        let p = path(dir.path());
        let id = begin(&p, TRIGGER_BADGE, "0.3.0-rc.5", "0.3.0");
        set_step(&p, &id, STEP_RELAUNCH);
        let failed = settle_newest(&p, "0.3.0-rc.5", &Ctx::default()).unwrap();
        assert_eq!(failed.code, "relaunch_failed");
        assert_eq!(newest(&p).unwrap().outcome, "failed");
        // settled once: nothing the next time
        assert!(settle_newest(&p, "0.3.0-rc.5", &Ctx::default()).is_none());
        let id = begin(&p, TRIGGER_BADGE, "0.3.0-rc.5", "0.3.0");
        set_step(&p, &id, STEP_RELAUNCH);
        assert!(settle_newest(&p, "0.3.0", &Ctx::default()).is_none());
        assert_eq!(newest(&p).unwrap().outcome, "installed");
    }

    #[test]
    fn every_code_from_the_words_of_the_error() {
        let none = Ctx::default();
        let cases: &[(&str, &str, Ctx, &str)] = &[
            (STEP_CHECK, "error sending request for url (https://github.com/...): dns error: failed to lookup address information", none, NO_NETWORK),
            (STEP_DOWNLOAD, "error trying to connect: tcp connect error: Connection refused (os error 111)", none, NO_NETWORK),
            (STEP_DOWNLOAD, "operation timed out", none, NO_NETWORK),
            (STEP_CHECK, "Could not fetch a valid release JSON from the remote", none, FEED_NOT_FOUND),
            (STEP_CHECK, "Download request failed with status: 404 Not Found", none, FEED_NOT_FOUND),
            (STEP_DOWNLOAD, "Download request failed with status: 404 Not Found", none, ASSET_NOT_FOUND),
            (STEP_VERIFY, "The signature could not be verified with the public key", none, SIGNATURE_MISMATCH),
            (STEP_INSTALL, "needs_admin: /Applications/Maktaba.app cannot be replaced", none, NEEDS_ADMIN),
            (STEP_INSTALL, "Permission denied (os error 13)", Ctx { needs_admin: true, ..none }, NEEDS_ADMIN),
            (STEP_INSTALL, "Permission denied (os error 13)", none, NOT_WRITABLE),
            (STEP_INSTALL, "Access is denied. (os error 5)", none, NOT_WRITABLE),
            (STEP_INSTALL, "Read-only file system (os error 30)", Ctx { translocated: true, ..none }, TRANSLOCATED),
            (STEP_INSTALL, "/private/var/folders/x/AppTranslocation/1234/d/Maktaba.app: busy", none, TRANSLOCATED),
            (STEP_RELAUNCH, "the bundle carries com.apple.quarantine", none, QUARANTINED),
            (STEP_DOWNLOAD, "No space left on device", none, DISK_FULL),
            (STEP_DOWNLOAD, "There is not enough space on the disk.", none, DISK_FULL),
            (STEP_INSTALL, "the installer exited with code 2", none, INSTALLER_EXIT),
            (STEP_INSTALL, "login_item_unsigned: the app is not code-signed", none, LOGIN_ITEM_UNSIGNED),
            (STEP_CHECK, "something new", none, CHECK_FAILED),
            (STEP_DOWNLOAD, "something new", none, DOWNLOAD_FAILED),
            (STEP_VERIFY, "something new", none, DOWNLOAD_FAILED),
            (STEP_INSTALL, "binary for the current target not found in the archive", none, INSTALL_FAILED),
        ];
        for (step, msg, ctx, want) in cases {
            assert_eq!(classify_text(step, msg, ctx), *want, "{step}: {msg}");
        }
    }

    #[test]
    fn every_code_from_the_updaters_errors() {
        use tauri_plugin_updater::Error as E;
        let none = Ctx::default();
        let json_err = serde_json::from_str::<serde_json::Value>("{").unwrap_err();
        let cases: Vec<(&'static str, E, Ctx, &str)> = vec![
            (STEP_CHECK, E::ReleaseNotFound, none, FEED_NOT_FOUND),
            (STEP_CHECK, E::Serialization(json_err), none, FEED_INVALID),
            (STEP_CHECK, E::EmptyEndpoints, none, FEED_INVALID),
            (
                STEP_CHECK,
                E::TargetNotFound("darwin-aarch64".into()),
                none,
                ASSET_NOT_FOUND,
            ),
            (
                STEP_DOWNLOAD,
                E::Network("Download request failed with status: 404 Not Found".into()),
                none,
                ASSET_NOT_FOUND,
            ),
            (
                STEP_DOWNLOAD,
                E::Network("Download request failed with status: 502 Bad Gateway".into()),
                none,
                DOWNLOAD_FAILED,
            ),
            (
                STEP_VERIFY,
                E::MissingSignedVersion,
                none,
                SIGNATURE_MISMATCH,
            ),
            (
                STEP_VERIFY,
                E::SignedVersionMismatch {
                    signed: "0.2.0".into(),
                    announced: "0.3.0".into(),
                },
                none,
                SIGNATURE_MISMATCH,
            ),
            (
                STEP_VERIFY,
                E::SignatureUtf8("x".into()),
                none,
                SIGNATURE_MISMATCH,
            ),
            (
                STEP_INSTALL,
                E::Io(std::io::ErrorKind::PermissionDenied.into()),
                none,
                NOT_WRITABLE,
            ),
            (
                STEP_INSTALL,
                E::Io(std::io::ErrorKind::PermissionDenied.into()),
                Ctx {
                    needs_admin: true,
                    ..none
                },
                NEEDS_ADMIN,
            ),
            (
                STEP_INSTALL,
                E::Io(std::io::ErrorKind::PermissionDenied.into()),
                Ctx {
                    translocated: true,
                    ..none
                },
                TRANSLOCATED,
            ),
            (
                STEP_DOWNLOAD,
                E::Io(std::io::Error::from_raw_os_error(if cfg!(windows) {
                    112
                } else {
                    28
                })),
                none,
                DISK_FULL,
            ),
            (
                STEP_INSTALL,
                E::TempDirNotOnSameMountPoint,
                Ctx {
                    translocated: true,
                    ..none
                },
                TRANSLOCATED,
            ),
            (
                STEP_INSTALL,
                E::TempDirNotOnSameMountPoint,
                none,
                NOT_WRITABLE,
            ),
            (STEP_INSTALL, E::AuthenticationFailed, none, NEEDS_ADMIN),
            (
                STEP_INSTALL,
                E::BinaryNotFoundInArchive,
                none,
                INSTALL_FAILED,
            ),
        ];
        for (step, e, ctx, want) in &cases {
            assert_eq!(classify_updater(step, e, ctx), *want, "{step}: {e}");
        }
    }

    #[test]
    fn the_cause_of_an_error_says_why() {
        use tauri_plugin_updater::Error as E;
        let inner = std::io::Error::other("dns error: failed to lookup address information");
        let wrapped = E::Io(std::io::Error::other(Chained(
            "error sending request for url",
            inner,
        )));
        let f = of_updater(STEP_CHECK, &wrapped, &Ctx::default());
        assert_eq!(f.code, NO_NETWORK, "{}", f.detail);
        assert!(f.detail.contains("dns error"), "{}", f.detail);
        assert!(!f.fix.is_empty());
        let t = of_text(
            STEP_INSTALL,
            "Permission denied (os error 13)",
            &Ctx {
                needs_admin: true,
                ..Ctx::default()
            },
        );
        assert_eq!((t.step, t.code), (STEP_INSTALL, NEEDS_ADMIN));
    }

    #[derive(Debug)]
    struct Chained(&'static str, std::io::Error);
    impl std::fmt::Display for Chained {
        fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
            f.write_str(self.0)
        }
    }
    impl std::error::Error for Chained {
        fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
            Some(&self.1)
        }
    }

    #[test]
    fn an_attempt_called_off_or_with_nothing_newer() {
        let dir = tempfile::tempdir().unwrap();
        let p = path(dir.path());
        let a = begin(&p, TRIGGER_ORDER, "1.0.0", "1.1.0");
        cancel(&p, &a);
        assert_eq!(newest(&p).unwrap().outcome, "cancelled");
        let b = begin(&p, TRIGGER_BADGE, "1.0.0", "");
        discard(&p, &b);
        assert_eq!(read(&p).len(), 1);
    }

    #[cfg(unix)]
    #[test]
    fn a_read_only_place_is_the_translocation_on_a_mac() {
        let ro = std::io::Error::from_raw_os_error(30);
        assert_eq!(
            classify_io(
                STEP_INSTALL,
                &ro,
                &Ctx {
                    translocated: true,
                    ..Ctx::default()
                }
            ),
            TRANSLOCATED
        );
        assert_eq!(
            classify_io(STEP_INSTALL, &ro, &Ctx::default()),
            NOT_WRITABLE
        );
    }

    #[test]
    fn translocation_is_seen_in_the_apps_path() {
        assert!(is_translocated(Path::new(
            "/private/var/folders/ab/T/AppTranslocation/0E5B/d/Maktaba.app/Contents/MacOS/Maktaba"
        )));
        assert!(!is_translocated(Path::new(
            "/Applications/Maktaba.app/Contents/MacOS/Maktaba"
        )));
    }

    #[test]
    fn fixes_for_the_codes_a_person_mends() {
        for code in [
            NO_NETWORK,
            DISK_FULL,
            NEEDS_ADMIN,
            TRANSLOCATED,
            QUARANTINED,
            LOGIN_ITEM_UNSIGNED,
        ] {
            assert!(!fix_for(code).is_empty(), "{code}");
        }
        assert!(fix_for(QUARANTINED).starts_with("xattr -dr com.apple.quarantine"));
        assert!(fix_for(LOGIN_ITEM_UNSIGNED).contains("--identifier com.hamzalabs.maktaba"));
        assert_eq!(fix_for(CHECK_FAILED), "");
    }

    #[test]
    fn times_are_rfc3339_in_utc() {
        assert_eq!(rfc3339(UNIX_EPOCH), "1970-01-01T00:00:00Z");
        assert_eq!(
            rfc3339(UNIX_EPOCH + Duration::from_secs(1_792_819_800)),
            "2026-10-24T05:30:00Z"
        );
        assert_eq!(
            rfc3339(UNIX_EPOCH + Duration::from_secs(951_868_799)),
            "2000-02-29T23:59:59Z"
        );
    }

    #[test]
    fn the_records_times_read_back() {
        let t = UNIX_EPOCH + Duration::from_secs(1_792_819_800);
        assert_eq!(parse_rfc3339("2026-10-24T05:30:00Z"), Some(t));
        assert_eq!(parse_rfc3339("2026-10-24T05:30:00.123456Z"), Some(t));
        assert_eq!(parse_rfc3339("2026-10-24T08:30:00+03:00"), Some(t));
        assert_eq!(parse_rfc3339("2026-10-24T00:30:00-05:00"), Some(t));
        assert_eq!(
            parse_rfc3339("2000-02-29T23:59:59Z")
                .map(rfc3339)
                .as_deref(),
            Some("2000-02-29T23:59:59Z")
        );
        for bad in [
            "",
            "yesterday",
            "2026-13-24T05:30:00Z",
            "2026-10-24 05:30",
            "2026-10-24T05:30:00",
        ] {
            assert_eq!(parse_rfc3339(bad), None, "{bad}");
        }
    }

    #[test]
    fn see_the_logs_opens_the_attempts_time_on_the_logs_page() {
        let failed = Attempt {
            at: "2026-10-24T05:30:00Z".into(),
            ended_at: "2026-10-24T05:31:12Z".into(),
            to: "0.3.0-rc.6".into(),
            outcome: FAILED.into(),
            ..Attempt::default()
        };
        assert_eq!(
            logs_url("http://127.0.0.1:8080/", &failed),
            "http://127.0.0.1:8080/admin/logs?since=2026-10-24T05%3A28%3A00Z&until=2026-10-24T05%3A33%3A12Z&q=0.3.0-rc.6"
        );
        // not ended (or its end unreadable): two minutes after it began; no release known:
        // no search
        let early = Attempt {
            at: "2026-10-24T05:30:00Z".into(),
            ..Attempt::default()
        };
        assert_eq!(
            logs_url("http://localhost:8080", &early),
            "http://localhost:8080/admin/logs?since=2026-10-24T05%3A28%3A00Z&until=2026-10-24T05%3A32%3A00Z"
        );
        // a time that does not read: the page as it is, with the search
        let odd = Attempt {
            at: "soon".into(),
            to: "1.0.0 beta&x=1".into(),
            ..Attempt::default()
        };
        assert_eq!(
            logs_url("http://h/", &odd),
            "http://h/admin/logs?q=1.0.0%20beta%26x%3D1"
        );
        assert_eq!(
            logs_url("http://h", &Attempt::default()),
            "http://h/admin/logs"
        );
    }

    #[test]
    fn the_log_keeps_one_line_per_event_and_rotates() {
        let dir = tempfile::tempdir().unwrap();
        let now = UNIX_EPOCH + Duration::from_secs(1_792_819_800);
        append_log(dir.path(), "update app-1: begin\nsecond line", now).unwrap();
        let text = std::fs::read_to_string(dir.path().join("app.log")).unwrap();
        assert_eq!(
            text,
            "2026-10-24T05:30:00Z update app-1: begin second line\n"
        );
        std::fs::write(
            dir.path().join("app.log"),
            vec![b'x'; (LOG_ROTATE_BYTES + 1) as usize],
        )
        .unwrap();
        append_log(dir.path(), "after", now).unwrap();
        assert!(dir.path().join("app.log.1").exists());
        assert_eq!(
            std::fs::read_to_string(dir.path().join("app.log")).unwrap(),
            "2026-10-24T05:30:00Z after\n"
        );
    }
}
