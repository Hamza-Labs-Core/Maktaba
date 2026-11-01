import MaktabaKit
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// Profile and Settings (Epic 48, FR-25 to FR-31): who is signed in and to which home; their
/// watch history, favorites and playlists; the account (sessions, approving a sign-in, the
/// linked cloud account, switching home, signing out); this device's settings (theme,
/// language, playback); help and about. Its pages show in this screen's place, with Back to
/// the first; a video opened from one pushes on the section's stack as from any screen.
struct ProfileScreen: View {
    let context: PlayerContext

    @State private var model: ProfileModel
    @AppStorage(ThemeMode.storageKey) private var theme = ThemeMode.dark
    @State private var confirmSignOut = false
    /// The page shown in the first screen's place (nil: the first screen). The pages are not
    /// pushed: the section's stack holds `PlayerRoute`s only, and a view pushed beside them
    /// would be put under a video opened from it.
    @State private var page: ProfilePage?
    #if DEBUG
    /// The last link opened, in a Debug launch with `-maktaba-record-opens` (the UI tests
    /// check the address instead of leaving the app).
    @State private var opened: URL?
    #endif

    init(context: PlayerContext) {
        self.context = context
        let session = context.session
        _model = State(initialValue: ProfileModel(client: context.client) { session.signedOut() })
    }

    private var homeName: String { context.session.current?.name ?? context.client.home.host() ?? "" }

    var body: some View {
        Group {
            if let page {
                pageView(page)
                    .navigationBarBackButtonHidden(true)
                    .toolbar {
                        ToolbarItem(placement: .navigation) {
                            Button {
                                self.page = page.parent
                            } label: {
                                Label { Text(page.parentTitle) } icon: { Image(systemName: "chevron.backward") }
                                    .labelStyle(.titleAndIcon)
                            }
                            .accessibilityIdentifier("profile.back")
                        }
                    }
                    .profileProblems(model)
            } else {
                root
            }
        }
        #if DEBUG
        .overlay(alignment: .bottom) {
            if let opened {
                Text(verbatim: opened.absoluteString)
                    .font(.caption2)
                    .padding(4)
                    .background(.regularMaterial)
                    .accessibilityIdentifier("profile.opened")
            }
        }
        #endif
        .onAppear { PlaybackSettings.resetIfAsked() }
        .task { await model.loadRoot() }
    }

    /// One of the Profile's pages, shown in the first screen's place.
    @ViewBuilder private func pageView(_ page: ProfilePage) -> some View {
        switch page {
        case .history: HistoryPage(model: model)
        case .favorites: FavoritesPage(model: model)
        case .playlists: PlaylistsPage(model: model) { self.page = .playlist($0) }
        case let .playlist(p): PlaylistPage(model: model, summary: p)
        case .sessions: SessionsPage(model: model)
        case .playback: PlaybackPage()
        case .diagnostics: DiagnosticsPage()
        }
    }

