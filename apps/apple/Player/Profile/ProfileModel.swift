import MaktabaKit
import Observation
import SwiftUI

/// A list a Profile page shows: loading, its rows, or why it could not be read.
enum Load<Value> {
    case loading
    case ready(Value)
    case failed(HomeFailure)

    var value: Value? {
        if case let .ready(v) = self { return v }
        return nil
    }
}

/// The Profile's lists and calls (FR-25, FR-26), kept for the signed-in home: leaving the
/// section and coming back finds them as they were. A 401 the client could not refresh goes
/// back to signing in (`AppSession.signedOut`).
@MainActor @Observable
final class ProfileModel {
    static let page = 50

    @ObservationIgnored let client: APIClient
    @ObservationIgnored private let signedOut: () -> Void

    var profile: Profile?
    var history: Load<[HistoryEntry]> = .loading
    /// The last page of history was full: there may be more.
    var historyMore = false
    var favorites: Load<[BrowseVideo]> = .loading
    private var favoritesNext: String?
    var favoritesMore: Bool { favoritesNext != nil }
    var playlists: Load<[PlaylistSummary]> = .loading
    var playlist: Load<Playlist> = .loading
    var sessions: Load<[SignInSession]> = .loading
    var cloud: CloudLinkState?
    var homeVersion: String?
    /// A change that did not go through (a removal, signing a session out), until shown.
    var problem: HomeFailure?

    init(client: APIClient, signedOut: @escaping () -> Void) {
        self.client = client
        self.signedOut = signedOut
    }

    /// The header and About: who, the cloud link, the home's version. Quiet on failure.
    func loadRoot() async {
        if profile == nil { profile = try? await client.profile() }
        if cloud == nil { cloud = try? await client.cloudLink() }
        if homeVersion == nil, case let .success(home) = await HomeProbe().check(client.home) {
            homeVersion = home.version
        }
    }

    func loadHistory() async {
        await load(\.history) {
            let items = try await self.client.watchHistory(limit: Self.page)
            self.historyMore = items.count == Self.page
            return items
        }
    }

    func moreHistory() async {
        guard let shown = history.value,
              let more = await quietly({ try await self.client.watchHistory(offset: shown.count, limit: Self.page) }) else { return }
        historyMore = more.count == Self.page
        let ids = Set(shown.map(\.id))
        history = .ready(shown + more.filter { !ids.contains($0.id) })
    }

    /// Takes `entry` off the list at once; puts it back if the home refuses.
    func forget(_ entry: HistoryEntry) async {
        guard let before = history.value else { return }
        history = .ready(before.filter { $0.id != entry.id })
        if await quietly({ try await self.client.forgetHistory(video: entry.videoID) }) == nil {
            history = .ready(before)
        }
    }

    func loadFavorites() async {
        await load(\.favorites) {
            let page = try await self.client.videos(VideoQuery(favorite: true))
            self.favoritesNext = page.next
            return page.items
        }
    }

    func moreFavorites() async {
        guard let shown = favorites.value, let cursor = favoritesNext,
              let page = await quietly({ try await self.client.videos(VideoQuery(favorite: true, cursor: cursor)) }) else { return }
        favoritesNext = page.next
        favorites = .ready(shown + page.items)
    }

    func loadPlaylists() async {
        await load(\.playlists) { try await self.client.playlists() }
    }

    func loadPlaylist(_ id: String) async {
        playlist = .loading
        await load(\.playlist) { try await self.client.playlist(id) }
    }

    func loadSessions() async {
        await load(\.sessions) { try await self.client.signInSessions() }
    }

    /// Signs `session` out; it leaves the list once the home has done it.
    func end(_ session: SignInSession) async {
        guard await quietly({ try await self.client.endSignInSession(session.id) }) != nil,
              let now = sessions.value else { return }
        sessions = .ready(now.filter { $0.id != session.id })
    }

    /// Fills `list` with `call`'s answer, or why it failed. A call cancelled (the page left
    /// before it answered) leaves the list as it is: the page asks again when it shows.
    private func load<T>(_ list: ReferenceWritableKeyPath<ProfileModel, Load<T>>, _ call: @escaping () async throws -> T) async {
        do {
            self[keyPath: list] = .ready(try await call())
        } catch {
            if case APIError.signedOut = error { signedOut() }
            guard !Log.isCancellation(error) else { return }
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
