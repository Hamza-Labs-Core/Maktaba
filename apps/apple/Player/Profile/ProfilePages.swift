import MaktabaKit
import SwiftUI

// The Profile's pages (FR-25, FR-26, FR-28, FR-31), pushed from `ProfileScreen`. A video
// opens on the section's stack as a `PlayerRoute`, as from any other screen.

// MARK: history

/// `GET /api/me/history`, newest first; each row opens its video, and its × removes it from
/// the history (`DELETE /api/me/history/{id}`).
struct HistoryPage: View {
    let model: ProfileModel

    var body: some View {
        Loaded(model.history, title: Text("Watch history", tableName: "Profile", comment: "The watch history page"),
               retry: model.loadHistory) { items in
            if items.isEmpty {
                Empty(Text("Nothing watched yet", tableName: "Profile", comment: "The history, empty"), systemImage: "clock")
            } else {
                List {
                    ForEach(items) { entry in
                        HistoryRow(entry: entry) { Task { await model.forget(entry) } }
                    }
                    if model.historyMore {
                        MoreButton { await model.moreHistory() }
                    }
                }
                .accessibilityIdentifier("history.list")
            }
        }
        .task { await model.loadHistory() }
    }
}

private struct HistoryRow: View {
    let entry: HistoryEntry
    let remove: () -> Void

    private var title: String {
        entry.title.isEmpty ? String(localized: "Untitled", table: "Profile", comment: "A video with no title") : entry.title
    }

    private var detail: String {
        var parts: [String] = []
        if let at = entry.lastWatchedAt { parts.append(at.formatted(.relative(presentation: .named))) }
        if entry.completed {
            parts.append(String(localized: "Watched", table: "Profile", comment: "History: watched to the end"))
        } else if entry.timesWatched > 1 {
            parts.append(String(localized: "Watched \(entry.timesWatched) times", table: "Profile", comment: "History: how many times"))
        }
        return parts.joined(separator: " · ")
    }

    var body: some View {
        HStack(spacing: MaktabaTokens.Spacing.s3) {
            NavigationLink(value: PlayerRoute.video(id: entry.videoID)) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(verbatim: title)
                        .font(.maktabaBody(16, weight: .medium, relativeTo: .body))
                    if !detail.isEmpty {
                        Text(verbatim: detail)
                            .font(.footnote)
                            .foregroundStyle(Color.mktMuted)
                    }
                    if !entry.completed, entry.progress > 0 {
                        ProgressStrip(value: entry.progress)
                            .frame(maxWidth: 240)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .accessibilityIdentifier("history.\(entry.videoID)")
            Button(action: remove) {
                Image(systemName: "xmark.circle")
                    .font(.title3)
                    .frame(minWidth: 44, minHeight: 44)
            }
            .buttonStyle(.borderless)
            .accessibilityLabel(Text("Remove \(title) from history", tableName: "Profile", comment: "The history row's remove button, spoken"))
            .accessibilityIdentifier("history.remove.\(entry.videoID)")
        }
        .swipeActions {
            Button(role: .destructive, action: remove) {
                Label { Text("Remove", tableName: "Profile", comment: "Removes a video from the history") } icon: {
                    Image(systemName: "trash")
                }
            }
        }
    }
}

// MARK: favorites

/// The person's favorites (`GET /api/videos?favorite=true`), as poster cards.
struct FavoritesPage: View {
    let model: ProfileModel

    var body: some View {
        Loaded(model.favorites, title: Text("Favorites", tableName: "Profile", comment: "The favorites page"),
               retry: model.loadFavorites) { items in
            if items.isEmpty {
                Empty(Text("No favorites yet", tableName: "Profile", comment: "The favorites, empty"), systemImage: "heart")
            } else {
                ScrollView {
                    PosterGrid(videos: items, client: model.client)
                    if model.favoritesMore {
                        MoreButton { await model.moreFavorites() }
                    }
                }
                .accessibilityIdentifier("favorites.grid")
            }
        }
        .task { await model.loadFavorites() }
    }
}

/// Videos as poster cards that wrap, each opening its page.
private struct PosterGrid: View {
    let videos: [BrowseVideo]
    let client: APIClient
    @Environment(\.horizontalSizeClass) private var sizeClass

    var body: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 160), spacing: MaktabaTokens.Spacing.s3, alignment: .top)],
                  spacing: MaktabaTokens.Spacing.s4) {
            ForEach(videos) { v in
                NavigationLink(value: PlayerRoute.video(id: v.id)) {
                    PosterCard(title: v.displayTitle, poster: ProfilePosters.url(client.home, video: v.id, small: sizeClass == .compact),
                               progress: v.progress, width: 160)
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("video.\(v.id)")
            }
        }
        .padding(MaktabaTokens.Spacing.s4)
    }
}

