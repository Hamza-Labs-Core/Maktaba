import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// What every app opens with: choose a home, sign in, then the app's own content. It also
/// takes a home's connect link (`maktaba://connect?…`) whenever one is opened, and shows
/// "Approve a sign-in" when an account menu asks for it.
public struct SessionGate<Content: View>: View {
    @Bindable var session: AppSession
    let title: String
    let content: (User, APIClient) -> Content
    private let words = ConnectStrings.forPreferredLanguages()

    public init(session: AppSession, title: String, @ViewBuilder content: @escaping (User, APIClient) -> Content) {
        self.session = session
        self.title = title
        self.content = content
        // the brand's fonts, from Bundle.module, before the first start screen draws
        Brand.registerFonts()
    }

    public var body: some View {
        // a container (not a Group, which would hand these modifiers to the screen, whose own
        // alerts would then hide them)
        ZStack {
            switch session.phase {
            case .choosingHome:
                HomePickerView(session: session, title: title)
            case .signingIn:
                SignInView(session: session)
            case let .signedIn(user):
                if let client = session.client {
                    content(user, client)
                }
            }
        }
        .onOpenURL { session.open($0) }
        #if os(macOS)
        // a link opened on the Mac goes to the window already open, not a new one
        .handlesExternalEvents(preferring: ["*"], allowing: ["*"])
        #endif
        .sheet(isPresented: $session.approvingSignIn) {
            if case let .signedIn(user) = session.phase, let client = session.client {
                ApproveSignInView(client: client, user: user, homeName: session.current?.name ?? "")
                    #if os(macOS)
                    .frame(minWidth: 460, minHeight: 420)
                    #endif
            }
        }
        .sheet(isPresented: $session.choosingThroughCloud) {
            CloudSheet(session: session, words: words)
                #if os(macOS)
                .frame(minWidth: 480, minHeight: 460)
                #endif
        }
        // away from the picker (which shows both itself): a link from outside the app, which
        // the person says yes to before anything is asked, or why the one they said yes to
        // did not lead to a home. One alert for both.
        .alert(alertTitle, isPresented: Binding(
            get: { session.phase != .choosingHome && (session.pendingLink != nil || session.connectProblem != nil) },
            set: { if !$0 { session.dismissLink(); session.connectProblem = nil } })
        ) {
            if session.pendingLink != nil {
                Button(words.text(.connect)) { session.confirmLink() }
                Button(words.text(.cancel), role: .cancel) { session.dismissLink() }
            } else {
                Button(words.text(.done)) { session.connectProblem = nil }
            }
        } message: {
            if let link = session.pendingLink {
                Text(LinkPrompt.message(link, words))
            } else {
                Text(session.connectProblem.map(words.reach) ?? "")
            }
        }
    }

    private var alertTitle: String {
        words.text(session.pendingLink != nil ? .linkTitle : .connect)
    }
}

/// Asking before a connect link opened from outside the app is followed.
struct LinkPrompt: View {
    let session: AppSession
    let link: ConnectLink
    let words: ConnectStrings

    static func message(_ link: ConnectLink, _ words: ConnectStrings) -> String {
        words.text(.linkMessage, link.name ?? link.urls.first?.host() ?? "",
                   link.urls.map { Home(url: $0, name: "").displayAddress }.joined(separator: ", "))
    }

    var body: some View {
        Section {
            Text(Self.message(link, words))
            Button(words.text(.connect)) { session.confirmLink() }
                .bold()
                .accessibilityIdentifier("link.connect")
            Button(words.text(.cancel), role: .cancel) { session.dismissLink() }
                .accessibilityIdentifier("link.cancel")
        } header: {
            Text(words.text(.linkTitle))
        }
    }
}

