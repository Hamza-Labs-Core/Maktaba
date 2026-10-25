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
        do {
            let s = try await client.openStream(video: video.id)
            guard let url = s.playbackURL(home: client.home) else {
                problem = "The home gave no address to play."
                return
            }
            session = s.sessionID
            let p = AVPlayer(url: url)
            player = p
            p.play()
            await reportWhilePlaying(p, session: s.sessionID)
        } catch {
            problem = error.localizedDescription
        }
    }

    /// Every 10 s while the screen is up: the position, so "continue watching" resumes here.
    private func reportWhilePlaying(_ p: AVPlayer, session: String) async {
        while !Task.isCancelled {
            try? await Task.sleep(for: .seconds(10))
            let at = p.currentTime().seconds
            if at.isFinite, at > 0 { await client.reportProgress(session: session, positionSec: Int(at)) }
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
