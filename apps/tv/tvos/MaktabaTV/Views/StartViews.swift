import MaktabaKit
import SwiftUI
import UIKit

// Epic 42 story 42.10: the home picker and sign-in by code on Apple TV, on MaktabaKit's
// session (homes known by their exact address, tokens bound to it, a link asked about
// first) and words (`ConnectStrings`, `TVConnectStrings`). Everything is reached with the
// remote: rows are focusable buttons, a row's other actions are under a long press.

/// The homes this TV knows (Recent homes: rename, remove), the ones announcing themselves on
/// this network (Bonjour `_maktaba._tcp`, `role=home` only, one per home id), and one by its
/// address or a pasted `maktaba://connect` link, asked whether it is a Maktaba home first.
struct TVHomePicker: View {
    @Bindable var session: AppSession
    @State private var address = ""
    @State private var problem: String?
    @State private var renaming: Home?
    @State private var newName = ""
    private let words = ConnectStrings.forPreferredLanguages()
    private let tv = TVConnectStrings.forPreferredLanguages()

    var body: some View {
        ZStack {
            LibraryBackdrop()
            ScrollView {
                VStack(alignment: .leading, spacing: 44) {
                    VStack(alignment: .leading, spacing: 8) {
                        SectionTitle(tv.text(.brand), size: 64)
                        Text(tv.text(.chooseHome))
                            .font(Theme.body(32))
                            .foregroundStyle(Theme.muted)
                    }
                    if let link = session.pendingLink {
                        TVLinkPrompt(session: session, link: link, words: words)
                    }
                    if session.connecting {
                        HStack(spacing: 16) {
                            ProgressView()
                            Text(words.text(.connecting)).foregroundStyle(Theme.muted)
                        }
                        .accessibilityIdentifier("picker.connecting")
                    }
                    if let shown = problem ?? session.connectProblem.map(words.reach) {
                        Label(shown, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                            .accessibilityIdentifier("picker.problem")
                    }
                    recent
                    nearby
                    byAddress
                }
                .disabled(session.connecting)
                .readingPanel(padding: 48)
                .frame(maxWidth: 1300)
                .padding(.vertical, 80)
                .frame(maxWidth: .infinity)
            }
        }
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
        .alert(words.text(.renameTitle), isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
            TextField(tv.text(.renamed), text: $newName)
            Button(words.text(.save)) {
                if let renaming { session.rename(renaming, to: newName) }
                renaming = nil
            }
            Button(words.text(.cancel), role: .cancel) { renaming = nil }
        } message: {
            Text(words.text(.renameHint))
        }
    }

    // MARK: sections

    @ViewBuilder private var recent: some View {
        if !session.homes.isEmpty {
            TVSection(words.text(.recentHomes), footer: tv.text(.holdHint)) {
                ForEach(session.recentHomes) { home in
                    TVHomeRow(name: home.name, address: home.displayAddress, systemImage: "house") {
                        clearProblems()
                        session.open(home)
                    }
                    .contextMenu {
                        Button(words.text(.rename), systemImage: "pencil") {
                            newName = home.renamed == true ? home.name : ""
                            renaming = home
                        }
                        Button(words.text(.remove), systemImage: "trash", role: .destructive) { session.forget(home) }
                    }
                    .accessibilityIdentifier("picker.recent.\(home.displayAddress)")
                }
            }
        }
    }

    private var nearby: some View {
        TVSection(words.text(.onThisNetwork), footer: tv.text(.nearbyHint)) {
            let list = TVStart.nearby(session.nearby, known: session.homes)
            ForEach(list) { home in
                TVHomeRow(name: home.name, address: Home(url: home.url, name: home.name).displayAddress, systemImage: "wifi") {
                    clearProblems()
                    session.use(Home(url: home.url, name: home.name, homeID: home.homeID))
                }
                .accessibilityIdentifier("picker.nearby.\(home.id)")
            }
            if list.isEmpty {
                HStack(spacing: 16) {
                    ProgressView()
                    Text(words.text(.searching)).foregroundStyle(Theme.muted)
                }
            }
        }
    }

    private var byAddress: some View {
        TVSection(words.text(.byAddress), footer: words.text(.addressHint)) {
            HStack(spacing: 24) {
                TextField(words.text(.addressPlaceholder), text: $address)
                    .keyboardType(.URL)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    .environment(\.layoutDirection, .leftToRight)
                    .onSubmit(add)
                    .frame(maxWidth: 900)
                    .accessibilityIdentifier("picker.address")
                Button(words.text(.connect), action: add)
                    .buttonStyle(.gold)
                    .disabled(address.trimmingCharacters(in: .whitespaces).isEmpty)
                    .accessibilityIdentifier("picker.connect")
            }
        }
    }

    /// A row was chosen: an earlier address's failure is not this home's (the sign-in screen
    /// would show it as an alert).
    private func clearProblems() {
        problem = nil
        session.connectProblem = nil
    }

    /// A typed address, or a pasted connect link (the person gave it here: no second ask).
    private func add() {
        problem = nil
        session.connectProblem = nil
        switch TVStart.typed(address) {
        case let .link(link):
            session.open(link)
        case let .addresses(candidates):
            Task { await session.connect(to: candidates) }
        case .invalid:
            problem = words.text(.notAnAddress)
        }
    }
}

/// A heading, its rows and a line under them.
private struct TVSection<Content: View>: View {
    let title: String
    let footer: String?
    @ViewBuilder let content: Content

