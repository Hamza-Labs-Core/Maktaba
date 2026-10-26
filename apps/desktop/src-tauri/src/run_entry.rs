// The tray at sign-in on Windows: the user's Run value "Maktaba"
// (HKCU\Software\Microsoft\Windows\CurrentVersion\Run), which starts the app
// with its window hidden in the tray (`Maktaba.exe --hidden`). The sign-in
// task starts only the server (agent_windows.rs): without this value nothing
// started the app after a reboot, and there was no tray icon.
//
// As the Linux app's autostart entry (agent_linux.rs): written once, when
// this PC is set up to run the server (setup, an adopted install), at the
// app's start in server mode and by the installer's --server-start (an
// update); afterwards rewritten only when it is ours and starts another
// program, and never put back after the user removed it (a marker beside the
// server's state says it was written once). Turned off in Task Manager's
// Startup apps (or Settings → Apps → Startup) it stays off: Windows keeps that
// in StartupApproved\Run under the same name, which a rewrite leaves alone.
// Removed, with that, when this PC no longer runs the server, and by the
// uninstaller (installer-hooks.nsh).
//
// The decisions are kept apart from the registry (Values) so every OS tests
// them; agent_windows.rs reads and writes the real one.
#![cfg_attr(not(windows), allow(dead_code))]

use std::path::Path;

use crate::agent::HIDDEN_FLAG;

/// The user's Run key, under HKEY_CURRENT_USER.
pub const RUN_KEY: &str = r"Software\Microsoft\Windows\CurrentVersion\Run";
/// Where Windows keeps a Run value's on/off (Task Manager's Startup apps).
pub const APPROVED_KEY: &str =
    r"Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run";
/// The value's name.
pub const VALUE: &str = "Maktaba";
/// The program a value of ours starts.
const PROGRAM_NAME: &str = "Maktaba.exe";

/// The user's registry values (tests use a stand-in).
pub trait Values {
    /// A string value, None when it is not there.
    fn get(&self, key: &str, name: &str) -> Option<String>;
    fn set(&self, key: &str, name: &str, data: &str) -> Result<(), String>;
    /// Remove a value; one that is not there is not an error.
    fn delete(&self, key: &str, name: &str) -> Result<(), String>;
}

/// The value's command line: the program, quoted, and the hidden flag.
pub fn command(program: &Path) -> String {
    format!("\"{}\" {HIDDEN_FLAG}", program.display())
}

/// A command line's program (quoted, or up to the first space) and what follows it.
fn split(cmd: &str) -> (&str, &str) {
    let cmd = cmd.trim();
    if let Some(rest) = cmd.strip_prefix('"') {
        return match rest.split_once('"') {
            Some((program, args)) => (program, args.trim()),
            None => (rest, ""),
        };
    }
    match cmd.split_once(' ') {
        Some((program, args)) => (program, args.trim()),
        None => (cmd, ""),
    }
}

/// Whether a program is the installed app (Maktaba.exe; a dev build, a test, is not).
fn is_app(program: &str) -> bool {
    let name = program.rsplit(['\\', '/']).next().unwrap_or(program);
    name.eq_ignore_ascii_case(PROGRAM_NAME)
}

/// Whether a Run value is this app's: Maktaba.exe with the hidden flag alone.
pub fn ours(cmd: &str) -> bool {
    let (program, args) = split(cmd);
    args == HIDDEN_FLAG && is_app(program)
}

/// What to do with the Run value.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Decision {
    Write,
    /// Leave it: the user removed it, it is not ours, or it starts this program already.
    Leave,
}

/// The value is written once; after that only when it is ours and starts
/// another program (an install moved), never back after the user removed it.
/// Only for the installed app: a dev build or a test writes nothing.
pub fn decision(existing: Option<&str>, written_before: bool, program: &Path) -> Decision {
    if !is_app(&program.to_string_lossy()) {
        return Decision::Leave;
    }
    let Some(cmd) = existing else {
        return if written_before {
            Decision::Leave
        } else {
            Decision::Write
        };
    };
    if !ours(cmd)
        || split(cmd)
            .0
            .eq_ignore_ascii_case(&program.to_string_lossy())
    {
        return Decision::Leave;
    }
    Decision::Write
}

