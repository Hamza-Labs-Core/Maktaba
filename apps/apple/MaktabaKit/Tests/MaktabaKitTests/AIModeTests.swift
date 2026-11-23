import Foundation
import Testing
@testable import MaktabaKit

// Story 49.14: AI mode in the Apple apps. `POST /api/search/ai` (story 49.12) streams Server-
// Sent Events; the shapes here are those `api/internal/handlers/search/ai.go` writes. A stub
// answers in place of the home: a status, then the stream in pieces, the rest held until the
// test lets it go (or the app closes the stream).

/// Answers `POST /api/search/ai` with `status` and `pieces`; with `holdAfter`, only the first
/// pieces come until `letGo` (or the request is stopped).
final class AIStub: URLProtocol, @unchecked Sendable {
    struct Script {
        var status = 200
        var headers: [String: String] = [:]
        var pieces: [String] = []
        var holdAfter: Int?
    }

    nonisolated(unsafe) private static var script = Script()
    nonisolated(unsafe) private static var bodies: [[String: Any]] = []
    nonisolated(unsafe) private static var accepts: [String] = []
    nonisolated(unsafe) private static var stops = 0
    nonisolated(unsafe) private static var gate = DispatchSemaphore(value: 0)
    private static let lock = NSLock()
    private let done = NSLock()
    nonisolated(unsafe) private var stopped = false

    static func reset(_ s: Script = Script()) {
        lock.withLock {
            script = s
            bodies = []
            accepts = []
            stops = 0
            gate = DispatchSemaphore(value: 0)
        }
    }

    static func letGo() { lock.withLock { gate }.signal() }
    static var asked: [[String: Any]] { lock.withLock { bodies } }
    static var accept: [String] { lock.withLock { accepts } }
    static var stopCount: Int { lock.withLock { stops } }

    static func client() -> APIClient {
        let home = URL(string: "http://ai.test:8088")!
        let tokens = TokenStore(app: "test.ai", home: home)
        tokens.clear()
        tokens.save(AuthTokens(accessToken: "access.ai.x", refreshToken: "refresh-ai", user: nil))
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [AIStub.self]
        return APIClient(home: home, tokens: tokens, session: URLSession(configuration: config))
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody
        if body == nil, let stream = request.httpBodyStream {
            stream.open()
            var d = Data()
            var buf = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                d.append(buf, count: n)
            }
            stream.close()
            body = d
        }
        let (s, gate) = Self.lock.withLock {
            Self.bodies.append(body.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } ?? [:])
            Self.accepts.append(request.value(forHTTPHeaderField: "Accept") ?? "")
            return (Self.script, Self.gate)
        }
        var headers = s.headers
        headers["Content-Type"] = s.status < 300 ? "text/event-stream" : "application/problem+json"
        let resp = HTTPURLResponse(url: request.url!, statusCode: s.status, httpVersion: "HTTP/1.1", headerFields: headers)!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        let first = s.holdAfter.map { Array(s.pieces.prefix($0)) } ?? s.pieces
        for p in first { client?.urlProtocol(self, didLoad: Data(p.utf8)) }
        guard let n = s.holdAfter else {
            client?.urlProtocolDidFinishLoading(self)
            return
        }
        nonisolated(unsafe) let me = self
        DispatchQueue.global().async {
            _ = gate.wait(timeout: .now() + 10)
            me.done.withLock {
                guard !me.stopped else { return }
                for p in s.pieces.dropFirst(n) { me.client?.urlProtocol(me, didLoad: Data(p.utf8)) }
                me.client?.urlProtocolDidFinishLoading(me)
            }
        }
    }

    override func stopLoading() {
        done.withLock { stopped = true }
        Self.lock.withLock { Self.stops += 1 }
        Self.letGo()
    }
}

/// A stream as the home writes it.
enum AIStream {
    static let v1 = "00000000-0000-4000-8000-000000000001"
    static let v2 = "00000000-0000-4000-8000-000000000002"