    private var root: some View {
        List {
            header
            Section {
                pageRow(.history, Text("Watch history", tableName: "Profile", comment: "The watch history page"), "clock.arrow.circlepath")
                pageRow(.favorites, Text("Favorites", tableName: "Profile", comment: "The favorites page"), "heart")
                pageRow(.playlists, Text("Playlists", tableName: "Profile", comment: "The playlists page"), "music.note.list")
            } header: {
                sectionTitle(Text("Your library", tableName: "Profile", comment: "A Profile group: history, favorites, playlists"))
            }

            Section {
                pageRow(.sessions, Text("Sessions", tableName: "Profile", comment: "The signed-in browsers page"), "laptopcomputer.and.iphone")
                Button {
                    context.session.approvingSignIn = true
                } label: {
                    Label { Text(verbatim: ConnectStrings.forPreferredLanguages().text(.approveMenu)) } icon: {
                        Image(systemName: "checkmark.shield")
                    }
                }
                .accessibilityIdentifier("profile.approve")
                if let cloud = model.cloud, cloud.available {
                    cloudRow(cloud)
                }
                Button {
                    context.session.switchHome()
                } label: {
                    row(Text("Switch home", tableName: "Profile", comment: "Goes back to choosing a home"), "house")
                }
                .accessibilityIdentifier("profile.switchHome")
                // red as a destructive row, without the role: the role's text did not follow
                // Dynamic Type in the accessibility audit
                Button {
                    confirmSignOut = true
                } label: {
                    row(Text("Sign out", tableName: "Profile", comment: "Signs out of this home"), "rectangle.portrait.and.arrow.right")
                }
                .foregroundStyle(Color.red)
                .accessibilityIdentifier("profile.signOut")
            } header: {
                sectionTitle(Text("Account", tableName: "Profile", comment: "A Profile group: sessions, switching home, signing out"))
            }

            Section {
                Picker(selection: $theme) {
                    ForEach([ThemeMode.dark, .light, .system]) { mode in
                        Text(verbatim: mode.label).tag(mode)
                    }
                } label: {
                    Text("Theme", tableName: "Profile", comment: "The theme setting: dark, light or the system's")
                }
                .pickerStyle(.segmented)
                .accessibilityIdentifier("profile.theme")
                Button {
                    open(ProfileLinks.languageSettings)
                } label: {
                    LabeledContent {
                        Text(verbatim: ProfileLinks.currentLanguageName)
                    } label: {
                        Label {
                            Text("Language", tableName: "Profile", comment: "The app's language; opens the system's page for it")
                        } icon: {
                            Image(systemName: "globe")
                        }
                    }
                }
                .accessibilityIdentifier("profile.language")
                .accessibilityHint(Text("Opens the system's settings for this app", tableName: "Profile",
                                        comment: "What the Language row does, spoken"))
                pageRow(.playback, Text("Playback", tableName: "Profile", comment: "The playback settings page"), "play.rectangle")
            } header: {
                sectionTitle(Text("Settings on this device", tableName: "Profile", comment: "A Profile group: theme, language, playback"))
            }

            Section {
                Button {
                    open(ProfileLinks.help(home: context.client.home))
                } label: {
                    row(Text("Help", tableName: "Profile", comment: "Opens the home's help page for this app"), "questionmark.circle")
                }
                .accessibilityIdentifier("profile.help")
                about
                pageRow(.diagnostics, Text("Diagnostics", tableName: "Profile", comment: "The crash reports kept on this device"), "stethoscope")
            } header: {
                sectionTitle(Text("Help and about", tableName: "Profile", comment: "A Profile group: help, versions, diagnostics"))
            }
        }
        .maktabaSurface()
        .accessibilityIdentifier("screen.profile")
        .navigationTitle(Text(PlayerSection.profile.title))
        .confirmationDialog(Text("Sign out of \(homeName)?", tableName: "Profile", comment: "Asks before signing out; the home's name"),
                            isPresented: $confirmSignOut, titleVisibility: .visible) {
            Button(role: .destructive) {
                context.session.signOut()
            } label: {
                Text("Sign out", tableName: "Profile", comment: "Signs out of this home")
            }
            .accessibilityIdentifier("profile.signOut.confirm")
        } message: {
            Text("You can sign in again with your password or a code.", tableName: "Profile",
                 comment: "Under the sign-out question")
        }
    }

