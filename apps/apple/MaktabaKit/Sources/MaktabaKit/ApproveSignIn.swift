import SwiftUI

/// "Approve a sign-in" (Epic 42 story 42.6): a device shows a code (a TV, a phone or a
/// computer signing in by code); a person signed in here types it or scans its QR code, sees
/// what is asking (app, system, device, the name it gave), and approves it explicitly. The
/// device is then signed in as this person. The Remote's and the Player's account menus
/// open it (`AppSession.approvingSignIn`).
public struct ApproveSignInView: View {
    let client: APIClient
    let user: User
    let homeName: String
    @Environment(\.dismiss) private var dismiss
    @State private var code = ""
    /// The code looked up, and what the home said of it.
    @State private var looked: (code: String, lookup: DeviceLookup)?
    @State private var busy = false
    @State private var problem: String?
    @State private var approved: String?
    @State private var scanning = false
    private let words = ConnectStrings.forPreferredLanguages()

    public init(client: APIClient, user: User, homeName: String) {
        self.client = client
        self.user = user
        self.homeName = homeName
    }

    public var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField(words.text(.codeField), text: $code)
                        .font(.title3.monospaced())
                        .autocorrectionDisabled()
                        #if os(iOS)
                        .textInputAutocapitalization(.characters)
                        #endif
                        .environment(\.layoutDirection, .leftToRight)
                        .onSubmit(lookUp)
                        .onChange(of: code) { _, _ in
                            if looked != nil || approved != nil {
                                looked = nil
                                approved = nil
                            }
                        }
                        .accessibilityIdentifier("approve.code")
                    Button(action: lookUp) {
                        if busy && looked == nil {
                            ConnectBusyLabel(ConnectProgress.Busy.lookingUpCode.text(words.locale))
                        } else {
                            Text(words.text(.lookUp))
                        }
                    }
                    .disabled(busy || code.trimmingCharacters(in: .whitespaces).isEmpty)
                    .accessibilityIdentifier("approve.lookUp")
                    #if os(iOS)
                    Button(words.text(.scanCode), systemImage: "qrcode.viewfinder") { scanning = true }
                        .accessibilityIdentifier("approve.scan")
                    #endif
                } footer: {
                    Text(words.text(.approveIntro, homeName))
                }
                if let approved {
                    Section {
                        Label(words.text(.approved, approved), systemImage: "checkmark.seal.fill")
                            .foregroundStyle(.green)
                            .accessibilityIdentifier("approve.done")
                    }
                } else if let looked {
                    result(looked.code, looked.lookup)
                }
                if let problem {
                    Section {
                        Label(problem, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                            .accessibilityIdentifier("approve.problem")
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle(words.text(.approveTitle))
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(words.text(.done)) { dismiss() }
                        .accessibilityIdentifier("approve.close")
                }
            }
        }
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
        #if os(iOS)
        .sheet(isPresented: $scanning) {
            QRScannerSheet(words: words) { text in
                if case let .signInCode(c) = ScannedCode.parse(text) {
                    code = c
                    lookUp()
                } else {
                    problem = words.text(.notASignInCode)
                }
            }
        }
        #endif
    }

    @ViewBuilder private func result(_ shown: String, _ lookup: DeviceLookup) -> some View {
        switch lookup {
        case let .found(d):
            Section(words.text(.asking)) {
                LabeledContent(words.text(.code)) {
                    Text(verbatim: shown).monospaced().environment(\.layoutDirection, .leftToRight)
                }
                if let app = d.clientApp { LabeledContent(words.text(.app), value: words.appName(app)) }
                if let p = d.platform { LabeledContent(words.text(.platform), value: p) }
                if let dev = d.device { LabeledContent(words.text(.device), value: dev) }
                if let label = d.deviceLabel { LabeledContent(words.text(.deviceName), value: label) }
                if let exp = d.expiresAt {
                    LabeledContent(words.text(.expires)) {
                        Text(exp, style: .relative)
                    }
                }
                approveButtons(shown, name: d.deviceLabel ?? d.device)
            }
            .accessibilityIdentifier("approve.found")
        case .unknownOnThisHome:
            Section {
                Text(words.text(.unknownOnThisHome))
                approveButtons(shown, name: nil)
            }
        case .notFound:
            Section {
                Label(words.text(.notFound), systemImage: "questionmark.circle")
                    .accessibilityIdentifier("approve.notFound")
            }
        }
    }

    @ViewBuilder private func approveButtons(_ shown: String, name: String?) -> some View {
        Text(words.text(.approveWarning, user.username, user.username))
            .font(.callout)
            .foregroundStyle(.secondary)
        Button {
            approve(shown, name: name)
        } label: {
            if busy { ConnectBusyLabel(ConnectProgress.Busy.approving.text(words.locale)) } else { Text(words.text(.approve)).bold() }
        }
        .buttonStyle(.borderedProminent)
        .disabled(busy)
        .accessibilityIdentifier("approve.approve")
        Button(words.text(.notThis), role: .cancel) {
            looked = nil
            code = ""
        }
        .disabled(busy)
    }

    private func lookUp() {
        problem = nil
        approved = nil
        guard let normalized = DeviceCode.normalize(code) ?? {
            if case let .signInCode(c) = ScannedCode.parse(code) { return c } else { return nil }
        }() else {
            problem = words.text(.notACode)
            return
        }
        code = normalized
        busy = true
        Task {
            defer { busy = false }
            do {
                looked = (normalized, try await client.pendingDevice(code: normalized))
            } catch {
                problem = HomeFailure(error).message()
            }
        }
    }

    private func approve(_ shown: String, name: String?) {
        problem = nil
        busy = true
        Task {
            defer { busy = false }
            do {
                try await client.approveDevice(code: shown)
                approved = name ?? words.text(.anotherDevice)
                looked = nil
            } catch let e as APIError {
                switch e {
                case .status(409, _, _): problem = words.text(.alreadyApproved)
                case .status(404, _, _), .status(410, _, _): problem = words.text(.expired)
                case .status(422, _, _): problem = words.text(.notACode)
                default: problem = e.message
                }
            } catch {
                problem = HomeFailure(error).message()
            }
        }
    }
}
