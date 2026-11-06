//! macOS: the Seatbelt profile `plugind.sb`, applied with `sandbox_init_with_parameters`
//! (the private API Chromium's renderers use; story 54.03 §2). The profile is compiled
//! into the binary from `plugind/resources/plugind.sb`, so the file a signed binary
//! applies cannot be swapped beside it.

use std::ffi::{c_char, c_int, CStr, CString};

use super::{injected, unavailable, Applied, Paths, Unavailable};

pub const PROFILE: &str = include_str!("../../../../resources/plugind.sb");

extern "C" {
    fn sandbox_init_with_parameters(
        profile: *const c_char,
        flags: u64,
        parameters: *const *const c_char,
        errorbuf: *mut *mut c_char,
    ) -> c_int;
    fn sandbox_free_error(errorbuf: *mut c_char);
}

fn real(p: &std::path::Path) -> String {
    // Seatbelt matches the resolved path (/var is /private/var)
    p.canonicalize().unwrap_or_else(|_| p.to_path_buf()).to_string_lossy().into_owned()
}

/// The socket paths may not exist yet (the API's): resolve the folder and keep the name.
fn real_file(p: &std::path::Path) -> String {
    match (p.parent(), p.file_name()) {
        (Some(dir), Some(name)) => format!("{}/{}", real(dir), name.to_string_lossy()),
        _ => p.to_string_lossy().into_owned(),
    }
}

pub fn apply(p: &Paths) -> Result<Applied, Unavailable> {
    injected("seatbelt")?;
    let params = [
        ("PLUGINS_DIR", real(&p.plugins_dir)),
        ("CACHE_DIR", real(&p.cache_dir)),
        ("RUN_DIR", real(&p.run_dir)),
        ("API_SOCKET", real_file(&p.api_socket)),
        ("DISPATCH_SOCKET", real_file(&p.dispatch_socket)),
    ];
    let mut owned = Vec::new();
    for (k, v) in params {
        owned.push(CString::new(k).map_err(|e| unavailable("seatbelt", e))?);
        owned.push(CString::new(v).map_err(|e| unavailable("seatbelt", e))?);
    }
    let mut ptrs: Vec<*const c_char> = owned.iter().map(|c| c.as_ptr()).collect();
    ptrs.push(std::ptr::null());
    let profile = CString::new(PROFILE).map_err(|e| unavailable("seatbelt", e))?;
    let mut err: *mut c_char = std::ptr::null_mut();
    // SAFETY: every pointer is a live NUL-terminated string, the list ends with NULL, and
    // errorbuf is freed with sandbox_free_error.
    let rc = unsafe { sandbox_init_with_parameters(profile.as_ptr(), 0, ptrs.as_ptr(), &mut err) };
    if rc != 0 {
        let msg = if err.is_null() {
            format!("rc {rc}")
        } else {
            // SAFETY: set by sandbox_init_with_parameters on failure
            let m = unsafe { CStr::from_ptr(err) }.to_string_lossy().into_owned();
            unsafe { sandbox_free_error(err) };
            m
        };
        return Err(unavailable("seatbelt", msg));
    }
    Ok(Applied { level: "full".into(), missing: vec![] })
}
