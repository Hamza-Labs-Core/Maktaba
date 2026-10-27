import Foundation
import os
#if canImport(MetricKit) && !os(tvOS)
import MetricKit
#endif

/// Crash reports, kept on this device: MetricKit hands the app its own diagnostics
/// (a crash's exception, signal and call stack) on the next launch. Each is logged
/// (`Log.crash`, at fault level: Console.app and a sysdiagnose keep it) and written, the
/// last `keep` of them, under Application Support/CrashReports, where a person can find one
/// to attach to a bug report. Nothing leaves the device: no third-party service, nothing
/// sent to the home. tvOS has no MetricKit: there the system's own crash logs are all.
public final class CrashReports: NSObject, @unchecked Sendable {
    public static let shared = CrashReports()

    /// How many reports are kept (the oldest go first).
    static let keep = 5

    private let lock = NSLock()
    private var started = false

    /// Starts listening (once per launch, from the app's init). Reports arrive at a launch
    /// after the crash, on a background queue.
    public func start() {
        lock.lock()
        defer { lock.unlock() }
        guard !started else { return }
        started = true
        #if canImport(MetricKit) && !os(tvOS)
        MXMetricManager.shared.add(self)
        #endif
    }

    /// Where the reports are written.
    public static var directory: URL? {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first?
            .appending(path: "CrashReports", directoryHint: .isDirectory)
    }

    /// One line per crash in a diagnostic payload's JSON: what it was (exception type and
    /// code, signal, termination reason) and which release crashed. No call stack, no
    /// path: the file keeps those.
    static func summaries(fromPayload json: Data) -> [String] {
        guard let root = try? JSONSerialization.jsonObject(with: json) as? [String: Any],
              let crashes = root["crashDiagnostics"] as? [[String: Any]] else { return [] }
        return crashes.map { crash in
            let meta = crash["diagnosticMetaData"] as? [String: Any] ?? [:]
            func field(_ key: String) -> String? {
                switch meta[key] {
                case let s as String: s
                case let n as NSNumber: n.stringValue
                default: nil
                }
            }
            return [
                field("exceptionType").map { "exception=\($0)" },
                field("exceptionCode").map { "code=\($0)" },
                field("signal").map { "signal=\($0)" },
                field("terminationReason").map { "reason=\($0)" },
                field("appVersion").map { "app=\($0)" },
                field("appBuildVersion").map { "build=\($0)" },
                field("osVersion").map { "os=\($0)" },
            ].compactMap { $0 }.joined(separator: " ")
        }
    }

    /// Keeps a payload's JSON as a file (the last `keep` of them).
    static func store(_ json: Data, in dir: URL, now: Date = .now) throws {
        let fm = FileManager.default
        try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        let name = "crash-\(Int(now.timeIntervalSince1970))-\(UUID().uuidString.prefix(8)).json"
        try json.write(to: dir.appending(path: name), options: .atomic)
        let files = try fm.contentsOfDirectory(at: dir, includingPropertiesForKeys: nil)
            .filter { $0.lastPathComponent.hasPrefix("crash-") && $0.pathExtension == "json" }
            .sorted { $0.lastPathComponent < $1.lastPathComponent }
        for old in files.dropLast(keep) { try? fm.removeItem(at: old) }
    }

    func received(_ payloadJSON: Data) {
        let lines = Self.summaries(fromPayload: payloadJSON)
        guard !lines.isEmpty else { return }
        for line in lines {
            Log.crash.fault("crashed at a previous launch: \(line, privacy: .public)")
        }
        guard let dir = Self.directory else { return }
        do {
            try Self.store(payloadJSON, in: dir)
        } catch {
            Log.crash.error("keeping a crash report failed: \(String(describing: error), privacy: .private)")
        }
    }
}

#if canImport(MetricKit) && !os(tvOS)
extension CrashReports: MXMetricManagerSubscriber {
    public func didReceive(_ payloads: [MXDiagnosticPayload]) {
        for p in payloads where !(p.crashDiagnostics ?? []).isEmpty {
            received(p.jsonRepresentation())
        }
    }
}
#endif
