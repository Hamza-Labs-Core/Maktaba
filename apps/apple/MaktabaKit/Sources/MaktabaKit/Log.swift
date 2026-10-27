import Foundation
import os

/// The apps' unified logging (`log stream --predicate 'subsystem BEGINSWITH "com.hamzalabs.maktaba"'`,
/// or Console.app), one category per area. What is logged in the open: a method, a route
/// with its ids taken out, a status, an error's kind and code. Anything about a person or
/// their home (an address, a name, a username, a server's own words) is `.private`, and
/// tokens, passwords and codes are never logged at all.
public enum Log {
    /// The app's bundle id (the subsystem), the same in every category.
    static let subsystem = Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba"

    /// Requests to the home and their failures.
    public static let network = Logger(subsystem: subsystem, category: "network")
    /// Homes, signing in and out, the saved sign-in.
    public static let session = Logger(subsystem: subsystem, category: "session")
    /// Keychain reads and writes.
    public static let keychain = Logger(subsystem: subsystem, category: "keychain")
    /// A screen that could not load or act.
    public static let ui = Logger(subsystem: subsystem, category: "ui")
    /// Crash and hang reports MetricKit delivers.
    public static let crash = Logger(subsystem: subsystem, category: "crash")

    /// A path as a route, every segment that may name something (an id, a code, a stage
    /// with a number) taken out: `/api/videos/01J…/poster` → `/api/videos/:id/poster`.
    public static func route(_ path: String) -> String {
        let segments = path.split(separator: "/", omittingEmptySubsequences: false).map { seg -> Substring in
            let plain = seg.allSatisfy { $0.isLetter && $0.isASCII || $0 == "-" || $0 == "_" || $0 == "." }
            return plain && seg.count <= 32 ? seg : ":id"
        }
        return segments.joined(separator: "/")
    }

    /// A screen's failure: where, and the error's kind, status and code (nothing private).
    /// A cancelled load (the screen moved on) is not a failure.
    public static func failure(_ what: StaticString, _ error: Error, logger: Logger = Log.ui) {
        guard !isCancellation(error) else { return }
        let f = HomeFailure(error)
        let w = "\(what)"
        let kind = "\(f.kind)"
        logger.error("\(w, privacy: .public) failed: \(kind, privacy: .public) status=\(f.status ?? 0, privacy: .public) type=\(f.slug ?? "-", privacy: .public)")
    }

    /// The request was cancelled because the screen that asked moved on (a search typed
    /// further, a tab left): nothing to show or log.
    public static func isCancellation(_ error: Error) -> Bool {
        if error is CancellationError { return true }
        let e = error as NSError
        return e.domain == NSURLErrorDomain && e.code == NSURLErrorCancelled
    }
}