enum ProfilePosters {
    /// `/api/videos/{id}/poster` (`?size=small` on a phone).
    static func url(_ home: URL, video: String, small: Bool) -> URL {
        let u = home.appending(path: "api/videos/\(video)/poster")
        return small ? u.appending(queryItems: [URLQueryItem(name: "size", value: "small")]) : u
    }
}

// MARK: playlists

/// `GET /api/me/playlists`: each opens its videos, with Play from the first.
struct PlaylistsPage: View {
    let model: ProfileModel
    let open: (PlaylistSummary) -> Void

    var body: some View {
        Loaded(model.playlists, title: Text("Playlists", tableName: "Profile", comment: "The playlists page"),
               retry: model.loadPlaylists) { items in
            if items.isEmpty {
                Empty(Text("No playlists yet. Make one on the home's web page.", tableName: "Profile", comment: "The playlists, empty"),
                      systemImage: "music.note.list")
            } else {
                List(items) { p in
                    Button {
                        open(p)
                    } label: {
                        HStack {
                            Label {
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(verbatim: PlaylistWords.name(p))
                                        .font(.maktabaBody(16, weight: .medium, relativeTo: .body))
                                    Text(verbatim: PlaylistWords.countAndLength(p))
                                        .font(.footnote)
                                        .foregroundStyle(Color.mktMuted)
                                }
                            } icon: {
                                Image(systemName: "music.note.list")
                            }
                            Spacer(minLength: 0)
                            Image(systemName: "chevron.forward")
                                .font(.footnote.weight(.semibold))
                                .foregroundStyle(.tertiary)
                                .accessibilityHidden(true)
                        }
                        .contentShape(Rectangle())
                    }
                    .foregroundStyle(Color.mktText)
                    .accessibilityIdentifier("playlist.\(p.id)")
                }
                .accessibilityIdentifier("playlists.list")
            }
        }
        .task { await model.loadPlaylists() }
    }
}

/// A playlist's videos in order (`GET /api/me/playlists/{id}`), with Play from the first.
struct PlaylistPage: View {
    let model: ProfileModel
    let summary: PlaylistSummary

    var body: some View {
        Loaded(model.playlist, title: Text(verbatim: PlaylistWords.name(summary)), retry: { await model.loadPlaylist(summary.id) }) { doc in
            ScrollView {
                VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                    if let d = doc.playlist.description { Text(verbatim: d).foregroundStyle(Color.mktMuted) }
                    Text(verbatim: PlaylistWords.countAndLength(doc.playlist))
                        .font(.footnote)
                        .foregroundStyle(Color.mktMuted)
                    if let first = doc.items.first {
                        NavigationLink(value: PlayerRoute.watch(videoID: first.id, at: 0)) {
                            Label { Text("Play", tableName: "Profile", comment: "Plays a playlist from its first video") } icon: {
                                Image(systemName: "play.fill")
                            }
                        }
                        .buttonStyle(.borderedProminent)
                        .accessibilityIdentifier("playlist.play")
                    } else {
                        Text("This playlist is empty", tableName: "Profile", comment: "A playlist with no videos")
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, MaktabaTokens.Spacing.s4)
                PosterGrid(videos: doc.items, client: model.client)
            }
        }
        .navigationTitle(Text(verbatim: PlaylistWords.name(summary)))
        .task { await model.loadPlaylist(summary.id) }
    }
}

enum PlaylistWords {
    static func name(_ p: PlaylistSummary) -> String {
        p.name.isEmpty ? String(localized: "Untitled", table: "Profile", comment: "A video with no title") : p.name
    }

    static func countAndLength(_ p: PlaylistSummary) -> String {
        let count = String(localized: "\(p.videos) videos", table: "Profile", comment: "A playlist's number of videos")
        guard p.durationSec > 0 else { return count }
        let length = Duration.seconds(p.durationSec).formatted(.units(allowed: [.hours, .minutes], width: .abbreviated))
        return "\(count) · \(length)"
    }
}

// MARK: sessions

/// `GET /api/me/sessions`: the browsers signed in as this person; Sign out ends one
/// (`DELETE /api/me/sessions/{id}`).
struct SessionsPage: View {
    let model: ProfileModel

