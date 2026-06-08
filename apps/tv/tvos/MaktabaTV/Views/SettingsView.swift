import SwiftUI

/// Server connection + preferences. Doubles as the onboarding /
/// sign-in screen: when `isOnboarding` is true (no session yet) the
/// view leads with the server URL + credentials form; otherwise it
/// shows the signed-in preferences and a sign-out button.
struct SettingsView: View {
    var isOnboarding = false

    @EnvironmentObject private var auth: AuthManager
    @EnvironmentObject private var settings: AppSettings
    @Environment(\.api) private var api

    @State private var serverText = ""
    @State private var username = ""
    @State private var password = ""
    @State private var error: String?
    @State private var working = false

    var body: some View {
        NavigationStack {
            Form {
                Section("Server") {
                    TextField("https://media.example.com", text: $serverText)
                        .textContentType(.URL)
                        .autocorrectionDisabled()
                    Button("Save Server") { applyServer() }
                }

                if isOnboarding || !auth.isAuthenticated {
                    Section("Sign In") {
                        TextField("Username", text: $username)
                            .textContentType(.username)
                            .autocorrectionDisabled()
                        SecureField("Password", text: $password)
                            .textContentType(.password)
                        if let error {
                            Text(error).foregroundStyle(.red)
                        }
                        Button(working ? "Signing in…" : "Sign In") {
                            Task { await signIn() }
                        }
                        .disabled(working)
                    }
                } else {
                    Section("Account") {
                        if let user = auth.currentUser {
                            LabeledContent("Signed in as", value: user.username)
                        }
                        Button("Sign Out", role: .destructive) { auth.logOut() }
                    }
                }

                Section("Language") {
                    Picker("Language", selection: $settings.language) {
                        Text("English").tag("en")
                        Text("العربية").tag("ar")
                    }
                    .pickerStyle(.inline)
                }
            }
            .navigationTitle(isOnboarding ? "Welcome to Maktaba" : "Settings")
        }
        .onAppear { serverText = settings.serverURL.absoluteString }
    }

    private func applyServer() {
        guard let url = URL(string: serverText.trimmingCharacters(in: .whitespaces)),
              url.scheme != nil else {
            error = "Enter a full URL including https://"
            return
        }
        settings.serverURL = url
        Task { await api.setBaseURL(url) }
        error = nil
    }

    private func signIn() async {
        applyServer()
        working = true
        defer { working = false }
        do {
            try await auth.logIn(username: username, password: password)
            error = nil
            password = ""
        } catch {
            self.error = error.localizedDescription
        }
    }
}
