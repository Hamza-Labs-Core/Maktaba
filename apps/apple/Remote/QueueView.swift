import MaktabaKit
import SwiftUI

/// The processing queue: every stage with what waits, runs and failed; pause or resume one
/// (a swipe, or its page) or all of them; a stage's page lists its failures.
struct QueueView: View {
    @Environment(RemoteModel.self) private var model

    var body: some View {
        List {
            ProblemSection()
            if model.queue?.pausedAll == true {
                Section {
                    Label("Every stage is paused. Running jobs finish; nothing new starts.", systemImage: "pause.circle")
                        .foregroundStyle(Color.brass)
                }
            }
            Section {
                ForEach(model.queue?.rows ?? []) { row in
                    NavigationLink(value: row.id) {
                        StageRow(row: row, busy: model.busy.contains(row.id))
                    }
                    .swipeActions {
                        if row.info.paused {
                            Button("Resume", systemImage: "play.fill") { Task { await model.setPaused(row, false) } }
                                .tint(.green)
                        } else {
                            Button("Pause", systemImage: "pause.fill") { Task { await model.setPaused(row, true) } }
                                .tint(Color.brass)
                        }
                    }
                }
            }
        }
        .maktabaSurface()
        .overlay {
            if !model.loaded { ProgressView() }
        }
        .navigationTitle("Queue")
        .navigationDestination(for: String.self) { StageView(stage: $0) }
        .refreshable { await model.load() }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                if model.queue?.pausedAll == true {
                    BusyButton(title: "Resume all", systemImage: "play.fill", busy: model.busy.contains("all")) {
                        await model.setPausedAll(false)
                    }
                } else {
                    BusyButton(title: "Pause all", systemImage: "pause.fill", busy: model.busy.contains("all")) {
                        await model.setPausedAll(true)
                    }
                    .disabled(model.queue == nil)
                }
            }
            AccountMenu()
        }
    }
}

struct StageRow: View {
    let row: QueueRow
    let busy: Bool

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 4) {
                Text(row.info.label).font(.headline)
                HStack(spacing: 12) {
                    Counter(value: row.waiting, label: "waiting", systemImage: "hourglass")
                    Counter(value: row.running, label: "running", systemImage: "gearshape.2")
                    if row.failed > 0 {
                        Counter(value: row.failed, label: "failed", systemImage: "xmark.octagon")
                            .foregroundStyle(.red)
                    }
                }
                .font(.caption)
                .foregroundStyle(.secondary)
            }
            Spacer()
            if busy {
                ProgressView()
            } else {
                StateBadge(row: row)
            }
        }
        .padding(.vertical, 2)
    }
}

struct Counter: View {
    let value: Int
    let label: String
    let systemImage: String

    var body: some View {
        Label {
            Text("\(value, format: .number) \(label)").monospacedDigit()
        } icon: {
            Image(systemName: systemImage)
        }
        .labelStyle(.titleAndIcon)
    }
}

struct StateBadge: View {
    let row: QueueRow

    var body: some View {
        Text(row.state)
            .font(.caption.bold())
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(color.opacity(0.18), in: .capsule)
            .foregroundStyle(color)
    }

    private var color: Color {
        switch row.state {
        case "Running": .green
        case "Paused", "Paused (all)": .brass
        case "No worker", "Not set up": .red
        case "Off": .secondary
        default: .secondary
        }
    }
}

/// A stage's page: its counts, pause or resume, and its failures grouped by cause.
struct StageView: View {
    let stage: String
    @Environment(RemoteModel.self) private var model
    @State private var failures: [FailureGroup] = []
    @State private var loaded = false

    private var row: QueueRow? { model.queue?.rows.first { $0.id == stage } }

    var body: some View {
        List {
            ProblemSection()
            if let row {
                Section {
                    LabeledContent("State") { StateBadge(row: row) }
                    LabeledContent("Waiting", value: row.waiting, format: .number)
                    LabeledContent("Running", value: row.running, format: .number)
                    LabeledContent("Failed", value: row.failed, format: .number)
                    if let s = row.summary {
                        LabeledContent("Done in 24 h", value: s.counts.done24h, format: .number)
                        if s.ratePerMin > 0 {
                            LabeledContent("Rate", value: "\(s.ratePerMin.formatted(.number.precision(.fractionLength(0...1)))) a minute")
                        }
                        if let eta = s.etaSec, eta > 0 {
                            LabeledContent("Time left", value: Duration.seconds(eta).formatted(.units(allowed: [.hours, .minutes], width: .abbreviated)))
                        }
                    }
                }
                Section {
                    if row.info.paused {
                        BusyButton(title: "Resume", systemImage: "play.fill", busy: model.busy.contains(stage)) {
                            await model.setPaused(row, false)
                        }
                    } else {
                        BusyButton(title: "Pause", systemImage: "pause.fill", busy: model.busy.contains(stage)) {
                            await model.setPaused(row, true)
                        }
                    }
                } footer: {
                    Text("Paused, the stage's running jobs finish and nothing new starts.")
                }
            }
            Section("Failures") {
                if loaded, failures.isEmpty {
                    Label("No failures", systemImage: "checkmark.seal").foregroundStyle(.secondary)
                }
                ForEach(failures) { g in
                    GroupRow(group: g, busy: model.busy.contains(g.id), showsStage: false) {
                        await model.retry([g])
                        await loadFailures()
                    }
                }
            }
        }
        .maktabaSurface()
        .navigationTitle(row?.info.label ?? stage)
        #if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
        .refreshable {
            await model.load()
            await loadFailures()
        }
        .task(id: row?.failed) { await loadFailures() }
    }

    private func loadFailures() async {
        if let f = await model.failures(stage: stage) { failures = f }
        loaded = true
    }
}
