import Foundation
import Observation

// Story 49.14 (Epic 49 §5.8): AI mode, Search's second tab, in the Apple apps (the Player and
// the TV share all of this). `POST /api/search/ai` (story 49.12) answers a question from the
// passages of the person's libraries as Server-Sent Events over the POST: `queue` (the place
// in line), `sources` (the numbered passages), one `text` per sentence with its citations,
// then `done` or `error`. The guide for clients is docs/developers/contributing/ai-mode.md.
// Every field is read tolerantly, as story 49.11 read an older home's search: a field of the
// wrong shape reads as absent, and an event this app does not know is skipped.

// MARK: Server-Sent Events

/// One event of a Server-Sent Events stream: its name (`message` when it names none) and its
/// data lines joined with "\n".
public struct SSEMessage: Equatable, Sendable {
    public let event: String
    public let data: String

    public init(event: String, data: String) {
        self.event = event
        self.data = data
    }
}

/// Reads a Server-Sent Events stream byte by byte, however it is cut into pieces: a line ends
/// at LF, CR or CR LF, and a blank line ends an event. A line is decoded once whole, so a
/// UTF-8 character cut between two pieces is read whole. Comments (`: ping`) and fields other
/// than `event` and `data` are skipped. (`URLSession.AsyncBytes.lines` drops the blank lines
/// that end the events, so it cannot be used.)
public struct SSEParser: Sendable {
    private var line: [UInt8] = []
    private var lastWasCR = false
    private var event = ""
    private var data: [String] = []
    private var hasData = false

    public init() {}

    /// Reads one byte; an event when that byte ended one.
    public mutating func feed(_ byte: UInt8) -> SSEMessage? {
        switch byte {
        case 0x0A where lastWasCR:
            // the LF of a CR LF: the line already ended at the CR
            lastWasCR = false
            return nil
        case 0x0A, 0x0D:
            lastWasCR = byte == 0x0D
            return endLine()
        default:
            lastWasCR = false
            line.append(byte)
            return nil
        }
    }

    /// Reads a piece of the stream; the events it ended, in order.
    public mutating func feed(_ bytes: some Sequence<UInt8>) -> [SSEMessage] {
        bytes.compactMap { feed($0) }
    }

    /// The stream ended: an event not ended by a blank line is dropped, as the standard says.
    public mutating func finish() {
        line = []
        event = ""
        data = []
        hasData = false
    }

    private mutating func endLine() -> SSEMessage? {
        defer { line = [] }
        guard !line.isEmpty else {
            // a blank line: the event so far, when it has data
            defer { event = ""; data = []; hasData = false }
            guard hasData else { return nil }
            return SSEMessage(event: event.isEmpty ? "message" : event, data: data.joined(separator: "\n"))
        }
        let text = String(decoding: line, as: UTF8.self)
        if text.hasPrefix(":") { return nil }
        let field: Substring, value: Substring
        if let colon = text.firstIndex(of: ":") {
            field = text[..<colon]
            var v = text[text.index(after: colon)...]
            if v.hasPrefix(" ") { v = v.dropFirst() }
            value = v
        } else {
            field = Substring(text)
            value = ""
        }
        switch field {
        case "event": event = String(value)
        case "data":
            data.append(String(value))
            hasData = true
        default: break
        }
        return nil
    }
}

// MARK: The answer's events

/// One passage the model reads (`sources`), numbered by `n`: what a citation names.
public struct AIPassage: Hashable, Sendable, Identifiable {
    public let n: Int
    public let videoID: String
    public let title: String
    public let libraryName: String?
    public let posterURL: String?
    public let startSec: Double
    public let endSec: Double?
    /// The passage's own words.
    public let text: String

    public var id: Int { n }

