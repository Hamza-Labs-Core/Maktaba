import MaktabaKit
import SwiftUI

/// "Needs attention": the home's failed jobs grouped by cause, a Retry on each group that
/// can be retried, and Retry all (as on the web's queue page).
struct StatusView: View {
    let session: AppSession
    let client: APIClient

    @State private var groups: [FailureGroup] = []
    @State private var loaded = false
    @State private var busy: String?
    @State private var problem: String?

    private var retryable: [FailureGroup] { groups.filter { $0.action == "retry" } }

    var body: some View {
        NavigationStack {
            List {
                if let problem {
                    Section { Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(.red) }
                }
                Section("Needs attention") {
                    if loaded, groups.isEmpty {
                        Label("Nothing needs attention", systemImage: "checkmark.seal")
                            .foregroundStyle(.secondary)
                    }
                    ForEach(groups) { g in
                        GroupRow(group: g, busy: busy == g.id) {
                            Task { await retry([g]) }
                        }
                    }
                }
            }
            .navigationTitle(session.current?.name ?? "Maktaba Remote")
            .refreshable { await load() }
            .task {
                while !Task.isCancelled {
                    await load()
                    try? await Task.sleep(for: .seconds(15))
                }
            }
            .toolbar {
                ToolbarItem(placement: .primaryAction) {
                    Button("Retry all", systemImage: "arrow.clockwise") {
                        Task { await retry(retryable) }
                    }
                    .disabled(retryable.isEmpty || busy != nil)
                }
                ToolbarItem(placement: .secondaryAction) {
                    Menu("Account", systemImage: "person.crop.circle") {
                        Button("Switch home", systemImage: "house") { session.switchHome() }
                        Button("Sign out", systemImage: "rectangle.portrait.and.arrow.right") { session.signOut() }
                    }
                }
            }
        }
    }

    private func load() async {
        do {
            groups = try await client.needsAttention()
            problem = nil
        } catch APIError.signedOut {
            session.signedOut()
        } catch {
            problem = error.localizedDescription
        }
        loaded = true
    }

    private func retry(_ gs: [FailureGroup]) async {
        busy = gs.count == 1 ? gs[0].id : "all"
        defer { busy = nil }
        for g in gs {
            do {
                try await client.retry(stage: g.stage, cause: g.cause)
            } catch {
                problem = "\(g.label): \(error.localizedDescription)"
            }
        }
        await load()
    }
}

struct GroupRow: View {
    let group: FailureGroup
    let busy: Bool
    let retry: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(group.count, format: .number).font(.headline.monospacedDigit())
                Text(group.label).font(.headline)
                Spacer()
                if group.action == "retry" {
                    Button(action: retry) {
                        if busy { ProgressView() } else { Text("Retry") }
                    }
                    .buttonStyle(.bordered)
                    .disabled(busy)
                }
            }
            Text(group.stage).font(.caption).foregroundStyle(.secondary)
            if let m = group.sampleMessage, !m.isEmpty {
                Text(m).font(.caption).foregroundStyle(.secondary).lineLimit(3)
            }
        }
        .padding(.vertical, 2)
    }
}
