#if os(macOS)
import Foundation
import Testing
import MaktabaKit
@testable import Maktaba_Player_Mac

/// Story 13.16 (the desktop design's D5): Profile's Language row on the Mac asks before the
/// Player starts again in another language, and says so when it could not, in English and in
/// Arabic (the person may read only the language they are switching to): each line in its own
/// language, so it is drawn in its direction and VoiceOver reads it in its voice.
@MainActor @Suite struct MacLanguagePromptTests {
    @Test func thePromptSpeaksBothLanguagesEnglishFirst() {
        let toArabic = MacLanguagePrompt(.arabic)
        #expect(toArabic.title.map(\.text) == ["Start Maktaba Player again in Arabic?", "هل تريد إعادة تشغيل مشغل مكتبة بالعربية؟"])
        #expect(toArabic.restart.map(\.text) == ["Restart", "إعادة التشغيل"])
        #expect(toArabic.cancel.map(\.text) == ["Cancel", "إلغاء"])
        #expect(toArabic.message.map(\.text) == ["The Player closes and opens again in the new language. Anything playing stops.",
                                                 "يُغلَق المشغل ثم يُفتح من جديد باللغة الجديدة، ويتوقف ما يُعرض الآن."])
    }

    @Test func eachChoiceHasItsQuestion() {
        #expect(MacLanguagePrompt(.english).title.map(\.text)
            == ["Start Maktaba Player again in English?", "هل تريد إعادة تشغيل مشغل مكتبة بالإنجليزية؟"])
        #expect(MacLanguagePrompt(.system).title.map(\.text)
            == ["Start Maktaba Player again in the system's language?", "هل تريد إعادة تشغيل مشغل مكتبة بلغة النظام؟"])
    }

    @Test func eachLineCarriesItsLanguageForVoiceOver() {
        for line in MacLanguagePrompt(.arabic).title + MacLanguagePrompt.failure.message {
            let a = line.attributed
            let runs = Array(a.runs)
            #expect(runs.count == 1)
            #expect(runs.first?.languageIdentifier == line.language.rawValue)
        }
        #expect(MacLanguagePrompt(.arabic).title.map(\.language) == [.english, .arabic])
    }

    @Test func aFailedRestartSaysSoInBothLanguages() {
        let words = MacLanguagePrompt.failure
        #expect(words.title.map(\.text) == ["Maktaba Player could not start again", "تعذّرت إعادة تشغيل مشغل مكتبة"])
        #expect(words.ok.map(\.text) == ["OK", "حسنًا"])
        #expect(words.message.count == 2)
    }
}
#endif
