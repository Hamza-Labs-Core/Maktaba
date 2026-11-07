import Charts
import MaktabaKit
import SwiftUI

/// Errors (Epic 53 story 53.11 §4): the home's error groups, the web's `/admin/errors`. New
/// and seen by default; a group's page has its chart, its recent lines (each opens the line,
/// and Follow this request), the releases and machines it came from, and Mark seen, Resolve
/// and Mute. Every change goes through the home, which keeps it in its audit log.
struct ErrorsView: View {
    @Environment(RemoteModel.self) private var model
    @State private var filter = ErrorFilter.open
    @State private var sort = ErrorSort.lastSeen
    @State private var groups: [ErrorGroup]?
    @State private var problem: String?

    var body: some View {
        List {
            if let problem {
                Section { Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(.red) }
            }
            Section {
                Picker("Show", selection: $filter) {
                    Text("New and seen").tag(ErrorFilter.open)
                    Text("Resolved").tag(ErrorFilter.resolved)
                    Text("Muted").tag(ErrorFilter.muted)
                    Text("All").tag(ErrorFilter.all)
                }
                .accessibilityIdentifier("errors.filter")
                Picker("Sort by", selection: $sort) {
                    Text("Last seen").tag(ErrorSort.lastSeen)
                    Text("Count").tag(ErrorSort.count)
                    Text("People").tag(ErrorSort.users)
                }
                .accessibilityIdentifier("errors.sort")
            }
            Section {
                if let groups, groups.isEmpty {
                    Text("No errors to show.").foregroundStyle(.secondary)
                        .accessibilityIdentifier("errors.none")
                }
                ForEach(groups ?? []) { g in
                    NavigationLink {
                        ErrorGroupView(fp: g.fp, onChange: { await load() })
                    } label: {
                        ErrorRow(group: g)
                    }
                    .accessibilityIdentifier("errors.group.\(g.fp)")
                }
            }
        }
        .maktabaSurface()
        .overlay { if groups == nil, problem == nil { ProgressView() } }
        .navigationTitle("Errors")
        .refreshable { await load() }
        .task(id: "\(filter.rawValue)|\(sort.rawValue)") { await load() }
        .toolbar { AccountMenu() }
    }

    private func load() async {
        do {
            groups = try await model.client.errorGroups(filter, sort: sort)
            problem = nil
        } catch {
            if !(error is CancellationError) { problem = HomeFailure(error).message() }
        }
        await model.loadNewErrors()
    }
}

/// A group in the list: its title, where, how often, how many people, when, its status.
struct ErrorRow: View {
    let group: ErrorGroup

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Text(verbatim: group.title).font(.headline).lineLimit(2)
                Spacer()
                Badge(text: ErrorWords.status(group.status), tint: ErrorWords.tint(group.status))
            }
            Text(verbatim: group.component ?? group.service)
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
            HStack {
                Text("\(group.count24h) in 24 h · \(group.count) in all · \(group.users) people")
                    .font(.caption)
                Spacer()
                Sparkline(days: group.days).frame(width: 70, height: 18)
            }
            if let last = group.lastSeen {
                Text("Last seen \(last.formatted(.relative(presentation: .named)))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
    }
}

/// The 14 days' counts as bars, oldest first (time runs left to right in every language).
struct Sparkline: View {
    let days: [Int]

    var body: some View {
        Chart(Array(days.enumerated()), id: \.offset) { i, n in
            BarMark(x: .value("Day", i), y: .value("Count", n)).foregroundStyle(Color.brass)
        }
        .chartXAxis(.hidden)
        .chartYAxis(.hidden)
        .environment(\.layoutDirection, .leftToRight)
        .accessibilityHidden(true)
    }
}

enum ErrorWords {
    static func status(_ s: String) -> String {
        switch s {
        case "new": String(localized: "New")
        case "seen": String(localized: "Seen")
        case "resolved": String(localized: "Resolved")
        case "muted": String(localized: "Muted")
        default: s
        }
    }

    static func tint(_ s: String) -> Color {
        switch s {
        case "new": .red
        case "seen": .brass
        case "resolved": .green
        default: .secondary
        }
    }
}

