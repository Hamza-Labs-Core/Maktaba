// The shell's own words, in English and Arabic (Epic 38 story 38.4, Epic 39
// story 39.8): the tray, the error and information dialogs, and on Linux the
// window's Server menu and the message for an app older than the server.
//
// The shell keeps no language of its own: the window's web app keeps the
// user's choice in its own storage, which the shell cannot read before the
// window opens (or at all, in client mode, or for a dialog without a window).
// So the shell follows the system's UI language, read once when the app
// starts: Windows' display language, the Mac's preferred languages, gettext's
// LANGUAGE / LC_ALL / LC_MESSAGES / LANG on Linux. The first of those the
// shell has words for wins; English when none. (The desktop entries the Linux
// shell writes carry both languages, Name[ar]=, whatever the locale.)

/// A language the tray speaks.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Lang {
    En,
    Ar,
}

impl Lang {
    /// The system's UI language.
    pub fn system() -> Lang {
        pick(system_tags())
    }

    pub fn words(self) -> &'static Words {
        match self {
            Lang::En => &EN,
            Lang::Ar => &AR,
        }
    }
}

/// The first of the user's languages (BCP 47 or POSIX tags, most wanted
/// first: `ar-SA`, `ar_EG.UTF-8`, `en`) the tray has words for; English when
/// none is.
pub fn pick<S: AsRef<str>>(tags: impl IntoIterator<Item = S>) -> Lang {
    for tag in tags {
        let primary = tag
            .as_ref()
            .trim()
            .split(['-', '_', '.', '@'])
            .next()
            .unwrap_or_default()
            .to_ascii_lowercase();
        match primary.as_str() {
            "ar" => return Lang::Ar,
            "en" => return Lang::En,
            _ => {}
        }
    }
    Lang::En
}

#[cfg(windows)]
fn system_tags() -> Vec<String> {
    // SAFETY: takes no arguments and only returns the user's display language.
    let id = unsafe { windows_sys::Win32::Globalization::GetUserDefaultUILanguage() };
    langid_tag(id).map(String::from).into_iter().collect()
}

/// The tag of a Windows LANGID's primary language (its low 10 bits), for the
/// languages the tray speaks.
#[cfg(any(test, windows))]
fn langid_tag(id: u16) -> Option<&'static str> {
    match id & 0x3ff {
        0x01 => Some("ar"), // LANG_ARABIC: ar-SA, ar-EG, ar-AE, …
        0x09 => Some("en"), // LANG_ENGLISH
        _ => None,
    }
}

#[cfg(target_os = "macos")]
fn system_tags() -> Vec<String> {
    objc2_foundation::NSLocale::preferredLanguages()
        .to_vec()
        .iter()
        .map(|tag| tag.to_string())
        .collect()
}

#[cfg(all(unix, not(target_os = "macos")))]
fn system_tags() -> Vec<String> {
    posix_tags(|name| std::env::var(name).ok())
}

#[cfg(not(any(windows, unix)))]
fn system_tags() -> Vec<String> {
    Vec::new()
}

/// The languages gettext would use for messages: LANGUAGE's list (unless the
/// locale is C or POSIX, where gettext ignores it), then the first of LC_ALL,
/// LC_MESSAGES and LANG that is set.
#[cfg(any(test, all(unix, not(target_os = "macos"))))]
fn posix_tags(var: impl Fn(&str) -> Option<String>) -> Vec<String> {
    let locale = ["LC_ALL", "LC_MESSAGES", "LANG"]
        .into_iter()
        .find_map(|name| var(name).filter(|v| !v.trim().is_empty()));
    let mut tags = Vec::new();
    if let Some(list) = var("LANGUAGE") {
        if locale
            .as_deref()
            .is_some_and(|l| !matches!(l.trim(), "C" | "POSIX"))
        {
            tags.extend(
                list.split(':')
                    .filter(|t| !t.trim().is_empty())
                    .map(String::from),
            );
        }
    }
    tags.extend(locale);
    tags
}

