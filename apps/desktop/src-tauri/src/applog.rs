// What goes wrong in the shell outside an update, kept in app.log beside server.log
// (attempts::log: one line per event, rotated at 1 MB). The errors audit (2026-10-27,
// specs/ERROR_LOGGING_AUDIT.md) found the shell's panics and the failures its background
// threads can only note going nowhere: a Windows release has no console for eprintln!, a
// panic ended its thread without a word, and one panic while a lock was held poisoned it,
// so every later tray click and status poll panicked too and the tray froze.
//
// - panic_hook: every panic, any thread, is a line in app.log (then the default hook).
// - Locked::locked: a lock a panic poisoned is taken anyway (the state it guards is plain
//   data the next write replaces), so one panic never takes the tray down with it.
// - keep_going: a loop's round or a tray click that panics is logged and the loop goes on.
// - warn / error: a failure with its context and its level.
use std::any::Any;
use std::panic::AssertUnwindSafe;
use std::sync::{Mutex, MutexGuard};

/// A failure the app goes on after (a state not saved, a check that failed).
pub fn warn(what: &str, err: impl std::fmt::Display) {
    crate::attempts::log(&line("warn", what, &err.to_string()));
}

/// A failure that stops what it was part of.
pub fn error(what: &str, err: impl std::fmt::Display) {
    crate::attempts::log(&line("error", what, &err.to_string()));
}

/// "<level>: <what>: <err>" (just "<level>: <what>" when the error says nothing).
fn line(level: &str, what: &str, err: &str) -> String {
    let err = err.trim();
    if err.is_empty() {
        format!("{level}: {what}")
    } else {
        format!("{level}: {what}: {err}")
    }
}

/// Every panic is logged in app.log before the default hook runs (which prints it where
/// there is a console). Installed first thing in lib::run, in every mode.
pub fn install_panic_hook() {
    let previous = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        let thread = std::thread::current();
        let at = info
            .location()
            .map(|l| format!("{}:{}", l.file(), l.line()));
        crate::attempts::log(&panic_line(thread.name(), info.payload(), at.as_deref()));
        previous(info);
    }));
}

/// The panic's line: its thread, its message and where in the source it was.
fn panic_line(thread: Option<&str>, payload: &(dyn Any + Send), at: Option<&str>) -> String {
    let msg = payload
        .downcast_ref::<&str>()
        .copied()
        .or_else(|| payload.downcast_ref::<String>().map(String::as_str))
        .unwrap_or("(no message)");
    let mut out = format!(
        "error: panic in thread {}: {msg}",
        thread.unwrap_or("unnamed")
    );
    if let Some(at) = at {
        out.push_str(&format!(" (at {at})"));
    }
    out
}

/// Runs `f`; a panic in it is caught (the hook logged it), noted as stopping `what`, and
/// the caller goes on. False when it panicked.
pub fn keep_going(what: &str, f: impl FnOnce()) -> bool {
    match std::panic::catch_unwind(AssertUnwindSafe(f)) {
        Ok(()) => true,
        Err(_) => {
            error(what, "stopped by a panic (above); the app goes on");
            false
        }
    }
}

/// A mutex's guard, poisoned or not.
pub trait Locked<T> {
    fn locked(&self) -> MutexGuard<'_, T>;
}

impl<T> Locked<T> for Mutex<T> {
    fn locked(&self) -> MutexGuard<'_, T> {
        self.lock().unwrap_or_else(|poisoned| poisoned.into_inner())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;

    #[test]
    fn a_line_has_its_level_its_context_and_the_error() {
        assert_eq!(
            line(
                "warn",
                "the order's state",
                "Permission denied (os error 13)"
            ),
            "warn: the order's state: Permission denied (os error 13)"
        );
        assert_eq!(line("error", "the tray", "  "), "error: the tray");
    }

    #[test]
    fn a_panic_line_says_its_thread_message_and_place() {
        let s: Box<dyn Any + Send> = Box::new("index out of bounds");
        assert_eq!(
            panic_line(Some("main"), s.as_ref(), Some("src/shell.rs:12")),
            "error: panic in thread main: index out of bounds (at src/shell.rs:12)"
        );
        let owned: Box<dyn Any + Send> = Box::new(String::from("called `Option::unwrap()`"));
        assert_eq!(
            panic_line(None, owned.as_ref(), None),
            "error: panic in thread unnamed: called `Option::unwrap()`"
        );
        let other: Box<dyn Any + Send> = Box::new(42u8);
        assert_eq!(
            panic_line(Some("t"), other.as_ref(), None),
            "error: panic in thread t: (no message)"
        );
    }

    #[test]
    fn keep_going_survives_a_panic() {
        let mut ran = false;
        assert!(keep_going("a round", || ran = true));
        assert!(ran);
        assert!(!keep_going("a round", || panic!("boom")));
        // and the next round runs
        assert!(keep_going("a round", || {}));
    }

    #[test]
    fn a_poisoned_lock_is_still_taken() {
        let m = Arc::new(Mutex::new(1));
        let m2 = m.clone();
        let _ = std::thread::spawn(move || {
            let _g = m2.lock().unwrap();
            panic!("poison it");
        })
        .join();
        assert!(m.is_poisoned());
        *m.locked() += 1;
        assert_eq!(*m.locked(), 2);
    }
}
