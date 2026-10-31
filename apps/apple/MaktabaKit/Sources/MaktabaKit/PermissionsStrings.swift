import Foundation

/// The walkthrough's words in English and Arabic, with the web's terms (web/src/i18n: a home
/// is "الجهاز الرئيسي", Settings "الإعدادات", Required "مطلوب"). Kept here rather than in a
/// string catalog: they were written before the apps listed Arabic, which a package's
/// catalog needs (MaktabaKit's other words are in its Localizable.xcstrings now).
public struct PermissionsStrings: Sendable {
    public enum Language: String, Sendable { case en, ar }

    public let language: Language

    public init(_ language: Language) { self.language = language }

    /// Arabic when it is the person's first language, else English.
    public static func forPreferredLanguages(_ preferred: [String] = Locale.preferredLanguages) -> PermissionsStrings {
        PermissionsStrings(preferred.first?.lowercased().hasPrefix("ar") == true ? .ar : .en)
    }

    public var isRightToLeft: Bool { language == .ar }
    public var locale: Locale { Locale(identifier: language.rawValue) }

    public func text(_ key: Key, _ args: String...) -> String {
        var s = (language == .ar ? Self.ar : Self.en)[key] ?? Self.en[key] ?? key.rawValue
        for a in args {
            if let r = s.range(of: "%@") { s.replaceSubrange(r, with: a) }
        }
        return s
    }

    public enum Key: String, CaseIterable, Sendable {
        case title, intro, introNone
        case required, optional
        case granted, denied, notAsked, unknown, checking
        case allow, openSettings, checkAgain, `continue`, notNow
        case localNetworkName, localNetworkWhy, localNetworkWithout
        case localNetworkAsk, localNetworkDeniedHow, localNetworkUnknown
        case cameraName, cameraWhy, cameraWithout, cameraAsk, cameraDeniedHow, cameraUnknown
    }

    /// The card's words for each permission.
    public func why(_ kind: PermissionKind) -> String { text(kind == .camera ? .cameraWhy : .localNetworkWhy) }
    public func without(_ kind: PermissionKind) -> String { text(kind == .camera ? .cameraWithout : .localNetworkWithout) }
    public func ask(_ kind: PermissionKind) -> String { text(kind == .camera ? .cameraAsk : .localNetworkAsk) }
    public func unknownHelp(_ kind: PermissionKind) -> String { text(kind == .camera ? .cameraUnknown : .localNetworkUnknown) }
    public func deniedHow(_ kind: PermissionKind, appName: String) -> String {
        kind == .camera ? text(.cameraDeniedHow, appName) : text(.localNetworkDeniedHow, appName, appName)
    }

    // The Settings path differs per platform (Apple's own names for the panes).
    #if os(macOS)
    private static let enSettings = "Open System Settings"
    private static let arSettings = "فتح إعدادات النظام"
    private static let enDeniedHow = "Turn it on in System Settings → Privacy & Security → Local Network → %@, then come back."
    private static let arDeniedHow = "فعّله من إعدادات النظام ← الخصوصية والأمن ← الشبكة المحلية ← %@، ثم عُد إلى التطبيق."
    #else
    private static let enSettings = "Open Settings"
    private static let arSettings = "فتح الإعدادات"
    private static let enDeniedHow = "Turn it on in Settings → Privacy & Security → Local Network → %@ (or Settings → Apps → %@ → Local Network), then come back."
    private static let arDeniedHow = "فعّله من الإعدادات ← الخصوصية والأمن ← الشبكة المحلية ← %@ (أو الإعدادات ← التطبيقات ← %@ ← الشبكة المحلية)، ثم عُد إلى التطبيق."
    #endif