    public init(n: Int, videoID: String, title: String, libraryName: String? = nil, posterURL: String? = nil,
                startSec: Double, endSec: Double? = nil, text: String) {
        self.n = n
        self.videoID = videoID
        self.title = title
        self.libraryName = libraryName
        self.posterURL = posterURL
        self.startSec = startSec
        self.endSec = endSec
        self.text = text
    }
}

/// Where a sentence comes from: the passage `n`, its video and moment, and the quoted words.
public struct AICitation: Hashable, Sendable {
    public let n: Int
    /// nil when the home did not say: the passage `n`'s video then.
    public let videoID: String?
    public let startSec: Double?
    public let quote: String?

    public init(n: Int, videoID: String? = nil, startSec: Double? = nil, quote: String? = nil) {
        self.n = n
        self.videoID = videoID
        self.startSec = startSec
        self.quote = quote
    }
}

/// One sentence of the answer with the passages it cites.
public struct AISentence: Hashable, Sendable {
    public let text: String
    public let citations: [AICitation]

    public init(text: String, citations: [AICitation]) {
        self.text = text
        self.citations = citations
    }
}

/// The last event: whether the passages answered the question, and why not.
public struct AIDone: Hashable, Sendable {
    /// nil when the home did not say: answered when a sentence came.
    public let answered: Bool?
    /// `no-passages`, `not-in-passages` or `timeout`.
    public let reason: String?

    public init(answered: Bool?, reason: String? = nil) {
        self.answered = answered
        self.reason = reason
    }
}

/// An event of `POST /api/search/ai`.
public enum AIEvent: Hashable, Sendable {
    case queue(position: Int)
    case sources(question: String, passages: [AIPassage])
    case text(AISentence)
    case done(AIDone)
    case error(code: String?)

    /// The event `m` is, read tolerantly; nil for an event this app does not know, or a
    /// `queue` or `text` it cannot read.
    public init?(_ m: SSEMessage) {
        let json = (try? JSONSerialization.jsonObject(with: Data(m.data.utf8))) as? [String: Any] ?? [:]
        switch m.event {
        case "queue":
            guard let p = Self.int(json["position"]) else { return nil }
            self = .queue(position: p)
        case "sources":
            let list = json["passages"] as? [Any] ?? []
            self = .sources(question: json["q"] as? String ?? "",
                            passages: list.compactMap { ($0 as? [String: Any]).flatMap(Self.passage) })
        case "text":
            guard let text = json["text"] as? String, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
            let cites = (json["citations"] as? [Any] ?? []).compactMap { ($0 as? [String: Any]).flatMap(Self.citation) }
            self = .text(AISentence(text: text, citations: cites))
        case "done":
            self = .done(AIDone(answered: json["answered"] as? Bool, reason: Self.text(json["reason"])))
        case "error":
            self = .error(code: Self.text(json["code"]))
        default:
            return nil
        }
    }

    static func passage(_ o: [String: Any]) -> AIPassage? {
        guard let n = int(o["n"]), let video = text(o["video_id"]) else { return nil }
        return AIPassage(n: n, videoID: video, title: o["title"] as? String ?? "", libraryName: text(o["library_name"]),
                         posterURL: text(o["poster_url"]), startSec: double(o["start_sec"]) ?? 0,
                         endSec: double(o["end_sec"]), text: o["text"] as? String ?? "")
    }

    static func citation(_ o: [String: Any]) -> AICitation? {
        guard let n = int(o["n"]) else { return nil }
        return AICitation(n: n, videoID: text(o["video_id"]), startSec: double(o["start_sec"]), quote: text(o["quote"]))
    }

    /// A whole number, sent as a number (2, 2.0) or as its text ("2").
    static func int(_ v: Any?) -> Int? {
        if let s = v as? String { return Int(s.trimmingCharacters(in: .whitespaces)) }
        guard let d = double(v), d.isFinite, d == d.rounded() else { return nil }
        return Int(d)
    }

