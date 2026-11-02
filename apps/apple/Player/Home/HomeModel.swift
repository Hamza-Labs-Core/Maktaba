import CryptoKit
import Foundation
import MaktabaKit
import Observation

/// Home's data (FR-10 to FR-13), kept by the Home screen (the tab keeps it while another
/// section shows) and on disk (`HomeCache`) for the next launch:
/// - `open` shows what is kept at once and reads Home again behind it (FR-12);
/// - `GET /api/home` is what Home needs; the recommendations and Live TV on now are extra
///   rows, and a failure of either leaves its rows out (or as they were) without failing Home;
/// - a refresh that fails while rows show keeps them and says so (`stale`).
///
/// Every route scopes to the person's libraries on the home (NFR-12): nothing here filters by
/// role.
@MainActor @Observable
final class HomeModel {
    private(set) var content: HomeContent?
    private(set) var recs: [RecRail] = []
    private(set) var now: [ChannelNow] = []
    /// Videos read for recommendation cards (`GET /api/videos/{id}`), by id.
    private(set) var known: [String: KnownVideo] = [:]
    /// Why Home could not load, when there is nothing to show instead.
    private(set) var problem: HomeFailure?
    /// A refresh failed: the rows shown are the last ones.
    private(set) var stale = false

    private let client: APIClient
    private let cache: HomeCache?
    private let signedOut: () -> Void
    private var restored = false
    @ObservationIgnored private var asking = Set<String>()

    init(client: APIClient, cache: HomeCache?, signedOut: @escaping () -> Void) {
        self.client = client
        self.cache = cache
        self.signedOut = signedOut
    }

    /// The blocks under the hero, in FR-10's order, empty rows left out.
    var sections: [HomeSection] {
        content.map { HomeLayout.sections($0, recs: recs, now: now, known: known) } ?? []
    }

    /// Home answered and has nothing at all (FR-13).
    var isEmpty: Bool {
        content.map { HomeLayout.isEmpty($0, sections: sections) } ?? false
    }

    /// Each time Home shows: what is kept first (from disk on a new launch), then a fresh read.
    func open() async {
        if !restored {
            restored = true
            if content == nil, let kept = await cache?.read() {
                content = kept.content
                recs = kept.recs
                known = kept.known
            }
        }
        await load()
    }

    /// Reads Home, its recommendations and Live TV on now together.
    func load() async {
        let client = client
        async let recsAnswer = Self.attempt { try await client.homeRails(surface: "mobile-home") }
        async let nowAnswer = Self.attempt { try await client.channelsNow() }
        let rows: HomeRows
        do {
            rows = try await client.home()
        } catch {
            _ = await (recsAnswer, nowAnswer)
            if Log.isCancellation(error) { return }
            if Self.isSignedOut(error) { signedOut(); return }
            Log.failure("player home", error)
            if content == nil { problem = HomeFailure(error) } else { stale = true }
            return
        }
        content = HomeContent(rows)
        problem = nil
        stale = false
        switch await recsAnswer {
        case let .success(rails): recs = rails.map(RecRail.init)
        case let .failure(e): extraFailed("player home recommendations", e)
        }
        switch await nowAnswer {
        case let .success(answer): now = answer.channels
        case let .failure(e):
            // a person with no Live TV (or a home without it): no row
            now = []
            extraFailed("player home live now", e)
        }
        await save()
    }

    private func extraFailed(_ what: StaticString, _ e: Error) {
        if Self.isSignedOut(e) { signedOut() } else { Log.failure(what, e) }
    }

    /// Reads `videoID` for a recommendation's card; a video the person may no longer see
    /// leaves the rows.
    func resolve(_ videoID: String) async {
        guard known[videoID] == nil, content?.names[videoID] == nil, asking.insert(videoID).inserted else { return }
        defer { asking.remove(videoID) }
        do {
            let v = try await client.videoDetail(videoID)
            known[videoID] = KnownVideo(title: v.displayTitle, series: nil)
            await save()
        } catch BrowseError.notAvailable {
            drop(videoID)
        } catch {
            if Self.isSignedOut(error) { signedOut() } else { Log.failure("player home video", error) }
        }
    }

    /// Not interested (FR-11): the card leaves its rail at once, and the home is told
    /// (`DELETE /api/recommendations/items/{id}`); when it cannot be told, the card comes back.
    /// True when the home took it.
    func dismiss(_ videoID: String) async -> Bool {
        let before = recs
        drop(videoID)
        do {
            try await client.dismissRecommendation(video: videoID)
            await save()
            return true
        } catch {
            recs = before
            if Self.isSignedOut(error) { signedOut() } else { Log.failure("player home not interested", error) }
            return false
        }
    }

    private func drop(_ videoID: String) {
        recs = recs.map { RecRail(kind: $0.kind, items: $0.items.filter { $0.videoID != videoID }) }
    }

    private func save() async {
        guard let content else { return }
        await cache?.write(CachedHome(content: content, recs: recs, known: known))
    }

    private static func attempt<T: Sendable>(_ call: @Sendable () async throws -> T) async -> Result<T, Error> {
        do { return .success(try await call()) } catch { return .failure(error) }
    }

    private static func isSignedOut(_ e: Error) -> Bool {
        (e as? APIError) == .signedOut
    }
}

/// Home's last answer as kept on the device: its own rows, the recommendations and the videos
/// read for their cards. Live TV on now is not kept: it is stale within minutes.
struct CachedHome: Codable, Sendable {
    var content: HomeContent
    var recs: [RecRail]
    var known: [String: KnownVideo]
}

/// Home's last answer on disk (FR-12: the next launch shows it at once and refreshes behind
/// it), one file per home and person under the app's Caches folder (the system may clear it;
/// a file that cannot be read is no cache). An actor, so one save at a time.
actor HomeCache {
    /// The folder under Caches.
    static let folder = "home-rows"

    let file: URL

    init(file: URL) {
        self.file = file
    }

    /// The cache for `user` on `home`: the file is named by a hash, so no address or id is
    /// readable in it.
    static func of(home: URL, user: String) -> HomeCache? {
        guard let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first else { return nil }
        let digest = SHA256.hash(data: Data("\(home.absoluteString)|\(user)".utf8))
        let name = digest.prefix(16).map { String(format: "%02x", $0) }.joined()
        return HomeCache(file: caches.appending(path: folder).appending(path: "\(name).json"))
    }

    func read() -> CachedHome? {
        guard let data = try? Data(contentsOf: file) else { return nil }
        return try? JSONDecoder().decode(CachedHome.self, from: data)
    }

    /// Writes `home` whole or not at all.
    func write(_ home: CachedHome) {
        do {
            try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
            try JSONEncoder().encode(home).write(to: file, options: .atomic)
        } catch {
            Log.failure("player home cache", error)
        }
    }
}
