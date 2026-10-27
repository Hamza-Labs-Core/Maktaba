import Foundation

/// The words of the home picker, signing in and approving a sign-in, in English and Arabic
/// with the web's terms (a home is "الجهاز الرئيسي"), as `PermissionsStrings`: kept here
/// rather than in a string catalog, which a package uses only in the languages the app lists.
public struct ConnectStrings: Sendable {
    public typealias Language = PermissionsStrings.Language

    public let language: Language

    public init(_ language: Language) { self.language = language }

    /// Arabic when it is the person's first language, else English.
    public static func forPreferredLanguages(_ preferred: [String] = Locale.preferredLanguages) -> ConnectStrings {
        ConnectStrings(PermissionsStrings.forPreferredLanguages(preferred).language)
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

    /// Why signing in by password did not work: the home's 401 is a wrong username or
    /// password here (not a sign-in that ended); anything else is said as every screen says it.
    public func signInProblem(_ error: Error) -> String {
        if case .status(401, _, _)? = error as? APIError { return text(.wrongPassword) }
        return HomeFailure(error).message(ErrorStrings(language))
    }

    public enum Key: String, CaseIterable, Sendable {
        // the home picker
        case onThisNetwork, searching, onThisNetworkHint
        case recentHomes, rename, remove, renameTitle, renameHint, save, cancel
        case byAddress, addressPlaceholder, addressHint, connect, connecting
        case scanQR, scanHint
        case notAnAddress, unreachable, notAHome, otherHome
        // scanning
        case scanTitle, scanUnsupported, cameraDenied, openSettings, notAHomeCode, notASignInCode
        // signing in
        case signInTitle, byPassword, byCode, username, password, signIn, homes, wrongPassword
        case codeIntro, codeOrOpen, waiting, codeChangesIn, codeUnsupported, qrLabel
        // approving
        case approveMenu, approveTitle, approveIntro, codeField, lookUp, scanCode, notACode
        case asking, app, platform, device, deviceName, expires
        case approveWarning, approve, notThis, done
        case unknownOnThisHome, notFound, approved, alreadyApproved, expired
        case anotherDevice, code
        case linkTitle, linkMessage
    }

    static let en: [Key: String] = [
        .onThisNetwork: "On this network",
        .searching: "Looking for homes on this network…",
        .onThisNetworkHint: "A home on this Wi-Fi or Ethernet shows here. Not there? Type its address or scan its QR code.",
        .recentHomes: "Recent homes",
        .rename: "Rename",
        .remove: "Remove",
        .renameTitle: "Rename home",
        .renameHint: "Leave it empty to use the home's own name.",
        .save: "Save",
        .cancel: "Cancel",
        .byAddress: "By address",
        .addressPlaceholder: "Address, like 192.168.1.20:8088",
        .addressHint: "The address you open Maktaba at in a browser. A maktaba:// link the home shared works too.",
        .connect: "Connect",
        .connecting: "Connecting…",
        .scanQR: "Scan a QR code",
        .scanHint: "Scan the QR code your home shows to connect an app.",
        .notAnAddress: "That is not a home's address.",
        .unreachable: "Could not reach the home: %@",
        .notAHome: "Something answered at that address, but it is not a Maktaba home.",
        .otherHome: "That address answers for another home than the one in the link.",
        .scanTitle: "Scan a QR code",
        .scanUnsupported: "This device can't scan codes. Type it instead.",
        .cameraDenied: "The camera is not allowed for this app. Turn it on in Settings, or type it instead.",
        .openSettings: "Open Settings",
        .notAHomeCode: "That QR code is not a Maktaba home's.",
        .notASignInCode: "That QR code is not a sign-in code.",
        .signInTitle: "Sign in",
        .byPassword: "Username & password",
        .byCode: "With a code",
        .username: "Username",
        .password: "Password",
        .signIn: "Sign in",
        .wrongPassword: "That username or password is not right.",
        .homes: "Homes",
        .codeIntro: "On a phone or computer already signed in to %@, open Approve a sign-in (in Maktaba Remote, or the Player's account menu) and enter this code, or scan the QR code.",
        .codeOrOpen: "Or open this page there:",
        .waiting: "Waiting for approval…",
        .codeChangesIn: "A new code in %@",
        .codeUnsupported: "This home signs in with a username and password only.",
        .qrLabel: "QR code of the sign-in page",
        .approveMenu: "Approve a sign-in…",
        .approveTitle: "Approve a sign-in",
        .approveIntro: "A device that shows a code asks to be signed in to %@ as you. Enter its code or scan its QR code.",
        .codeField: "Code, like ABCD-1234",
        .lookUp: "Continue",
        .scanCode: "Scan its QR code",
        .notACode: "That is not a sign-in code: eight letters and digits, like ABCD-1234.",
        .asking: "What is asking",
        .app: "App",
        .platform: "System",
        .device: "Device",
        .deviceName: "Name",
        .expires: "Code expires",
        .approveWarning: "Approve only a device you have in front of you. It will be signed in as %@, and can do what %@ can.",
        .approve: "Approve",
        .notThis: "Not this one",
        .done: "Done",
        .unknownOnThisHome: "This home can't say what is asking. Approve only if this code is on a device in front of you right now.",
        .notFound: "No device is waiting for that code. Check it, or ask the device for a new one.",
        .approved: "Approved: %@ is signed in.",
        .alreadyApproved: "That code was approved already.",
        .expired: "The code has expired. Ask the device for a new one.",
        .anotherDevice: "The device",
        .code: "Code",
        .linkTitle: "Connect to this home?",
        .linkMessage: "A link asks to open the home “%@” at %@. Connect only if you trust where the link came from; you sign in there yourself.",
    ]

    static let ar: [Key: String] = [
        .onThisNetwork: "على هذه الشبكة",
        .searching: "جارٍ البحث عن أجهزة رئيسية على هذه الشبكة…",
        .onThisNetworkHint: "يظهر هنا الجهاز الرئيسي الموجود على شبكة Wi-Fi أو Ethernet هذه. لا تجده؟ اكتب عنوانه أو امسح رمز QR الخاص به.",
        .recentHomes: "الأجهزة الرئيسية الأخيرة",
        .rename: "إعادة التسمية",
        .remove: "إزالة",
        .renameTitle: "إعادة تسمية الجهاز الرئيسي",
        .renameHint: "اتركه فارغًا لاستخدام اسم الجهاز الرئيسي نفسه.",
        .save: "حفظ",
        .cancel: "إلغاء",
        .byAddress: "بالعنوان",
        .addressPlaceholder: "العنوان، مثل 192.168.1.20:8088",
        .addressHint: "العنوان الذي تفتح به مكتبة في المتصفح. يصلح أيضًا رابط maktaba:// شاركه الجهاز الرئيسي.",
        .connect: "اتصال",
        .connecting: "جارٍ الاتصال…",
        .scanQR: "مسح رمز QR",
        .scanHint: "امسح رمز QR الذي يعرضه جهازك الرئيسي لربط تطبيق.",
        .notAnAddress: "هذا ليس عنوان جهاز رئيسي.",
        .unreachable: "تعذّر الوصول إلى الجهاز الرئيسي: %@",
        .notAHome: "أجاب شيء ما على هذا العنوان، لكنه ليس جهاز مكتبة رئيسيًا.",
        .otherHome: "هذا العنوان يجيب عن جهاز رئيسي غير الذي في الرابط.",
        .scanTitle: "مسح رمز QR",
        .scanUnsupported: "لا يستطيع هذا الجهاز مسح الرموز. اكتبه بدلًا من ذلك.",
        .cameraDenied: "الكاميرا غير مسموحة لهذا التطبيق. فعّلها من الإعدادات، أو اكتبه بدلًا من ذلك.",
        .openSettings: "فتح الإعدادات",
        .notAHomeCode: "رمز QR هذا ليس لجهاز مكتبة رئيسي.",
        .notASignInCode: "رمز QR هذا ليس رمز تسجيل دخول.",
        .signInTitle: "تسجيل الدخول",
        .byPassword: "اسم المستخدم وكلمة المرور",
        .byCode: "برمز",
        .username: "اسم المستخدم",
        .password: "كلمة المرور",
        .signIn: "تسجيل الدخول",
        .wrongPassword: "اسم المستخدم أو كلمة المرور غير صحيحة.",
        .homes: "الأجهزة الرئيسية",
        .codeIntro: "على هاتف أو حاسوب مسجَّل الدخول إلى %@، افتح «الموافقة على تسجيل دخول» (في Maktaba Remote، أو قائمة الحساب في المشغّل) وأدخل هذا الرمز، أو امسح رمز QR.",
        .codeOrOpen: "أو افتح هذه الصفحة هناك:",
        .waiting: "في انتظار الموافقة…",
        .codeChangesIn: "رمز جديد بعد %@",
        .codeUnsupported: "يسجّل هذا الجهاز الرئيسي الدخول باسم المستخدم وكلمة المرور فقط.",
        .qrLabel: "رمز QR لصفحة تسجيل الدخول",
        .approveMenu: "الموافقة على تسجيل دخول…",
        .approveTitle: "الموافقة على تسجيل دخول",
        .approveIntro: "جهاز يعرض رمزًا يطلب تسجيل دخوله إلى %@ باسمك. أدخل رمزه أو امسح رمز QR الخاص به.",
        .codeField: "الرمز، مثل ABCD-1234",
        .lookUp: "متابعة",
        .scanCode: "مسح رمز QR الخاص به",
        .notACode: "هذا ليس رمز تسجيل دخول: ثمانية أحرف وأرقام، مثل ABCD-1234.",
        .asking: "ما الذي يطلب",
        .app: "التطبيق",
        .platform: "النظام",
        .device: "الجهاز",
        .deviceName: "الاسم",
        .expires: "ينتهي الرمز",
        .approveWarning: "وافق فقط على جهاز أمامك. سيُسجَّل دخوله باسم %@، ويستطيع ما يستطيعه %@.",
        .approve: "موافقة",
        .notThis: "ليس هذا",
        .done: "تم",
        .unknownOnThisHome: "لا يستطيع هذا الجهاز الرئيسي أن يخبرك بما يطلب. وافق فقط إن كان هذا الرمز على جهاز أمامك الآن.",
        .notFound: "لا يوجد جهاز ينتظر هذا الرمز. تحقّق منه، أو اطلب من الجهاز رمزًا جديدًا.",
        .approved: "تمت الموافقة: سُجّل دخول %@.",
        .alreadyApproved: "تمت الموافقة على هذا الرمز من قبل.",
        .expired: "انتهت صلاحية الرمز. اطلب من الجهاز رمزًا جديدًا.",
        .anotherDevice: "الجهاز",
        .code: "الرمز",
        .linkTitle: "الاتصال بهذا الجهاز الرئيسي؟",
        .linkMessage: "يطلب رابط فتح الجهاز الرئيسي «%@» على %@. اتصل فقط إن كنت تثق بمصدر الرابط؛ وستسجّل الدخول هناك بنفسك.",
    ]

    /// Why an address did not lead to a home.
    public func reach(_ f: ReachFailure) -> String {
        switch f {
        // the app's sentence, not the system's English words for the failure
        case .unreachable: ErrorStrings(language).text(.unreachable)
        case .notAHome: text(.notAHome)
        case .otherHome: text(.otherHome)
        }
    }

    /// An app's name from its `X-Maktaba-Client` app (`player`, `remote`, `tv`, `web`).
    public func appName(_ raw: String) -> String {
        switch raw.lowercased() {
        case "player": "Maktaba Player"
        case "remote": "Maktaba Remote"
        case "tv": "Maktaba TV"
        case "web": language == .ar ? "المتصفح" : "Web browser"
        case "desktop": "Maktaba Desktop"
        default: raw
        }
    }
}
