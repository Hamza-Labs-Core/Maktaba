import MaktabaKit
import SwiftUI

/// The guide: channels down the side, six hours across from the half hour before now, a
/// line at now. Every programme is a focusable block (the focus engine moves between them
/// by where they are, so ▲ / ▼ lands on what is on at the same time on the next channel).
/// Select on what is on now tunes; on a later one it shows the programme (title, time,
/// description). Under Arabic the time runs right to left, as the layout does.
struct LiveGuideView: View {
    let model: LiveModel
    let tune: (String) -> Void

    @State private var detail: GuideBlock?
    /// The focused programme (`GuideBlock.id`): what is on now on the first channel when
    /// the guide opens.
    @FocusState private var focused: String?

    /// Epic 46's 10-foot sizes: 28 pt text, rows tall enough for two lines, half an hour
    /// as wide as a poster card's half.
    static let pointsPerMinute: CGFloat = 8
    static let rowHeight: CGFloat = 120
    static let channelWidth: CGFloat = 320
    static let spacing: CGFloat = 8

    var body: some View {
        if let window = model.guide {
            TimelineView(.periodic(from: .now, by: 30)) { context in
                grid(window, at: context.date)
            }
            // moving into the guide lands on what is on now on the first channel (set as the
            // focus moves in, not by hand at layout: that scrolled the grid before it had a size)
            .defaultFocus($focused, firstOnNow, priority: .userInitiated)
            .focusSection()
            .sheet(item: $detail) { block in
                GuideDetail(block: block, channel: model.channels.first { $0.id == block.channelID }) {
                    detail = nil
                    tune(block.channelID)
                }
            }
        } else {
            ProgressView().frame(maxWidth: .infinity).padding(.vertical, 80)
        }
    }

    /// What is on now on the first channel that has something on (its `GuideBlock.id`).
    private var firstOnNow: String? {
        let now = Date.now
        return model.channels.lazy.compactMap { model.row($0.id)?.block(at: now) }.first?.id
    }

    private func grid(_ window: GuideWindow, at now: Date) -> some View {
        let start = window.start
        let end = window.end
        let width = Self.width(from: start, to: end)
        return ScrollView(.vertical) {
            HStack(alignment: .top, spacing: 24) {
                // the channels: not focusable, they scroll with their rows
                VStack(alignment: .leading, spacing: Self.spacing) {
                    Color.clear.frame(height: 48)
                    ForEach(model.channels) { channel in
                        GuideChannelLabel(channel: channel)
                            .frame(width: Self.channelWidth, height: Self.rowHeight, alignment: .leading)
                    }
                }
                .frame(width: Self.channelWidth)
                .padding(.vertical, 16) // as the programmes' (room for the focus lift)
                ScrollView(.horizontal) {
                    VStack(alignment: .leading, spacing: Self.spacing) {
                        timeline(from: start, to: end)
                        ForEach(model.channels) { channel in
                            GuideRow(blocks: model.row(channel.id)?.blocks ?? [], channelID: channel.id,
                                     start: start, end: end, now: now, focused: $focused) { block in
                                if block.isOn(at: now) { tune(block.channelID) } else { detail = block }
                            }
                            .frame(width: width, height: Self.rowHeight, alignment: .leading)
                        }
                    }
                    .overlay(alignment: .topLeading) {
                        nowLine(start: start, end: end, now: now)
                    }
                    .padding(.vertical, 16)
                }
                .scrollClipDisabled()
            }
            .padding(.vertical, 16)
        }
        .accessibilityIdentifier("live.guide")
    }

    /// The half hours along the top.
    private func timeline(from start: Date, to end: Date) -> some View {
        let slots = Int((end.timeIntervalSince(start) / 1800).rounded(.down))
        return HStack(spacing: 0) {
            ForEach(0..<max(slots, 0), id: \.self) { i in
                Text(start.addingTimeInterval(Double(i) * 1800), format: .dateTime.hour().minute())
                    .font(Theme.body(26, weight: .medium))
                    .foregroundStyle(Theme.muted)
                    .frame(width: Self.width(minutes: 30), height: 48, alignment: .leading)
            }
        }
    }

    @ViewBuilder
    private func nowLine(start: Date, end: Date, now: Date) -> some View {
        if now >= start, now < end {
            Rectangle()
                .fill(Theme.gold)
                .frame(width: 4)
                .padding(.leading, Self.width(from: start, to: now))
                .allowsHitTesting(false)
                .accessibilityHidden(true)
        }
    }

    static func width(minutes: Double) -> CGFloat { CGFloat(minutes) * pointsPerMinute }

    static func width(from a: Date, to b: Date) -> CGFloat { width(minutes: max(0, b.timeIntervalSince(a) / 60)) }
}

/// A channel at the start of its row.
private struct GuideChannelLabel: View {
    let channel: LiveChannel

    var body: some View {
        HStack(spacing: 16) {
            ChannelLogo(channel: channel, size: 64)
            VStack(alignment: .leading, spacing: 2) {
                Text(channel.number, format: .number)
                    .font(Theme.body(24, weight: .semibold))
                    .foregroundStyle(Theme.goldText)
                Text(channel.name)
                    .font(Theme.body(28, weight: .semibold))
                    .foregroundStyle(Theme.text)
                    .lineLimit(2)
            }
        }
        .padding(.horizontal, 16)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
        .background(Theme.surface.opacity(0.9), in: .rect(cornerRadius: Theme.cornerRadius))
    }
}

