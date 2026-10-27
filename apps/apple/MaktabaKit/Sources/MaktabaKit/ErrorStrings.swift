import Foundation

/// What every app says when something goes wrong (`AppError`), in English and Arabic with
/// the web's words (web/src/i18n `player.error.*`: a home is "الجهاز الرئيسي"), kept here as
/// `PermissionsStrings` is.
public struct ErrorStrings: Sendable {
    public typealias Language = PermissionsStrings.Language

    public let language: Language

    public init(_ language: Language) { self.language = language }

    /// Arabic when it is the person's first language, else English.
    public static func forPreferredLanguages(_ preferred: [String] = Locale.preferredLanguages) -> ErrorStrings {
        ErrorStrings(PermissionsStrings.forPreferredLanguages(preferred).language)
    }

    public var isRightToLeft: Bool { language == .ar }

    public func text(_ key: Key) -> String {
        (language == .ar ? Self.ar : Self.en)[key] ?? Self.en[key] ?? key.rawValue
    }

    public func message(_ kind: AppError.Kind) -> String {
        text(Key(rawValue: kind.rawValue) ?? .server)
    }

    public func title(_ kind: AppError.Kind) -> String {
        switch kind {
        case .offline, .unreachable, .timedOut, .insecure: text(.titleUnreachable)
        case .signedOut, .wrongCredentials, .noAccess: text(.titleAccount)
        default: text(.titleLoad)
        }
    }

    public func action(_ action: AppError.Action) -> String {
        switch action {
        case .retry: text(.retry)
        case .signIn: text(.signIn)
        case .chooseHome: text(.chooseHome)
        case .back: text(.back)
        }
    }

    public enum Key: String, CaseIterable, Sendable {
        // one per AppError.Kind (cancelled is never shown)
        case offline, unreachable, timedOut, insecure, signedOut, wrongCredentials, noAccess
        case notFound, notReady, busy, diskFull, invalid, badAnswer, server, cancelled
        // titles and actions
        case titleLoad, titleUnreachable, titleAccount
        case retry, signIn, chooseHome, back
        // the sign-in screen when a saved sign-in could not be checked
        case restoreFailed
    }

    static let en: [Key: String] = [
        .offline: "This device is offline. Check its Wi-Fi or network connection.",
        .unreachable: "Your home can't be reached. Check that it's on and connected to the network.",
        .timedOut: "Your home took too long to answer. Try again in a moment.",
        .insecure: "A secure connection to your home couldn't be made. Check its address, or choose another home.",
        .signedOut: "Your sign-in has ended. Sign in again to continue.",
        .wrongCredentials: "That username and password don't match an account on this home.",
        .noAccess: "Your account doesn't have access to this.",
        .notFound: "This is no longer on your home.",
        .notReady: "Your home is still preparing this. Try again in a moment.",
        .busy: "Your home is busy right now. Try again in a moment.",
        .diskFull: "There isn't enough free disk space on the server to prepare this. Free some space there and try again.",
        .invalid: "Your home couldn't do that. Check and try again.",
        .badAnswer: "Your home sent an answer this app doesn't understand. Update the app or the home, then try again.",
        .server: "Something went wrong on your home. Try again in a moment.",
        .cancelled: "",
        .titleLoad: "Can't load this",
        .titleUnreachable: "Can't reach your home",
        .titleAccount: "Sign-in needed",
        .retry: "Try again",
        .signIn: "Sign in again",
        .chooseHome: "Choose another home",
        .back: "Go back",
        .restoreFailed: "You're still signed in, but your home couldn't be checked.",
    ]

    static let ar: [Key: String] = [
        .offline: "هذا الجهاز غير متصل بالشبكة. تحقق من اتصال Wi-Fi أو الشبكة.",
        .unreachable: "تعذّر الوصول إلى الجهاز الرئيسي. تأكد من أنه يعمل ومتصل بالشبكة.",
        .timedOut: "استغرق الجهاز الرئيسي وقتًا طويلًا في الرد. حاول مجددًا بعد قليل.",
        .insecure: "تعذّر إنشاء اتصال آمن بالجهاز الرئيسي. تحقق من عنوانه، أو اختر جهازًا رئيسيًا آخر.",
        .signedOut: "انتهت جلسة تسجيل الدخول. سجّل الدخول مجددًا للمتابعة.",
        .wrongCredentials: "اسم المستخدم وكلمة المرور لا يطابقان أي حساب على هذا الجهاز الرئيسي.",
        .noAccess: "لا يملك حسابك صلاحية الوصول إلى هذا.",
        .notFound: "لم يعد هذا موجودًا على الجهاز الرئيسي.",
        .notReady: "ما زال الجهاز الرئيسي يجهّز هذا. حاول مجددًا بعد قليل.",
        .busy: "الجهاز الرئيسي مشغول الآن. حاول مجددًا بعد قليل.",
        .diskFull: "لا توجد مساحة كافية على قرص الخادم لتجهيز هذا. أفرغ بعض المساحة هناك ثم أعد المحاولة.",
        .invalid: "تعذّر على الجهاز الرئيسي تنفيذ ذلك. تحقق وحاول مجددًا.",
        .badAnswer: "أرسل الجهاز الرئيسي ردًا لا يفهمه هذا التطبيق. حدّث التطبيق أو الجهاز الرئيسي ثم حاول مجددًا.",
        .server: "حدث خطأ في الجهاز الرئيسي. حاول مجددًا بعد قليل.",
        .cancelled: "",
        .titleLoad: "تعذّر التحميل",
        .titleUnreachable: "تعذّر الوصول إلى الجهاز الرئيسي",
        .titleAccount: "يلزم تسجيل الدخول",
        .retry: "حاول مجددًا",
        .signIn: "سجّل الدخول مجددًا",
        .chooseHome: "اختر جهازًا رئيسيًا آخر",
        .back: "رجوع",
        .restoreFailed: "ما زلت مسجّل الدخول، لكن تعذّر التحقق من الجهاز الرئيسي.",
    ]
}
