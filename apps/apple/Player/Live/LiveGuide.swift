import MaktabaKit
import SwiftUI

/// The guide (FR-22): channels down the side, time across (three hours on a phone, six on a
/// tablet, from the half hour before now; a later day whole), a brass line at now. It scrolls
/// both ways: up and down through the channels, sideways through the time. A day picker
/// chooses the day; tapping a programme opens its sheet (title, time, description, and Watch
/// while it is on). In Arabic time runs right to left, as the layout does.
struct LiveGuideView: View {
    let model: LiveModel
    let client: APIClient

    @Environment(\.horizontalSizeClass) private var sizeClass

    /// Points per minute: half an hour is 120 pt on a phone, 150 pt on a tablet.
    var pointsPerMinute: CGFloat { sizeClass == .regular ? 5 : 4 }
    var channelWidth: CGFloat { sizeClass == .regular ? 180 : 104 }
    static let rowHeight: CGFloat = 68
    static let headerHeight: CGFloat = 32
    static let gap: CGFloat = 4

    var body: some View {
        VStack(spacing: 0) {
            dayPicker
            if let window = model.guide {
                TimelineView(.periodic(from: .now, by: 30)) { context in
                    grid(window, at: context.date)
                }
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("live.guide")
    }

    private var dayPicker: some View {
        HStack {
            Picker(selection: Binding(get: { model.day }, set: { day in
                model.day = day
                Task { await model.loadGuide(client) }
            })) {
                ForEach(0..<LiveModel.days, id: \.self) { day in
                    Self.dayName(day).tag(day)
                }
            } label: {
                Text("Day", tableName: "Live", comment: "Live TV guide: choose the day")
            }
            .pickerStyle(.menu)
            .accessibilityIdentifier("live.guide.day")
            Spacer()
        }
        .padding(.horizontal, MaktabaTokens.Spacing.s4)
    }

    /// Today, Tomorrow, then the weekday and date.
    static func dayName(_ day: Int, now: Date = .now, calendar: Calendar = .current) -> Text {
        switch day {
        case 0: Text("Today", tableName: "Live", comment: "Live TV guide: the day picker's today")
        case 1: Text("Tomorrow", tableName: "Live", comment: "Live TV guide: the day picker's tomorrow")
        default:
            Text(calendar.date(byAdding: .day, value: day, to: now) ?? now,
                 format: .dateTime.weekday(.wide).day().month(.abbreviated))
        }
    }

    private func grid(_ window: GuideWindow, at now: Date) -> some View {
        let start = window.start, end = window.end
        return ScrollView(.vertical) {
            HStack(alignment: .top, spacing: Self.gap) {
                // the channels: they scroll up and down with their rows, not sideways
                VStack(alignment: .leading, spacing: Self.gap) {
                    Color.clear.frame(width: channelWidth, height: Self.headerHeight)
                    ForEach(model.channels) { channel in
                        GuideChannelLabel(channel: channel, logo: client.channelLogoURL(channel), compact: sizeClass != .regular)
                            .frame(width: channelWidth, height: Self.rowHeight, alignment: .leading)
                    }
                }
                ScrollView(.horizontal) {
                    VStack(alignment: .leading, spacing: Self.gap) {
                        timeline(from: start, to: end)
                        ForEach(model.channels) { channel in
                            GuideRow(blocks: model.row(channel.id)?.blocks ?? [], channelID: channel.id,
                                     start: start, end: end, now: now, pointsPerMinute: pointsPerMinute) { model.programme = $0 }
                                .frame(width: width(from: start, to: end), height: Self.rowHeight, alignment: .leading)
                        }
                    }
                    .overlay(alignment: .topLeading) { nowLine(start: start, end: end, now: now) }
                    .padding(.trailing, MaktabaTokens.Spacing.s4)
                }
                .accessibilityIdentifier("live.guide.times")
            }
            .padding(.leading, MaktabaTokens.Spacing.s2)
            .padding(.vertical, MaktabaTokens.Spacing.s2)
        }
        .accessibilityIdentifier("live.guide.grid")
    }

    /// The half hours along the top.
    private func timeline(from start: Date, to end: Date) -> some View {
        let slots = max(0, Int((end.timeIntervalSince(start) / 1800).rounded(.down)))
        return HStack(spacing: 0) {
            ForEach(0..<slots, id: \.self) { i in
                Text(start.addingTimeInterval(Double(i) * 1800), format: .dateTime.hour().minute())
                    .font(.maktabaBody(12, weight: .medium))
                    .foregroundStyle(Color.mktMuted)
                    .padding(.leading, 4)
                    .frame(width: width(minutes: 30), height: Self.headerHeight, alignment: .leading)
                    .overlay(alignment: .leading) { Rectangle().fill(Color.mktBorder).frame(width: 1) }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("live.guide.timeline")
    }

    @ViewBuilder private func nowLine(start: Date, end: Date, now: Date) -> some View {
        if now >= start, now < end {
            Rectangle()
                .fill(Color.brass)
                .frame(width: 2)
                .frame(maxHeight: .infinity)
                .offset(x: width(from: start, to: now) - 1)
                .allowsHitTesting(false)
                .accessibilityElement()
                .accessibilityLabel(Text("Now", tableName: "Live", comment: "Live TV: what is on now on every channel"))
                .accessibilityIdentifier("live.guide.nowline")
        }
    }

    func width(minutes: Double) -> CGFloat { CGFloat(minutes) * pointsPerMinute }

    func width(from a: Date, to b: Date) -> CGFloat { width(minutes: max(0, b.timeIntervalSince(a) / 60)) }
}

/// A channel at the start of its row: its logo, and its number and name when there is room.
private struct GuideChannelLabel: View {
    let channel: LiveChannel
    let logo: URL?
    let compact: Bool

    var body: some View {
        HStack(spacing: MaktabaTokens.Spacing.s2) {
            ChannelLogo(name: channel.name, number: channel.number, logo: logo, size: 36)
            VStack(alignment: .leading, spacing: 0) {
                Text(channel.number, format: .number)
                    .font(.maktabaBody(12, weight: .semibold))
                    .foregroundStyle(Color.mktBrassText)
                Text(channel.name)
                    .font(.maktabaBody(compact ? 12 : MaktabaTokens.TypeSize.sm, weight: .semibold))
                    .foregroundStyle(Color.mktText)
                    .lineLimit(2)
                    .minimumScaleFactor(0.8)
            }
        }
        .padding(.horizontal, MaktabaTokens.Spacing.s2)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
        .background(Color.mktSurface, in: .rect(cornerRadius: MaktabaTokens.Radius.md))
        .accessibilityElement(children: .combine)
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
    let pointsPerMinute: CGFloat
    let open: (GuideBlock) -> Void

    var body: some View {
        HStack(spacing: 0) {
            ForEach(Array(pieces.enumerated()), id: \.offset) { _, piece in
                switch piece {
                case let .gap(width):
                    Color.clear.frame(width: width)
                case let .block(block, width):
                    Button { open(block) } label: {
                        GuideBlockLabel(block: block, now: now)
                    }
                    .buttonStyle(.plain)
                    .frame(width: max(width - LiveGuideView.gap, 8), height: LiveGuideView.rowHeight)
                    .padding(.trailing, LiveGuideView.gap)
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

    private func width(from a: Date, to b: Date) -> CGFloat { CGFloat(max(0, b.timeIntervalSince(a) / 60)) * pointsPerMinute }

    /// The blocks in the window in time order, and the gaps between them.
    private var pieces: [Piece] {
        var out: [Piece] = []
        var cursor = start
        for b in blocks.sorted(by: { $0.start < $1.start }) where b.stop > start && b.start < end {
            let from = max(b.start, cursor)
            let to = min(b.stop, end)
            guard to > from else { continue }
            if from > cursor { out.append(.gap(width(from: cursor, to: from))) }
            out.append(.block(b, width(from: from, to: to)))
            cursor = to
        }
        return out
    }
}

private struct GuideBlockLabel: View {
    let block: GuideBlock
    let now: Date

    var body: some View {
        let live = block.isOn(at: now)
        VStack(alignment: .leading, spacing: 2) {
            Text(block.title)
                .font(.maktabaBody(MaktabaTokens.TypeSize.sm, weight: .medium))
                .foregroundStyle(Color.mktText)
                .lineLimit(2)
            Text(verbatim: "\(block.start.formatted(date: .omitted, time: .shortened)) – \(block.stop.formatted(date: .omitted, time: .shortened))")
                .font(.maktabaBody(11))
                .foregroundStyle(Color.mktMuted)
                .lineLimit(1)
        }
        .padding(.horizontal, MaktabaTokens.Spacing.s2)
        .padding(.vertical, 6)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(live ? Color.mktRaised : Color.mktSurface, in: .rect(cornerRadius: MaktabaTokens.Radius.md))
        .overlay {
            RoundedRectangle(cornerRadius: MaktabaTokens.Radius.md)
                .strokeBorder(live ? Color.brass : Color.mktBorder, lineWidth: live ? 1.5 : 0.5)
        }
        .contentShape(.rect)
    }
}

/// A programme's sheet: its channel, title, day and time, description; Watch while it is on
/// (live TV cannot play what has not started).
struct LiveProgrammeSheet: View {
    let block: GuideBlock
    let channel: LiveChannel?

    let watch: (String) -> Void

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s3) {
                    if let channel {
                        Text(verbatim: "\(channel.number.formatted()) · \(channel.name)")
                            .font(.maktabaBody(MaktabaTokens.TypeSize.sm, weight: .semibold))
                            .foregroundStyle(Color.mktBrassText)
                    }
                    Text(block.title)
                        .font(.maktabaHeading(MaktabaTokens.TypeSize.h1, relativeTo: .title))
                        .foregroundStyle(Color.mktText)
                        .accessibilityIdentifier("live.detail.title")
                    Text(verbatim: "\(block.start.formatted(.dateTime.weekday(.wide).hour().minute())) – \(block.stop.formatted(date: .omitted, time: .shortened))")
                        .font(.maktabaBody(MaktabaTokens.TypeSize.sm))
                        .foregroundStyle(Color.mktMuted)
                        .accessibilityIdentifier("live.detail.time")
                    if let desc = block.desc, !desc.isEmpty {
                        Text(desc)
                            .font(.maktabaBody())
                            .foregroundStyle(Color.mktText)
                            .accessibilityIdentifier("live.detail.desc")
                    }
                    if block.isOn(at: .now) {
                        Button {
                            dismiss()
                            watch(block.channelID)
                        } label: {
                            Label { Text("Watch", tableName: "Live", comment: "Live TV: watch what is on now") }
                                icon: { Image(systemName: "play.fill") }
                                .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.large)
                        .padding(.top, MaktabaTokens.Spacing.s2)
                        .accessibilityIdentifier("live.detail.watch")
                    }
                }
                .padding(MaktabaTokens.Spacing.s4)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .maktabaSurface()
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button { dismiss() } label: { Text("Close", tableName: "Live", comment: "Closes a programme's sheet") }
                        .accessibilityIdentifier("live.detail.close")
                }
            }
        }
        .presentationDetents([.medium, .large])
    }
}