    init(_ title: String, footer: String? = nil, @ViewBuilder content: () -> Content) {
        self.title = title
        self.footer = footer
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text(title)
                .font(Theme.body(29, weight: .semibold))
                .foregroundStyle(Theme.goldText)
            content
            if let footer {
                Text(footer)
                    .font(Theme.body(24))
                    .foregroundStyle(Theme.faint)
            }
        }
    }
}

private struct TVHomeRow: View {
    let name: String
    let address: String
    let systemImage: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 24) {
                Image(systemName: systemImage).font(.system(size: 40))
                VStack(alignment: .leading, spacing: 4) {
                    Text(name).font(Theme.heading(40))
                    Text(verbatim: address)
                        .font(Theme.body(26))
                        .opacity(0.75)
                        .environment(\.layoutDirection, .leftToRight)
                }
                Spacer()
            }
            .frame(maxWidth: .infinity)
        }
        .buttonStyle(.gold)
    }
}

/// A `maktaba://connect` link opened from outside the app: nothing is asked of its addresses
/// until the person says Connect here.
struct TVLinkPrompt: View {
    let session: AppSession
    let link: ConnectLink
    let words: ConnectStrings
    /// Cancel has the focus first: connecting is a choice the person makes.
    @FocusState private var cancelFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text(words.text(.linkTitle))
                .font(Theme.heading(40))
                .foregroundStyle(Theme.text)
            Text(words.text(.linkMessage, link.name ?? link.urls.first?.host() ?? "",
                            link.urls.map { Home(url: $0, name: "").displayAddress }.joined(separator: ", ")))
                .font(Theme.body(28))
                .foregroundStyle(Theme.muted)
                .fixedSize(horizontal: false, vertical: true)
            HStack(spacing: 32) {
                Button(words.text(.connect)) { session.confirmLink() }
                    .accessibilityIdentifier("link.connect")
                Button(words.text(.cancel)) { session.dismissLink() }
                    .focused($cancelFocused)
                    .accessibilityIdentifier("link.cancel")
            }
            .buttonStyle(.gold)
        }
        .onAppear { cancelFocused = true }
        .padding(32)
        .background(Theme.raised, in: .rect(cornerRadius: Theme.cornerRadius))
    }
}

/// Signing in on a TV, as the home allows: a code first (shown large, with a QR code of the
/// approve page; a person signed in on a phone or computer approves it and the TV is signed
/// in as them; a new code when one expires), or a username and password typed with the remote.
struct TVSignInView: View {
    @Bindable var session: AppSession
    @State private var method = TVStart.Method.code
    @State private var codeFlow: CodeSignIn?
    private let words = ConnectStrings.forPreferredLanguages()
    private let tv = TVConnectStrings.forPreferredLanguages()

    private var offered: [TVStart.Method] { TVStart.offered(session.signInMethods) }
    private var shown: TVStart.Method { offered.contains(method) ? method : offered[0] }

    var body: some View {
        ZStack {
            LibraryBackdrop()
            Group {
                if session.restoring {
                    // the saved sign-in is being checked: no code asked for meanwhile
                    ProgressView()
                } else if let f = session.restoreProblem {
                    restorePanel(f)
                } else {
                    switch shown {
                    case .code:
                        codePanel
                    case .password:
                        TVPasswordPanel(session: session, words: words, tv: tv,
                                        useCode: offered.contains(.code) ? { method = .code } : nil)
                    }
                }
            }
            .padding(80)
        }
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
        // one code at a time, asked for only while it shows
        .task(id: CodeFlowKey(home: session.current?.url, shown: codeShown)) {
            codeFlow = nil
            guard codeShown, let client = session.client else { return }
            let m = CodeSignIn(client: client, label: UIDevice.current.name) { [session] in
                try await session.signedInByCode()
            }
            codeFlow = m
            await m.run()
        }
    }

    private struct CodeFlowKey: Equatable {
        let home: URL?
        let shown: Bool
    }

    private var codeShown: Bool {
        shown == .code && !session.restoring && session.restoreProblem == nil
    }

    /// Still signed in, but the home did not answer the check: why, Try again, another home.
    private func restorePanel(_ f: HomeFailure) -> some View {
        VStack(alignment: .leading, spacing: 28) {
            SectionTitle(session.current?.name ?? "", size: 52)
            Text(ErrorStrings.forPreferredLanguages().text(.restoreFailed))
                .font(Theme.body(30))
                .foregroundStyle(Theme.muted)
            ErrorBanner(failure: f) { Task { await session.retryRestore() } }
        }
        .frame(maxWidth: 1200)
        .readingPanel(padding: 56)
        .accessibilityIdentifier("signin.restoreProblem")
    }

