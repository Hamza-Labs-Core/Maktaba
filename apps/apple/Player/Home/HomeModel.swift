import CryptoKit
import Foundation
import MaktabaKit
import Observation

/// Home's data (FR-10 to FR-13), kept by the Home screen (the tab keeps it while another
/// section shows) and on disk (`HomeCache`) for the next launch:
/// - `open` shows what is kept at once and reads Home again behind it (FR-12);
/// - the rows are MaktabaKit's `HomeShelves`, built from `GET /api/home`, the
///   recommendations and Live TV on now as the Apple TV builds them: a source that cannot be
///   read keeps the rows it showed, or becomes a retry tile where they would be;
/// - Home's own answer failing is the exception: with rows shown it keeps them and says so
///   (`stale`), with none it is the whole screen's problem (FR-13's look).
///
/// Every route scopes to the person's libraries on the home (NFR-12): nothing here filters by
/// role.
@MainActor @Observable
final class HomeModel {
    private(set) var hero: HomeHero?
    private(set) var shelves: [HomeShelf] = []
    /// Home answered at least once (here, or kept from an earlier launch).
    private(set) var loaded = false
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
    var sections: [HomeSection] { HomeLayout.sections(shelves, known: known) }

    /// Home answered and has nothing at all (FR-13).
    var isEmpty: Bool { loaded && HomeLayout.isEmpty(hero: hero, sections: sections) }

    /// Each time Home shows: what is kept first (from disk on a new launch), then a fresh read.
    func open() async {
        if !restored {
            restored = true
            if !loaded, let kept = await cache?.read() {
                hero = kept.hero
                shelves = kept.shelves.map(\.shelf)
                known = kept.known
                loaded = true
            }
        }
        await load()
    }

    /// Reads Home, its recommendations and Live TV on now together (a retry tile's Try again
    /// too), then builds the rows.
    func load() async {
        let client = client
        let answers = await HomeShelves.read(
            home: { try await client.home() },
            rails: { try await client.homeRails(surface: "mobile-home") },
            now: { try await client.channelsNow() })
        if Task.isCancelled { return }
        let failures: [(StaticString, Error)] = [
            answers.rails.failure.map { ("player home recommendations", $0) },
            answers.now.failure.map { ("player home live now", $0) },
        ].compactMap { $0 }
        switch answers.home {
        case let .success(rows):
            hero = rows.featured.map(HomeHero.init)
            problem = nil
            stale = false
        case let .failure(error):
            if Log.isCancellation(error) { return }
            if Self.isSignedOut(error) { signedOut(); return }
            Log.failure("player home", error)
            guard loaded else {
                problem = HomeFailure(error)
                return
            }
            stale = true
        }
        if failures.contains(where: { Self.isSignedOut($0.1) }) { signedOut(); return }
        for (what, e) in failures where !Log.isCancellation(e) { Log.failure(what, e) }
        shelves = HomeShelves.build(home: answers.home, rails: answers.rails, now: answers.now, keeping: shelves)
        loaded = true
        await save()
    }

    /// Reads `videoID` for a recommendation's card; a video the person may no longer see
    /// leaves the rows.
    func resolve(_ videoID: String) async {
        guard known[videoID] == nil, asking.insert(videoID).inserted else { return }
        defer { asking.remove(videoID) }
        do {
            let v = try await client.videoDetail(videoID)
            known[videoID] = KnownVideo(title: v.displayTitle, series: nil)
            await save()
        } catch BrowseError.notAvailable {
            shelves = HomeShelves.dropping(videoID, from: shelves)
        } catch {
            if Self.isSignedOut(error) { signedOut() } else { Log.failure("player home video", error) }
        }
    }

    /// Not interested (FR-11): the card leaves its rail at once, and the home is told
    /// (`DELETE /api/recommendations/items/{id}`); when it cannot be told, the card comes back.
    /// True when the home took it.
    func dismiss(_ videoID: String) async -> Bool {
        let before = shelves
        shelves = HomeShelves.dropping(videoID, from: shelves)
        do {
            try await client.dismissRecommendation(video: videoID)
            await save()
            return true
        } catch {
            shelves = before
            if Self.isSignedOut(error) { signedOut() } else { Log.failure("player home not interested", error) }
            return false
        }
    }

    private func save() async {
        guard loaded else { return }
        await cache?.write(CachedHome(hero: hero, shelves: shelves.compactMap(KeptShelf.init), known: known))
    }

    private static func isSignedOut(_ e: Error) -> Bool {
        (e as? APIError) == .signedOut
    }
}

private extension Result {
    var failure: Failure? {
        if case let .failure(e) = self { return e }
        return nil
    }
}

/// Home's last answer as kept on the device: the hero, the rows of cards and the videos read
/// for their cards. Live TV on now is not kept: it is stale within minutes.
struct CachedHome: Codable, Sendable {
    var hero: HomeHero?
    var shelves: [KeptShelf]
    var known: [String: KnownVideo]
}


/// Home's last answer on disk (FR-12: the next launch shows it at once and refreshes behind
/// it), one file per home and person under the app's Caches folder (the system may clear it;
/// a file that cannot be read is no cache). An actor, so one save at a time.
actor HomeCache {
    /// The folder under Caches (the shelves' shape since the TV's `HomeShelves`; the older
    /// `home-rows` files are not read).
    static let folder = "home-shelves"

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
