import Foundation

/// A screen pushed on a section's stack (Epic 48's navigation map). Each case's view lives
/// in its screen's folder and belongs to that screen's story: `Video/VideoScreen.swift`,
/// `Libraries/SeriesScreen.swift`, `Libraries/LibraryScreen.swift`, `Live/ChannelScreen.swift`,
/// `Watch/WatchScreen.swift`.
enum PlayerRoute: Hashable, Codable, Sendable {
    /// A video's page.
    case video(id: String)
    /// A series' page.
    case series(id: String)
    /// A library's page.
    case library(id: String)
    /// A channel (Live TV).
    case channel(id: String)
    /// The player, at a time in seconds (nil: where the person left off).
    case watch(videoID: String, at: Double? = nil)

    /// The route as a short path, as a deep link's path would carry it:
    /// `video/v1`, `series/s1`, `library/l1`, `channel/c1`, `watch/v1` or `watch/v1@90`.
    var path: String {
        switch self {
        case let .video(id): "video/\(id)"
        case let .series(id): "series/\(id)"
        case let .library(id): "library/\(id)"
        case let .channel(id): "channel/\(id)"
        case let .watch(id, at?): "watch/\(id)@\(at.formatted(.number.grouping(.never).locale(Locale(identifier: "en_US_POSIX"))))"
        case let .watch(id, nil): "watch/\(id)"
        }
    }

    init?(path: String) {
        let parts = path.split(separator: "/", maxSplits: 1).map(String.init)
        guard parts.count == 2, !parts[1].isEmpty, !parts[1].contains("/") else { return nil }
        let id = parts[1]
        switch parts[0] {
        case "video": self = .video(id: id)
        case "series": self = .series(id: id)
        case "library": self = .library(id: id)
        case "channel": self = .channel(id: id)
        case "watch":
            let w = id.split(separator: "@", maxSplits: 1, omittingEmptySubsequences: false).map(String.init)
            guard let video = w.first, !video.isEmpty else { return nil }
            if w.count == 2 {
                guard let at = Double(w[1]), at >= 0 else { return nil }
                self = .watch(videoID: video, at: at)
            } else {
                self = .watch(videoID: video)
            }
        default: return nil
        }
    }
}
