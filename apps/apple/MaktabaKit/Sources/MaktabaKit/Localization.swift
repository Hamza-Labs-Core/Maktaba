import Foundation

// MaktabaKit's own words live in its String Catalog (Localizable.xcstrings beside this file,
// English and Arabic), read from `Bundle.module`. A package's catalog is used in the
// languages the app lists: the Remote, Player and TV apps list English and Arabic.

public enum AppLanguage {
    /// The language the app shows (its main bundle's first localization, which the system
    /// picked from the person's languages), with the person's region for numbers and dates.
    public static var locale: Locale {
        let lang = Bundle.main.preferredLocalizations.first ?? "en"
        var c = Locale.Components(locale: .current)
        c.languageComponents = Locale.Language.Components(identifier: lang)
        return Locale(components: c)
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
    String(localized: LocalizedStringResource(resource.defaultValue, table: resource.table, locale: locale,
                                              bundle: .atURL(Bundle.module.bundleURL)))
}

/// A word the catalog keeps under a key made at run time (`updates.state.<value>`): nil
/// when the catalog has none.
func kitWord(_ key: String, _ locale: Locale = AppLanguage.locale) -> String? {
    let s = String(localized: LocalizedStringResource(String.LocalizationValue(key), locale: locale,
                                                      bundle: .atURL(Bundle.module.bundleURL)))
    return s == key ? nil : s
}
