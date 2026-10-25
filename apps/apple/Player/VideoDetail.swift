import AVKit
import MaktabaKit
import SwiftUI

struct VideoDetail: View {
    let client: APIClient
    let video: Video
    @State private var watching = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Button { watching = true } label: {
                    AuthorizedImage(client: client, video: video.id)
                        .overlay {
                            Image(systemName: "play.circle.fill")
                                .font(.system(size: 64))
                                .foregroundStyle(.white, Color.brass)
                                .shadow(radius: 8)
                        }
                        .clipShape(.rect(cornerRadius: 12))
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Play \(video.displayTitle)")

                Text(video.displayTitle)
                    .font(.title2.bold())
                    .multilineTextAlignment(.leading)
                if let d = video.description, !d.isEmpty {
                    Text(d).foregroundStyle(.secondary)
                }
            }
            .padding()
            .frame(maxWidth: 900, alignment: .leading)
        }
        .navigationTitle(video.displayTitle)
        #if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
        .fullScreenCover(isPresented: $watching) { WatchView(client: client, video: video) }
        #else
        .sheet(isPresented: $watching) { WatchView(client: client, video: video).frame(minWidth: 800, minHeight: 500) }
        #endif
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Play", systemImage: "play.fill") { watching = true }
            }
        }
    }
}

/// Plays a video: opens a stream session, plays its signed URL, reports progress, closes
/// the session when done. On iOS/macOS 27.1 an ArrangementView with the overlay style
/// (Apple's own example for a player): on a flat screen the controls float over the video;
/// on a half-folded iPhone Duo the video takes one half and the controls the other.
struct WatchView: View {
    let client: APIClient
    let video: Video
    @Environment(\.dismiss) private var dismiss
    @State private var player: AVPlayer?
    @State private var session: String?
    @State private var problem: String?
    /// When to reopen: just before the signed URL or the access token AVPlayer sends ends.
    @State private var renewAt = Date.distantFuture
    @State private var openedAt = Date.distantPast

    var body: some View {
        Group {
            #if os(iOS)
            if #available(iOS 27.1, *) {
                ArrangementView {
                    controls
                } secondary: {
                    screen
                }
                .arrangementViewStyle(.overlay)
            } else {
                stacked
            }
            #else
            // the fold is an iPhone Duo's: the Mac stacks the controls over the video
            stacked
            #endif
        }
        .background(.black)
        .task { await open() }
        .onDisappear { close() }
    }

    private var stacked: some View {
        ZStack(alignment: .top) {
            screen
            controls
        }
    }

    private var screen: some View {
        ZStack {
            Color.black
            if let player {
                VideoPlayer(player: player)
            } else if let problem {
                ContentUnavailableView("Can't play", systemImage: "exclamationmark.triangle", description: Text(problem))
                    .foregroundStyle(.white)
            } else {
                ProgressView().tint(.white)
            }
        }
        .ignoresSafeArea()
    }

    private var controls: some View {
        HStack {
            Button("Close", systemImage: "xmark") { dismiss() }
                .labelStyle(.iconOnly)
                .font(.title2)
                .padding(12)
                .background(.ultraThinMaterial, in: .circle)
            Text(video.displayTitle)
                .font(.headline)
                .lineLimit(1)
                .foregroundStyle(.white)
                .shadow(radius: 4)
            Spacer()
        }
        .padding()
    }

    private func open() async {
        await play(from: 0)
        guard player != nil else { return }
        await watch()
    }

    /// Opens (or reopens) the stream at `start` seconds. AVPlayer sends the Bearer header on
    /// every request of the asset: the home's web port lets only a signed-in caller through
    /// to `/stream/`, the signed URL alone is not enough there.
    private func play(from start: Double) async {
        do {
            let s = try await client.openStream(video: video.id, startSec: Int(start))
            guard let url = s.playbackURL(home: client.home) else {
                problem = "The home gave no address to play."
                return
            }
            let auth = try await client.playbackHeaders()
            if let old = session, old != s.sessionID {
                Task { await client.closeStream(old) }
            }
            session = s.sessionID
            try await client.waitUntilPlayable(url, headers: auth.headers)
            openedAt = .now
            renewAt = min(auth.until, s.expires ?? .distantFuture).addingTimeInterval(-60)
            let item = AVPlayerItem(asset: AVURLAsset(url: url, options: ["AVURLAssetHTTPHeaderFieldsKey": auth.headers]))
            let p = player ?? AVPlayer()
            p.replaceCurrentItem(with: item)
            if start > 0 { await p.seek(to: CMTime(seconds: start, preferredTimescale: 600)) }
            player = p
            p.play()
        } catch {
            player = nil
            problem = error.localizedDescription
        }
    }

    /// Every 10 s while the screen is up: the position, so "continue watching" resumes
    /// here; a reopen where it is before the URL or token ends, or once when the item failed.
    private func watch() async {
        while !Task.isCancelled {
            try? await Task.sleep(for: .seconds(10))
            guard let p = player, let session else { return }
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
                await play(from: at.isFinite ? at : 0)
                if !wasPlaying { player?.pause() }
            }
        }
    }

    private func close() {
        player?.pause()
        guard let session else { return }
        let at = player?.currentTime().seconds ?? 0
        Task {
            if at.isFinite, at > 0 { await client.reportProgress(session: session, positionSec: Int(at)) }
            await client.closeStream(session)
        }
    }
}
