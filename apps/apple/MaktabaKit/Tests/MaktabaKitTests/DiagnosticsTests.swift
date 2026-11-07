import Foundation
import Testing
@testable import MaktabaKit

// Epic 53 on the Apple apps: the Remote's Errors, Logs, dashboards and inbox (53.11, 53.12,
// 53.15 §5) read the web's routes; crash reports (53.14 T04, T10) are envelopes the home
// takes, sent only when the person said yes; a problem report (53.15 T04, T08) sends what
// its form showed, the screenshot only when ticked.

/// Answers each "METHOD /path" from a queue and keeps what the app asked.
final class DiagStub: URLProtocol, @unchecked Sendable {
    struct Seen {
        let method: String
        let path: String
        let query: [String: String]
        let contentType: String?
        let body: Data
        var json: [String: Any]? { try? JSONSerialization.jsonObject(with: body) as? [String: Any] }
    }

    nonisolated(unsafe) private static var answers: [String: [(Int, String, [String: String])]] = [:]
    nonisolated(unsafe) private static var seenList: [Seen] = []
    private static let lock = NSLock()
    static let home = URL(string: "http://192.168.50.177:8088")!

    static func reset() { lock.withLock { answers = [:]; seenList = [] } }
    static func on(_ route: String, _ code: Int = 200, _ body: String = "{}", headers: [String: String] = [:]) {
        lock.withLock { answers[route, default: []].append((code, body, headers)) }
    }
    static var seen: [Seen] { lock.withLock { seenList } }

