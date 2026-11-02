import MaktabaKit
import SwiftUI

/// What the Live TV tab knows: what is on now on every channel the person may read
/// (`GET /api/channels/now`) and the guide's window (`GET /api/channels/guide`). It is
/// asked again once a minute, and only while the tab is on screen (NFR-16). A channel the
/// person may not read is never here: the home leaves it out (FR-24).
@MainActor @Observable
final class LiveModel {
    private(set) var now: [ChannelNow] = []
    private(set) var guide: GuideWindow?
    private(set) var problem: HomeFailure?
    private(set) var loading = true

    /// How often the tab asks again while it shows.
    static let refresh: Duration = .seconds(60)
    /// The guide's window: from the half hour before now, six hours (the web's tablet span).
    static let guideHours: Double = 6

    /// The channels in number order: the order ▲ / ▼ surf in. The guide's channels are
    /// added when `now` lacks one (it should not, both are the home's lineup).
    var channels: [LiveChannel] {
        var seen = Set<String>()
        var all: [LiveChannel] = []
        for c in now.map(\.channel) + (guide?.channels.map(\.channel) ?? []) where seen.insert(c.id).inserted {
            all.append(c)
        }
        return all.sorted { ($0.number, $0.name) < ($1.number, $1.name) }
    }

    /// What is on now and next on a channel.
    func onNow(_ channel: String) -> ChannelNow? { now.first { $0.channel.id == channel } }

    /// One channel's row in the guide.
    func row(_ channel: String) -> ChannelGuide? { guide?.channels.first { $0.channel.id == channel } }

    /// Asks again every minute while the calling task lives (the tab's `.task`).
    func keepFresh(_ client: APIClient) async {
        while !Task.isCancelled {
            await load(client)
            try? await Task.sleep(for: Self.refresh)
        }
    }

    func load(_ client: APIClient) async {
        do {
            async let n = client.channelsNow()
            let start = Self.windowStart(.now)
            async let g = client.guide(from: start, to: start.addingTimeInterval(Self.guideHours * 3600))
            let (nowAnswer, guideAnswer) = try await (n, g)
            now = nowAnswer.channels
            guide = guideAnswer
            problem = nil
        } catch {
            problem = HomeFailure.shown("load live tv", error) ?? problem
        }
        loading = false
    }

    /// The half hour at or before `date`: the guide's first column.
    static func windowStart(_ date: Date) -> Date {
        let t = date.timeIntervalSince1970
        return Date(timeIntervalSince1970: (t / 1800).rounded(.down) * 1800)
    }
}

/// The Live TV tab: **Now** (every channel with what is on and its progress) and **Guide**
/// (channels × time), over the library scene. Select on a channel, or on what is on now in
/// the guide, tunes: the live player full screen, ▲ / ▼ for the next and previous channel.
struct LiveView: View {
    enum Mode: Hashable { case now, guide }

    @Environment(\.client) private var client
    @State private var model = LiveModel()
    @State private var mode = Mode.now
    @State private var tuned: Tuned?

    /// The channel the player opens on.
    struct Tuned: Identifiable {
        let channel: String
        var id: String { channel }
    }

    var body: some View {
        ZStack {
            LibraryBackdrop()
            VStack(alignment: .leading, spacing: 32) {
                HStack(spacing: 48) {
                    SectionTitle(String(localized: "Live TV"), size: 52)
                    Spacer()
                    LiveModeButton(title: String(localized: "Now"), selected: mode == .now) { mode = .now }
                        .accessibilityIdentifier("live.mode.now")
                    LiveModeButton(title: String(localized: "Guide"), selected: mode == .guide) { mode = .guide }
                        .accessibilityIdentifier("live.mode.guide")
                }
                .focusSection()
                .readingPanel(padding: 28)
                .padding(.top, 140)

                Group {
                    if model.loading && model.now.isEmpty {
                        ProgressView().frame(maxWidth: .infinity).padding(.vertical, 80)
                    } else if let problem = model.problem, model.now.isEmpty {
                        ErrorBanner(failure: problem) { Task { if let client { await model.load(client) } } }
                    } else if model.channels.isEmpty {
                        ContentUnavailableView("No channels yet", systemImage: "dot.radiowaves.left.and.right",
                                               description: Text("Channels appear here once this home shares one with you."))
                            .accessibilityIdentifier("live.empty")
                    } else if mode == .now {
                        LiveNowGrid(model: model) { tuned = Tuned(channel: $0) }
                    } else {
                        LiveGuideView(model: model) { tuned = Tuned(channel: $0) }
                    }
                }
                .focusSection()
                .frame(maxHeight: .infinity, alignment: .top)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .padding(.horizontal, 80) // 10-foot safe-area gutter
            .padding(.bottom, 40)
        }
        .ignoresSafeArea(edges: .top)
        .task {
            guard let client else { return }
            await model.keepFresh(client)
        }
        .fullScreenCover(item: $tuned) { t in
            if let client {
                LivePlayerScreen(client: client, model: model, start: t.channel)
                    .environment(\.client, client)
            }
        }
    }
}

/// Now or Guide: a pill that turns gold when it is the one showing (focus lifts it).
private struct LiveModeButton: View {
    let title: String
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(title)
        }
        .buttonStyle(LiveModeStyle(selected: selected))
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

private struct LiveModeStyle: ButtonStyle {
    let selected: Bool

