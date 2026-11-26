import Foundation

// MaktabaKit's own words live in its String Catalog (Localizable.xcstrings beside this file,
// English and Arabic), read from `Bundle.module`. A package's catalog is used in the
// languages the app lists: the Remote, Player and TV apps list English and Arabic.

public enum AppLanguage {
    /// `locale` with Western digits (`latn` numbering system) when its language is Arabic,
    /// so every Arabic number, duration, and date uses 0–9 across Maktaba's Apple apps.
    public static func westernDigits(_ locale: Locale) -> Locale {
        guard locale.language.languageCode?.identifier == "ar" else { return locale }
        var c = Locale.Components(locale: locale)
        c.numberingSystem = Locale.NumberingSystem("latn")
        return Locale(components: c)
    }

    /// The language the app shows (its main bundle's first localization, which the system
    /// picked from the person's languages), with the person's region for numbers and dates
    /// and Western digits in Arabic.
    public static var locale: Locale {
        let lang = Bundle.main.preferredLocalizations.first ?? "en"
        var c = Locale.Components(locale: .current)
        c.languageComponents = Locale.Language.Components(identifier: lang)
        return westernDigits(Locale(components: c))
    }

    /// Whether the app shows a right-to-left language (Arabic).
    public static var isRightToLeft: Bool {
        locale.language.characterDirection == .rightToLeft
    }
}

/// A sentence from MaktabaKit's catalog, in `locale`'s language (the app's by default).
/// A literal passed here is extracted into the catalog (the parameter is a
/// LocalizedStringResource), its interpolations as arguments and plural variations.
func kit(_ resource: LocalizedStringResource, _ locale: Locale = AppLanguage.locale) -> String {
    String(localized: LocalizedStringResource(resource.defaultValue, table: resource.table,
                                              locale: AppLanguage.westernDigits(locale),
                                              bundle: .atURL(Bundle.module.bundleURL)))
}

/// A word the catalog keeps under a key made at run time (`updates.state.<value>`): nil
/// when the catalog has none.
func kitWord(_ key: String, _ locale: Locale = AppLanguage.locale) -> String? {
    let s = String(localized: LocalizedStringResource(String.LocalizationValue(key),
                                                      locale: AppLanguage.westernDigits(locale),
                                                      bundle: .atURL(Bundle.module.bundleURL)))
    return s == key ? nil : s
}
