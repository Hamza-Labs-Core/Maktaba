import Foundation

extension LocalizedStringResource {
    /// This sentence in `locale`'s language and numbers. A page passes its `\.locale`, so a
    /// sentence it builds as a `String` (joined with others, or handed to a view that takes a
    /// string) speaks the page's language as its `Text`s do; `String(localized:)` alone follows
    /// the process's language, which a page drawn in another language (a preview, a snapshot)
    /// does not share.
    func string(in locale: Locale) -> String {
        var r = self
        r.locale = locale
        return String(localized: r)
    }
}
