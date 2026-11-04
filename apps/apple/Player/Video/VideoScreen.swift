import MaktabaKit
import SwiftUI

/// A video's page (`PlayerRoute.video`, FR-18): the poster at the top at its own shape, the
/// series and episode, the title, length and date; **Resume** from `playback_state` with
/// **Play from start**, or **Play**; **Favorite** and **Mark watched**; the description;
/// **Chapters** (each plays from its time); a transcript excerpt; in a series, the next
/// episodes. A 404 says "not available"; `maktaba://video/{id}` lands here too (`VideoLink`).
///
/// The poster is the small copy (`?size=small`) on a phone and the full one on an iPad and the
/// Mac (`VideoPoster`). Coming back to the page (from the player) reads the home again.
struct VideoScreen: View {
    let context: PlayerContext
    let id: String

    @State private var page: VideoPage
    /// The scroll view's size: the poster's largest box.
    @State private var area = CGSize(width: 390, height: 800)

    init(context: PlayerContext, id: String) {
        self.context = context
        self.id = id
        _page = State(initialValue: VideoPage(client: context.client, id: id))
    }

    var body: some View {
        content(page)
            .maktabaSurface()
            .navigationTitle(Text(verbatim: page.video?.displayTitle ?? ""))
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .onGeometryChange(for: CGSize.self) { $0.size } action: { area = $0 }
            // each time the page shows: coming back from the player reads the home again
            .task { await page.load() }
            .alert(Text("Couldn't save that. Try again.", tableName: "Video", comment: "A favorite or watched mark the home did not take"),
                   isPresented: Binding { page.saveFailed } set: { page.saveFailed = $0 }) {}
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("screen.video")
    }

