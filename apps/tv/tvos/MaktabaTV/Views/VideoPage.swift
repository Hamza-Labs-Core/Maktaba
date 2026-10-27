import MaktabaKit
import SwiftUI

/// A video's page: its picture, title, length and description, and Resume (where this
/// person stopped, focused first) or Play.
struct VideoPage: View {
    let id: String
    @Environment(\.client) private var client
    @State private var video: Video?
    @State private var problem: AppError?
    @State private var playing: TVPlayback?
    /// What played last: stopped when the player closes.
    @State private var played: TVPlayback?

    var body: some View {
        Group {
            if let video, let client {
                HStack(alignment: .top, spacing: 64) {
                    AuthorizedImage(client: client, video: video.id)
                        .frame(width: 880)
                        .clipShape(.rect(cornerRadius: Theme.cornerRadius))
                    VStack(alignment: .leading, spacing: 28) {
                        Text(video.displayTitle)
                            .font(Theme.heading(56))
                            .foregroundStyle(Theme.text)
                            .lineLimit(3)
                        if let d = video.durationSec, d > 0 {
                            Text(Self.clock(d)).font(Theme.body(29)).foregroundStyle(Theme.muted)
                        }
                        HStack(spacing: 32) {
                            if let at = Self.resumePoint(video) {
                                Button("Resume from \(Self.clock(at))", systemImage: "play.fill") { play(video, from: at) }
                                Button("Play from the start", systemImage: "arrow.counterclockwise") { play(video, from: 0) }
                            } else {
                                Button("Play", systemImage: "play.fill") { play(video, from: 0) }
                            }
                        }
                        .buttonStyle(.gold)
                        if let d = video.description, !d.isEmpty {
                            Text(d)
                                .font(Theme.body(28))
                                .foregroundStyle(Theme.muted)
                                .lineLimit(8)
                        }
                    }
                }
                .padding(80)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            } else if let problem {
                ErrorBanner(error: problem) { Task { await load() } }
            } else {
                ProgressView()
            }
        }
        .background(Theme.background.ignoresSafeArea())
        .task { await load() }
        .fullScreenCover(item: $playing, onDismiss: stopped) { p in
            PlayerScreen(playback: p)
        }
    }

    private func play(_ v: Video, from at: Double) {
        guard let client else { return }
        let p = TVPlayback(client: client, video: v)
        p.start(at: at)
        played = p
        playing = p
    }

    /// The player closed: stop (the home keeps where), then show the page as it is now.
    private func stopped() {
        let p = played
        played = nil
        Task {
            await p?.stop()
            await load()
        }
    }

    private func load() async {
        guard let client else { return }
        do {
            video = try await client.video(id)
            problem = nil
        } catch {
            problem = AppError.shown("load video", error) ?? problem
        }
    }

    /// Where to resume: where the person stopped, unless at the very start, finished, or
    /// within the last 10 s.
    static func resumePoint(_ v: Video) -> Double? {
        guard let s = v.playbackState, !s.completed, s.positionSec >= 10 else { return nil }
        if let d = v.durationSec, d > 0, s.positionSec > d - 10 { return nil }
        return s.positionSec
    }

    static func clock(_ seconds: Double) -> String {
        Duration.seconds(seconds.rounded(.down))
            .formatted(.time(pattern: seconds >= 3600 ? .hourMinuteSecond : .minuteSecond))
    }
}

extension TVPlayback: Identifiable {
    nonisolated var id: ObjectIdentifier { ObjectIdentifier(self) }
}
