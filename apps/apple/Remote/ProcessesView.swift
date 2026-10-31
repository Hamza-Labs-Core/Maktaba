import MaktabaKit
import SwiftUI

/// The processes the home's supervisor runs, read only (the web's System health ->
/// Processes): each by role and instance (the TV's slots by colour), its state, restarts,
/// last exit and what it waits on; then the supervisor's recent events.
struct ProcessesView: View {
    @Environment(RemoteModel.self) private var model

    var body: some View {
        List {
            if let problem = model.statusProblems[.processes] {
                Section {
                    Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(.red)
                }
            }
            if let doc = model.processes {
                if !doc.available {
                    Section {
                        Callout(systemImage: "bolt.horizontal.circle", tint: .brass,
                                title: "The home's supervisor isn't reachable, so its processes can't be shown. The home may not be running under maktaba-server.",
                                detail: doc.reason)
                            .accessibilityIdentifier("processes.unavailable")
                    }
                } else {
                    Section {
                        ForEach(doc.processes) { ProcessRow(process: $0) }
                    } header: {
                        Text(doc.host.map { "On \($0)" } ?? "Processes")
                    }
                    let events = doc.recentEvents()
                    Section("Recent events") {
                        if events.isEmpty {
                            Text("No events recorded.").foregroundStyle(.secondary)
                        }
                        ForEach(events) { EventRow(event: $0) }
                    }
                }
            }
        }
        .maktabaSurface()
        .overlay {
            if model.processes == nil, model.statusProblems[.processes] == nil { ProgressView() }
        }
        .navigationTitle("Processes")
        .refreshable { await model.load() }
        .toolbar { AccountMenu() }
    }
}

private struct ProcessRow: View {
    let process: ProcessStatus

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Image(systemName: process.role == "tv-slot" ? "circle.fill" : "gearshape")
                    .font(process.role == "tv-slot" ? .caption : .body)
                    .foregroundStyle(slotColor)
                    .accessibilityHidden(true)
                Text(process.name).font(.headline)
                if process.wanted {
                    Text("wanted").font(.caption2).foregroundStyle(.secondary)
                }
                Spacer()
                Badge(text: process.stateText, tint: stateTint)
            }
            Text(facts).font(.caption).foregroundStyle(.secondary)
            if process.crashLoop {
                Label("Crash loop: it keeps exiting soon after it starts. The other processes keep running, and this one is retried slowly.",
                      systemImage: "arrow.triangle.2.circlepath")
                    .font(.callout)
                    .foregroundStyle(.red)
            }
            if let exit = process.lastExit, exit.failed {
                Text("Last exit: \(exit.sentence)\(exit.at.map { ", \($0.formatted(.relative(presentation: .named)))" } ?? "")")
                    .font(.callout)
                    .foregroundStyle(.red)
                if let error = exit.error, !error.isEmpty {
                    Text(error).font(.caption).foregroundStyle(.secondary)
                }
            }
            if let detail = process.detailText {
                VStack(alignment: .leading, spacing: 2) {
                    Text(detail).font(.callout)
                    if let code = process.detailCode, process.detail != nil {
                        Text(code).font(.caption.monospaced()).foregroundStyle(.secondary)
                    }
                }
                .accessibilityIdentifier("process.detail.\(process.id)")
            }
            if let probe = process.lastProbe, !probe.ok {
                Text("Health check failing (\(probe.failures) in a row)").font(.callout).foregroundStyle(Color.brass)
            }
        }
        .padding(.vertical, 2)
    }

    private var facts: String {
        var parts: [String] = []
        parts.append(process.restarts == 1 ? "1 restart" : "\(process.restarts) restarts")
        if process.pid != nil, let up = process.uptimeSec { parts.append("up for \(shortDuration(up))") }
        if let next = process.nextRestartAt { parts.append("next restart \(next.formatted(date: .omitted, time: .shortened))") }
        if process.adopted { parts.append("kept across a restart") }
        return parts.joined(separator: " · ")
    }

    private var slotColor: Color {
        switch process.instance {
        case "blue": .blue
        case "green": .green
        case "amber": .orange
        default: .secondary
        }
    }

    private var stateTint: Color {
        switch process.state {
        case "running": .green
        case "starting": .blue
        case "unhealthy", "backoff": .brass
        case "failed": .red
        default: .secondary
        }
    }
}

private struct EventRow: View {
    let event: ProcessEvent

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(alignment: .firstTextBaseline) {
                Text(event.processName).font(.subheadline.bold())
                Text(event.sentence)
                    .font(.subheadline)
                    .foregroundStyle(event.isFailure ? .red : .primary)
            }
            if let at = event.at {
                Text(at.formatted(date: .abbreviated, time: .standard)).font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}
