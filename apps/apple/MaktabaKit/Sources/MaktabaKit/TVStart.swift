import Foundation

// Epic 42 story 42.10: the home picker and sign-in by code on Apple TV. The screens are the
// TV app's own (apps/tv/tvos, focus-driven); what they decide and the words they add to
// `ConnectStrings` live here so the package's tests cover them.

public enum TVStart {
    /// How a TV signs in.
    public enum Method: Hashable, Sendable { case code, password }

    /// The ways a home allows, the code first (no typing with a remote). A home that has not
    /// said (older, or not asked yet) is offered both; one that allows neither, the password.
    public static func offered(_ methods: SignInMethods?) -> [Method] {
        guard let m = methods else { return [.code, .password] }
        let list = (m.code ? [Method.code] : []) + (m.password ? [Method.password] : [])
        return list.isEmpty ? [.password] : list
    }

    /// The homes on this network that are not already among `known`: by exact address only
    /// (an announced id is not proof, so a known id at a new address is listed as new).
    public static func nearby(_ found: [HomeBrowser.Found], known: [Home]) -> [HomeBrowser.Found] {
        let addresses = Set(known.map(\.url))
        return found.filter { !addresses.contains($0.url) }
    }

    /// What a person typed in "By address": a pasted connect link, the addresses to try, or
    /// nothing usable.
    public enum Typed: Equatable, Sendable {
        case link(ConnectLink)
        case addresses([URL])
        case invalid
    }

    public static func typed(_ text: String) -> Typed {
        if let link = ConnectLink(string: text) { return .link(link) }
        let candidates = Home.candidates(for: text)
        return candidates.isEmpty ? .invalid : .addresses(candidates)
    }
}

/// The TV's words beyond `ConnectStrings` (no camera: nothing to scan; a remote: hold to see
/// more), in English and Arabic with the web's terms.
public struct TVConnectStrings: Sendable {
    public let language: ConnectStrings.Language

    public init(_ language: ConnectStrings.Language) { self.language = language }

    public static func forPreferredLanguages(_ preferred: [String] = Locale.preferredLanguages) -> TVConnectStrings {
        TVConnectStrings(ConnectStrings.forPreferredLanguages(preferred).language)
    }

    public func text(_ key: Key, _ args: String...) -> String {
        var s = (language == .ar ? Self.ar : Self.en)[key] ?? Self.en[key] ?? key.rawValue
        for a in args {
            if let r = s.range(of: "%@") { s.replaceSubrange(r, with: a) }
        }
        return s
    }

    public enum Key: String, CaseIterable, Sendable {
        case brand, chooseHome, nearbyHint, holdHint
        case codeTitle, codeAlt, otherHome, withCode, renamed
    }

    static let en: [Key: String] = [
        .brand: "Maktaba",
        .chooseHome: "Choose your home",
        .nearbyHint: "A home on this Wi-Fi or Ethernet shows here. Not there? Enter its address below.",
        .holdHint: "Hold the select button on a home to rename or remove it.",
        .codeTitle: "Sign in to %@",
        .codeAlt: "Or scan the QR code with that phone.",
        .otherHome: "Choose another home",
        .withCode: "Sign in with a code",
        .renamed: "Name on this TV",
    ]

    static let ar: [Key: String] = [
        .brand: "مكتبة",
        .chooseHome: "اختر جهازك الرئيسي",
        .nearbyHint: "يظهر هنا الجهاز الرئيسي الموجود على شبكة Wi-Fi أو Ethernet هذه. لا تجده؟ أدخل عنوانه أدناه.",
        .holdHint: "اضغط مطوّلًا على زر التحديد فوق جهاز رئيسي لإعادة تسميته أو إزالته.",
        .codeTitle: "تسجيل الدخول إلى %@",
        .codeAlt: "أو امسح رمز QR بذلك الهاتف.",
        .otherHome: "اختيار جهاز رئيسي آخر",
        .withCode: "تسجيل الدخول برمز",
        .renamed: "الاسم على هذا التلفاز",
    ]
}
