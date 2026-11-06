import Foundation
import Testing
@testable import MaktabaKit

/// Epic 53 story 53.06 T01-T04: the strip's lines for each status, in priority order;
/// off is no outage; admin-only lines; every line's words have their Arabic.
@Suite struct HomeStatusTests {
    let ok = HomeStatusDoc(components: ["database": "ok", "api": "ok", "streaming": "ok", "processing": "ok",
                                        "search": "ok", "logs": "off"])

    func doc(_ change: (inout HomeStatusDoc) -> Void) -> HomeStatusDoc {
        var d = ok
        change(&d)
        return d
    }

    @Test func nothingWhenAllIsWellOrOff() {
        #expect(StatusStripRules.lines(ok, online: true, admin: true).isEmpty)
        let off = doc { $0.components["streaming"] = "off"; $0.components["processing"] = "off" }
        #expect(StatusStripRules.lines(off, online: true, admin: true).isEmpty)
        #expect(StatusStripRules.lines(nil, online: true, admin: false).isEmpty)
    }

    @Test func thePriorityOrder() {
        let s = doc { $0.mode = "safe"; $0.overall = "down"; $0.components["api"] = "restarting"; $0.components["streaming"] = "down" }
        #expect(StatusStripRules.lines(s, online: false, admin: false).map(\.key) == ["offline", "restarting", "safe", "streaming"])
        let db = doc { $0.overall = "down"; $0.components["database"] = "down" }
        #expect(StatusStripRules.lines(db, online: true, admin: true).first == StripLine(key: "database", text: StripWords.database, details: "/recovery"))
    }

    @Test func adminsOnlyLinesAndDetails() {
        let p = doc { $0.overall = "degraded"; $0.components["processing"] = "degraded" }
        #expect(StatusStripRules.lines(p, online: true, admin: false).isEmpty)
        #expect(StatusStripRules.lines(p, online: true, admin: true).map(\.key) == ["processing"])
        let tv = doc { $0.overall = "degraded"; $0.components["streaming"] = "down" }
        #expect(StatusStripRules.lines(tv, online: true, admin: false).first?.details == nil)
        #expect(StatusStripRules.lines(tv, online: true, admin: true).first?.details == "/admin/health")
    }

    @Test func liveTVOffIsLeftOutOfThePlaybackSentence() {
        let tv = doc { $0.overall = "degraded"; $0.components["streaming"] = "down" }
        #expect(StatusStripRules.lines(tv, online: true, admin: false, liveTVOff: true).first?.text == StripWords.streamingNoTV)
        #expect(StatusStripRules.playbackDown(tv) && !StatusStripRules.playbackDown(ok) && !StatusStripRules.playbackDown(nil))
    }

    @Test func theDocumentReads() throws {
        let json = #"{"v":1,"at":"2026-11-06T10:00:00Z","mode":"safe","overall":"degraded","components":{"api":"ok","streaming":"down"},"features_down":["live_tv","playback"],"banner":"streaming_down"}"#
        let d = try JSONDecoder().decode(HomeStatusDoc.self, from: Data(json.utf8))
        #expect(d.mode == "safe" && d.components["streaming"] == "down" && d.featuresDown == ["live_tv", "playback"])
    }

    @Test func everyLineHasItsArabic() throws {
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().appendingPathComponent("Sources/MaktabaKit/Localizable.xcstrings")
        let cat = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
        let strings = try #require(cat["strings"] as? [String: Any])
        for w in StripWords.all {
            let entry = strings[w] as? [String: Any]
            let ar = (entry?["localizations"] as? [String: Any])?["ar"]
            #expect(ar != nil, "\(w) has no Arabic")
        }
    }
}
