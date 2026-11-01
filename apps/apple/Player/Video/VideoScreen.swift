import MaktabaKit
import SwiftUI

/// A video's page as it loads (FR-18): the video (`GET /api/videos/{id}`, with the caller's
/// `playback_state`), the caller's own marks (`GET /api/me/videos/{id}`: favorite, watched, the
/// series), its chapters, a transcript excerpt and, in a series, the next episodes. Only the
/// video itself is required: a 404 (or the 403 of a library this person has no access to) is
/// `notAvailable`, since the home does not tell a hidden video from a missing one; the rest is
/// left out when it fails. As Android's `VideoPage` (48.28).
@MainActor @Observable
final class VideoPage {
    /// How many lines of the transcript the page shows.
    static let transcriptLines = 5
    /// How many of a series' next episodes the page offers.
    static let nextEpisodeCount = 10

    let id: String
    private let client: APIClient

    private(set) var video: VideoInfo?
    private(set) var series: SeriesRef?
    /// This video as its series lists it (its season and episode).
    private(set) var episode: Episode?
    private(set) var chapters: [Chapter] = []
    private(set) var transcript: [TranscriptSegment] = []
    private(set) var nextEpisodes: [Episode] = []
    private(set) var favorite = false
    private(set) var watched = false
    private(set) var notAvailable = false
    private(set) var problem: HomeFailure?
    /// A mark the home did not take (the toggle went back).
    var saveFailed = false
    /// Marked watched or not since the last load: the home starts the video over either way.
    private var startsOver = false

    init(client: APIClient, id: String) {
        self.client = client
        self.id = id
    }

    /// Where Resume opens, in seconds: the saved position, unless watched or not started.
    var resumeAt: Double? { watched || startsOver ? nil : video?.resumeAt }

    func load() async {
        problem = nil
        async let detail = client.videoDetail(id)
        async let mine = optional { try await self.client.myVideo(self.id) }
        async let chapterList = optional { try await self.client.chapters(video: self.id) }
        async let lines = optional { try await self.client.transcript(video: self.id, limit: Self.transcriptLines) }
        do {
            let v = try await detail
            let m = await mine
            video = v
            notAvailable = false
            startsOver = false
            favorite = m?.favorite ?? false
            watched = m?.completed ?? (v.playbackState?.completed ?? false)
            series = m?.series
            chapters = (await chapterList ?? []).sorted { $0.startSec < $1.startSec }
            transcript = Array((await lines)?.segments.prefix(Self.transcriptLines) ?? [])
            if let s = m?.series, let all = await optional({ try await self.client.episodes(series: s.id) }) {
                let ordered = Self.ordered(all.episodes)
                let at = ordered.firstIndex { $0.videoID == id }
                episode = at.map { ordered[$0] }
                nextEpisodes = at.map { Array(ordered.dropFirst($0 + 1).prefix(Self.nextEpisodeCount)) } ?? []
            } else {
                episode = nil
                nextEpisodes = []
            }
        } catch {
            if Task.isCancelled || error is CancellationError { return }
            if (error as? BrowseError) == .notAvailable {
                notAvailable = true
            } else if video == nil {
                problem = HomeFailure(error)
            }
        }
    }

    /// Favorite on or off (`POST /api/me/favorites`); the toggle goes back when the home does
    /// not take it.
    func toggleFavorite() async {
        let want = !favorite
        favorite = want
        do {
            try await client.markFavorite([id], want)
        } catch {
            favorite = !want
            saveFailed = true
        }
    }

    /// Watched on or off (`POST /api/me/watched`); the home starts the video over either way
    /// (position 0), so Resume goes.
    func toggleWatched() async {
        let want = !watched
        let before = startsOver
        watched = want
        startsOver = true
        do {
            try await client.markWatched([id], want)
        } catch {
            watched = !want
            startsOver = before
            saveFailed = true
        }
    }

    /// A series' episodes by season, then episode (or absolute number), as they are watched.
    static func ordered(_ episodes: [Episode]) -> [Episode] {
        episodes.enumerated().sorted { a, b in
            let x = (a.element.season ?? 0, a.element.episode ?? a.element.absoluteNumber ?? Int.max, a.offset)
            let y = (b.element.season ?? 0, b.element.episode ?? b.element.absoluteNumber ?? Int.max, b.offset)
            return x < y
        }.map(\.element)
    }

    private nonisolated func optional<T: Sendable>(_ work: @Sendable () async throws -> T) async -> T? {
        try? await work()
    }
}

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
            VideoDetails(page: page, video: video, client: context.client, area: area)
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
                if !page.chapters.isEmpty {
                    section(Text("Chapters", tableName: "Video", comment: "A video's chapters, a heading"))
                    VStack(spacing: 0) {
                        ForEach(page.chapters) { c in
                            TimedLine(videoID: video.id, time: c.startSec,
                                      text: c.title.isEmpty ? Self.clock(c.startSec) : c.title)
                                .accessibilityIdentifier("video.chapter.\(c.seq)")
                        }
                    }
                }
                if !page.transcript.isEmpty {
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
