import Foundation
@testable import MaktabaKit
import Testing

/// Every Arabic number, duration, and date across Apple apps uses Western digits (0–9),
/// never Eastern Arabic digits (٠–٩), even when the user's region defaults to `arab` (#1199).
@Suite("Western digits in Arabic")
struct WesternDigitsTests {
    private static let easternArabicDigits = CharacterSet(charactersIn: "٠١٢٣٤٥٦٧٨٩")
    private static let arabicLocales = [
        Locale(identifier: "ar"),
        Locale(identifier: "ar_SA"),
        Locale(identifier: "ar_EG"),
        Locale(identifier: "ar_AE"),
        Locale(identifier: "ar_SA@numbers=arab"),
    ]

    private func assertNoEasternArabicDigits(_ text: String, sourceLocation: SourceLocation = #_sourceLocation) {
        #expect(text.rangeOfCharacter(from: Self.easternArabicDigits) == nil,
                "Expected Western digits only, got: \(text)", sourceLocation: sourceLocation)
    }

    @Test func westernDigitsNormalizesArabicLocalesAndLeavesEnglishAlone() {
        for loc in Self.arabicLocales {
            let normalized = AppLanguage.westernDigits(loc)
            #expect(normalized.language.languageCode?.identifier == "ar")
            let dur = Duration.seconds(480).formatted(.units(allowed: [.hours, .minutes], width: .abbreviated).locale(normalized))
            #expect(dur.contains("8"), "got \(dur)")
            assertNoEasternArabicDigits(dur)
            let clock = Duration.seconds(380).formatted(.time(pattern: .minuteSecond).locale(normalized))
            #expect(clock == "6:20" || clock.contains("6:20"), "got \(clock)")
            assertNoEasternArabicDigits(clock)
            let year = 2024.formatted(.number.grouping(.never).locale(normalized))
            #expect(year == "2024")
            let date = Date(timeIntervalSince1970: 1_793_999_100)
                .formatted(Date.FormatStyle(date: .abbreviated, time: .shortened).locale(normalized))
            assertNoEasternArabicDigits(date)
        }
        let en = Locale(identifier: "en_US")
        #expect(AppLanguage.westernDigits(en) == en)
    }

    @Test func kitFormattersEmitWesternDigitsForAllArabicLocales() {
        let now = Date(timeIntervalSince1970: 1_793_999_100)
        let later = now.addingTimeInterval(3 * 86_400 + 7_200)
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(secondsFromGMT: 0)!

        for loc in Self.arabicLocales {
            assertNoEasternArabicDigits(SearchWords.number(1234, loc))
            #expect(SearchWords.number(1234, loc).contains("1"))
            assertNoEasternArabicDigits(SearchWords.bucket(.year, "2024", label: nil, loc))
            #expect(SearchWords.bucket(.year, "2024", label: nil, loc) == "2024")
            assertNoEasternArabicDigits(SearchWords.band("under_5m", loc))
            assertNoEasternArabicDigits(SearchWords.videos(12, loc))

            assertNoEasternArabicDigits(AIWords.clock(380, loc))
            #expect(AIWords.clock(380, loc) == "6:20")
            assertNoEasternArabicDigits(AIWords.waiting(4, loc))
            assertNoEasternArabicDigits(AIWords.citation(2, title: "الصبر", at: AIWords.clock(1622.5, loc), loc))

            assertNoEasternArabicDigits(ParentalWords.length(seconds: 480, loc))
            #expect(ParentalWords.length(seconds: 480, loc).contains("8"))
            assertNoEasternArabicDigits(ParentalWords.clock(now, loc, zone: TimeZone(secondsFromGMT: 0)!))
            let timeUp = TimeUp(reason: "limit_reached", nextAllowedAt: later)
            if let next = timeUp.nextLine(loc, now: now, calendar: cal) {
                assertNoEasternArabicDigits(next)
            }

            let hold = ComponentHold(component: "transcription", stopped: false, queued: 1840)
            assertNoEasternArabicDigits(QueueWords.componentLine(hold, loc))

            assertNoEasternArabicDigits(UpdateWords.day(later, now: now, calendar: cal, locale: loc))
            assertNoEasternArabicDigits(UpdateWords.span(until: later, now: now, locale: loc))

            assertNoEasternArabicDigits(AgeTier.label(13, loc))
            #expect(AgeTier.label(13, loc).contains("13"))
            assertNoEasternArabicDigits(ActivityModel.heading(later, now: now, calendar: cal, loc))
        }
        let streamLimit = ErrorStrings(.ar).sentence(.relay(.streamLimit(3)))
        assertNoEasternArabicDigits(streamLimit)
        #expect(streamLimit.contains("3"))
    }
}
