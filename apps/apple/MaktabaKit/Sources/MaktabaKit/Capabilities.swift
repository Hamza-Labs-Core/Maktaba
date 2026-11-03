import Foundation
import Observation
import SwiftUI

/// What the home says is on (`GET /api/capabilities`, Epic 51 story 51.09), read by every app
/// to hide a feature that is off from a viewer and to explain it to an admin (story 51.11).
///
/// The rules: until the first answer every feature is on; a feature the answer does not name
/// (an older home) is on; a feature this app does not know (a newer home) is ignored. "Admin"
/// is an answer with an `admin` block: a cloud-linked play sign-in never gets one, so it is a
/// viewer even on an admin's account.
public struct Capabilities: Equatable, Sendable {
    /// The features the home answers for (the catalog's nine).
    public enum Feature: String, CaseIterable, Sendable {
        case liveTV = "live_tv"
        case transcripts
        case subtitles
        case semanticSearch = "semantic_search"
        case chapters
        case youtube
        case plex
        case remoteAccess = "remote_access"
        case recommendations
    }

    /// What turns one feature off, as the admin block says.
    public struct Off: Equatable, Sendable, Decodable {
        /// The components that turn it off (`livetv.slots`, `search.semantic` …).
        public let components: [String]
        /// The first one's reason: `switched_off`, `held`, `dependency_off`, `licence_missing`.
        public let reason: String?

        public init(components: [String], reason: String? = nil) {
            self.components = components
            self.reason = reason
        }

        enum CodingKeys: String, CodingKey { case components, reason }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            components = (try? c.decodeIfPresent([String].self, forKey: .components)) ?? []
            reason = try? c.decodeIfPresent(String.self, forKey: .reason)
        }
    }

    /// The admin block: only an admin's answer has one.
    public struct Admin: Equatable, Sendable {
        public let manageComponents: Bool
        /// One entry per feature that is off, by the home's feature id (a newer home's too:
        /// its components are off all the same).
        public let off: [String: Off]

        public init(manageComponents: Bool = true, off: [String: Off] = [:]) {
            self.manageComponents = manageComponents
            self.off = off
        }
    }

    /// The known features the home answered "off".
    public let off: Set<Feature>
    /// nil: a viewer (or no answer yet).
    public let admin: Admin?

    public init(off: Set<Feature> = [], admin: Admin? = nil) {
        self.off = off
        self.admin = admin
    }

    /// Every feature on, nothing known about the person: before the first answer, on a home
    /// from before capabilities, and with the build switch off.
    public static let allOn = Capabilities()

    public func isOn(_ f: Feature) -> Bool { !off.contains(f) }

    public var isAdmin: Bool { admin != nil }

    /// What a screen does with a feature: show it, explain it is off (an admin), or leave it
    /// out (anyone else).
    public enum Shown: Equatable, Sendable { case on, explain, hidden }

    public func shown(_ f: Feature) -> Shown {
        isOn(f) ? .on : isAdmin ? .explain : .hidden
    }

    /// The parts that are off, each once (an admin's answer; empty for anyone else). An entry
    /// that names no component counts as one part.
    public var offComponents: Set<String> {
        var parts = Set<String>()
        for (feature, o) in admin?.off ?? [:] {
            if o.components.isEmpty { parts.insert("feature:\(feature)") } else { parts.formUnion(o.components) }
        }
        return parts
    }

    /// The home's Components page (story 51.10), where an admin turns a part back on.
    public static func componentsPage(_ home: URL) -> URL {
        home.appending(path: "settings/components")
    }

    /// Reads the home's answer. Values other than "off" count as on.
    public static func parse(_ data: Data) throws -> (Capabilities, etag: String?) {
        let w = try JSONDecoder().decode(Wire.self, from: data)
        let off = Set((w.features ?? [:]).compactMap { $0.value == "off" ? Feature(rawValue: $0.key) : nil })
        let admin = w.admin.map { Admin(manageComponents: $0.manageComponents ?? false, off: $0.off ?? [:]) }
        return (Capabilities(off: off, admin: admin), w.etag)
    }

    private struct Wire: Decodable {
        struct AdminBlock: Decodable {
            let manageComponents: Bool?
            let off: [String: Off]?
            enum CodingKeys: String, CodingKey {
                case manageComponents = "manage_components"
                case off
            }
        }

        let etag: String?
        let features: [String: String]?
        let admin: AdminBlock?
    }
}

/// What `GET /api/capabilities` answered.
public enum CapabilitiesAnswer: Equatable, Sendable {
    /// A new answer, with the ETag to send next time.
    case changed(Capabilities, etag: String?)
    /// 304: the same as the ETag sent.
    case notModified
    /// 404: a home from before capabilities (every feature on).
    case notOnThisHome
}