/// The home picker (Epic 42 story 42.2): the homes on this network (Bonjour), the recent
/// ones (rename, remove), a home through Maktaba Cloud (story 42.7), a home by its address or
/// connect link, and (iPhone, iPad) a home's QR code. A typed or scanned address is asked whether it is a Maktaba home first.
public struct HomePickerView: View {
    @Bindable var session: AppSession
    let title: String
    @State private var browser = HomeBrowser()
    @State private var address = ""
    @State private var problem: String?
    @State private var scanning = false
    @State private var renaming: Home?
    @State private var newName = ""
    private let words = ConnectStrings.forPreferredLanguages()

    public init(session: AppSession, title: String) {
        self.session = session
        self.title = title
    }

    public var body: some View {
        NavigationStack {
            Form {
                StartBrandSection(appName: appName, tagline: MaktabaApp.current?.tagline())
                Group {
                    pickerSections
                }
                .startPanelRows()
            }
            .formStyle(.grouped)
            .startScreen()
            .navigationTitle(appName)
            .startTitleHidden()
            .disabled(session.connecting)
        }
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
        .onAppear { browser.start() }
        .onDisappear { browser.stop() }
        #if os(iOS)
        .sheet(isPresented: $scanning) {
            QRScannerSheet(words: words, found: scanned)
        }
        #endif
        .alert(words.text(.renameTitle), isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
            TextField(renaming?.name ?? "", text: $newName)
            Button(words.text(.save)) {
                if let renaming { session.rename(renaming, to: newName) }
                renaming = nil
            }
            Button(words.text(.cancel), role: .cancel) { renaming = nil }
        } message: {
            Text(words.text(.renameHint))
        }
    }

    /// The running app's name under «Maktaba» (the title the app gave, in a test host).
    private var appName: String { MaktabaApp.current?.name() ?? title }

    // MARK: sections

    @ViewBuilder private var pickerSections: some View {
        if let link = session.pendingLink {
            LinkPrompt(session: session, link: link, words: words)
        }
        if let progress = session.progress, problem == nil {
            Section {
                // its own identifier says the step (connect.progress.trying, …failed)
                ConnectProgressView(progress, locale: words.locale, retryID: "picker.retry") {
                    Task { await session.retryConnect() }
                }
            }
        } else if let shown = problem ?? session.connectProblem.map(words.reach) {
            Section {
                Label(shown, systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.red)
                    .accessibilityIdentifier("picker.problem")
            }
        }
        nearby
        recent
        throughCloud
        byAddress
        #if os(iOS)
        Section {
            Button(words.text(.scanQR), systemImage: "qrcode.viewfinder") { scanning = true }
                .accessibilityIdentifier("picker.scan")
        } footer: {
            Text(words.text(.scanHint))
        }
        #endif
    }

    private var nearby: some View {
        Section {
            ForEach(browser.found) { home in
                Button {
                    problem = nil
                    session.use(Home(url: home.url, name: home.name, homeID: home.homeID))
                } label: {
                    row(name: home.name, address: Home(url: home.url, name: home.name).displayAddress, symbol: "wifi")
                }
                .accessibilityIdentifier("picker.nearby.\(home.id)")
            }
            if browser.found.isEmpty {
                ConnectBusyLabel(words.text(.searching)).foregroundStyle(.secondary)
            }
        } header: {
            Text(words.text(.onThisNetwork))
        } footer: {
            Text(words.text(.onThisNetworkHint))
        }
    }

    @ViewBuilder private var recent: some View {
        if !session.homes.isEmpty {
            Section(words.text(.recentHomes)) {
                ForEach(session.recentHomes) { home in
                    Button {
                        problem = nil
                        session.open(home)
                    } label: {
                        row(name: home.name, address: home.displayAddress, symbol: "house")
                    }
                    .accessibilityIdentifier("picker.recent.\(home.displayAddress)")
                    .contextMenu {
                        Button(words.text(.rename), systemImage: "pencil") { startRenaming(home) }
                        Button(words.text(.remove), systemImage: "trash", role: .destructive) { session.forget(home) }
                    }
                    #if os(iOS)
                    .swipeActions {
                        Button(words.text(.remove), systemImage: "trash", role: .destructive) { session.forget(home) }
                        Button(words.text(.rename), systemImage: "pencil") { startRenaming(home) }
                    }
                    #endif
                }
            }
        }
    }

