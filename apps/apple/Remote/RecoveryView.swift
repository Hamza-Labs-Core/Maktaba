import MaktabaKit
import SwiftUI

/// The recovery console on the Remote (Epic 53 stories 53.04 and 53.05): what the web's
/// recovery page shows and does, from the same routes, `/recovery/api/*`, which the
/// supervisor answers with the API or the database down. The state of each group and part
/// with its last error, Restart a part, Restart everything, safe mode on and off, going back
/// a version (confirmed twice), the disk and the last warnings and errors. Restoring a backup
/// and the diagnostics download stay on the web page, linked at the end. The console is the
/// rule (admins only): a refusal says so and never signs the app out.
struct RecoveryView: View {
    @Environment(RemoteModel.self) private var model
    @State private var recovery: RecoveryModel?

    var body: some View {
        Group {
            if let recovery {
                RecoveryList(recovery: recovery, home: model.client.home)
            } else {
                ProgressView()
            }
        }
        .navigationTitle("Recovery")
        .task {
            let r = recovery ?? RecoveryModel(client: model.client)
            recovery = r
            // a fresh pass while the API answers, for the next time it does not
            let client = model.client
            Task { await client.refreshRecoveryPass() }
            await r.run()
        }
    }
}

private struct RecoveryList: View {
    @Bindable var recovery: RecoveryModel
    let home: URL