/// Where the store asks (the home's client; a fake in tests).
public protocol CapabilitiesSource: AnyObject, Sendable {
    func capabilities(etag: String?) async throws -> CapabilitiesAnswer
}

extension APIClient: CapabilitiesSource {
    /// `GET /api/capabilities` with `If-None-Match` when an ETag is kept. A 304 and a 404 are
    /// answers here, not failures (neither is logged as an error).
    public func capabilities(etag: String?) async throws -> CapabilitiesAnswer {
        var r = request("GET", "/api/capabilities")
        // the ETag is the app's, kept in memory: URLSession's own cache never answers for it
        r.cachePolicy = .reloadIgnoringLocalCacheData
        if let etag { r.setValue(etag, forHTTPHeaderField: "If-None-Match") }
        let (data, response) = try await exchange(r, answered: [304, 404])
        switch response?.statusCode ?? 0 {
        case 304: return .notModified
        case 404: return .notOnThisHome
        default: break
        }
        do {
            let (caps, bodyTag) = try Capabilities.parse(data)
            let header = response?.value(forHTTPHeaderField: "ETag")
            return .changed(caps, etag: header ?? bodyTag.map { "\"\($0)\"" })
        } catch {
            Log.network.error("GET /api/capabilities: answer not capabilities: \(String(describing: error), privacy: .private)")
            throw APIError.decoding(String(describing: error))
        }
    }
}

/// The current home's capabilities, kept by the session (`AppSession.capabilities`): asked at
/// sign-in, when the app comes to the front (`SessionGate`, the TV's root) and every 5
/// minutes, with the ETag kept in memory. A 304 keeps what is known; a failure keeps the last
/// good answer; a home from before capabilities (404) has every feature on. Another home, or
/// signing out, starts again from every feature on.
///
/// The build switch `capabilitiesGate` (the app's Info.plist key `MaktabaCapabilitiesGate`,
/// on when absent): off, nothing is asked and every feature is on. The home's 409
/// `component_off` still explains a screen whose part is off.
@MainActor @Observable
public final class CapabilitiesStore {
    /// What the screens read.
    public private(set) var current: Capabilities = .allOn
    /// The ETag of `current` (sent as If-None-Match).
    @ObservationIgnored public private(set) var etag: String?

    @ObservationIgnored private var source: (any CapabilitiesSource)?
    /// Moves on each `use` and `reset`: an answer for an older home is dropped.
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var timer: Task<Void, Never>?

    /// The build switch.
    public let gate: Bool
    /// How often it asks while signed in.
    public let every: Duration
    private let sleep: @Sendable (Duration) async throws -> Void

    public static let interval: Duration = .seconds(300)

    /// The build switch `capabilitiesGate`: the app's `MaktabaCapabilitiesGate` (a Bool, or
    /// "off"/"NO"), on when the app does not set it.
    public static var capabilitiesGate: Bool {
        switch Bundle.main.object(forInfoDictionaryKey: "MaktabaCapabilitiesGate") {
        case let b as Bool: b
        case let s as String: !["off", "no", "false", "0"].contains(s.lowercased())
        default: true
        }
    }

    public init(gate: Bool = CapabilitiesStore.capabilitiesGate, every: Duration = CapabilitiesStore.interval,
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.gate = gate
        self.every = every
        self.sleep = sleep
    }

    /// A home was signed in to: forget the last home's answer and ask this one from now on.
    public func use(_ source: any CapabilitiesSource) {
        reset()
        self.source = source
    }

    /// Signed out or another home: every feature on, nothing asked.
    public func reset() {
        timer?.cancel()
        timer = nil
        source = nil
        etag = nil
        generation += 1
        if current != .allOn { current = .allOn }
    }

    /// Asks now, then every `every` until `reset` or another `use`.
    public func start() {
        timer?.cancel()
        guard gate, source != nil else { return }
        let sleep = sleep, every = every
        timer = Task { [weak self] in
            await self?.refresh()
            while !Task.isCancelled {
                do { try await sleep(every) } catch { return }
                await self?.refresh()
            }
        }
    }

    /// Asks the home once (the app came to the front, the timer).
    public func refresh() async {
        guard gate, let source else { return }
        let mine = generation
        do {
            let answer = try await source.capabilities(etag: etag)
            guard mine == generation else { return }
            switch answer {
            case let .changed(caps, tag):
                if caps != current { current = caps }
                etag = tag
            case .notModified:
                break
            case .notOnThisHome:
                etag = nil
                if current != .allOn { current = .allOn }
            }
        } catch {
            // the last good answer stays (an app never hides a feature because the home did
            // not answer)
            if Log.isCancellation(error) { return }
            Log.failure("capabilities", error, logger: Log.network)
        }
    }
}

