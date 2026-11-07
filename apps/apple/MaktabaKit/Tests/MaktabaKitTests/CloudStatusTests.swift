import Foundation
import Testing
@testable import MaktabaKit

/// Epic 50 story 50.11 (T06): Maktaba Cloud's line in the status strip, from summary.json.
struct CloudStatusTests {
    let ok = CloudSummary(components: ["relay": "operational", "api": "operational", "push": "operational"])
    let now = ISO8601DateFormatter().date(from: "2026-11-03T12:00:00Z")!
    func when(_ d: Date) -> (date: String, time: String) {
        let s = ISO8601DateFormatter().string(from: d)
        return (String(s.prefix(10)), String(s.dropFirst(11).prefix(5)))
    }

    @Test func theFeedReads() throws {
        let json = #"""
        {"updated_at":"2026-11-03T10:15:00Z","overall":"degraded",
         "components":{"relay":"degraded","api":"operational","push":"operational"},
         "incident":{"id":"2026-11-03-relay-slow","state":"identified","components":["relay"],
                     "title":{"en":"Remote access is slow","ar":"الوصول عن بُعد بطيء"},
                     "url":"https://status.maktaba.tv/i/2026-11-03-relay-slow"},
         "maintenance":[{"id":"m","starts_at":"2026-11-05T02:00:00Z","ends_at":"2026-11-05T02:30:00Z",
                         "components":["relay"],"title":{"en":"Upgrade","ar":"ترقية"},"url":null}]}
        """#
        let s = try JSONDecoder().decode(CloudSummary.self, from: Data(json.utf8))
        #expect(s.components["relay"] == "degraded" && s.incident?.id == "2026-11-03-relay-slow")
        #expect(s.maintenance.first?.startsAt == "2026-11-05T02:00:00Z")
    }

    @Test func nothingWhenAllWorksOrBeforeTheFeedAnswered() {
        #expect(CloudStatusRules.line(ok, now: now, when: when) == nil)
        #expect(CloudStatusRules.line(nil, now: now, when: when) == nil)
    }

    @Test func remoteAccessDownLinksToTheIncident() {
        var s = ok
        s.components["relay"] = "outage"
        s.incident = .init(id: "2026-11-03-relay-down", state: "investigating", components: ["relay"],
                           title: .init(en: "Down", ar: "متوقف"), url: "https://status.maktaba.tv/i/2026-11-03-relay-down")
        let l = CloudStatusRules.line(s, now: now, when: when)
        #expect(l?.text == StripWords.relayOutage)
        #expect(l?.key == "cloud:2026-11-03-relay-down")
        #expect(l?.link?.absoluteString == "https://status.maktaba.tv/i/2026-11-03-relay-down")
    }

    @Test func downBeforeSlowAndNeverForNotificationsAlone() {
        let s = CloudSummary(components: ["relay": "degraded", "api": "outage", "push": "operational"])
        #expect(CloudStatusRules.line(s, now: now, when: when)?.text == StripWords.apiOutage)
        #expect(CloudStatusRules.line(s, now: now, when: when)?.link == CloudStatusRules.site)
        #expect(CloudStatusRules.line(CloudSummary(components: ["api": "degraded"]), now: now, when: when)?.text == StripWords.apiDegraded)
        #expect(CloudStatusRules.line(CloudSummary(components: ["push": "outage"]), now: now, when: when) == nil)
    }

    @Test func maintenanceFrom24HoursAheadUntilItEnds() {
        let m = CloudSummary.Maintenance(id: "db", startsAt: "2026-11-04T02:00:00Z", endsAt: "2026-11-04T02:30:00Z",
                                         components: ["relay", "api"], title: .init(en: "Upgrade", ar: "ترقية"), url: nil)
        let s = CloudSummary(components: ok.components, maintenance: [m])
        let at = { (iso: String) in ISO8601DateFormatter().date(from: iso)! }
        #expect(CloudStatusRules.line(s, now: at("2026-11-03T01:59:00Z"), when: when) == nil)
        let l = CloudStatusRules.line(s, now: at("2026-11-03T02:00:00Z"), when: when)
        #expect(l?.text == StripWords.maintenance && l?.args == ["2026-11-04", "02:00", "02:30"] && l?.key == "cloud:db")
        #expect(CloudStatusRules.line(s, now: at("2026-11-04T02:30:00Z"), when: when) == nil)
        let push = CloudSummary(components: ok.components, maintenance: [.init(id: "p", startsAt: m.startsAt, endsAt: m.endsAt,
                                components: ["push"], title: m.title, url: nil)])
        #expect(CloudStatusRules.line(push, now: at("2026-11-04T02:10:00Z"), when: when) == nil)
    }

    @Test func comesAfterTheHomesOwnLines() {
        let cloud = CloudStatusRules.line(CloudSummary(components: ["relay": "outage"]), now: now, when: when)
        let home = HomeStatusDoc(overall: "degraded", components: ["streaming": "down", "processing": "down"])
        let lines = StatusStripRules.lines(home, online: true, admin: true, cloud: cloud)
        #expect(lines.map(\.key) == ["streaming", "cloud:relay-outage", "processing"])
        #expect(StatusStripRules.lines(nil, online: true, admin: false, cloud: cloud).map(\.key) == ["cloud:relay-outage"])
    }

    @Test func onlyThroughTheCloud() {
        #expect(CloudStatusRules.throughCloud(URL(string: "https://skylark-12.relay.maktaba.tv")))
        #expect(!CloudStatusRules.throughCloud(URL(string: "http://192.168.1.20:8080")))
        #expect(!CloudStatusRules.throughCloud(URL(string: "https://evilrelay.maktaba.tv")))
        #expect(!CloudStatusRules.throughCloud(nil))
    }

    @Test func theAddressCarriesTheMinute() {
        #expect(CloudStatusRules.summaryURL(now: now).absoluteString
            == "https://status.maktaba.tv/summary.json?t=\(Int(now.timeIntervalSince1970 / 60))")
    }

    @Test func theMaintenanceWordsTakeTheirValuesInArabic() throws {
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().appendingPathComponent("Sources/MaktabaKit/Localizable.xcstrings")
        let cat = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
        let entry = try #require((cat["strings"] as? [String: Any])?[StripWords.maintenance] as? [String: Any])
        let ar = try #require(((entry["localizations"] as? [String: Any])?["ar"] as? [String: Any])?["stringUnit"] as? [String: Any])
        let value = try #require(ar["value"] as? String)
        #expect(String(format: value, arguments: ["4 نوفمبر", "2:00", "2:30"]) == "صيانة مجدولة في 4 نوفمبر، من 2:00 إلى 2:30. قد يتوقف التشغيل عن بُعد بضع ثوانٍ.")
    }
}