    var body: some View {
        List {
            switch recovery.phase {
            case .loading:
                Section { ProgressView().frame(maxWidth: .infinity) }
            case .denied:
                Section {
                    Label("Only an admin of this home can open the recovery page.", systemImage: "lock")
                        .accessibilityIdentifier("recovery.denied")
                    Text("Or, once the home answers again, sign in as an admin and come back to this page.")
                        .foregroundStyle(.secondary)
                }
            case .notHere:
                Section {
                    Label("This home has no recovery page yet. Update it to use one.", systemImage: "info.circle")
                        .accessibilityIdentifier("recovery.notHere")
                }
            case .unreachable:
                Section {
                    Label("The recovery page did not answer.", systemImage: "wifi.exclamationmark")
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("recovery.unreachable")
                    Button(ErrorStrings.forPreferredLanguages().text(.tryAgain), systemImage: "arrow.clockwise") {
                        Task { await recovery.load() }
                    }
                }
            case .ready:
                if let state = recovery.state { content(state) }
            }
        }
        .maktabaSurface()
        .refreshable { await recovery.load() }
        // the last action's outcome, pinned below the list: a part's Restart is far down the list,
        // and a line at its top would be out of sight (and out of the hierarchy) when it lands
        .safeAreaInset(edge: .bottom) {
            if let outcome = recovery.outcome {
                Group {
                    switch outcome {
                    case .done:
                        Label("Done.", systemImage: "checkmark").accessibilityIdentifier("recovery.done")
                    case .failed(let why) where !why.isEmpty:
                        Label("That did not work: \(why)", systemImage: "xmark.octagon").foregroundStyle(.red)
                            .accessibilityIdentifier("recovery.failed")
                    case .failed:
                        Label("That did not work.", systemImage: "xmark.octagon").foregroundStyle(.red)
                            .accessibilityIdentifier("recovery.failed")
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding()
                .background(.thinMaterial)
            }
        }
        .alert(recovery.asking.map { title($0.action) } ?? "Recovery", isPresented: Binding(
            get: { recovery.asking != nil },
            set: { if !$0 { recovery.cancel() } }
        ), presenting: recovery.asking) { asking in
            Button(title(asking.action), role: asking.action.confirms > 1 ? .destructive : nil) {
                Task { await recovery.confirm(asking) }
            }
            .accessibilityIdentifier("recovery.confirm")
            Button("Cancel", role: .cancel) { recovery.cancel() }
        } message: { asking in
            Text(asking.step > 1 ? "This replaces what is there now. Are you sure?" : "Are you sure?")
        }
    }

    @ViewBuilder
    private func content(_ s: RecoveryState) -> some View {
        Section {
            if s.safeMode {
                Label { Text(verbatim: StripWords.localized(StripWords.safe)) } icon: { Image(systemName: "shield.lefthalf.filled") }
                    .foregroundStyle(Color.brass)
                    .accessibilityIdentifier("recovery.safe")
            }
            if s.allWell {
                Label("Every part of Maktaba is working.", systemImage: "checkmark.circle").foregroundStyle(.green)
            } else {
                Label("Part of Maktaba isn't working. You can see what happened and fix it here.",
                      systemImage: "exclamationmark.triangle")
            }
        }
        Section("Status") {
            ForEach(s.groups, id: \.name) { g in
                LabeledContent {
                    Text(Self.stateWord(g.state)).foregroundStyle(Self.color(g.state))
                } label: {
                    Text(Self.groupWord(g.name))
                }
                .accessibilityIdentifier("recovery.group.\(g.name)")
            }
        }
        Section("Parts") {
            ForEach(s.components) { part in
                PartRow(part: part, busy: recovery.running == .restart(part.id)) { recovery.ask(.restart(part.id)) }
            }
        }
        Section {
            actionButton(.restartAll, systemImage: "arrow.clockwise.circle")
            if s.safeMode {
                actionButton(.leaveSafe, systemImage: "shield.slash")
                actionButton(.staySafe, systemImage: "shield")
            } else {
                actionButton(.safeMode, systemImage: "shield.lefthalf.filled")
            }
            if let version = s.rollback {
                actionButton(.rollback(version), systemImage: "arrow.uturn.backward.circle")
            }
        } header: {
            Text("Actions")
        } footer: {
            Text("Disk: \(Self.bytes(s.diskFree)) free, logs \(Self.bytes(s.logsBytes))")
                .accessibilityIdentifier("recovery.disk")
        }
        Section {
            if recovery.lines.isEmpty {
                Text("No warnings or errors in this window.").foregroundStyle(.secondary)
            }
            ForEach(recovery.lines) { line in LineRow(line: line) }
            Button("Load 15 more minutes", systemImage: "clock.arrow.circlepath") {
                Task { await recovery.loadMore() }
            }
            .accessibilityIdentifier("recovery.more")
        } header: {
            Text("Recent problems")
        }
        Section {
            Link(destination: home.appending(path: "recovery")) {
                Label("Open the recovery page", systemImage: "safari")
            }
            .accessibilityIdentifier("recovery.web")
        } footer: {
            Text("Restoring a backup and downloading diagnostics are on the recovery page, in a browser.")
        }
    }

    private func actionButton(_ action: RecoveryAction, systemImage: String) -> some View {
        Button {
            recovery.ask(action)
        } label: {
            if recovery.running == action {
                ProgressView()
            } else {
                Label(title(action), systemImage: systemImage)
            }
        }
        .disabled(recovery.running != nil)
        .accessibilityIdentifier("recovery.action.\(Self.slug(action))")
    }

    private func title(_ action: RecoveryAction) -> LocalizedStringKey {
        switch action {
        case .restart: "Restart"
        case .restartAll: "Restart everything"
        case .safeMode: "Start in safe mode"
        case .staySafe: "Stay in safe mode"
        case .leaveSafe: "Leave safe mode"
        case .rollback(let v): "Go back to version \(v)"
        }
    }

    static func slug(_ action: RecoveryAction) -> String {
        switch action {
        case .restart(let id): "restart.\(id)"
        case .restartAll: "restartAll"
        case .safeMode: "safeMode"
        case .staySafe: "staySafe"
        case .leaveSafe: "leaveSafe"
        case .rollback: "rollback"
        }
    }

    static func groupWord(_ g: String) -> LocalizedStringKey {
        switch g {
        case "database": "Database"
        case "api": "Maktaba service"
        case "streaming": "Playback"
        case "processing": "Processing"
        case "search": "Search"
        case "logs": "Log search"
        default: "Other parts"
        }
    }

    static func stateWord(_ s: String) -> LocalizedStringKey {
        switch s {
        case "ok": "working"
        case "starting": "starting"
        case "degraded": "having trouble"
        case "restarting": "restarting"
        case "down": "stopped"
        default: "off"
        }
    }

    static func color(_ s: String) -> Color {
        switch s {
        case "ok": .green
        case "starting", "degraded": Color.brass
        case "restarting", "down": .red
        default: .secondary
        }
    }

    static func bytes(_ n: Int64) -> String {
        n < 0 ? "?" : ByteCountFormatter.string(fromByteCount: n, countStyle: .file)
    }
}

/// One part: its catalog id, its state (and the reconciler's reason), restarts, when it last
/// stopped and its last error line, and Restart when it may be restarted from here.
private struct PartRow: View {
    let part: RecoveryPart
    let busy: Bool
    let restart: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Text(verbatim: part.id).font(.body.monospaced()).environment(\.layoutDirection, .leftToRight)
                Spacer()
                Text(RecoveryList.stateWord(part.state)).foregroundStyle(RecoveryList.color(part.state))
            }
            if let reason = part.reason, !reason.isEmpty {
                Text(verbatim: reason).font(.caption.monospaced()).foregroundStyle(.secondary)
            }
            Group {
                if let at = part.lastExitDate, let code = part.lastCode {
                    Text("\(part.restarts) restarts · last stopped \(at.formatted(date: .abbreviated, time: .shortened)) (code \(code))")
                } else {
                    Text("\(part.restarts) restarts")
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            if let error = part.lastError, !error.isEmpty {
                Text(verbatim: error).font(.caption.monospaced()).foregroundStyle(.red).lineLimit(4)
            }
            if part.canRestart {
                Button(action: restart) {
                    if busy { ProgressView() } else { Label("Restart", systemImage: "arrow.clockwise") }
                }
                .buttonStyle(.bordered)
                .disabled(busy)
                .accessibilityIdentifier("recovery.restart.\(part.id)")
            }
        }
        .padding(.vertical, 2)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("recovery.part.\(part.id)")
    }
}

/// One warning or error line: its words, then when, its level and part.
private struct LineRow: View {
    let line: RecoveryLine

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(verbatim: line.msg).font(.caption.monospaced()).lineLimit(6)
                .foregroundStyle(line.level == "error" ? .red : .primary)
            Text(verbatim: [line.ts.map { $0.formatted(date: .omitted, time: .standard) }, line.level,
                            line.component.isEmpty ? line.service : line.component]
                .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
        .environment(\.layoutDirection, .leftToRight)
    }
}