/// One language's tray words. `{name}` is filled in by `fill`. The two lines
/// that carry the server's own detail (its English: what a process waits on)
/// are None in a language other than English: that tray says the status in
/// its own words and leaves the English out.
#[derive(Debug)]
pub struct Words {
    /// The app's name: the tooltip of a tray that runs no server.
    pub brand: &'static str,
    pub setup_needed: &'static str,
    pub paused: &'static str,
    pub stopped: &'static str,
    pub starting: &'static str,
    /// `{detail}`: what the server waits on.
    pub starting_on: Option<&'static str>,
    pub running: &'static str,
    /// `{roles}` cannot start, `{detail}` why.
    pub cannot_start: Option<&'static str>,
    /// `{roles}` cannot start, in a language that leaves the detail out.
    pub cannot_start_plain: &'static str,
    /// `{roles}` keep stopping.
    pub keeps_stopping: &'static str,
    /// `{reason}`: the line of server.log that says why, or `no_answer`.
    pub has_a_problem: &'static str,
    /// The reason when the log names none (status::NO_ANSWER).
    pub no_answer: &'static str,
    pub update_ready: &'static str,
    pub open: &'static str,
    pub change_server: &'static str,
    pub resume: &'static str,
    pub pause: &'static str,
    pub open_logs: &'static str,
    pub play: &'static str,
    pub pause_playback: &'static str,
    pub settings: &'static str,
    pub quit_server_runs: &'static str,
    pub stop_and_quit: &'static str,
    pub quit: &'static str,
    /// The language model's line: `{name}` downloading, with `{pct}` done.
    pub model_downloading: &'static str,
    pub model_downloading_pct: &'static str,
    pub model_ready: &'static str,
    pub model_starting: &'static str,
    pub model_failed: &'static str,
    pub model_missing: &'static str,
    // The error dialogs: a sentence, then the program's own error as it is.
    // The words of the dialogs only Windows shows (an update that failed once
    // the server was asked to stop, the sign-in task) exist only there.
    /// The update failed (its error follows).
    pub update_failed: &'static str,
    /// The server did not stop for the update.
    pub update_stop_failed: &'static str,
    /// After a failed update: a paused server.
    #[cfg(windows)]
    pub update_stays_paused: &'static str,
    /// After a failed update: the server starts again.
    #[cfg(windows)]
    pub update_starting_again: &'static str,
    /// After a failed update: it could not start again.
    #[cfg(windows)]
    pub update_start_failed: &'static str,
    /// `{url}` could not be opened in the window.
    pub open_failed: &'static str,
    /// The sign-in task of the old shared name could not become this user's.
    #[cfg(windows)]
    pub task_move_failed: &'static str,
    /// The server could not start when the window opened (macOS: its login item
    /// could not be registered for this build).
    #[cfg(any(windows, target_os = "linux", target_os = "macos"))]
    pub server_start_failed: &'static str,
    /// Linux: the server could not be switched to this app's payload.
    #[cfg(target_os = "linux")]
    pub switch_failed: &'static str,
    /// Linux: this app (`{app}`) is older than the server (`{server}`), which
    /// keeps running (agent::linux::older_message; its tests run everywhere).
    #[cfg(any(target_os = "linux", test))]
    pub older_app: &'static str,
    /// Pause server failed.
    pub pause_failed: &'static str,
    /// Resume server failed.
    pub resume_failed: &'static str,
    /// Stop server and quit failed.
    pub stop_failed: &'static str,
    /// Open logs failed.
    pub logs_failed: &'static str,
    /// Connect to another Maktaba… failed.
    pub setup_failed: &'static str,
    /// Any other tray item failed.
    pub action_failed: &'static str,
    // Linux without a tray: the window's Server menu (tray::WINDOW_ITEMS).
    /// The menu's title.
    pub window_menu: &'static str,
    /// Its update item: what it does from a menu.
    pub install_update: &'static str,
    /// The release the home ordered (owner decision D1), ready to install with a click:
    /// on the home itself, and on a machine the cluster's update waits for.
    pub order_home: &'static str,
    pub order_node: &'static str,
    /// Someone clicked: it installs when the cluster is ready.
    pub order_clicked: &'static str,
    /// The OS notification once the home's order is ready to install (one per order):
    /// on the home itself, and on a machine the cluster's update waits for.
    pub order_ready_home: &'static str,
    pub order_ready_node: &'static str,
    /// Between the names in a list (the roles in a status line).
    pub list_sep: &'static str,
}

