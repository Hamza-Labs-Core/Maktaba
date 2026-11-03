import Foundation
import Testing
@testable import MaktabaKit

// A Needs attention group says its cause in words, in the app's language: never the grouping
// key with its `kind:` and its `<n>` / `<path>`, never a backslash-n, an Arabic file name in
// its own direction. The words are the Android app's (AttentionWords.kt) and the web's.

@Suite struct AttentionTitles {
    let en = Locale(identifier: "en"), ar = Locale(identifier: "ar")

    func group(_ cause: String, label: String? = nil, sample: String? = nil) throws -> FailureGroup {
        var o: [String: Any] = ["stage": "s", "cause": cause, "label": label ?? cause, "count": 1, "action": "retry"]
        if let sample { o["sample_message"] = sample }
        return try JSONDecoder.home.decode(FailureGroup.self, from: JSONSerialization.data(withJSONObject: o))
    }

    @Test func aGeneratedCauseIsTitledByItsKind() throws {
        let model = try group("model_unavailable:<n> of <n> clips got no answer from the model")
        #expect(model.title(locale: en) == "The model did not answer")
        #expect(model.title(locale: ar) == "لم يُجب النموذج")
        let nas = try group("nas_timeout:<path> شاهين/المعجزات والخوارق..برهان النبوة الثاني<n> | دلائل النبوة <n>.mp<n>>: no answer")
        #expect(nas.title(locale: en) == "NAS read timed out")
        #expect(nas.title(locale: ar) == "انتهت مهلة القراءة من NAS")
        // a chapter's frames fail as ffmpeg does
        #expect(try group("chapter_frames_failed:<n>").title(locale: ar) == "فشل ffmpeg في معالجة الملف")
    }

    @Test func aKnownCauseIsSaidInTheAppsLanguage() throws {
        let g = try group("plex_unreachable", label: "Plex is unreachable")
        #expect(g.title(locale: en) == "Plex is unreachable")
        #expect(g.title(locale: ar) == "لا يمكن الوصول إلى Plex")
    }

    @Test func aCauseWithNoWordsHereTakesTheHomesLabelUnlessItIsAKey() throws {
        // a newer home's cause, labelled by the home (api queues/causes.go)
        let g = try group("disk_read_only", label: "Disk is read-only")
        #expect(g.title(locale: en) == "Disk is read-only")
        #expect(g.title(locale: ar) == "Disk is read-only")
        // an older home sent the key itself as the label: the kind in words instead
        #expect(try group("some_new_kind:<n> things").title(locale: en) == "Some new kind")
        #expect(AttentionWords.looksLikeKey("nas_timeout:<path>: no answer"))
        #expect(!AttentionWords.looksLikeKey("NAS read timed out"))
    }

    /// Every cause's words have their Arabic in the catalog (the web's ar.json).
    @Test func everyCauseIsInArabic() {
        for code in AttentionWords.causes.keys {
            let arabic = AttentionWords.title(cause: code, label: "", locale: ar)
            #expect(arabic.unicodeScalars.contains { (0x0600...0x06FF).contains($0.value) }, "\(code): \(arabic)")
            #expect(AttentionWords.title(cause: code, label: "", locale: en) == AttentionWords.causes[code])
        }
    }

    @Test func aMessagesEscapesAreLineBreaks() throws {
        #expect(AttentionWords.readableMessage(#"return code -22 (Invalid argument)\n[out#0/image2 @ 0x6000]\n"#)
            == "return code -22 (Invalid argument)\n[out#0/image2 @ 0x6000]")
        #expect(!AttentionWords.readableMessage(#"a\nb\r\nc"#).contains("\\"))
        #expect(try group("x", sample: #"one\ntwo"#).readableSample == "one\ntwo")
        #expect(try group("x", sample: #"\n  \n"#).readableSample == nil)
        #expect(try group("x").readableSample == nil)
    }

    @Test func anArabicFileNameInAnEnglishLineIsIsolated() {
        #expect(AttentionWords.isolateRTL("/Volumes/Media/شاهين/المعجزات.mp4: no answer")
            == "\u{2068}/Volumes/Media/شاهين/المعجزات.mp4\u{2069}: no answer")
        // nothing right-to-left: as it was
        #expect(AttentionWords.isolateRTL("read failed: head, tail") == "read failed: head, tail")
    }
}
