//! Windows (story 54.03 §4): the supervisor starts the host inside an AppContainer with no
//! capabilities (no internetClient, internetClientServer or privateNetworkClientServer)
//! and inside a Job Object (`cmd/maktaba-server/internal/supervisor/appcontainer_windows.go`).
//! The Windows Filtering Platform blocks every network path of such a container, and its
//! file access is only what folder ACLs grant its SID. The host checks that it really is in
//! an AppContainer and refuses to run otherwise.
//!
//! The host's own dispatch socket is AF_UNIX through Winsock, bound before this check, so
//! Winsock is loaded by design; the network refusal is the container's, which the escape
//! probes test on loopback, the LAN address and a public literal.

use windows_sys::Win32::Foundation::{CloseHandle, HANDLE};
use windows_sys::Win32::Security::{GetTokenInformation, TokenIsAppContainer, TOKEN_QUERY};
use windows_sys::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};

use super::{injected, unavailable, Applied, Paths, Unavailable};

pub fn in_app_container() -> bool {
    let mut token: HANDLE = std::ptr::null_mut();
    // SAFETY: opens this process's own token for query; closed below
    if unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token) } == 0 {
        return false;
    }
    let mut is_ac: u32 = 0;
    let mut len: u32 = 0;
    // SAFETY: TokenIsAppContainer answers a DWORD into is_ac
    let ok = unsafe {
        GetTokenInformation(
            token,
            TokenIsAppContainer,
            &mut is_ac as *mut u32 as *mut _,
            std::mem::size_of::<u32>() as u32,
            &mut len,
        )
    };
    // SAFETY: the handle opened above
    unsafe { CloseHandle(token) };
    ok != 0 && is_ac != 0
}

pub fn apply(_p: &Paths) -> Result<Applied, Unavailable> {
    injected("appcontainer")?;
    if !in_app_container() {
        return Err(unavailable("appcontainer", "the host is not running in an AppContainer"));
    }
    Ok(Applied { level: "full".into(), missing: vec![] })
}