pub static EN: Words = Words {
    brand: "Maktaba",
    setup_needed: "Maktaba: finish setup to start the server",
    paused: "Maktaba is paused",
    stopped: "Maktaba is stopped",
    starting: "Maktaba is starting…",
    starting_on: Some("Maktaba is starting: {detail}"),
    running: "Maktaba is running",
    cannot_start: Some("Maktaba: {roles} cannot start ({detail})"),
    cannot_start_plain: "Maktaba: {roles} cannot start",
    keeps_stopping: "Maktaba: {roles} keeps stopping",
    has_a_problem: "Maktaba has a problem: {reason}",
    no_answer: "the server does not answer",
    update_ready: "Update ready: restart to install",
    open: "Open Maktaba",
    change_server: "Connect to another Maktaba…",
    resume: "Resume server",
    pause: "Pause server",
    open_logs: "Open logs",
    play: "Play",
    pause_playback: "Pause playback",
    settings: "Settings",
    quit_server_runs: "Quit (server keeps running)",
    stop_and_quit: "Stop server and quit",
    quit: "Quit",
    model_downloading: "Language model: downloading {name}",
    model_downloading_pct: "Language model: downloading {name} ({pct}%)",
    model_ready: "Language model: ready",
    model_starting: "Language model: starting…",
    model_failed: "Language model: {name} did not download (retrying)",
    model_missing: "Language model: {name} not downloaded",
    update_failed: "The update could not be installed.",
    update_stop_failed: "The update was not installed: the server could not be stopped for it.",
    #[cfg(windows)]
    update_stays_paused: "The server stays paused.",
    #[cfg(windows)]
    update_starting_again: "The server is starting again.",
    #[cfg(windows)]
    update_start_failed: "The server could not start again.",
    open_failed: "Cannot open {url}.",
    #[cfg(windows)]
    task_move_failed: "Maktaba could not move its sign-in task to this user's name.",
    #[cfg(any(windows, target_os = "linux", target_os = "macos"))]
    server_start_failed: "The Maktaba server could not start.",
    #[cfg(target_os = "linux")]
    switch_failed: "Maktaba could not switch the server to this version.",
    #[cfg(any(target_os = "linux", test))]
    older_app: "This Maktaba app ({app}) is older than the Maktaba this computer already runs ({server}). The server keeps running {server}; to manage it from here, install Maktaba {server} or newer.",
    pause_failed: "The server could not be paused.",
    resume_failed: "The server could not be resumed.",
    stop_failed: "The server could not be stopped.",
    logs_failed: "The logs could not be opened.",
    setup_failed: "The setup screens could not be opened.",
    action_failed: "That did not work.",
    window_menu: "Server",
    install_update: "Install update and restart",
    order_home: "Install Maktaba {version} (this home's update) and restart",
    order_node: "Install Maktaba {version}: the cluster's update waits for this machine",
    order_clicked: "Installing Maktaba {version} when the cluster is ready…",
    order_ready_home: "Maktaba {version} is ready to install on this home",
    order_ready_node: "Maktaba {version} is ready: the cluster's update waits for this machine",
    list_sep: ", ",
};

