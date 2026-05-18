import Foundation

#if canImport(Combine)
import Combine
#endif

/// AppSession holds the paired-server state and vends real,
/// network-backed services. It is `ObservableObject` only where
/// Combine is available (the tvOS UI target); the core stays usable
/// host-side for unit tests.
#if canImport(Combine)
public final class AppSession: ObservableObject {
    @Published public var isPaired: Bool
    @Published public var serverURL: URL?
    @Published public var pairedDeviceID: String?

    public init(isPaired: Bool = false, serverURL: URL? = nil, pairedDeviceID: String? = nil) {
        self.isPaired = isPaired
        self.serverURL = serverURL
        self.pairedDeviceID = pairedDeviceID
    }

    /// Live API config derived from the paired server + device token.
    public var apiConfig: APIConfig? {
        guard let url = serverURL else { return nil }
        return APIConfig(baseURL: url, deviceToken: pairedDeviceID)
    }
}
#else
public final class AppSession {
    public var isPaired: Bool
    public var serverURL: URL?
    public var pairedDeviceID: String?

    public init(isPaired: Bool = false, serverURL: URL? = nil, pairedDeviceID: String? = nil) {
        self.isPaired = isPaired
        self.serverURL = serverURL
        self.pairedDeviceID = pairedDeviceID
    }

    public var apiConfig: APIConfig? {
        guard let url = serverURL else { return nil }
        return APIConfig(baseURL: url, deviceToken: pairedDeviceID)
    }
}
#endif

public struct PairingCode: Codable, Equatable {
    public let code: String
    public let expiresAt: Date

    public init(code: String, expiresAt: Date) {
        self.code = code
        self.expiresAt = expiresAt
    }
}

public struct VideoSummary: Codable, Equatable, Identifiable {
    public let id: String
    public let title: String
    public let durationSec: Double
    public let positionSec: Double?
    public let posterURL: URL?

    public init(id: String, title: String, durationSec: Double, positionSec: Double?, posterURL: URL?) {
        self.id = id
        self.title = title
        self.durationSec = durationSec
        self.positionSec = positionSec
        self.posterURL = posterURL
    }

    public var progressFraction: Double {
        guard let p = positionSec, durationSec > 0 else { return 0 }
        return min(1.0, p / durationSec)
    }

    /// Remaining-time label source (Story 14.5 AC: items show
    /// poster+title+remaining+progress).
    public var remainingSec: Double {
        guard let p = positionSec, durationSec > 0 else { return durationSec }
        return max(0, durationSec - p)
    }
}
