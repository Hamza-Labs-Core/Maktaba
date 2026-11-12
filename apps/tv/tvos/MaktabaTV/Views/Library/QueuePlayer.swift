import AVKit
import MaktabaKit
import SwiftUI

/// Epic 56 stories 56.02 and 56.11: the Apple TV's player on the server queue. Every Play and
/// Shuffle makes a queue (`PlayQueueModel`, MaktabaKit) and plays its current item; Next,
/// Previous and Shuffle again are in the transport bar's menu; near the end the Up next card
/// counts down (Play the next video automatically, Settings) and the next item plays.
@MainActor @Observable
final class QueuePlayer {
    let client: APIClient
    let model: PlayQueueModel
    /// The video playing (the full-screen player shows while set).
    var playback: TVPlayback?
    var item: QueueItem?
    /// Why a Play did not start (the page says it): nothing to play, or the queue failed.
    var failure: String?
    var starting = false
    /// Epic 57: a queue item the home does not give under a parental limit (its id): the
    /// blocked page shows in the player's place.
    var blocked: String?
    /// Whether this sign-in is under a parental limit (set where the player is made).
    @ObservationIgnored var limited: () -> Bool = { false }

    /// The player's cover shows: a video plays (or was stopped by the home), or one is blocked.
    var showing: Bool { playback != nil || blocked != nil }

    init(client: APIClient) {
        self.client = client
        model = PlayQueueModel(transport: client, deviceID: "tv-appletv")
    }

    /// Makes a queue from `source` and plays its first item.
    func play(_ source: QueueSource, shuffle: Bool, start: String? = nil) {
        guard !starting else { return }
        starting = true
        failure = nil
        Task {
            defer { starting = false }
            do {
                guard let first = try await model.create(source, shuffle: shuffle, startVideo: start) else {
                    failure = String(localized: "Nothing to play here.")
                    return
                }
                await show(first)
            } catch is EmptySource {
                failure = String(localized: "Nothing to play here.")
            } catch {
                failure = String(localized: "The queue could not be loaded.")
            }
        }
    }

    /// Continues a queue from another device (Home).
    func resume(_ doc: QueueDoc) {
        guard let cur = model.resume(doc) else { return }
        Task { await show(cur) }
    }

    func next() { move { try await $0.next() } }
    func previous() { move { try await $0.previous() } }
    func reshuffle() { Task { try? await model.reshuffle() } }

    private func move(_ step: @escaping (PlayQueueModel) async throws -> QueueItem?) {
        Task {
            if let it = try? await step(model) { await show(it) }
        }
    }

    /// Plays `it` from where this person stopped (its card's position).
    func show(_ it: QueueItem) async {
        let old = playback
        let video: Video
        do {
            video = try await client.video(it.videoID)
        } catch {
            // a stale item of the queue that is now hidden (404 under a limit): the blocked page
            guard PlaybackVerdict.open(error, limited: limited()) == .blocked else { return }
            await old?.stop()
            playback = nil
            item = it
            blocked = it.videoID
            return
        }
        await old?.stop()
        blocked = nil
        let p = TVPlayback(client: client, video: video)
        p.limited = limited
        let at = it.completed == true ? 0 : (it.positionSec ?? 0)
        p.start(at: at > 10 ? at : 0)
        item = it
        playback = p
    }

    func close() {
        let p = playback
        playback = nil
        item = nil
        blocked = nil
        Task { await p?.stop() }
    }
}

extension EnvironmentValues {
    /// The signed-in home's queue player (set in ContentView).
    @Entry var queuePlayer: QueuePlayer?
}

/// Seconds the Up next card counts down from once the video ends.
let upNextSeconds = 10

/// The queue's player full screen: the system's transport bar with Previous, Next and (when
/// shuffled) Shuffle again in its menu, and the Up next card in the bottom trailing corner
/// (bottom left in Arabic) over the last 30 seconds.
struct QueuePlayerScreen: View {
    let queue: QueuePlayer
    @AppStorage("autoplayNext") private var autoplay = true
    @State private var next: QueueItem?
    @State private var left: Int?
    @State private var cancelled = false
    @State private var endedAt: Date?
    @Environment(\.appSession) private var session