pub static AR: Words = Words {
    brand: "مكتبة",
    setup_needed: "مكتبة: أكمل الإعداد لتشغيل الخادم",
    paused: "مكتبة متوقفة مؤقتًا",
    stopped: "مكتبة متوقفة",
    starting: "مكتبة قيد التشغيل…",
    starting_on: None,
    running: "مكتبة تعمل",
    cannot_start: None,
    cannot_start_plain: "مكتبة: تعذّر تشغيل {roles}",
    keeps_stopping: "مكتبة: يتكرر توقف {roles}",
    has_a_problem: "في مكتبة مشكلة: {reason}",
    no_answer: "الخادم لا يستجيب",
    update_ready: "التحديث جاهز: أعد التشغيل لتثبيته",
    open: "فتح مكتبة",
    change_server: "الاتصال بمكتبة أخرى…",
    resume: "استئناف الخادم",
    pause: "إيقاف الخادم مؤقتًا",
    open_logs: "فتح السجلات",
    play: "تشغيل",
    pause_playback: "إيقاف التشغيل مؤقتًا",
    settings: "الإعدادات",
    quit_server_runs: "خروج (يبقى الخادم عاملًا)",
    stop_and_quit: "إيقاف الخادم والخروج",
    quit: "خروج",
    model_downloading: "النموذج اللغوي: جارٍ تنزيل {name}",
    model_downloading_pct: "النموذج اللغوي: جارٍ تنزيل {name} ({pct}%)",
    model_ready: "النموذج اللغوي: جاهز",
    model_starting: "النموذج اللغوي: قيد التشغيل…",
    model_failed: "النموذج اللغوي: لم يُنزَّل {name} (تُعاد المحاولة)",
    model_missing: "النموذج اللغوي: {name} غير مُنزَّل",
    update_failed: "تعذّر تثبيت التحديث.",
    update_stop_failed: "لم يُثبَّت التحديث: تعذّر إيقاف الخادم من أجله.",
    #[cfg(windows)]
    update_stays_paused: "يبقى الخادم متوقفًا مؤقتًا.",
    #[cfg(windows)]
    update_starting_again: "يبدأ الخادم من جديد.",
    #[cfg(windows)]
    update_start_failed: "تعذّر تشغيل الخادم من جديد.",
    open_failed: "يتعذّر فتح {url}.",
    #[cfg(windows)]
    task_move_failed: "تعذّر على مكتبة نقل مهمة تسجيل الدخول إلى اسم هذا المستخدم.",
    #[cfg(any(windows, target_os = "linux", target_os = "macos"))]
    server_start_failed: "تعذّر تشغيل خادم مكتبة.",
    #[cfg(target_os = "linux")]
    switch_failed: "تعذّر على مكتبة نقل الخادم إلى هذا الإصدار.",
    #[cfg(any(target_os = "linux", test))]
    older_app: "تطبيق مكتبة هذا ({app}) أقدم من إصدار مكتبة الذي يعمل على هذا الحاسوب ({server}). يواصل الخادم العمل بالإصدار {server}؛ ولإدارته من هنا ثبّت مكتبة {server} أو أحدث.",
    pause_failed: "تعذّر إيقاف الخادم مؤقتًا.",
    resume_failed: "تعذّر استئناف الخادم.",
    stop_failed: "تعذّر إيقاف الخادم.",
    logs_failed: "تعذّر فتح السجلات.",
    setup_failed: "تعذّر فتح شاشات الإعداد.",
    action_failed: "لم ينجح ذلك.",
    window_menu: "الخادم",
    install_update: "تثبيت التحديث وإعادة التشغيل",
    order_home: "ثبّت الإصدار {version} من مكتبة (تحديث هذا الخادم الرئيسي) وأعد التشغيل",
    order_node: "ثبّت الإصدار {version} من مكتبة: تحديث المجموعة ينتظر هذا الجهاز",
    order_clicked: "يُثبَّت الإصدار {version} من مكتبة حين تصبح المجموعة جاهزة…",
    order_ready_home: "الإصدار {version} من مكتبة جاهز للتثبيت على هذا الخادم الرئيسي",
    order_ready_node: "الإصدار {version} من مكتبة جاهز: تحديث المجموعة ينتظر هذا الجهاز",
    list_sep: "، ",
};

/// An error dialog's text: the sentence in the tray's language, then the
/// programs' own errors (their English), each on its own line, as they are.
pub fn error_message(sentence: &str, errors: &[&str]) -> String {
    let mut out = sentence.trim().to_string();
    let errors: Vec<&str> = errors
        .iter()
        .map(|e| e.trim())
        .filter(|e| !e.is_empty())
        .collect();
    if !errors.is_empty() {
        out.push_str("\n\n");
        out.push_str(&errors.join("\n"));
    }
    out
}

