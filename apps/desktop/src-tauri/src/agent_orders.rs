// The server agent installs the home's orders on Windows (owner, 2026-10-31: an update must
// not wait for the window app). The always-running `Maktaba.exe --server-agent` (the sign-in
// task's) answers the order the server writes (updates/app-install.json, autoinstall.rs) in
// the window app's place: ordered, it stages the release from the release's own feed
// (latest.json: the installer, checked with this app's key, the version its signature carries
// and the file it names checked against the order) and says staged; at the home's go it says installing
// and starts the installer out of its own kill-on-close job. The installer stops this agent
// (--server-stop), replaces the app and its payload, and starts the server again
// (--server-start); the new agent settles the attempt (attempts.rs). The decisions are
// autoinstall::decide's, the window app's own; the window app only shows the order
// (shell.rs). The server sends no maktaba:// link to wake a window (AGENT_ORDERS_ENV).
// Only the Windows agent runs it; elsewhere its decisions are only tested.
#![cfg_attr(not(windows), allow(dead_code))]

use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use base64::Engine as _;

use crate::attempts::{self, Failure};
use crate::autoinstall::{self, Action, Request};

/// What the agent tells the server it starts: the agent answers the orders, so the server
/// starts no window app with maktaba://install-update (cmd/maktaba-server serve_update.go).
pub const AGENT_ORDERS_ENV: &str = "MAKTABA_AGENT_ORDERS";

/// How often the order is read.
pub const EVERY: Duration = Duration::from_secs(2);

/// How long after a failure that could not be said in the order the same run is tried
/// again (never a download every look).
const RETRY_AFTER: Duration = Duration::from_secs(60);

/// The download's bounds, the window app's own (shell.rs DOWNLOAD_TIMEOUT, DOWNLOAD_STALL).
const DOWNLOAD_TIMEOUT: Duration = Duration::from_secs(45 * 60);
const DOWNLOAD_STALL: Duration = Duration::from_secs(120);

/// The installer's arguments: passive (its progress shows, nothing to answer), as an update
/// of this install (tauri's NSIS template keeps the state and the shortcuts). No restart of
/// the window app: the installer's --server-start brings the server back, and the tray comes
/// back at the next sign-in or when someone opens Maktaba.
pub const INSTALLER_ARGS: [&str; 2] = ["/P", "/UPDATE"];

/// The feed's keys for the Windows installer, the NSIS one first (tauri's latest.json).
const TARGETS: [&str; 2] = ["windows-x86_64-nsis", "windows-x86_64"];

/// What this build says of itself: tauri.conf.json as the release stamped it (its version
/// and updater key, desktop-release.yml) before this program was compiled.
#[derive(Debug, Clone, PartialEq)]
pub struct Build {
    pub version: String,
    pub pubkey: Option<String>,
    /// The app's own repository (autoinstall::own_repo of the updater's endpoints).
    pub repo: String,
    /// The updater refuses a signature that carries no version (requireSignedVersion), and
    /// so does the agent.
    pub require_signed_version: bool,
}

impl Build {
    pub fn here() -> Build {
        Build::of(include_str!("../tauri.conf.json"))
    }

    pub fn of(conf: &str) -> Build {
        let c: serde_json::Value = serde_json::from_str(conf).unwrap_or_default();
        let updater = &c["plugins"]["updater"];
        let endpoints: Vec<&str> = updater["endpoints"]
            .as_array()
            .map(|a| a.iter().filter_map(|v| v.as_str()).collect())
            .unwrap_or_default();
        Build {
            version: c["version"].as_str().unwrap_or_default().to_string(),
            pubkey: updater["pubkey"].as_str().map(String::from),
            repo: autoinstall::own_repo(endpoints),
            require_signed_version: updater["requireSignedVersion"].as_bool().unwrap_or(false),
        }
    }
}

/// What the agent does outside itself: the network, and starting the installer.
pub trait Io {
    /// The body at url (the feed, the installer).
    fn get(&self, url: &str) -> Result<Vec<u8>, String>;
    /// Starts the installer at path with args, to outlive this agent.
    fn start(&self, installer: &Path, args: &[&str]) -> Result<(), String>;
}

/// The feed's installer for version: its url, its file's name on the release page, and its
/// signature.
#[derive(Debug, Clone, PartialEq)]
pub struct Installer {
    pub url: String,
    pub name: String,
    pub signature: String,
}

/// A plain installer's file name: one path segment of letters, digits, '.', '_' and '-'
/// (no "..", no %-escape a URL parser would turn into one), ending in -setup.exe.
pub fn installer_name(name: &str) -> bool {
    !name.is_empty()
        && name.ends_with("-setup.exe")
        && !name.contains("..")
        && name
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '_' | '-'))
}

/// installer_of reads the release's feed: it must offer exactly version, with the Windows
/// installer on the app's own repository's release of it.
pub fn installer_of(feed: &[u8], version: &str, repo: &str) -> Result<Installer, Failure> {
    let bad = |why: String| Failure::new(attempts::STEP_CHECK, attempts::FEED_INVALID, why);
    let j: serde_json::Value =
        serde_json::from_slice(feed).map_err(|e| bad(format!("the feed is not JSON: {e}")))?;
    let offered = j["version"].as_str().unwrap_or_default();
    if !autoinstall::exactly(version, offered) {
        return Err(bad(format!("the feed offers {offered:?}, not {version}")));
    }
    let entry = TARGETS
        .iter()
        .map(|t| &j["platforms"][*t])
        .find(|e| e.is_object())
        .ok_or_else(|| {
            Failure::new(
                attempts::STEP_CHECK,
                attempts::ASSET_NOT_FOUND,
                format!("the feed of {version} has no Windows installer"),
            )
        })?;
    let url = entry["url"].as_str().unwrap_or_default().to_string();
    let signature = entry["signature"].as_str().unwrap_or_default().to_string();
    let v = version.strip_prefix('v').unwrap_or(version);
    let release = format!("{repo}/releases/download/v{v}/");
    let name = url.strip_prefix(&release).unwrap_or_default().to_string();
    if !installer_name(&name) || signature.is_empty() {
        return Err(bad(format!(
            "the feed's installer {url:?} is not a setup .exe on the release's own page ({release})"
        )));
    }
    Ok(Installer {
        url,
        name,
        signature,
    })
}

