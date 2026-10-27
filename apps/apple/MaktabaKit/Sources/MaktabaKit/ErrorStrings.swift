import Foundation

/// What a screen says when the home did not give what it asked for, in English and Arabic
/// (the same words as the web, Android and the TV apps; a home is "الجهاز الرئيسي"), kept
/// here as `PermissionsStrings` and `ConnectStrings` are.
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

    public enum Key: String, CaseIterable, Sendable {
        case playerTitle, title
        case notFoundVideo, notFound, notReady, signedOut, noAccess, unreachable, busy, server
        case tryAgain, back, signInAgain, chooseAnotherHome, details
        // the sign-in screen, when the saved sign-in could not be checked
        case restoreFailed
    }

    /// The one sentence for `kind`; `video`: said about the video being played.
    public func sentence(_ kind: ErrorKind, video: Bool = false) -> String {
        switch kind {
        case .notFound: text(video ? .notFoundVideo : .notFound)
        case .notReady: text(.notReady)
        case .signedOut: text(.signedOut)
        case .noAccess: text(.noAccess)
        case .unreachable: text(.unreachable)
        case .busy: text(.busy)
        case .server: text(.server)
        }
    }

    public func action(_ a: ErrorAction) -> String {
        switch a {
        case .tryAgain: text(.tryAgain)
        case .back: text(.back)
        case .signInAgain: text(.signInAgain)
        case .chooseAnotherHome: text(.chooseAnotherHome)
        }
    }

    static let en: [Key: String] = [
        .playerTitle: "This video can't play right now",
        .title: "Something went wrong",
        .notFoundVideo: "This video or its file is no longer on your home.",
        .notFound: "We couldn't find what you were looking for.",
        .notReady: "Your home is still preparing this video. Try again in a moment.",
        .signedOut: "Your sign-in has ended. Sign in again to keep watching.",
        .noAccess: "Your account doesn't have access to this.",
        .unreachable: "Your home can't be reached. Check that it's on and connected to the network.",
        .busy: "Your home is busy right now. Try again in a moment.",
        .server: "Something went wrong on your home. Try again in a moment.",
        .tryAgain: "Try again",
        .back: "Back",
        .signInAgain: "Sign in again",
        .chooseAnotherHome: "Choose another home",
        .details: "Details",
        .restoreFailed: "You're still signed in, but your home couldn't be checked.",
    ]

    static let ar: [Key: String] = [
        .playerTitle: "لا يمكن تشغيل هذا الفيديو الآن",
        .title: "حدث خطأ ما",
        .notFoundVideo: "لم يعد هذا الفيديو أو ملفه موجودًا على الجهاز الرئيسي.",
        .notFound: "لم نعثر على ما تبحث عنه.",
        .notReady: "ما زال الجهاز الرئيسي يجهّز هذا الفيديو. حاول مجددًا بعد قليل.",
        .signedOut: "انتهت جلسة تسجيل الدخول. سجّل الدخول مجددًا لمتابعة المشاهدة.",
        .noAccess: "لا يملك حسابك صلاحية الوصول إلى هذا.",
        .unreachable: "تعذّر الوصول إلى الجهاز الرئيسي. تأكد من أنه يعمل ومتصل بالشبكة.",
        .busy: "الجهاز الرئيسي مشغول الآن. حاول مجددًا بعد قليل.",
        .server: "حدث خطأ في الجهاز الرئيسي. حاول مجددًا بعد قليل.",
        .tryAgain: "إعادة المحاولة",
        .back: "رجوع",
        .signInAgain: "سجّل الدخول مجددًا",
        .chooseAnotherHome: "اختيار جهاز رئيسي آخر",
        .details: "التفاصيل",
        .restoreFailed: "ما زلت مسجّل الدخول، لكن تعذّر التحقق من الجهاز الرئيسي.",
    ]
}
