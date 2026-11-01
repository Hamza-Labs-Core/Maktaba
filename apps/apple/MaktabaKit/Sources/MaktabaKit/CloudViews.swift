import SwiftUI

/// The home picker's third way (story 42.7): sign in to Maktaba Cloud (email, password; its
/// address under Advanced), see the homes the account reaches (online or not, linked or
/// not), and pick a linked one to sign in to it. Shown from `SessionGate`, so it stays up
/// while the chosen home is reached and signed in to, and says why when that fails.
struct CloudSheet: View {
    @Bindable var session: AppSession
    let words: ConnectStrings
    @State private var email = ""
    @State private var password = ""
    @State private var address = ""
    @State private var busy = false
    /// The home being signed in to (its cloud id).
    @State private var signingInto: String?
    @State private var problem: String?
    @State private var showAdvanced = false

    var body: some View {
        NavigationStack {
            Form {
                if let problem {
                    Section {
                        Label(problem, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                            .accessibilityIdentifier("cloud.problem")
                    }
                }
                // the chosen home being reached and signed in to, step by step
                if signingInto != nil, let p = session.progress, p.isWorking {
                    Section {
                        ConnectProgressView(p, locale: words.locale)
                    }
                    .accessibilityIdentifier("cloud.progress")
                }
                if let user = session.cloudUser {
                    homes(user)
                } else {
                    signInForm
                }
            }
            .formStyle(.grouped)
            .navigationTitle(words.text(.throughCloud))
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(words.text(.close)) { session.choosingThroughCloud = false }
                        .accessibilityIdentifier("cloud.close")
                }
            }
        }
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
        .onAppear {
            address = session.cloudAddress == CloudAddress.standard ? "" : session.cloudAddress.absoluteString
            showAdvanced = !address.isEmpty
        }
        .onChange(of: session.signedIn) { _, now in
            if now { session.choosingThroughCloud = false }
        }
    }

    // MARK: signing in to the cloud

    @ViewBuilder private var signInForm: some View {
        Section {
            TextField(words.text(.email), text: $email)
                .textContentType(.username)
                .autocorrectionDisabled()
                #if os(iOS)
                .keyboardType(.emailAddress)
                .textInputAutocapitalization(.never)
                #endif
                .environment(\.layoutDirection, .leftToRight)
                .accessibilityIdentifier("cloud.email")
            SecureField(words.text(.password), text: $password)
                .textContentType(.password)
                .onSubmit(signIn)
                .accessibilityIdentifier("cloud.password")
        } header: {
            Text(words.text(.cloudSignInTitle))
        } footer: {
            Text(words.text(.cloudIntro))
        }
        Button(action: signIn) {
            if busy { ConnectBusyLabel(ConnectProgress.Busy.cloudSignIn.text(words.locale)) } else { Text(words.text(.signIn)) }
        }
        .disabled(email.trimmingCharacters(in: .whitespaces).isEmpty || password.isEmpty || busy)
        .accessibilityIdentifier("cloud.signin")
        Section {
            // a disclosure by hand: DisclosureGroup is not on tvOS, which builds this package too
            Button(words.text(.advanced), systemImage: showAdvanced ? "chevron.down" : "chevron.forward") {
                showAdvanced.toggle()
            }
            .accessibilityIdentifier("cloud.advanced")
            if showAdvanced {
                TextField(words.text(.cloudAddress), text: $address, prompt: Text(verbatim: CloudAddress.standard.absoluteString))
                    .textContentType(.URL)
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    #endif
                    .environment(\.layoutDirection, .leftToRight)
                    .accessibilityIdentifier("cloud.address")
                if !address.isEmpty {
                    Button(words.text(.useStandard)) { address = "" }
                }
            }
        } footer: {
            if showAdvanced {
                Text(words.text(.cloudAddressHint, Self.isolated(CloudAddress.standard.host() ?? "")))
            }
        }
    }

    private func signIn() {
        guard !busy else { return }
        problem = nil
        guard session.setCloudAddress(address) else {
            problem = words.text(.notACloudAddress)
            return
        }
        busy = true
        Task {
            defer { busy = false }
            do {
                try await session.cloudSignIn(email: email, password: password)
                password = ""
            } catch {
                problem = words.cloudProblem(error)
            }
        }
    }

    // MARK: the account's homes

    @ViewBuilder private func homes(_ user: CloudUser) -> some View {
        let list = session.cloudHomes
        Section {
            if list.isEmpty {
                Text(words.text(.noCloudHomes)).foregroundStyle(.secondary)
            }
            ForEach(list) { home in
                if home.canSignIn {
                    Button { pick(home) } label: { row(home) }
                        .disabled(signingInto != nil)
                        .accessibilityIdentifier("cloud.home.\(home.serverID)")
                } else {
                    // shown, never offered: owning a home is no account in it
                    row(home)
                        .accessibilityIdentifier("cloud.home.\(home.serverID)")
                }
            }
        } header: {
            Text(words.text(.yourHomes))
        } footer: {
            if list.contains(where: { !$0.canSignIn }) {
                Text(words.text(.notLinkedHint))
            }
        }
        Section {
            Button(words.text(.refresh), systemImage: "arrow.clockwise") { refresh() }
                .disabled(busy || signingInto != nil)
                .accessibilityIdentifier("cloud.refresh")
            Button(words.text(.cloudSignOut), systemImage: "rectangle.portrait.and.arrow.right", role: .destructive) {
                problem = nil
                session.cloudSignOut()
            }
            .disabled(signingInto != nil)
            .accessibilityIdentifier("cloud.signout")
        } footer: {
            Text(words.text(.signedInAs, Self.isolated(user.email)))
        }
    }

    private func row(_ home: CloudHome) -> some View {
        HStack(spacing: 12) {
            Image(systemName: home.canSignIn ? "house" : "house.slash")
                .foregroundStyle(home.canSignIn ? Color.accentColor : Color.secondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(home.name).foregroundStyle(home.canSignIn ? Color.primary : Color.secondary)
                HStack(spacing: 6) {
                    Circle()
                        .fill(home.online ? Color.green : Color.secondary)
                        .frame(width: 8, height: 8)
                        .accessibilityHidden(true)
                    Text(words.text(home.online ? .online : .offline))
                    if !home.linked {
                        Text(verbatim: "·").accessibilityHidden(true)
                        Text(words.text(.notLinked))
                    }
                }
                .font(.footnote)
                .foregroundStyle(.secondary)
            }
            Spacer()
            if signingInto == home.serverID {
                Image(systemName: "arrow.triangle.2.circlepath")
                    .foregroundStyle(Color.mktBrassText)
                    .accessibilityLabel(words.text(.signingInTo, home.name))
            }
        }
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }

    /// An address or email kept left to right inside an Arabic sentence (its dots and the
    /// sentence's full stop stay in place).
    static func isolated(_ s: String) -> String { "\u{2068}\(s)\u{2069}" }

    private func pick(_ home: CloudHome) {
        problem = nil
        signingInto = home.serverID
        Task {
            defer { signingInto = nil }
            do {
                try await session.signIn(through: home)
            } catch {
                problem = words.cloudProblem(error)
            }
        }
    }

    private func refresh() {
        problem = nil
        busy = true
        Task {
            defer { busy = false }
            do {
                try await session.loadCloudHomes()
            } catch {
                problem = words.cloudProblem(error)
            }
        }
    }
}
