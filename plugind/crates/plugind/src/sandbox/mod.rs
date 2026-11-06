//! The OS sandbox the host puts itself in (story 54.03, README §5.3).
//!
//! Applied in `main` after the dispatch socket is bound and the folders exist, before any
//! plugin is read and before any thread starts. A required layer that fails ends the
//! process with exit code 78 and `sandbox_unavailable:<step>`; the supervisor reports
//! `plugins.host` Failed with reason `sandbox_unavailable` and does not restart it.

use std::path::PathBuf;

#[cfg(target_os = "linux")]
mod linux;
#[cfg(target_os = "macos")]
mod macos;
#[cfg(windows)]
mod windows;

/// The exit code of a host that could not sandbox itself (EX_CONFIG).
pub const EXIT_UNAVAILABLE: i32 = 78;

/// What the sandbox may reach.
#[derive(Debug, Clone)]
pub struct Paths {
    /// read only
    pub plugins_dir: PathBuf,
    /// read and write
    pub cache_dir: PathBuf,
    /// read and write: the host's own run folder (its dispatch socket)
    pub run_dir: PathBuf,
    /// the plugin API's socket: the one connection out
    pub api_socket: PathBuf,
    pub dispatch_socket: PathBuf,
    /// the jobs the pool runs at once (sizes RLIMIT_AS on Linux)
    #[cfg_attr(not(target_os = "linux"), allow(dead_code))]
    pub pool_size: usize,
}

/// What was applied.
#[derive(Debug, Clone, Default)]
pub struct Applied {
    /// "full" or "partial"
    pub level: String,
    /// the layers that could not be applied on this machine (sandbox_partial)
    pub missing: Vec<String>,
}

/// A required layer failed: the step names it.
#[derive(Debug)]
pub struct Unavailable {
    pub step: String,
    pub detail: String,
}

impl std::fmt::Display for Unavailable {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "sandbox_unavailable:{}: {}", self.step, self.detail)
    }
}

pub fn unavailable(step: &str, detail: impl ToString) -> Unavailable {
    Unavailable { step: step.into(), detail: detail.to_string() }
}

/// Test hook (story 54.03 T05): `MAKTABA_PLUGIND_FAIL_STEP=<step>` makes that step fail,
/// so each OS's refusal path is exercised. It can only stop the host from starting, which
/// whoever sets its environment could do anyway, so every build honours it.
pub fn injected(step: &str) -> Result<(), Unavailable> {
    if std::env::var("MAKTABA_PLUGIND_FAIL_STEP").as_deref() == Ok(step) {
        return Err(unavailable(step, "failure injected by a test"));
    }
    Ok(())
}

/// Applies the sandbox of this OS.
pub fn apply(p: &Paths) -> Result<Applied, Unavailable> {
    #[cfg(target_os = "linux")]
    return linux::apply(p);
    #[cfg(target_os = "macos")]
    return macos::apply(p);
    #[cfg(windows)]
    return windows::apply(p);
    #[allow(unreachable_code)]
    Err(unavailable("os", "no sandbox for this operating system"))
}
