import Foundation

/// Crash reports as the home takes them (Epic 53 story 53.14): a Sentry envelope (README
/// D5) with the person's consent in its header (`consent: crash_reports_to_home`; the home
/// answers 403 without it), one `event` item (the crash: its exception, the crashed thread's
/// frames, the binary images) and one `attachment` (the app's log ring, at most 512 KB). The
/// whole envelope stays under 1 MB, the home's limit. Every string is scrubbed with the
/// shared rules (53.01) before it leaves the device; the home scrubs again.
///
/// Apple stacks are not symbolicated on the device: a frame is the binary and its offset
/// ("Maktaba Remote + 0x1a2b"), with the image's UUID, which is enough for the home to group
/// a crash and for Maktaba to symbolicate one a person forwards (53.16).
public enum Envelope {
    public static let consent = "crash_reports_to_home"
    /// The home's limits: the envelope, and the log attachment within it.
    public static let maxBytes = 1 << 20
    public static let maxAttachment = 512 << 10
    /// Frames kept from the crashed thread.
    static let maxFrames = 100

    /// The device's side of the header.
    public struct Header: Sendable {
        public var app: String
        public var appVersion: String
        public var platform: String
        public var osVersion: String
        public var deviceModel: String
        public var installID: String

        public init(app: String, appVersion: String, platform: String, osVersion: String, deviceModel: String, installID: String) {
            self.app = app
            self.appVersion = appVersion
            self.platform = platform
            self.osVersion = osVersion
            self.deviceModel = deviceModel
            self.installID = installID
        }

        /// This app on this device.
        public static func current(bundle: Bundle = .main) -> Header {
            let f = LogRing.appFields(bundle: bundle)
            return Header(app: f["service"] ?? "apple", appVersion: f["app_version"] ?? "-", platform: f["platform"] ?? "ios",
                          osVersion: f["os_version"] ?? "", deviceModel: DeviceInfo.model, installID: DeviceInfo.installID())
        }
    }

    /// One envelope: the header, the event and the log lines (the newest kept when they do not
    /// fit). `eventID` is 32 hex characters.
    public static func build(_ header: Header, event: [String: Any], log: [String], eventID: String = Envelope.newEventID()) -> Data {
        var event = scrubbed(event) as? [String: Any] ?? [:]
        event["event_id"] = eventID
        let hdr: [String: Any] = [
            "event_id": eventID, "consent": consent, "app": header.app, "app_version": header.appVersion,
            "platform": header.platform, "os_version": header.osVersion, "device_model": header.deviceModel,
            "install_id": header.installID,
        ]
        let hdrData = json(hdr)
        var eventData = json(event)
        if hdrData.count + eventData.count + 256 > maxBytes {
            // a stack this big is cut, never the envelope refused
            event = trimmedFrames(event, to: 20)
            eventData = json(event)
        }
        let room = min(maxAttachment, maxBytes - hdrData.count - eventData.count - 256)
        let attachment = tail(log.map(Redact.scrub), within: max(0, room))
        var out = Data()
        out.append(hdrData); out.append(0x0A)
        out.append(json(["type": "event", "length": eventData.count])); out.append(0x0A)
        out.append(eventData); out.append(0x0A)
        if !attachment.isEmpty {
            out.append(json(["type": "attachment", "length": attachment.count, "filename": "app.ndjson",
                             "content_type": "application/x-ndjson"]))
            out.append(0x0A)
            out.append(attachment); out.append(0x0A)
        }
        return out
    }

    /// The newest lines that fit in `limit` bytes, oldest first, joined by newlines.
    static func tail(_ lines: [String], within limit: Int) -> Data {
        var kept: [Data] = []
        var size = 0
        for l in lines.reversed() {
            let d = Data(l.utf8)
            if size + d.count + 1 > limit { break }
            kept.append(d)
            size += d.count + 1
        }
        return Data(kept.reversed().joined(separator: [0x0A]))
    }

    public static func newEventID() -> String { UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased() }

    // MARK: kept crashes as events

