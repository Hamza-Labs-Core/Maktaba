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
        // a part of the home that is off (story 51.09); offAdmin follows the off sentence (%@)
        case off, offAdmin
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
        case .off: text(.off)
        }
    }

    /// A feature's name (`live_tv`: the `feature` of a 409 `component_off` and the keys of
    /// `GET /api/capabilities`, story 51.09); nil for one this app does not know.
    public func featureName(_ feature: String) -> String? {
        (language == .ar ? Self.featuresAR : Self.featuresEN)[feature]
    }

    /// What a 409 `component_off` says: an admin is told where to turn the feature on;
    /// anyone else (or a feature with no name here) reads the kind's sentence.
    /// The feature's own sentence ("Transcripts are off on this home.", FeatureWords) comes
    /// first, then where to turn it on.
    public func offSentence(feature: String?, admin: Bool) -> String {
        guard admin, let feature, featureName(feature) != nil else { return text(.off) }
        let off = FeatureWords.offSentence(feature, Locale(identifier: language == .ar ? "ar" : "en"))
        return text(.offAdmin).replacingOccurrences(of: "%@", with: off)
    }

    static let featuresEN: [String: String] = [
        "live_tv": "Live TV", "transcripts": "Transcripts", "subtitles": "Subtitles",
        "semantic_search": "Search by meaning", "chapters": "Chapters", "youtube": "YouTube",
        "plex": "Plex", "remote_access": "Access away from home", "recommendations": "Recommendations",
        "plugin_panels": "Plugin panels",
    ]

    static let featuresAR: [String: String] = [
        "live_tv": "البث المباشر", "transcripts": "نصوص الفيديو", "subtitles": "الترجمة",
        "semantic_search": "البحث بالمعنى", "chapters": "الفصول", "youtube": "يوتيوب",
        "plex": "Plex", "remote_access": "الوصول من خارج المنزل", "recommendations": "الاقتراحات",
        "plugin_panels": "لوحات الإضافات",
    ]

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
        .off: "This feature is turned off on this home.",
        .offAdmin: "%@ Turn it on in Components.",
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
        .off: "هذه الميزة متوقفة على الجهاز الرئيسي.",
        .offAdmin: "%@ شغّل من صفحة المكوّنات.",
        .tryAgain: "إعادة المحاولة",
        .back: "رجوع",
        .signInAgain: "سجّل الدخول مجددًا",
        .chooseAnotherHome: "اختيار جهاز رئيسي آخر",
        .details: "التفاصيل",
        .restoreFailed: "ما زلت مسجّل الدخول، لكن تعذّر التحقق من الجهاز الرئيسي.",
    ]
}
