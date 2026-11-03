import Foundation
import Observation

/// One device waiting for an approval (`GET /api/pairing/pending`, Epic 42 story 42.6): what
/// it is and when it asked, never its code. The person still types or scans the code from
/// the device's own screen: that is the proof they can see it.
public struct PendingSignIn: Decodable, Sendable, Equatable, Identifiable {
    public let id: String
    /// `tv`, `player`, `remote`, … (the asking app's `X-Maktaba-Client`).
    public let clientApp: String?
    public let platform: String?
    public let device: String?
    /// The name the device gave itself ("Living room TV").
    public let deviceLabel: String?
    public let createdAt: Date?
    public let expiresAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, platform, device
        case clientApp = "client_app"
        case deviceLabel = "device_label"
        case createdAt = "created_at"
        case expiresAt = "expires_at"
    }

    public init(id: String, clientApp: String? = nil, platform: String? = nil, device: String? = nil,
                deviceLabel: String? = nil, createdAt: Date? = nil, expiresAt: Date? = nil) {
        self.id = id
        self.clientApp = clientApp?.nilIfEmpty
        self.platform = platform?.nilIfEmpty
        self.device = device?.nilIfEmpty
        self.deviceLabel = deviceLabel?.nilIfEmpty
        self.createdAt = createdAt
        self.expiresAt = expiresAt
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        func text(_ k: CodingKeys) throws -> String? {
            try c.decodeIfPresent(String.self, forKey: k)?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty
        }
        clientApp = try text(.clientApp)
        platform = try text(.platform)
        device = try text(.device)
        deviceLabel = try text(.deviceLabel)
        createdAt = try text(.createdAt).flatMap(homeTime)
        expiresAt = try text(.expiresAt).flatMap(homeTime)
    }

    /// A TV: it says so (its app or its device), or it says nothing at all (only an older TV
    /// app asks without naming itself). Anything else (a phone, a computer) is a device.
    public var isTV: Bool {
        if clientApp?.lowercased() == "tv" || device?.lowercased() == "tv" { return true }
        return clientApp == nil && device == nil
    }
}

/// `GET /api/pairing/pending`.
struct PendingSignIns: Decodable {
    let requests: [PendingSignIn]
}

/// The banner a signed-in app shows while a device waits for an approval: the newest request
/// the person has not dismissed. Dismissing hides it until a request with a new id arrives;
/// none waiting hides it. `run` reads the list every `interval` while the app is in front.
@MainActor @Observable
public final class PendingSignInsWatcher {
    /// What the home last said is waiting, newest first.
    public private(set) var requests: [PendingSignIn] = []
    /// The ids the person dismissed (only those still waiting are kept).
    private var dismissed: Set<String> = []

    public init() {}

    /// The request the banner shows (nil: no banner).
    public var banner: PendingSignIn? {
        requests.first { !dismissed.contains($0.id) }
    }

    /// Takes the home's latest list.
    public func update(_ list: [PendingSignIn]) {
        requests = list
        let ids = Set(list.map(\.id))
        dismissed.formIntersection(ids)
    }

    /// Hides the banner until a request with a new id arrives.
    public func dismiss() {
        dismissed.formUnion(requests.map(\.id))
    }

    /// Reads the list once. false: this home has no such list (it is older), stop asking.
    @discardableResult
    public func refresh(_ client: APIClient) async -> Bool {
        do {
            guard let list = try await client.pendingSignIns() else {
                update([])
                return false
            }
            update(list)
        } catch {
            // a blip keeps the last list; a sign-out is the session's to handle
            if !Log.isCancellation(error) { Log.failure("pending sign-ins", error) }
            if case APIError.signedOut = error { return false }
        }
        return true
    }

    /// Reads the list every `interval` until the task ends (the app goes to the background)
    /// or the home turns out to have none.
    public func run(_ client: APIClient, every interval: Duration = .seconds(12),
                    sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) async {
        while !Task.isCancelled {
            guard await refresh(client) else { return }
            do { try await sleep(interval) } catch { return }
        }
    }
}