/// One channel's programmes in the window, each as wide as it lasts (cut at the window's
/// edges), with gaps where nothing is planned.
private struct GuideRow: View {
    let blocks: [GuideBlock]
    let channelID: String
    let start: Date
    let end: Date
    let now: Date
    let focused: FocusState<String?>.Binding
    let select: (GuideBlock) -> Void

    var body: some View {
        HStack(spacing: 0) {
            ForEach(Array(pieces.enumerated()), id: \.offset) { _, piece in
                switch piece {
                case let .gap(width):
                    Color.clear.frame(width: width)
                case let .block(block, width):
                    Button { select(block) } label: {
                        GuideBlockLabel(block: block, now: now)
                    }
                    .buttonStyle(GuideBlockStyle(live: block.isOn(at: now)))
                    .focused(focused, equals: block.id)
                    .frame(width: max(width - LiveGuideView.spacing, 24))
                    .padding(.trailing, LiveGuideView.spacing)
                    .accessibilityIdentifier(block.isOn(at: now)
                        ? "live.guide.\(channelID).now"
                        : "live.guide.\(channelID).\(Int(block.start.timeIntervalSince1970))")
                }
            }
        }
    }

    private enum Piece {
        case gap(CGFloat)
        case block(GuideBlock, CGFloat)
    }

    /// The blocks in the window in time order, and the gaps between them.
    private var pieces: [Piece] {
        var out: [Piece] = []
        var cursor = start
        for b in blocks.sorted(by: { $0.start < $1.start }) where b.stop > start && b.start < end {
            let from = max(b.start, cursor)
            let to = min(b.stop, end)
            guard to > from else { continue }
            if from > cursor { out.append(.gap(LiveGuideView.width(from: cursor, to: from))) }
            out.append(.block(b, LiveGuideView.width(from: from, to: to)))
            cursor = to
        }
        return out
    }
}

private struct GuideBlockLabel: View {
    let block: GuideBlock
    let now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(block.title)
                .font(Theme.body(28, weight: .medium))
                .lineLimit(2)
            Text(timeRange)
                .font(Theme.body(22))
                .opacity(0.8)
                .lineLimit(1)
            if block.isOn(at: now) {
                ProgressView(value: block.progress(at: now))
                    .tint(Theme.gold)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private var timeRange: String {
        "\(block.start.formatted(date: .omitted, time: .shortened)) – \(block.stop.formatted(date: .omitted, time: .shortened))"
    }
}

/// A programme in the guide: a quiet surface, raised when on now, gold when focused.
private struct GuideBlockStyle: ButtonStyle {
    let live: Bool

    func makeBody(configuration: Configuration) -> some View {
        Styled(configuration: configuration, live: live)
    }

    private struct Styled: View {
        let configuration: ButtonStyleConfiguration
        let live: Bool
        @Environment(\.isFocused) private var focused

        var body: some View {
            configuration.label
                .foregroundStyle(focused ? Theme.onGold : Theme.text)
                .padding(.horizontal, 18)
                .padding(.vertical, 12)
                .frame(height: LiveGuideView.rowHeight)
                .background(focused ? Theme.gold : live ? Theme.raised : Theme.surface.opacity(0.9),
                            in: .rect(cornerRadius: 12))
                .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Theme.border, lineWidth: focused ? 0 : 1))
                .shadow(color: focused ? Theme.goldText.opacity(0.5) : .clear, radius: 18)
                .scaleEffect(configuration.isPressed ? 0.97 : focused ? 1.04 : 1)
                .animation(.easeOut(duration: 0.15), value: focused)
        }
    }
}

/// A programme's page from the guide: its title, time and description; Watch while it is
/// on, else only Close (live TV cannot play what has not started).
private struct GuideDetail: View {
    let block: GuideBlock
    let channel: LiveChannel?
    let watch: () -> Void

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 28) {
            if let channel {
                Text(verbatim: "\(channel.number.formatted()) · \(channel.name)")
                    .font(Theme.body(28, weight: .semibold))
                    .foregroundStyle(Theme.goldText)
            }
            Text(block.title)
                .font(Theme.heading(52))
                .foregroundStyle(Theme.text)
                .accessibilityIdentifier("live.detail.title")
            Text(block.start, format: .dateTime.weekday(.wide).hour().minute())
                .font(Theme.body(28))
                .foregroundStyle(Theme.muted)
            if let desc = block.desc, !desc.isEmpty {
                Text(desc)
                    .font(Theme.body(29))
                    .foregroundStyle(Theme.text)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack(spacing: 32) {
                if block.isOn(at: .now) {
                    Button("Watch", action: watch)
                        .accessibilityIdentifier("live.detail.watch")
                }
                Button("Close") { dismiss() }
                    .accessibilityIdentifier("live.detail.close")
            }
            .buttonStyle(.gold)
        }
        .frame(maxWidth: 1100, alignment: .leading)
        .padding(64)
    }
}
