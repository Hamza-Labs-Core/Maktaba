//! Unix domain sockets on every OS: std's on macOS and Linux, AF_UNIX through Winsock on
//! Windows 10 and later (`uds_windows`). The host's only sockets are these: the dispatch
//! listener and connections to the plugin API (README §5.1).

use std::io;
use std::path::Path;
use std::time::Duration;

#[cfg(unix)]
pub use std::os::unix::net::{UnixListener, UnixStream};
#[cfg(windows)]
pub use uds_windows::{UnixListener, UnixStream};

pub fn connect(path: &Path) -> io::Result<UnixStream> {
    UnixStream::connect(path)
}

pub fn set_timeouts(s: &UnixStream, d: Duration) -> io::Result<()> {
    s.set_read_timeout(Some(d))?;
    s.set_write_timeout(Some(d))
}

/// Binds the dispatch socket, replacing a stale file from a crashed run, readable and
/// writable by the server's user only (0600 on Unix; on Windows the run folder's ACL).
pub fn bind(path: &Path) -> io::Result<UnixListener> {
    match std::fs::remove_file(path) {
        Ok(()) => {}
        Err(e) if e.kind() == io::ErrorKind::NotFound => {}
        Err(e) => return Err(e),
    }
    let l = UnixListener::bind(path)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o600))?;
    }
    Ok(l)
}
