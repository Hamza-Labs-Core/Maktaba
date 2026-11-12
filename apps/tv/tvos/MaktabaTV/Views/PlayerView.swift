import AVKit
import MaktabaKit
import SwiftUI

/// One video playing, as the Player apps play (apps/apple/Player/Playback.swift): a stream
/// session from the home, read through MaktabaKit's `StreamProxy` (the home's web port
/// wants the user's token on every `/stream/` request and AVPlayer cannot renew one), the
/// position reported every 10 s so continue watching resumes there, a reopen before the
/// signed URL ends or when the item fails, and the session closed when it stops.
///
/// Epic 57 (stories 57.11 and 57.15): the progress post is also the stream's heartbeat (a
/// restricted account's stream is served only while it comes at least every 60 s; here every
/// 10 s). When the home refuses the stream or a heartbeat for a parental reason
/// (`PlaybackVerdict`, MaktabaKit: the account's time is over, or the video is now hidden)
/// the video stops, and nothing is tried again until `resumeAfterStop`.
@MainActor @Observable
final class TVPlayback {
    let client: APIClient
    let video: Video
    private(set) var player: AVPlayer?
    /// Why it does not play (the player screen says it and what can be done).
    private(set) var failure: HomeFailure?
    /// The home stopped this video for a parental reason: the screen shows the time's-up
    /// screen or the blocked page, and no stream is asked for again from here.
    private(set) var parentalStop: ParentalStop?
    /// The home cannot read the account's watching time right now (503
    /// `parental_time_unknown`, story 57.14): no error screen and no time's-up screen, a calm
    /// line while the stream is asked for again as the home's `Retry-After` says.
    private(set) var checkingTime: HomeFailure?
    /// Whether this sign-in is under a parental limit (the session's store): a "not found"
    /// then may be a block.
    @ObservationIgnored var limited: () -> Bool = { false }

    enum ParentalStop: Equatable {
        /// 404 under a limit: the home does not give the video (`midPlay`: it was playing).
        case blocked(midPlay: Bool)
        /// 403 `parental-time`: the account's time is over, with where the video stopped.
        case time(TimeUp, position: Double)
    }

    /// How often the position is posted: the stream's heartbeat (the home wants one at least
    /// every 60 s from a restricted account).
    static let heartbeat: Duration = .seconds(10)

    private var session: String?
    /// When the stream was last opened again for a missed heartbeat.
    private var reopenedAt = Date.distantPast
    private var renewAt = Date.distantFuture
    private var openedAt = Date.distantPast
    private var running: Task<Void, Never>?
    private var proxy: StreamProxy?
    /// Where the stream last opened: Try again starts there.
    private var lastStart: Double = 0

    init(client: APIClient, video: Video) {
        self.client = client
        self.video = video
    }

    /// Plays from `start` seconds (0: the beginning).
    func start(at start: Double) {
        failure = nil
        parentalStop = nil
        checkingTime = nil
        running?.cancel()
        running = Task { await run(from: start) }
    }

    /// Starts again after a failure, from where it last opened.
    func retry() {
        start(at: lastStart)
    }

    /// Plays on after a parental stop, from where it stopped: the home said there is time
    /// again, or a parent opened the video. Never called by the player on its own.
    func resumeAfterStop() {
        guard let stop = parentalStop else { return }
        if case let .time(_, position) = stop { start(at: position) } else { start(at: lastStart) }
    }

    /// Stops, and returns once the home has where it stopped (so a page shown next offers
    /// to resume there).
    func stop() async {
        // let a stream being opened finish first: its session is then ours to close
        running?.cancel()
        await running?.value
        running = nil
        player?.pause()
        let stopped = session.map { session in
            let at = player?.currentTime().seconds ?? 0
            let done = player?.currentItem.map { $0.duration.seconds.isFinite && at >= $0.duration.seconds - 5 } ?? false
            return Task { [client] in
                if at.isFinite, at > 0 { await client.reportProgress(session: session, positionSec: Int(at), completed: done) }
                await client.closeStream(session)
            }
        }
        session = nil
        player = nil
        proxy?.stop()
        proxy = nil
        await stopped?.value
    }

    private func run(from start: Double) async {
        await open(from: start)
        guard player != nil else { return }
        await watch()
    }

