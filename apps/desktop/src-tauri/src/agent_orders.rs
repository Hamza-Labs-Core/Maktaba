// The server agent installs the home's orders on Windows (owner, 2026-10-31: an update must
// not wait for the window app). The always-running `Maktaba.exe --server-agent` (the sign-in
// task's) answers the order the server writes (updates/app-install.json, autoinstall.rs) in
// the window app's place: ordered, it stages the release from the release's own feed
// (latest.json: the installer, checked with this app's key, and the file its signature names
// checked against the order's version) and says staged; at the home's go it says installing
// and starts the installer out of its own kill-on-close job. The installer stops this agent
// (--server-stop), replaces the app and its payload, and starts the server again
// (--server-start); the new agent settles the attempt (attempts.rs). The decisions are
// autoinstall::decide's, the window app's own; the window app only shows the order
// (shell.rs). The server sends no maktaba:// link to wake a window (AGENT_ORDERS_ENV).
// Only the Windows agent runs it; elsewhere its decisions are only tested.
#![cfg_attr(not(windows), allow(dead_code))]

use std::path::{Path, PathBuf};
use std::time::Duration;

use base64::Engine as _;

use crate::attempts::{self, Failure};
use crate::autoinstall::{self, Action, Request};

/// What the agent tells the server it starts: the agent answers the orders, so the server
/// starts no window app with maktaba://install-update (cmd/maktaba-server serve_update.go).
pub const AGENT_ORDERS_ENV: &str = "MAKTABA_AGENT_ORDERS";

/// How often the order is read.
pub const EVERY: Duration = Duration::from_secs(2);

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

/// The feed's installer for version: its url and signature.
#[derive(Debug, Clone, PartialEq)]
pub struct Installer {
    pub url: String,
    pub signature: String,
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
    if !url.starts_with(&release) || url.len() == release.len() || signature.is_empty() {
        return Err(bad(format!(
            "the feed's installer {url:?} is not on the release's own page ({release})"
        )));
    }
    Ok(Installer { url, signature })
}