    func makeBody(configuration: Configuration) -> some View {
        Styled(configuration: configuration, selected: selected)
    }

    private struct Styled: View {
        let configuration: ButtonStyleConfiguration
        let selected: Bool
        @Environment(\.isFocused) private var focused

        var body: some View {
            configuration.label
                .font(Theme.body(30, weight: .semibold))
                .foregroundStyle(focused || selected ? Theme.onGold : Theme.text)
                .padding(.horizontal, 40)
                .padding(.vertical, 14)
                .background(focused ? Theme.goldLight : selected ? Theme.gold : Theme.raised, in: .capsule)
                .overlay(Capsule().strokeBorder(Theme.border, lineWidth: focused || selected ? 0 : 1))
                .shadow(color: focused ? Theme.goldText.opacity(0.5) : .clear, radius: 18)
                .scaleEffect(focused ? 1.06 : 1)
                .animation(.easeOut(duration: 0.15), value: focused)
        }
    }
}

// MARK: Now

/// Every channel as a card: its logo, number and name, what is on with its progress, and
/// what is next. Select tunes.
struct LiveNowGrid: View {
    let model: LiveModel
    let tune: (String) -> Void

    static let cardWidth: CGFloat = 540
    private let columns = Array(repeating: GridItem(.fixed(LiveNowGrid.cardWidth), spacing: 56), count: 3)

    var body: some View {
        ScrollView {
            // the progress bars move on their own, a little every half minute
            TimelineView(.periodic(from: .now, by: 30)) { context in
                LazyVGrid(columns: columns, spacing: 56) {
                    ForEach(model.channels) { channel in
                        Button { tune(channel.id) } label: {
                            LiveChannelCard(channel: channel, now: model.onNow(channel.id), at: context.date)
                        }
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("live.channel.\(channel.id)")
                    }
                }
                .padding(.vertical, 40) // room for the focus lift
            }
        }
        .scrollClipDisabled()
        .accessibilityIdentifier("live.now")
    }
}

/// One channel on the Now grid.
struct LiveChannelCard: View {
    let channel: LiveChannel
    let now: ChannelNow?
    let at: Date

    @Environment(\.isFocused) private var focused

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack(spacing: 20) {
                ChannelLogo(channel: channel, size: 88)
                VStack(alignment: .leading, spacing: 4) {
                    Text(channel.number, format: .number)
                        .font(Theme.body(26, weight: .semibold))
                        .foregroundStyle(Theme.goldText)
                    Text(channel.name)
                        .font(Theme.body(30, weight: .semibold))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)
                }
                Spacer(minLength: 0)
                if now?.current?.isOn(at: at) == true {
                    LiveBadge()
                }
            }
            if let current = now?.current {
                Text(current.title)
                    .font(Theme.body(28, weight: .medium))
                    .foregroundStyle(Theme.text)
                    .lineLimit(2, reservesSpace: true)
                ProgressView(value: current.progress(at: at))
                    .tint(Theme.gold)
            } else {
                Text("Nothing scheduled")
                    .font(Theme.body(28))
                    .foregroundStyle(Theme.muted)
                    .lineLimit(2, reservesSpace: true)
            }
            Text(nextLine)
                .font(Theme.body(24))
                .foregroundStyle(Theme.muted)
                .lineLimit(1)
        }
        .padding(28)
        .frame(width: LiveNowGrid.cardWidth, alignment: .leading)
        .background(Theme.surface.opacity(focused ? 1 : 0.92), in: .rect(cornerRadius: Theme.cornerRadius))
        .goldFocus(focused, scale: 1.05)
    }

    private var nextLine: String {
        guard let next = now?.next else { return " " }
        return String(localized: "Next: \(next.start.formatted(date: .omitted, time: .shortened)) \(next.title)")
    }
}

/// "Live" in the live colour: the programme is on now.
struct LiveBadge: View {
    var body: some View {
        Text("LIVE")
            .font(Theme.body(20, weight: .semibold))
            .foregroundStyle(Theme.onGold)
            .padding(.horizontal, 12)
            .padding(.vertical, 4)
            .background(Theme.live, in: .capsule)
    }
}

/// A channel's logo from the home (fetched with the token), else its number in a disc.
struct ChannelLogo: View {
    let channel: LiveChannel
    let size: CGFloat

    @Environment(\.client) private var client
    @State private var image: Image?

    var body: some View {
        ZStack {
            Circle().fill(Theme.raised)
            if let image {
                image.resizable().scaledToFit().padding(size * 0.12)
            } else {
                Text(channel.number, format: .number)
                    .font(Theme.body(size * 0.38, weight: .semibold))
                    .foregroundStyle(Theme.goldText)
            }
        }
        .frame(width: size, height: size)
        .clipShape(.circle)
        .accessibilityHidden(true)
        .task(id: channel.id) {
            image = nil
            guard let client, !(channel.logoPath ?? "").isEmpty,
                  let data = try? await client.channelLogo(channel.id),
                  let ui = UIImage(data: data) else { return }
            image = Image(uiImage: ui)
        }
    }
}