    private var codePanel: some View {
        HStack(alignment: .center, spacing: 64) {
            VStack(alignment: .leading, spacing: 28) {
                SectionTitle(tv.text(.codeTitle, session.current?.name ?? ""), size: 52)
                switch codeFlow?.status {
                case let .showing(p)?:
                    Text(words.text(.codeIntro, session.current?.name ?? ""))
                        .font(Theme.body(30))
                        .foregroundStyle(Theme.muted)
                        .fixedSize(horizontal: false, vertical: true)
                    Text(verbatim: p.code)
                        .font(.system(size: 110, weight: .bold, design: .monospaced))
                        .foregroundStyle(Theme.goldText)
                        .environment(\.layoutDirection, .leftToRight)
                        .accessibilityLabel(p.code.map(String.init).joined(separator: " "))
                        .accessibilityIdentifier("pairing-code")
                    if let url = verifyURL(p) {
                        VStack(alignment: .leading, spacing: 6) {
                            Text(words.text(.codeOrOpen)).foregroundStyle(Theme.muted)
                            Text(verbatim: url.absoluteString)
                                .font(Theme.body(30, weight: .semibold))
                                .foregroundStyle(Theme.text)
                                .environment(\.layoutDirection, .leftToRight)
                        }
                    }
                    HStack(spacing: 16) {
                        ProgressView()
                        Text(words.text(.waiting)).foregroundStyle(Theme.muted)
                    }
                    TimelineView(.periodic(from: .now, by: 1)) { ctx in
                        let left = max(0, Int(p.expiresAt.timeIntervalSince(ctx.date)))
                        Text(words.text(.codeChangesIn, String(format: "%d:%02d", left / 60, left % 60)))
                            .font(Theme.body(24).monospacedDigit())
                            .foregroundStyle(Theme.faint)
                    }
                case .unsupported?:
                    Text(words.text(.codeUnsupported))
                        .font(Theme.body(30))
                        .foregroundStyle(Theme.muted)
                        .accessibilityIdentifier("signin.code.unsupported")
                default:
                    ProgressView()
                }
                if let problem = codeFlow?.problem {
                    Text(problem).foregroundStyle(.red)
                }
                HStack(spacing: 32) {
                    if offered.contains(.password) || codeFlow?.status == .unsupported {
                        Button(words.text(.byPassword), systemImage: "keyboard") { method = .password }
                            .accessibilityIdentifier("signin.password")
                    }
                    Button(tv.text(.otherHome), systemImage: "house") { session.switchHome() }
                        .accessibilityIdentifier("signin.homes")
                }
                .buttonStyle(.gold)
                .padding(.top, 20)
            }
            .frame(maxWidth: 1050, alignment: .leading)
            if let p = codeFlow?.pairing, let url = verifyURL(p) {
                QRCodeView(text: url.absoluteString)
                    .frame(width: 440, height: 440)
                    .accessibilityLabel(words.text(.qrLabel))
                    .accessibilityHint(tv.text(.codeAlt))
            }
        }
        .readingPanel(padding: 56)
    }

    private func verifyURL(_ p: DevicePairing) -> URL? {
        session.current.flatMap { p.verifyURL(home: $0.url) }
    }
}

/// Username and password with the TV's keyboard.
private struct TVPasswordPanel: View {
    let session: AppSession
    let words: ConnectStrings
    let tv: TVConnectStrings
    /// Back to the code (nil: this home has none).
    let useCode: (() -> Void)?
    @State private var username = ""
    @State private var password = ""
    @State private var busy = false
    @State private var problem: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 28) {
            SectionTitle(tv.text(.codeTitle, session.current?.name ?? ""), size: 52)
            TextField(words.text(.username), text: $username)
                .textContentType(.username)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .accessibilityIdentifier("signin.username")
            SecureField(words.text(.password), text: $password)
                .textContentType(.password)
                .onSubmit(signIn)
                .accessibilityIdentifier("signin.password.field")
            if let problem {
                Text(problem).foregroundStyle(.red)
            }
            HStack(spacing: 32) {
                Button(action: signIn) {
                    if busy { ProgressView() } else { Text(words.text(.signIn)) }
                }
                .disabled(username.isEmpty || password.isEmpty || busy)
                .accessibilityIdentifier("signin.submit")
                if let useCode {
                    Button(tv.text(.withCode), systemImage: "qrcode", action: useCode)
                        .accessibilityIdentifier("signin.code")
                }
                Button(tv.text(.otherHome), systemImage: "house") { session.switchHome() }
                    .accessibilityIdentifier("signin.homes")
            }
            .buttonStyle(.gold)
        }
        .frame(maxWidth: 1200)
        .readingPanel(padding: 56)
    }

    private func signIn() {
        busy = true
        problem = nil
        Task {
            defer { busy = false }
            do {
                try await session.signIn(username: username, password: password)
            } catch {
                Log.failure("sign in", error, logger: Log.session)
                problem = Log.isCancellation(error) ? nil : words.signInProblem(error)
            }
        }
    }
}