    static func double(_ v: Any?) -> Double? {
        // a JSON true or false reads as a number to NSNumber: not one here
        var d: Double?
        if let n = v as? NSNumber, CFGetTypeID(n) != CFBooleanGetTypeID() { d = n.doubleValue }
        if let s = v as? String { d = Double(s.trimmingCharacters(in: .whitespaces)) }
        // a number that is not finite (Double("inf"), "nan") reads as absent
        guard let d, d.isFinite else { return nil }
        return d
    }

    /// A text that is not blank.
    static func text(_ v: Any?) -> String? {
        guard let s = v as? String, !s.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        return s
    }
}

/// An earlier question and its answer, for a follow-up (`history`).
public struct AIHistoryTurn: Hashable, Sendable {
    public let q: String
    public let answer: String

    public init(q: String, answer: String) {
        self.q = q
        self.answer = answer
    }
}

// MARK: The client

extension APIClient {
    /// The longest an answer's stream stays open: it waits in line, and the home sends a
    /// `: ping` every 15 s meanwhile, so only a stream silent this long has gone.
    static let aiTimeout: TimeInterval = 60

    /// Asks `question` (with the conversation so far, oldest first) and hands each event to
    /// `onEvent` as it comes. Returns when the stream ends; cancelling the calling task closes
    /// it (the home then stops the model and gives the turn to the next question). A refusal
    /// (409 `ai-unavailable` or `ai-off`, 429 `ai-busy`, 404 from a home before 49.12) throws
    /// `APIError.status`, its problem kept; `AIModeModel.refusal` reads it.
    public func askAI(_ question: String, history: [AIHistoryTurn] = [],
                      onEvent: @escaping @Sendable (AIEvent) async -> Void) async throws {
        var body: [String: Any] = ["q": question]
        if !history.isEmpty { body["history"] = history.map { ["q": $0.q, "answer": $0.answer] } }
        var r = request("POST", "/api/search/ai", body: body)
        r.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        r.timeoutInterval = Self.aiTimeout
        try await streamAI(r, retried: false, onEvent: onEvent)
    }

    private func streamAI(_ original: URLRequest, retried: Bool,
                          onEvent: @escaping @Sendable (AIEvent) async -> Void) async throws {
        var r = original
        if let access = tokens.access { r.setValue("Bearer \(access)", forHTTPHeaderField: "Authorization") }
        let bytes: URLSession.AsyncBytes, response: URLResponse
        do {
            (bytes, response) = try await session.bytes(for: r, delegate: SameOriginRedirects.shared)
        } catch {
            if Log.isCancellation(error) || Task.isCancelled { throw CancellationError() }
            Log.network.error("\(Self.describe(r), privacy: .public): no answer (URLError \((error as? URLError)?.code.rawValue ?? 0, privacy: .public))")
            throw APIError.transport(error.localizedDescription)
        }
        let task = bytes.task
        let http = response as? HTTPURLResponse
        let code = http?.statusCode ?? 0
        if code == 401, !retried, tokens.refresh != nil {
            task.cancel()
            _ = try await refreshOnce()
            return try await streamAI(original, retried: true, onEvent: onEvent)
        }
        if code == 401 {
            task.cancel()
            Log.session.notice("\(Self.describe(r), privacy: .public): 401 after a refresh: signed out")
            tokens.clear()
            throw APIError.signedOut
        }
        try await withTaskCancellationHandler {
            guard (200..<300).contains(code) else {
                var d = Data()
                do {
                    for try await b in bytes {
                        d.append(b)
                        if d.count >= 4096 { break }
                    }
                } catch {
                    // the refusal's body cut short: what came is enough to read its code
                }
                task.cancel()
                let e = APIError.status(code, String(decoding: d, as: UTF8.self), retryAfter: http.flatMap(Self.retryAfter))
                Log.network.notice("\(Self.describe(r), privacy: .public) -> \(code, privacy: .public) code=\(e.problem?.code ?? "-", privacy: .public)")
                throw e
            }
            var parser = SSEParser()
            do {
                for try await b in bytes {
                    if let m = parser.feed(b), let e = AIEvent(m) { await onEvent(e) }
                }
            } catch {
                if Log.isCancellation(error) || Task.isCancelled { throw CancellationError() }
                Log.network.error("\(Self.describe(r), privacy: .public): the stream broke (URLError \((error as? URLError)?.code.rawValue ?? 0, privacy: .public))")
                throw APIError.transport(error.localizedDescription)
            }
            parser.finish()
            if Task.isCancelled { throw CancellationError() }
        } onCancel: {
            task.cancel()
        }
    }
}

