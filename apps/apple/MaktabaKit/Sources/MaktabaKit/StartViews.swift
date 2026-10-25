import SwiftUI

/// What every app opens with: choose a home, sign in, then the app's own content.
public struct SessionGate<Content: View>: View {
    @Bindable var session: AppSession
    let title: String
    let content: (User, APIClient) -> Content

    public init(session: AppSession, title: String, @ViewBuilder content: @escaping (User, APIClient) -> Content) {
        self.session = session
        self.title = title
        self.content = content
    }

    public var body: some View {
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
}

/// The homes this app knows, and a new one by its address. (Finding homes on the network
/// and through Maktaba Cloud come next: Epic 42.3 and 42.7.)
public struct HomePickerView: View {
    @Bindable var session: AppSession
    let title: String
    @State private var address = ""
    @State private var problem: String?

    public init(session: AppSession, title: String) {
        self.session = session
        self.title = title
    }

    public var body: some View {
        NavigationStack {
            Form {
                if !session.homes.isEmpty {
                    Section("Your homes") {
                        ForEach(session.homes) { home in
                            Button {
                                session.use(home)
                            } label: {
                                LabeledContent(home.name, value: home.url.host() ?? "")
                            }
                        }
                        .onDelete { idx in idx.map { session.homes[$0] }.forEach(session.forget) }
                    }
                }
                Section {
                    TextField("Address, like 192.168.1.20:8088", text: $address)
                        .textContentType(.URL)
                        .autocorrectionDisabled()
                        #if os(iOS)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        #endif
                        .onSubmit(add)
                    Button("Connect", action: add)
                        .disabled(address.isEmpty)
                } header: {
                    Text("Add a home")
                } footer: {
                    if let problem { Text(problem).foregroundStyle(.red) }
                }
            }
            .navigationTitle(title)
        }
    }

    private func add() {
        guard let url = Home.normalize(address) else {
            problem = "That is not a home's address."
            return
        }
        problem = nil
        session.use(Home(url: url, name: url.host() ?? url.absoluteString))
    }
}

public struct SignInView: View {
    @Bindable var session: AppSession
    @State private var username = ""
    @State private var password = ""
    @State private var busy = false
    @State private var problem: String?

    public init(session: AppSession) {
        self.session = session
    }

    public var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Username", text: $username)
                        .textContentType(.username)
                        .autocorrectionDisabled()
                        #if os(iOS)
                        .textInputAutocapitalization(.never)
                        #endif
                    SecureField("Password", text: $password)
                        .textContentType(.password)
                        .onSubmit(signIn)
                } header: {
                    Text(session.current?.name ?? "")
                } footer: {
                    if let problem { Text(problem).foregroundStyle(.red) }
                }
                Button(action: signIn) {
                    if busy { ProgressView() } else { Text("Sign in") }
                }
                .disabled(username.isEmpty || password.isEmpty || busy)
            }
            .navigationTitle("Sign in")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Homes", systemImage: "house") { session.switchHome() }
                }
            }
        }
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
