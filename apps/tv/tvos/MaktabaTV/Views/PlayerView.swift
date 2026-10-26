import AVKit
import MaktabaKit
import SwiftUI

/// One video playing, as the Player apps play (apps/apple/Player/Playback.swift): a stream
/// session from the home, read through MaktabaKit's `StreamProxy` (the home's web port
/// wants the user's token on every `/stream/` request and AVPlayer cannot renew one), the
/// position reported every 10 s so continue watching resumes there, a reopen before the
/// signed URL ends or when the item fails, and the session closed when it stops.
@MainActor @Observable
final class TVPlayback {
    let client: APIClient
    let video: Video
    private(set) var player: AVPlayer?
    private(set) var problem: String?

    private var session: String?
    private var renewAt = Date.distantFuture
    private var openedAt = Date.distantPast
    private var running: Task<Void, Never>?
    private var proxy: StreamProxy?
    private var ended: NSObjectProtocol?

    init(client: APIClient, video: Video) {
        self.client = client
        self.video = video
    }

    /// Plays from `start` seconds (0: the beginning).
    func start(at start: Double) {
        running?.cancel()
        running = Task { await run(from: start) }
    }

    func stop() {
        running?.cancel()
        running = nil
        player?.pause()
        if let session {
            let at = player?.currentTime().seconds ?? 0
            let done = player?.currentItem.map { $0.duration.seconds.isFinite && at >= $0.duration.seconds - 5 } ?? false
            Task { [client] in
                if at.isFinite, at > 0 { await client.reportProgress(session: session, positionSec: Int(at), completed: done) }
                await client.closeStream(session)
            }
        }
        if let ended { NotificationCenter.default.removeObserver(ended) }
        ended = nil
        session = nil
        player = nil
        proxy?.stop()
        proxy = nil
    }

    private func run(from start: Double) async {
        await open(from: start)
        guard player != nil else { return }
        await watch()
    }

    private func open(from start: Double) async {
        do {
            let s = try await client.openStream(video: video.id, startSec: Int(start))
            guard let url = s.playbackURL(home: client.home) else {
                problem = "The home gave no address to play."
                return
            }
            let auth = try await client.playbackHeaders()
            let proxy = try self.proxy ?? StreamProxy(client: client)
            self.proxy = proxy
            try await proxy.start()
            guard let local = proxy.local(url) else {
                problem = "Could not start playback on this TV."
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
            player = p
            problem = nil
            p.play()
        } catch is CancellationError {
            return
        } catch {
            player = nil
            problem = error.localizedDescription
        }
    }

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
                problem = p.currentItem?.error?.localizedDescription ?? "The video stopped playing."
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

    var body: some View {
        ZStack {
            Color.black
            if let player = playback.player {
                SystemPlayer(player: player)
            } else if let problem = playback.problem {
                VStack(spacing: 32) {
                    ContentUnavailableView("Can't play", systemImage: "exclamationmark.triangle", description: Text(problem))
                    Button("Close") { dismiss() }.buttonStyle(.gold)
                }
                .foregroundStyle(.white)
            } else {
                VStack(spacing: 24) {
                    ProgressView().tint(.white)
                    Text("Starting \(playback.video.displayTitle)…").foregroundStyle(.white.opacity(0.8))
                }
            }
        }
        .ignoresSafeArea()
        .onReceive(NotificationCenter.default.publisher(for: AVPlayerItem.didPlayToEndTimeNotification)) { note in
            if (note.object as? AVPlayerItem) === playback.player?.currentItem { dismiss() }
        }
    }
}

private struct SystemPlayer: UIViewControllerRepresentable {
    let player: AVPlayer

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let c = AVPlayerViewController()
        c.player = player
        return c
    }

    func updateUIViewController(_ c: AVPlayerViewController, context: Context) {
        if c.player !== player { c.player = player }
    }
}