// MARK: The tab's state

/// AI mode's state for one Search screen: the conversation (each question with its passages,
/// its sentences and how it ended) and the stream being read. One stream at a time: asking
/// again, a follow-up and `cancel` each close the one before. The screen calls `cancel` when
/// the person leaves the tab or Search.
@MainActor @Observable
public final class AIModeModel {
    /// Why the home gave no answer to write.
    public enum Refusal: Equatable, Sendable {
        /// 409 `ai-unavailable`: no language model. `settings` and `help` are paths of the
        /// home's web app: where an admin sets the model server, and the help page.
        case unavailable(settings: String?, help: String?)
        /// 409 `ai-off`: an admin turned AI mode off.
        case off
        /// 429 `ai-busy`: the line is full; ask again after `retryAfter` seconds.
        case busy(position: Int?, retryAfter: Int?)
        /// 404: a home before story 49.12 has no AI mode.
        case missing
        /// Any other failure before the answer began.
        case failed(HomeFailure)
    }

    /// Where a question is.
    public enum State: Equatable, Sendable {
        /// Sent; nothing back yet.
        case asking
        /// Waiting in line: 1 is next.
        case waiting(position: Int)
        /// The passages are shown; the sentences come.
        case writing
        /// Every sentence came.
        case answered
        /// The passages do not answer it (`no-passages`, `not-in-passages`) or the answer took
        /// too long (`timeout`); the passages and any sentences stay shown.
        case notAnswered(reason: String?)
        /// The answer stopped: an `error` event (its code), the stream broke, or the person
        /// left (`AIModeModel.left`). What was shown stays.
        case stopped(code: String?)
        case refused(Refusal)

        public var isRunning: Bool {
            switch self {
            case .asking, .waiting, .writing: true
            default: false
            }
        }
    }

    /// One question and its answer.
    public struct Turn: Identifiable, Equatable, Sendable {
        public let id: Int
        public let question: String
        /// The conversation before it, as it was sent (Ask again sends the same).
        let history: [AIHistoryTurn]
        public internal(set) var passages: [AIPassage] = []
        public internal(set) var sentences: [AISentence] = []
        public internal(set) var state: State = .asking

        /// The answer's sentences as one text (a follow-up's `history`).
        public var answer: String { sentences.map(\.text).joined(separator: " ") }

        public func passage(_ n: Int) -> AIPassage? { passages.first { $0.n == n } }

        /// Where a citation plays: its own video and moment, else its passage's.
        public func target(_ c: AICitation) -> (videoID: String, startSec: Double)? {
            let p = passage(c.n)
            guard let video = c.videoID ?? p?.videoID else { return nil }
            return (video, c.startSec ?? p?.startSec ?? 0)
        }
    }

    /// The code of a turn stopped because the person left the tab, asked again or left Search.
    public static let left = "left"
    /// How many earlier turns a follow-up sends (the home reads the last three).
    static let historyTurns = 3

    /// The conversation, oldest first.
    public private(set) var turns: [Turn] = []
    /// A question is being answered.
    public var running: Bool { turns.last?.state.isRunning ?? false }
    /// The question the conversation began with.
    public var question: String? { turns.first?.question }

