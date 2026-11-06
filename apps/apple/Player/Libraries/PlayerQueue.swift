import MaktabaKit
import SwiftUI

/// Epic 56 story 56.12: the iPhone, iPad and Mac Player on the server play queue. One queue
/// per home (`PlayQueueModel`, MaktabaKit, shared with the Apple TV); Play and Shuffle make it,
/// and the player's Up next reads it first (then the series, as before).
@MainActor
enum PlayerQueues {
    private static var byHome: [URL: PlayQueueModel] = [:]

    static func of(_ client: APIClient) -> PlayQueueModel {
        if let q = byHome[client.home] { return q }
        let q = PlayQueueModel(transport: client, deviceID: "phone-apple")
        byHome[client.home] = q
        return q
    }

    /// The queue's next item as an Up next episode, when `video` is the queue's current one.
    static func next(after video: String, _ client: APIClient) async -> Episode? {
        let q = of(client)
        guard q.doc?.current?.videoID == video, let n = await q.peekNext() else { return nil }
        var o: [String: Any] = ["video_id": n.videoID, "title": n.name]
        if let d = n.durationSec { o["duration_sec"] = d }
        guard let data = try? JSONSerialization.data(withJSONObject: o) else { return nil }
        return try? JSONDecoder().decode(Episode.self, from: data)
    }

    /// The queue moves on when its next item plays.
    static func played(_ video: String, _ client: APIClient) {
        let q = of(client)
        guard let item = q.doc?.items.first(where: { $0.videoID == video }), item.pos != q.doc?.currentPos else { return }
        Task { _ = try? await q.move(to: item.pos) }
    }
}

/// Play and Shuffle on a source: one server queue, then the player at its first item.
struct PlayShuffleButtons: View {
    let context: PlayerContext
    let source: QueueSource
    var shuffle = true
    var onlyShuffle = false
    @State private var target: String?
    @State private var problem: Text?
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: MaktabaTokens.Spacing.s2) {
                if !onlyShuffle {
                    Button { start(false) } label: {
                        Label { Text("Play", tableName: "Libraries", comment: "Plays a library, series, collection or playlist in order") } icon: { Image(systemName: "play.fill") }
                    }
                    .buttonStyle(BrassButtonStyle())
                    .accessibilityIdentifier("queue.play")
                }
                if shuffle {
                    Button { start(true) } label: {
                        Label { Text("Shuffle", tableName: "Libraries", comment: "Plays it at random, the whole of it") } icon: { Image(systemName: "shuffle") }
                    }
                    .buttonStyle(OutlineButtonStyle())
                    .accessibilityIdentifier("queue.shuffle")
                }
            }
            .disabled(busy)
            if let problem { problem.font(.footnote).foregroundStyle(.red) }
        }
        .navigationDestination(item: $target) { id in PlayerDestination(context: context, route: .watch(videoID: id, at: nil)) }
    }

    private func start(_ sh: Bool) {
        busy = true
        Task {
            defer { busy = false }
            do {
                if let first = try await PlayerQueues.of(context.client).create(source, shuffle: sh) {
                    problem = nil
                    target = first.videoID
                } else {
                    problem = Text("Nothing to play here.", tableName: "Libraries", comment: "Play on an empty view")
                }
            } catch is EmptySource {
                problem = Text("Nothing to play here.", tableName: "Libraries", comment: "Play on an empty view")
            } catch {
                problem = Text("The queue could not be loaded.", tableName: "Libraries", comment: "Play failed")
            }
        }
    }
}

/// The library's Collections tab: each with its badge; one opens with Play and Shuffle.
struct CollectionsTab: View {
    let context: PlayerContext
    private var client: APIClient { context.client }
    let library: String
    @State private var items: [LibraryCollection]?
    @State private var open: String?

