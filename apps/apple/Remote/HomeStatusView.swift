import MaktabaKit
import SwiftUI

/// The home at a glance: its name and release, whether the queue runs, what waits and
/// what failed, its machines, its updates and processes (each a page of its own).
struct HomeStatusView: View {
    let user: User
    @Environment(RemoteModel.self) private var model

    var body: some View {
        List {
            ProblemSection()
            Section("Home") {
                LabeledContent("Name", value: model.session.current?.name ?? model.cluster?.home.name ?? "")
                if let url = model.session.current?.url {
                    LabeledContent("Address", value: url.absoluteString)
                }
                if let v = model.cluster?.home.version, !v.isEmpty {
                    LabeledContent("Release", value: v)
                }
                LabeledContent("Signed in as", value: user.username)
            }
            Section("Running work") {
                NavigationLink {
                    UpdatesView()
                } label: {
                    LabeledContent {
                        Text(updatesSummary).lineLimit(2)
                    } label: {
                        Label("Updates", systemImage: "arrow.down.circle")
                    }
                }
                .accessibilityIdentifier("home.updates")
                NavigationLink {
                    ProcessesView()
                } label: {
                    LabeledContent {
                        Text(processesSummary).lineLimit(2)
                    } label: {
                        Label("Processes", systemImage: "cpu")
                    }
                }
                .accessibilityIdentifier("home.processes")
                if let held = model.updates?.tvHeld, held.tv.held {
                    Label(held.summary, systemImage: "tv").foregroundStyle(Color.brass)
                }
            }
            if let q = model.queue {
                Section("Processing") {
                    LabeledContent("Queue") {
                        Text(q.pausedAll ? "Paused" : "Running")
                            .foregroundStyle(q.pausedAll ? Color.brass : .green)
                    }
                    LabeledContent("Waiting", value: q.rows.reduce(0) { $0 + $1.waiting }, format: .number)
                    LabeledContent("Running", value: q.rows.reduce(0) { $0 + $1.running }, format: .number)
                    LabeledContent("Needs attention", value: model.attentionCount, format: .number)
                    let paused = q.rows.filter(\.info.paused)
                    if !paused.isEmpty {
                        LabeledContent("Paused stages", value: paused.map(\.info.label).joined(separator: ", "))
                    }
                    if let cpu = q.health?.cpu?.pct {
                        LabeledContent("CPU", value: (cpu / 100).formatted(.percent.precision(.fractionLength(0))))
                    }
                }
            }
            if let c = model.cluster {
                Section("Machines") {
                    let approved = c.nodes.filter { $0.group == .approved }
                    LabeledContent("Online", value: "\(approved.filter(\.online).count) of \(approved.count)")
                    if !model.asking.isEmpty {
                        LabeledContent("Asking to join", value: model.asking.count, format: .number)
                    }
                }
            }
        }
        .maktabaSurface()
        .overlay {
            if !model.loaded { ProgressView() }
        }
        .navigationTitle(model.session.current?.name ?? "Home")
        .refreshable { await model.load() }
        .toolbar { AccountMenu() }
    }

    private var updatesSummary: String {
        guard let o = model.updates else { return model.statusProblems[.updates] == nil ? "…" : "Not available" }
        if let run = o.activeRun { return "Updating to \(run.version)" }
        if !o.blockedReleases.isEmpty { return "\(o.blockedReleases.count) blocked" }
        if let target = o.plan.target { return "\(target.version) available" }
        return "Up to date"
    }

    private var processesSummary: String {
        guard let doc = model.processes else { return model.statusProblems[.processes] == nil ? "…" : "Not available" }
        guard doc.available else { return "Supervisor not reachable" }
        let troubled = doc.processes.filter { $0.crashLoop || ["failed", "backoff", "unhealthy"].contains($0.state) }
        let running = doc.processes.filter { $0.state == "running" }.count
        return troubled.isEmpty ? "\(running) running" : "\(troubled.count) need attention"
    }
}