    private let client: APIClient
    private let signedOut: @MainActor () -> Void
    @ObservationIgnored private var task: Task<Void, Never>?
    @ObservationIgnored private var nextID = 0

    public init(client: APIClient, signedOut: @escaping @MainActor () -> Void) {
        self.client = client
        self.signedOut = signedOut
    }

    /// Starts a new conversation with `q`; the one before goes. Blank asks nothing.
    @discardableResult
    public func ask(_ q: String) -> Task<Void, Never>? {
        let q = SearchModel.sendable(q)
        guard !q.isEmpty else { return nil }
        cancel()
        turns = []
        return start(q, history: [])
    }

    /// Asks `q` after the conversation so far: the earlier questions and their answers go
    /// with it. With no conversation, a new one.
    @discardableResult
    public func followUp(_ q: String) -> Task<Void, Never>? {
        let q = SearchModel.sendable(q)
        guard !q.isEmpty else { return nil }
        cancel()
        return start(q, history: Self.history(turns))
    }

    /// Asks the last question again, with the same conversation before it, in its place.
    @discardableResult
    public func askAgain() -> Task<Void, Never>? {
        guard let last = turns.last else { return nil }
        cancel()
        turns.removeLast()
        return start(last.question, history: last.history)
    }

    /// Closes the stream: the question being answered stops, what was shown stays.
    public func cancel() {
        task?.cancel()
        task = nil
        if let i = turns.indices.last, turns[i].state.isRunning { turns[i].state = .stopped(code: Self.left) }
    }

    /// Forgets the conversation (a new question).
    public func clear() {
        cancel()
        turns = []
    }

    /// The turns a follow-up sends: the last three with both a question and an answer.
    static func history(_ turns: [Turn]) -> [AIHistoryTurn] {
        Array(turns.filter { !$0.sentences.isEmpty }.suffix(historyTurns).map { AIHistoryTurn(q: $0.question, answer: $0.answer) })
    }

    private func start(_ q: String, history: [AIHistoryTurn]) -> Task<Void, Never> {
        nextID += 1
        let id = nextID
        turns.append(Turn(id: id, question: q, history: history))
        let client = client
        let t = Task { [weak self] in
            do {
                try await client.askAI(q, history: history) { [weak self] event in
                    await self?.apply(event, to: id)
                }
                self?.ended(id)
            } catch {
                self?.failed(id, error)
            }
        }
        task = t
        return t
    }

    private func index(_ id: Int) -> Int? {
        guard let i = turns.firstIndex(where: { $0.id == id }), turns[i].state.isRunning else { return nil }
        return i
    }

    private func apply(_ e: AIEvent, to id: Int) {
        guard let i = index(id) else { return }
        switch e {
        case let .queue(position):
            if turns[i].passages.isEmpty, turns[i].sentences.isEmpty { turns[i].state = .waiting(position: max(position, 1)) }
        case let .sources(_, passages):
            turns[i].passages = passages
            turns[i].state = .writing
        case let .text(s):
            turns[i].sentences.append(s)
            turns[i].state = .writing
        case let .done(d):
            let answered = d.answered ?? !turns[i].sentences.isEmpty
            turns[i].state = answered && d.reason == nil ? .answered : .notAnswered(reason: d.reason)
        case let .error(code):
            turns[i].state = .stopped(code: code)
        }
    }

    /// The stream ended without `done`: the answer stopped.
    private func ended(_ id: Int) {
        guard let i = index(id) else { return }
        turns[i].state = .stopped(code: nil)
    }

    private func failed(_ id: Int, _ error: Error) {
        if case APIError.signedOut = error { return signedOut() }
        guard let i = index(id) else { return }
        if Log.isCancellation(error) {
            turns[i].state = .stopped(code: Self.left)
            return
        }
        if let r = Self.refusal(error) {
            turns[i].state = .refused(r)
            return
        }
        Log.failure("ai mode", error)
        // the answer had begun: what came stands
        if !turns[i].passages.isEmpty || !turns[i].sentences.isEmpty {
            turns[i].state = .stopped(code: nil)
        } else {
            turns[i].state = .refused(.failed(HomeFailure(error)))
        }
    }

