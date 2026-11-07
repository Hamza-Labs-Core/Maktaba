import MaktabaKit
import SwiftUI

/// Logs (Epic 53 story 53.11 §4): search the home's log index (words, "phrases", -word) by
/// level and time, and Follow a request: a reference from a person's error screen (its 8
/// characters) or a whole request id opens every line of that request from every part, in
/// time order. No live tail on a phone. Log lines stay as written: left to right.
struct LogsView: View {
    @Environment(RemoteModel.self) private var model
    @State private var text = ""
    @State private var query = LogQuery()
    @State private var result: LogSearch?
    @State private var more: [(id: Int64?, line: LogLine)] = []
    @State private var problem: String?
    @State private var following: String?

    var body: some View {
        List {
            Section {
                Picker("Level", selection: $query.level) {
                    Text("Every level").tag("")
                    Text("Warnings and errors").tag("warn")
                    Text("Errors").tag("error")
                }
                .accessibilityIdentifier("logs.level")
                Picker("Time", selection: $query.window) {
                    Text("Last 15 minutes").tag(LogWindow.quarter)
                    Text("Last hour").tag(LogWindow.hour)
                    Text("Last 24 hours").tag(LogWindow.day)
                    Text("Last 7 days").tag(LogWindow.week)
                }
                .accessibilityIdentifier("logs.window")
            } footer: {
                Text("Type a reference from an error screen to follow that request.")
            }
            if let problem {
                Section {
                    Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(Color.brass)
                        .accessibilityIdentifier("logs.problem")
                }
            }
            if let result {
                Section {
                    if result.lines.isEmpty { Text("No lines match.").foregroundStyle(.secondary).accessibilityIdentifier("logs.none") }
                    ForEach(Array((result.rows + more).enumerated()), id: \.offset) { _, row in
                        NavigationLink {
                            LogLineView(line: row.line, id: row.id)
                        } label: {
                            LogLineRow(line: row.line)
                        }
                    }
                    if let cursor = nextCursor {
                        Button("More") { Task { await loadMore(cursor) } }
                            .accessibilityIdentifier("logs.more")
                    }
                }
            }
        }
        .maktabaSurface()
        .overlay { if result == nil, problem == nil { ProgressView() } }
        .navigationTitle("Logs")
        .searchable(text: $text, prompt: Text("Search logs"))
        .onSubmit(of: .search) { submit() }
        .navigationDestination(item: $following) { FollowRequestView(requestID: $0) }
        .refreshable { await load() }
        .task(id: query) { await load() }
        .toolbar { AccountMenu() }
    }

    @State private var lastCursor: String?
    private var nextCursor: String? { lastCursor }

    private func submit() {
        if let ref = LogQuery.requestReference(text) {
            following = ref
        } else {
            query.text = text
        }
    }

    private func load() async {
        do {
            let r = try await model.client.searchLogs(query)
            result = r
            more = []
            lastCursor = r.cursor
            problem = nil
        } catch {
            if error is CancellationError { return }
            problem = LogWords.problem(error)
        }
    }

    private func loadMore(_ cursor: String) async {
        do {
            let r = try await model.client.searchLogs(query, cursor: cursor)
            more += r.rows
            lastCursor = r.cursor
        } catch {
            problem = LogWords.problem(error)
        }
    }
}

enum LogWords {
    /// A down index (503) says so; anything else is the usual sentence.
    static func problem(_ error: Error) -> String {
        if case let APIError.status(503, body, _) = error {
            return body.contains("off")
                ? String(localized: "Log search is off. Turn it on in Settings → Components on the web.")
                : String(localized: "Log search is restarting. Try again in a minute.")
        }
        return HomeFailure(error).message()
    }

    static func levelTint(_ level: String) -> Color {
        switch level {
        case "fatal", "error": .red
        case "warn": .brass
        case "debug": .secondary
        default: .blue
        }
    }
}

