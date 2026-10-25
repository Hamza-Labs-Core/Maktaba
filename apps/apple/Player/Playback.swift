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
    private(set) var problem: String?

    private var session: String?
    /// When to reopen: just before the signed URL ends.
    private var renewAt = Date.distantFuture
    private var openedAt = Date.distantPast
    private var running: Task<Void, Never>?

    init(client: APIClient) {
        self.client = client
    }

    func play(_ v: Video) {
        if v.id == video?.id, problem == nil { return }
        stop()
        video = v
        running = Task { await run() }
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
        problem = nil
        video = nil
    }

    private func run() async {
        await open(from: 0)
        guard player != nil else { return }
        await watch()
    }

    /// Opens (or reopens) the stream at `start` seconds. The signed URL is enough for a home
    /// from 0.3.18 on; AVPlayer also sends the Bearer header, which an older home's web port
    /// wants on `/stream/` (there a token that ends mid-video fails the item, and `watch`
    /// reopens it).
    private func open(from start: Double) async {
        guard let video else { return }
        do {
            let s = try await client.openStream(video: video.id, startSec: Int(start))
            guard let url = s.playbackURL(home: client.home) else {
                problem = "The home gave no address to play."
                return
            }
            let auth = try await client.playbackHeaders()
            if let old = session, old != s.sessionID {
                Task { [client] in await client.closeStream(old) }
            }
            session = s.sessionID
            try await client.waitUntilPlayable(url, headers: auth.headers)
            try Task.checkCancellation()
            openedAt = .now
            renewAt = (s.expires ?? .distantFuture).addingTimeInterval(-60)
            let item = AVPlayerItem(asset: AVURLAsset(url: url, options: ["AVURLAssetHTTPHeaderFieldsKey": auth.headers]))
            let p = player ?? AVPlayer()
            p.replaceCurrentItem(with: item)
            if start > 0 { await p.seek(to: CMTime(seconds: start, preferredTimescale: 600)) }
            player = p
            p.play()
        } catch is CancellationError {
            return
        } catch {
            player = nil
            problem = error.localizedDescription
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
}

/// The video with its title and a close button over it (AVKit's own controls on the
/// video). Full screen, or one half of a half-folded iPhone Duo.
struct WatchView: View {
    let playback: Playback

    var body: some View {
        ZStack(alignment: .top) {
            ZStack {
                Color.black
                if let player = playback.player {
                    VideoPlayer(player: player)
                } else if let problem = playback.problem {
                    ContentUnavailableView("Can't play", systemImage: "exclamationmark.triangle", description: Text(problem))
                        .foregroundStyle(.white)
                } else {
                    ProgressView().tint(.white)
                }
            }
            .ignoresSafeArea()
            HStack {
                Button("Close", systemImage: "xmark") { playback.stop() }
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
}
