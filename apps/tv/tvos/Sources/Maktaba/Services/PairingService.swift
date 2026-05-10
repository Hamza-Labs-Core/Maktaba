import Foundation

public struct PairingService {
    public init() {}

    public func requestCode() async throws -> PairingCode {
        // Real impl: POST /api/pairing/request — returns short code + QR url
        return PairingCode(
            code: "ABCD-1234",
            expiresAt: Date().addingTimeInterval(300)
        )
    }

    public func waitForApproval(code: String) async throws -> String {
        // Real impl: long-poll GET /api/pairing/status?code=...
        return "stub-device-id"
    }
}

public struct LibraryService {
    public init() {}

    public func continueWatching() async throws -> [VideoSummary] {
        []
    }

    public func recommendations() async throws -> [VideoSummary] {
        []
    }

    public func listVideos() async throws -> [VideoSummary] {
        []
    }
}

public struct SearchService {
    public init() {}

    public func query(_ q: String) async throws -> [VideoSummary] {
        guard !q.isEmpty else { return [] }
        return []
    }
}
