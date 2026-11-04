import CryptoKit
import Foundation
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
public final class HomeModel {
    public private(set) var hero: HomeHero?
    public private(set) var shelves: [HomeShelf] = []
    /// Home answered at least once (here, or kept from an earlier launch).
    public private(set) var loaded = false
    /// Videos read for recommendation cards (`GET /api/videos/{id}`), by id.
    public private(set) var known: [String: KnownVideo] = [:]
    /// Why Home could not load, when there is nothing to show instead.
    public private(set) var problem: HomeFailure?
    /// A refresh failed: the rows shown are the last ones.
    public private(set) var stale = false

    private let client: APIClient
    private let cache: HomeCache?
    private let signedOut: () -> Void
    private var restored = false
    @ObservationIgnored private var asking = Set<String>()
    /// Home's reads against Not interested: a read that began before one was saved still has
    /// the card, so it is dropped (else the card comes back while the home has let it go).
    @ObservationIgnored private let order = ReadOrder()

    public init(client: APIClient, cache: HomeCache?, signedOut: @escaping () -> Void) {
        self.client = client
        self.cache = cache
        self.signedOut = signedOut
    }

    /// Each time Home shows: what is kept first (from disk on a new launch), then a fresh read.
    public func open() async {
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
    public func load() async {
        let client = client
        let ticket = order.begin()
        let answers = await HomeShelves.read(
            home: { try await client.home() },
            rails: { try await client.homeRails(surface: "mobile-home") },
            now: { try await client.channelsNow() })
        if Task.isCancelled || !order.fresh(ticket) { return }
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
    public func resolve(_ videoID: String) async {
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
    public func dismiss(_ videoID: String) async -> Bool {
        let before = shelves
        shelves = HomeShelves.dropping(videoID, from: shelves)
        do {
            try await order.saving { try await client.dismissRecommendation(video: videoID) }
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

/// Home's hero (FR-10): the featured series (from its first lesson) or the video an admin picked.
public struct HomeHero: Codable, Hashable, Sendable {
    public let videoID: String
    public let videoTitle: String
    public let seriesID: String?
    public let seriesName: String?
    public let episodes: Int?
    public let durationSec: Double?
    public let picked: Bool
    public let progress: Double?

    /// A series' hero opens the series; a video's opens its page.
    public var isSeries: Bool { seriesID != nil }


    public init(_ f: Featured) {
        videoID = f.video.id
        videoTitle = f.video.displayTitle
        let series = f.kind == "series" ? f.series : nil
        seriesID = series?.id
        seriesName = (series?.name).flatMap { $0.trimmingCharacters(in: .whitespaces).isEmpty ? nil : $0 }
        episodes = series?.episodes
        durationSec = f.video.durationSec
        picked = f.picked
        progress = f.video.progress > 0 ? f.video.progress : nil
    }
}

/// What a video is called, for a recommendation's card.
public struct KnownVideo: Codable, Hashable, Sendable {
    public let title: String
    public let series: String?

    public init(title: String, series: String?) {
        self.title = title
        self.series = series
    }
}

/// A shelf of cards as the device keeps it (Live TV on now and retry tiles are not kept: one
/// is stale within minutes, the other is no row).
public struct KeptShelf: Codable, Hashable, Sendable {
    public let key: String
    public let kind: HomeShelf.Kind
    public let cards: [HomeShelfCard]

    public init?(_ shelf: HomeShelf) {
        guard case let .cards(cards) = shelf.content else { return nil }
        key = shelf.key
        kind = shelf.kind
        self.cards = cards
    }

    public var shelf: HomeShelf { HomeShelf(key: key, kind: kind, content: .cards(cards)) }
}

/// Home's last answer as kept on the device: the hero, the rows of cards and the videos read
/// for their cards. Live TV on now is not kept: it is stale within minutes.
public struct CachedHome: Codable, Sendable {
    public var hero: HomeHero?
    public var shelves: [KeptShelf]
    public var known: [String: KnownVideo]

    public init(hero: HomeHero?, shelves: [KeptShelf], known: [String: KnownVideo]) {
        self.hero = hero
        self.shelves = shelves
        self.known = known
    }
}


/// Home's last answer on disk (FR-12: the next launch shows it at once and refreshes behind
/// it), one file per home and person under the app's Caches folder (the system may clear it;
/// a file that cannot be read is no cache). An actor, so one save at a time.
public actor HomeCache {
    /// The folder under Caches (the shelves' shape since the TV's `HomeShelves`; the older
    /// `home-rows` files are not read).
    public static let folder = "home-shelves"

    public let file: URL

    public init(file: URL) {
        self.file = file
    }

    /// The cache for `user` on `home`: the file is named by a hash, so no address or id is
    /// readable in it.
    public static func of(home: URL, user: String) -> HomeCache? {
        guard let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first else { return nil }
        let digest = SHA256.hash(data: Data("\(home.absoluteString)|\(user)".utf8))
        let name = digest.prefix(16).map { String(format: "%02x", $0) }.joined()
        return HomeCache(file: caches.appending(path: folder).appending(path: "\(name).json"))
    }

    public func read() -> CachedHome? {
        guard let data = try? Data(contentsOf: file) else { return nil }
        return try? JSONDecoder().decode(CachedHome.self, from: data)
    }

    /// Writes `home` whole or not at all.
    public func write(_ home: CachedHome) {
        do {
            try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
            try JSONEncoder().encode(home).write(to: file, options: .atomic)
        } catch {
            Log.failure("player home cache", error)
        }
    }
}