    static let en: [Key: String] = [
        .title: "Before you start",
        .intro: "%@ needs your permission for what is below. You can change it at any time in Settings.",
        .introNone: "%@ needs no permissions on this device.",
        .required: "Required",
        .optional: "Optional",
        .granted: "Allowed",
        .denied: "Not allowed",
        .notAsked: "Not asked yet",
        .unknown: "Can't tell",
        .checking: "Checking…",
        .allow: "Allow access",
        .openSettings: enSettings,
        .checkAgain: "Check again",
        .continue: "Continue",
        .notNow: "Not now",
        .localNetworkName: "Local Network",
        .localNetworkWhy: "To find your Maktaba home on this network and connect to it, for example at 192.168.1.20:8088 or mac.local.",
        .localNetworkWithout: "Without it the app can't reach a home on your Wi-Fi or Ethernet. A home you reach over the internet still works.",
        .localNetworkAsk: "Tap Allow access and the system asks you once: choose Allow.",
        .localNetworkDeniedHow: enDeniedHow,
        .localNetworkUnknown: "The app could not tell whether it may use the local network (no Wi-Fi, or the check took too long). Join your home's network and check again.",
        .cameraName: "Camera",
        .cameraWhy: "To scan your home's QR code to connect to it, or another device's sign-in QR code to approve it.",
        .cameraWithout: "Without it you type the address or the code instead of scanning it.",
        .cameraAsk: "Tap Allow access and the system asks you once, or leave it until you first scan a code.",
        .cameraDeniedHow: "Turn it on in Settings → Apps → %@ → Camera, then come back.",
        .cameraUnknown: "The app could not tell whether it may use the camera.",
    ]

    static let ar: [Key: String] = [
        .title: "قبل أن تبدأ",
        .intro: "يحتاج %@ إلى إذنك لما يلي. يمكنك تغييره في أي وقت من الإعدادات.",
        .introNone: "لا يحتاج %@ إلى أي إذن على هذا الجهاز.",
        .required: "مطلوب",
        .optional: "اختياري",
        .granted: "مسموح",
        .denied: "غير مسموح",
        .notAsked: "لم يُطلب بعد",
        .unknown: "تعذّر التحقق",
        .checking: "جارٍ التحقق…",
        .allow: "السماح بالوصول",
        .openSettings: arSettings,
        .checkAgain: "تحقّق مجددًا",
        .continue: "متابعة",
        .notNow: "ليس الآن",
        .localNetworkName: "الشبكة المحلية",
        .localNetworkWhy: "للعثور على جهاز مكتبة الرئيسي لديك على هذه الشبكة والاتصال به، مثل 192.168.1.20:8088 أو mac.local.",
        .localNetworkWithout: "بدونه لا يصل التطبيق إلى جهاز رئيسي على شبكة Wi-Fi أو Ethernet لديك. أما الجهاز الرئيسي الذي تصل إليه عبر الإنترنت فيبقى متاحًا.",
        .localNetworkAsk: "اضغط «السماح بالوصول» وسيسألك النظام مرة واحدة: اختر السماح.",
        .localNetworkDeniedHow: arDeniedHow,
        .localNetworkUnknown: "تعذّر على التطبيق معرفة ما إذا كان مسموحًا له باستخدام الشبكة المحلية (لا توجد شبكة Wi-Fi، أو استغرق التحقق وقتًا طويلًا). اتصل بشبكة جهازك الرئيسي ثم تحقّق مجددًا.",
        .cameraName: "الكاميرا",
        .cameraWhy: "لمسح رمز QR الخاص بجهازك الرئيسي للاتصال به، أو رمز QR لتسجيل دخول جهاز آخر للموافقة عليه.",
        .cameraWithout: "بدونها يمكنك كتابة العنوان أو الرمز بدلًا من مسحه.",
        .cameraAsk: "اضغط «السماح بالوصول» وسيسألك النظام مرة واحدة، أو اتركه حتى تمسح رمزًا لأول مرة.",
        .cameraDeniedHow: "فعّلها من الإعدادات ← التطبيقات ← %@ ← الكاميرا، ثم عُد إلى التطبيق.",
        .cameraUnknown: "تعذّر على التطبيق معرفة ما إذا كان مسموحًا له باستخدام الكاميرا.",
    ]

    public func name(_ kind: PermissionKind) -> String {
        switch kind {
        case .localNetwork: text(.localNetworkName)
        case .camera: text(.cameraName)
        }
    }

    /// The card's symbol.
    public static func symbol(_ kind: PermissionKind) -> String {
        switch kind {
        case .localNetwork: "network"
        case .camera: "camera"
        }
    }

    public func state(_ state: PermissionState) -> String {
        switch state {
        case .granted: text(.granted)
        case .denied: text(.denied)
        case .notAsked: text(.notAsked)
        case .unknown: text(.unknown)
        }
    }
}