    /// The events of one kept crash file: a MetricKit diagnostic payload (iPhone, iPad, Mac),
    /// or a `KeptCrash` (the Apple TV's PLCrashReporter report). `inApp` is the app's
    /// executable name: its frames are the app's own.
    public static func events(fromFile data: Data, inApp: String = DeviceInfo.executable) -> [[String: Any]] {
        guard let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [] }
        if root["crashDiagnostics"] != nil { return metricKitEvents(root, inApp: inApp) }
        if let kept = try? JSONDecoder().decode(KeptCrash.self, from: data), kept.kind == KeptCrash.kindName {
            return [kept.event(inApp: inApp)]
        }
        return []
    }

    /// MetricKit's `MXDiagnosticPayload.jsonRepresentation()`: one event per crash.
    static func metricKitEvents(_ root: [String: Any], inApp: String) -> [[String: Any]] {
        let crashes = root["crashDiagnostics"] as? [[String: Any]] ?? []
        let at = (root["timeStampEnd"] as? String).flatMap(metricKitTime) ?? Date()
        return crashes.map { crash in
            let meta = crash["diagnosticMetaData"] as? [String: Any] ?? [:]
            func field(_ k: String) -> String? {
                switch meta[k] {
                case let s as String: s
                case let n as NSNumber: n.stringValue
                default: nil
                }
            }
            let tree = crash["callStackTree"] as? [String: Any] ?? [:]
            let stacks = tree["callStacks"] as? [[String: Any]] ?? []
            let crashed = stacks.first { ($0["threadAttributed"] as? Bool) == true } ?? stacks.first ?? [:]
            var raw: [[String: Any]] = []
            // the root frame is the top of the stack; each first sub-frame is its caller
            var next = (crashed["callStackRootFrames"] as? [[String: Any]])?.first
            while let f = next, raw.count < maxFrames {
                raw.append(f)
                next = (f["subFrames"] as? [[String: Any]])?.first
            }
            var images: [String: String] = [:]
            let frames: [[String: Any]] = raw.reversed().map { f in
                let binary = f["binaryName"] as? String ?? "?"
                let offset = (f["offsetIntoBinaryTextSegment"] as? NSNumber)?.uint64Value ?? 0
                if let uuid = f["binaryUUID"] as? String { images[binary] = uuid }
                var out: [String: Any] = ["function": "\(binary) + 0x\(String(offset, radix: 16))", "package": binary,
                                          "in_app": binary == inApp]
                if let a = (f["address"] as? NSNumber)?.uint64Value { out["instruction_addr"] = "0x" + String(a, radix: 16) }
                return out
            }
            let objc = meta["objectiveCexceptionReason"] as? [String: Any]
            let type = (objc?["exceptionName"] as? String) ?? machException(field("exceptionType"))
            let value = [(objc?["composedMessage"] as? String), signalName(field("signal")), field("terminationReason")]
                .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
            return event(type: type, value: value, mechanism: "metrickit", frames: frames, images: images, at: at,
                         release: field("appVersion"), build: field("appBuildVersion"), os: field("osVersion"),
                         device: field("deviceType"))
        }
    }

    static func event(type: String, value: String, mechanism: String, frames: [[String: Any]], images: [String: String],
                      at: Date, release: String?, build: String?, os: String?, device: String?) -> [String: Any] {
        var e: [String: Any] = [
            "platform": "cocoa", "level": "fatal", "timestamp": LogRing.timestamp(at),
            "exception": ["values": [[
                "type": type, "value": value.isEmpty ? type : value,
                "mechanism": ["type": mechanism, "handled": false],
                "stacktrace": ["frames": frames],
            ] as [String: Any]]],
            "debug_meta": ["images": images.keys.sorted().map { ["type": "macho", "code_file": $0, "debug_id": images[$0]!] }],
        ]
        if let release { e["release"] = build.map { "\(release) (\($0))" } ?? release }
        var contexts: [String: Any] = [:]
        if let os { contexts["os"] = ["version": os] }
        if let device { contexts["device"] = ["model": device] }
        if !contexts.isEmpty { e["contexts"] = contexts }
        return e
    }

    /// Mach exception numbers (MetricKit sends them as numbers).
    static func machException(_ n: String?) -> String {
        switch n {
        case "1": "EXC_BAD_ACCESS"
        case "2": "EXC_BAD_INSTRUCTION"
        case "3": "EXC_ARITHMETIC"
        case "4": "EXC_EMULATION"
        case "5": "EXC_SOFTWARE"
        case "6": "EXC_BREAKPOINT"
        case "10": "EXC_CRASH"
        case "11": "EXC_RESOURCE"
        case "12": "EXC_GUARD"
        case nil: "crash"
        case let n?: "EXC_\(n)"
        }
    }

    static func signalName(_ n: String?) -> String? {
        switch n {
        case "4": "SIGILL"
        case "5": "SIGTRAP"
        case "6": "SIGABRT"
        case "8": "SIGFPE"
        case "9": "SIGKILL"
        case "10": "SIGBUS"
        case "11": "SIGSEGV"
        case "13": "SIGPIPE"
        case nil, "0": nil
        case let n?: "signal \(n)"
        }
    }

    nonisolated(unsafe) private static let mkTime: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd HH:mm:ss Z"
        return f
    }()

    static func metricKitTime(_ s: String) -> Date? { homeTime(s) ?? mkTime.date(from: s) }

    // MARK: plumbing

    static func json(_ v: Any) -> Data {
        (try? JSONSerialization.data(withJSONObject: v, options: [.sortedKeys, .withoutEscapingSlashes])) ?? Data("{}".utf8)
    }

    /// Strings scrubbed and sensitive keys masked, all the way down.
    static func scrubbed(_ v: Any) -> Any {
        switch v {
        case let s as String: return Redact.scrub(s)
        case let d as [String: Any]:
            var out: [String: Any] = [:]
            for (k, x) in d { out[k] = Redact.keySensitive(k, isString: x is String) ? Redact.keyMask : scrubbed(x) }
            return out
        case let a as [Any]: return a.map(scrubbed)
        default: return v
        }
    }

    static func trimmedFrames(_ e: [String: Any], to n: Int) -> [String: Any] {
        var e = e
        guard var ex = e["exception"] as? [String: Any], var values = ex["values"] as? [[String: Any]] else { return e }
        for i in values.indices {
            if var st = values[i]["stacktrace"] as? [String: Any], let frames = st["frames"] as? [[String: Any]] {
                st["frames"] = Array(frames.suffix(n))
                values[i]["stacktrace"] = st
            }
        }
        ex["values"] = values
        e["exception"] = ex
        return e
    }
}

