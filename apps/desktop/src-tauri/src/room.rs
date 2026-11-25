// Whether the app's own update fits on the disk, looked at before its server stops for it
// (Epic 13 story 13.14). Windows only: there the installer unpacks all it holds into %TEMP% and
// copies it from there, and it is the installer that decides (installer.nsi, MaktabaRoom): it
// refuses before it changes anything, but by then the app has stopped the server and closed. This
// spares the home that when there is clearly no room, and the attempt says the disk is full.
//
// What it needs on the temporary folder's drive: the installer's file, and what it unpacks. How
// much that is only the installer knows; the payload this machine runs is about as large (a
// release adds little to the one before), so a tenth less than that is asked for: an update that
// would fit is never refused here. The install folder's drive needs nothing more, the old payload
// going before the new one is copied.
#![cfg_attr(not(windows), allow(dead_code))]

use std::path::Path;

/// The bytes the files under `dir` hold (0 when there is none). Links are not followed.
pub fn size_of(dir: &Path) -> u64 {
    let Ok(entries) = std::fs::read_dir(dir) else {
        return 0;
    };
    entries
        .flatten()
        .map(|e| match e.file_type() {
            Ok(t) if t.is_dir() => size_of(&e.path()),
            Ok(t) if t.is_file() => e.metadata().map(|m| m.len()).unwrap_or(0),
            _ => 0,
        })
        .sum()
}

/// What an update needs free where it unpacks: its installer's file, and nine tenths of the
/// payload installed now.
pub fn needed(installer: u64, payload: u64) -> u64 {
    installer + (payload - payload / 10)
}

/// Why an update of `installer` bytes does not fit where `free` bytes are (None: unknown, and
/// nothing is refused for it), over a payload of `payload` bytes.
pub fn short(installer: u64, payload: u64, free: Option<u64>) -> Option<String> {
    let need = needed(installer, payload);
    let free = free?;
    (free < need).then(|| {
        format!(
            "the update needs about {} MB free where Windows unpacks it, and {} MB is free: \
             nothing was stopped or changed",
            need.div_ceil(1 << 20),
            free >> 20
        )
    })
}

/// Why this app's update of `installer` bytes does not fit, now.
#[cfg(windows)]
pub fn short_here(installer: u64) -> Option<String> {
    let payload = std::env::current_exe().ok()?.parent()?.join("server");
    let temp = std::env::temp_dir();
    let why = short(installer, size_of(&payload), free_at(&temp))?;
    Some(format!("{} ({})", why, temp.display()))
}

/// The bytes this user can still write on `dir`'s drive.
#[cfg(windows)]
fn free_at(dir: &Path) -> Option<u64> {
    use std::os::windows::ffi::OsStrExt;
    use windows_sys::Win32::Storage::FileSystem::GetDiskFreeSpaceExW;
    let wide: Vec<u16> = dir.as_os_str().encode_wide().chain([0]).collect();
    let (mut free, mut total) = (0u64, 0u64);
    // SAFETY: a NUL-terminated path and two numbers that outlive the call
    let ok =
        unsafe { GetDiskFreeSpaceExW(wide.as_ptr(), &mut free, &mut total, std::ptr::null_mut()) };
    // a network drive that gives no size
    (ok != 0 && total > 0).then_some(free)
}

#[cfg(test)]
mod tests {
    use super::*;

    const MB: u64 = 1 << 20;

    #[test]
    fn an_update_that_clearly_does_not_fit_says_how_much_it_needs() {
        // a 200 MB installer over a 583 MB payload: 725 MB
        let why = short(200 * MB, 583 * MB, Some(600 * MB)).unwrap();
        assert!(why.contains("about 725 MB"), "{why}");
        assert!(why.contains("600 MB is free"), "{why}");
        assert_eq!(short(200 * MB, 583 * MB, Some(726 * MB)), None);
    }

    #[test]
    fn a_drive_that_does_not_say_refuses_nothing() {
        assert_eq!(short(200 * MB, 583 * MB, None), None);
        // an app with no payload of its own asks for its installer only
        assert_eq!(short(100 * MB, 0, Some(100 * MB)), None);
        assert!(short(100 * MB, 0, Some(99 * MB)).is_some());
    }

    #[test]
    fn the_payload_is_its_files_not_what_its_links_point_to() {
        let d = tempfile::tempdir().unwrap();
        std::fs::create_dir_all(d.path().join("server/bin")).unwrap();
        std::fs::write(d.path().join("server/a"), [0u8; 10]).unwrap();
        std::fs::write(d.path().join("server/bin/b"), [0u8; 32]).unwrap();
        std::fs::write(d.path().join("beside"), [0u8; 99]).unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink(d.path().join("beside"), d.path().join("server/link")).unwrap();
        assert_eq!(size_of(&d.path().join("server")), 42);
        assert_eq!(size_of(&d.path().join("none")), 0);
    }
}