/// verify checks data against the feed's signature with this app's updater key, as the
/// app's updater does (minisign), and that what was signed is this version's installer:
/// the version its trusted comment carries, else the file it names (Maktaba_<v>_…): the
/// feed itself is not signed, so a feed pairing a version with another release's signed
/// installer is refused.
pub fn verify(data: &[u8], signature: &str, pubkey: &str, version: &str) -> Result<(), Failure> {
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
    let v = version.strip_prefix('v').unwrap_or(version);
    let comment = sig.trusted_comment();
    let field = |name: &str| {
        comment
            .split('\t')
            .find_map(|f| f.strip_prefix(name))
            .map(str::to_string)
    };
    if let Some(signed) = field("version:") {
        if !autoinstall::same_version(&signed, v) {
            return Err(bad(format!(
                "the installer was signed for {signed}, not {v}"
            )));
        }
    } else if let Some(file) = field("file:") {
        if !file.contains(&format!("_{v}_")) {
            return Err(bad(format!(
                "the signature is {file}'s, not {v}'s installer"
            )));
        }
    } else {
        return Err(bad("the signature names no version or file".into()));
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

    fn failed(&mut self, path: &Path, req: &Request, f: &Failure) {
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
            Action::Show => self.answer(&path, &req, autoinstall::STAGED, ""),
            Action::Stage { .. } => {
                autoinstall::clean_staged(&self.root, Some(&staged));
                let rec = attempts::path(&self.root);
                match self.stage(&req, &staged) {
                    Ok(()) => {
                        (self.say)(&format!("staged {} for the home's go", req.version));
                        self.answer(&path, &req, autoinstall::STAGED, "");
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
                if !autoinstall::still_go(&path, &req.run_id) {
                    return;
                }
                self.answer(&path, &req, autoinstall::INSTALLING, "");
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
        verify(&bytes, &inst.signature, &pubkey, &req.version)?;
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
        std::fs::write(signature_of(staged), &inst.signature).map_err(io)?;
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
        let signature = std::fs::read_to_string(signature_of(staged)).map_err(|e| {
            fail(
                attempts::INSTALL_FAILED,
                format!("the staged signature: {e}"),
            )
        })?;
        verify(&bytes, &signature, &pubkey, &req.version)?;
        let v: String = req
            .version
            .strip_prefix('v')
            .unwrap_or(&req.version)
            .chars()
            .filter(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-'))
            .collect();
        let exe = staged.with_file_name(format!("Maktaba_{v}_x64-setup.exe"));
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

/// The staged installer's signature, beside it.
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
        if name.starts_with("Maktaba_") && name.ends_with("_x64-setup.exe") {
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

/// The network and the installer's start, for real.
#[cfg(windows)]
pub struct Net;

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
                // a download that gets no byte for a minute has stalled
                .read_timeout(Duration::from_secs(60))
                .timeout(Duration::from_secs(30 * 60))
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
        start_detached(installer, args)
    }
}

/// start_detached starts exe out of this agent's kill-on-close job (which allows it): the
/// installer stops this agent, and must not end with it. An agent that holds no job of its
/// own (it could not make one) is only in a job that may refuse a breakaway (the sign-in
/// task's): the installer starts there, where nothing ends it with the agent.
#[cfg(windows)]
pub fn start_detached(exe: &Path, args: &[&str]) -> Result<(), String> {
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
        Err(e) if e.raw_os_error() == Some(ACCESS_DENIED) => spawn(CREATE_NEW_PROCESS_GROUP)
            .map(|_| ())
            .map_err(|e| format!("{}: {e}", exe.display())),
        Err(e) => Err(format!("{}: {e}", exe.display())),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::RefCell;
    use std::collections::HashMap;
    use std::rc::Rc;

    // A throwaway minisign key (its secret thrown away) and signatures of BODY, made for
    // these tests the way tauri's signer makes them (prehashed, "ED").
    const PUBKEY: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IEU0RUEyNjM4RkM3ODk5RDgKUldUWW1YajhPQ2JxNUxyYjlUeFh6M0QwdE5CK3BrZ25Iemd2NC9CWE1OODhoZjlxSTRjdE14dzQK";
    const BODY: &[u8] = b"a stand-in installer for 0.9.1";
    // trusted comment: timestamp:1793450056\tfile:Maktaba_0.9.1_x64-setup.exe
    const SIG_OK: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IHNpZ25hdHVyZSBmcm9tIHRhdXJpIHNlY3JldCBrZXkKUlVUWW1YajhPQ2JxNVBHVDV3OHlPSDZ2eW8rOHRMV0E3eUNTcE95UEFCajZXTXZlSmtvenVuKzdPSlZLc3VnYWVaaWFFVjNZV1c3SmwvYUMzK1YvR1AranNtNGhOem15V3cwPQp0cnVzdGVkIGNvbW1lbnQ6IHRpbWVzdGFtcDoxNzkzNDUwMDU2CWZpbGU6TWFrdGFiYV8wLjkuMV94NjQtc2V0dXAuZXhlClZmdEpsWDkraG9rRHRKNDNYOURpZzMvUVZIV0p1WjI3R3BYR2NmRXZlMDR5VndjUjBQSmZpRXBFeXRUL2F2eVJ6Um1SQnhmMWVLSVpQUTlCem9WaUFRPT0K";
    // trusted comment: timestamp:1793450056\tfile:Maktaba_0.9.0_x64-setup.exe
    const SIG_OLD_FILE: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IHNpZ25hdHVyZSBmcm9tIHRhdXJpIHNlY3JldCBrZXkKUlVUWW1YajhPQ2JxNVBHVDV3OHlPSDZ2eW8rOHRMV0E3eUNTcE95UEFCajZXTXZlSmtvenVuKzdPSlZLc3VnYWVaaWFFVjNZV1c3SmwvYUMzK1YvR1AranNtNGhOem15V3cwPQp0cnVzdGVkIGNvbW1lbnQ6IHRpbWVzdGFtcDoxNzkzNDUwMDU2CWZpbGU6TWFrdGFiYV8wLjkuMF94NjQtc2V0dXAuZXhlCkFNTGR0WlpBNi9IMTlHL0N5R1loaEF5VmFWV3REeVVyNDBoR2toNnlqcFoyYTJGQWVtcmE5S0hYRnZhei9OT3NmMG1PME13amVISjZHSnpOYkc1cEJRPT0K";
    // trusted comment: timestamp:1793450056\tfile:Maktaba_0.9.1_x64-setup.exe\tversion:0.9.0
    const SIG_OLD_VERSION: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IHNpZ25hdHVyZSBmcm9tIHRhdXJpIHNlY3JldCBrZXkKUlVUWW1YajhPQ2JxNVBHVDV3OHlPSDZ2eW8rOHRMV0E3eUNTcE95UEFCajZXTXZlSmtvenVuKzdPSlZLc3VnYWVaaWFFVjNZV1c3SmwvYUMzK1YvR1AranNtNGhOem15V3cwPQp0cnVzdGVkIGNvbW1lbnQ6IHRpbWVzdGFtcDoxNzkzNDUwMDU2CWZpbGU6TWFrdGFiYV8wLjkuMV94NjQtc2V0dXAuZXhlCXZlcnNpb246MC45LjAKNERSU0F5MzFtWEQwdm9SRnZRMVM5R21TN0pZcENralB5VlpZUjVuWVd5SE9WbDhzdzBjU1JJQ1Q3dzN4Yk9Hd1JKU05VMGF2aXczU2luaExadnhTREE9PQo=";

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
            r#"{"version":"0.3.45","plugins":{"updater":{"pubkey":"K",
            "endpoints":["https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/latest.json"]}}}"#,
        );
        assert_eq!(b.version, "0.3.45");
        assert_eq!(b.pubkey.as_deref(), Some("K"));
        assert_eq!(b.repo, REPO);
        // this build's own config parses (include_str!)
        let here = Build::here();
        assert!(!here.version.is_empty());
        assert_eq!(here.repo, REPO);
    }

    #[test]
    fn the_feed_must_offer_exactly_the_order_on_its_own_release() {
        let got = installer_of(&feed("0.9.1", URL, SIG_OK), "0.9.1", REPO).unwrap();
        assert_eq!(got.url, URL);
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
        let mac = serde_json::json!({"version": "0.9.1",
            "platforms": {"darwin-aarch64": {"url": URL, "signature": SIG_OK}}});
        assert_eq!(code(mac.to_string().as_bytes()), attempts::ASSET_NOT_FOUND);
    }

    #[test]
    fn the_installer_must_be_signed_with_this_apps_key_for_this_version() {
        verify(BODY, SIG_OK, PUBKEY, "0.9.1").unwrap();
        verify(BODY, SIG_OK, PUBKEY, "v0.9.1").unwrap();
        let code = |data: &[u8], sig: &str, key: &str, v: &str| {
            verify(data, sig, key, v).unwrap_err().code
        };
        // another body
        assert_eq!(
            code(b"tampered", SIG_OK, PUBKEY, "0.9.1"),
            attempts::SIGNATURE_MISMATCH
        );
        // the right body, signed as another release's installer
        assert_eq!(
            code(BODY, SIG_OLD_FILE, PUBKEY, "0.9.1"),
            attempts::SIGNATURE_MISMATCH
        );
        assert_eq!(
            code(BODY, SIG_OLD_VERSION, PUBKEY, "0.9.1"),
            attempts::SIGNATURE_MISMATCH
        );
        // another key (a second throwaway one)
        let other = "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IERGODM4QjZFNDY4MTAyM0EKUldRNkFvRkdib3VEMzhVaXJuNFMraVMxa2lkS0lSNmIxNmZoNzVzR20xd3pXOVN5UndzeTE2Q1kK";
        assert_eq!(
            code(BODY, SIG_OK, other, "0.9.1"),
            attempts::SIGNATURE_MISMATCH
        );
        assert_eq!(
            code(BODY, "not base64!", PUBKEY, "0.9.1"),
            attempts::SIGNATURE_MISMATCH
        );
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
        assert_eq!(
            std::fs::read_to_string(signature_of(&staged)).unwrap(),
            SIG_OK
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
            start_detached(Path::new(&cmd), &["/c", &script]).expect("start the installer");
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