/// `template` with each `{name}` replaced by its value in `vars`, in one pass
/// (a value that holds braces stays as it is); an unknown name is kept.
pub fn fill(template: &str, vars: &[(&str, &str)]) -> String {
    let mut out = String::with_capacity(template.len());
    let mut rest = template;
    while let Some(open) = rest.find('{') {
        out.push_str(&rest[..open]);
        let after = &rest[open + 1..];
        match after.find('}') {
            Some(close) => {
                let name = &after[..close];
                match vars.iter().find(|(k, _)| *k == name) {
                    Some((_, v)) => out.push_str(v),
                    None => {
                        out.push('{');
                        out.push_str(name);
                        out.push('}');
                    }
                }
                rest = &after[close + 1..];
            }
            None => {
                out.push_str(&rest[open..]);
                rest = "";
            }
        }
    }
    out.push_str(rest);
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Every line of a table, by name, with the `{names}` it fills.
    fn lines(w: &Words) -> Vec<(&'static str, Option<&'static str>)> {
        let out = vec![
            ("brand", Some(w.brand)),
            ("setup_needed", Some(w.setup_needed)),
            ("paused", Some(w.paused)),
            ("stopped", Some(w.stopped)),
            ("starting", Some(w.starting)),
            ("starting_on", w.starting_on),
            ("running", Some(w.running)),
            ("cannot_start", w.cannot_start),
            ("cannot_start_plain", Some(w.cannot_start_plain)),
            ("keeps_stopping", Some(w.keeps_stopping)),
            ("has_a_problem", Some(w.has_a_problem)),
            ("no_answer", Some(w.no_answer)),
            ("update_ready", Some(w.update_ready)),
            ("open", Some(w.open)),
            ("change_server", Some(w.change_server)),
            ("resume", Some(w.resume)),
            ("pause", Some(w.pause)),
            ("open_logs", Some(w.open_logs)),
            ("play", Some(w.play)),
            ("pause_playback", Some(w.pause_playback)),
            ("settings", Some(w.settings)),
            ("quit_server_runs", Some(w.quit_server_runs)),
            ("stop_and_quit", Some(w.stop_and_quit)),
            ("quit", Some(w.quit)),
            ("model_downloading", Some(w.model_downloading)),
            ("model_downloading_pct", Some(w.model_downloading_pct)),
            ("model_ready", Some(w.model_ready)),
            ("model_starting", Some(w.model_starting)),
            ("model_failed", Some(w.model_failed)),
            ("model_missing", Some(w.model_missing)),
            ("update_failed", Some(w.update_failed)),
            ("update_stop_failed", Some(w.update_stop_failed)),
            ("open_failed", Some(w.open_failed)),
            ("pause_failed", Some(w.pause_failed)),
            ("resume_failed", Some(w.resume_failed)),
            ("stop_failed", Some(w.stop_failed)),
            ("logs_failed", Some(w.logs_failed)),
            ("setup_failed", Some(w.setup_failed)),
            ("action_failed", Some(w.action_failed)),
            ("window_menu", Some(w.window_menu)),
            ("install_update", Some(w.install_update)),
            ("order_home", Some(w.order_home)),
            ("order_node", Some(w.order_node)),
            ("order_clicked", Some(w.order_clicked)),
            ("order_ready_home", Some(w.order_ready_home)),
            ("order_ready_node", Some(w.order_ready_node)),
            ("older_app", Some(w.older_app)),
        ];
        // the lines only Windows reads
        #[cfg(windows)]
        let out = [
            out,
            vec![
                ("update_stays_paused", Some(w.update_stays_paused)),
                ("update_starting_again", Some(w.update_starting_again)),
                ("update_start_failed", Some(w.update_start_failed)),
                ("task_move_failed", Some(w.task_move_failed)),
            ],
        ]
        .concat();
        // the lines Windows, Linux and macOS read
        #[cfg(any(windows, target_os = "linux", target_os = "macos"))]
        let out = [
            out,
            vec![("server_start_failed", Some(w.server_start_failed))],
        ]
        .concat();
        // the lines only Linux reads
        #[cfg(target_os = "linux")]
        let out = [out, vec![("switch_failed", Some(w.switch_failed))]].concat();
        out
    }

    fn names(template: &str) -> Vec<&str> {
        let mut out: Vec<&str> = template
            .split('{')
            .skip(1)
            .filter_map(|s| s.split_once('}').map(|(n, _)| n))
            .collect();
        out.sort_unstable();
        out
    }

    #[test]
    fn arabic_has_every_line_of_english_with_the_same_names() {
        for ((name, en), (_, ar)) in lines(&EN).into_iter().zip(lines(&AR)) {
            let Some(ar) = ar else {
                // only the lines that carry the server's English detail
                assert!(
                    matches!(name, "starting_on" | "cannot_start"),
                    "{name} has no Arabic"
                );
                continue;
            };
            let en = en.expect("English has every line");
            assert!(!ar.trim().is_empty(), "{name}");
            assert_eq!(names(en), names(ar), "{name}");
            let arabic = |c: char| ('\u{0600}'..='\u{06ff}').contains(&c);
            assert!(ar.chars().any(arabic), "{name} is not in Arabic: {ar}");
        }
        assert_eq!(EN.no_answer, crate::status::NO_ANSWER);
        assert_eq!((EN.list_sep, AR.list_sep), (", ", "، "));
    }

    #[test]
    fn the_first_language_the_tray_speaks_wins() {
        assert_eq!(pick(["ar-SA"]), Lang::Ar);
        assert_eq!(pick(["ar_EG.UTF-8"]), Lang::Ar);
        assert_eq!(pick(["AR"]), Lang::Ar);
        assert_eq!(pick(["en-US", "ar"]), Lang::En);
        assert_eq!(pick(["fr-FR", "ar-AE"]), Lang::Ar);
        assert_eq!(pick(["fr-FR", "de"]), Lang::En);
        assert_eq!(pick(["arn-CL"]), Lang::En, "Mapudungun is not Arabic");
        assert_eq!(pick(Vec::<String>::new()), Lang::En);
        assert_eq!(pick(["C"]), Lang::En);
    }

    #[test]
    fn windows_display_languages() {
        assert_eq!(langid_tag(0x0401), Some("ar")); // ar-SA
        assert_eq!(langid_tag(0x0c01), Some("ar")); // ar-EG
        assert_eq!(langid_tag(0x3801), Some("ar")); // ar-AE
        assert_eq!(langid_tag(0x0409), Some("en")); // en-US
        assert_eq!(langid_tag(0x0809), Some("en")); // en-GB
        assert_eq!(langid_tag(0x040c), None); // fr-FR
    }

    #[test]
    fn linux_languages_follow_gettext() {
        let env = |vars: &'static [(&'static str, &'static str)]| {
            move |name: &str| {
                vars.iter()
                    .find(|(k, _)| *k == name)
                    .map(|(_, v)| v.to_string())
            }
        };
        assert_eq!(posix_tags(env(&[("LANG", "ar_EG.UTF-8")])), ["ar_EG.UTF-8"]);
        assert_eq!(
            posix_tags(env(&[
                ("LANG", "en_US.UTF-8"),
                ("LC_MESSAGES", "ar_SA.UTF-8")
            ])),
            ["ar_SA.UTF-8"]
        );
        assert_eq!(
            posix_tags(env(&[("LC_ALL", "fr_FR.UTF-8"), ("LANG", "ar_SA.UTF-8")])),
            ["fr_FR.UTF-8"]
        );
        assert_eq!(
            posix_tags(env(&[("LANGUAGE", "ar:en"), ("LANG", "fr_FR.UTF-8")])),
            ["ar", "en", "fr_FR.UTF-8"]
        );
        // an empty variable is not set; C over a later Arabic LANG
        assert_eq!(
            pick(posix_tags(env(&[
                ("LC_ALL", ""),
                ("LC_MESSAGES", "ar_JO.UTF-8"),
                ("LANG", "en_GB")
            ]))),
            Lang::Ar
        );
        assert_eq!(
            pick(posix_tags(env(&[("LC_ALL", "C"), ("LANG", "ar_EG.UTF-8")]))),
            Lang::En
        );
        // gettext ignores LANGUAGE under the C locale
        assert_eq!(posix_tags(env(&[("LANGUAGE", "ar"), ("LANG", "C")])), ["C"]);
        assert_eq!(pick(posix_tags(env(&[("LANG", "ar_EG.UTF-8")]))), Lang::Ar);
        assert!(posix_tags(env(&[])).is_empty());
    }

    #[test]
    fn an_error_dialog_is_its_sentence_then_the_errors_as_they_are() {
        assert_eq!(
            error_message(AR.update_failed, &["signature mismatch"]),
            "تعذّر تثبيت التحديث.\n\nsignature mismatch"
        );
        assert_eq!(
            error_message(
                &format!("{} {}", EN.update_failed, EN.resume_failed),
                &["timed out", " ", "schtasks: access denied"]
            ),
            "The update could not be installed. The server could not be resumed.\n\ntimed out\nschtasks: access denied"
        );
        assert_eq!(error_message(EN.pause_failed, &[]), EN.pause_failed);
    }

    #[test]
    fn fill_puts_each_value_in_its_place_once() {
        assert_eq!(
            fill(
                EN.cannot_start.unwrap(),
                &[("roles", "api"), ("detail", "disk full")]
            ),
            "Maktaba: api cannot start (disk full)"
        );
        // a value with braces is not filled again; an unknown name stays
        assert_eq!(
            fill("{a} and {b} {c", &[("a", "{b}"), ("b", "B")]),
            "{b} and B {c"
        );
        assert_eq!(fill("{x}", &[]), "{x}");
        assert_eq!(
            fill(AR.has_a_problem, &[("reason", AR.no_answer)]),
            "في مكتبة مشكلة: الخادم لا يستجيب"
        );
    }
}