    static let queue = "event: queue\ndata: {\"position\":2}\n\n"
    static let sources = """
    event: sources
    data: {"q":"ما أنواع الصبر؟","passages":[{"n":1,"video_id":"\(v1)","title":"الصبر","library_name":"دروس","poster_url":"/api/videos/\(v1)/poster","segment_id":11,"start_sec":1622.5,"end_sec":1630,"text":"الصبر ثلاثة أنواع"},{"n":2,"video_id":"\(v2)","title":"Patience","library_name":"Lectures","poster_url":"","segment_id":12,"start_sec":90,"end_sec":98,"text":"patience on obedience"}]}


    """
    static let first = """
    event: text
    data: {"text":"الصبر ثلاثة أنواع.","citations":[{"n":1,"video_id":"\(v1)","start_sec":1622.5,"end_sec":1630,"quote":"الصبر ثلاثة أنواع"}]}


    """
    static let second = """
    event: text
    data: {"text":"Patience on obedience is one.","citations":[{"n":2,"video_id":"\(v2)","start_sec":90,"end_sec":98,"quote":"patience on obedience"},{"n":1,"video_id":"\(v1)","start_sec":1622.5,"end_sec":1630,"quote":"x"}]}


    """
    static let done = "event: done\ndata: {\"answered\":true,\"sentences\":2,\"dropped\":0,\"first_token_ms\":300,\"took_ms\":900}\n\n"
    static let whole = queue + ": ping\n\n" + sources + first + second + done
}

@MainActor @Suite(.serialized) struct AIMode {
    init() { AIStub.reset() }

    // MARK: the stream, however it is cut

    /// The same events whether the stream comes whole, byte by byte, or cut in two anywhere
    /// (inside an Arabic letter's bytes too).
    @Test func eventsAreTheSameWhereverTheStreamIsCut() {
        let bytes = Array(AIStream.whole.utf8)
        var p = SSEParser()
        let whole = p.feed(bytes)
        #expect(whole.map(\.event) == ["queue", "sources", "text", "text", "done"])
        #expect(whole[1].data.contains("الصبر ثلاثة أنواع"))
        for cut in 0...bytes.count {
            var q = SSEParser()
            let got = q.feed(bytes[..<cut]) + q.feed(bytes[cut...])
            #expect(got == whole, "cut at \(cut)")
        }
        var b = SSEParser()
        #expect(bytes.compactMap { b.feed($0) } == whole)
    }

    /// CR LF and CR end lines as LF does; data lines join with "\n"; a comment and other
    /// fields are skipped; an event the stream ends before its blank line is dropped.
    @Test func linesEndAnyWayTheStandardAllows() {
        var p = SSEParser()
        let got = p.feed(Array("event: a\r\ndata: one\r\ndata:two\r\nid: 7\r\n\r\n: ping\r\revent: b\rdata: x\r\r".utf8))
        #expect(got == [SSEMessage(event: "a", data: "one\ntwo"), SSEMessage(event: "b", data: "x")])
        #expect(p.feed(Array("data: plain\n\nevent: cut\ndata: {".utf8)) == [SSEMessage(event: "message", data: "plain")])
        p.finish()
        #expect(p.feed(Array("\n".utf8)).isEmpty, "the cut event is gone")
    }

    // MARK: each event