/// verify checks data against the feed's signature with this app's updater key, as the
/// app's updater does (minisign), and that what was signed is this version's installer:
/// the version its trusted comment carries (required when the updater requires it), and
/// the file it names, which must be `file` (the installer's name on the release page). The
/// feed itself is not signed: a feed pairing a version with another release's signed
/// file, or with another file of the release (the Player's .msi), is refused.
pub fn verify(
    data: &[u8],
    signature: &str,
    pubkey: &str,
    version: &str,
    file: &str,
    require_version: bool,
) -> Result<(), Failure> {
    let bad = |why: String| Failure::new(attempts::STEP_VERIFY, attempts::SIGNATURE_MISMATCH, why);
    let text = |b64: &str, what: &str| {
        base64::engine::general_purpose::STANDARD
            .decode(b64.trim())
            .ok()
            .and_then(|b| String::from_utf8(b).ok())
            .ok_or_else(|| bad(format!("the {what} is not base64 text")))
    };
    let key = minisign_verify::PublicKey::decode(&text(pubkey, "updater key")?)
        .map_err(|e| bad(format!("the updater key: {e}")))?;
    let sig = minisign_verify::Signature::decode(&text(signature, "signature")?)
        .map_err(|e| bad(format!("the signature: {e}")))?;
    key.verify(data, &sig, true)
        .map_err(|e| bad(format!("the installer does not match its signature: {e}")))?;
    // only now is the trusted comment the key's word
    let v = version.strip_prefix('v').unwrap_or(version);
    let comment = sig.trusted_comment();
    let field = |name: &str| {
        comment
            .split('\t')
            .find_map(|f| f.strip_prefix(name))
            .map(str::to_string)
    };
    match field("version:") {
        Some(signed) if !autoinstall::same_version(&signed, v) => {
            return Err(bad(format!(
                "the installer was signed for {signed}, not {v}"
            )));
        }
        None if require_version => {
            return Err(bad(format!("the signature of {file} carries no version")));
        }
        _ => {}
    }
    match field("file:") {
        Some(signed) if signed != file => {
            return Err(bad(format!("the signature is {signed}'s, not {file}'s")));
        }
        None if field("version:").is_none() => {
            return Err(bad("the signature names no version or file".into()));
        }
        _ => {}
    }
    Ok(())
}

/// The order's answers and the installs of one agent.
pub struct Orders<I: Io> {
    pub root: PathBuf,
    pub build: Build,
    pub io: I,
    /// whether the user paused the server here (an install would start it)
    pub paused: fn() -> bool,
    pub say: Box<dyn FnMut(&str) + Send>,
    /// the run whose installer this agent started: never started twice
    started: Option<String>,
    /// the run that last failed here, and when (RETRY_AFTER)
    failed_at: Option<(String, Instant)>,
}

impl<I: Io> Orders<I> {
    pub fn new(
        root: PathBuf,
        build: Build,
        io: I,
        paused: fn() -> bool,
        say: Box<dyn FnMut(&str) + Send>,
    ) -> Orders<I> {
        Orders {
            root,
            build,
            io,
            paused,
            say,
            started: None,
            failed_at: None,
        }
    }

    fn answer(&mut self, path: &Path, req: &Request, state: &str, error: &str) {
        if let Err(e) = autoinstall::write_state(path, req, state, error) {
            (self.say)(&format!(
                "the answer {state} to the order of {} could not be written: {e}",
                req.version
            ));
        }
    }

    /// advance answers the order's next step (staged, installing) only while it is still
    /// where it was read: a download's late "staged" never takes back the server's go.
    /// Whether the answer went in.
    fn advance(&mut self, path: &Path, req: &Request, state: &str) -> bool {
        match autoinstall::advance(path, req, state) {
            Ok(wrote) => wrote,
            Err(e) => {
                (self.say)(&format!(
                    "the answer {state} to the order of {} could not be written: {e}",
                    req.version
                ));
                false
            }
        }
    }

    fn failed(&mut self, path: &Path, req: &Request, f: &Failure) {
        self.failed_at = Some((req.run_id.clone(), Instant::now()));
        (self.say)(&format!(
            "the update to {} failed at {} ({}): {}",
            req.version, f.step, f.code, f.detail
        ));
        self.answer(
            path,
            req,
            autoinstall::FAILED,
            &autoinstall::order_error(f.code, &f.detail),
        );
    }