    private func open(from start: Double) async {
        lastStart = start
        do {
            let s = try await client.openStream(video: video.id, startSec: Int(start))
            guard let url = s.playbackURL(home: client.home) else {
                failure = HomeFailure(kind: .server, detail: String(localized: "The home gave no address to play."))
                return
            }
            let auth = try await client.playbackHeaders()
            let proxy = try self.proxy ?? StreamProxy(client: client)
            self.proxy = proxy
            try await proxy.start()
            guard let local = proxy.local(url) else {
                failure = HomeFailure(kind: .server, detail: String(localized: "Could not start playback on this TV."))
                return
            }
            if let old = session, old != s.sessionID {
                Task { [client] in await client.closeStream(old) }
            }
            session = s.sessionID
            // a transcode writes its first playlist a moment after the session opens
            try await client.waitUntilPlayable(url, headers: auth.headers, timeout: .seconds(60))
            try Task.checkCancellation()
            openedAt = .now
            renewAt = (s.expires ?? .distantFuture).addingTimeInterval(-60)
            let item = AVPlayerItem(url: local)
            item.externalMetadata = Self.metadata(video)
            let p = player ?? AVPlayer()
            p.replaceCurrentItem(with: item)
            if start > 0 { await p.seek(to: CMTime(seconds: start, preferredTimescale: 600)) }
            try Task.checkCancellation()
            player = p
            failure = nil
            checkingTime = nil
            p.play()
        } catch is CancellationError {
            return
        } catch {
            // Epic 57: the account's time is over (403 `parental-time`), or the home does not
            // give the video under a limit (404): not an error to try again
            switch PlaybackVerdict.open(error, limited: limited()) {
            case let .timeUp(up): stopped(.time(up, position: start))
            case .blocked: stopped(.blocked(midPlay: player != nil))
            case .other, .reopen:
                let f = HomeFailure(error)
                if let after = f.kind.timeUnknownRetrySeconds {
                    // what plays plays on (a renewal: the watch asks again); with nothing
                    // playing, the line shows while the stream is asked for again
                    if player != nil { return }
                    checkingTime = f
                    do { try await Task.sleep(for: .seconds(after)) } catch { return }
                    await open(from: start)
                    return
                }
                player = nil
                checkingTime = nil
                failure = f
            }
        }
    }

    /// Stops for a parental reason: the stream is closed and nothing plays until
    /// `resumeAfterStop` (the home has the position from the heartbeats).
    private func stopped(_ stop: ParentalStop) {
        Log.ui.info("playback: stopped by the home for a parental reason")
        player?.pause()
        if let session {
            Task { [client] in await client.closeStream(session) }
        }
        session = nil
        player = nil
        proxy?.stop()
        proxy = nil
        failure = nil
        parentalStop = stop
    }

    /// The home refused a heartbeat. True when playback ended here.
    private func refused(_ error: Error, at: Double) async -> Bool {
        switch PlaybackVerdict.progress(error, limited: limited()) {
        case .other:
            return false
        case let .timeUp(up):
            stopped(.time(up, position: at))
            return true
        case .blocked:
            stopped(.blocked(midPlay: true))
            return true
        case .reopen:
            // no heartbeat reached the home for a minute (a long pause): the stream is opened
            // again once, and the home then says whether there is time. Twice within two
            // minutes is the home saying no.
            guard Date.now.timeIntervalSince(reopenedAt) > 120 else {
                stopped(.time(TimeUp(reason: "no_heartbeat", nextAllowedAt: nil), position: at))
                return true
            }
            reopenedAt = .now
            let wasPlaying = (player?.rate ?? 0) > 0
            await open(from: at)
            if !wasPlaying { player?.pause() }
            return player == nil
        }
    }

