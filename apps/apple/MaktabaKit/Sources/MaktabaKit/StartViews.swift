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
    }

    public var body: some View {
        Group {
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
        .sheet(isPresented: $session.approvingSignIn) {
            if case let .signedIn(user) = session.phase, let client = session.client {
                ApproveSignInView(client: client, user: user, homeName: session.current?.name ?? "")
                    #if os(macOS)
                    .frame(minWidth: 460, minHeight: 420)
                    #endif
            }
        }
        // a link opened while a home is in use: the picker shows its own problems
        .alert(words.text(.connect), isPresented: Binding(
            get: { session.connectProblem != nil && session.phase != .choosingHome },
            set: { if !$0 { session.connectProblem = nil } })
        ) {
            Button(words.text(.done)) { session.connectProblem = nil }
        } message: {
            Text(session.connectProblem.map(words.reach) ?? "")
        }
    }
}

/// The home picker (Epic 42 story 42.2): the homes on this network (Bonjour), the recent
/// ones (rename, remove), a home by its address or connect link, and (iPhone, iPad) a home's
/// QR code. A typed or scanned address is asked whether it is a Maktaba home first.
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
                if session.connecting {
                    Section {
                        HStack(spacing: 10) {
                            ProgressView()
                            Text(words.text(.connecting))
                        }
                        .accessibilityIdentifier("picker.connecting")
                    }
                }
                if let shown = problem ?? session.connectProblem.map(words.reach) {
                    Section {
                        Label(shown, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                            .accessibilityIdentifier("picker.problem")
                    }
                }
                nearby
                recent
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
            .formStyle(.grouped)
            .navigationTitle(title)
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

    // MARK: sections

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
                HStack(spacing: 10) {
                    ProgressView()
                    Text(words.text(.searching)).foregroundStyle(.secondary)
                }
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
    private let words = ConnectStrings.forPreferredLanguages()

    enum Method: Hashable { case password, code }

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

    public var body: some View {
        NavigationStack {
            Form {
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
                switch shown {
                case .password:
                    passwordForm
                case .code:
                    CodeSignInSection(session: session, words: words) { method = .password }
                }
            }
            .formStyle(.grouped)
            .navigationTitle(words.text(.signInTitle))
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(words.text(.homes), systemImage: "house") { session.switchHome() }
                        .accessibilityIdentifier("signin.homes")
                }
            }
        }
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
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
            if let problem { Text(problem).foregroundStyle(.red) }
        }
        Button(action: signIn) {
            if busy { ProgressView() } else { Text(words.text(.signIn)) }
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
                problem = error.localizedDescription
            }
        }
    }
}

/// The code this device shows, large, with a QR code of the approve page, until a signed-in
/// device approves it; a new code when it expires.
struct CodeSignInSection: View {
    let session: AppSession
    let words: ConnectStrings
    let usePassword: () -> Void
    @State private var model: CodeSignIn?

    var body: some View {
        Group {
            switch model?.status {
            case nil, .starting?, .approved?:
                Section { HStack { Spacer(); ProgressView(); Spacer() } }
            case .unsupported?:
                Section {
                    Text(words.text(.codeUnsupported))
                        .accessibilityIdentifier("signin.code.unsupported")
                    Button(words.text(.byPassword), action: usePassword)
                }
            case let .showing(p)?:
                showing(p)
            }
            if let problem = model?.problem {
                Section { Text(problem).foregroundStyle(.red) }
            }
        }
        .task(id: session.current?.url) {
            guard let client = session.client else { return }
            let m = CodeSignIn(client: client, label: Self.deviceLabel) { [session] in
                try await session.signedInByCode()
            }
            model = m
            await m.run()
        }
    }

    private func showing(_ p: DevicePairing) -> some View {
        Section {
            VStack(spacing: 16) {
                Text(words.text(.codeIntro, session.current?.name ?? ""))
                    .frame(maxWidth: .infinity, alignment: .leading)
                Text(verbatim: p.code)
                    .font(.system(size: 44, weight: .bold, design: .monospaced))
                    .foregroundStyle(Color.brass)
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
                HStack(spacing: 8) {
                    ProgressView()
                    Text(words.text(.waiting))
                }
                .foregroundStyle(.secondary)
                TimelineView(.periodic(from: .now, by: 1)) { ctx in
                    let left = max(0, Int(p.expiresAt.timeIntervalSince(ctx.date)))
                    Text(words.text(.codeChangesIn, String(format: "%d:%02d", left / 60, left % 60)))
                        .font(.footnote.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 8)
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
            Rectangle().fill(.quaternary)
            if let image {
                image.resizable().scaledToFill()
            } else {
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
