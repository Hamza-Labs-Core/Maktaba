import MaktabaKit
import SwiftUI

/// "Needs attention": the home's failed jobs grouped by cause, a Retry on each group that
/// can be retried, and Retry all (as on the web's queue page).
struct AttentionView: View {
    @Environment(RemoteModel.self) private var model

    var body: some View {
        List {
            ProblemSection()
            Section {
                if model.loaded, model.problem == nil, model.attention.isEmpty {
                    Label("Nothing needs attention", systemImage: "checkmark.seal")
                        .foregroundStyle(.secondary)
                }
                ForEach(model.attention) { g in
                    GroupRow(group: g, busy: model.busy.contains(g.id) || model.busy.contains("all")) {
                        await model.retry([g])
                    }
                }
            }
        }
        .maktabaSurface()
        .overlay { if !model.loaded { ProgressView() } }
        .navigationTitle("Needs attention")
        .refreshable { await model.load() }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                BusyButton(title: "Retry all", systemImage: "arrow.clockwise", busy: model.busy.contains("all")) {
                    await model.retry(model.retryable)
                }
                .disabled(model.retryable.isEmpty)
            }
            AccountMenu()
        }
    }
}

/// A group of failed jobs: how many, why, a sample, and Retry when retrying can help.
struct GroupRow: View {
    let group: FailureGroup
    let busy: Bool
    var showsStage = true
    let retry: () async -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(group.count, format: .number).font(.headline.monospacedDigit())
                Text(group.label).font(.headline)
                Spacer()
                if group.action == "retry" {
                    Button {
                        Task { await retry() }
                    } label: {
                        if busy { ProgressView() } else { Text("Retry") }
                    }
                    .buttonStyle(.bordered)
                    .disabled(busy)
                } else {
                    Text(Self.hint(group.action)).font(.caption).foregroundStyle(.secondary)
                }
            }
            if showsStage {
                Text(group.stage).font(.caption).foregroundStyle(.secondary)
            }
            if let m = group.sampleMessage, !m.isEmpty {
                Text(m).font(.caption).foregroundStyle(.secondary).lineLimit(3)
            }
        }
        .padding(.vertical, 2)
    }

    /// What fixes a group that retrying alone does not (done in the home's web pages).
    static func hint(_ action: String) -> String {
        switch action {
        case "open_settings": String(localized: "Fix in Settings")
        case "show_workers": String(localized: "Needs a worker")
        case "apply": String(localized: "Apply on the web")
        default: ""
        }
    }
}