/// The words for a feature that is off, English and Arabic (MaktabaKit's catalog; the
/// features' names are `ErrorStrings.featureName`, the same as the 409's).
public enum FeatureWords {
    /// The feature's name in the app's language.
    public static func name(_ f: Capabilities.Feature) -> String {
        ErrorStrings(AppLanguage.isRightToLeft ? .ar : .en).featureName(f.rawValue) ?? f.rawValue
    }

    /// "Live TV is off on this home." (an admin's note): each feature has its own sentence
    /// (`capabilities.off.<id>`, the web's words) so the verb agrees with its name
    /// ("Transcripts are off", «البث المباشر متوقف»).
    public static func offOnThisHome(_ f: Capabilities.Feature, _ locale: Locale = AppLanguage.locale) -> String {
        offSentence(f.rawValue, locale)
    }

    /// The off sentence of a feature id; one this app has no sentence for reads through the
    /// frame whose head noun carries the agreement ("The <id> feature is off on this home.").
    public static func offSentence(_ id: String, _ locale: Locale = AppLanguage.locale) -> String {
        if let s = kitWord("capabilities.off.\(id)", locale) { return s }
        let name = featureName(id, locale)
        return kit("The \(name) feature is off on this home.", locale)
    }

    private static func featureName(_ id: String, _ locale: Locale) -> String {
        let ar = locale.language.characterDirection == .rightToLeft
        return ErrorStrings(ar ? .ar : .en).featureName(id) ?? id
    }

    /// The admin's button to the Components page.
    public static var turnOn: String { kit("Turn it on in Components") }

    /// "Live TV isn't available on this home." (a viewer's link to a hidden screen).
    public static func unavailable(_ f: Capabilities.Feature, _ locale: Locale = AppLanguage.locale) -> String {
        if let s = kitWord("capabilities.unavailable.\(f.rawValue)", locale) { return s }
        let name = featureName(f.rawValue, locale)
        return kit("The \(name) feature isn't available on this home.", locale)
    }

    /// An admin's line under the search field while search by meaning is off.
    public static var wordsOnly: String { kit("Searching by words only. Search by meaning is off.") }

    /// The Remote's line: "3 parts are off".
    public static func partsOff(_ n: Int) -> String { kit("\(n) parts are off") }
}

/// An admin's note where a feature that is off would be: "Live TV is off on this home." and
/// a button to the home's Components page (in the browser). On Apple TV, which has no
/// browser, the page's address is shown instead.
public struct FeatureOffNote: View {
    let feature: Capabilities.Feature
    let home: URL
    var compact: Bool

    @Environment(\.openURL) private var openURL

    public init(_ feature: Capabilities.Feature, home: URL, compact: Bool = false) {
        self.feature = feature
        self.home = home
        self.compact = compact
    }

    public var body: some View {
        VStack(alignment: compact ? .leading : .center, spacing: MaktabaTokens.Spacing.s3) {
            if !compact {
                Image(systemName: "poweroff")
                    .font(.largeTitle)
                    .foregroundStyle(Color.mktMuted)
                    .accessibilityHidden(true)
            }
            Text(verbatim: FeatureWords.offOnThisHome(feature))
                .font(.maktabaBody(compact ? 15 : MaktabaTokens.TypeSize.lg, weight: compact ? .regular : .semibold,
                                   relativeTo: compact ? .body : .title3))
                .foregroundStyle(compact ? Color.mktMuted : Color.mktText)
                .multilineTextAlignment(compact ? .leading : .center)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("feature.off.\(feature.rawValue)")
            #if os(tvOS)
            Text(verbatim: "\(FeatureWords.turnOn): \(Capabilities.componentsPage(home).absoluteString)")
                .foregroundStyle(Color.mktMuted)
                .multilineTextAlignment(.center)
                .accessibilityIdentifier("feature.turnOn")
            #else
            Button {
                openURL(Capabilities.componentsPage(home))
            } label: {
                Text(verbatim: FeatureWords.turnOn)
            }
            .buttonStyle(.bordered)
            .tint(Color.brass)
            .accessibilityIdentifier("feature.turnOn")
            #endif
        }
        .padding(compact ? 0 : MaktabaTokens.Spacing.s5)
        .frame(maxWidth: compact ? .infinity : 520, alignment: compact ? .leading : .center)
        .accessibilityElement(children: .contain)
    }
}
