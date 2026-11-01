import MaktabaKit
import SwiftUI

/// A series' page (`PlayerRoute.series`, FR-17): its art, name, year and progress,
/// **Continue** (its next episode), a season picker, and the episodes with their progress and
/// watched marks. Mark watched works on one episode or the whole season (`POST /api/me/watched`).
/// The home sends no description for a series, so none shows.
struct SeriesScreen: View {
    let context: PlayerContext
    let id: String

    @State private var model: SeriesModel

    init(context: PlayerContext, id: String) {
        self.context = context
        self.id = id
        _model = State(initialValue: SeriesModel(client: context.client, id: id, signedOut: { context.session.signedOut() }))
    }

    private var client: APIClient { context.client }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
                if let problem = model.problem, model.series == nil {
                    ProblemView(problem: problem) { Task { await model.load() } }
                } else if !model.loaded {
                    ProgressView().frame(maxWidth: .infinity, minHeight: 240)
                } else {
                    header
                    seasons
                    episodes
                }
            }
            .padding(MaktabaTokens.Spacing.s4)
            .frame(maxWidth: 900, alignment: .leading)
        }
        .maktabaSurface()
        .refreshable { await model.load() }
        .task { await model.load() }
        .alert(Text("Could not save the watched mark. Try again.", tableName: "Libraries",
                    comment: "Marking an episode or a season watched did not work"),
               isPresented: $model.markFailed) {
            Button(role: .cancel) {} label: { Text("OK", tableName: "Libraries", comment: "Closes a message") }
        }
        .navigationTitle(Text(verbatim: model.series?.name ?? ""))
        #if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
        .accessibilityIdentifier("screen.series")
    }

    private var name: String {
        guard let n = model.series?.name, !n.isEmpty else { return String(localized: SeriesGrid.untitled) }
        return n
    }

    private var header: some View {
        HStack(alignment: .top, spacing: MaktabaTokens.Spacing.s4) {
            PosterArt(url: LibraryArt.poster(client, video: model.series?.posterVideoID ?? model.episodes.first?.videoID),
                      title: name)
                .aspectRatio(2 / 3, contentMode: .fit)
                .frame(width: 120)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                Text(verbatim: name)
                    .font(.maktabaHeading(MaktabaTokens.TypeSize.h1, relativeTo: .largeTitle))
                    .foregroundStyle(Color.mktText)
                    .accessibilityAddTraits(.isHeader)
                Text(verbatim: details)
                    .font(.maktabaBody(14, relativeTo: .subheadline))
                    .foregroundStyle(Color.mktMuted)
                if model.episodeCount > 0 {
                    ProgressStrip(value: Double(model.watchedCount) / Double(model.episodeCount))
                        .frame(maxWidth: 200)
                    Text("\(model.watchedCount) of \(model.episodeCount) watched", tableName: "Libraries",
                         comment: "A series' progress: episodes watched of all")
                        .font(.maktabaBody(13, relativeTo: .caption))
                        .foregroundStyle(Color.mktMuted)
                }
                if let next = model.next {
                    NavigationLink(value: PlayerRoute.video(id: next.videoID)) {
                        Label {
                            Text("Continue", tableName: "Libraries", comment: "A series' button: open its next episode")
                        } icon: { Image(systemName: "play.fill") }
                    }
                    .buttonStyle(BrassButtonStyle())
                    .accessibilityHint(Text(verbatim: next.title))
                    .accessibilityIdentifier("series.continue")
                    .padding(.top, MaktabaTokens.Spacing.s2)
                }
            }
            .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var details: String {
        var parts = [String(localized: SeriesGrid.episodes(model.episodeCount))]
        if let y = model.series?.year { parts.append(y.formatted(.number.grouping(.never))) }
        return parts.joined(separator: " · ")
    }

    /// The season picker (only when there is more than one), and Mark watched on the season.
    private var seasons: some View {
        HStack(spacing: MaktabaTokens.Spacing.s2) {
            if model.seasons.count > 1 {
                Picker(selection: $model.season) {
                    ForEach(model.seasons) { s in Text(Self.seasonName(s.season)).tag(Optional(s.season)) }
                } label: {
                    Text("Season", tableName: "Libraries", comment: "A series' season picker, spoken")
                }
                .pickerStyle(.menu)
                .accessibilityIdentifier("series.season")
            } else if let s = model.seasons.first, s.season > 0 {
                Text(Self.seasonName(s.season))
                    .font(.maktabaHeading(MaktabaTokens.TypeSize.h2, relativeTo: .title2))
                    .foregroundStyle(Color.mktText)
                    .accessibilityAddTraits(.isHeader)
            }
            Spacer(minLength: MaktabaTokens.Spacing.s2)
            if !model.episodes.isEmpty {
                Button {
                    Task { await model.markSeason() }
                } label: {
                    Label {
                        model.seasonWatched
                            ? Text("Mark the season not watched", tableName: "Libraries", comment: "Clears the watched mark of every episode of the season")
                            : Text("Mark the season watched", tableName: "Libraries", comment: "Marks every episode of the season watched")
                    } icon: { Image(systemName: model.seasonWatched ? "eye.slash" : "checkmark.circle") }
                        .font(.maktabaBody(14, weight: .medium, relativeTo: .subheadline))
                        .frame(minHeight: 44)
                }
                .foregroundStyle(Color.mktBrassText)
                .accessibilityIdentifier("series.markSeason")
            }
        }
    }

    static func seasonName(_ n: Int) -> LocalizedStringResource {
        n > 0 ? LocalizedStringResource("Season \(n)", table: "Libraries", comment: "A season of a series, by number")
            : LocalizedStringResource("Episodes", table: "Libraries", comment: "The episodes of a series with no season")
    }

    private var episodes: some View {
        LazyVStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s3) {
            ForEach(model.episodes) { e in
                EpisodeRow(client: client, episode: e, watched: model.watched(e), progress: model.progress(e)) {
                    Task { await model.mark([e.videoID], watched: !model.watched(e)) }
                }
            }
        }
    }
}

