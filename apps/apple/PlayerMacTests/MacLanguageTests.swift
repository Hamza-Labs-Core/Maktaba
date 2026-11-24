#if os(macOS)
import Testing
import MaktabaKit
@testable import Maktaba_Player_Mac

/// Story 13.16 (the desktop design's D5): Profile's Language row on the Mac asks before the
/// Player starts again in the other language, in English and in Arabic, since the person may
/// read only the language they are switching to.
@MainActor @Suite struct MacLanguagePromptTests {
    @Test func thePromptSpeaksBothLanguagesEnglishFirst() {
        let toArabic = MacLanguagePrompt(.arabic)
        #expect(toArabic.title == "Start Maktaba Player again in Arabic?\nهل تريد إعادة تشغيل مشغل مكتبة بالعربية؟")
        #expect(toArabic.restart == "Restart · إعادة التشغيل")
        #expect(toArabic.cancel == "Cancel · إلغاء")
        #expect(toArabic.message == "The Player closes and opens again in the new language. Anything playing stops."
            + "\n\nيُغلَق المشغل ثم يُفتح من جديد باللغة الجديدة، ويتوقف ما يُعرض الآن.")
    }

    @Test func backToEnglishSaysEnglish() {
        let toEnglish = MacLanguagePrompt(.english)
        #expect(toEnglish.title == "Start Maktaba Player again in English?\nهل تريد إعادة تشغيل مشغل مكتبة بالإنجليزية؟")
        #expect(toEnglish.restart == MacLanguagePrompt(.arabic).restart)
    }
}
#endif
