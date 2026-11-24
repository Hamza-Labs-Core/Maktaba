import Foundation

/// Whether this build shows Watch a broadcast (story 50.27) at all: off while home
/// broadcasting is off. Off, no app shows the entry (home picker, settings), an invite link
/// opens nothing and saved broadcasts never show; the guest code stays, unreached, for when
/// broadcasting is turned on (`built` set to true). On, `GuestGate` still decides each time.
public enum GuestBroadcast {
    /// The build's switch.
    public static let built = false

    /// Unit tests set it (nil: the build's switch).
    nonisolated(unsafe) public static var override: Bool?

    /// Whether the entry, invite links and saved broadcasts may show in this build. A Debug
    /// build launched by the UI tests with `-maktaba-guest-broadcast` turns it on.
    public static var enabled: Bool {
        if let override { return override }
        #if DEBUG
        if CommandLine.arguments.contains("-maktaba-guest-broadcast") { return true }
        #endif
        return built
    }
}

/// Whether Watch a broadcast (story 50.27) may open on this device now.
///
/// A guest has no account the edge could check, so hiding the entry under a parental limit
/// (Epic 57) is the only guard, and it fails closed: the entry opens only once the limit is
/// known and is not a limit.
///
/// - Signed in to a home: the home must have said (`ParentalStore.confirmed`; a failed read
///   is not an answer), and the sign-in must not be limited, by its account or its device.
/// - Connecting to a home or restoring its sign-in (the last home, reopened at launch, from
///   the moment the app starts): not known yet.
/// - No home signed in: the device's kept flag (`ParentalStore.deviceWasLimited`), true while
///   any sign-in on this device was limited when its home last said. A device no home ever
///   answered on may watch: the spec makes Watch a broadcast need no home and no account.
///   A home that answers 404 (from before Epic 57) says nothing of a limit, so the kept
///   flag counts while signed in to it too.
public enum GuestGate: Equatable, Sendable {
    /// The entry shows; an invite link opens.
    case allowed
    /// The limit is not known yet: the entry hides and an invite link waits.
    case checking
    /// Under a parental limit: the entry hides and an invite link is refused.
    case limited

    public var allowed: Bool { self == .allowed }

    /// The decision from what the session knows.
    /// `predatesLimits`: the signed-in home answered 404 (from before Epic 57), so it says
    /// nothing of a limit and the device's kept flag still counts. `restoring` covers any
    /// connect under way to a home, the last one reopened at launch among them.
    public static func decide(signedIn: Bool, restoring: Bool, confirmed: Bool, limited: Bool,
                              deviceWasLimited: Bool, predatesLimits: Bool = false) -> GuestGate {
        if signedIn {
            guard confirmed else { return .checking }
            return limited || (predatesLimits && deviceWasLimited) ? .limited : .allowed
        }
        if restoring { return .checking }
        return deviceWasLimited ? .limited : .allowed
    }

    /// What an invite link opened while the entry is closed says: it waits for the limit
    /// (`checking`) or is refused (`limited`); nil when it opens.
    public var inviteMessage: String? {
        switch self {
        case .allowed: nil
        case .checking: GuestWords.message("guest.gate.checking")
        case .limited: GuestWords.message("guest.gate.limited")
        }
    }
}

public extension AppSession {
    /// Whether Watch a broadcast shows now: the build has it on (`GuestBroadcast`) and the
    /// gate allows it (`GuestGate`).
    var showsWatchBroadcast: Bool { GuestBroadcast.enabled && guestGate.allowed }

    /// Watch a broadcast's gate now (`GuestGate`).
    var guestGate: GuestGate {
        GuestGate.decide(signedIn: signedIn, restoring: restoring || connecting, confirmed: parental.confirmed,
                         limited: parental.current.limited, deviceWasLimited: parental.deviceWasLimited,
                         predatesLimits: parental.predatesLimits)
    }
}

/// An invite link that came while the gate was closed: kept while the limit is not known,
/// opened once it is known and not a limit, dropped (with the refusal) under one.
public struct HeldInvite: Equatable, Sendable {
    public private(set) var token: String?

    public init(token: String? = nil) { self.token = token }

    /// What to do with a link's `invite` under `gate`: open it now (the token), or keep it.
    public mutating func arrive(_ invite: String, gate: GuestGate) -> String? {
        switch gate {
        case .allowed:
            token = nil
            return invite
        case .checking:
            token = invite
            return nil
        case .limited:
            token = nil
            return nil
        }
    }

    /// The gate moved: the kept invite to open now, if it may.
    public mutating func gateChanged(_ gate: GuestGate) -> String? {
        switch gate {
        case .allowed:
            defer { token = nil }
            return token
        case .checking:
            return nil
        case .limited:
            token = nil
            return nil
        }
    }
}
