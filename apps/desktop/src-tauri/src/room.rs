// Whether the app's own update fits on the disk, looked at before its server stops for it
// (Epic 13 story 13.14). Windows only: there the installer unpacks all it holds into %TEMP% and
// copies it from there, and it is the installer that decides (installer.nsi, MaktabaRoom): it
// refuses before it changes anything, but by then the app has stopped the server and closed.
//
// This asks for what that installer will ask for, and never more: what it unpacks is in its own
// version information (UNPACKED_KEY), read from the file the app holds; of the old payload it
// counts all that is there, where the installer counts what it will delete or write over. One
// whose file does not say (an older release's) is left to it.
#![cfg_attr(not(windows), allow(dead_code))]

use std::path::Path;

/// The installer's version information, in KB (installer.nsi VIAddVersionKey).
pub const UNPACKED_KEY: &str = "MaktabaUnpackedKB";
/// What it keeps beside what it copies (installer.nsi MK_ROOM_MARGIN).
const MARGIN_KB: u64 = 65_536;

/// More than the installer's own program takes (its icons and its version information in it).
const PROGRAM: usize = 8 << 20;

/// What `installer` unpacks, in KB, as its version information says (None: it does not). The
/// key and its value are UTF-16 text in the file, the value after the key's zero and the zeros
/// that align it, in the program at the file's start (what it installs comes after).
pub fn unpacked_kb(installer: &[u8]) -> Option<u64> {
    let installer = &installer[..installer.len().min(PROGRAM)];
    let key: Vec<u8> = UNPACKED_KEY
        .encode_utf16()
        .chain([0])
        .flat_map(u16::to_le_bytes)
        .collect();
    let at = installer.windows(key.len()).position(|w| w == key)? + key.len();
    let mut digits = String::new();
    for c in installer[at..].chunks_exact(2).take(64) {
        match u16::from_le_bytes([c[0], c[1]]) {
            0 if digits.is_empty() => {}
            c @ 0x30..=0x39 => digits.push(char::from(c as u8)),
            _ => break,
        }
    }
    digits.parse().ok()
}

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

/// A folder's drive: what is free there (None: unknown, and nothing is refused for it), and its
/// volume's number (None: unknown, as no other).
#[derive(Debug, Clone, Copy, Default)]
pub struct Drive {
    pub free: Option<u64>,
    pub volume: Option<u32>,
}

/// Why an update does not fit, in words, as its installer will find it: `file` bytes of installer
/// to write to `temp` first, `unpacked` KB in it, `old` bytes installed in `install`.
pub fn short(file: u64, unpacked: u64, old: u64, temp: Drive, install: Drive) -> Option<String> {
    let margin = MARGIN_KB << 10;
    let new = unpacked << 10;
    let same = temp.volume.is_some() && temp.volume == install.volume;
    // the installer looks with its own file on %TEMP%'s drive already
    let there = new + margin + if same { new } else { 0 };
    let on_install = there.saturating_sub(old).max(margin) + if same { file } else { 0 };
    let looks = [
        (!same).then_some((new + margin + file, temp.free)),
        Some((on_install, install.free)),
    ];
    looks.into_iter().flatten().find_map(|(need, free)| {
        let free = free?;
        (free < need).then(|| {
            format!(
                "the update needs {} MB free, and {} MB is: nothing was stopped or changed",
                need.div_ceil(1 << 20),
                free >> 20
            )
        })
    })
}

/// Why this app's update, an installer of `file` bytes that unpacks `unpacked` KB, does not fit, now.
#[cfg(windows)]
pub fn short_here(file: u64, unpacked: u64) -> Option<String> {
    let exe = std::env::current_exe().ok()?;
    let dir = exe.parent()?;
    let old = size_of(&dir.join("server")) + std::fs::metadata(&exe).map(|m| m.len()).unwrap_or(0);
    let temp = std::env::temp_dir();
    short(file, unpacked, old, drive(&temp), drive(dir))
}