    /// Story 42.7: a home reached through the person's Maktaba Cloud account.
    private var throughCloud: some View {
        Section {
            Button(words.text(.throughCloud), systemImage: "cloud") {
                problem = nil
                session.connectProblem = nil
                session.choosingThroughCloud = true
            }
            .accessibilityIdentifier("picker.cloud")
        } footer: {
            Text(words.text(.throughCloudHint))
        }
    }

    private var byAddress: some View {
        Section {
            TextField(words.text(.addressPlaceholder), text: $address)
                .textContentType(.URL)
                .autocorrectionDisabled()
                #if os(iOS)
                .keyboardType(.URL)
                .textInputAutocapitalization(.never)
                #endif
                .environment(\.layoutDirection, .leftToRight)
                .onSubmit(add)
                .accessibilityIdentifier("picker.address")
            Button(words.text(.connect), action: add)
                .disabled(address.trimmingCharacters(in: .whitespaces).isEmpty)
                .accessibilityIdentifier("picker.connect")
        } header: {
            Text(words.text(.byAddress))
        } footer: {
            Text(words.text(.addressHint))
        }
    }

    private func row(name: String, address: String, symbol: String) -> some View {
        HStack {
            Label(name, systemImage: symbol)
                .foregroundStyle(Color.primary)
            Spacer()
            Text(verbatim: address)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .environment(\.layoutDirection, .leftToRight)
        }
    }

    // MARK: actions

    private func startRenaming(_ home: Home) {
        newName = home.renamed == true ? home.name : ""
        renaming = home
    }

    /// A typed address (or a pasted connect link: the Mac's way to "scan").
    private func add() {
        problem = nil
        session.connectProblem = nil
        if let link = ConnectLink(string: address) {
            session.open(link)
            return
        }
        let candidates = Home.candidates(for: address)
        guard !candidates.isEmpty else {
            problem = words.text(.notAnAddress)
            return
        }
        Task { await session.connect(to: candidates) }
    }

    private func scanned(_ text: String) {
        problem = nil
        session.connectProblem = nil
        switch ScannedCode.parse(text) {
        case let .connect(link):
            session.open(link)
        case .address:
            // a QR code with a plain address: as typed (its path first)
            Task { await session.connect(to: Home.candidates(for: text)) }
        case .signInCode, nil:
            problem = words.text(.notAHomeCode)
        }
    }
}

/// Signing in to the chosen home: a username and password, or a code another device
/// approves (Epic 42 story 42.6), as the home allows.
public struct SignInView: View {
    @Bindable var session: AppSession
    @State private var method = Method.password
    @State private var username = ""
    @State private var password = ""
    @State private var busy = false
    @State private var problem: String?
    @State private var codeFlow: CodeSignIn?
    /// A Retry on the code's progress: a new flow, at the next attempt.
    @State private var codeRound = 0
    private let words = ConnectStrings.forPreferredLanguages()

    enum Method: Hashable { case password, code }

    struct CodeFlowKey: Equatable {
        let home: URL?
        let shown: Bool
        var round = 0
    }

    public init(session: AppSession) {
        self.session = session
    }

    /// The ways this home allows (both when it has not said).
    private var offered: [Method] {
        guard let m = session.signInMethods else { return [.password, .code] }
        let list = (m.password ? [Method.password] : []) + (m.code ? [Method.code] : [])
        return list.isEmpty ? [.password] : list
    }

    private var shown: Method { offered.contains(method) ? method : offered[0] }

    /// A code is asked for only while it shows, and not while the saved sign-in is checked.
    private var codeShown: Bool {
        shown == .code && !session.restoring && session.restoreProblem == nil
    }

