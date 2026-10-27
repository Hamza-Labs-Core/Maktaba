import MaktabaKit
import SwiftUI

/// The admin's screens: tabs on iPhone, a sidebar on iPad (and tabs again when the iPad's
/// window is narrow), each a NavigationStack of its own.
struct RemoteRoot: View {
    let session: AppSession
    let user: User
    @State private var model: RemoteModel
    @SceneStorage("remote.tab") private var tab = RemoteTab.attention

    init(session: AppSession, user: User, client: APIClient) {
        self.session = session
        self.user = user
        _model = State(initialValue: RemoteModel(session: session, client: client))
    }

    var body: some View {
        TabView(selection: $tab) {
            Tab("Needs attention", systemImage: "exclamationmark.triangle", value: .attention) {
                screen { AttentionView() }
            }
            .badge(model.attentionCount)
            Tab("Queue", systemImage: "list.bullet.rectangle", value: .queue) {
                screen { QueueView() }
            }
            Tab("Libraries", systemImage: "books.vertical", value: .libraries) {
                screen { LibrariesView() }
            }
            Tab("Machines", systemImage: "desktopcomputer", value: .machines) {
                screen { MachinesView() }
            }
            .badge(model.asking.count)
            Tab("Home", systemImage: "house", value: .home) {
                screen { HomeStatusView(user: user) }
            }
        }
        .tabViewStyle(.sidebarAdaptable)
        .environment(model)
        .task { await model.poll() }
    }

    private func screen(@ViewBuilder _ content: () -> some View) -> some View {
        NavigationStack { content() }
    }
}

enum RemoteTab: String, Hashable {
    case attention, queue, libraries, machines, home
}

/// The account menu every screen's toolbar carries: who, which home, the theme, switching
/// home, signing out.
struct AccountMenu: ToolbarContent {
    @Environment(RemoteModel.self) private var model
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.system

    var body: some ToolbarContent {
        ToolbarItem(placement: .secondaryAction) {
            Menu("Account", systemImage: "person.crop.circle") {
                if let home = model.session.current { Text(home.name) }
                // a TV, phone or computer showing a sign-in code (Epic 42 story 42.6)
                Button(ConnectStrings.forPreferredLanguages().text(.approveMenu), systemImage: "checkmark.shield") {
                    model.session.approvingSignIn = true
                }
                .accessibilityIdentifier("account.approve")
                ThemePicker(mode: $theme)
                Button("Switch home", systemImage: "house") { model.session.switchHome() }
                Button("Sign out", systemImage: "rectangle.portrait.and.arrow.right") { model.session.signOut() }
            }
        }
    }
}

/// The last read or action that did not work, at the top of a list.
struct ProblemSection: View {
    @Environment(RemoteModel.self) private var model

    var body: some View {
        if let problem = model.problem {
            Section {
                Label(problem, systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.red)
            }
        }
    }
}

/// A button whose action runs on the home: a spinner while it does.
struct BusyButton: View {
    let title: String
    let systemImage: String
    let busy: Bool
    var role: ButtonRole?
    let action: () async -> Void

    var body: some View {
        Button(role: role) {
            Task { await action() }
        } label: {
            if busy {
                ProgressView()
            } else {
                Label(title, systemImage: systemImage)
            }
        }
        .disabled(busy)
    }
}