    @ViewBuilder private func content(_ page: VideoPage) -> some View {
        if page.notAvailable {
            ContentUnavailableView {
                Label {
                    Text("This video isn't available", tableName: "Video", comment: "A video that does not exist or this person may not see")
                    .accessibilityIdentifier("video.notAvailable")
                } icon: {
                    Image(systemName: "eye.slash")
                }
            } description: {
                Text("It may have been removed, or your account can't see it.", tableName: "Video",
                     comment: "Why a video is not available")
            }
        } else if let problem = page.problem {
            HomeErrorView(problem, video: false) { _ in Task { await page.load() } }
        } else if let video = page.video {
            VideoDetails(page: page, video: video, client: context.client, area: area,
                         caps: context.session.capabilities.current)
                .refreshable { await page.load() }
        } else {
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

/// The page once the video has loaded.
private struct VideoDetails: View {
    let page: VideoPage
    let video: VideoInfo
    let client: APIClient
    let area: CGSize
    /// What the home has on (51.11).
    var caps: Capabilities = .allOn

    @Environment(\.dynamicTypeSize) private var typeSize
    private let gutter = MaktabaTokens.Spacing.s4
    private static let maxWidth: CGFloat = 840

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s3) {
                VideoPoster(title: video.displayTitle,
                            url: VideoPoster.url(client.home, video: video.id, small: VideoPoster.smallCopy),
                            width: min(area.width, Self.maxWidth) - 2 * gutter,
                            maxHeight: VideoPoster.maxHeight(page: area.height))
                    .padding(.horizontal, gutter)
                facts.padding(.horizontal, gutter)
                actions.padding(.horizontal, gutter)
                if let d = video.description {
                    Text(verbatim: d)
                        .font(.maktabaBody(16, relativeTo: .body))
                        .foregroundStyle(Color.mktText)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.horizontal, gutter)
                        .accessibilityIdentifier("video.description")
                }
                // chapters and the transcript: left out while off on the home, and explained
                // to an admin (51.11)
                if caps.shown(.chapters) == .explain {
                    section(Text("Chapters", tableName: "Video", comment: "A video's chapters, a heading"))
                    FeatureOffNote(.chapters, home: client.home, compact: true).padding(.horizontal, gutter)
                } else if caps.isOn(.chapters), !page.chapters.isEmpty {
                    section(Text("Chapters", tableName: "Video", comment: "A video's chapters, a heading"))
                    VStack(spacing: 0) {
                        ForEach(page.chapters) { c in
                            TimedLine(videoID: video.id, time: c.startSec,
                                      text: c.title.isEmpty ? Self.clock(c.startSec) : c.title)
                                .accessibilityIdentifier("video.chapter.\(c.seq)")
                        }
                    }
                }
                if caps.shown(.transcripts) == .explain {
                    section(Text("From the transcript", tableName: "Video", comment: "A few lines of a video's transcript, a heading"))
                    FeatureOffNote(.transcripts, home: client.home, compact: true).padding(.horizontal, gutter)
                } else if caps.isOn(.transcripts), !page.transcript.isEmpty {
                    section(Text("From the transcript", tableName: "Video", comment: "A few lines of a video's transcript, a heading"))
                    VStack(spacing: 0) {
                        ForEach(page.transcript) { s in
                            TimedLine(videoID: video.id, time: s.startSec, text: s.text)
                                .accessibilityIdentifier("video.segment.\(s.seq)")
                        }
                    }
                }
                if !page.nextEpisodes.isEmpty {
                    Rail(Text("Next episodes", tableName: "Video", comment: "The episodes after this one, a row's title"),
                         items: page.nextEpisodes) { e in
                        NavigationLink(value: PlayerRoute.video(id: e.videoID)) {
                            PosterCard(title: e.title.isEmpty ? (Self.episodeLabel(e) ?? e.videoID) : e.title,
                                       detail: Self.episodeLabel(e),
                                       poster: VideoPoster.url(client.home, video: e.videoID, small: true),
                                       progress: e.progress > 0 ? e.progress : nil)
                        }
                        .buttonStyle(.plain)
                    }
                    .padding(.top, MaktabaTokens.Spacing.s4)
                    .accessibilityIdentifier("video.next")
                }
            }
            .frame(maxWidth: Self.maxWidth, alignment: .leading)
            .padding(.bottom, MaktabaTokens.Spacing.s8)
            .frame(maxWidth: .infinity)
        }
        .accessibilityIdentifier("video.details")
    }

    /// The series and episode, the title, the length and date.
    private var facts: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s1) {
            if let s = page.series {
                NavigationLink(value: PlayerRoute.series(id: s.id)) {
                    Text(verbatim: [s.name.isEmpty ? nil : s.name, page.episode.flatMap(Self.episodeLabel)]
                        .compactMap { $0 }.joined(separator: " · "))
                        .font(.maktabaBody(14, weight: .semibold, relativeTo: .subheadline))
                        .foregroundStyle(Color.mktBrassText)
                        .frame(minHeight: 44, alignment: .leading)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("video.series")
            }
            Text(verbatim: video.displayTitle)
                .font(.maktabaHeading(MaktabaTokens.TypeSize.h1, relativeTo: .largeTitle))
                .foregroundStyle(Color.mktText)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityAddTraits(.isHeader)
                .accessibilityIdentifier("video.title")
            let facts = [video.durationSec.flatMap { $0 > 0 ? Self.clock($0) : nil },
                         (video.publishedAt ?? video.createdAt).map { $0.formatted(date: .abbreviated, time: .omitted) }]
                .compactMap { $0 }
            if !facts.isEmpty {
                Text(verbatim: facts.joined(separator: " · "))
                    .font(.maktabaBody(15, relativeTo: .subheadline))
                    .foregroundStyle(Color.mktMuted)
                    .accessibilityIdentifier("video.facts")
            }
        }
        .multilineTextAlignment(.leading)
    }

    /// Resume (and Play from start), or Play; Favorite and Mark watched.
    private var actions: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s3) {
            let resumeAt = page.resumeAt
            if let resumeAt, let d = video.durationSec, d > 0 {
                ProgressStrip(value: resumeAt / d).frame(maxWidth: 320)
            }
            // side by side where both fit (a wide page); stacked on a phone and at the larger
            // text sizes, where the labels wrap rather than cut off (NFR-7)
            let playLayout = typeSize >= .xLarge || area.width < 600
                ? AnyLayout(VStackLayout(alignment: .leading, spacing: MaktabaTokens.Spacing.s2))
                : AnyLayout(HStackLayout(spacing: MaktabaTokens.Spacing.s3))
            playLayout { playButtons(resumeAt) }
            // side by side; stacked at the larger text sizes, where two no longer fit (NFR-7)
            let marksLayout = typeSize >= .xxLarge
                ? AnyLayout(VStackLayout(alignment: .leading, spacing: MaktabaTokens.Spacing.s2))
                : AnyLayout(HStackLayout(spacing: MaktabaTokens.Spacing.s2))
            marksLayout { marks }
        }
    }

    @ViewBuilder private func playButtons(_ resumeAt: Double?) -> some View {
        if let resumeAt {
            NavigationLink(value: PlayerRoute.watch(videoID: video.id, at: resumeAt)) {
                Label {
                    Text("Resume from \(Self.clock(resumeAt))", tableName: "Video", comment: "Play on from where the person stopped; the time")
                        .fixedSize(horizontal: false, vertical: true)
                } icon: { Image(systemName: "play.fill") }
            }
            .buttonStyle(BrassButtonStyle())
            .accessibilityIdentifier("video.resume")
            NavigationLink(value: PlayerRoute.watch(videoID: video.id, at: 0)) {
                Label {
                    Text("Play from start", tableName: "Video", comment: "Play a started video from its beginning")
                        .fixedSize(horizontal: false, vertical: true)
                } icon: { Image(systemName: "arrow.counterclockwise") }
            }
            .buttonStyle(OutlineButtonStyle())
            .accessibilityIdentifier("video.playFromStart")
        } else {
            NavigationLink(value: PlayerRoute.watch(videoID: video.id, at: 0)) {
                Label {
                    Text("Play", tableName: "Video", comment: "Play the video")
                } icon: { Image(systemName: "play.fill") }
            }
            .buttonStyle(BrassButtonStyle())
            .accessibilityIdentifier("video.play")
        }
    }

    @ViewBuilder private var marks: some View {
        MarkButton(on: page.favorite, symbol: "heart", id: "video.favorite") {
            Text("Favorite", tableName: "Video", comment: "Keep the video among the person's favorites (a toggle)")
        } action: { Task { await page.toggleFavorite() } }
        MarkButton(on: page.watched, symbol: "checkmark.circle", id: "video.watched") {
            Text("Mark watched", tableName: "Video", comment: "Mark the video watched (a toggle)")
        } action: { Task { await page.toggleWatched() } }
    }

    private func section(_ title: Text) -> some View {
        title
            .font(.maktabaHeading(MaktabaTokens.TypeSize.h2, relativeTo: .title2))
            .foregroundStyle(Color.mktText)
            .accessibilityAddTraits(.isHeader)
            .padding(.horizontal, gutter)
            .padding(.top, MaktabaTokens.Spacing.s4)
    }

    /// 7:05 or 1:02:05.
    static func clock(_ seconds: Double) -> String {
        let s = max(0, seconds.isFinite ? seconds : 0)
        return Duration.seconds(Int(s)).formatted(.time(pattern: s >= 3600 ? .hourMinuteSecond : .minuteSecond))
    }

    /// "Season 1, Episode 3", or "Episode 3"; nil when the series does not number it.
    static func episodeLabel(_ e: Episode) -> String? {
        guard let n = e.episode ?? e.absoluteNumber else { return nil }
        if let season = e.season, season > 0 {
            return String(localized: LocalizedStringResource("Season \(season), Episode \(n)", table: "Video",
                                                             comment: "An episode's place in its series"))
        }
        return String(localized: LocalizedStringResource("Episode \(n)", table: "Video",
                                                         comment: "An episode's number in a series with no seasons"))
    }
}

