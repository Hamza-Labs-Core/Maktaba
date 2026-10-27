import Foundation
import Observation

/// Signing this device in by code (Epic 42 story 42.6): it shows a code from the home
/// (`POST /api/pairing/device`) until a person signed in elsewhere (the Remote, the Player's
/// account menu, the home's web page) approves it, polling `POST /api/pairing/device/token`;
/// an expired code is replaced by a new one. A home from before codes signs in by password.
@MainActor @Observable
public final class CodeSignIn {
    public enum Status: Equatable, Sendable {
        /// Asking the home for a code.
        case starting
        /// Showing this code until it is approved or expires.
        case showing(DevicePairing)
        /// This home signs in by password only.
        case unsupported
        /// Approved: this device holds the tokens; `finish` said who it is.
        case approved
    }

    public private(set) var status: Status = .starting
    /// The last thing that went wrong (it keeps trying).
    public private(set) var problem: String?

    private let client: APIClient
    private let label: String
    private let finish: @MainActor () async throws -> Void
    private let sleep: @Sendable (Duration) async throws -> Void

    /// - `label`: this device's name, shown to the person approving it.
    /// - `finish`: once approved (the tokens are kept), say who signed in (`AppSession.signedInByCode`).
    public init(client: APIClient, label: String,
                finish: @escaping @MainActor () async throws -> Void,
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.client = client
        self.label = label
        self.finish = finish
        self.sleep = sleep
    }

    /// The pairing on screen, if any.
    public var pairing: DevicePairing? {
        if case let .showing(p) = status { p } else { nil }
    }

    /// Runs until approved, unsupported or cancelled (the screen goes away).
    public func run() async {
        while !Task.isCancelled {
            do {
                status = .starting
                guard let p = try await client.startDevicePairing(label: label) else {
                    status = .unsupported
                    return
                }
                status = .showing(p)
                problem = nil
                if try await wait(for: p) { return }
            } catch is CancellationError {
                return
            } catch {
                problem = Self.say(error)
                try? await sleep(.seconds(5))
            }
        }
    }

    /// Polls until approved (true) or expired (false: a new code).
    private func wait(for p: DevicePairing) async throws -> Bool {
        while Date.now < p.expiresAt {
            try await sleep(.seconds(p.interval))
            try Task.checkCancellation()
            let state: DevicePairing.State
            do {
                state = try await client.collectDevicePairing(p, label: label)
            } catch let e as APIError {
                // a blip: keep the code and ask again
                problem = Self.say(e)
                continue
            }
            switch state {
            case .waiting:
                problem = nil
            case .approved:
                // this device holds the tokens now: keep asking who it is, never for a new code
                while !Task.isCancelled {
                    do {
                        try await finish()
                        status = .approved
                        return true
                    } catch {
                        problem = Self.say(error)
                        try await sleep(.seconds(3))
                    }
                }
                return true
            case .expired:
                return false
            }
        }
        return false
    }

    /// What went wrong in the person's words (it keeps trying, so no action), logged.
    static func say(_ error: Error) -> String? {
        let e = AppError(error)
        guard !e.isCancellation else { return nil }
        Log.failure("sign in by code", error, logger: Log.session)
        return e.message()
    }
}
