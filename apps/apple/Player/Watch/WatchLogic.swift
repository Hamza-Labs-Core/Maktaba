import Foundation

/// The player's decisions that need no player (story 48.23), so the UI-test targets compile
/// this file and test them alone (see project.yml): where a video opens, when Up next shows,
/// which episode follows, which qualities are offered, how long the controls stay.
enum WatchLogic {
    /// The controls hide this long after the last touch while the video plays.
    static let controlsHideAfter: Duration = .seconds(4)
    /// A double-tap moves this many seconds.
    static let doubleTapSeconds: Double = 10
    /// Up next shows in an episode's last seconds, and counts down this many.
    static let upNextSeconds = 10

    /// Where a video opens: the time asked for (`watch/v1@90`), else where the person left
    /// off, else the start. A finished video starts again.
    static func start(asked: Double?, resume position: Double?, completed: Bool) -> Double {
        if let asked { return max(0, asked) }
        guard let position, !completed, position > 0 else { return 0 }
        return position
    }

    /// What a double-tap on a side does: the start side (left in English, right in Arabic,
    /// as the layout mirrors) goes back, the end side forward.
    static func doubleTapSeek(onStartSide: Bool) -> Double {
        onStartSide ? -doubleTapSeconds : doubleTapSeconds
    }

    /// Up next is due in the last `upNextSeconds` of a video, or at its end.
    static func upNextDue(position: Double, duration: Double, ended: Bool) -> Bool {
        if ended { return true }
        guard position.isFinite, duration.isFinite, duration > Double(upNextSeconds) else { return false }
        return duration - position <= Double(upNextSeconds)
    }

    /// An episode as far as ordering goes.
    struct EpisodeKey: Equatable {
        let id: String
        let season: Int?
        let episode: Int?
        let absolute: Int?
    }

    /// The episode after `video`, in the seasons' order (season, then episode or absolute
    /// number; the home's own order for the rest), nil after the last or when it is not one.
    static func next(after video: String, in episodes: [EpisodeKey]) -> String? {
        let ordered = episodes.enumerated().sorted { a, b in
            let l = (a.element.season ?? 0, a.element.episode ?? a.element.absolute ?? Int.max, a.offset)
            let r = (b.element.season ?? 0, b.element.episode ?? b.element.absolute ?? Int.max, b.offset)
            return l < r
        }.map(\.element.id)
        guard let i = ordered.firstIndex(of: video), i + 1 < ordered.count else { return nil }
        return ordered[i + 1]
    }

    /// A quality to offer: its height and the cap it sends (`max_bitrate_kbps`).
    struct Rung: Hashable, Identifiable {
        let height: Int
        let kbps: Int
        var id: Int { kbps }
    }

    /// The streaming service's own rungs, offered when a session names none (direct play),
    /// so a person can still ask for less.
    static let defaultRungs = [Rung(height: 1080, kbps: 6000), Rung(height: 720, kbps: 3000), Rung(height: 480, kbps: 1200)]

    /// The qualities offered besides Auto: the session's ladder, highest first, else
    /// `defaultRungs`.
    static func qualities(_ ladder: [Rung]) -> [Rung] {
        let usable = Set(ladder.filter { $0.kbps > 0 && $0.height > 0 })
        return usable.isEmpty ? defaultRungs : usable.sorted { ($0.height, $0.kbps) > ($1.height, $1.kbps) }
    }
}
