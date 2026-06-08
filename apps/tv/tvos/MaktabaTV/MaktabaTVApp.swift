import SwiftUI

/// App entry point. Owns the long-lived `AuthManager` and `APIClient`
/// and injects them into the SwiftUI environment so any screen can
/// reach the session and the network without prop-drilling.
@main
struct MaktabaTVApp: App {
    @StateObject private var auth: AuthManager
    @StateObject private var settings: AppSettings
    private let api: APIClient

    init() {
        let settings = AppSettings()
        let auth = AuthManager()

        // Break the AuthManager <-> APIClient cycle: the client reads the
        // token and the refresh hook through closures, not a hard ref.
        let api = APIClient(
            baseURL: settings.serverURL,
            tokenProvider: { auth.accessToken },
            refresher: { try await auth.refresh() }
        )
        auth.api = api

        _settings = StateObject(wrappedValue: settings)
        _auth = StateObject(wrappedValue: auth)
        self.api = api
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(auth)
                .environmentObject(settings)
                .environment(\.api, api)
        }
    }
}

/// Gates the app: unauthenticated households see the sign-in screen,
/// everyone else lands on the tab bar.
struct RootView: View {
    @EnvironmentObject private var auth: AuthManager

    var body: some View {
        if auth.isAuthenticated {
            ContentView()
        } else {
            SettingsView(isOnboarding: true)
        }
    }
}

/// User-tunable app config (server URL, language) persisted in
/// `UserDefaults`. Non-secret, so it does not belong in the Keychain.
@MainActor
final class AppSettings: ObservableObject {
    @Published var serverURL: URL {
        didSet { UserDefaults.standard.set(serverURL.absoluteString, forKey: "serverURL") }
    }
    /// BCP-47 language tag, e.g. "en" or "ar". The UI mirrors RTL for
    /// Arabic — Maktaba is Arabic-first.
    @Published var language: String {
        didSet { UserDefaults.standard.set(language, forKey: "language") }
    }

    init() {
        let stored = UserDefaults.standard.string(forKey: "serverURL")
        self.serverURL = URL(string: stored ?? "") ?? URL(string: "https://demo.maktaba.app")!
        self.language = UserDefaults.standard.string(forKey: "language") ?? "en"
    }
}

/// Environment plumbing for the API client so views can pull it with
/// `@Environment(\.api)`.
private struct APIClientKey: EnvironmentKey {
    // A harmless default; the real client is injected at the root.
    static let defaultValue: APIClient = APIClient(
        baseURL: URL(string: "https://demo.maktaba.app")!,
        tokenProvider: { nil },
        refresher: { throw AuthError.notConfigured }
    )
}

extension EnvironmentValues {
    var api: APIClient {
        get { self[APIClientKey.self] }
        set { self[APIClientKey.self] = newValue }
    }
}