    @Test func eachEventIsRead() throws {
        var p = SSEParser()
        let events = p.feed(Array(AIStream.whole.utf8)).compactMap(AIEvent.init)
        #expect(events.count == 5)
        #expect(events[0] == .queue(position: 2))
        guard case let .sources(q, passages) = events[1] else { Issue.record("sources"); return }
        #expect(q == "ما أنواع الصبر؟")
        #expect(passages.map(\.n) == [1, 2])
        #expect(passages[0] == AIPassage(n: 1, videoID: AIStream.v1, title: "الصبر", libraryName: "دروس",
                                         posterURL: "/api/videos/\(AIStream.v1)/poster", startSec: 1622.5, endSec: 1630,
                                         text: "الصبر ثلاثة أنواع"))
        #expect(passages[1].posterURL == nil, "a blank poster reads as none")
        guard case let .text(s) = events[3] else { Issue.record("text"); return }
        #expect(s.text == "Patience on obedience is one.")
        #expect(s.citations.map(\.n) == [2, 1] && s.citations[0].startSec == 90 && s.citations[0].videoID == AIStream.v2)
        #expect(events[4] == .done(AIDone(answered: true)))
        #expect(AIEvent(SSEMessage(event: "error", data: #"{"code":"model-failed"}"#)) == .error(code: "model-failed"))
        #expect(AIEvent(SSEMessage(event: "done", data: #"{"answered":false,"reason":"not-in-passages","sentences":0}"#))
            == .done(AIDone(answered: false, reason: "not-in-passages")))
    }

    /// Odd shapes: numbers as texts, a passage without its video or number left out, a
    /// citation without its moment kept, an unknown event and a broken `text` skipped, a
    /// broken `done` still the end.
    @Test func oddShapesAreReadTolerantly() {
        func ev(_ e: String, _ d: String) -> AIEvent? { AIEvent(SSEMessage(event: e, data: d)) }
        #expect(ev("queue", #"{"position":"3"}"#) == .queue(position: 3))
        #expect(ev("queue", #"{"position":true}"#) == nil)
        #expect(ev("sources", #"{"passages":[{"n":"1","video_id":"v","start_sec":"12"},{"n":2},{"video_id":"w"},7,{"n":3,"video_id":"x","start_sec":5,"title":9}]}"#)
            == .sources(question: "", passages: [AIPassage(n: 1, videoID: "v", title: "", startSec: 12, text: ""),
                                                 AIPassage(n: 3, videoID: "x", title: "", startSec: 5, text: "")]))
        #expect(ev("text", #"{"text":"A.","citations":[{"n":4},{"x":1}]}"#) == .text(AISentence(text: "A.", citations: [AICitation(n: 4)])))
        #expect(ev("text", #"{"citations":[]}"#) == nil)
        #expect(ev("text", "not json") == nil)
        #expect(ev("summary", "{}") == nil)
        #expect(ev("done", "broken") == .done(AIDone(answered: nil)))
        #expect(ev("error", "{}") == .error(code: nil))
    }

    // MARK: the client

    @Test func theClientSendsTheQuestionAndHistoryAndReadsTheEventsInOrder() async throws {
        AIStub.reset(.init(pieces: [AIStream.whole]))
        let got = Collected()
        try await AIStub.client().askAI("ما أنواع الصبر؟", history: [AIHistoryTurn(q: "ما الصبر؟", answer: "حبس النفس.")]) {
            await got.add($0)
        }
        let events = await got.events
        #expect(events.count == 5 && events.first == .queue(position: 2) && events.last == .done(AIDone(answered: true)))
        let body = try #require(AIStub.asked.first)
        #expect(body["q"] as? String == "ما أنواع الصبر؟")
        #expect((body["history"] as? [[String: String]]) == [["q": "ما الصبر؟", "answer": "حبس النفس."]])
        #expect(AIStub.accept.first == "text/event-stream")
    }

    /// Each code the home refuses with, as the tab tells it.
    @Test func theCodesAreRefusals() async {
        func refusal(_ status: Int, _ body: String, headers: [String: String] = [:]) async -> AIModeModel.Refusal? {
            AIStub.reset(.init(status: status, headers: headers, pieces: [body]))
            do {
                try await AIStub.client().askAI("q") { _ in }
                return nil
            } catch {
                return AIModeModel.refusal(error)
            }
        }
        let unavailable = #"{"type":"https://maktaba.tv/problems/ai-unavailable","title":"no language model","status":409,"code":"ai-unavailable","help":{"settings":"/settings/rules?section=tagcheck&q=url","help":"/help/search#ai-mode"}}"#
        #expect(await refusal(409, unavailable) == .unavailable(settings: "/settings/rules?section=tagcheck&q=url", help: "/help/search#ai-mode"))
        #expect(await refusal(409, #"{"type":"https://maktaba.tv/problems/ai-off","status":409,"code":"ai-off"}"#) == .off)
        #expect(await refusal(429, #"{"type":"https://maktaba.tv/problems/ai-busy","status":429,"code":"ai-busy","position":9}"#,
                              headers: ["Retry-After": "30"]) == .busy(position: 9, retryAfter: 30))
        #expect(await refusal(404, #"{"title":"not found","status":404}"#) == .missing, "a home before 49.12")
        #expect(await refusal(409, #"{"code":"conflict"}"#) == nil, "another 409 is a failure")
        #expect(await refusal(500, "oops") == nil)
    }

    // MARK: the tab's model

    @Test func anAnswerShowsItsSourcesThenItsSentences() async {
        AIStub.reset(.init(pieces: [AIStream.whole]))
        let m = AIModeModel(client: AIStub.client()) {}
        await m.ask("  ما أنواع الصبر؟ ")?.value
        let t = m.turns[0]
        #expect(m.question == "ما أنواع الصبر؟")
        #expect(t.state == .answered && !m.running)
        #expect(t.passages.map(\.n) == [1, 2])
        #expect(t.sentences.map(\.text) == ["الصبر ثلاثة أنواع.", "Patience on obedience is one."])
        let c = t.sentences[1].citations[0]
        #expect(t.target(c)! == (AIStream.v2, 90))
        // a citation without its own video and moment plays its passage's
        #expect(t.target(AICitation(n: 1))! == (AIStream.v1, 1622.5))
        #expect(t.target(AICitation(n: 7)) == nil)
    }

    /// The sources show at once, and the place in line before them.
    @Test func theSourcesShowBeforeTheSentences() async throws {
        AIStub.reset(.init(pieces: [AIStream.queue, AIStream.sources, AIStream.first, AIStream.done], holdAfter: 1))
        let m = AIModeModel(client: AIStub.client()) {}
        let task = m.ask("q")
        try await until { m.turns.first?.state == .waiting(position: 2) }
        AIStub.letGo()
        await task?.value
        #expect(m.turns[0].state == .answered && m.turns[0].passages.count == 2 && m.turns[0].sentences.count == 1)

        AIStub.reset(.init(pieces: [AIStream.sources, AIStream.first, AIStream.done], holdAfter: 1))
        let again = m.ask("q")
        try await until { m.turns.first?.passages.count == 2 }
        #expect(m.turns[0].state == .writing && m.turns[0].sentences.isEmpty)
        AIStub.letGo()
        await again?.value
        #expect(m.turns[0].sentences.count == 1)
    }

    @Test func passagesThatDoNotAnswerStayShown() async {
        AIStub.reset(.init(pieces: [AIStream.sources, #"event: done\#ndata: {"answered":false,"reason":"not-in-passages"}\#n\#n"#]))
        let m = AIModeModel(client: AIStub.client()) {}
        await m.ask("q")?.value
        #expect(m.turns[0].state == .notAnswered(reason: "not-in-passages"))
        #expect(m.turns[0].passages.count == 2)
        #expect(AIWords.ending(m.turns[0].state, Locale(identifier: "en")) == "The passages found do not answer this question. Here they are.")
    }

    /// An `error` event, or a stream that ends without `done`: the sentences shown stand.
    @Test func anAnswerThatStopsKeepsWhatWasShown() async {
        AIStub.reset(.init(pieces: [AIStream.sources, AIStream.first, #"event: error\#ndata: {"code":"model-failed"}\#n\#n"#]))
        let m = AIModeModel(client: AIStub.client()) {}
        await m.ask("q")?.value
        #expect(m.turns[0].state == .stopped(code: "model-failed"))
        #expect(m.turns[0].sentences.count == 1 && m.turns[0].passages.count == 2)

        AIStub.reset(.init(pieces: [AIStream.sources, AIStream.first]))
        await m.ask("q")?.value
        #expect(m.turns[0].state == .stopped(code: nil) && m.turns[0].sentences.count == 1)
    }

    @Test func aRefusalIsTheTurnsEnd() async {
        AIStub.reset(.init(status: 409, pieces: [#"{"code":"ai-off","status":409}"#]))
        let m = AIModeModel(client: AIStub.client()) {}
        await m.ask("q")?.value
        #expect(m.turns[0].state == .refused(.off))
        AIStub.reset(.init(status: 500, pieces: ["{}"]))
        await m.ask("q")?.value
        guard case let .refused(.failed(f)) = m.turns[0].state else { Issue.record("failed: \(m.turns[0].state)"); return }
        #expect(f.status == 500)
    }

    /// A follow-up sends the earlier questions with their answers (the last three answered);
    /// Ask again sends its own question with the same conversation, in its place.
    @Test func aFollowUpCarriesTheConversation() async throws {
        AIStub.reset(.init(pieces: [AIStream.whole]))
        let m = AIModeModel(client: AIStub.client()) {}
        await m.ask("one")?.value
        await m.followUp("two")?.value
        await m.followUp("three")?.value
        AIStub.reset(.init(pieces: [AIStream.sources, #"event: done\#ndata: {"answered":false,"reason":"not-in-passages"}\#n\#n"#]))
        await m.followUp("four")?.value
        AIStub.reset(.init(pieces: [AIStream.whole]))
        await m.followUp("five")?.value
        #expect(m.turns.map(\.question) == ["one", "two", "three", "four", "five"])
        let history = try #require(AIStub.asked.last?["history"] as? [[String: String]])
        // "four" had no answer: the three answered before it
        #expect(history.map { $0["q"] } == ["one", "two", "three"])
        #expect(history[0]["answer"] == "الصبر ثلاثة أنواع. Patience on obedience is one.")

        await m.askAgain()?.value
        #expect(m.turns.map(\.question) == ["one", "two", "three", "four", "five"])
        #expect((AIStub.asked.last?["history"] as? [[String: String]])?.map { $0["q"] } == ["one", "two", "three"])
        #expect(AIStub.asked.last?["q"] as? String == "five")

        await m.ask("new")?.value
        #expect(m.turns.map(\.question) == ["new"])
        #expect(AIStub.asked.last?["history"] == nil, "a new question carries no conversation")
    }

    /// Leaving the tab closes the stream: the turn stops, what came stays, and the request is
    /// stopped. Asking again closes the one before too.
    @Test func cancelClosesTheStream() async throws {
        AIStub.reset(.init(pieces: [AIStream.sources, AIStream.first, AIStream.done], holdAfter: 1))
        let m = AIModeModel(client: AIStub.client()) {}
        let task = m.ask("q")
        try await until { m.turns.first?.passages.count == 2 }
        m.cancel()
        #expect(m.turns[0].state == .stopped(code: AIModeModel.left) && !m.running)
        await task?.value
        try await until { AIStub.stopCount == 1 }
        #expect(m.turns[0].sentences.isEmpty, "nothing after the cancel")
        #expect(AIWords.ending(m.turns[0].state, Locale(identifier: "ar")) == "توقف الجواب.")

        AIStub.reset(.init(pieces: [AIStream.sources, AIStream.first, AIStream.done], holdAfter: 1))
        _ = m.ask("first")
        try await until { m.turns.first?.passages.count == 2 }
        AIStub.reset(.init(pieces: [AIStream.whole]))
        await m.ask("second")?.value
        #expect(m.turns.map(\.question) == ["second"] && m.turns[0].state == .answered)
    }

    // MARK: words

    @Test func theWordsAreInEnglishAndArabic() {
        let en = Locale(identifier: "en"), ar = Locale(identifier: "ar"), arSA = Locale(identifier: "ar_SA")
        func plain(_ s: String) -> String {
            s.replacingOccurrences(of: "\u{2068}", with: "").replacingOccurrences(of: "\u{2069}", with: "")
        }
        #expect(AIWords.aiMode(en) == "AI mode" && AIWords.aiMode(ar) == "وضع الذكاء الاصطناعي")
        #expect(AIWords.results(ar) == "النتائج")
        #expect(plain(AIWords.ask("ما الصبر؟", ar)) == "اسأل «ما الصبر؟»")
        #expect(AIWords.waiting(1, en) == "Waiting: your question is next.")
        #expect(AIWords.waiting(2, en) == "Waiting in line: 1 question before yours.")
        #expect(AIWords.waiting(4, en) == "Waiting in line: 3 questions before yours.")
        #expect(plain(AIWords.waiting(4, arSA)) == "في الانتظار: عدد الأسئلة قبل سؤالك: ٣.")
        #expect(plain(AIWords.citation(2, title: "Patience", at: AIWords.clock(1622.5, en), en)) == "Source 2: Patience, at 27:02")
        #expect(plain(AIWords.citation(2, title: "الصبر", at: AIWords.clock(1622.5, arSA), arSA)) == "المصدر ٢: الصبر، عند ٢٧:٠٢")
        #expect(AIWords.ending(.refused(.unavailable(settings: nil, help: nil)), ar)
            == "يحتاج وضع الذكاء الاصطناعي إلى نموذج اللغة في الجهاز الرئيسي، وهو غير مُعدّ.")
        #expect(AIWords.ending(.refused(.missing), en) == "This home does not have AI mode yet. It comes with an update of the home.")
        #expect(AIWords.ending(.refused(.busy(position: 9, retryAfter: 30)), en) == "AI mode is busy with other questions. Ask again in a moment.")
        #expect(AIWords.ending(.notAnswered(reason: "no-passages"), ar) == "لا شيء في مكتباتك يجيب عن هذا السؤال.")
        #expect(AIWords.ending(.answered, en) == nil)
        let p = AIPassage(n: 1, videoID: "v", title: "Patience", libraryName: "Lectures", startSec: 1622, text: "Of three kinds.")
        #expect(plain(AIWords.source(p, en)) == "Source 1: Patience, at 27:02. Of three kinds. Lectures")
    }

    /// Waits (up to 5 s) until `check` holds.
    private func until(_ check: () -> Bool) async throws {
        let end = Date().addingTimeInterval(5)
        while !check(), Date() < end { try await Task.sleep(for: .milliseconds(10)) }
        #expect(check())
    }
}

actor Collected {
    var events: [AIEvent] = []
    func add(_ e: AIEvent) { events.append(e) }
}
