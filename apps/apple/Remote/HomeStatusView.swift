import MaktabaKit
import SwiftUI

/// The home at a glance: its name and release, whether the queue runs, what waits and
/// what failed, its machines.
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
}