    var body: some View {
        Loaded(model.sessions, title: Text("Sessions", tableName: "Profile", comment: "The signed-in browsers page"),
               retry: model.loadSessions) { items in
            if items.isEmpty {
                Empty(Text("No browser is signed in as you", tableName: "Profile", comment: "The sessions, empty"),
                      systemImage: "laptopcomputer")
            } else {
                List {
                    Section {
                        ForEach(items) { s in
                            SessionRow(session: s) { Task { await model.end(s) } }
                        }
                    } header: {
                        Text("Browsers signed in to the home as you. Sign out any you do not recognise.", tableName: "Profile",
                             comment: "Above the sessions list")
                            .textCase(nil)
                    }
                }
                .accessibilityIdentifier("sessions.list")
            }
        }
        .task { await model.loadSessions() }
    }
}

private struct SessionRow: View {
    let session: SignInSession
    let signOut: () -> Void

    private var details: String {
        var parts: [String] = []
        if let ip = session.ip { parts.append(ip) }
        if let seen = session.lastSeenAt {
            let ago = seen.formatted(.relative(presentation: .named))
            parts.append(String(localized: "last used \(ago)", table: "Profile", comment: "A session: when it was last used"))
        }
        if session.current {
            parts.append(String(localized: "this browser", table: "Profile", comment: "A session: the one this request came with"))
        }
        return parts.joined(separator: " · ")
    }

    var body: some View {
        HStack(spacing: MaktabaTokens.Spacing.s3) {
            Label {
                VStack(alignment: .leading, spacing: 2) {
                    Text(verbatim: SessionNames.browser(session.userAgent)
                        ?? String(localized: "A browser", table: "Profile", comment: "A session whose browser is not known"))
                    if !details.isEmpty {
                        Text(verbatim: details)
                            .font(.footnote)
                            .foregroundStyle(Color.mktMuted)
                    }
                }
            } icon: {
                Image(systemName: "laptopcomputer.and.iphone")
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("session.\(session.id)")
            Spacer(minLength: 0)
            Button(action: signOut) {
                Text("Sign out", tableName: "Profile", comment: "Signs one session out")
                    .frame(minHeight: 44)
            }
            .buttonStyle(.borderless)
            .accessibilityIdentifier("session.signOut.\(session.id)")
        }
    }
}

enum SessionNames {
    /// A browser's name from its user agent, as a person knows it ("Safari · Mac").
    static func browser(_ userAgent: String?) -> String? {
        guard let ua = userAgent, !ua.trimmingCharacters(in: .whitespaces).isEmpty else { return nil }
        let browser: String? = ua.contains("Edg/") ? "Edge" : ua.contains("Firefox/") ? "Firefox"
            : ua.contains("Chrome/") ? "Chrome" : ua.contains("Safari/") ? "Safari" : ua.contains("Maktaba") ? "Maktaba" : nil
        let os: String? = ua.contains("Android") ? "Android" : ua.contains("iPhone") ? "iPhone" : ua.contains("iPad") ? "iPad"
            : (ua.contains("Mac OS X") || ua.contains("Macintosh")) ? "Mac" : ua.contains("Windows") ? "Windows"
            : ua.contains("Linux") ? "Linux" : nil
        let name = [browser, os].compactMap { $0 }.joined(separator: " · ")
        return name.isEmpty ? String(ua.prefix(40)) : name
    }
}

// MARK: playback

/// FR-31, kept on this device (`PlaybackSettings`); the player reads them.
struct PlaybackPage: View {
    @Bindable private var settings = PlaybackSettings.shared

    var body: some View {
        Form {
            Section {
                Toggle(isOn: $settings.autoplayNext) {
                    Text("Play the next episode automatically", tableName: "Profile", comment: "A playback setting")
                }
                .accessibilityIdentifier("playback.autoplay")
                Toggle(isOn: $settings.resumeWithoutAsking) {
                    Text("Resume without asking", tableName: "Profile", comment: "A playback setting")
                }
                .accessibilityIdentifier("playback.resume")
            }
            Section {
                Toggle(isOn: $settings.subtitlesOn) {
                    Text("Subtitles on by default", tableName: "Profile", comment: "A playback setting")
                }
                .accessibilityIdentifier("playback.subtitles")
                Picker(selection: $settings.subtitleLanguage) {
                    ForEach(SubtitleLanguage.allCases) { Text($0.label).tag($0) }
                } label: {
                    Text("Subtitle language", tableName: "Profile", comment: "A playback setting")
                }
                .accessibilityIdentifier("playback.subtitleLanguage")
                Picker(selection: $settings.subtitleSize) {
                    ForEach(SubtitleSize.allCases) { Text($0.label).tag($0) }
                } label: {
                    Text("Subtitle size", tableName: "Profile", comment: "A playback setting")
                }
                .accessibilityIdentifier("playback.subtitleSize")
            }
            Section {
                Picker(selection: $settings.qualityWifi) {
                    ForEach(StreamQuality.allCases) { Text($0.label).tag($0) }
                } label: {
                    Text("Quality on Wi-Fi", tableName: "Profile", comment: "A playback setting")
                }
                .accessibilityIdentifier("playback.qualityWifi")
                Picker(selection: $settings.qualityMobile) {
                    ForEach(StreamQuality.allCases) { Text($0.label).tag($0) }
                } label: {
                    Text("Quality on mobile data", tableName: "Profile", comment: "A playback setting")
                }
                .accessibilityIdentifier("playback.qualityMobile")
            }
        }
        .maktabaSurface()
        .navigationTitle(Text("Playback", tableName: "Profile", comment: "The playback settings page"))
        .accessibilityIdentifier("screen.playbackSettings")
    }
}

// MARK: diagnostics

/// The crash reports this app kept on this device (`CrashReports`), to share with whoever runs
/// the home. Nothing leaves the device unless the person shares one.
struct DiagnosticsPage: View {
    @State private var reports: [URL] = []