/// What Windows says of `dir`'s drive.
#[cfg(windows)]
fn drive(dir: &Path) -> Drive {
    use std::os::windows::ffi::OsStrExt;
    use std::ptr::null_mut;
    use windows_sys::Win32::Storage::FileSystem::{
        GetDiskFreeSpaceExW, GetVolumeInformationW, GetVolumePathNameW,
    };
    let wide: Vec<u16> = dir.as_os_str().encode_wide().chain([0]).collect();
    let (mut free, mut total) = (0u64, 0u64);
    // SAFETY: a NUL-terminated path and two numbers that outlive the call
    let ok = unsafe { GetDiskFreeSpaceExW(wide.as_ptr(), &mut free, &mut total, null_mut()) };
    let mut root = [0u16; 1024];
    let mut serial = 0u32;
    // SAFETY: NUL-terminated paths, a buffer of the length given, and a number, all of which
    // outlive the calls; what is not asked for is null
    let named = unsafe {
        GetVolumePathNameW(wide.as_ptr(), root.as_mut_ptr(), root.len() as u32) != 0
            && GetVolumeInformationW(
                root.as_ptr(),
                null_mut(),
                0,
                &mut serial,
                null_mut(),
                null_mut(),
                null_mut(),
                0,
            ) != 0
    };
    Drive {
        // a network drive that gives no size
        free: (ok != 0 && total > 0).then_some(free),
        volume: named.then_some(serial),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const MB: u64 = 1 << 20;

    fn on(volume: u32, free: u64) -> Drive {
        Drive {
            free: Some(free),
            volume: Some(volume),
        }
    }

    /// CI's sizes: a 160 MB installer of 584 MB over a 566 MB install.
    fn update(temp: Drive, install: Drive) -> Option<String> {
        short(160 * MB, 584 << 10, 566 * MB, temp, install)
    }

    #[test]
    fn on_one_drive_the_old_payload_makes_room_for_the_new() {
        // 160 + 2 × 584 + 64 − 566
        let at = |free: u64| update(on(1, free * MB), on(1, free * MB));
        assert_eq!(at(826), None);
        let why = at(825).unwrap();
        assert!(why.contains("826 MB free"), "{why}");
        assert!(why.contains("and 825 MB is"), "{why}");
    }

    #[test]
    fn the_temporary_folder_on_another_drive_is_asked_for_the_whole() {
        let roomy = on(2, 9000 * MB);
        // the temporary drive: 160 + 584 + 64
        assert_eq!(update(on(1, 808 * MB), roomy), None);
        assert!(update(on(1, 807 * MB), roomy).is_some());
        // the install's: 584 + 64 − 566
        assert_eq!(update(roomy, on(1, 82 * MB)), None);
        assert!(update(roomy, on(1, 81 * MB)).is_some());
        // a release smaller than the old payload asks for the margin, no more
        assert_eq!(
            short(9 * MB, 100 << 10, 566 * MB, on(2, 173 * MB), on(1, 64 * MB)),
            None
        );
    }

    #[test]
    fn a_drive_that_does_not_say_refuses_nothing() {
        let none = Drive::default();
        assert_eq!(update(none, none), None);
        // two unknown volumes are not one
        let free = Drive {
            free: Some(808 * MB),
            volume: None,
        };
        assert_eq!(update(free, free), None);
    }

    /// The bytes a resource compiler makes of a version string, among others.
    fn installer(value: &str) -> Vec<u8> {
        let utf16 = |s: &str| -> Vec<u8> {
            s.encode_utf16()
                .chain([0])
                .flat_map(u16::to_le_bytes)
                .collect()
        };
        let mut b = vec![0x4d, 0x5a, 0x90];
        b.extend(utf16("ProductVersion"));
        b.extend(utf16("0.3.170"));
        b.extend(utf16(UNPACKED_KEY));
        b.extend([0, 0]);
        b.extend(utf16(value));
        b.extend([7u8; 40]);
        b
    }

    #[test]
    fn what_it_unpacks_is_read_from_the_installer_s_own_file() {
        assert_eq!(unpacked_kb(&installer("597712")), Some(597_712));
        // not a number, or an installer that does not say: it decides itself
        assert_eq!(unpacked_kb(&installer("much")), None);
        assert_eq!(unpacked_kb(b"MZ another program"), None);
        let cut = installer("597712");
        let end = cut.len() - 40 - 14;
        assert_eq!(unpacked_kb(&cut[..end]), None);
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