/// An episode: its picture with the progress, number and title, length, and its watched mark
/// (a button that marks it or clears it). The row opens the episode's page.
private struct EpisodeRow: View {
    let client: APIClient
    let episode: Episode
    let watched: Bool
    let progress: Double
    let toggle: () -> Void

    @Environment(\.dynamicTypeSize) private var typeSize

    private var number: Int? { episode.episode ?? episode.absoluteNumber }

    /// The picture beside the text; at the accessibility sizes, above it (the text keeps the
    /// row's width and wraps in full, NFR-7).
    private var layout: AnyLayout {
        typeSize.isAccessibilitySize
            ? AnyLayout(VStackLayout(alignment: .leading, spacing: MaktabaTokens.Spacing.s2))
            : AnyLayout(HStackLayout(spacing: MaktabaTokens.Spacing.s3))
    }

    var body: some View {
        HStack(spacing: MaktabaTokens.Spacing.s3) {
            NavigationLink(value: PlayerRoute.video(id: episode.videoID)) {
                layout {
                    PosterArt(url: LibraryArt.poster(client, video: episode.videoID), title: episode.title, progress: progress)
                        .aspectRatio(16 / 9, contentMode: .fit)
                        .frame(width: typeSize.isAccessibilitySize ? 200 : 128)
                    VStack(alignment: .leading, spacing: 2) {
                        if let n = number {
                            Text("Episode \(n)", tableName: "Libraries", comment: "An episode's number")
                                .font(.maktabaBody(12, weight: .semibold, relativeTo: .caption))
                                .foregroundStyle(Color.mktBrassText)
                        }
                        Text(verbatim: episode.title)
                            .font(.maktabaBody(15, weight: .medium, relativeTo: .subheadline))
                            .foregroundStyle(Color.mktText)
                        if let d = episode.durationSec {
                            Text(verbatim: VideoCard.length(d))
                                .font(.maktabaBody(13, relativeTo: .caption))
                                .foregroundStyle(Color.mktMuted)
                                .monospacedDigit()
                        }
                    }
                    .multilineTextAlignment(.leading)
                    .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 0)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            // one element, as a PosterCard: the number, title and length, the progress as its value
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(label)
            .accessibilityAddTraits(.isButton)
            .accessibilityValue(progress > 0 ? ProgressStrip.spoken(progress) : Text(verbatim: ""))
            .accessibilityIdentifier("episode.\(episode.videoID)")

            Button(action: toggle) {
                Image(systemName: watched ? "checkmark.circle.fill" : "circle")
                    .font(.system(size: 22))
                    .foregroundStyle(watched ? Color.brass : Color.mktMuted)
                    .frame(width: 44, height: 44)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(watched
                ? Text("Mark not watched", tableName: "Libraries", comment: "Clears an episode's watched mark")
                : Text("Mark watched", tableName: "Libraries", comment: "Marks an episode watched"))
            .accessibilityValue(watched ? Text("Watched", tableName: "Libraries", comment: "An episode's mark: watched") : Text(verbatim: ""))
            .accessibilityIdentifier("episode.mark.\(episode.videoID)")
        }
    }

    private var label: Text {
        let rest = Text(verbatim: ([episode.title] + [episode.durationSec.map(VideoCard.length)].compactMap { $0 }).joined(separator: ", "))
        guard let number else { return rest }
        return Text("Episode \(number)", tableName: "Libraries", comment: "An episode's number") + Text(verbatim: ", ") + rest
    }
}
