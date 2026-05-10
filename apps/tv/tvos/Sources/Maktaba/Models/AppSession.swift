import Foundation

public final class AppSession: ObservableObject {
    @Published public var isPaired: Bool
    @Published public var serverURL: URL?
    @Published public var pairedDeviceID: String?

    public init(isPaired: Bool = false, serverURL: URL? = nil, pairedDeviceID: String? = nil) {
        self.isPaired = isPaired
        self.serverURL = serverURL
        self.pairedDeviceID = pairedDeviceID
    }
}

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
}