    /// A row that opens one of the Profile's pages: a button that reads as a link, with the
    /// chevron a pushed page's row has (mirrored in Arabic).
    private func pageRow(_ target: ProfilePage, _ title: Text, _ systemImage: String) -> some View {
        Button {
            page = target
        } label: {
            HStack {
                row(title, systemImage)
                Spacer(minLength: 0)
                Image(systemName: "chevron.forward")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
            .contentShape(Rectangle())
        }
        .foregroundStyle(Color.mktText)
        .accessibilityIdentifier("profile.\(target.id)")
    }

    // MARK: the rows

    private var header: some View {
        HStack(spacing: MaktabaTokens.Spacing.s3) {
            Image(systemName: "person.crop.circle.fill")
                .font(.system(size: 44))
                .foregroundStyle(Color.brass)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: model.profile?.name ?? context.user.username)
                    .font(.maktabaBody(20, weight: .semibold, relativeTo: .title3))
                Text(verbatim: homeName)
                    .font(.maktabaBody(15, relativeTo: .subheadline))
                    .foregroundStyle(Color.mktMuted)
            }
        }
        .padding(.vertical, MaktabaTokens.Spacing.s2)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("profile.header")
    }

    private var about: some View {
        let info = Bundle.main.infoDictionary ?? [:]
        let version = info["CFBundleShortVersionString"] as? String ?? "?"
        let build = info["CFBundleVersion"] as? String ?? "?"
        let unknown = String(localized: "unknown", table: "Profile", comment: "The home's version, not known")
        return Label {
            VStack(alignment: .leading, spacing: 2) {
                Text("Maktaba Player \(version) (build \(build))", tableName: "Profile", comment: "About: this app's version and build")
                Text("The home: \(homeVersionText(unknown))", tableName: "Profile", comment: "About: the home's version")
                    .font(.footnote)
                    .foregroundStyle(Color.mktMuted)
            }
        } icon: {
            Image(systemName: "info.circle")
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("profile.about")
    }

    private func homeVersionText(_ unknown: String) -> String { model.homeVersion ?? unknown }

    private func cloudRow(_ cloud: CloudLinkState) -> some View {
        Label {
            VStack(alignment: .leading, spacing: 2) {
                Text("Maktaba Cloud", tableName: "Profile", comment: "The linked cloud account row's title")
                Group {
                    if let label = cloud.linkedLabel {
                        Text("Linked as \(label)", tableName: "Profile", comment: "The cloud account this person linked")
                    } else if cloud.linked {
                        Text("Linked", tableName: "Profile", comment: "A cloud account is linked")
                    } else {
                        Text("Not linked. Link one on the home's web page.", tableName: "Profile",
                             comment: "No cloud account linked; where to link one")
                    }
                }
                .font(.footnote)
                .foregroundStyle(Color.mktMuted)
            }
        } icon: {
            Image(systemName: "cloud")
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("profile.cloud")
    }

    private func row(_ title: Text, _ systemImage: String) -> some View {
        Label { title } icon: { Image(systemName: systemImage) }
    }

    private func sectionTitle(_ title: Text) -> some View {
        title.accessibilityAddTraits(.isHeader)
    }

    // MARK: links

    @Environment(\.openURL) private var systemOpenURL

    private func open(_ url: URL?) {
        guard let url else { return }
        #if DEBUG
        if CommandLine.arguments.contains("-maktaba-record-opens") {
            opened = url
            return
        }
        #endif
        systemOpenURL(url)
    }
}

/// Where the Profile's links go.
enum ProfileLinks {
    /// The home's help page for this app (FR-27).
    static func help(home: URL) -> URL { home.appending(path: "help/apple-apps") }

    /// The system's page for this app, where its language is chosen (FR-30): the app's own
    /// page in Settings on an iPhone and iPad (Language shows there for an app in two
    /// languages); Language & Region on the Mac (its Applications list).
    static var languageSettings: URL? {
        #if os(iOS)
        URL(string: UIApplication.openSettingsURLString)
        #else
        URL(string: "x-apple.systempreferences:com.apple.Localization-Settings.extension")
        #endif
    }

    /// The language the app speaks now, in that language.
    static var currentLanguageName: String {
        let code = Bundle.main.preferredLocalizations.first ?? "en"
        return code.hasPrefix("ar") ? "العربية" : "English"
    }
}

/// The Profile's pages.
enum ProfilePage: Hashable, Identifiable {
    case history, favorites, playlists, sessions, playback, diagnostics
    case playlist(PlaylistSummary)

    var id: String {
        switch self {
        case .history: "history"
        case .favorites: "favorites"
        case .playlists: "playlists"
        case .sessions: "sessions"
        case .playback: "playback"
        case .diagnostics: "diagnostics"
        case let .playlist(p): "playlist.\(p.id)"
        }
    }

    /// Where Back goes: a playlist to the playlists, the rest to the first screen.
    var parent: ProfilePage? {
        if case .playlist = self { return .playlists }
        return nil
    }

    /// What Back says.
    var parentTitle: LocalizedStringResource {
        parent == .playlists
            ? LocalizedStringResource("Playlists", table: "Profile", comment: "The playlists page")
            : PlayerSection.profile.title
    }
}

extension View {
    /// "That did not work" for a change the home refused (a removal, signing a session out).
    func profileProblems(_ model: ProfileModel) -> some View {
        alert(Text("That did not work", tableName: "Profile", comment: "A change the home refused"),
              isPresented: Binding { model.problem != nil } set: { if !$0 { model.problem = nil } }) {
            Button(role: .cancel) {} label: { Text("OK", tableName: "Profile", comment: "Closes a message") }
        } message: {
            Text(verbatim: model.problem?.message(video: false) ?? "")
        }
    }
}
