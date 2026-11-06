import Foundation

/// The app's own log lines, kept on the device (Epic 53 story 53.01): each event `Log` writes to
/// `os.Logger` is also appended, as a shared-schema line (`shared/log/schema/line.schema.json`), to
/// `Application Support/Logs/app.ndjson`. At 512 KB the file becomes `app.1.ndjson` (the older one
/// goes), so the ring stays under 1 MB with the newest lines kept. What a problem report attaches
/// (story 53.15). Values are scrubbed with the shared list; nothing names a person.
public final class LogRing: @unchecked Sendable {
    /// One file's size before it rolls over.
    public static let fileLimit = 512 * 1024

    public let directory: URL
    private let lock = NSLock()
    private let base: [String: String]
    private var handle: FileHandle?
    private var size = 0

    /// The app's ring, in Application Support (Caches on tvOS, which keeps no other local
    /// storage); nil when there is none, as in some test hosts.
    public static let shared: LogRing? = {
        #if os(tvOS)
        let where_ = FileManager.SearchPathDirectory.cachesDirectory
        #else
        let where_ = FileManager.SearchPathDirectory.applicationSupportDirectory
        #endif
        guard let support = FileManager.default.urls(for: where_, in: .userDomainMask).first else { return nil }
        return LogRing(directory: support.appendingPathComponent("Logs", isDirectory: true), base: LogRing.appFields())
    }()

    public init(directory: URL, base: [String: String]) {
        self.directory = directory
        self.base = base
    }

    public var current: URL { directory.appendingPathComponent("app.ndjson") }
    public var previous: URL { directory.appendingPathComponent("app.1.ndjson") }

    /// The client fields every line carries: the app, its version, the platform and OS version.
    public static func appFields(bundle: Bundle = .main) -> [String: String] {
        let info = bundle.infoDictionary ?? [:]
        var f: [String: String] = [
            "service": service(bundleID: bundle.bundleIdentifier ?? ""),
            "app": bundle.bundleIdentifier ?? "com.hamzalabs.maktaba",
            "app_version": info["CFBundleShortVersionString"] as? String ?? "-",
            "os_version": ProcessInfo.processInfo.operatingSystemVersionString,
        ]
        #if os(tvOS)
        f["platform"] = "tvos"
        #elseif os(iOS)
        f["platform"] = "ios"
        #else
        f["platform"] = "macos"
        #endif
        return f
    }

    /// The schema's `service` for an app: apple.tv, apple.remote or apple.player.
    public static func service(bundleID: String) -> String {
        #if os(tvOS)
        return "apple.tv"
        #else
        return bundleID.lowercased().contains("remote") ? "apple.remote" : "apple.player"
        #endif
    }

    /// Appends one line. `msg` is a snake_case event name; `attrs` are scrubbed.
    public func write(level: String, msg: String, attrs: [String: Any] = [:], at: Date = Date()) {
        var line: [String: Any] = base
        line["ts"] = LogRing.timestamp(at)
        line["level"] = level
        line["msg"] = msg
        if !attrs.isEmpty { line["attrs"] = Redact.attrs(attrs) }
        guard var data = try? JSONSerialization.data(withJSONObject: line, options: [.sortedKeys, .withoutEscapingSlashes]) else { return }
        data.append(0x0A)
        lock.lock()
        defer { lock.unlock() }
        append(data)
    }

    /// Both files' lines, oldest first.
    public func lines() -> [String] {
        lock.lock()
        defer { lock.unlock() }
        try? handle?.synchronize()
        return [previous, current].flatMap { url -> [String] in
            guard let text = try? String(contentsOf: url, encoding: .utf8) else { return [] }
            return text.split(separator: "\n").map(String.init)
        }
    }

    private func append(_ data: Data) {
        let fm = FileManager.default
        if handle == nil {
            try? fm.createDirectory(at: directory, withIntermediateDirectories: true)
            if !fm.fileExists(atPath: current.path) { fm.createFile(atPath: current.path, contents: nil) }
            handle = try? FileHandle(forWritingTo: current)
            size = Int((try? handle?.seekToEnd()) ?? 0)
        }
        if size + data.count > LogRing.fileLimit {
            try? handle?.close()
            handle = nil
            try? fm.removeItem(at: previous)
            try? fm.moveItem(at: current, to: previous)
            fm.createFile(atPath: current.path, contents: nil)
            handle = try? FileHandle(forWritingTo: current)
            size = 0
        }
        guard let h = handle else { return }
        do {
            try h.write(contentsOf: data)
            size += data.count
        } catch {
            // the disk is full or gone: os_log still has the line
        }
    }

    // ISO8601DateFormatter is thread-safe once set up
    nonisolated(unsafe) private static let stamp: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        f.timeZone = TimeZone(identifier: "UTC")
        return f
    }()

    static func timestamp(_ d: Date) -> String { stamp.string(from: d) }
}
