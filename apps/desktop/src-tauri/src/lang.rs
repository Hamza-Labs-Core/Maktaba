// The language of the shell's own text on Linux (its native dialogs): Arabic
// when the session's locale is Arabic, else English. The web app keeps its
// own choice; a dialog the shell shows before (or without) a window cannot
// ask it. The desktop entries the shell writes carry both languages
// (Name[ar]=, Comment[ar]=), whatever the locale.
#![cfg_attr(not(target_os = "linux"), allow(dead_code))]

/// Whether the locale is Arabic, read as POSIX orders the variables: LC_ALL,
/// else LC_MESSAGES, else LANG (the first one set decides).
pub fn arabic_locale(getenv: impl Fn(&str) -> Option<String>) -> bool {
    ["LC_ALL", "LC_MESSAGES", "LANG"]
        .iter()
        .find_map(|v| getenv(v).filter(|s| !s.is_empty()))
        .is_some_and(|l| l == "ar" || l.starts_with("ar_") || l.starts_with("ar."))
}

/// The text in the session's language.
pub fn t(en: &str, ar: &str) -> String {
    if arabic_locale(|v| std::env::var(v).ok()) {
        ar.to_string()
    } else {
        en.to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn env(pairs: &'static [(&'static str, &'static str)]) -> impl Fn(&str) -> Option<String> {
        move |k| {
            pairs
                .iter()
                .find(|(n, _)| *n == k)
                .map(|(_, v)| v.to_string())
        }
    }

    #[test]
    fn the_first_locale_variable_set_decides() {
        assert!(arabic_locale(env(&[("LANG", "ar_SA.UTF-8")])));
        assert!(arabic_locale(env(&[("LANG", "ar")])));
        assert!(!arabic_locale(env(&[("LANG", "en_US.UTF-8")])));
        assert!(!arabic_locale(env(&[])));
        // LC_ALL over LC_MESSAGES over LANG; an empty one is not set
        assert!(!arabic_locale(env(&[
            ("LC_ALL", "C"),
            ("LANG", "ar_EG.UTF-8")
        ])));
        assert!(arabic_locale(env(&[
            ("LC_ALL", ""),
            ("LC_MESSAGES", "ar_JO.UTF-8"),
            ("LANG", "en_GB")
        ])));
        // "arn" (Mapudungun) is not Arabic
        assert!(!arabic_locale(env(&[("LANG", "arn_CL.UTF-8")])));
    }
}