    static func client() -> APIClient {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [DiagStub.self]
        let tokens = TokenStore(service: "test.diag", home: home,
                                primary: FakeSecrets(["\(home.absoluteString)#access": "a-diag"]), legacy: nil)
        return APIClient(home: home, tokens: tokens, session: URLSession(configuration: c))
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody ?? Data()
        if body.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buf = [UInt8](repeating: 0, count: 65536)
            while stream.hasBytesAvailable {
                let n = stream.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                body.append(buf, count: n)
            }
            stream.close()
        }
        let url = request.url!
        let q = Dictionary((URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []).map { ($0.name, $0.value ?? "") },
                           uniquingKeysWith: { a, _ in a })
        let method = request.httpMethod ?? "GET"
        let route = "\(method) \(url.path(percentEncoded: true))"
        let (code, text, headers): (Int, String, [String: String]) = Self.lock.withLock {
            Self.seenList.append(Seen(method: method, path: url.path(percentEncoded: true), query: q,
                                      contentType: request.value(forHTTPHeaderField: "Content-Type"), body: body))
            if var queue = Self.answers[route], !queue.isEmpty {
                let a = queue.count > 1 ? queue.removeFirst() : queue[0]
                Self.answers[route] = queue
                return a
            }
            return (599, "no stub for \(route)", [:])
        }
        var h = ["Content-Type": "application/json"]
        h.merge(headers) { _, b in b }
        let resp = HTTPURLResponse(url: url, statusCode: code, httpVersion: "HTTP/1.1", headerFields: h)!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(text.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

/// One at a time: they share DiagStub.
@Suite(.serialized) struct DiagnosticsOverHTTP {}

extension DiagnosticsOverHTTP {
@Suite struct RemoteDiagnostics {
    init() { DiagStub.reset() }

    static let groups = #"""
    {"groups":[{"fp":"a1b2","service":"api","component":"core.api","code":"pq_timeout","title":"database timed out",
      "first_seen":"2026-11-01T10:00:00Z","last_seen":"2026-11-06T09:30:00Z","count":42,"count_24h":7,"users":3,
      "status":"new","last_version":"0.3.90","last_request_id":"0192f0c4-1111-7000-8000-000000000001",
      "days":[0,0,1,2,0,0,0,0,0,5,0,0,3,7]},
     {"fp":"ff00","service":"apple.remote","title":"EXC_BAD_ACCESS","first_seen":"","last_seen":"2026-11-05T09:30:00Z",
      "count":1,"count_24h":0,"users":1,"status":"muted","muted_until":"2026-11-07T00:00:00Z"}]}
    """#

    @Test func errorGroupsDecodeAndAskWithTheWebsFilters() async throws {
        DiagStub.on("GET /api/admin/errors", 200, Self.groups)
        let gs = try await DiagStub.client().errorGroups(.open, sort: .count)
        #expect(gs.map(\.fp) == ["a1b2", "ff00"])
        #expect(gs[0].count24h == 7 && gs[0].users == 3 && gs[0].days.count == 14 && gs[0].component == "core.api")
        #expect(gs[1].firstSeen == nil && gs[1].mutedUntil != nil && gs[1].component == nil)
        let q = try #require(DiagStub.seen.last).query
        #expect(q == ["status": "new,seen", "sort": "count"])
        _ = try await DiagStub.client().errorGroups(.all)
        #expect(DiagStub.seen.last?.query == ["sort": "last_seen"])
    }

    @Test func aGroupsPageHasItsHoursSamplesVersionsAndMachines() async throws {
        DiagStub.on("GET /api/admin/errors/a1b2", 200, #"""
        {"group":{"fp":"a1b2","service":"api","title":"database timed out","count":2,"count_24h":2,"users":1,"status":"seen",
                  "first_seen":"2026-11-06T08:00:00Z","last_seen":"2026-11-06T09:00:00Z"},
         "hours":{"2026-11-06T09:00:00Z":1,"2026-11-06T08:00:00Z":1},
         "samples":{"lines":[{"ts":"2026-11-06T09:00:00.5Z","level":"error","service":"api","node":"home","msg":"db_timeout",
                              "version":"0.3.90","request_id":"0192f0c4-1111-7000-8000-000000000001","err":{"code":"pq_timeout"},"took_ms":5000}],
                    "ids":[991]}}
        """#)
        let d = try await DiagStub.client().errorGroup("a1b2")
        #expect(d.hourly.map(\.count) == [1, 1] && d.hourly[0].at < d.hourly[1].at)
        #expect(d.versions == ["0.3.90"] && d.machines == ["home"])
        let row = try #require(d.samples.rows.first)
        #expect(row.id == 991 && row.line.isError && row.line.requestID?.hasPrefix("0192f0c4") == true)
        #expect(row.line.fields["took_ms"] == "5000" && row.line.fields["err"] == #"{"code":"pq_timeout"}"#)
    }

    @Test func statusActionsPostTheWebsBodies() async throws {
        DiagStub.on("POST /api/admin/errors/a1b2/status", 204, "")
        let c = DiagStub.client()
        try await c.setErrorStatus("a1b2", .seen)
        #expect(DiagStub.seen.last?.json?["status"] as? String == "seen")
        try await c.setErrorStatus("a1b2", .resolve(version: "0.3.90"))
        #expect(DiagStub.seen.last?.json?["version"] as? String == "0.3.90")
        let until = Date(timeIntervalSince1970: 1_800_000_000)
        try await c.setErrorStatus("a1b2", .mute(until: until))
        let j = try #require(DiagStub.seen.last?.json)
        #expect(j["status"] as? String == "muted" && (j["muted_until"] as? String)?.hasPrefix("2027-01-15T08:00:00") == true)
        try await c.setErrorStatus("a1b2", .mute(until: nil))
        #expect(DiagStub.seen.last?.json?["muted_until"] == nil)
    }

    @Test func aSearchTakesWordsLevelAndWindowAndAReferenceFollowsTheRequest() async throws {
        DiagStub.on("GET /api/admin/logs/search", 200, #"{"lines":[],"ids":null}"#)
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let r = try await DiagStub.client().searchLogs(LogQuery(text: " playback -debug ", level: "warn", window: .hour), now: now)
        #expect(r.lines.isEmpty && r.ids.isEmpty)
        let q = try #require(DiagStub.seen.last).query
        #expect(q["q"] == "playback -debug" && q["level"] == "warn" && q["since"]?.hasPrefix("2027-01-15T07:00:00") == true)
        #expect(LogQuery.requestReference("0192F0C4") == "0192f0c4")
        #expect(LogQuery.requestReference("0192f0c4-1111-7000-8000-000000000001") != nil)
        #expect(LogQuery.requestReference("playback") == nil && LogQuery.requestReference("0192f0c") == nil)
        _ = try await DiagStub.client().searchLogs(LogQuery(text: "ignored", requestID: "0192f0c4"), now: now)
        #expect(DiagStub.seen.last?.query == ["request_id": "0192f0c4"])
    }

    @Test func aRestartingIndexIsA503TheScreenCanSay() async throws {
        DiagStub.on("GET /api/admin/logs/search", 503, #"{"type":"about:blank","title":"log search is restarting","status":503,"code":"log_index_unavailable"}"#)
        await #expect(throws: APIError.self) { _ = try await DiagStub.client().searchLogs(LogQuery()) }
    }

    @Test func gapsOverASecondAreMarked() throws {
        let json = #"[{"ts":"2026-11-06T09:00:00Z","msg":"a"},{"ts":"2026-11-06T09:00:00.4Z","msg":"b"},{"ts":"2026-11-06T09:00:02Z","msg":"c"}]"#
        let lines = try JSONDecoder.home.decode([LogLine].self, from: Data(json.utf8))
        #expect(gapsOverASecond(lines) == [false, false, true])
    }

    @Test func boardsAreGroupedIntoPanelsWithTheirLegends() async throws {
        DiagStub.on("GET /api/admin/dashboards/storage", 200, #"""
        {"board":"storage","range":"7d","step":3600,"series":[
          {"name":"maktaba_logs_bytes","labels":"kind=segments","kind":"gauge","points":[[1800000000,1024],[1800003600,2048]]},
          {"name":"maktaba_logs_bytes","labels":"kind=index","kind":"gauge","points":[[1800000000,512]]},
          {"name":"maktaba_disk_free_bytes","kind":"gauge","points":[[1800000000,9.5e10]]}]}
        """#)
        let b = try await DiagStub.client().board(.storage, range: .week)
        #expect(DiagStub.seen.last?.query == ["range": "7d"])
        #expect(b.panels.map(\.name) == ["maktaba_logs_bytes", "maktaba_disk_free_bytes"])
        #expect(b.panels[0].series.map(\.legend) == ["segments", "index"] && b.panels[1].series[0].legend == "")
        #expect(b.panels[0].series[0].points[1].value == 2048)
    }

    @Test func theInboxReadsAReportsTimelineAndChangesIt() async throws {
        DiagStub.on("GET /api/reports", 200, #"{"reports":[{"id":27,"at":"2026-11-06T09:00:00Z","name":"Test Viewer","app":"apple.player","category":"playback","status":"new","shared_with_maktaba":false}]}"#)
        DiagStub.on("GET /api/reports/27", 200, #"""
        {"id":27,"at":"2026-11-06T09:00:00Z","when":"2026-11-06T08:59:00Z","name":"Test Viewer","app":"apple.player","version":"0.3.90",
         "platform":"ios","device_model":"iPhone17,1","category":"playback","text":"it stopped","status":"new","shared_with_maktaba":false,
         "screenshot":false,"request_ids":["0192f0c4"],
         "lines":[{"ts":"2026-11-06T08:59:02Z","level":"warn","service":"apple.player","msg":"stall"}],
         "server":[{"ts":"2026-11-06T08:59:01Z","level":"error","service":"streaming","msg":"segment_missing"}],
         "crashes":[{"platform":"cocoa"}]}
        """#)
        DiagStub.on("POST /api/reports/27/status", 200, #"{"id":27}"#)
        DiagStub.on("POST /api/reports/27/reply", 200, #"{"id":27}"#)
        let c = DiagStub.client()
        #expect(try await c.problemReports().map(\.id) == [27])
        let d = try await c.problemReport(27)
        #expect(d.summary.name == "Test Viewer" && d.crashes == 1 && d.device == "iPhone17,1")
        #expect(d.timeline.map(\.fromHome) == [true, false])
        try await c.setReportStatus(27, .looking)
        #expect(DiagStub.seen.last?.json?["status"] as? String == "looking")
        try await c.replyToReport(27, "Fixed in the next release")
        #expect(DiagStub.seen.last?.json?["reply"] as? String == "Fixed in the next release")
    }
}

}

// MARK: crash reports (53.14)

@Suite struct CrashEnvelopes {
    static let header = Envelope.Header(app: "apple.remote", appVersion: "0.3.90", platform: "ios", osVersion: "26.0",
                                        deviceModel: "iPhone17,1", installID: "install-1")

    /// A MetricKit payload as `jsonRepresentation()` writes it: the crashed thread's root frame
    /// is the top of the stack.
    static let metricKit = #"""
    {"timeStampEnd":"2026-11-06 09:00:00 +0000","crashDiagnostics":[{
      "diagnosticMetaData":{"appVersion":"0.3.90","appBuildVersion":"412","osVersion":"iPhone OS 26.0 (23A)","deviceType":"iPhone17,1",
        "exceptionType":1,"signal":11,"exceptionCode":0,"terminationReason":"Namespace SIGNAL, Code 11 Authorization: Bearer abcdef.ghijkl"},
      "callStackTree":{"callStackPerThread":true,"callStacks":[
        {"threadAttributed":false,"callStackRootFrames":[{"binaryName":"libsystem_kernel.dylib","binaryUUID":"K","offsetIntoBinaryTextSegment":1,"address":1}]},
        {"threadAttributed":true,"callStackRootFrames":[
          {"binaryName":"Maktaba Remote","binaryUUID":"UUID-APP","offsetIntoBinaryTextSegment":4660,"address":4300000000,"subFrames":[
            {"binaryName":"SwiftUI","binaryUUID":"UUID-SUI","offsetIntoBinaryTextSegment":16,"address":4400000000,"subFrames":[
              {"binaryName":"Maktaba Remote","binaryUUID":"UUID-APP","offsetIntoBinaryTextSegment":255,"address":4300000100}]}]}]}]}}]}
    """#

    static func parse(_ env: Data) throws -> (header: [String: Any], items: [(type: String, payload: Data)]) {
        var rest = env[...]
        func line() -> Data? {
            guard let nl = rest.firstIndex(of: 0x0A) else { return nil }
            defer { rest = rest[(nl + 1)...] }
            return Data(rest[..<nl])
        }
        let first = try #require(line())
        let hdr = try #require(try JSONSerialization.jsonObject(with: first) as? [String: Any])
        var items: [(String, Data)] = []
        while let ih = line(), !ih.isEmpty {
            let h = try #require(try JSONSerialization.jsonObject(with: ih) as? [String: Any])
            let n = try #require(h["length"] as? Int)
            items.append((h["type"] as? String ?? "", Data(rest.prefix(n))))
            rest = rest.dropFirst(n + 1)
        }
        return (hdr, items)
    }

    @Test func aMetricKitCrashIsOneScrubbedEventWithTheConsentInItsHeader() throws {
        let events = Envelope.events(fromFile: Data(Self.metricKit.utf8), inApp: "Maktaba Remote")
        #expect(events.count == 1)
        let env = Envelope.build(Self.header, event: events[0], log: [#"{"msg":"launched"}"#, #"{"msg":"Authorization: Bearer abcdef.ghijkl"}"#])
        let (hdr, items) = try Self.parse(env)
        #expect(hdr["consent"] as? String == "crash_reports_to_home" && hdr["app"] as? String == "apple.remote")
        #expect(hdr["install_id"] as? String == "install-1" && hdr["device_model"] as? String == "iPhone17,1")
        #expect(items.map(\.type) == ["event", "attachment"])
        let e = try #require(try JSONSerialization.jsonObject(with: items[0].payload) as? [String: Any])
        let ex = try #require(((e["exception"] as? [String: Any])?["values"] as? [[String: Any]])?.first)
        #expect(ex["type"] as? String == "EXC_BAD_ACCESS")
        let value = try #require(ex["value"] as? String)
        #expect(value.hasPrefix("SIGSEGV · Namespace SIGNAL") && value.contains("Bearer [REDACTED]") && !value.contains("abcdef"))
        let frames = try #require((ex["stacktrace"] as? [String: Any])?["frames"] as? [[String: Any]])
        // oldest first, the crashing frame last: the home groups on the last own frame
        #expect(frames.map { $0["function"] as? String } == ["Maktaba Remote + 0xff", "SwiftUI + 0x10", "Maktaba Remote + 0x1234"])
        #expect(frames.map { $0["in_app"] as? Bool } == [true, false, true])
        let images = try #require((e["debug_meta"] as? [String: Any])?["images"] as? [[String: String]])
        #expect(Set(images.compactMap { $0["debug_id"] }) == ["UUID-APP", "UUID-SUI"])
        #expect(e["release"] as? String == "0.3.90 (412)")
        let log = String(decoding: items[1].payload, as: UTF8.self)
        #expect(log.contains("launched") && !log.contains("abcdef.ghijkl"))
    }

    @Test func theEnvelopeStaysUnderTheHomesLimitsWithTheNewestLinesKept() throws {
        let events = Envelope.events(fromFile: Data(Self.metricKit.utf8), inApp: "Maktaba Remote")
        let lines = (0..<20000).map { #"{"n":\#($0),"msg":"a line of the app's log, long enough to add up"}"# }
        let env = Envelope.build(Self.header, event: events[0], log: lines)
        #expect(env.count <= Envelope.maxBytes)
        let (_, items) = try Self.parse(env)
        let log = String(decoding: items[1].payload, as: UTF8.self)
        #expect(items[1].payload.count <= Envelope.maxAttachment)
        #expect(log.hasSuffix(#""n":19999,"msg":"a line of the app's log, long enough to add up"}"#) && !log.contains(#""n":0,"#))
    }

    /// The Apple TV's PLCrashReporter report, kept as a `KeptCrash`.
    @Test func anAppleTVCrashIsTheSameEnvelope() throws {
        let kept = KeptCrash(at: Date(timeIntervalSince1970: 1_800_000_000), appVersion: "0.3.90", build: "412", osVersion: "26.0",
                             device: "AppleTV14,1", type: "SIGABRT", reason: "NSInvalidArgumentException: someone@example.com",
                             frames: [.init(binary: "Maktaba TV", offset: 0x40, address: 0x1000),
                                      .init(binary: "UIKitCore", offset: 0x20, address: 0x2000)],
                             images: ["Maktaba TV": "UUID-TV", "UIKitCore": "UUID-UIK", "unused": "U"])
        let events = Envelope.events(fromFile: kept.data(), inApp: "Maktaba TV")
        #expect(events.count == 1)
        let ex = try #require(((events[0]["exception"] as? [String: Any])?["values"] as? [[String: Any]])?.first)
        #expect(ex["type"] as? String == "SIGABRT")
        #expect(((ex["mechanism"] as? [String: Any])?["type"] as? String) == "plcrashreporter")
        let frames = try #require((ex["stacktrace"] as? [String: Any])?["frames"] as? [[String: Any]])
        #expect(frames.last?["function"] as? String == "Maktaba TV + 0x40" && frames.last?["in_app"] as? Bool == true)
        let images = (events[0]["debug_meta"] as? [String: Any])?["images"] as? [[String: String]] ?? []
        #expect(images.count == 2)
        let env = Envelope.build(Self.header, event: events[0], log: [])
        let (_, items) = try Self.parse(env)
        #expect(items.map(\.type) == ["event"])
        #expect(!String(decoding: items[0].payload, as: UTF8.self).contains("someone@example.com"))
    }

    @Test func somethingElseIsNoEvent() {
        #expect(Envelope.events(fromFile: Data(#"{"hello":1}"#.utf8)).isEmpty)
        #expect(Envelope.events(fromFile: Data("not json".utf8)).isEmpty)
    }
}

@Suite struct CrashSending {
    let dir: URL
    let outbox: CrashOutbox

    init() throws {
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("crash-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        outbox = CrashOutbox(directory: dir)
    }

    func keep(at t: Int, _ json: String = CrashEnvelopes.metricKit) throws -> String {
        let name = "crash-\(t)-abcd.json"
        try Data(json.utf8).write(to: dir.appendingPathComponent(name))
        return name
    }

    final class Sends: @unchecked Sendable {
        var bodies: [Data] = []
        var answers: [CrashOutbox.SendResult] = []
        func send(_ d: Data) -> CrashOutbox.SendResult {
            bodies.append(d)
            return answers.isEmpty ? .sent : answers.removeFirst()
        }
    }

    @Test func offSendsNothingAndTheCrashIsKept() async throws {
        let name = try keep(at: 1_800_000_000)
        let s = Sends()
        let n = await outbox.flush(on: false, header: CrashEnvelopes.header, log: []) { s.send($0) }
        #expect(n == 0 && s.bodies.isEmpty)
        #expect(FileManager.default.fileExists(atPath: dir.appendingPathComponent(name).path))
        #expect(outbox.notice() == .kept)
        #expect(outbox.notice() == nil, "said once")
    }

    @Test func onSendsEachCrashOnce() async throws {
        let name = try keep(at: 1_800_000_000)
        let s = Sends()
        #expect(await outbox.flush(on: true, header: CrashEnvelopes.header, log: []) { s.send($0) } == 1)
        #expect(await outbox.flush(on: true, header: CrashEnvelopes.header, log: []) { s.send($0) } == 0)
        #expect(s.bodies.count == 1 && outbox.wasSent(name))
        #expect(String(decoding: s.bodies[0].prefix(400), as: UTF8.self).contains(#""consent":"crash_reports_to_home""#))
        #expect(outbox.notice() == .sent)
    }

    @Test func a429KeepsItForLaterAndAtMostThreeRetries() async throws {
        let t0 = Date(timeIntervalSince1970: 1_800_000_000)
        let name = try keep(at: Int(t0.timeIntervalSince1970))
        let s = Sends()
        s.answers = [.later(120), .later(nil), .later(nil), .later(nil)]
        _ = await outbox.flush(on: true, header: CrashEnvelopes.header, log: [], now: t0) { s.send($0) }
        // not before the home's Retry-After
        _ = await outbox.flush(on: true, header: CrashEnvelopes.header, log: [], now: t0.addingTimeInterval(60)) { s.send($0) }
        #expect(s.bodies.count == 1)
        _ = await outbox.flush(on: true, header: CrashEnvelopes.header, log: [], now: t0.addingTimeInterval(121)) { s.send($0) }
        _ = await outbox.flush(on: true, header: CrashEnvelopes.header, log: [], now: t0.addingTimeInterval(2 * 86400)) { s.send($0) }
        _ = await outbox.flush(on: true, header: CrashEnvelopes.header, log: [], now: t0.addingTimeInterval(5 * 86400)) { s.send($0) }
        _ = await outbox.flush(on: true, header: CrashEnvelopes.header, log: [], now: t0.addingTimeInterval(6 * 86400)) { s.send($0) }
        #expect(s.bodies.count == 4, "the first try and three retries")
        #expect(!outbox.wasSent(name))
        #expect(outbox.notice() == .kept)
    }

    @Test func aCrashOlderThanAWeekIsNotRetried() async throws {
        let t0 = Date(timeIntervalSince1970: 1_800_000_000)
        _ = try keep(at: Int(t0.timeIntervalSince1970))
        let s = Sends()
        s.answers = [.later(nil)]
        _ = await outbox.flush(on: true, header: CrashEnvelopes.header, log: [], now: t0) { s.send($0) }
        _ = await outbox.flush(on: true, header: CrashEnvelopes.header, log: [], now: t0.addingTimeInterval(8 * 86400)) { s.send($0) }
        #expect(s.bodies.count == 1)
    }

    @Test func aRefusalStopsSending() async throws {
        _ = try keep(at: 1_800_000_000)
        _ = try keep(at: 1_800_000_001)
        let s = Sends()
        s.answers = [.refused]
        _ = await outbox.flush(on: true, header: CrashEnvelopes.header, log: []) { s.send($0) }
        #expect(s.bodies.count == 1)
    }

    @Test func theQuestionIsAskedOncePerHomeAndStartsOff() throws {
        let d = try #require(UserDefaults(suiteName: "test.crash.\(UUID().uuidString)"))
        let c = CrashConsent(defaults: d)
        let home = Home(url: URL(string: "http://192.168.50.177:8088")!, name: "Test Home", homeID: "h1")
        let other = Home(url: URL(string: "http://192.168.50.178:8088")!, name: "Other")
        #expect(c.answer(home) == nil && !c.isOn(home), "a fresh install, or one updated from before: asked, off")
        c.set(false, for: home)
        #expect(c.answer(home) == false && !c.isOn(home))
        c.set(true, for: home)
        #expect(c.isOn(home) && c.answer(other) == nil)
        #expect(CrashConsent.questionAllowed(["app"]))
        #expect(!CrashConsent.questionAllowed(["app", "-maktaba-permissions-fake", "granted"]), "other UI tests never see it")
        #expect(CrashConsent.questionAllowed(["app", "-uitest-reset", "-maktaba-crash-question"]))
    }
}

// MARK: Report a problem (53.15)

extension DiagnosticsOverHTTP {
@Suite struct ProblemReports {
    init() { DiagStub.reset() }

    static let now = Date(timeIntervalSince1970: 1_800_000_000)

    static func line(_ offset: TimeInterval, _ msg: String) -> String {
        #"{"level":"info","msg":"\#(msg)","service":"apple.player","ts":"\#(LogRing.timestamp(now.addingTimeInterval(offset)))"}"#
    }

    @Test func theLogIsTheLastThirtyMinutesScrubbed() {
        let all = [Self.line(-3600, "old"), Self.line(-600, "recent"), Self.line(-60, "mail someone@example.com"), "not a line"]
        let l = ReportProblem.lines(all, now: Self.now)
        #expect(l.count == 2 && l[0].contains("recent") && !l[1].contains("someone@example.com"))
        let many = (0..<3000).map { Self.line(-Double($0 % 1000), "n\($0)") }
        #expect(ReportProblem.lines(many, now: Self.now).count == 2000)
    }

    @Test func thePartIsWhatTheFormShowedAndFitsTheHomesLimit() throws {
        var d = ReportDraft(category: .playback, text: String(repeating: "x", count: 2500), reference: "0192f0c4")
        d.earlier = Self.now.addingTimeInterval(-3600)
        let part = ReportProblem.part(d, lines: [Self.line(-10, "stall")], crashes: [["platform": "cocoa"]], header: CrashEnvelopes.header,
                                      requests: ["aaaa1111-0000-7000-8000-000000000000"], screenshotPreviewed: false)
        let j = try #require(try JSONSerialization.jsonObject(with: part) as? [String: Any])
        #expect(j["category"] as? String == "playback" && (j["text"] as? String)?.count == 2000)
        #expect(j["request_ids"] as? [String] == ["aaaa1111-0000-7000-8000-000000000000", "0192f0c4"])
        #expect(j["screenshot_previewed"] as? Bool == false && j["share_with_maktaba"] as? Bool == false)
        #expect((j["crashes"] as? [Any])?.count == 1 && (j["lines"] as? [Any])?.count == 1)
        #expect((j["when"] as? String).flatMap(homeTime) == Self.now.addingTimeInterval(-3600))
        d.includeCrashes = false
        let without = try #require(try JSONSerialization.jsonObject(with: ReportProblem.part(d, lines: [], crashes: [["a": 1]],
            header: CrashEnvelopes.header, requests: [], screenshotPreviewed: false)) as? [String: Any])
        #expect(without["crashes"] == nil, "unticked: not sent")
        let big = (0..<2000).map { Self.line(-1, "line \($0) " + String(repeating: "y", count: 400)) }
        let fitted = ReportProblem.part(d, lines: big, crashes: [], header: CrashEnvelopes.header, requests: [], screenshotPreviewed: false)
        #expect(fitted.count <= ReportProblem.partMax)
    }

    @Test func theScreenshotGoesOnlyWhileTicked() async throws {
        DiagStub.on("POST /api/reports", 200, #"{"id":27}"#)
        let shot = Data([0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3])
        var d = ReportDraft(category: .playback)
        #expect(!d.includeScreenshot, "unticked when the form opens")
        let c = DiagStub.client()
        #expect(try await ReportProblem.send(d, screenshot: shot, client: c, lines: [], crashes: [], header: CrashEnvelopes.header) == 27)
        var body = try #require(DiagStub.seen.last).body
        #expect(!String(decoding: body, as: UTF8.self).contains("name=\"screenshot\""))
        #expect(String(decoding: body, as: UTF8.self).contains(#""screenshot_previewed":false"#))
        #expect(DiagStub.seen.last?.contentType?.hasPrefix("multipart/form-data; boundary=") == true)
        d.includeScreenshot = true
        _ = try await ReportProblem.send(d, screenshot: shot, client: c, lines: [], crashes: [], header: CrashEnvelopes.header)
        body = try #require(DiagStub.seen.last).body
        let text = String(decoding: body, as: UTF8.self)
        #expect(text.contains("name=\"screenshot\"") && text.contains(#""screenshot_previewed":true"#))
        #expect(body.range(of: shot) != nil)
    }

    @Test func theDailyLimitIsA429() async throws {
        DiagStub.on("POST /api/reports", 429, #"{"type":"about:blank","title":"too many reports from this device today","status":429}"#,
                    headers: ["Retry-After": "3600"])
        await #expect(throws: APIError.status(429, #"{"type":"about:blank","title":"too many reports from this device today","status":429}"#, retryAfter: 3600)) {
            _ = try await ReportProblem.send(ReportDraft(), screenshot: nil, client: DiagStub.client(), lines: [], crashes: [], header: CrashEnvelopes.header)
        }
    }

    @Test func everyWordHasItsArabic() {
        let en = Locale(identifier: "en"), ar = Locale(identifier: "ar")
        for c in ReportCategory.allCases {
            #expect(ReportWords.category(c) != c.rawValue)
        }
        let pairs: [(String, String)] = [
            (kit("Report a problem", en), kit("Report a problem", ar)),
            (kit("Send crash reports", en), kit("Send crash reports", ar)),
            (kit("Not now", en), kit("Not now", ar)),
            (kit("Include a screenshot", en), kit("Include a screenshot", ar)),
            (kit("Looking into it", en), kit("Looking into it", ar)),
        ]
        for (e, a) in pairs {
            #expect(e != a, "\(e) has its Arabic")
            #expect(a.unicodeScalars.contains { (0x0600...0x06FF).contains($0.value) })
        }
        #expect(kit("Help \("Test Home") fix problems?", ar).contains("Test Home"))
    }
}
}