    var body: some View {
        List {
            Section {
                if reports.isEmpty {
                    Text("No problems recorded", tableName: "Profile", comment: "Diagnostics: no crash report kept")
                        .accessibilityIdentifier("diagnostics.none")
                }
                ForEach(reports, id: \.self) { url in
                    ShareLink(item: url) {
                        Label { Text(verbatim: Self.when(url)) } icon: { Image(systemName: "doc.text") }
                    }
                }
            }
            Text("Problems this app recorded on this device, without anything personal. Share one with whoever runs the home if they ask.",
                 tableName: "Profile", comment: "Under the diagnostics list")
                .font(.maktabaBody(13, relativeTo: .footnote))
                .foregroundStyle(Color.mktMuted)
                .fixedSize(horizontal: false, vertical: true)
                .listRowBackground(Color.clear)
        }
        .maktabaSurface()
        .navigationTitle(Text("Diagnostics", tableName: "Profile", comment: "The crash reports kept on this device"))
        .accessibilityIdentifier("screen.diagnostics")
        .task { reports = Self.list() }
    }

    /// The kept reports, newest first.
    static func list() -> [URL] {
        guard let dir = CrashReports.directory,
              let files = try? FileManager.default.contentsOfDirectory(at: dir, includingPropertiesForKeys: nil) else { return [] }
        return files.filter { $0.lastPathComponent.hasPrefix("crash-") && $0.pathExtension == "json" }
            .sorted { $0.lastPathComponent > $1.lastPathComponent }
    }

    /// `crash-<unix time>-….json` as a date.
    static func when(_ url: URL) -> String {
        let parts = url.deletingPathExtension().lastPathComponent.split(separator: "-")
        guard parts.count > 1, let t = TimeInterval(parts[1]) else { return url.lastPathComponent }
        return Date(timeIntervalSince1970: t).formatted(date: .abbreviated, time: .shortened)
    }
}

// MARK: shared pieces

/// A page's list: a spinner while it loads, the error with Try again, or the content.
private struct Loaded<Value, Content: View>: View {
    let load: Load<Value>
    let title: Text
    let retry: () async -> Void
    @ViewBuilder let content: (Value) -> Content

    init(_ load: Load<Value>, title: Text, retry: @escaping () async -> Void, @ViewBuilder content: @escaping (Value) -> Content) {
        self.load = load
        self.title = title
        self.retry = retry
        self.content = content
    }

    var body: some View {
        Group {
            switch load {
            case .loading:
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                    .accessibilityIdentifier("profile.loading")
            case let .failed(failure):
                HomeErrorView(failure, video: false) { _ in Task { await retry() } }
            case let .ready(value):
                content(value)
            }
        }
        .maktabaSurface()
        .navigationTitle(title)
    }
}

private struct Empty: View {
    let text: Text
    let systemImage: String

    init(_ text: Text, systemImage: String) {
        self.text = text
        self.systemImage = systemImage
    }

    var body: some View {
        ContentUnavailableView { Label { text } icon: { Image(systemName: systemImage) } }
            .accessibilityIdentifier("profile.empty")
    }
}

private struct MoreButton: View {
    let more: () async -> Void
    @State private var busy = false

    var body: some View {
        Button {
            busy = true
            Task {
                await more()
                busy = false
            }
        } label: {
            Text("Show more", tableName: "Profile", comment: "Loads the next page of a list")
                .frame(maxWidth: .infinity, minHeight: 44)
        }
        .disabled(busy)
        .accessibilityIdentifier("profile.more")
    }
}
