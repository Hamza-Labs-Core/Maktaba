import MaktabaKit
import SwiftUI

/// A season's name: Season n (Specials for 0), else its own.
func seasonTitle(_ s: SeriesSeason) -> String {
    if let n = s.name, !n.isEmpty { return n }
    return s.season == 0 ? String(localized: "Specials") : String(localized: "Season \(s.season)")
}

/// A series (story 56.04): Continue (its next unwatched episode) or Play, Shuffle, Mark watched;
/// a pill per season (Select twice opens the season's page); the season's episodes.
struct SeriesView: View {
    let id: String
    @Environment(\.client) private var client
    @State private var name = ""
    @State private var eps: SeriesEpisodes?
    @State private var season: Int?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 32) {
                Text(name.isEmpty ? String(localized: "Untitled series") : name).font(Theme.heading(56)).foregroundStyle(Theme.text)
                if let eps {
                    let started = eps.episodes.contains { $0.watched || $0.progressPct > 0 }
                    HStack(spacing: 32) {
                        PlayShuffleButtons(source: QueueSource(kind: "series", id: id), playLabel: started ? "Continue" : "Play")
                        Button("Mark watched") {
                            Task { _ = try? await client?.markWatched(eps.episodes.map(\.videoID)); await load() }
                        }
                        .buttonStyle(.gold)
                    }
                    if eps.seasons.count > 1 {
                        HStack(spacing: 20) {
                            ForEach(eps.seasons) { s in
                                NavigationLink { SeasonView(seriesID: id, number: s.season) } label: {
                                    Text((s.season == season ? "● " : "") + seasonTitle(s))
                                }
                                .buttonStyle(.gold)
                                .accessibilityIdentifier("season.\(s.season)")
                            }
                        }
                        .focusSection()
                    }
                    EpisodeRow(episodes: shown(eps))
                }
            }
            .padding(80)
        }
        .background(Theme.background.ignoresSafeArea())
        .accessibilityIdentifier("series.page")
        .task { await load() }
    }

    private func shown(_ eps: SeriesEpisodes) -> [Episode] {
        let s = eps.seasons.first { $0.season == (season ?? eps.nextEpisode?.season ?? eps.seasons.first?.season) }
        let list = s?.episodes ?? []
        return s?.kind == "ongoing" ? list.reversed() : list
    }

    private func load() async {
        guard let client else { return }
        if let s = try? await client.episodes(series: id) {
            eps = s
            season = season ?? s.nextEpisode?.season ?? s.seasons.first?.season
        }
        if let h = try? await client.series(id) { name = h.name }
    }
}

struct EpisodeRow: View {
    let episodes: [Episode]
    var body: some View {
        ScrollView(.horizontal) {
            LazyHStack(spacing: 48) {
                ForEach(episodes) { e in
                    NavigationLink { VideoPage(id: e.videoID) } label: {
                        VideoCard(id: e.videoID, title: [e.episode.map(String.init), e.title].compactMap { $0 }.joined(separator: ". "),
                                  progress: e.progress)
                    }
                    .buttonStyle(.borderless)
                }
            }
            .padding(.vertical, 24)
        }
    }
}

/// A season (story 56.04): Play (its next unwatched episode), Shuffle, Mark season watched.
struct SeasonView: View {
    let seriesID: String
    let number: Int
    @Environment(\.client) private var client
    @State private var season: SeriesSeason?

    var body: some View {
        VStack(alignment: .leading, spacing: 32) {
            if let season {
                Text(seasonTitle(season)).font(Theme.heading(56)).foregroundStyle(Theme.text)
                HStack(spacing: 32) {
                    PlayShuffleButtons(source: season.seasonID.map { QueueSource(kind: "season", id: $0) } ?? QueueSource(kind: "series", id: seriesID))
                    Button("Mark season watched") {
                        Task { _ = try? await client?.markWatched(season.episodes.map(\.videoID)) }
                    }
                    .buttonStyle(.gold)
                }
                EpisodeRow(episodes: season.kind == "ongoing" ? season.episodes.reversed() : season.episodes)
            } else {
                ProgressView()
            }
        }
        .padding(80)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Theme.background.ignoresSafeArea())
        .accessibilityIdentifier("season.page")
        .task { season = try? await client?.episodes(series: seriesID).seasons.first { $0.season == number } }
    }
}

/// A collection's or a playlist's page: name, count, badge or rules; Play and Shuffle; the videos.
struct ListPageView: View {
    let title: String
    let badge: String?
    let source: QueueSource
    let videos: [BrowseVideo]
    var remove: ((BrowseVideo) -> Void)?
    @Environment(\.queuePlayer) private var queue

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 28) {
                Text(title).font(Theme.heading(56)).foregroundStyle(Theme.text)
                Text([badge, String(localized: "\(videos.count) videos")].compactMap { $0 }.joined(separator: "  ·  "))
                    .font(Theme.body(28)).foregroundStyle(Theme.muted)
                PlayShuffleButtons(source: source, shuffle: videos.count > 1)
                    .disabled(videos.isEmpty)
                ForEach(videos) { v in
                    HStack(spacing: 32) {
                        Button { queue?.play(source, shuffle: false, start: v.id) } label: {
                            VideoCard(id: v.id, title: v.title ?? v.filename)
                        }
                        .buttonStyle(.borderless)
                        if let remove {
                            Button("Remove from playlist") { remove(v) }.buttonStyle(.gold)
                        }
                    }
                }
            }
            .padding(80)
        }
        .background(Theme.background.ignoresSafeArea())
        .accessibilityIdentifier("list.page")
    }
}

struct CollectionPageView: View {
    let library: String
    let collection: LibraryCollection
    @Environment(\.client) private var client
    @State private var videos: [BrowseVideo] = []

    var body: some View {
        ListPageView(title: collection.name, badge: CollectionsTab.badge(collection), source: collection.queueSource, videos: videos)
            .task { videos = (try? await client?.collectionVideos(library, collection)) ?? [] }
    }
}

/// A playlist (story 56.10): a smart one shows its rules and has no Remove.
struct PlaylistPageView: View {
    let id: String
    @Environment(\.client) private var client
    @State private var page: LibraryPlaylistPage?

    var body: some View {
        Group {
            if let page {
                let p = page.playlist
                ListPageView(
                    title: p.name,
                    badge: p.smart ? String(localized: "Smart") + " · " + String(localized: "Rules") + ": " + p.rules.joined(separator: ", ") : nil,
                    source: QueueSource(kind: "playlist", id: id), videos: page.items,
                    remove: p.smart ? nil : { v in Task { try? await client?.removeFromPlaylist(id, video: v.id); await load() } })
            } else {
                ProgressView()
            }
        }
        .task { await load() }
    }

    private func load() async { page = try? await client?.libraryPlaylist(id) }
}
