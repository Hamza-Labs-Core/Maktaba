import Foundation
import Observation

/// Signing this device in by code (Epic 42 story 42.6): it shows a code from the home
/// (`POST /api/pairing/device`) until a person signed in elsewhere (the Remote, the Player's
/// account menu, the home's web page) approves it, polling `POST /api/pairing/device/token`;
/// an expired code is replaced by a new one. A home from before codes signs in by password.
///
/// Story 48.37: it publishes each step as a `ConnectProgress` (asking for a code, waiting
/// for approval with the code and its expiry, the code expired, the home busy until its
/// Retry-After, why asking failed), the attempt one more after each new try.
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
    /// What it is doing now, step by step (nil: the home signs in by password only).
    public private(set) var progress: ConnectProgress?
    /// Hears every `progress` published, in order (the tests' transition log).
    var progressObserver: (@MainActor (ConnectProgress?) -> Void)?

    private let client: APIClient
    private let label: String
    private let homeName: String
    private let finish: @MainActor () async throws -> Void
    private let clock: ConnectClock
    private var attempt: Int

    /// - `label`: this device's name, shown to the person approving it.
    /// - `homeName`: the home's name, as the progress says it.
    /// - `attempt`: the first attempt's number (a Retry starts a new flow at the next one).
    /// - `finish`: once approved (the tokens are kept), say who signed in (`AppSession.signedInByCode`).
    public init(client: APIClient, label: String, homeName: String = "", attempt: Int = 1,
                clock: ConnectClock = .system,
                finish: @escaping @MainActor () async throws -> Void) {
        self.client = client
        self.label = label
        self.homeName = homeName.isEmpty ? client.home.progressAddress : homeName
        self.attempt = max(1, attempt)
        self.clock = clock
        self.finish = finish
    }

    /// As above, waiting with `sleep` on the system's time.
    public convenience init(client: APIClient, label: String,
                            finish: @escaping @MainActor () async throws -> Void,
                            sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.init(client: client, label: label, clock: ConnectClock(now: { Date() }, sleep: sleep), finish: finish)
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
                publish(.signingIn(home: homeName))
                guard let p = try await client.startDevicePairing(label: label) else {
                    status = .unsupported
                    publish(nil)
                    return
                }
                status = .showing(p)
                problem = nil
                publish(.awaitingApproval(code: p.code, expiresAt: p.expiresAt))
                if try await wait(for: p) { return }
                // expired before anyone approved it: said so, then a new code
                publish(.failed(reason: .approvalExpired, tried: []))
                attempt += 1
            } catch is CancellationError {
                return
            } catch {
                let f = HomeFailure(error)
                problem = f.message()
                do {
                    if case let .busy(after?) = f.kind {
                        publish(.homeBusy(retryAt: clock.now().addingTimeInterval(TimeInterval(after))))
                        try await clock.sleep(.seconds(after))
                    } else {
                        publish(.failed(reason: ConnectProgress.failure(f), tried: [client.home.progressAddress]))
                        try await clock.sleep(.seconds(5))
                    }
                } catch {
                    return
                }
                attempt += 1
            }
        }
    }

    /// Polls until approved (true) or expired (false: a new code).
    private func wait(for p: DevicePairing) async throws -> Bool {
        while clock.now() < p.expiresAt {
            try await clock.sleep(.seconds(p.interval))
            try Task.checkCancellation()
            let state: DevicePairing.State
            do {
                state = try await client.collectDevicePairing(p, label: label)
            } catch let e as APIError {
                // a blip: keep the code and ask again
                problem = e.message
                continue
            }
            switch state {
            case .waiting:
                problem = nil
            case .approved:
                // this device holds the tokens now: keep asking who it is, never for a new code
                publish(.signingIn(home: homeName))
                while !Task.isCancelled {
                    do {
                        try await finish()
                        status = .approved
                        return true
                    } catch {
                        problem = HomeFailure(error).message()
                        try await clock.sleep(.seconds(3))
                    }
                }
                return true
            case .expired:
                return false
            }
        }
        return false
    }

    private func publish(_ step: ConnectProgress.Step?) {
        let next = step.map { ConnectProgress($0, attempt: attempt) }
        guard next != progress else { return }
        progress = next
        progressObserver?(next)
    }
}