/// A line in a list: its time, level, part, message and request.
struct LogLineRow: View {
    let line: LogLine

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 6) {
                Text(verbatim: line.level.uppercased())
                    .font(.caption2.bold().monospaced())
                    .foregroundStyle(LogWords.levelTint(line.level))
                Text(verbatim: line.component ?? line.service)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                Spacer()
                if let ts = line.ts {
                    Text(ts.formatted(.dateTime.hour().minute().second()))
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }
            // as written: left to right in every language
            Text(verbatim: line.msg)
                .font(.callout.monospaced())
                .lineLimit(3)
                .frame(maxWidth: .infinity, alignment: .leading)
                .environment(\.layoutDirection, .leftToRight)
            if let r = line.requestID {
                Text(verbatim: RequestID.short(r))
                    .font(.caption2.monospaced())
                    .padding(.horizontal, 6).padding(.vertical, 1)
                    .background(Color.secondary.opacity(0.15), in: .capsule)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// One line: every field, Follow this request, Lines around this.
struct LogLineView: View {
    let line: LogLine
    let id: Int64?
    @Environment(RemoteModel.self) private var model
    @State private var userName: String?

    var body: some View {
        List {
            Section {
                if let r = line.requestID {
                    NavigationLink {
                        FollowRequestView(requestID: r)
                    } label: {
                        Label("Follow this request", systemImage: "point.topleft.down.to.point.bottomright.curvepath")
                    }
                    .accessibilityIdentifier("log.follow")
                }
                if let id {
                    NavigationLink {
                        AroundView(id: id)
                    } label: {
                        Label("Lines around this", systemImage: "text.line.first.and.arrowtriangle.forward")
                    }
                    .accessibilityIdentifier("log.around")
                }
                if line.userH != nil {
                    LabeledContent("Person", value: userName ?? String(localized: "Unknown user"))
                }
            }
            Section("Fields") {
                ForEach(line.fields.keys.sorted(), id: \.self) { k in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(verbatim: k).font(.caption.bold().monospaced()).foregroundStyle(.secondary)
                        Text(verbatim: line.fields[k] ?? "")
                            .font(.callout.monospaced())
                            .textSelection(.enabled)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .environment(\.layoutDirection, .leftToRight)
                }
            }
        }
        .maktabaSurface()
        .navigationTitle("Line")
        .task {
            if let h = line.userH { userName = (try? await model.client.logUserNames())?[h] }
        }
    }
}

/// Follow a request (53.11 §1): every line with this id from every part and machine, oldest
/// first, with a gap of more than a second marked.
struct FollowRequestView: View {
    let requestID: String
    @Environment(RemoteModel.self) private var model
    @State private var lines: [(id: Int64?, line: LogLine)]?
    @State private var problem: String?

    var body: some View {
        List {
            if let problem {
                Section { Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(Color.brass) }
            }
            if let lines {
                Section {
                    if lines.isEmpty { Text("No line of this request is kept any more.").foregroundStyle(.secondary) }
                    let gaps = gapsOverASecond(lines.map(\.line))
                    ForEach(Array(lines.enumerated()), id: \.offset) { i, row in
                        if gaps[i], let a = lines[i - 1].line.ts, let b = row.line.ts {
                            Label("\(Int(b.timeIntervalSince(a).rounded())) s later", systemImage: "hourglass")
                                .font(.caption)
                                .foregroundStyle(Color.brass)
                        }
                        NavigationLink {
                            LogLineView(line: row.line, id: row.id)
                        } label: {
                            LogLineRow(line: row.line)
                        }
                    }
                } header: {
                    Text(verbatim: requestID).font(.caption.monospaced()).environment(\.layoutDirection, .leftToRight)
                }
            }
        }
        .maktabaSurface()
        .overlay { if lines == nil, problem == nil { ProgressView() } }
        .navigationTitle("Follow a request")
        .accessibilityIdentifier("logs.follow")
        .task {
            do {
                let r = try await model.client.searchLogs(LogQuery(requestID: requestID))
                lines = r.rows.sorted { ($0.line.ts ?? .distantPast) < ($1.line.ts ?? .distantPast) }
            } catch {
                if !(error is CancellationError) { problem = LogWords.problem(error) }
            }
        }
    }
}

/// Every part's lines around one line.
struct AroundView: View {
    let id: Int64
    @Environment(RemoteModel.self) private var model
    @State private var result: LogSearch?
    @State private var problem: String?

    var body: some View {
        List {
            if let problem { Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(Color.brass) }
            ForEach(Array((result?.rows ?? []).enumerated()), id: \.offset) { _, row in
                LogLineRow(line: row.line)
                    .listRowBackground(row.id == id ? Color.brass.opacity(0.15) : nil)
            }
        }
        .maktabaSurface()
        .overlay { if result == nil, problem == nil { ProgressView() } }
        .navigationTitle("Lines around this")
        .task {
            do { result = try await model.client.linesAround(id) } catch {
                if !(error is CancellationError) { problem = LogWords.problem(error) }
            }
        }
    }
}
