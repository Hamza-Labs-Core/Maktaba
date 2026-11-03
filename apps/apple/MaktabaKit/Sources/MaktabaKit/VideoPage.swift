import Foundation
import Observation

/// A video's page as it loads (FR-18): the video (`GET /api/videos/{id}`, with the caller's
/// `playback_state`), the caller's own marks (`GET /api/me/videos/{id}`: favorite, watched, the
/// series), its chapters, a transcript excerpt and, in a series, the next episodes. Only the
/// video itself is required: a 404 (or the 403 of a library this person has no access to) is
/// `notAvailable`, since the home does not tell a hidden video from a missing one; the rest is
/// left out when it fails. As Android's `VideoPage` (48.28). The Player's `VideoScreen`
/// shows it on iPhone, iPad and Mac.
@MainActor @Observable
public final class VideoPage {
    /// How many lines of the transcript the page shows.
    public static let transcriptLines = 5
    /// How many of a series' next episodes the page offers.
    public static let nextEpisodeCount = 10

    public let id: String
    private let client: APIClient

    public private(set) var video: VideoInfo?
    public private(set) var series: SeriesRef?
    /// This video as its series lists it (its season and episode).
    public private(set) var episode: Episode?
    public private(set) var chapters: [Chapter] = []
    public private(set) var transcript: [TranscriptSegment] = []
    public private(set) var nextEpisodes: [Episode] = []
    public private(set) var favorite = false
    public private(set) var watched = false
    public private(set) var notAvailable = false
    public private(set) var problem: HomeFailure?
    /// A mark the home did not take (the toggle went back).
    public var saveFailed = false
    /// Marked watched or not since the last load: the home starts the video over either way.
    private var startsOver = false
    /// How many times Favorite and Mark watched were toggled. A read again that began before a
    /// toggle was answered before its save, so it keeps what the person chose (else the old
    /// mark comes back on screen while the home has the new one). As Android's `VideoPage`.
    private var favoriteEdits = 0
    private var watchedEdits = 0

    public init(client: APIClient, id: String) {
        self.client = client
        self.id = id
    }

    /// Where Resume opens, in seconds: the saved position, unless watched or not started.
    public var resumeAt: Double? { watched || startsOver ? nil : video?.resumeAt }

    public func load() async {
        problem = nil
        let favoriteSeen = favoriteEdits
        let watchedSeen = watchedEdits
        async let detail = client.videoDetail(id)
        async let mine = optional { try await self.client.myVideo(self.id) }
        async let chapterList = optional { try await self.client.chapters(video: self.id) }
        async let lines = optional { try await self.client.transcript(video: self.id, limit: Self.transcriptLines) }
        do {
            let v = try await detail
            let m = await mine
            video = v
            notAvailable = false
            if favoriteEdits == favoriteSeen { favorite = m?.favorite ?? false }
            // a Mark watched since the read began keeps starting the video over (Play, not Resume)
            if watchedEdits == watchedSeen {
                startsOver = false
                watched = m?.completed ?? (v.playbackState?.completed ?? false)
            }
            series = m?.series
            chapters = (await chapterList ?? []).sorted { $0.startSec < $1.startSec }
            transcript = Array((await lines)?.segments.prefix(Self.transcriptLines) ?? [])
            if let s = m?.series, let all = await optional({ try await self.client.episodes(series: s.id) }) {
                let ordered = Self.ordered(all.episodes)
                let at = ordered.firstIndex { $0.videoID == id }
                episode = at.map { ordered[$0] }
                nextEpisodes = at.map { Array(ordered.dropFirst($0 + 1).prefix(Self.nextEpisodeCount)) } ?? []
            } else {
                episode = nil
                nextEpisodes = []
            }
        } catch {
            if Task.isCancelled || error is CancellationError { return }
            if (error as? BrowseError) == .notAvailable {
                notAvailable = true
            } else if video == nil {
                problem = HomeFailure(error)
            }
        }
    }

    /// Favorite on or off (`POST /api/me/favorites`); the toggle goes back when the home does
    /// not take it.
    public func toggleFavorite() async {
        let want = !favorite
        favoriteEdits += 1
        favorite = want
        do {
            try await client.markFavorite([id], want)
        } catch {
            favorite = !want
            saveFailed = true
        }
    }

    /// Watched on or off (`POST /api/me/watched`); the home starts the video over either way
    /// (position 0), so Resume goes.
    public func toggleWatched() async {
        let want = !watched
        let before = startsOver
        watchedEdits += 1
        watched = want
        startsOver = true
        do {
            try await client.markWatched([id], want)
        } catch {
            watched = !want
            startsOver = before
            saveFailed = true
        }
    }

    /// A series' episodes by season, then episode (or absolute number), as they are watched.
    public static func ordered(_ episodes: [Episode]) -> [Episode] {
        episodes.enumerated().sorted { a, b in
            let x = (a.element.season ?? 0, a.element.episode ?? a.element.absoluteNumber ?? Int.max, a.offset)
            let y = (b.element.season ?? 0, b.element.episode ?? b.element.absoluteNumber ?? Int.max, b.offset)
            return x < y
        }.map(\.element)
    }

    private nonisolated func optional<T: Sendable>(_ work: @Sendable () async throws -> T) async -> T? {
        try? await work()
    }
}