    /// One look at the order, and what it asks done.
    pub fn tick(&mut self) {
        let path = autoinstall::request_path(&self.root);
        let Some(req) = autoinstall::read(&path) else {
            // no order (withdrawn, or installed): what was staged goes; one that cannot be
            // read now (the server replacing it) is read again at the next look
            if !path.exists() {
                autoinstall::clean_staged(&self.root, None);
                clean_installers(&self.root);
            }
            return;
        };
        if self.started.as_deref() == Some(req.run_id.as_str()) {
            // its installer runs: it stops this agent
            return;
        }
        if let Some((run, at)) = &self.failed_at {
            if *run == req.run_id && at.elapsed() < RETRY_AFTER {
                return;
            }
        }
        let staged = autoinstall::staged_path(&self.root, &req.version);
        let decided = autoinstall::decide(
            Some(&req),
            true,
            (self.paused)(),
            staged.exists() && signature_of(&staged).exists(),
            false,
            &self.build.repo,
        );
        match decided {
            Action::Nothing => {}
            Action::Refuse(why) => {
                (self.say)(&format!("the order of {} is refused: {why}", req.version));
                self.answer(&path, &req, autoinstall::FAILED, &why);
            }
            Action::Show => {
                self.advance(&path, &req, autoinstall::STAGED);
            }
            Action::Stage { .. } => {
                autoinstall::clean_staged(&self.root, Some(&staged));
                let rec = attempts::path(&self.root);
                match self.stage(&req, &staged) {
                    Ok(()) => {
                        (self.say)(&format!("staged {} for the home's go", req.version));
                        self.advance(&path, &req, autoinstall::STAGED);
                    }
                    Err(f) => {
                        attempts::record_failed(
                            &rec,
                            attempts::TRIGGER_ORDER,
                            &self.build.version,
                            &req.version,
                            &f,
                        );
                        self.failed(&path, &req, &f);
                    }
                }
            }
            Action::Install { .. } => {
                if !self.advance(&path, &req, autoinstall::INSTALLING) {
                    // the order moved on meanwhile (withdrawn, replaced), or the answer could
                    // not be written (the go stays: looked at again)
                    return;
                }
                let rec = attempts::path(&self.root);
                let id = attempts::begin(
                    &rec,
                    attempts::TRIGGER_ORDER,
                    &self.build.version,
                    &req.version,
                );
                // the go came to an agent that has nothing staged (it lost it): staged now
                let staged_now = if staged.exists() && signature_of(&staged).exists() {
                    Ok(())
                } else {
                    autoinstall::clean_staged(&self.root, Some(&staged));
                    self.stage(&req, &staged)
                };
                if let Err(f) = staged_now {
                    attempts::fail(&rec, &id, &f);
                    self.failed(&path, &req, &f);
                    return;
                }
                attempts::set_step(&rec, &id, attempts::STEP_INSTALL);
                match self.install(&req, &staged) {
                    Ok(installer) => {
                        self.started = Some(req.run_id.clone());
                        (self.say)(&format!(
                            "the installer of {} started ({}): it stops the server, installs, \
                             and starts it again",
                            req.version,
                            installer.display()
                        ));
                    }
                    Err(f) => {
                        attempts::fail(&rec, &id, &f);
                        self.failed(&path, &req, &f);
                    }
                }
            }
        }
    }

    /// stage downloads the order's installer from the release's own feed, checks it, and
    /// keeps it with its signature (checked again before it runs).
    fn stage(&mut self, req: &Request, staged: &Path) -> Result<(), Failure> {
        let ctx = attempts::Ctx::here();
        let pubkey = self.pubkey()?;
        let feed = self
            .io
            .get(&req.feed)
            .map_err(|e| net_failure(attempts::STEP_CHECK, e, &ctx))?;
        let inst = installer_of(&feed, &req.version, &self.build.repo)?;
        (self.say)(&format!("downloading {} ({})", req.version, inst.url));
        let bytes = self
            .io
            .get(&inst.url)
            .map_err(|e| net_failure(attempts::STEP_DOWNLOAD, e, &ctx))?;
        verify(
            &bytes,
            &inst.signature,
            &pubkey,
            &req.version,
            &inst.name,
            self.build.require_signed_version,
        )?;
        let io = |e: std::io::Error| {
            let mut f = Failure::new(
                attempts::STEP_DOWNLOAD,
                attempts::DOWNLOAD_FAILED,
                e.to_string(),
            );
            f.code = attempts::classify_io(attempts::STEP_DOWNLOAD, &e, &ctx);
            f.fix = attempts::fix_for(f.code);
            f
        };
        let part = staged.with_extension("part");
        std::fs::write(&part, &bytes).map_err(io)?;
        let sidecar = serde_json::json!({"name": inst.name, "signature": inst.signature});
        std::fs::write(signature_of(staged), sidecar.to_string()).map_err(io)?;
        std::fs::rename(&part, staged).map_err(io)?;
        Ok(())
    }

    /// install checks the staged installer again and starts it: as an .exe of its own in
    /// the updates folder (Windows starts nothing else), out of this agent's job.
    fn install(&mut self, req: &Request, staged: &Path) -> Result<PathBuf, Failure> {
        let ctx = attempts::Ctx::here();
        let fail =
            |code: &'static str, why: String| Failure::new(attempts::STEP_INSTALL, code, why);
        let pubkey = self.pubkey()?;
        let bytes = std::fs::read(staged)
            .map_err(|e| fail(attempts::INSTALL_FAILED, format!("the staged update: {e}")))?;
        let sidecar: serde_json::Value = std::fs::read(signature_of(staged))
            .ok()
            .and_then(|raw| serde_json::from_slice(&raw).ok())
            .unwrap_or_default();
        let (name, signature) = (
            sidecar["name"].as_str().unwrap_or_default(),
            sidecar["signature"].as_str().unwrap_or_default(),
        );
        if !installer_name(name) || signature.is_empty() {
            return Err(fail(
                attempts::INSTALL_FAILED,
                "the staged update's name and signature cannot be read".into(),
            ));
        }
        verify(
            &bytes,
            signature,
            &pubkey,
            &req.version,
            name,
            self.build.require_signed_version,
        )?;
        let exe = staged.with_file_name(name);
        std::fs::write(&exe, &bytes).map_err(|e| {
            let code = attempts::classify_io(attempts::STEP_INSTALL, &e, &ctx);
            fail(code, format!("{}: {e}", exe.display()))
        })?;
        if !autoinstall::still_go(&autoinstall::request_path(&self.root), &req.run_id) {
            return Err(fail(
                attempts::INSTALL_FAILED,
                "the order was withdrawn".into(),
            ));
        }
        self.io.start(&exe, &INSTALLER_ARGS).map_err(|e| {
            fail(
                attempts::INSTALL_FAILED,
                format!("the installer did not start: {e}"),
            )
        })?;
        Ok(exe)
    }