    public var body: some View {
        NavigationStack {
            Form {
                StartBrandSection(appName: MaktabaApp.current?.name())
                Group {
                    signInSections
                }
                .startPanelRows()
            }
            .formStyle(.grouped)
            .startScreen()
            .navigationTitle(words.text(.signInTitle))
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(words.text(.homes), systemImage: "house") { session.switchHome() }
                        .accessibilityIdentifier("signin.homes")
                }
            }
        }
        // one code at a time, asked for only while it shows (here, not on the Form's rows,
        // which come and go as the code's state changes)
        .task(id: CodeFlowKey(home: session.current?.url, shown: codeShown, round: codeRound)) {
            let next = (codeFlow?.progress?.attempt ?? 0) + 1
            codeFlow = nil
            guard codeShown, let client = session.client else { return }
            let m = CodeSignIn(client: client, label: CodeSignInSection.deviceLabel, homeName: session.current?.name ?? "",
                               attempt: codeRound == 0 ? 1 : next) { [session] in
                try await session.signedInByCode()
            }
            codeFlow = m
            await m.run()
        }
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
    }

    @ViewBuilder private var signInSections: some View {
        Section {
            if offered.count > 1 {
                Picker(words.text(.signInTitle), selection: $method) {
                    Text(words.text(.byPassword)).tag(Method.password)
                    Text(words.text(.byCode)).tag(Method.code)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .accessibilityIdentifier("signin.method")
            }
        } header: {
            Text(session.current?.name ?? "")
        }
        RestoreSection(session: session)
        switch shown {
        case .password:
            passwordForm
        case .code:
            CodeSignInSection(session: session, words: words, model: codeFlow, retry: { codeRound += 1 }) { method = .password }
        }
    }

    @ViewBuilder private var passwordForm: some View {
        Section {
            TextField(words.text(.username), text: $username)
                .textContentType(.username)
                .autocorrectionDisabled()
                #if os(iOS)
                .textInputAutocapitalization(.never)
                #endif
                .accessibilityIdentifier("signin.username")
            SecureField(words.text(.password), text: $password)
                .textContentType(.password)
                .onSubmit(signIn)
                .accessibilityIdentifier("signin.password")
        } footer: {
            // why the last try did not work (the form is its retry)
            if !busy, problem != nil, let p = session.progress, !p.isWorking {
                ConnectProgressView(p, locale: words.locale)
                    .accessibilityIdentifier("signin.problem")
            } else if let problem {
                Text(problem).foregroundStyle(.red)
            }
        }
        Button(action: signIn) {
            if busy, let p = session.progress, p.isWorking {
                ConnectBusyLabel(p.headline(words.locale))
            } else {
                Text(words.text(.signIn))
            }
        }
        .disabled(username.isEmpty || password.isEmpty || busy)
        .accessibilityIdentifier("signin.submit")
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

/// The saved sign-in being checked ("Reconnecting to <home>…", the home busy with the
/// countdown to its next try), or why it could not be: the home did not answer, so the
/// person is still signed in and can try again (or sign in afresh below).
struct RestoreSection: View {
    let session: AppSession

    var body: some View {
        let locale = ConnectStrings.forPreferredLanguages().locale
        if session.restoring {
            Section {
                ConnectProgressView(session.progress ?? ConnectProgress(.reconnecting(name: session.current?.name ?? "")),
                                    locale: locale)
            }
            .accessibilityIdentifier("signin.restoring")
        } else if session.restoreProblem != nil {
            let words = ErrorStrings.forPreferredLanguages()
            Section {
                Text(words.text(.restoreFailed))
                if let p = session.progress, !p.isWorking {
                    ConnectProgressView(p, locale: locale, retryID: "signin.restore.retry") {
                        Task { await session.retryRestore() }
                    }
                }
            }
            .accessibilityIdentifier("signin.restoreProblem")
        }
    }
}

/// The code this device shows, large, with a QR code of the approve page, until a signed-in
/// device approves it; a new code when it expires.
struct CodeSignInSection: View {
    let session: AppSession
    let words: ConnectStrings
    let model: CodeSignIn?
    /// Retry after a failure: a new code flow at the next attempt.
    let retry: () -> Void
    let usePassword: () -> Void

    /// What the flow is doing (asking for a code before it has said).
    private var progress: ConnectProgress {
        model?.progress ?? ConnectProgress(.signingIn(home: session.current?.name ?? ""))
    }

    var body: some View {
        Group {
            switch model?.status {
            case nil, .starting?, .approved?:
                Section {
                    ConnectProgressView(progress, locale: words.locale, retryID: "signin.code.retry", retry: retry)
                }
            case .unsupported?:
                Section {
                    Text(words.text(.codeUnsupported))
                        .accessibilityIdentifier("signin.code.unsupported")
                    Button(words.text(.byPassword), action: usePassword)
                }
            case let .showing(p)?:
                showing(p)
            }
            // a blip while the code shows (the progress says why a code could not be had)
            if case .showing? = model?.status, let problem = model?.problem {
                Section { Text(problem).foregroundStyle(.red) }
            }
        }
    }

    private func showing(_ p: DevicePairing) -> some View {
        Section {
            VStack(spacing: 16) {
                Text(words.text(.codeIntro, session.current?.name ?? ""))
                    .frame(maxWidth: .infinity, alignment: .leading)
                Text(verbatim: p.code)
                    .font(.system(size: 44, weight: .bold, design: .monospaced))
                    .foregroundStyle(Color.mktBrassText)
                    .environment(\.layoutDirection, .leftToRight)
                    #if !os(tvOS)
                    .textSelection(.enabled)
                    #endif
                    .accessibilityLabel(p.code.map(String.init).joined(separator: " "))
                    .accessibilityIdentifier("signin.code")
                if let url = session.current.flatMap({ p.verifyURL(home: $0.url) }) {
                    QRCodeView(text: url.absoluteString)
                        .frame(width: 200, height: 200)
                        .accessibilityLabel(words.text(.qrLabel))
                    VStack(spacing: 4) {
                        Text(words.text(.codeOrOpen)).font(.footnote).foregroundStyle(.secondary)
                        Text(verbatim: url.absoluteString)
                            .font(.footnote.monospaced())
                            .environment(\.layoutDirection, .leftToRight)
                            #if !os(tvOS)
                            .textSelection(.enabled)
                            #endif
                    }
                }
                // waiting for approval, the code and the time it has left, the attempt
                ConnectProgressView(model?.progress ?? ConnectProgress(.awaitingApproval(code: p.code, expiresAt: p.expiresAt)),
                                    locale: words.locale)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 8)
            .accessibilityElement(children: .contain)
        }
    }

    /// This device's name, shown to the person who approves it.
    @MainActor static var deviceLabel: String {
        #if os(macOS)
        Host.current().localizedName ?? "Mac"
        #elseif canImport(UIKit)
        UIDevice.current.name
        #else
        ""
        #endif
    }
}

/// A picture the home serves behind sign-in (a video's poster): loaded with the token.
public struct AuthorizedImage: View {
    let client: APIClient
    let video: String
    @State private var image: Image?

    public init(client: APIClient, video: String) {
        self.client = client
        self.video = video
    }

    public var body: some View {
        ZStack {
            if let image {
                // the whole frame, as a player shows the video: a vertical or square one
                // has black bars at its sides, an ultra-wide one above and below
                Rectangle().fill(.black)
                image.resizable().scaledToFit()
            } else {
                Rectangle().fill(.quaternary)
                Image(systemName: "film").foregroundStyle(.secondary)
            }
        }
        .aspectRatio(16 / 9, contentMode: .fit)
        .clipped()
        .task(id: video) {
            // another video's picture must not stay up while (or when) this one does not load
            image = nil
            guard let data = try? await client.poster(video: video) else { return }
            #if canImport(UIKit)
            if let ui = UIImage(data: data) { image = Image(uiImage: ui) }
            #elseif canImport(AppKit)
            if let ns = NSImage(data: data) { image = Image(nsImage: ns) }
            #endif
        }
    }
}