/// Write the Run value when `decision` says so, and the marker (`written`) the
/// first time the value is ours. True when it was written.
pub fn sync(reg: &dyn Values, written: &Path, program: &Path) -> Result<bool, String> {
    let existing = reg.get(RUN_KEY, VALUE);
    let written_before = written.is_file();
    let d = decision(existing.as_deref(), written_before, program);
    if d == Decision::Write {
        reg.set(RUN_KEY, VALUE, &command(program))?;
    }
    // ours and there (written now, or before the marker existed): its removal
    // from now on is the user's
    let ours_now = d == Decision::Write || existing.as_deref().is_some_and(ours);
    if ours_now && !written_before {
        if let Some(dir) = written.parent() {
            std::fs::create_dir_all(dir).map_err(|e| format!("{}: {e}", dir.display()))?;
        }
        std::fs::write(
            written,
            "written by the Maktaba app; remove the Run value to keep it off\n",
        )
        .map_err(|e| format!("{}: {e}", written.display()))?;
    }
    Ok(d == Decision::Write)
}

/// Remove the Run value when it is ours (and Windows' on/off for it), and the
/// marker: this PC no longer runs the server, and a later setup writes it again.
pub fn remove(reg: &dyn Values, written: &Path) -> Result<(), String> {
    if reg.get(RUN_KEY, VALUE).is_some_and(|c| ours(&c)) {
        reg.delete(RUN_KEY, VALUE)?;
        reg.delete(APPROVED_KEY, VALUE)?;
    }
    match std::fs::remove_file(written) {
        Err(e) if e.kind() != std::io::ErrorKind::NotFound => {
            Err(format!("{}: {e}", written.display()))
        }
        _ => Ok(()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::RefCell;
    use std::collections::HashMap;
    use std::path::PathBuf;

    /// The registry as a map: (key, name) → data.
    #[derive(Default)]
    struct Mem(RefCell<HashMap<(String, String), String>>);

    impl Values for Mem {
        fn get(&self, key: &str, name: &str) -> Option<String> {
            self.0.borrow().get(&(key.into(), name.into())).cloned()
        }
        fn set(&self, key: &str, name: &str, data: &str) -> Result<(), String> {
            self.0
                .borrow_mut()
                .insert((key.into(), name.into()), data.into());
            Ok(())
        }
        fn delete(&self, key: &str, name: &str) -> Result<(), String> {
            self.0.borrow_mut().remove(&(key.into(), name.into()));
            Ok(())
        }
    }

    const APP: &str = r"C:\Users\me\AppData\Local\Maktaba\Maktaba.exe";

    fn app() -> PathBuf {
        PathBuf::from(APP)
    }

    #[test]
    fn the_command_starts_the_app_hidden() {
        assert_eq!(command(&app()), format!("\"{APP}\" --hidden"));
        assert!(ours(&command(&app())));
        assert!(ours(r#""D:\Apps\maktaba.EXE"  --hidden "#));
        assert!(ours(r"C:\Maktaba\Maktaba.exe --hidden"));
        // another program, or this one started another way, is not ours
        assert!(!ours(r#""C:\Other\Other.exe" --hidden"#));
        assert!(!ours(&format!("\"{APP}\"")));
        assert!(!ours(&format!("\"{APP}\" --hidden --other")));
        assert!(!ours(""));
    }

    #[test]
    fn it_is_written_once_and_follows_only_its_own_program() {
        let ours_now = command(&app());
        // the first time: written
        assert_eq!(decision(None, false, &app()), Decision::Write);
        // the user removed it since: it stays removed
        assert_eq!(decision(None, true, &app()), Decision::Leave);
        // it starts this program already (the case of the path is Windows')
        assert_eq!(decision(Some(&ours_now), true, &app()), Decision::Leave);
        let upper = command(Path::new(&APP.to_uppercase()));
        assert_eq!(decision(Some(&upper), true, &app()), Decision::Leave);
        // the install moved: it follows
        let moved = Path::new(r"D:\Maktaba\Maktaba.exe");
        assert_eq!(decision(Some(&ours_now), true, moved), Decision::Write);
        // another program's value of the same name is left
        let other = r#""C:\Other\Maktaba Helper.exe" /background"#;
        assert_eq!(decision(Some(other), false, &app()), Decision::Leave);
        // a dev build (target\debug\maktaba-desktop.exe) or a test writes nothing
        let dev = Path::new(r"E:\src\target\debug\maktaba-desktop.exe");
        assert_eq!(decision(None, false, dev), Decision::Leave);
        assert_eq!(decision(Some(&ours_now), true, dev), Decision::Leave);
    }

    #[test]
    fn sync_writes_the_value_and_the_marker_and_then_honours_a_removal() {
        let dir = tempfile::tempdir().unwrap();
        let marker = dir.path().join("state").join("autostart-written");
        let reg = Mem::default();
        assert!(sync(&reg, &marker, &app()).unwrap());
        assert_eq!(reg.get(RUN_KEY, VALUE), Some(command(&app())));
        assert!(marker.is_file());
        // a second start: nothing to do
        assert!(!sync(&reg, &marker, &app()).unwrap());
        // turned off in Task Manager (StartupApproved), then the install moved: the value
        // follows, the user's off stays
        reg.set(APPROVED_KEY, VALUE, "off").unwrap();
        let moved = Path::new(r"D:\Maktaba\Maktaba.exe");
        assert!(sync(&reg, &marker, moved).unwrap());
        assert_eq!(reg.get(RUN_KEY, VALUE), Some(command(moved)));
        assert_eq!(reg.get(APPROVED_KEY, VALUE).as_deref(), Some("off"));
        // the user deleted the value: it is never put back
        reg.delete(RUN_KEY, VALUE).unwrap();
        assert!(!sync(&reg, &marker, moved).unwrap());
        assert_eq!(reg.get(RUN_KEY, VALUE), None);
    }

    #[test]
    fn a_value_of_ours_from_before_the_marker_counts_as_written() {
        let dir = tempfile::tempdir().unwrap();
        let marker = dir.path().join("autostart-written");
        let reg = Mem::default();
        reg.set(RUN_KEY, VALUE, &command(&app())).unwrap();
        assert!(!sync(&reg, &marker, &app()).unwrap());
        assert!(marker.is_file(), "its removal from now on is the user's");
        // another program's value of that name: neither touched nor counted
        let dir2 = tempfile::tempdir().unwrap();
        let marker2 = dir2.path().join("autostart-written");
        let reg2 = Mem::default();
        reg2.set(RUN_KEY, VALUE, r#""C:\Other\Other.exe""#).unwrap();
        assert!(!sync(&reg2, &marker2, &app()).unwrap());
        assert_eq!(
            reg2.get(RUN_KEY, VALUE).as_deref(),
            Some(r#""C:\Other\Other.exe""#)
        );
        assert!(!marker2.exists());
    }

    #[test]
    fn remove_takes_out_only_its_own_value_and_lets_a_later_setup_write_it_again() {
        let dir = tempfile::tempdir().unwrap();
        let marker = dir.path().join("autostart-written");
        let reg = Mem::default();
        sync(&reg, &marker, &app()).unwrap();
        reg.set(APPROVED_KEY, VALUE, "off").unwrap();
        remove(&reg, &marker).unwrap();
        assert_eq!(reg.get(RUN_KEY, VALUE), None);
        assert_eq!(reg.get(APPROVED_KEY, VALUE), None);
        assert!(!marker.exists());
        // set up again: written again
        assert!(sync(&reg, &marker, &app()).unwrap());
        // another program's value is left
        let reg2 = Mem::default();
        reg2.set(RUN_KEY, VALUE, r#""C:\Other\Other.exe""#).unwrap();
        reg2.set(APPROVED_KEY, VALUE, "theirs").unwrap();
        remove(&reg2, &marker).unwrap();
        assert!(reg2.get(RUN_KEY, VALUE).is_some());
        assert!(reg2.get(APPROVED_KEY, VALUE).is_some());
        // nothing there: no error
        remove(&Mem::default(), &dir.path().join("none")).unwrap();
    }

    // The uninstaller (installer-hooks.nsh) removes this value: the same key, name and
    // command, its StartupApproved twin and the same marker (agent_windows.rs keeps it in
    // <state>\state, and the state root is the install folder), on a real uninstall only.
    #[test]
    fn the_uninstaller_removes_this_value() {
        let nsh = include_str!("../windows/installer-hooks.nsh").replace("\r\n", "\n");
        assert!(nsh.contains(&format!("!define MAKTABA_RUN_KEY \"{RUN_KEY}\"")));
        assert!(nsh.contains(&format!("!define MAKTABA_APPROVED_KEY \"{APPROVED_KEY}\"")));
        assert!(nsh.contains(&format!("!define MAKTABA_RUN_VALUE \"{VALUE}\"")));
        let installed = command(Path::new(r"$INSTDIR\${MAINBINARYNAME}.exe"));
        assert!(
            nsh.contains(&format!("${{If}} $R9 == '{installed}'")),
            "{nsh}"
        );
        assert!(nsh.contains(r#"Delete "$INSTDIR\state\autostart-written""#));
        let conf: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        assert_eq!(
            format!("{}.exe", conf["mainBinaryName"].as_str().unwrap()),
            PROGRAM_NAME
        );
        // in the uninstall's real branch (not the one an update runs), and nowhere else:
        // an install or an update keeps the value
        let pre = nsh.split("!macro NSIS_HOOK_PREUNINSTALL").nth(1).unwrap();
        let pre = pre.split("!macroend").next().unwrap();
        let real = pre.split("${If} $UpdateMode <> 1").nth(1).unwrap();
        assert!(real.contains("!insertmacro MAKTABA_REMOVE_RUN"));
        assert_eq!(nsh.matches("!insertmacro MAKTABA_REMOVE_RUN").count(), 1);
    }
}