    /// What a refusal of the home means, when `error` is one.
    nonisolated static func refusal(_ error: Error) -> Refusal? {
        guard case let APIError.status(code, body, retryAfter) = error else { return nil }
        let json = (try? JSONSerialization.jsonObject(with: Data(body.utf8))) as? [String: Any]
        let p = Problem.parse(body)
        let what = p?.code ?? p?.slug
        switch code {
        case 409 where what == "ai-off":
            return .off
        case 409 where what == "ai-unavailable":
            let help = json?["help"] as? [String: Any]
            return .unavailable(settings: AIEvent.text(help?["settings"]), help: AIEvent.text(help?["help"]))
        case 429 where what == "ai-busy" || what == nil:
            return .busy(position: AIEvent.int(json?["position"]), retryAfter: retryAfter)
        case 404, 405:
            // a home before 49.12: no such route (its catch-all may answer 405 to a POST)
            return .missing
        default:
            return nil
        }
    }
}

// MARK: Words

/// AI mode's words, shared by the Player and the TV (MaktabaKit's catalog), English and Arabic.
public enum AIWords {
    public static func results(_ l: Locale = AppLanguage.locale) -> String { kit("Results", l) }
    public static func aiMode(_ l: Locale = AppLanguage.locale) -> String { kit("AI mode", l) }
    public static func intro(_ l: Locale = AppLanguage.locale) -> String {
        kit("Ask a question: the answer comes from your libraries' passages, each sentence with its source.", l)
    }
    /// "Ask “…”": the TV's button that asks what was typed.
    public static func ask(_ q: String, _ l: Locale = AppLanguage.locale) -> String { kit("Ask “\(q)”", l) }
    /// The TV's button that asks what was typed after the conversation so far.
    public static func askFollowUp(_ q: String, _ l: Locale = AppLanguage.locale) -> String { kit("Ask as a follow-up: “\(q)”", l) }
    public static func followUpPrompt(_ l: Locale = AppLanguage.locale) -> String { kit("Ask a follow-up", l) }
    public static func send(_ l: Locale = AppLanguage.locale) -> String { kit("Ask", l) }
    public static func newQuestion(_ l: Locale = AppLanguage.locale) -> String { kit("New question", l) }
    public static func sources(_ l: Locale = AppLanguage.locale) -> String { kit("Sources", l) }
    /// "Show all 12 sources".
    public static func allSources(_ n: Int, _ l: Locale = AppLanguage.locale) -> String { kit("Show all \(n) sources", l) }
    public static func answer(_ l: Locale = AppLanguage.locale) -> String { kit("Answer", l) }
    public static func asking(_ l: Locale = AppLanguage.locale) -> String { kit("Finding the passages…", l) }
    public static func writing(_ l: Locale = AppLanguage.locale) -> String { kit("Writing the answer…", l) }
    /// "Waiting in line: 2 questions before yours" (1: next).
    public static func waiting(_ position: Int, _ l: Locale = AppLanguage.locale) -> String {
        position <= 1 ? kit("Waiting: your question is next.", l)
            : kit("Waiting in line: \(position - 1) questions before yours.", l)
    }
    public static func notInPassages(_ l: Locale = AppLanguage.locale) -> String {
        kit("The passages found do not answer this question. Here they are.", l)
    }
    public static func noPassages(_ l: Locale = AppLanguage.locale) -> String {
        kit("Nothing in your libraries answers this question.", l)
    }
    public static func timedOut(_ l: Locale = AppLanguage.locale) -> String {
        kit("The answer took too long and stopped here.", l)
    }
    public static func stopped(_ l: Locale = AppLanguage.locale) -> String { kit("The answer stopped.", l) }
    public static func askAgain(_ l: Locale = AppLanguage.locale) -> String { kit("Ask again", l) }
    public static func unavailable(_ l: Locale = AppLanguage.locale) -> String {
        kit("AI mode needs the home's language model, and it is not set up.", l)
    }
    public static func unavailableAdmin(_ l: Locale = AppLanguage.locale) -> String {
        kit("An admin sets the model server in the home's web app: Settings, Rules, Tag check.", l)
    }
    public static func unavailableViewer(_ l: Locale = AppLanguage.locale) -> String {
        kit("Ask the home's admin to set it up.", l)
    }
    public static func openSettings(_ l: Locale = AppLanguage.locale) -> String { kit("Open the settings", l) }
    public static func help(_ l: Locale = AppLanguage.locale) -> String { kit("Help: AI mode", l) }
    /// The help page's address, for a screen with no browser (the TV).
    public static func helpAt(_ address: String, _ l: Locale = AppLanguage.locale) -> String {
        kit("Help: \(address)", l)
    }
    public static func off(_ l: Locale = AppLanguage.locale) -> String { kit("An admin turned AI mode off for this home.", l) }
    public static func busy(_ l: Locale = AppLanguage.locale) -> String {
        kit("AI mode is busy with other questions. Ask again in a moment.", l)
    }
    public static func missing(_ l: Locale = AppLanguage.locale) -> String {
        kit("This home does not have AI mode yet. It comes with an update of the home.", l)
    }
    /// A citation's mark: its passage's number in the app's digits.
    public static func mark(_ n: Int, _ l: Locale = AppLanguage.locale) -> String { SearchWords.number(n, l) }
    /// What VoiceOver reads for a citation: "Source 2: Patience, at 27:02".
    public static func citation(_ n: Int, title: String, at time: String, _ l: Locale = AppLanguage.locale) -> String {
        kit("Source \(SearchWords.number(n, l)): \(title), at \(time)", l)
    }
    /// What VoiceOver reads for a source's card: every word it shows. "Source 1: Patience, at
    /// 27:02. Patience is of three kinds. Lectures"
    public static func source(_ p: AIPassage, _ l: Locale = AppLanguage.locale) -> String {
        let parts = [citation(p.n, title: p.title, at: clock(p.startSec, l), l), p.text, p.libraryName ?? ""]
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty }
        // a part that ends its own sentence (".", "?", "؟") takes no second stop
        return parts.dropFirst().reduce(parts.first ?? "") { out, part in
            let ended = out.last.map { ".?!؟۔".contains($0) } ?? true
            return out + (ended ? " " : ". ") + part
        }
    }

    /// A passage's time as a clock (27:02, 1:02:03) in the app's digits.
    public static func clock(_ seconds: Double, _ l: Locale = AppLanguage.locale) -> String {
        // a time no video has (a broken home's 1e308) is clamped: Duration traps past Int128
        let s = min(max(0, seconds.isFinite ? seconds.rounded(.down) : 0), 9_999_999)
        return Duration.seconds(s).formatted(.time(pattern: s >= 3600 ? .hourMinuteSecond : .minuteSecond).locale(AppLanguage.westernDigits(l)))
    }

    /// What a turn that is not running says (nil: nothing to add under it).
    public static func ending(_ s: AIModeModel.State, _ l: Locale = AppLanguage.locale) -> String? {
        switch s {
        case let .notAnswered(reason):
            switch reason {
            case "no-passages": noPassages(l)
            case "timeout": timedOut(l)
            default: notInPassages(l)
            }
        case .stopped: stopped(l)
        case let .refused(r):
            switch r {
            case .unavailable: unavailable(l)
            case .off: off(l)
            case .busy: busy(l)
            case .missing: missing(l)
            case let .failed(f): f.message(video: false)
            }
        default: nil
        }
    }
}