    /// The updater key, or why this build takes no release (a local build: a person
    /// installs a release once).
    fn pubkey(&self) -> Result<String, Failure> {
        match self.build.pubkey.as_deref() {
            Some(k) if !attempts::is_local_build(Some(k)) => Ok(k.to_string()),
            _ => Err(attempts::local_build_failure(
                attempts::STEP_CHECK,
                &self.build.version,
            )),
        }
    }
}

/// The staged installer's name and signature, beside it.
pub fn signature_of(staged: &Path) -> PathBuf {
    staged.with_extension("sig")
}

/// A failure of the network, coded as the app's updater's are (its words: "status: 404").
fn net_failure(step: &'static str, e: String, ctx: &attempts::Ctx) -> Failure {
    Failure::new(step, attempts::classify_text(step, &e, ctx), e)
}

/// Removes the installers this agent wrote out of the updates folder (one still running
/// stays: Windows keeps it open).
fn clean_installers(root: &Path) {
    let Ok(dir) = std::fs::read_dir(root.join("updates")) else {
        return;
    };
    for e in dir.flatten() {
        let name = e.file_name().to_string_lossy().to_string();
        if installer_name(&name) {
            let _ = std::fs::remove_file(e.path());
        }
    }
}

/// Settles the newest update attempt at the agent's start: installed when this agent is
/// its release or newer (the installer it started took), failed otherwise.
pub fn settle(root: &Path, build: &Build) {
    let rec = attempts::path(root);
    let _ = attempts::settle_newest(&rec, &build.version, &attempts::Ctx::here(), None);
}

/// The network and the installer's start, for real. own_job: this agent holds its own
/// kill-on-close job (start_detached).
#[cfg(windows)]
pub struct Net {
    pub own_job: bool,
}

#[cfg(windows)]
impl Io for Net {
    fn get(&self, url: &str) -> Result<Vec<u8>, String> {
        // the updater's TLS: rustls on ring, set once per process
        if rustls::crypto::CryptoProvider::get_default().is_none() {
            let _ = rustls::crypto::ring::default_provider().install_default();
        }
        let url = url.to_string();
        tauri::async_runtime::block_on(async move {
            let client = reqwest::Client::builder()
                .user_agent(concat!("Maktaba-agent/", env!("CARGO_PKG_VERSION")))
                .connect_timeout(Duration::from_secs(30))
                // a download that gets no byte for DOWNLOAD_STALL has stalled
                .read_timeout(DOWNLOAD_STALL)
                .timeout(DOWNLOAD_TIMEOUT)
                .build()
                .map_err(|e| e.to_string())?;
            let res = client
                .get(&url)
                .send()
                .await
                .map_err(|e| attempts::error_chain(&e))?;
            let status = res.status();
            if !status.is_success() {
                return Err(format!("{url}: status: {status}"));
            }
            let body = res.bytes().await.map_err(|e| attempts::error_chain(&e))?;
            Ok(body.to_vec())
        })
    }

    fn start(&self, installer: &Path, args: &[&str]) -> Result<(), String> {
        start_detached(installer, args, self.own_job)
    }
}