    var body: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s3) {
            if let items, items.isEmpty {
                Text("No collections in this library yet.", tableName: "Libraries", comment: "An empty Collections tab").foregroundStyle(.secondary)
            }
            ForEach(items ?? []) { c in
                VStack(alignment: .leading, spacing: 4) {
                    Button { open = open == c.id ? nil : c.id } label: {
                        VStack(alignment: .leading) {
                            Text(verbatim: c.name).font(.headline)
                            if let b = Self.badge(c) { b.font(.caption).foregroundStyle(.tint) }
                        }
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("collection.\(c.id)")
                    if open == c.id { PlayShuffleButtons(context: context, source: c.queueSource, shuffle: (c.videos ?? 2) > 1) }
                }
            }
        }
        .accessibilityIdentifier("library.collections")
        .task(id: library) { items = (try? await client.libraryCollections(library)) ?? [] }
    }

    static func badge(_ c: LibraryCollection) -> Text? {
        switch (c.sourceKind, c.kind) {
        case ("youtube_playlist", _): Text("YouTube playlist", tableName: "Libraries", comment: "A collection's badge")
        case ("youtube_channel", _): Text("YouTube channel", tableName: "Libraries", comment: "A collection's badge")
        case ("plex", _): Text("Plex collection", tableName: "Libraries", comment: "A collection's badge")
        case (_, "smart"): Text("Smart", tableName: "Libraries", comment: "A smart collection's or playlist's badge")
        default: nil
        }
    }
}

/// The library's Playlists tab: the user's lists with videos here (smart ones whose rules cover it).
struct PlaylistsTab: View {
    let context: PlayerContext
    private var client: APIClient { context.client }
    let library: String
    @State private var items: [LibraryPlaylist]?
    @State private var open: String?

    var body: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s3) {
            if let items, items.isEmpty {
                Text("None of your playlists has videos from this library.", tableName: "Libraries", comment: "An empty Playlists tab").foregroundStyle(.secondary)
            }
            ForEach(items ?? []) { p in
                VStack(alignment: .leading, spacing: 4) {
                    Button { open = open == p.id ? nil : p.id } label: {
                        VStack(alignment: .leading) {
                            Text(verbatim: p.name).font(.headline)
                            if p.smart { Text("Smart", tableName: "Libraries", comment: "A smart collection's or playlist's badge").font(.caption).foregroundStyle(.tint) }
                        }
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("playlist.\(p.id)")
                    if open == p.id { PlayShuffleButtons(context: context, source: QueueSource(kind: "playlist", id: p.id)) }
                }
            }
        }
        .accessibilityIdentifier("library.playlists")
        .task(id: library) { items = (try? await client.libraryPlaylists(library: library)) ?? [] }
    }
}

/// Play next and Add to queue on a video's page (a queue starts at it when none plays).
struct QueueMarks: View {
    let client: APIClient
    let video: String
    @State private var added = false

    var body: some View {
        MarkButton(on: false, symbol: "text.line.first.and.arrowtriangle.forward", id: "video.playNext") {
            Text("Play next", tableName: "Libraries", comment: "Puts the video right after the one playing")
        } action: { add("next") }
        MarkButton(on: added, symbol: "text.append", id: "video.addToQueue") {
            Text("Add to queue", tableName: "Libraries", comment: "Puts the video at the end of the play queue")
        } action: { add("end") }
    }

    private func add(_ place: String) {
        Task { added = (try? await PlayerQueues.of(client).add([video], where: place)) != nil }
    }
}

/// Home's first row: the queue last used on any device in the last day.
struct ContinueQueueRow: View {
    let context: PlayerContext
    let doc: QueueDoc
    @State private var target: String?

    static func recent(_ client: APIClient) async -> QueueDoc? {
        guard let d = try? await PlayerQueues.of(client).fetchCurrent(), let used = d.usedAt,
              Date.now.timeIntervalSince(used) <= 86_400, d.current != nil else { return nil }
        return d
    }

    var body: some View {
        if let item = doc.current {
            VStack(alignment: .leading, spacing: 8) {
                Text("Continue your queue", tableName: "Libraries", comment: "Home's row: the queue last used on any device").font(.headline)
                Button { target = PlayerQueues.of(context.client).resume(doc)?.videoID } label: {
                    Text(verbatim: item.name)
                }
                .buttonStyle(OutlineButtonStyle())
                .accessibilityIdentifier("home.continueQueue")
            }
            .navigationDestination(item: $target) { id in PlayerDestination(context: context, route: .watch(videoID: id, at: nil)) }
        }
    }
}