    private func watch() async {
        while !Task.isCancelled {
            try? await Task.sleep(for: Self.heartbeat)
            guard !Task.isCancelled, let p = player, let session else { return }
            let at = p.currentTime().seconds
            // under a limit the post is the heartbeat too, so it goes from the very start
            if at.isFinite, at > 0 || limited() {
                do {
                    try await client.postProgress(session: session, positionSec: Int(max(0, at)))
                } catch {
                    if Log.isCancellation(error) { return }
                    if await refused(error, at: at) { return }
                    continue
                }
            }
            let failed = p.currentItem?.status == .failed
            if failed, Date.now.timeIntervalSince(openedAt) < 30 {
                p.pause()
                player = nil
                failure = p.currentItem?.error.map(HomeFailure.init) ?? HomeFailure(kind: .server, detail: String(localized: "The video stopped playing."))
                return
            }
            if failed || Date.now >= renewAt {
                let wasPlaying = p.rate > 0
                await open(from: at.isFinite ? at : 0)
                if !wasPlaying { player?.pause() }
            }
        }
    }

    /// The title and description the TV's info panel shows.
    static func metadata(_ v: Video) -> [AVMetadataItem] {
        func item(_ id: AVMetadataIdentifier, _ value: String) -> AVMetadataItem {
            let m = AVMutableMetadataItem()
            m.identifier = id
            m.value = value as NSString
            m.extendedLanguageTag = "und"
            return m
        }
        var items = [item(.commonIdentifierTitle, v.displayTitle)]
        if let d = v.description, !d.isEmpty { items.append(item(.commonIdentifierDescription, d)) }
        return items
    }
}

/// The video full screen, with the system's transport bar (scrubbing, audio and subtitle
/// choices, the Siri remote's gestures). Menu on the remote closes it.
struct PlayerScreen: View {
    let playback: TVPlayback
    @Environment(\.dismiss) private var dismiss
    @Environment(\.appSession) private var session

    var body: some View {
        ZStack(alignment: .topLeading) {
            Color.black
            if let stop = playback.parentalStop, let session {
                // Epic 57: the time's-up screen or the blocked page, in place of the picture
                TVParentalStopView(playback: playback, stop: stop, session: session) { dismiss() }
            } else if let player = playback.player {
                SystemPlayer(player: player)
                // the time left today, in its last 15 minutes (57.15)
                if let session {
                    TVTimeLeft(store: session.parental, overVideo: true, client: playback.client, library: playback.video.libraryID)
                        .padding(80)
                        .allowsHitTesting(false)
                }
            } else if let failure = playback.failure {
                // the first action has the focus: Try again, or Sign in again after a 401
                HomeErrorView(failure) { act($0) }
                    .buttonStyle(.gold)
                    .foregroundStyle(.white)
                    .environment(\.colorScheme, .dark)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .accessibilityIdentifier("player.error")
            } else {
                TVStarting(playback: playback)
            }
        }
        .ignoresSafeArea()
        .onReceive(NotificationCenter.default.publisher(for: AVPlayerItem.didPlayToEndTimeNotification)) { note in
            if (note.object as? AVPlayerItem) === playback.player?.currentItem { dismiss() }
        }
    }
}

/// Before the picture: a spinner and the video's title, or the home's calm line while it
/// cannot read the account's watching time yet (57.14: asked again by itself).
struct TVStarting: View {
    let playback: TVPlayback

    var body: some View {
        VStack(spacing: 24) {
            ProgressView().tint(.white)
            if let checking = playback.checkingTime {
                Text(verbatim: checking.message(video: true))
                    .foregroundStyle(.white)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("player.waiting")
            } else {
                Text("Starting \(playback.video.displayTitle)…").foregroundStyle(.white.opacity(0.8))
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

extension PlayerScreen {
    private func act(_ a: ErrorAction) {
        switch a {
        case .tryAgain: playback.retry()
        case .back: dismiss()
        case .signInAgain:
            dismiss()
            session?.signedOut()
        case .chooseAnotherHome:
            dismiss()
            session?.switchHome()
        }
    }
}

struct SystemPlayer: UIViewControllerRepresentable {
    let player: AVPlayer
    /// Epic 56: the queue's Previous, Next and Shuffle again, in the transport bar's menu.
    var menu: [UIAction] = []

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let c = AVPlayerViewController()
        c.player = player
        c.transportBarCustomMenuItems = menu
        return c
    }

    func updateUIViewController(_ c: AVPlayerViewController, context: Context) {
        if c.player !== player { c.player = player }
        if c.transportBarCustomMenuItems.count != menu.count { c.transportBarCustomMenuItems = menu }
    }
}
