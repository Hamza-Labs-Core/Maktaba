import Foundation
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

/// Report a problem (Epic 53 story 53.15): what the person sends to the home's admin from
/// Settings → Help (and an error screen's "Report this"). The form shows what will be sent
/// before it is: the app and device, the app's log for the last 30 minutes (at most 2,000
/// lines, already scrubbed, readable in the preview), the last 20 request ids, this app's
/// kept crashes (a tick box, on), and a screenshot of the screen before the form only when
/// the person ticks it and sees it (off by default; the home refuses one sent without
/// `screenshot_previewed`). The home adds its own side and answers the report's number.
public struct ReportDraft: Sendable, Equatable {
    public var category: ReportCategory = .other
    public var text = ""
    /// nil: just now; else a time earlier today (the home then takes its lines around it)
    public var earlier: Date?
    public var includeCrashes = true
    public var includeScreenshot = false
    /// The error screen's reference this was opened from (its request id, or its first 8).
    public var reference: String?

    public init(category: ReportCategory = .other, text: String = "", reference: String? = nil) {
        self.category = category
        self.text = text
        self.reference = reference
    }
}

public enum ReportProblem {
    public static let textMax = 2000
    public static let linesMax = 2000
    public static let window: TimeInterval = 30 * 60
    /// The home's limits: the app's part and the screenshot.
    public static let partMax = 512 << 10
    public static let screenshotMax = 1 << 20

    /// The log lines a report takes: the ring's last 30 minutes, at most 2,000, scrubbed again.
    public static func lines(_ all: [String], now: Date = Date()) -> [String] {
        let since = now.addingTimeInterval(-window)
        let recent = all.filter { line in
            guard let r = line.range(of: "\"ts\":\""), let end = line[r.upperBound...].firstIndex(of: "\"") else { return false }
            return homeTime(String(line[r.upperBound..<end])).map { $0 >= since && $0 <= now.addingTimeInterval(60) } ?? false
        }
        return recent.suffix(linesMax).map(Redact.scrub)
    }

    /// The app's part (`report`, JSON), as the home's `reportIn` reads it. Lines are dropped
    /// from the oldest until it fits in 512 KB.
    public static func part(_ d: ReportDraft, lines: [String], crashes: [[String: Any]], header: Envelope.Header,
                            requests: [String], screenshotPreviewed: Bool) -> Data {
        var reqs = requests
        if let ref = d.reference, !reqs.contains(where: { $0.hasPrefix(ref) }) { reqs.append(ref) }
        var body: [String: Any] = [
            "category": d.category.rawValue,
            "text": Redact.scrub(String(d.text.prefix(textMax))),
            "app": header.app, "version": header.appVersion, "platform": header.platform,
            "os_version": header.osVersion, "device_model": header.deviceModel, "install_id": header.installID,
            "request_ids": Array(reqs.suffix(20)),
            "screenshot_previewed": screenshotPreviewed,
            "share_with_maktaba": false,
        ]
        if let e = d.earlier { body["when"] = wholeSeconds.string(from: e) }
        if d.includeCrashes, !crashes.isEmpty { body["crashes"] = crashes }
        var objects = lines.compactMap { l in (try? JSONSerialization.jsonObject(with: Data(l.utf8))) }
        while true {
            body["lines"] = objects
            let data = Envelope.json(body)
            if data.count <= partMax || objects.isEmpty { return data }
            objects.removeFirst(max(1, objects.count / 10))
        }
    }

    /// The multipart body: the part, and the screenshot when it is sent.
    public static func multipart(part: Data, screenshot: Data?, boundary: String) -> Data {
        var out = Data()
        func add(_ s: String) { out.append(Data(s.utf8)) }
        add("--\(boundary)\r\nContent-Disposition: form-data; name=\"report\"; filename=\"report.json\"\r\nContent-Type: application/json\r\n\r\n")
        out.append(part)
        add("\r\n")
        if let screenshot {
            add("--\(boundary)\r\nContent-Disposition: form-data; name=\"screenshot\"; filename=\"screenshot.jpg\"\r\nContent-Type: image/jpeg\r\n\r\n")
            out.append(screenshot)
            add("\r\n")
        }
        add("--\(boundary)--\r\n")
        return out
    }

    /// Sends a report: the screenshot only when the person ticked it (and so saw it).
    public static func send(_ d: ReportDraft, screenshot: Data?, client: APIClient,
                            lines: [String] = LogRing.shared?.lines() ?? [],
                            crashes: [[String: Any]] = CrashOutbox.shared?.events() ?? [],
                            header: Envelope.Header = .current(), now: Date = Date()) async throws -> Int {
        let shot = d.includeScreenshot ? screenshot : nil
        let part = part(d, lines: Self.lines(lines, now: now), crashes: crashes, header: header,
                        requests: RequestID.recent(), screenshotPreviewed: shot != nil)
        let boundary = "maktaba-" + UUID().uuidString
        return try await client.sendProblemReport(multipart(part: part, screenshot: shot, boundary: boundary), boundary: boundary)
    }

    nonisolated(unsafe) private static let wholeSeconds: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()
}

/// A picture of the app's window, taken before the form opens (JPEG, under 1 MB). Nothing is
/// sent unless the person ticks it in and sees it.
public enum ScreenCapture {
    @MainActor public static func window() -> Data? {
        #if canImport(UIKit)
        guard let scene = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene })
            .first(where: { $0.activationState == .foregroundActive }) ?? UIApplication.shared.connectedScenes.first as? UIWindowScene,
            let window = scene.windows.first(where: \.isKeyWindow) ?? scene.windows.first else { return nil }
        let image = UIGraphicsImageRenderer(bounds: window.bounds).image { _ in
            window.drawHierarchy(in: window.bounds, afterScreenUpdates: false)
        }
        return fit { image.jpegData(compressionQuality: $0) }
        #elseif canImport(AppKit)
        guard let view = NSApp.keyWindow?.contentView ?? NSApp.windows.first?.contentView,
              let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return nil }
        view.cacheDisplay(in: view.bounds, to: rep)
        return fit { rep.representation(using: .jpeg, properties: [.compressionFactor: $0]) }
        #else
        return nil
        #endif
    }

    /// The best quality under the home's 1 MB.
    static func fit(_ encode: (CGFloat) -> Data?) -> Data? {
        for q in [0.8, 0.6, 0.4, 0.2] as [CGFloat] {
            if let d = encode(q), d.count <= ReportProblem.screenshotMax { return d }
        }
        return nil
    }
}
