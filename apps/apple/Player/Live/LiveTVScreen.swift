import MaktabaKit
import SwiftUI

/// The Live TV section's first screen (FR-21, FR-22): **Now** (every channel with what is on,
/// its progress and what is next: a list on a phone, a grid on a tablet or the Mac) and
/// **Guide** (channels × time). Tapping a channel, or Watch on what is on now, opens the live
/// player (`PlayerRoute.channel`). Shown only when the person may watch a channel
/// (`LiveSectionGate`).
struct LiveTVScreen: View {
    let context: PlayerContext

    enum Mode: String, Hashable { case now, guide }

    @State private var model = LiveModel()
    /// A channel opened from a programme's sheet (Watch).
    @State private var watching: String?
    @SceneStorage("live.mode") private var mode = Mode.now
    @Environment(\.horizontalSizeClass) private var sizeClass

    var body: some View {
        VStack(spacing: 0) {
            Picker(selection: $mode) {
                Text("Now", tableName: "Live", comment: "Live TV: what is on now on every channel").tag(Mode.now)
                Text("Guide", tableName: "Live", comment: "Live TV: the programme guide").tag(Mode.guide)
            } label: {
                Text("View", tableName: "Live", comment: "Live TV: choose Now or Guide")
            }
            .pickerStyle(.segmented)
            .frame(maxWidth: 420)
            .padding(.horizontal, MaktabaTokens.Spacing.s4)
            .padding(.vertical, MaktabaTokens.Spacing.s2)
            .accessibilityIdentifier("live.mode")

            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .maktabaSurface()
        .navigationTitle(Text(PlayerSection.liveTV.title))
        #if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("screen.liveTV")
        .navigationDestination(item: $watching) { ChannelScreen(context: context, id: $0) }
        .sheet(item: Binding(get: { model.programme }, set: { model.programme = $0 })) { block in
            LiveProgrammeSheet(block: block, channel: model.channels.first { $0.id == block.channelID }) { watching = $0 }
        }
        .task(id: sizeClass) {
            model.hours = sizeClass == .regular ? 6 : 3
            await model.keepFresh(context.client)
        }
    }

    @ViewBuilder private var content: some View {
        if model.loading && model.now.isEmpty {
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let problem = model.problem, model.now.isEmpty {
            HomeErrorView(problem, video: false) { _ in Task { await model.load(context.client) } }
        } else if model.channels.isEmpty {
            ContentUnavailableView {
                Label { Text("No channels yet", tableName: "Live", comment: "Live TV with no channel to watch") }
                    icon: { Image(systemName: PlayerSection.liveTV.systemImage) }
            } description: {
                Text("Channels show here once the home shares one with you.", tableName: "Live",
                     comment: "Live TV with no channel: why, and when that changes")
            }
            .accessibilityIdentifier("live.empty")
        } else if mode == .now {
            LiveNowView(model: model, client: context.client)
        } else {
            LiveGuideView(model: model, client: context.client)
        }
    }
}

// MARK: Now

/// Every channel with what is on: a list on a phone, a grid of cards when there is room.
struct LiveNowView: View {
    let model: LiveModel
    let client: APIClient

    @Environment(\.horizontalSizeClass) private var sizeClass

    var body: some View {
        // the progress bars move on their own, a little every half minute
        TimelineView(.periodic(from: .now, by: 30)) { context in
            if sizeClass == .regular {
                ScrollView {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 320), spacing: MaktabaTokens.Spacing.s4)],
                              spacing: MaktabaTokens.Spacing.s4) {
                        ForEach(model.channels) { channel in
                            NavigationLink(value: PlayerRoute.channel(id: channel.id)) {
                                LiveChannelRow(channel: channel, now: model.onNow(channel.id), at: context.date,
                                               logo: client.channelLogoURL(channel))
                                    .padding(MaktabaTokens.Spacing.s4)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .background(Color.mktSurface, in: .rect(cornerRadius: MaktabaTokens.Radius.lg))
                            }
                            .buttonStyle(.plain)
                            .accessibilityIdentifier("live.channel.\(channel.id)")
                        }
                    }
                    .padding(MaktabaTokens.Spacing.s4)
                }
                .accessibilityIdentifier("live.now")
            } else {
                List(model.channels) { channel in
                    NavigationLink(value: PlayerRoute.channel(id: channel.id)) {
                        LiveChannelRow(channel: channel, now: model.onNow(channel.id), at: context.date,
                                       logo: client.channelLogoURL(channel))
                    }
                    .listRowBackground(Color.mktSurface)
                    .accessibilityIdentifier("live.channel.\(channel.id)")
                }
                .listStyle(.plain)
                .maktabaSurface()
                .accessibilityIdentifier("live.now")
            }
        }
    }
}

/// One channel on Now: its logo, number and name, what is on with its progress, what is next.
struct LiveChannelRow: View {
    let channel: LiveChannel
    let now: ChannelNow?
    let at: Date
    let logo: URL?

    var body: some View {
        HStack(alignment: .top, spacing: MaktabaTokens.Spacing.s3) {
            ChannelLogo(name: channel.name, number: channel.number, logo: logo, size: 52)
            VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s1) {
                Text(verbatim: "\(channel.number.formatted()) · \(channel.name)")
                    .font(.maktabaBody(MaktabaTokens.TypeSize.sm, weight: .semibold))
                    .foregroundStyle(Color.mktBrassText)
                    .lineLimit(1)
                if let current = now?.current {
                    Text(current.title)
                        .font(.maktabaBody(MaktabaTokens.TypeSize.base, weight: .medium))
                        .foregroundStyle(Color.mktText)
                        .lineLimit(2)
                    LiveProgress(block: current, at: at)
                } else {
                    Text("Nothing scheduled", tableName: "Live", comment: "Live TV: a channel with nothing on now")
                        .font(.maktabaBody(MaktabaTokens.TypeSize.base))
                        .foregroundStyle(Color.mktMuted)
                }
                if let next = now?.next {
                    LiveNextLine(block: next)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.vertical, MaktabaTokens.Spacing.s1)
        .contentShape(.rect)
        .accessibilityElement(children: .combine)
    }
}

/// What is on's progress: start, a brass bar, stop. VoiceOver reads the percent.
struct LiveProgress: View {
    let block: GuideBlock
    let at: Date

    var body: some View {
        HStack(spacing: MaktabaTokens.Spacing.s2) {
            Text(block.start, format: .dateTime.hour().minute())
            ProgressView(value: block.progress(at: at))
                .tint(.brass)
                .accessibilityLabel(Text("\(Int((block.progress(at: at) * 100).rounded()))% through", tableName: "Live",
                                         comment: "Live TV: how far into the programme on now, spoken"))
            Text(block.stop, format: .dateTime.hour().minute())
        }
        .font(.maktabaBody(12))
        .foregroundStyle(Color.mktMuted)
        .accessibilityIdentifier("live.progress")
    }
}

/// "Next: 21:30 Tafsir".
struct LiveNextLine: View {
    let block: GuideBlock

    var body: some View {
        Text("Next: \(block.start.formatted(date: .omitted, time: .shortened)) \(block.title)", tableName: "Live",
             comment: "Live TV: what is on next, its start time then its title")
            .font(.maktabaBody(MaktabaTokens.TypeSize.sm))
            .foregroundStyle(Color.mktMuted)
            .lineLimit(1)
    }
}
