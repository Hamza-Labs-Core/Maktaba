import MaktabaKit
import SwiftUI

/// A video's page: its picture, title, length and description, and Resume (where this
/// person stopped, focused first) or Play. Under a parental limit, a video the home does not
/// give is the blocked page (Epic 57 story 57.11).
struct VideoPage: View {
    let id: String
    @Environment(\.client) private var client
    @State private var video: Video?
    @State private var problem: HomeFailure?
    @State private var playing: TVPlayback?
    /// What played last: stopped when the player closes.
    @State private var played: TVPlayback?
    @State private var seriesID: String?
    @Environment(\.queuePlayer) private var queue
    @Environment(\.appSession) private var session
    /// Story 54.20: installed plugins' panels, the TV's subset (kv, badges, markers).
    @State private var panels: [PanelView] = []
    /// Epic 57: the home does not give this video under the account's parental limit (a
    /// hidden video answers exactly as a missing one): the blocked page, not an error.
    @State private var blocked = false
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        Group {
            if blocked, let client, let session {
                TVBlockedPage(client: client, session: session, subject: .video(id), back: { dismiss() }) {
                    Task { await load() }
                }
            } else if let video, let client {
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
                            if let seriesID {
                                NavigationLink { SeriesView(id: seriesID).parentalFresh() } label: { Label("Go to series", systemImage: "list.bullet") }
                            }
                        }
                        .buttonStyle(.gold)
                        if let d = video.description, !d.isEmpty {
                            Text(d)
                                .font(Theme.body(28))
                                .foregroundStyle(Theme.muted)
                                .lineLimit(8)
                        }
                        // story 54.20: "From plugins", open on the TV; a marker plays from its time
                        PluginPanelsSection<String>(panels: panels, play: { play(video, from: $0) })
                    }
                }
                .padding(80)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            } else if let problem {
                ErrorBanner(failure: problem) { Task { await load() } }
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
        // Epic 56: a video on its own plays in a queue, so an episode goes on through its series
        if let queue {
            queue.play(QueueSource(kind: "video", id: v.id), shuffle: false)
            return
        }
        guard let client else { return }
        let p = TVPlayback(client: client, video: v)
        if let parental = session?.parental { p.limited = { parental.current.limited } }
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
            seriesID = try? await client.myVideo(id).series?.id
            problem = nil
            blocked = false
            panels = session?.capabilities.current.panels == true
                ? ((try? await client.panels(kind: "video", id: id, language: SubjectPanels.language)) ?? [])
                : []
        } catch {
            if session?.parental.current.limited == true, !Log.isCancellation(error), HomeFailure(error).kind == .notFound {
                blocked = true
                return
            }
            problem = HomeFailure.shown("load video", error) ?? problem
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