    var body: some View {
        ZStack(alignment: .bottomTrailing) {
            Color.black
            if let id = queue.blocked, let session {
                // Epic 57: a queue item the home does not give: the blocked page
                TVBlockedPage(client: queue.client, session: session, subject: .video(id), back: { queue.close() }) {
                    if let item = queue.item { Task { await queue.show(item) } }
                }
            } else if let p = queue.playback, let stop = p.parentalStop, let session {
                // the home stopped the video: the time's-up screen or the blocked page
                TVParentalStopView(playback: p, stop: stop, session: session) { queue.close() }
            } else if let p = queue.playback, let player = p.player {
                SystemPlayer(player: player, menu: menu)
                    .id(p.video.id)
                    .accessibilityIdentifier("queue.player")
                // the time left today, in its last 15 minutes (57.15)
                if let session {
                    TVTimeLeft(store: session.parental, overVideo: true, client: p.client, library: p.video.libraryID)
                        .padding(80)
                        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                        .allowsHitTesting(false)
                }
            } else if let p = queue.playback, let failure = p.failure {
                // why it does not play, with what can be done (it showed a spinner for good)
                HomeErrorView(failure) { action in
                    if action == .tryAgain { p.retry() } else { queue.close() }
                }
                .buttonStyle(.gold)
                .foregroundStyle(.white)
                .environment(\.colorScheme, .dark)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityIdentifier("player.error")
            } else {
                ProgressView().tint(.white)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            if let next, let left, !cancelled {
                UpNextCard(item: next, secondsLeft: left, playNow: { queue.next() }, cancel: { cancelled = true })
                    .padding(80)
            }
        }
        .ignoresSafeArea()
        .onChange(of: queue.item?.pos) { _, _ in
            next = nil; left = nil; cancelled = false; endedAt = nil
        }
        .onReceive(NotificationCenter.default.publisher(for: AVPlayerItem.didPlayToEndTimeNotification)) { note in
            if (note.object as? AVPlayerItem) === queue.playback?.player?.currentItem { endedAt = .now }
        }
        .task(id: queue.item?.pos) { await watch() }
    }

    private var menu: [UIAction] {
        var items = [
            UIAction(title: String(localized: "Previous"), image: UIImage(systemName: "backward.end.fill")) { _ in queue.previous() },
            UIAction(title: String(localized: "Next"), image: UIImage(systemName: "forward.end.fill")) { _ in queue.next() },
        ]
        if queue.model.doc?.shuffle == true {
            items.append(UIAction(title: String(localized: "Shuffle again"), image: UIImage(systemName: "shuffle")) { _ in queue.reshuffle() })
        }
        return items
    }

    /// Twice a second: the last 30 s show Up next; 10 s after the end (autoplay on) the next plays.
    private func watch() async {
        while !Task.isCancelled {
            try? await Task.sleep(for: .milliseconds(500))
            guard let item = queue.playback?.player?.currentItem else { continue }
            let dur = item.duration.seconds, at = item.currentTime().seconds
            guard dur.isFinite, dur > 0 else { continue }
            if dur - at <= 30 || endedAt != nil {
                if next == nil, !cancelled { next = await queue.model.peekNext() }
                if let endedAt, autoplay {
                    let l = max(0, upNextSeconds - Int(Date.now.timeIntervalSince(endedAt)))
                    left = l
                    if l == 0, next != nil, !cancelled { queue.next(); return }
                } else {
                    left = -1
                }
            }
        }
    }
}

/// The Up next card: the next item's name and series, the countdown, Play now (focused) and Cancel.
struct UpNextCard: View {
    let item: QueueItem
    let secondsLeft: Int
    let playNow: () -> Void
    let cancel: () -> Void
    @FocusState private var focus: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(secondsLeft >= 0 ? "Up next in \(secondsLeft) s" : "Up next")
                .font(Theme.body(28, weight: .semibold)).foregroundStyle(Theme.gold)
            Text(item.name).font(Theme.heading(36)).foregroundStyle(Theme.text).lineLimit(2)
            if let s = item.seriesName { Text(s).font(Theme.body(26)).foregroundStyle(Theme.muted) }
            HStack(spacing: 24) {
                Button("Play now") { playNow() }.focused($focus).accessibilityIdentifier("upnext.play")
                Button("Cancel") { cancel() }
            }
            .buttonStyle(.gold)
        }
        .padding(32)
        .frame(width: 640, alignment: .leading)
        .background(Theme.raised, in: .rect(cornerRadius: Theme.cornerRadius))
        .accessibilityIdentifier("upnext")
        .onAppear { focus = true }
    }
}

/// Play and Shuffle for a source; disabled while a queue is being made.
struct PlayShuffleButtons: View {
    let source: QueueSource
    var shuffle = true
    var playLabel: LocalizedStringKey = "Play"
    @Environment(\.queuePlayer) private var queue

    var body: some View {
        HStack(spacing: 32) {
            Button(playLabel, systemImage: "play.fill") { queue?.play(source, shuffle: false) }
                .accessibilityIdentifier("play.play")
            if shuffle {
                Button("Shuffle", systemImage: "shuffle") { queue?.play(source, shuffle: true) }
                    .accessibilityIdentifier("play.shuffle")
            }
        }
        .buttonStyle(.gold)
        .disabled(queue?.starting == true)
        .focusSection()
    }
}