/// One group: what it is, its last week by the hour, its lines, and what the admin does.
struct ErrorGroupView: View {
    let fp: String
    var onChange: () async -> Void = {}
    @Environment(RemoteModel.self) private var model
    @State private var detail: ErrorGroupDetail?
    @State private var problem: String?
    @State private var busy = false

    var body: some View {
        List {
            if let problem {
                Section { Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(.red) }
            }
            if let d = detail {
                let g = d.group
                // what the admin does, first: on screen without scrolling
                Section {
                    Text(verbatim: g.title).font(.headline)
                }
                Section {
                    BusyButton(title: "Mark seen", systemImage: "eye", busy: busy) { await act(.seen) }
                        .disabled(g.status == "seen")
                        .accessibilityIdentifier("error.seen")
                    BusyButton(title: "Resolve", systemImage: "checkmark.circle", busy: busy) { await act(.resolve(version: g.lastVersion)) }
                        .accessibilityIdentifier("error.resolve")
                    Menu {
                        Button("For 24 hours") { Task { await act(.mute(until: Date().addingTimeInterval(86400))) } }
                        Button("For 7 days") { Task { await act(.mute(until: Date().addingTimeInterval(7 * 86400))) } }
                        Button("Until it is resolved") { Task { await act(.mute(until: nil)) } }
                    } label: {
                        Label("Mute", systemImage: "bell.slash")
                    }
                    .disabled(busy)
                    .accessibilityIdentifier("error.mute")
                }
                Section {
                    LabeledContent("Status") { Badge(text: ErrorWords.status(g.status), tint: ErrorWords.tint(g.status)) }
                    LabeledContent("Part") { Text(verbatim: g.component ?? g.service).font(.callout.monospaced()) }
                    if let code = g.code { LabeledContent("Code") { Text(verbatim: code).font(.callout.monospaced()) } }
                    LabeledContent("In 24 hours", value: g.count24h, format: .number)
                    LabeledContent("In all", value: g.count, format: .number)
                    LabeledContent("People", value: g.users, format: .number)
                    if let first = g.firstSeen { LabeledContent("First seen", value: first.formatted(date: .abbreviated, time: .shortened)) }
                    if let last = g.lastSeen { LabeledContent("Last seen", value: last.formatted(date: .abbreviated, time: .shortened)) }
                    if let until = g.mutedUntil { LabeledContent("Muted until", value: until.formatted(date: .abbreviated, time: .shortened)) }
                    if !d.versions.isEmpty { LabeledContent("Releases", value: d.versions.joined(separator: ", ")) }
                    if !d.machines.isEmpty { LabeledContent("Machines", value: d.machines.joined(separator: ", ")) }
                }
                Section("The last 7 days") {
                    Chart(d.hourly, id: \.at) { h in
                        BarMark(x: .value("Hour", h.at, unit: .hour), y: .value("Count", h.count)).foregroundStyle(Color.brass)
                    }
                    .frame(height: 140)
                    .environment(\.layoutDirection, .leftToRight)
                    .accessibilityLabel(Text("\(d.hourly.reduce(0) { $0 + $1.count }) in the last 7 days"))
                }
                Section("Recent lines") {
                    if d.samples.lines.isEmpty { Text("No line of it is kept any more.").foregroundStyle(.secondary) }
                    ForEach(Array(d.samples.rows.enumerated()), id: \.offset) { _, row in
                        NavigationLink {
                            LogLineView(line: row.line, id: row.id)
                        } label: {
                            LogLineRow(line: row.line)
                        }
                    }
                }
            }
        }
        .maktabaSurface()
        .overlay { if detail == nil, problem == nil { ProgressView() } }
        .navigationTitle("Error")
        .refreshable { await load() }
        .task { await load() }
    }

    private func load() async {
        do {
            detail = try await model.client.errorGroup(fp)
            problem = nil
        } catch {
            if !(error is CancellationError) { problem = HomeFailure(error).message() }
        }
    }

    private func act(_ a: ErrorGroupAction) async {
        busy = true
        defer { busy = false }
        do {
            try await model.client.setErrorStatus(fp, a)
            problem = nil
        } catch {
            problem = HomeFailure(error).message()
        }
        await load()
        await onChange()
    }
}