/// A line that plays from its time: a chapter, a transcript line.
private struct TimedLine: View {
    let videoID: String
    let time: Double
    let text: String

    var body: some View {
        NavigationLink(value: PlayerRoute.watch(videoID: videoID, at: time)) {
            HStack(alignment: .firstTextBaseline, spacing: MaktabaTokens.Spacing.s3) {
                Text(verbatim: VideoDetails.clock(time))
                    .font(.maktabaBody(14, weight: .semibold, relativeTo: .subheadline).monospacedDigit())
                    .foregroundStyle(Color.mktBrassText)
                Text(verbatim: text)
                    .font(.maktabaBody(15, relativeTo: .body))
                    .foregroundStyle(Color.mktText)
                    .multilineTextAlignment(.leading)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 0)
            }
            .padding(.horizontal, MaktabaTokens.Spacing.s4)
            .padding(.vertical, MaktabaTokens.Spacing.s2)
            .frame(minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .combine)
        .accessibilityHint(Text("Play from \(VideoDetails.clock(time))", tableName: "Video",
                                comment: "What a chapter or transcript line does: play from its time"))
    }
}

/// Favorite or Mark watched: a chip, filled when on; VoiceOver hears it as a selected button.
private struct MarkButton<Label: View>: View {
    let on: Bool
    let symbol: String
    let id: String
    @ViewBuilder let label: () -> Label
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            SwiftUI.Label {
                // wraps rather than cut off when the text grows (NFR-7)
                label().fixedSize(horizontal: false, vertical: true)
            } icon: {
                // decorative (a checkmark symbol would otherwise read as "selected")
                Image(systemName: on ? "\(symbol).fill" : symbol).accessibilityHidden(true)
            }
            .font(.maktabaBody(16, weight: .semibold, relativeTo: .body))
            .padding(.vertical, MaktabaTokens.Spacing.s2)
            .foregroundStyle(on ? MaktabaTokens.Colors.accentFg.color : Color.mktBrassText)
            .padding(.horizontal, MaktabaTokens.Spacing.s4)
            .frame(minHeight: 44)
            .background(Capsule().fill(on ? Color.brass : Color.mktSurface))
            .overlay(Capsule().strokeBorder(Color.brass.opacity(on ? 0 : 0.6), lineWidth: 1))
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(on ? .isSelected : [])
        .accessibilityIdentifier(id)
    }
}

/// A brass outline on the page, at least 44 pt tall: the second play button.
private struct OutlineButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.maktabaBody(16, weight: .semibold, relativeTo: .body))
            .foregroundStyle(Color.mktBrassText)
            .padding(.horizontal, MaktabaTokens.Spacing.s5)
            .padding(.vertical, MaktabaTokens.Spacing.s3)
            .frame(minHeight: 44)
            .overlay(Capsule().strokeBorder(Color.brass, lineWidth: 1.5))
            .opacity(configuration.isPressed ? 0.7 : 1)
            .contentShape(Capsule())
    }
}
