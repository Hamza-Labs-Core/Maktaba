import MaktabaKit
import SwiftUI

/// The queue tab (story 42.8): on a wide window (an iPad, full screen or a wide split) the
/// stages beside the chosen stage's page; on a narrow one (an iPhone, an iPad in Slide Over
/// or a narrow split) the stages, and a stage's page pushed over them.
struct QueueScreen: View {
    @Environment(\.horizontalSizeClass) private var width
    @State private var chosen: String?

    var body: some View {
        if width == .regular {
            NavigationSplitView {
                QueueView(selection: $chosen)
            } detail: {
                NavigationStack {
                    if let chosen {
                        StageView(stage: chosen).id(chosen)
                    } else {
                        ContentUnavailableView("Choose a stage", systemImage: "list.bullet.rectangle")
                    }
                }
            }
        } else {
            NavigationStack { QueueView() }
        }
    }
}

/// The processing queue: every stage with what waits, runs and failed; pause or resume one
/// (a swipe, or its page) or all of them; a stage's page lists its failures. Given a
/// [selection], a stage opens beside the list (QueueScreen); without one, it is pushed.
struct QueueView: View {
    var selection: Binding<String?>?
    @Environment(RemoteModel.self) private var model

    var body: some View {
        // a List with a selection turns its links into choosing (the split's detail); a plain
        // one pushes them
        if let selection {
            decorated(List(selection: selection) { rows })
        } else {
            decorated(List { rows })
                .navigationDestination(for: String.self) { StageView(stage: $0) }
        }
    }

    @ViewBuilder private var rows: some View {
        ProblemSection()
        if model.queue?.pausedAll == true {
            Section {
                Label("Every stage is paused. Running jobs finish; nothing new starts.", systemImage: "pause.circle")
                    .foregroundStyle(Color.brass)
            }
        }
        if !model.running.isEmpty {
            Section {
                ForEach(model.running.prefix(Self.runningShown)) { job in
                    RunningJobRow(job: job, stageLabel: label(job.stage))
                }
            } header: {
                Text("Running now (\(model.running.count))")
            } footer: {
                if model.running.count > Self.runningShown {
                    Text("And \(model.running.count - Self.runningShown) more; the oldest are shown first.")
                }
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

    static let runningShown = 20

    private func label(_ stage: String) -> String {
        model.queue?.stages.first { $0.stage == stage }?.label ?? stage
    }

    private func decorated(_ list: some View) -> some View {
        list
            .maktabaSurface()
            .overlay {
                if !model.loaded { ProgressView() }
            }
            .navigationTitle("Queue")
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

/// A job running now: its stage, its subject, how long it has run, its progress.
struct RunningJobRow: View {
    let job: RunningJob
    let stageLabel: String

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Text(job.subjectLabel).font(.subheadline.bold()).lineLimit(1)
                Spacer()
                Text(stageLabel).font(.caption.bold()).foregroundStyle(Color.brass)
            }
            TimelineView(.periodic(from: .now, by: 1)) { context in
                Text(details(now: context.date))
                    .font(.caption)
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
            }
            if let f = job.fraction {
                ProgressView(value: f).tint(Color.brass)
            }
        }
        .accessibilityElement(children: .combine)
    }

    private func details(now: Date) -> String {
        var parts: [String] = []
        if let secs = job.runningFor(now: now) {
            parts.append(runningText(secs))
        } else {
            parts.append(job.state == "claimed" ? String(localized: "starting") : job.state.replacingOccurrences(of: "_", with: " "))
        }
        if let step = job.progressStep, !step.isEmpty { parts.append(step.replacingOccurrences(of: "_", with: " ")) }
        if job.attempts > 1, job.maxAttempts > 0 { parts.append(String(localized: "attempt \(job.attempts) of \(job.maxAttempts)")) }
        return parts.joined(separator: " · ")
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
                    Counter(text: Text("\(row.waiting) waiting"), systemImage: "hourglass")
                    Counter(text: Text("\(row.running) running"), systemImage: "gearshape.2")
                    if row.failed > 0 {
                        Counter(text: Text("\(row.failed) failed"), systemImage: "xmark.octagon")
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
    let text: Text
    let systemImage: String

    var body: some View {
        Label {
            text.monospacedDigit()
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
        switch row.status {
        case .running: .green
        case .paused, .pausedAll: .brass
        case .noWorker, .notSetUp: .red
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
    /// The last read of the failures did not work (the problem above says why).
    @State private var readFailed = false

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
                            LabeledContent("Rate", value: String(localized: "\(s.ratePerMin.formatted(.number.precision(.fractionLength(0...1)))) a minute"))
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
                if readFailed {
                    Button("Try again", systemImage: "arrow.clockwise") { Task { await loadFailures() } }
                } else if !loaded {
                    HStack { Spacer(); ProgressView(); Spacer() }
                } else if failures.isEmpty {
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
        // a failed read leaves what was shown (the problem says why), never "No failures"
        if let f = await model.failures(stage: stage) {
            failures = f
            loaded = true
            readFailed = false
        } else {
            readFailed = true
        }
    }
}
