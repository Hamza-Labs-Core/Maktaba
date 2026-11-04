import Foundation
import Observation

/// A list a Profile page shows: loading, its rows, or why it could not be read.
public enum Load<Value> {
    case loading
    case ready(Value)
    case failed(HomeFailure)

    public var value: Value? {
        if case let .ready(v) = self { return v }
        return nil
    }
}

/// The Profile's lists and calls (FR-25, FR-26), kept for the signed-in home: leaving the
/// section and coming back finds them as they were. A 401 the client could not refresh goes
/// back to signing in (`AppSession.signedOut`). The Player's Profile shows it on iPhone, iPad
/// and Mac.
@MainActor @Observable
public final class ProfileModel {
    public static let page = 50

    @ObservationIgnored public let client: APIClient
    @ObservationIgnored private let signedOut: () -> Void

    public private(set) var profile: Profile?
    public private(set) var history: Load<[HistoryEntry]> = .loading
    /// The last page of history was full: there may be more.
    public private(set) var historyMore = false
    public private(set) var favorites: Load<[BrowseVideo]> = .loading
    private var favoritesNext: String?
    public var favoritesMore: Bool { favoritesNext != nil }
    public private(set) var playlists: Load<[PlaylistSummary]> = .loading
    public private(set) var playlist: Load<Playlist> = .loading
    public private(set) var sessions: Load<[SignInSession]> = .loading
    public private(set) var cloud: CloudLinkState?
    public private(set) var homeVersion: String?
    /// A change that did not go through (a removal, signing a session out), until shown.
    public var problem: HomeFailure?
    /// The history's and the sessions' reads against their removals: a read that began
    /// before a removal was saved still has the row, so it is dropped (else the row comes
    /// back on screen while the home has let it go).
    @ObservationIgnored private let historyOrder = ReadOrder()
    @ObservationIgnored private let sessionsOrder = ReadOrder()

    public init(client: APIClient, signedOut: @escaping () -> Void) {
        self.client = client
        self.signedOut = signedOut
    }

    /// The header and About: who, the cloud link, the home's version. Quiet on failure.
    public func loadRoot() async {
        if profile == nil { profile = try? await client.profile() }
        if cloud == nil { cloud = try? await client.cloudLink() }
        if homeVersion == nil, case let .success(home) = await HomeProbe().check(client.home) {
            homeVersion = home.version
        }
    }

    public func loadHistory() async {
        await load(\.history, order: historyOrder) {
            try await self.client.watchHistory(limit: Self.page)
        } then: { items in
            self.historyMore = items.count == Self.page
        }
    }

    /// The next page, after the rows shown when it is answered (not those shown when it was
    /// asked for: one may have been forgotten since).
    public func moreHistory() async {
        guard let asked = history.value else { return }
        let ticket = historyOrder.begin()
        guard let more = await quietly({ try await self.client.watchHistory(offset: asked.count, limit: Self.page) }),
              historyOrder.fresh(ticket), let shown = history.value else { return }
        historyMore = more.count == Self.page
        let ids = Set(shown.map(\.id))
        history = .ready(shown + more.filter { !ids.contains($0.id) })
    }

    /// Takes `entry` off the list at once; puts it back if the home refuses.
    public func forget(_ entry: HistoryEntry) async {
        guard let before = history.value, let at = before.firstIndex(where: { $0.id == entry.id }) else { return }
        history = .ready(before.filter { $0.id != entry.id })
        let done = await historyOrder.saving {
            await self.quietly({ try await self.client.forgetHistory(video: entry.videoID) }) != nil
        }
        guard !done, let now = history.value, !now.contains(where: { $0.id == entry.id }) else { return }
        // back in its place, among the rows shown now
        var back = now
        back.insert(entry, at: min(at, back.count))
        history = .ready(back)
    }

    public func loadFavorites() async {
        await load(\.favorites) {
            let page = try await self.client.videos(VideoQuery(favorite: true))
            self.favoritesNext = page.next
            return page.items
        }
    }

    public func moreFavorites() async {
        guard let shown = favorites.value, let cursor = favoritesNext,
              let page = await quietly({ try await self.client.videos(VideoQuery(favorite: true, cursor: cursor)) }) else { return }
        favoritesNext = page.next
        favorites = .ready(shown + page.items)
    }

    public func loadPlaylists() async {
        await load(\.playlists) { try await self.client.playlists() }
    }

    public func loadPlaylist(_ id: String) async {
        playlist = .loading
        await load(\.playlist) { try await self.client.playlist(id) }
    }

    public func loadSessions() async {
        await load(\.sessions, order: sessionsOrder) { try await self.client.signInSessions() }
    }

    /// Signs `session` out; it leaves the list once the home has done it.
    public func end(_ session: SignInSession) async {
        let done = await sessionsOrder.saving {
            await self.quietly({ try await self.client.endSignInSession(session.id) }) != nil
        }
        guard done, let now = sessions.value else { return }
        sessions = .ready(now.filter { $0.id != session.id })
    }

    /// Fills `list` with `call`'s answer, or why it failed; `then` runs with an answer applied.
    /// A call cancelled (the page left before it answered) leaves the list as it is: the page
    /// asks again when it shows. An answer `order` says is older than a change is dropped.
    private func load<T>(_ list: ReferenceWritableKeyPath<ProfileModel, Load<T>>, order: ReadOrder = ReadOrder(),
                         _ call: @escaping () async throws -> T, then: (T) -> Void = { _ in }) async {
        let ticket = order.begin()
        do {
            let value = try await call()
            guard order.fresh(ticket) else { return }
            self[keyPath: list] = .ready(value)
            then(value)
        } catch {
            if case APIError.signedOut = error { signedOut() }
            guard !Log.isCancellation(error), order.fresh(ticket) else { return }
            Log.failure("profile load", error)
            self[keyPath: list] = .failed(HomeFailure(error))
        }
    }

    /// `call`'s answer, or nil with `problem` set.
    private func quietly<T>(_ call: @escaping () async throws -> T) async -> T? {
        do {
            return try await call()
        } catch {
            if case APIError.signedOut = error {
                signedOut()
                return nil
            }
            guard !Log.isCancellation(error) else { return nil }
            Log.failure("profile change", error)
            problem = HomeFailure(error)
            return nil
        }
    }
}
