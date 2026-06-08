import Foundation
import Combine
import Security

/// Owns the user's session: JWT access/refresh tokens (persisted in
/// the Keychain) and the derived auth state the UI binds to.
///
/// Why Keychain and not `UserDefaults`? tvOS gives apps no guaranteed
/// persistent on-device storage — the system can evict the app's data
/// container under disk pressure. The Keychain is the one store that
/// survives, and it encrypts the token at rest. See the README.
@MainActor
final class AuthManager: ObservableObject {
    @Published private(set) var currentUser: User?
    @Published private(set) var isAuthenticated = false

    /// Lazily-wired API client. `AuthManager` and `APIClient` need each
    /// other, so the client is injected after both exist (see App init).
    var api: APIClient?

    private let service = "com.hamzalabs.maktaba.tv"
    private let accessKey = "access_token"
    private let refreshKey = "refresh_token"

    init() {
        // Treat a stored access token as a warm session; the first API
        // 401 will trigger a refresh or bounce to the login screen.
        isAuthenticated = (accessToken != nil)
    }

    // MARK: - Token accessors

    var accessToken: String? { read(accessKey) }
    var refreshToken: String? { read(refreshKey) }

    // MARK: - Auth flows

    func logIn(username: String, password: String) async throws {
        guard let api else { throw AuthError.notConfigured }
        let tokens: AuthTokens = try await api.post(
            "/api/auth/login",
            body: ["username": username, "password": password]
        )
        persist(tokens)
    }

    /// Exchanges the refresh token for a fresh access token. Returns the
    /// new access token so a 401-retry path can use it immediately.
    @discardableResult
    func refresh() async throws -> String {
        guard let api else { throw AuthError.notConfigured }
        guard let refresh = refreshToken else { throw AuthError.sessionExpired }
        let tokens: AuthTokens = try await api.post(
            "/api/auth/refresh",
            body: ["refresh_token": refresh]
        )
        persist(tokens)
        return tokens.accessToken
    }

    func logOut() {
        delete(accessKey)
        delete(refreshKey)
        currentUser = nil
        isAuthenticated = false
    }

    private func persist(_ tokens: AuthTokens) {
        write(accessKey, tokens.accessToken)
        write(refreshKey, tokens.refreshToken)
        if let user = tokens.user { currentUser = user }
        isAuthenticated = true
    }

    // MARK: - Keychain primitives

    private func write(_ key: String, _ value: String) {
        let data = Data(value.utf8)
        let base: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
        ]
        SecItemDelete(base as CFDictionary)
        var add = base
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        SecItemAdd(add as CFDictionary, nil)
    }

    private func read(_ key: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data else { return nil }
        return String(decoding: data, as: UTF8.self)
    }

    private func delete(_ key: String) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
        ]
        SecItemDelete(query as CFDictionary)
    }
}

enum AuthError: LocalizedError {
    case notConfigured
    case sessionExpired

    var errorDescription: String? {
        switch self {
        case .notConfigured: return "Auth manager is not connected to an API client."
        case .sessionExpired: return "Your session expired. Please sign in again."
        }
    }
}
