import SwiftUI

/// "Approve a TV or device" (Epic 42 story 42.6): a device shows a code (a TV, a phone or a
/// computer signing in by code); a person signed in here types it or scans its QR code, sees
/// what is asking (app, system, device, the name it gave), and approves it explicitly. The
/// device is then signed in as this person. The Remote's and the Player's account menus
/// open it (`AppSession.approvingSignIn`). A TV's code from Maktaba Cloud (story 58.03) is
/// confirmed ("Is this your TV?") and approved through the home; `ApproveCode` holds the steps.
public struct ApproveSignInView: View {
    let client: APIClient
    let user: User
    let homeName: String
    @Environment(\.dismiss) private var dismiss
    @State private var code = ""
    @State private var model: ApproveCode
    @State private var scanning = false
    private let words = ConnectStrings.forPreferredLanguages()

    public init(client: APIClient, user: User, homeName: String) {
        self.client = client
        self.user = user
        self.homeName = homeName
        _model = State(initialValue: ApproveCode(client: client))
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
                        .onChange(of: code) { _, now in
                            // the code shown was edited: what was looked up no longer applies
                            if model.step != .idle, model.step.code != now { model.reset() }
                        }
                        .accessibilityIdentifier("approve.code")
                    Button(action: lookUp) {
                        if model.busy && model.step == .idle {
                            ConnectBusyLabel(ConnectProgress.Busy.lookingUpCode.text(words.locale))
                        } else {
                            Text(words.text(.lookUp))
                        }
                    }
                    .disabled(model.busy || code.trimmingCharacters(in: .whitespaces).isEmpty)
                    .accessibilityIdentifier("approve.lookUp")
                    #if os(iOS)
                    Button(words.text(.scanCode), systemImage: "qrcode.viewfinder") { scanning = true }
                        .accessibilityIdentifier("approve.scan")
                    #endif
                } footer: {
                    Text(words.text(.approveIntro))
                }
                switch model.step {
                case .idle:
                    EmptyView()
                case let .approved(sentence):
                    Section {
                        Label(sentence, systemImage: "checkmark.seal.fill")
                            .foregroundStyle(.green)
                            .accessibilityIdentifier("approve.done")
                    }
                case let .home(shown, lookup):
                    result(shown, lookup)
                case let .cloud(shown, tv):
                    cloudResult(shown, tv)
                }
                if let problem = model.problem {
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
                    model.problem = words.text(.notASignInCode)
                }
            }
        }
        #endif
    }

    @ViewBuilder private func result(_ shown: String, _ lookup: DeviceLookup) -> some View {
        switch lookup {
        case let .found(d):
            Section(words.text(.asking)) {
                codeRow(shown)
                if let app = d.clientApp { LabeledContent(words.text(.app), value: words.appName(app)) }
                if let p = d.platform { LabeledContent(words.text(.platform), value: p) }
                if let dev = d.device { LabeledContent(words.text(.device), value: dev) }
                if let label = d.deviceLabel { LabeledContent(words.text(.deviceName), value: label) }
                if let exp = d.expiresAt {
                    LabeledContent(words.text(.expires)) {
                        Text(exp, style: .relative)
                    }
                }
                let name = d.deviceLabel ?? d.clientApp.map(words.appName) ?? d.device
                approveButtons(confirm: name.map { words.text(.confirmSignIn, $0, user.username) })
            }
            .accessibilityIdentifier("approve.found")
        case .unknownOnThisHome:
            Section {
                Text(words.text(.unknownOnThisHome))
                approveButtons(confirm: nil)
            }
        case .notFound:
            Section {
                Label(words.text(.notFound), systemImage: "questionmark.circle")
                    .accessibilityIdentifier("approve.notFound")
            }
        }
    }

    /// A TV's code from Maktaba Cloud: what the cloud knows of it (its system and country),
    /// for the person to confirm it is the TV in front of them.
    @ViewBuilder private func cloudResult(_ shown: String, _ tv: CloudTVCode) -> some View {
        Section(words.text(.asking)) {
            codeRow(shown)
            if let exp = tv.expiresAt {
                LabeledContent(words.text(.expires)) {
                    Text(exp, style: .relative)
                }
            }
            approveButtons(confirm: model.confirmation(tv))
        }
        .accessibilityIdentifier("approve.cloud")
    }

    private func codeRow(_ shown: String) -> some View {
        LabeledContent(words.text(.code)) {
            Text(verbatim: shown).monospaced().environment(\.layoutDirection, .leftToRight)
        }
    }

    @ViewBuilder private func approveButtons(confirm: String?) -> some View {
        // what is asking and as whom, before Approve
        if let confirm {
            Text(confirm)
                .font(.headline)
                .accessibilityIdentifier("approve.confirm")
        }
        Text(words.text(.approveWarning, user.username, user.username))
            .font(.callout)
            .foregroundStyle(.secondary)
        Button {
            Task { await model.approve() }
        } label: {
            if model.busy { ConnectBusyLabel(ConnectProgress.Busy.approving.text(words.locale)) } else { Text(words.text(.approve)).bold() }
        }
        .buttonStyle(.borderedProminent)
        .disabled(model.busy)
        .accessibilityIdentifier("approve.approve")
        Button(words.text(.notThis), role: .cancel) {
            model.reset()
            code = ""
        }
        .disabled(model.busy)
    }

    private func lookUp() {
        let typed = code
        Task {
            if let normalized = await model.lookUp(typed) { code = normalized }
        }
    }
}
