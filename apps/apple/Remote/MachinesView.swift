import MaktabaKit
import SwiftUI

/// The home's machines: let new ones ask to join, answer a machine that asks (approve with
/// the code it shows, or turn it down), and let an approved machine go.
struct MachinesView: View {
    @Environment(RemoteModel.self) private var model
    @State private var approving: Machine?
    @State private var revoking: Machine?

    var body: some View {
        List {
            ProblemSection()
            if let home = model.cluster?.home {
                Section {
                    Toggle(isOn: Binding(get: { home.acceptJoins },
                                         set: { open in Task { await model.setAcceptJoins(open) } })) {
                        Label("Machines may ask to join", systemImage: "person.badge.plus")
                    }
                    .disabled(model.busy.contains("joins"))
                } footer: {
                    if home.acceptJoins, let until = home.acceptJoinsUntil {
                        Text("Open until \(until, style: .time). Each machine shows a six-digit code to approve it with.")
                    } else if !home.rolesSupported {
                        Text(home.rolesReason ?? "This home cannot take machines.")
                    } else {
                        Text("Turn on, then run the installer on the new machine. It opens for an hour.")
                    }
                }
            }
            let nodes = model.cluster?.sortedNodes ?? []
            section("Asking to join", nodes.filter { $0.group == .asking })
            section("Machines", nodes.filter { $0.group == .approved })
            section("Turned down or let go", nodes.filter { $0.group == .other })
            if model.loaded, nodes.isEmpty, model.cluster != nil {
                Label("No machines. This home works alone.", systemImage: "desktopcomputer")
                    .foregroundStyle(.secondary)
            }
        }
        .maktabaSurface()
        .overlay { if !model.loaded { ProgressView() } }
        .navigationTitle("Machines")
        .refreshable { await model.load() }
        .toolbar { AccountMenu() }
        .sheet(item: $approving) { ApproveSheet(machine: $0) }
        .confirmationDialog("Let \(revoking?.name ?? "the machine") go?", isPresented: Binding(get: { revoking != nil }, set: { if !$0 { revoking = nil } }),
                            titleVisibility: .visible, presenting: revoking) { m in
            Button("Revoke", role: .destructive) { Task { await model.revoke(m) } }
        } message: { _ in
            Text("It stops working for the home at once; its jobs go back to the queue. To use it again it must ask to join again.")
        }
    }

    @ViewBuilder private func section(_ title: String, _ machines: [Machine]) -> some View {
        if !machines.isEmpty {
            Section(title) {
                ForEach(machines) { m in
                    MachineRow(machine: m, busy: model.busy.contains(m.id))
                        .swipeActions {
                            if m.group == .approved {
                                Button("Revoke", systemImage: "xmark.circle") { revoking = m }.tint(.red)
                            }
                        }
                        .contextMenu {
                            if m.group == .approved {
                                Button("Revoke", systemImage: "xmark.circle", role: .destructive) { revoking = m }
                            }
                        }
                    if m.group == .asking {
                        HStack {
                            Button("Approve…", systemImage: "checkmark.shield") { approving = m }
                                .buttonStyle(.borderedProminent)
                            Button("Turn down", systemImage: "xmark", role: .destructive) { Task { await model.reject(m) } }
                                .buttonStyle(.bordered)
                        }
                        .disabled(model.busy.contains(m.id))
                    }
                }
            }
        }
    }
}

struct MachineRow: View {
    let machine: Machine
    let busy: Bool

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Image(systemName: "circle.fill")
                .font(.caption2)
                .foregroundStyle(dot)
                .accessibilityLabel(machine.online ? "Online" : "Offline")
            VStack(alignment: .leading, spacing: 4) {
                Text(machine.name).font(.headline)
                Text(details).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if busy { ProgressView() }
        }
    }

    private var dot: Color {
        switch machine.group {
        case .asking: .brass
        case .approved: machine.online ? .green : .secondary
        case .other: .red
        }
    }

    private var details: String {
        var parts: [String] = []
        switch machine.group {
        case .asking:
            if let a = machine.remoteAddr { parts.append("from \(a)") }
            if let e = machine.expiresAt { parts.append("asks until \(e.formatted(date: .omitted, time: .shortened))") }
        case .approved:
            parts.append(machine.online ? "online" : "offline")
            if !machine.online, let seen = machine.lastSeenAt { parts.append("seen \(seen.formatted(.relative(presentation: .named)))") }
        case .other:
            parts.append(machine.expired && machine.state == "pending" ? "request expired" : machine.state)
        }
        if let p = machine.platform { parts.append(p) }
        if let v = machine.version { parts.append(v) }
        return parts.joined(separator: " · ")
    }
}

/// Approving a machine: the six digits it shows, checked by the home.
struct ApproveSheet: View {
    let machine: Machine
    @Environment(RemoteModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var code = ""
    @State private var failure: ApproveFailure?
    @State private var busy = false
    @FocusState private var focused: Bool

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("123 456", text: $code)
                        .font(.title.monospacedDigit())
                        .multilineTextAlignment(.center)
                        .textContentType(.oneTimeCode)
                        #if os(iOS)
                        .keyboardType(.numberPad)
                        #endif
                        .focused($focused)
                        .onSubmit(approve)
                } header: {
                    Text("The code \(machine.name) shows")
                } footer: {
                    if let failure {
                        Text(failure.message).foregroundStyle(.red)
                    } else {
                        Text("Approve only a machine you are setting up: it will read the home's libraries and run its jobs.")
                    }
                }
            }
            .navigationTitle("Approve \(machine.name)")
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if busy {
                        ProgressView()
                    } else {
                        Button("Approve", action: approve)
                            .disabled(PairingCode.normalize(code) == nil)
                    }
                }
            }
            .onAppear { focused = true }
        }
        .presentationDetents([.medium, .large])
    }

    private func approve() {
        guard let digits = PairingCode.normalize(code), !busy else { return }
        busy = true
        Task {
            defer { busy = false }
            failure = await model.approve(machine, code: digits)
            if failure == nil { dismiss() }
        }
    }
}
