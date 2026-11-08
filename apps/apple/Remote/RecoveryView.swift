import MaktabaKit
import SwiftUI

/// The recovery console on the Remote (Epic 53 stories 53.04 and 53.05): what the web's
/// recovery page shows and does, from the same routes, `/recovery/api/*`, which the
/// supervisor answers with the API or the database down. The state of each group and part
/// with its last error, Restart a part, Restart everything, safe mode on and off, going back
/// a version and restoring a backup (each confirmed twice, as the web page asks), the
/// diagnostics bundle handed to the share sheet, the disk and the last warnings and errors.
/// The console is the rule (admins only): a refusal says so and never signs the app out.
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
            if recovery == nil { RecoveryModel.sweepDiagnostics() } // a bundle left by an app that stopped
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
                    case .restoring:
                        Label("The home is restarting to put the backup back.", systemImage: "arrow.counterclockwise")
                            .accessibilityIdentifier("recovery.restoring")
                    case .noBackups:
                        Label("No backups were found on the data disk.", systemImage: "info.circle")
                            .accessibilityIdentifier("recovery.noBackups")
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
            Button(button(asking.action), role: asking.action.confirms > 1 ? .destructive : nil) {
                Task { await recovery.confirm(asking) }
            }
            .accessibilityIdentifier("recovery.confirm")
            Button("Cancel", role: .cancel) { recovery.cancel() }
        } message: { asking in
            Text(asking.step > 1 ? "This replaces what is there now. Are you sure?" : "Are you sure?")
        }
        .sheet(isPresented: Binding(
            get: { recovery.backups != nil },
            set: { if !$0 { recovery.backups = nil } }
        )) {
            BackupPicker(backups: recovery.backups ?? [], pick: { recovery.pick($0) }, cancel: { recovery.backups = nil })
        }
        .sheet(isPresented: Binding(
            get: { recovery.diagnostics != nil },
            set: { if !$0 { recovery.doneSharing() } }
        ), onDismiss: { recovery.doneSharing() }) {
            if let file = recovery.diagnostics {
                ShareSheet(file: file) { recovery.doneSharing() }
                    .ignoresSafeArea()
            }
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
            fetchButton(.backups, "Restore a backup", systemImage: "externaldrive.badge.timemachine",
                        id: "recovery.action.restore") { await recovery.chooseBackup() }
            fetchButton(.diagnostics, "Download diagnostics", systemImage: "square.and.arrow.down",
                        id: "recovery.action.diagnostics") { await recovery.fetchDiagnostics() }
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
        }
    }

    /// Restore a backup and Download diagnostics: a read first (the backups, the bundle), its
    /// progress in the button.
    private func fetchButton(_ kind: RecoveryModel.Fetching, _ label: LocalizedStringKey, systemImage: String, id: String,
                             run: @escaping () async -> Void) -> some View {
        Button {
            Task { await run() }
        } label: {
            if recovery.fetching == kind {
                ProgressView()
            } else {
                Label(label, systemImage: systemImage)
            }
        }
        .disabled(recovery.running != nil || recovery.fetching != nil)
        .accessibilityIdentifier(id)
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
        .disabled(recovery.running != nil || recovery.fetching != nil)
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
        case .restore(let name): "Restore \(name)"
        }
    }

    /// The confirmation's button: the action's title, or Restore (the backup is in the alert's title).
    private func button(_ action: RecoveryAction) -> LocalizedStringKey {
        if case .restore = action { return "Restore" }
        return title(action)
    }

    static func slug(_ action: RecoveryAction) -> String {
        switch action {
        case .restart(let id): "restart.\(id)"
        case .restartAll: "restartAll"
        case .safeMode: "safeMode"
        case .staySafe: "staySafe"
        case .leaveSafe: "leaveSafe"
        case .rollback: "rollback"
        case .restore: "restore"
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

/// The backups the home can restore, newest first: its file name, when it was taken and its
/// size. Choosing one asks twice before anything is sent.
private struct BackupPicker: View {
    let backups: [RecoveryBackup]
    let pick: (RecoveryBackup) -> Void
    let cancel: () -> Void

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(backups) { b in
                        Button { pick(b) } label: {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(verbatim: b.name).font(.body.monospaced()).environment(\.layoutDirection, .leftToRight)
                                Text(verbatim: [b.at.map { $0.formatted(date: .abbreviated, time: .shortened) },
                                                b.size < 0 ? nil : RecoveryList.bytes(b.size)]
                                    .compactMap { $0 }.joined(separator: " · "))
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .accessibilityIdentifier("recovery.backup.\(b.name)")
                    }
                } footer: {
                    Text("Restoring replaces the library database with the backup and loses every change made since it was taken.")
                }
            }
            .maktabaSurface()
            .navigationTitle("Choose a backup")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel", action: cancel) }
            }
        }
        .accessibilityIdentifier("recovery.backups")
    }
}

/// The system share sheet for the diagnostics bundle's file; `done` when it closes, shared or not.
private struct ShareSheet: UIViewControllerRepresentable {
    let file: URL
    let done: () -> Void

    func makeUIViewController(context: Context) -> UIActivityViewController {
        let vc = UIActivityViewController(activityItems: [file], applicationActivities: nil)
        vc.completionWithItemsHandler = { _, _, _, _ in done() }
        return vc
    }

    func updateUIViewController(_ vc: UIActivityViewController, context: Context) {}
}
