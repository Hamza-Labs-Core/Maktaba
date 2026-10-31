import AVKit
import MaktabaKit
import SwiftUI

/// What is playing, for the whole app: the watch screen may be full screen (folded, open
/// flat, the iPad, the Mac) or one half of a half-folded iPhone Duo with the library on the
/// other, so the stream lives here and not in a view.
///
/// Opens a stream session, plays its signed URL, reports progress every 10 s, reopens
/// before the signed URL ends (the home signs it for the video's length plus an hour, so
/// normally never) or when the item fails, and closes the session when stopped.
@MainActor @Observable
final class Playback {
    let client: APIClient
    private(set) var video: Video?
    private(set) var player: AVPlayer?
    /// Why it does not play (the watch screen says it and what can be done).
    private(set) var failure: HomeFailure?

    private var session: String?
    /// When to reopen: just before the signed URL ends.
    private var renewAt = Date.distantFuture
    private var openedAt = Date.distantPast
    private var running: Task<Void, Never>?
    /// Where the player reads the stream from: every request reaches the home with the
    /// user's token as it is now (see `StreamProxy`).
    private var proxy: StreamProxy?

    init(client: APIClient) {
        self.client = client
    }

    func play(_ v: Video) {
        if v.id == video?.id, failure == nil { return }
        stop()
        #if os(iOS)
        // the app declares background audio: with the playback category the sound goes on
        // when the screen locks or the app goes to the background (and ignores the mute switch)
        try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .moviePlayback)
        try? AVAudioSession.sharedInstance().setActive(true)
        #endif
        video = v
        running = Task { await run() }
    }

    /// Opens the same video again, after it failed.
    func retry() {
        if let v = video { play(v) }
    }

    /// Play or pause what is open (the keyboard's space, the Playback menu).
    func togglePause() {
        guard let p = player else { return }
        if p.rate > 0 { p.pause() } else { p.play() }
    }

    /// Moves `seconds` forward (back when negative), not before the start nor past the end.
    func skip(by seconds: Double) {
        guard let p = player else { return }
        let now = p.currentTime().seconds
        guard now.isFinite else { return }
        var to = max(0, now + seconds)
        if let end = p.currentItem?.duration.seconds, end.isFinite { to = min(to, max(0, end - 1)) }
        p.seek(to: CMTime(seconds: to, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
    }

    func stop() {
        running?.cancel()
        running = nil
        player?.pause()
        if let session {
            let at = player?.currentTime().seconds ?? 0
            Task { [client] in
                if at.isFinite, at > 0 { await client.reportProgress(session: session, positionSec: Int(at)) }
                await client.closeStream(session)
            }
        }
        session = nil
        player = nil
        failure = nil
        video = nil
        proxy?.stop()
        proxy = nil
    }

    private func run() async {
        await open(from: 0)
        guard player != nil else { return }
        await watch()
    }

    /// Opens (or reopens) the stream at `start` seconds, through the app's stream proxy: the
    /// home's web port wants the user's token on every `/stream/` request, and AVPlayer
    /// cannot renew one mid-video.
    private func open(from start: Double) async {
        guard let video else { return }
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
                failure = HomeFailure(kind: .server, detail: String(localized: "Could not start playback on this device."))
                return
            }
            if let old = session, old != s.sessionID {
                Task { [client] in await client.closeStream(old) }
            }
            session = s.sessionID
            try await client.waitUntilPlayable(url, headers: auth.headers)
            try Task.checkCancellation()
            openedAt = .now
            renewAt = (s.expires ?? .distantFuture).addingTimeInterval(-60)
            let item = AVPlayerItem(url: local)
            let p = player ?? AVPlayer()
            p.replaceCurrentItem(with: item)
            if start > 0 { await p.seek(to: CMTime(seconds: start, preferredTimescale: 600)) }
            player = p
            p.play()
        } catch is CancellationError {
            return
        } catch {
            player = nil
            failure = HomeFailure(error)
        }
    }

    /// Every 10 s while playing: the position, so "continue watching" resumes here; a reopen
    /// where it is before the URL ends, or when the item failed (not again within 30 s of
    /// opening: then it shows why).
    private func watch() async {
        while !Task.isCancelled {
            try? await Task.sleep(for: .seconds(10))
            guard !Task.isCancelled, let p = player, let session else { return }
            let at = p.currentTime().seconds
            if at.isFinite, at > 0 { await client.reportProgress(session: session, positionSec: Int(at)) }
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
}

/// The video with its title and a close button over it (AVKit's own controls on the
/// video; Esc closes). Full screen, the Mac's whole window, or one half of a half-folded
/// iPhone Duo.
struct WatchView: View {
    let playback: Playback
    let session: AppSession

    var body: some View {
        ZStack(alignment: .top) {
            ZStack {
                Color.black
                if let player = playback.player {
                    PlayerSurface(player: player)
                } else if let failure = playback.failure {
                    HomeErrorView(failure) { act($0) }
                        .environment(\.colorScheme, .dark)
                        .accessibilityIdentifier("player.error")
                } else {
                    ProgressView().tint(.white)
                }
            }
            .ignoresSafeArea()
            HStack {
                Button("Close", systemImage: "xmark") { playback.stop() }
                    .keyboardShortcut(.cancelAction)
                    .labelStyle(.iconOnly)
                    .font(.title2)
                    .padding(12)
                    .background(.ultraThinMaterial, in: .circle)
                Text(playback.video?.displayTitle ?? "")
                    .font(.headline)
                    .lineLimit(1)
                    .foregroundStyle(.white)
                    .shadow(radius: 4)
                Spacer()
            }
            .padding()
        }
        .background(.black)
    }

    private func act(_ a: ErrorAction) {
        switch a {
        case .tryAgain: playback.retry()
        case .back: playback.stop()
        case .signInAgain:
            playback.stop()
            session.signedOut()
        case .chooseAnotherHome:
            playback.stop()
            session.switchHome()
        }
    }
}

/// The Playback menu (the Mac's menu bar, an iPad's with a keyboard): space plays and
/// pauses, the arrows skip 10 s, Command-period stops. Each is off while nothing plays, so
/// the keys stay the search field's and the lists'.
struct PlaybackCommands: Commands {
    @FocusedValue(\.playback) private var playback

    var body: some Commands {
        CommandMenu("Playback") {
            Button("Play/Pause") { playback?.togglePause() }
                .keyboardShortcut(.space, modifiers: [])
                .disabled(playback?.player == nil)
            Button("Skip Forward 10 Seconds") { playback?.skip(by: 10) }
                .keyboardShortcut(.rightArrow, modifiers: [])
                .disabled(playback?.player == nil)
            Button("Skip Back 10 Seconds") { playback?.skip(by: -10) }
                .keyboardShortcut(.leftArrow, modifiers: [])
                .disabled(playback?.player == nil)
            Divider()
            Button("Stop") { playback?.stop() }
                .keyboardShortcut(".", modifiers: .command)
                .disabled(playback?.video == nil)
        }
    }
}

extension FocusedValues {
    /// What plays in the focused window.
    @Entry var playback: Playback?
}