/// A crash kept as this kit's own file: what the Apple TV app writes from PLCrashReporter's
/// report (tvOS has no MetricKit). Frames are the crashed thread's, top first.
public struct KeptCrash: Codable, Sendable {
    public static let kindName = "maktaba.crash"

    public struct Frame: Codable, Sendable {
        public var binary: String
        public var offset: UInt64
        public var address: UInt64
        public init(binary: String, offset: UInt64, address: UInt64) {
            self.binary = binary
            self.offset = offset
            self.address = address
        }
    }

    public var kind = KeptCrash.kindName
    public var at: Date
    public var appVersion: String?
    public var build: String?
    public var osVersion: String?
    public var device: String?
    /// The signal ("SIGSEGV") or exception name ("NSInvalidArgumentException").
    public var type: String
    public var reason: String
    public var frames: [Frame]
    /// Each binary's UUID, by name.
    public var images: [String: String]

    public init(at: Date, appVersion: String?, build: String?, osVersion: String?, device: String?, type: String,
                reason: String, frames: [Frame], images: [String: String]) {
        self.at = at
        self.appVersion = appVersion
        self.build = build
        self.osVersion = osVersion
        self.device = device
        self.type = type
        self.reason = reason
        self.frames = frames
        self.images = images
    }

    func event(inApp: String) -> [String: Any] {
        let frames: [[String: Any]] = self.frames.prefix(Envelope.maxFrames).reversed().map { f in
            ["function": "\(f.binary) + 0x\(String(f.offset, radix: 16))", "package": f.binary, "in_app": f.binary == inApp,
             "instruction_addr": "0x" + String(f.address, radix: 16)]
        }
        return Envelope.event(type: type, value: reason, mechanism: "plcrashreporter", frames: frames,
                              images: images.filter { name, _ in self.frames.contains { $0.binary == name } },
                              at: at, release: appVersion, build: build, os: osVersion, device: device)
    }

    /// The file it is kept as, beside MetricKit's.
    public func data() -> Data {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .secondsSince1970
        return (try? e.encode(self)) ?? Data()
    }
}

extension KeptCrash {
    enum CodingKeys: String, CodingKey { case kind, at, appVersion, build, osVersion, device, type, reason, frames, images }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        kind = try c.decode(String.self, forKey: .kind)
        at = Date(timeIntervalSince1970: try c.decode(Double.self, forKey: .at))
        appVersion = try c.decodeIfPresent(String.self, forKey: .appVersion)
        build = try c.decodeIfPresent(String.self, forKey: .build)
        osVersion = try c.decodeIfPresent(String.self, forKey: .osVersion)
        device = try c.decodeIfPresent(String.self, forKey: .device)
        type = try c.decode(String.self, forKey: .type)
        reason = try c.decodeIfPresent(String.self, forKey: .reason) ?? ""
        frames = try c.decodeIfPresent([Frame].self, forKey: .frames) ?? []
        images = try c.decodeIfPresent([String: String].self, forKey: .images) ?? [:]
    }
}

/// The device's model and this install's id (what the home's limit counts, never a person).
public enum DeviceInfo {
    /// "iPhone17,1", "AppleTV14,1", "Mac16,10" (a simulator says the model it plays).
    public static var model: String {
        if let sim = ProcessInfo.processInfo.environment["SIMULATOR_MODEL_IDENTIFIER"] { return sim }
        #if os(macOS)
        var size = 0
        sysctlbyname("hw.model", nil, &size, nil, 0)
        var buf = [CChar](repeating: 0, count: max(size, 1))
        sysctlbyname("hw.model", &buf, &size, nil, 0)
        return String(cString: buf)
        #else
        var u = utsname()
        uname(&u)
        return withUnsafeBytes(of: &u.machine) { String(decoding: $0.prefix { $0 != 0 }, as: UTF8.self) }
        #endif
    }

    /// The app's executable name (its frames are "in app").
    public static var executable: String { Bundle.main.infoDictionary?["CFBundleExecutable"] as? String ?? ProcessInfo.processInfo.processName }

    static let installKey = "maktaba.installID"

    /// A random id made once per install, kept in the defaults.
    public static func installID(_ defaults: UserDefaults = .standard) -> String {
        if let id = defaults.string(forKey: installKey) { return id }
        let id = UUID().uuidString.lowercased()
        defaults.set(id, forKey: installKey)
        return id
    }
}