/// start_detached starts exe out of this agent's kill-on-close job (own_job: which allows
/// it): the installer stops this agent, and must not end with it. An agent that holds no
/// job of its own (it could not make one) is only in a job that may refuse a breakaway (the
/// sign-in task's): there the installer starts where it is, and nothing ends it with the
/// agent. An agent in its own job never starts it inside that job (a refused breakaway
/// there is something else: an antivirus on the new .exe).
#[cfg(windows)]
pub fn start_detached(exe: &Path, args: &[&str], own_job: bool) -> Result<(), String> {
    use std::os::windows::process::CommandExt;
    use std::process::{Command, Stdio};
    const CREATE_BREAKAWAY_FROM_JOB: u32 = 0x0100_0000;
    const CREATE_NEW_PROCESS_GROUP: u32 = 0x0000_0200;
    const ACCESS_DENIED: i32 = 5;
    let spawn = |flags: u32| {
        Command::new(exe)
            .args(args)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .creation_flags(flags)
            .spawn()
    };
    match spawn(CREATE_BREAKAWAY_FROM_JOB | CREATE_NEW_PROCESS_GROUP) {
        Ok(_) => Ok(()),
        Err(e) if !own_job && e.raw_os_error() == Some(ACCESS_DENIED) => {
            spawn(CREATE_NEW_PROCESS_GROUP)
                .map(|_| ())
                .map_err(|e| format!("{}: {e}", exe.display()))
        }
        Err(e) => Err(format!("{}: {e}", exe.display())),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::RefCell;
    use std::collections::HashMap;
    use std::rc::Rc;

    // A throwaway minisign key (its secret thrown away), a second one, and signatures of BODY
    // made for these tests the way tauri's signer makes them (prehashed, "ED"; the trusted
    // comment as a release's: timestamp, file, version).
    const PUBKEY: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IDU5MDVEQjhFQ0M3QzVDQUEKUldTcVhIek1qdHNGV2FwM0NpaVNOSE9Fb2lCSSt4WVdLUFZ2NW13OGhjelZZNWpJS2psc3JYc2kK";
    const OTHER_KEY: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IDBCMUVCODJDOEUzNDVBOTEKUldTUldqU09MTGdlQzM1dzJLWWVKdk12MlFxUS95dHdEcmNCZURjNXhjbUJWS0U0Y1ZJbStNNzIK";
    const BODY: &[u8] = b"a stand-in installer for 0.9.1";
    // file:Maktaba_0.9.1_x64-setup.exe version:0.9.1
    const SIG_OK: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IHNpZ25hdHVyZSBmcm9tIHRhdXJpIHNlY3JldCBrZXkKUlVTcVhIek1qdHNGV1lhaHNLSFdUKzZDNGR3MDFscU1uaGhhTjVtRHZmYUdpTjE2dGJTWjdSaXBzcnNzdktoNU9hYjArbTUxUC9yK2o0Rzg1dm9DRm1Zb0h1VFdNc2hWd3c0PQp0cnVzdGVkIGNvbW1lbnQ6IHRpbWVzdGFtcDoxNzkzNDUwMDU2CWZpbGU6TWFrdGFiYV8wLjkuMV94NjQtc2V0dXAuZXhlCXZlcnNpb246MC45LjEKaDBnWXNYc1FjbWhxOFhOcUlLQW5YUzNLYk5yclpxb1B3R21uZm5Md3V5L09Gb0hLV3B0YnlPWVhuN0JUREExZ3FML0JRekRoNTFYeWN4bFY3MWpLQ0E9PQo=";
    // file:Maktaba_0.9.1_x64-setup.exe version:0.9.0
    const SIG_OLD_VERSION: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IHNpZ25hdHVyZSBmcm9tIHRhdXJpIHNlY3JldCBrZXkKUlVTcVhIek1qdHNGV1lhaHNLSFdUKzZDNGR3MDFscU1uaGhhTjVtRHZmYUdpTjE2dGJTWjdSaXBzcnNzdktoNU9hYjArbTUxUC9yK2o0Rzg1dm9DRm1Zb0h1VFdNc2hWd3c0PQp0cnVzdGVkIGNvbW1lbnQ6IHRpbWVzdGFtcDoxNzkzNDUwMDU2CWZpbGU6TWFrdGFiYV8wLjkuMV94NjQtc2V0dXAuZXhlCXZlcnNpb246MC45LjAKMUtMaVp0QVFwd1RpSzF5SnlTWlJYdjNTWWc2dTdYell5MVlnOEx2dWlkcTFxQlRtZFFsSTVZK2xwRnc3MytzLyszWjI0eEwrV3RBMzBMdWxGN0FBQ3c9PQo=";
    // file:Maktaba_0.9.1_x64-setup.exe, no version
    const SIG_NO_VERSION: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IHNpZ25hdHVyZSBmcm9tIHRhdXJpIHNlY3JldCBrZXkKUlVTcVhIek1qdHNGV1lhaHNLSFdUKzZDNGR3MDFscU1uaGhhTjVtRHZmYUdpTjE2dGJTWjdSaXBzcnNzdktoNU9hYjArbTUxUC9yK2o0Rzg1dm9DRm1Zb0h1VFdNc2hWd3c0PQp0cnVzdGVkIGNvbW1lbnQ6IHRpbWVzdGFtcDoxNzkzNDUwMDU2CWZpbGU6TWFrdGFiYV8wLjkuMV94NjQtc2V0dXAuZXhlCmc5cDJDVU4rclRveGxZVlV4Z0FyNjdJR2lxZHpRYVFjMEZrWmJIRUV2UUNHV0YxUHlKS0g1aEpnODZOM244VXgxcGs4WDc5ZlFXWFFPWmtZTExaNkJBPT0K";
    // file:Maktaba-Player_0.9.1_x64.msi version:0.9.1 (another file of the release)
    const SIG_PLAYER: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IHNpZ25hdHVyZSBmcm9tIHRhdXJpIHNlY3JldCBrZXkKUlVTcVhIek1qdHNGV1lhaHNLSFdUKzZDNGR3MDFscU1uaGhhTjVtRHZmYUdpTjE2dGJTWjdSaXBzcnNzdktoNU9hYjArbTUxUC9yK2o0Rzg1dm9DRm1Zb0h1VFdNc2hWd3c0PQp0cnVzdGVkIGNvbW1lbnQ6IHRpbWVzdGFtcDoxNzkzNDUwMDU2CWZpbGU6TWFrdGFiYS1QbGF5ZXJfMC45LjFfeDY0Lm1zaQl2ZXJzaW9uOjAuOS4xCjBtRURiTjRUdExEUUQ0empWRkd4Y0l3TVM4eDBwamNtSEJ5WURjcnZ1aFJJdVBQSWFjYlYyRFI4cTkrb0h3TUhWNTNhSXJHYkkzWHZ5NHBSc1lLWUJRPT0K";
    const NAME: &str = "Maktaba_0.9.1_x64-setup.exe";

    const REPO: &str = "https://github.com/Hamza-Labs-Core/Maktaba";
    const FEED: &str =
        "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v0.9.1/latest.json";
    const URL: &str =
        "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v0.9.1/Maktaba_0.9.1_x64-setup.exe";

    fn feed(version: &str, url: &str, sig: &str) -> Vec<u8> {
        serde_json::json!({
            "version": version,
            "platforms": {
                "darwin-aarch64": {"url": "https://example.com/mac", "signature": "x"},
                "windows-x86_64-nsis": {"url": url, "signature": sig},
            }
        })
        .to_string()
        .into_bytes()
    }

    /// The installers started: each one's path and arguments.
    type Started = Rc<RefCell<Vec<(PathBuf, Vec<String>)>>>;

    #[derive(Clone, Default)]
    struct Fake {
        bodies: Rc<RefCell<HashMap<String, Vec<u8>>>>,
        started: Started,
        fail_start: bool,
    }

    impl Io for Fake {
        fn get(&self, url: &str) -> Result<Vec<u8>, String> {
            self.bodies
                .borrow()
                .get(url)
                .cloned()
                .ok_or_else(|| format!("{url}: status: 404 Not Found"))
        }
        fn start(&self, installer: &Path, args: &[&str]) -> Result<(), String> {
            if self.fail_start {
                return Err("CreateProcess: denied".into());
            }
            self.started.borrow_mut().push((
                installer.to_path_buf(),
                args.iter().map(|a| a.to_string()).collect(),
            ));
            Ok(())
        }
    }

    // Orders is Send for the agent's thread (Box<dyn FnMut + Send>); the fake is not, which
    // the tests never need.
    struct Rig {
        dir: PathBuf,
        fake: Fake,
        orders: Orders<Fake>,
    }

    fn not_paused() -> bool {
        false
    }

    fn rig(name: &str, pubkey: Option<&str>) -> Rig {
        let dir = std::env::temp_dir().join(format!(
            "maktaba-agent-orders-{name}-{}",
            std::process::id()
        ));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(dir.join("updates")).unwrap();
        let fake = Fake::default();
        fake.bodies
            .borrow_mut()
            .insert(FEED.into(), feed("0.9.1", URL, SIG_OK));
        fake.bodies.borrow_mut().insert(URL.into(), BODY.to_vec());
        let build = Build {
            version: "0.9.0".into(),
            pubkey: pubkey.map(String::from),
            repo: REPO.into(),
            require_signed_version: true,
        };
        let orders = Orders::new(
            dir.clone(),
            build,
            fake.clone(),
            not_paused,
            Box::new(|_| {}),
        );
        Rig { dir, fake, orders }
    }

    impl Rig {
        fn order(&self, state: &str) {
            let r = Request {
                run_id: "run-1".into(),
                version: "0.9.1".into(),
                feed: FEED.into(),
                kind: autoinstall::KIND_APP.into(),
                state: state.into(),
                ..Request::default()
            };
            std::fs::write(
                autoinstall::request_path(&self.dir),
                serde_json::to_vec(&r).unwrap(),
            )
            .unwrap();
        }
        fn state(&self) -> Request {
            autoinstall::read(&autoinstall::request_path(&self.dir)).unwrap()
        }
    }

    impl Drop for Rig {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.dir);
        }
    }

    #[test]
    fn the_build_says_its_version_key_and_repository() {
        let b = Build::of(
            r#"{"version":"0.3.45","plugins":{"updater":{"pubkey":"K","requireSignedVersion":true,
            "endpoints":["https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/latest.json"]}}}"#,
        );
        assert_eq!(b.version, "0.3.45");
        assert_eq!(b.pubkey.as_deref(), Some("K"));
        assert_eq!(b.repo, REPO);
        assert!(b.require_signed_version);
        // this build's own config parses (include_str!), and requires the signed version
        let here = Build::here();
        assert!(!here.version.is_empty());
        assert_eq!(here.repo, REPO);
        assert!(here.require_signed_version);
    }

    #[test]
    fn the_feed_must_offer_exactly_the_order_on_its_own_release() {
        let got = installer_of(&feed("0.9.1", URL, SIG_OK), "0.9.1", REPO).unwrap();
        assert_eq!(got.url, URL);
        assert_eq!(got.name, NAME);
        assert_eq!(got.signature, SIG_OK);
        // the plain key when there is no NSIS one
        let plain = serde_json::json!({"version": "0.9.1",
            "platforms": {"windows-x86_64": {"url": URL, "signature": SIG_OK}}});
        assert_eq!(
            installer_of(plain.to_string().as_bytes(), "v0.9.1", REPO)
                .unwrap()
                .url,
            URL
        );

        let code = |f: &[u8]| installer_of(f, "0.9.1", REPO).unwrap_err().code;
        assert_eq!(code(&feed("0.9.2", URL, SIG_OK)), attempts::FEED_INVALID);
        assert_eq!(code(b"<html>"), attempts::FEED_INVALID);
        let other = URL.replace("Hamza-Labs-Core", "someone-else");
        assert_eq!(code(&feed("0.9.1", &other, SIG_OK)), attempts::FEED_INVALID);
        let older = URL.replace("v0.9.1/", "v0.9.0/");
        assert_eq!(code(&feed("0.9.1", &older, SIG_OK)), attempts::FEED_INVALID);
        assert_eq!(code(&feed("0.9.1", URL, "")), attempts::FEED_INVALID);
        // only a plain setup .exe on that page: not the Player's .msi, not a path a URL
        // parser would take elsewhere
        for bad in [
            "Maktaba-Player_0.9.1_x64.msi",
            "../../../../someone/else/releases/download/v1/x-setup.exe",
            "..%2F..%2Fx-setup.exe",
            "sub/Maktaba_0.9.1_x64-setup.exe",
            "Maktaba_0.9.1_x64-setup.exe?x=1",
            "",
        ] {
            let url = format!("{REPO}/releases/download/v0.9.1/{bad}");
            assert_eq!(
                code(&feed("0.9.1", &url, SIG_OK)),
                attempts::FEED_INVALID,
                "{bad}"
            );
        }
        let mac = serde_json::json!({"version": "0.9.1",
            "platforms": {"darwin-aarch64": {"url": URL, "signature": SIG_OK}}});
        assert_eq!(code(mac.to_string().as_bytes()), attempts::ASSET_NOT_FOUND);
    }

    #[test]
    fn the_installer_must_be_signed_with_this_apps_key_for_this_version() {
        verify(BODY, SIG_OK, PUBKEY, "0.9.1", NAME, true).unwrap();
        verify(BODY, SIG_OK, PUBKEY, "v0.9.1", NAME, true).unwrap();
        let code = |data: &[u8], sig: &str, key: &str, file: &str, require: bool| {
            verify(data, sig, key, "0.9.1", file, require)
                .unwrap_err()
                .code
        };
        let mismatch = attempts::SIGNATURE_MISMATCH;
        // another body
        assert_eq!(code(b"tampered", SIG_OK, PUBKEY, NAME, true), mismatch);
        // the right body, signed for another version
        assert_eq!(code(BODY, SIG_OLD_VERSION, PUBKEY, NAME, true), mismatch);
        assert_eq!(code(BODY, SIG_OLD_VERSION, PUBKEY, NAME, false), mismatch);
        // no version, where the updater requires one; accepted where it does not
        assert_eq!(code(BODY, SIG_NO_VERSION, PUBKEY, NAME, true), mismatch);
        verify(BODY, SIG_NO_VERSION, PUBKEY, "0.9.1", NAME, false).unwrap();
        // another file of the release, signed with the same key (the Player's .msi)
        assert_eq!(code(BODY, SIG_PLAYER, PUBKEY, NAME, true), mismatch);
        // another key
        assert_eq!(code(BODY, SIG_OK, OTHER_KEY, NAME, true), mismatch);
        assert_eq!(code(BODY, "not base64!", PUBKEY, NAME, true), mismatch);
    }

    #[test]
    fn a_late_answer_never_takes_back_the_servers_go() {
        let r = rig("late", Some(PUBKEY));
        r.order(autoinstall::ORDERED);
        let read_then = r.state();
        // the server's go came while the answer was on its way (a download)
        r.order(autoinstall::GO);
        let wrote = autoinstall::advance(
            &autoinstall::request_path(&r.dir),
            &read_then,
            autoinstall::STAGED,
        )
        .unwrap();
        assert!(!wrote);
        assert_eq!(r.state().state, autoinstall::GO);
        // and an answer to the state it was read at goes in
        let wrote = autoinstall::advance(
            &autoinstall::request_path(&r.dir),
            &r.state(),
            autoinstall::INSTALLING,
        )
        .unwrap();
        assert!(wrote);
        assert_eq!(r.state().state, autoinstall::INSTALLING);
    }

    #[test]
    fn an_order_is_staged_then_installed_at_the_go_once() {
        let mut r = rig("flow", Some(PUBKEY));
        r.order(autoinstall::ORDERED);
        r.orders.tick();
        assert_eq!(
            r.state().state,
            autoinstall::STAGED,
            "{:?}",
            r.state().error
        );
        let staged = autoinstall::staged_path(&r.dir, "0.9.1");
        assert_eq!(std::fs::read(&staged).unwrap(), BODY);
        let sidecar: serde_json::Value =
            serde_json::from_slice(&std::fs::read(signature_of(&staged)).unwrap()).unwrap();
        assert_eq!(
            (sidecar["name"].as_str(), sidecar["signature"].as_str()),
            (Some(NAME), Some(SIG_OK))
        );
        assert!(
            r.fake.started.borrow().is_empty(),
            "nothing starts before the go"
        );

        // staged and still waiting: nothing again
        r.orders.tick();
        assert_eq!(r.state().state, autoinstall::STAGED);

        r.order(autoinstall::GO);
        r.orders.tick();
        assert_eq!(r.state().state, autoinstall::INSTALLING);
        let started = r.fake.started.borrow().clone();
        assert_eq!(started.len(), 1);
        assert_eq!(
            started[0].0,
            r.dir.join("updates").join("Maktaba_0.9.1_x64-setup.exe")
        );
        assert_eq!(std::fs::read(&started[0].0).unwrap(), BODY);
        assert_eq!(started[0].1, vec!["/P", "/UPDATE"]);
        // the attempt is at its install: the next agent settles it
        let a = attempts::newest(&attempts::path(&r.dir)).unwrap();
        assert_eq!(
            (a.step.as_str(), a.to.as_str()),
            (attempts::STEP_INSTALL, "0.9.1")
        );

        // the go seen again (the server wrote it back): never a second installer
        r.order(autoinstall::GO);
        r.orders.tick();
        assert_eq!(r.fake.started.borrow().len(), 1);
    }

    #[test]
    fn a_go_with_nothing_staged_stages_then_installs() {
        let mut r = rig("unstaged", Some(PUBKEY));
        r.order(autoinstall::GO);
        r.orders.tick();
        assert_eq!(
            r.state().state,
            autoinstall::INSTALLING,
            "{:?}",
            r.state().error
        );
        assert_eq!(r.fake.started.borrow().len(), 1);
    }

    #[test]
    fn an_order_that_cannot_be_read_now_keeps_what_was_staged() {
        let mut r = rig("unreadable", Some(PUBKEY));
        r.order(autoinstall::ORDERED);
        r.orders.tick();
        let staged = autoinstall::staged_path(&r.dir, "0.9.1");
        assert!(staged.exists());
        // the server half way through replacing it
        std::fs::write(autoinstall::request_path(&r.dir), b"{\"run_id\": \"run-").unwrap();
        r.orders.tick();
        assert!(
            staged.exists(),
            "a half-written order withdrew the staged update"
        );
        std::fs::remove_file(autoinstall::request_path(&r.dir)).unwrap();
        r.orders.tick();
        assert!(!staged.exists(), "a withdrawn order kept its staged update");
    }

    #[test]
    fn a_staged_installer_changed_since_is_not_started() {
        let mut r = rig("tampered", Some(PUBKEY));
        r.order(autoinstall::ORDERED);
        r.orders.tick();
        std::fs::write(autoinstall::staged_path(&r.dir, "0.9.1"), b"something else").unwrap();
        r.order(autoinstall::GO);
        r.orders.tick();
        let s = r.state();
        assert_eq!(s.state, autoinstall::FAILED);
        assert!(
            s.error.starts_with(attempts::SIGNATURE_MISMATCH),
            "{}",
            s.error
        );
        assert!(r.fake.started.borrow().is_empty());
    }

    #[test]
    fn what_cannot_be_staged_or_started_is_said_in_the_order() {
        // the feed is gone
        let mut r = rig("nofeed", Some(PUBKEY));
        r.fake.bodies.borrow_mut().remove(FEED);
        r.order(autoinstall::ORDERED);
        r.orders.tick();
        let s = r.state();
        assert_eq!(s.state, autoinstall::FAILED);
        assert!(s.error.starts_with(attempts::FEED_NOT_FOUND), "{}", s.error);
        let a = attempts::newest(&attempts::path(&r.dir)).unwrap();
        assert_eq!(a.code, attempts::FEED_NOT_FOUND);

        // the installer does not start
        let mut r = rig("nostart", Some(PUBKEY));
        r.order(autoinstall::ORDERED);
        r.orders.tick();
        r.orders.io.fail_start = true;
        r.order(autoinstall::GO);
        r.orders.tick();
        let s = r.state();
        assert_eq!(s.state, autoinstall::FAILED);
        assert!(s.error.starts_with(attempts::INSTALL_FAILED), "{}", s.error);
        let a = attempts::newest(&attempts::path(&r.dir)).unwrap();
        assert_eq!(
            (a.outcome.as_str(), a.code.as_str()),
            (attempts::FAILED, attempts::INSTALL_FAILED)
        );
    }

    #[test]
    fn a_local_build_says_so_and_takes_nothing() {
        for key in [None, Some(attempts::PLACEHOLDER_PUBKEY)] {
            let mut r = rig("local", key);
            r.order(autoinstall::ORDERED);
            r.orders.tick();
            let s = r.state();
            assert_eq!(s.state, autoinstall::FAILED);
            assert!(s.error.starts_with(attempts::LOCAL_BUILD), "{}", s.error);
            assert!(!autoinstall::staged_path(&r.dir, "0.9.1").exists());
        }
    }

    #[test]
    fn an_order_for_another_repository_is_refused() {
        let mut r = rig("refused", Some(PUBKEY));
        let mut req = Request {
            run_id: "run-1".into(),
            version: "0.9.1".into(),
            feed: FEED.replace("Hamza-Labs-Core", "someone-else"),
            kind: autoinstall::KIND_APP.into(),
            state: autoinstall::ORDERED.into(),
            ..Request::default()
        };
        std::fs::write(
            autoinstall::request_path(&r.dir),
            serde_json::to_vec(&req).unwrap(),
        )
        .unwrap();
        r.orders.tick();
        assert_eq!(r.state().state, autoinstall::FAILED);
        assert!(r.fake.started.borrow().is_empty());
        // and a withdrawn order cleans what was staged
        req.feed = FEED.into();
        std::fs::write(
            autoinstall::request_path(&r.dir),
            serde_json::to_vec(&req).unwrap(),
        )
        .unwrap();
        r.orders.tick();
        assert!(autoinstall::staged_path(&r.dir, "0.9.1").exists());
        std::fs::remove_file(autoinstall::request_path(&r.dir)).unwrap();
        r.orders.tick();
        assert!(!autoinstall::staged_path(&r.dir, "0.9.1").exists());
    }

    // The installer outlives the agent that started it: the agent's own kill-on-close job
    // (BREAKAWAY_OK, as run_agent's) closes with it, and the installer, started out of it,
    // goes on. The agent here is this test binary run again (MAKTABA_TEST_AGENT_SPAWN).
    #[cfg(windows)]
    #[test]
    fn the_installer_outlives_the_agent_that_started_it() {
        const ENV: &str = "MAKTABA_TEST_AGENT_SPAWN";
        if let Some(marker) = std::env::var_os(ENV) {
            // the agent: in a kill-on-close job, it starts the "installer" and exits
            crate::agent::join_job_for_tests().expect("the agent's job");
            let marker = PathBuf::from(marker);
            let cmd = std::env::var_os("ComSpec").unwrap_or_else(|| "cmd.exe".into());
            // (no quotes: Rust would escape them as \", which cmd.exe does not read; the
            // temporary folder has no space)
            let script = format!(
                "ping -n 4 127.0.0.1 >nul & echo outlived>{}",
                marker.display()
            );
            start_detached(Path::new(&cmd), &["/c", &script], true).expect("start the installer");
            std::process::exit(0);
        }
        let dir = std::env::temp_dir().join(format!("maktaba-agent-spawn-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let marker = dir.join("marker.txt");
        let status = std::process::Command::new(std::env::current_exe().unwrap())
            .args([
                "--exact",
                "agent::orders::tests::the_installer_outlives_the_agent_that_started_it",
                "--nocapture",
            ])
            .env(ENV, &marker)
            .status()
            .unwrap();
        assert!(status.success(), "the agent: {status}");
        let end = std::time::Instant::now() + Duration::from_secs(30);
        while !marker.exists() && std::time::Instant::now() < end {
            std::thread::sleep(Duration::from_millis(200));
        }
        let got = std::fs::read_to_string(&marker).unwrap_or_default();
        let _ = std::fs::remove_dir_all(&dir);
        assert!(
            got.contains("outlived"),
            "the installer ended with its agent"
        );
    }
}
