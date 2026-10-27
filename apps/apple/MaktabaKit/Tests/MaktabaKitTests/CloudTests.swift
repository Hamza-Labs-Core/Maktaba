import Foundation
import Testing
@testable import MaktabaKit

// Epic 42 story 42.7: signing in to a home through Maktaba Cloud.

@Suite struct CloudAnswers {
    @Test func readsTheHomesList() throws {
        let json = #"{"homes":[{"server_id":"s1","name":"Kitchen","slug":"kitchen","relay_url":"https://kitchen.relay.test/","direct_url":"http://203.0.113.5:8088","online":true,"linked":true,"owned":true},{"server_id":"s2","name":"","slug":"cabin","relay_url":"https://cabin.relay.test","online":false,"linked":false,"owned":true}]}"#
        let homes = try JSONDecoder().decode(CloudHomes.self, from: Data(json.utf8)).homes
        #expect(homes.map(\.name) == ["Kitchen", "cabin"])
        // the direct address first, then the relay; no trailing slash
        #expect(homes[0].addresses.map(\.absoluteString) == ["http://203.0.113.5:8088", "https://kitchen.relay.test"])
        #expect(homes[0].canSignIn && homes[0].online)
        // owned but not linked: shown, not offered
        #expect(!homes[1].canSignIn && homes[1].owned && !homes[1].online)
    }

    @Test func theCloudsAddressIsRememberedAndCanGoBack() throws {
        let suite = "maktabakit.test.\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        #expect(CloudAddress.current(defaults) == URL(string: "https://api.maktaba.app")!)
        #expect(CloudAddress.set("staging-api.maktaba.test/", in: defaults))
        #expect(CloudAddress.current(defaults).absoluteString == "https://staging-api.maktaba.test")
        #expect(!CloudAddress.set("ftp://x", in: defaults))
        #expect(CloudAddress.set("", in: defaults))
        #expect(CloudAddress.current(defaults) == CloudAddress.standard)
    }

    @Test func theCloudSessionKeepsNoCookies() {
        let c = CloudClient.ephemeral().configuration
        #expect(c.httpCookieStorage == nil && !c.httpShouldSetCookies && c.urlCache == nil)
    }

    @Test func whySigningInFailedInBothLanguages() {
        for lang in [PermissionsStrings.Language.en, .ar] {
            let w = ConnectStrings(lang)
            let all: [Error] = [CloudError.wrongCredentials, CloudError.tooManyAttempts(retryAfter: 30),
                                CloudError.tooManyAttempts(retryAfter: nil), CloudError.signedOut, CloudError.notLinked,
                                CloudError.refused(code: "email_not_verified", message: ""),
                                APIError.status(401, ""), APIError.status(423, ""), APIError.status(403, "")]
            let said = all.map(w.cloudProblem)
            #expect(Set(said).count == said.count, "each says something of its own")
            #expect(said.allSatisfy { !$0.isEmpty && !$0.contains("%@") })
        }
        #expect(ConnectStrings(.en).cloudProblem(CloudError.tooManyAttempts(retryAfter: 30)) == "Too many attempts. Try again in 30 seconds.")
        #expect(ConnectStrings(.en).cloudProblem(APIError.status(423, "")) == "Your account on this home is locked for now. Try again later.")
    }
}

extension AgainstAStubHome {
    /// Maktaba Cloud at `cloud.test`, the home at `cloudrelay.test` (its relay address) and
    /// `cloudlan.test` (its address at home). One stub: these run one at a time too.
    @MainActor @Suite struct ThroughTheCloud {
        static let cloud = URL(string: "https://cloud.test")!
        static let relay = URL(string: "https://cloudrelay.test")!
        static let lan = URL(string: "http://cloudlan.test:8088")!
        static let login = #"{"access_token":"cloud.access.token","expires_in":900,"user":{"id":"cu1","email":"sara@example.com","email_verified":true}}"#
        static let homes = #"{"homes":[{"server_id":"srv-1","name":"Kitchen","slug":"kitchen","relay_url":"https://cloudrelay.test","online":true,"linked":true,"owned":false},{"server_id":"srv-2","name":"Cabin","slug":"cabin","relay_url":"https://cabin.test","online":true,"linked":false,"owned":true}]}"#
        static let discovery = #"{"id":"home-k","name":"Kitchen","urls":["http://cloudlan.test:8088","https://cloudrelay.test"],"sign_in":{"password":true,"code":true}}"#
        static let assertion = #"{"assertion":"one.use.assertion","expires_at":"2099-01-01T00:00:00Z","server_id":"srv-1","relay_url":"https://cloudrelay.test"}"#
        static let me = #"{"id":"u1","username":"sara","is_admin":false,"home_name":"Kitchen"}"#

        func client() -> CloudClient { CloudClient(base: Self.cloud, session: RouteStub.session()) }

        func session(_ app: String) throws -> (AppSession, UserDefaults) {
            let defaults = try #require(UserDefaults(suiteName: "maktabakit.test.\(UUID().uuidString)"))
            CloudAddress.set(Self.cloud.absoluteString, in: defaults)
            for u in [Self.relay, Self.lan, URL(string: "https://cabin.test")!, Self.cloud] { TokenStore(app: app, home: u).clear() }
            let s = AppSession(app: app, defaults: defaults, probe: HomeProbe(session: RouteStub.session(), timeout: 2),
                               asksHomes: false, homeSession: RouteStub.session(), cloudSession: RouteStub.session())
            return (s, defaults)
        }

        func cleared(_ app: String) {
            for u in [Self.relay, Self.lan, URL(string: "https://cabin.test")!, Self.cloud] { TokenStore(app: app, home: u).clear() }
        }

        func waitUntil(_ done: @MainActor () -> Bool) async throws {
            for _ in 0..<100 where !done() { try await Task.sleep(for: .milliseconds(20)) }
        }

        // MARK: the cloud's own requests

        @Test func signsInListsTheHomesAndAsksForAnAssertion() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, Self.login))
            RouteStub.on("GET cloud.test/v1/homes", .json(200, Self.homes))
            RouteStub.on("POST cloud.test/v1/homes/srv-1/assertion", .json(200, Self.assertion))
            let c = client()
            let user = try await c.signIn(email: "sara@example.com", password: "pw")
            #expect(user.email == "sara@example.com" && user.id == "cu1")
            #expect(await c.isSignedIn)
            let homes = try await c.homes()
            #expect(homes.map(\.serverID) == ["srv-1", "srv-2"])
            let a = try await c.signInAssertion(serverID: "srv-1")
            #expect(a.assertion == "one.use.assertion")
            let seen = RouteStub.seen
            #expect(seen.map(\.route) == ["POST cloud.test/v1/auth/login", "GET cloud.test/v1/homes", "POST cloud.test/v1/homes/srv-1/assertion"])
            #expect(seen[0].body == ["email": "sara@example.com", "password": "pw"] && seen[0].auth == nil)
            #expect(seen[1].auth == "Bearer cloud.access.token" && seen[2].auth == "Bearer cloud.access.token")
            #expect(seen[2].body == ["purpose": "signin"])
            #expect(seen.allSatisfy { $0.client == ClientHeader.value })
        }

        @Test func theCloudsRefusals() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login",
                         .json(401, #"{"error":"invalid_credentials","message":""}"#),
                         .init(status: 429, body: #"{"error":"too_many_attempts","message":"too many failed sign-ins"}"#, headers: ["Retry-After": "30"]),
                         .json(403, #"{"error":"email_not_verified","message":"confirm your email address first"}"#),
                         .json(200, Self.login))
            let c = client()
            await #expect(throws: CloudError.wrongCredentials) { try await c.signIn(email: "a@b.c", password: "x") }
            await #expect(throws: CloudError.tooManyAttempts(retryAfter: 30)) { try await c.signIn(email: "a@b.c", password: "x") }
            await #expect(throws: CloudError.refused(code: "email_not_verified", message: "confirm your email address first")) {
                try await c.signIn(email: "a@b.c", password: "x")
            }
            #expect(await !c.isSignedIn)
            try await c.signIn(email: "a@b.c", password: "x")
            // a home this account is not linked to: the cloud's 404
            RouteStub.on("POST cloud.test/v1/homes/srv-2/assertion", .json(404, #"{"error":"not_found","message":""}"#))
            await #expect(throws: CloudError.notLinked) { try await c.signInAssertion(serverID: "srv-2") }
            // the cloud ended the session: signed out here too, then nothing is sent
            RouteStub.on("GET cloud.test/v1/homes", .json(401, #"{"error":"unauthorized","message":""}"#))
            await #expect(throws: CloudError.signedOut) { try await c.homes() }
            #expect(await !c.isSignedIn)
            let before = RouteStub.seen.count
            await #expect(throws: CloudError.signedOut) { try await c.homes() }
            #expect(RouteStub.seen.count == before)
        }

        @Test func anEndedTokenIsNotSent() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, Self.login))
            final class Clock: @unchecked Sendable { var now = Date(timeIntervalSince1970: 1_000_000) }
            let clock = Clock()
            let c = CloudClient(base: Self.cloud, session: RouteStub.session(), now: { clock.now })
            try await c.signIn(email: "a@b.c", password: "x")
            #expect(await c.isSignedIn)
            clock.now += 900
            #expect(await !c.isSignedIn)
            await #expect(throws: CloudError.signedOut) { try await c.signInAssertion(serverID: "srv-1") }
            #expect(RouteStub.seen.map(\.route) == ["POST cloud.test/v1/auth/login"])
        }

        /// The password and the cloud's token stay with the cloud's address: a redirect to
        /// another origin is not followed (a 307 would resend the body).
        @Test func noRedirectToAnotherOrigin() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .init(status: 307, body: "", redirect: "https://evil.test/v1/auth/login", redirectStatus: 307))
            RouteStub.on("POST evil.test/v1/auth/login", .json(200, Self.login))
            await #expect(throws: CloudError.self) { try await client().signIn(email: "a@b.c", password: "secret") }
            #expect(!RouteStub.seen.contains { $0.route.contains("evil.test") })
        }

        /// The cloud's token is kept in memory only: not in the defaults, not in the Keychain,
        /// and the next start of the app is signed out of the cloud.
        @Test func theCloudTokenIsNotKept() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, Self.login))
            RouteStub.on("GET cloud.test/v1/homes", .json(200, Self.homes))
            let app = "test.cloud.memory"
            let (s, defaults) = try session(app)
            defer { cleared(app) }
            try await s.cloudSignIn(email: "sara@example.com", password: "pw")
            #expect(s.cloudUser?.email == "sara@example.com" && s.cloudHomes.count == 2)
            let stored = defaults.dictionaryRepresentation().values.map { String(describing: $0) }.joined()
            #expect(!stored.contains("cloud.access.token"))
            #expect(TokenStore(app: app, home: Self.cloud).access == nil)
            let again = AppSession(app: app, defaults: defaults, asksHomes: false, cloudSession: RouteStub.session())
            #expect(again.cloudUser == nil)
            #expect(await !again.cloud.isSignedIn)
            #expect(again.cloudAddress == Self.cloud, "the address is remembered, the token is not")
        }

        @Test func changingTheCloudsAddressSignsOutOfIt() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, Self.login))
            RouteStub.on("GET cloud.test/v1/homes", .json(200, Self.homes))
            let app = "test.cloud.address"
            let (s, _) = try session(app)
            try await s.cloudSignIn(email: "sara@example.com", password: "pw")
            #expect(s.setCloudAddress("https://cloud.test"))
            #expect(s.cloudUser != nil, "the same address: still signed in")
            #expect(s.setCloudAddress("https://other-cloud.test"))
            #expect(s.cloudUser == nil && s.cloudHomes.isEmpty && s.cloudAddress.absoluteString == "https://other-cloud.test")
            #expect(!s.setCloudAddress("not a url at all ://"))
        }

        // MARK: signing in to a home with an assertion

        @Test func signingInThroughTheCloudKeepsTheHomesTokensAtThatAddressOnly() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, Self.login))
            RouteStub.on("GET cloud.test/v1/homes", .json(200, Self.homes))
            RouteStub.on("GET cloudrelay.test/api/discovery/home", .json(200, Self.discovery))
            RouteStub.on("POST cloud.test/v1/homes/srv-1/assertion", .json(200, Self.assertion))
            RouteStub.on("POST cloudrelay.test/api/auth/cloud", .json(200, #"{"access_token":"relay.access","refresh_token":"relay.refresh","user":{"id":"u1","username":"sara","is_admin":false},"cloud_scope":"play"}"#))
            RouteStub.on("GET cloudrelay.test/api/auth/me", .json(200, Self.me))
            let app = "test.cloud.signin"
            let (s, _) = try session(app)
            defer { cleared(app) }
            try await s.cloudSignIn(email: "sara@example.com", password: "pw")
            let kitchen = try #require(s.cloudHomes.first { $0.serverID == "srv-1" })
            try await s.signIn(through: kitchen)
            #expect(s.signedIn)
            #expect(s.current?.url == Self.relay && s.current?.cloudServerID == "srv-1" && s.current?.homeID == "home-k")
            // both addresses known, the LAN's from the home's own list
            #expect(s.current?.addresses == [Self.lan])
            #expect(TokenStore(app: app, home: Self.relay).access == "relay.access")
            #expect(TokenStore(app: app, home: Self.relay).refresh == "relay.refresh")
            #expect(TokenStore(app: app, home: Self.lan).access == nil, "nothing at the other address")
            let trade = try #require(RouteStub.seen.first { $0.route == "POST cloudrelay.test/api/auth/cloud" })
            #expect(trade.body == ["assertion": "one.use.assertion"] && trade.client == ClientHeader.value && trade.auth == nil)
            // the cloud's token never goes to the home, the home's never to the cloud
            #expect(RouteStub.seen.filter { $0.route.contains("cloudrelay.test") }.allSatisfy { $0.auth != "Bearer cloud.access.token" })
            #expect(RouteStub.seen.filter { $0.route.contains("cloud.test/") }.allSatisfy { $0.auth != "Bearer relay.access" })
        }

        @Test func anUnlinkedHomeIsNotOffered() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, Self.login))
            RouteStub.on("GET cloud.test/v1/homes", .json(200, Self.homes))
            let (s, _) = try session("test.cloud.unlinked")
            try await s.cloudSignIn(email: "sara@example.com", password: "pw")
            let cabin = try #require(s.cloudHomes.first { $0.serverID == "srv-2" })
            #expect(!cabin.canSignIn)
            let before = RouteStub.seen.count
            await #expect(throws: CloudError.notLinked) { try await s.signIn(through: cabin) }
            #expect(RouteStub.seen.count == before, "no assertion asked for, no home asked")
            #expect(s.current == nil && s.phase == .choosingHome)
            #expect(s.cloudServer(for: URL(string: "https://cabin.test")!) == nil)
        }

        @Test(arguments: [(401, "The home refused"), (423, "locked"), (403, "change your password")])
        func theHomesRefusals(status: Int, says: String) async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, Self.login))
            RouteStub.on("GET cloud.test/v1/homes", .json(200, Self.homes))
            RouteStub.on("GET cloudrelay.test/api/discovery/home", .json(200, Self.discovery))
            RouteStub.on("POST cloud.test/v1/homes/srv-1/assertion", .json(200, Self.assertion))
            RouteStub.on("POST cloudrelay.test/api/auth/cloud", .init(status: status, body: #"{"title":"no","status":\#(status)}"#, contentType: "application/problem+json"))
            let app = "test.cloud.refused.\(status)"
            let (s, _) = try session(app)
            defer { cleared(app) }
            try await s.cloudSignIn(email: "sara@example.com", password: "pw")
            let kitchen = try #require(s.cloudHomes.first { $0.serverID == "srv-1" })
            do {
                try await s.signIn(through: kitchen)
                Issue.record("signed in")
            } catch {
                #expect(ConnectStrings(.en).cloudProblem(error).contains(says))
            }
            #expect(!s.signedIn && s.phase == .signingIn, "the sign-in screen for that address")
            #expect(TokenStore(app: app, home: Self.relay).access == nil)
        }

        @Test func theAssertionIsNotSentAcrossARedirect() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloudrelay.test/api/auth/cloud", .init(status: 307, body: "", redirect: "https://evil.test/api/auth/cloud", redirectStatus: 307))
            RouteStub.on("POST evil.test/api/auth/cloud", .json(200, #"{"access_token":"x","refresh_token":"y"}"#))
            let tokens = TokenStore(app: "test.cloud.redirect", home: Self.relay)
            tokens.clear()
            defer { tokens.clear() }
            let c = APIClient(home: Self.relay, tokens: tokens, session: RouteStub.session())
            await #expect(throws: APIError.self) { _ = try await c.signIn(assertion: "one.use.assertion") }
            #expect(!RouteStub.seen.contains { $0.route.contains("evil.test") })
            #expect(tokens.access == nil)
        }

        // MARK: both addresses

        /// Away from home: the LAN address does not answer, the relay does. Its tokens are not
        /// the LAN's; signed in to the cloud, the app signs in there afresh without asking, and
        /// the LAN's token never goes to the relay.
        @Test func awayFromHomeTheRelayIsSignedInToAfresh() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, Self.login))
            RouteStub.on("GET cloud.test/v1/homes", .json(200, Self.homes))
            RouteStub.on("GET cloudrelay.test/api/discovery/home", .json(200, Self.discovery))
            RouteStub.on("POST cloud.test/v1/homes/srv-1/assertion", .json(200, Self.assertion))
            RouteStub.on("POST cloudrelay.test/api/auth/cloud", .json(200, #"{"access_token":"relay.access","refresh_token":"relay.refresh"}"#))
            RouteStub.on("GET cloudrelay.test/api/auth/me", .json(200, Self.me))
            let app = "test.cloud.away"
            let (s, _) = try session(app)
            defer { cleared(app) }
            // known from the LAN, signed in there
            TokenStore(app: app, home: Self.lan).save(AuthTokens(accessToken: "issued.by.lan", refreshToken: "lan.refresh", user: nil))
            s.use(Home(url: Self.lan, name: "Kitchen", homeID: "home-k", addresses: [Self.relay]))
            s.switchHome()
            try await s.cloudSignIn(email: "sara@example.com", password: "pw")
            // cloudlan.test does not answer now
            s.open(try #require(s.homes.first { $0.url == Self.lan }))
            try await waitUntil { s.signedIn }
            #expect(s.signedIn && s.current?.url == Self.relay)
            #expect(TokenStore(app: app, home: Self.relay).access == "relay.access")
            #expect(TokenStore(app: app, home: Self.lan).access == "issued.by.lan", "the LAN keeps its own")
            let toRelay = RouteStub.seen.filter { $0.route.contains("cloudrelay.test") }
            #expect(!toRelay.isEmpty && toRelay.allSatisfy { $0.auth != "Bearer issued.by.lan" })
            #expect(!RouteStub.seen.contains { $0.auth == "Bearer issued.by.lan" && !$0.route.contains("cloudlan.test") })
        }

        /// Signed out of the cloud, a new address shows its sign-in screen: nothing is sent.
        @Test func withoutTheCloudANewAddressAsksToSignIn() async throws {
            RouteStub.reset()
            RouteStub.on("GET cloudrelay.test/api/discovery/home", .json(200, Self.discovery))
            let app = "test.cloud.nocloud"
            let (s, _) = try session(app)
            defer { cleared(app) }
            TokenStore(app: app, home: Self.lan).save(AuthTokens(accessToken: "issued.by.lan", refreshToken: "r", user: nil))
            s.use(Home(url: Self.lan, name: "Kitchen", homeID: "home-k", addresses: [Self.relay]))
            s.switchHome()
            s.open(try #require(s.homes.first { $0.url == Self.lan }))
            try await waitUntil { s.current?.url == Self.relay }
            try await Task.sleep(for: .milliseconds(150))
            #expect(s.current?.url == Self.relay && s.phase == .signingIn)
            #expect(!RouteStub.seen.contains { $0.route.contains("auth/cloud") || $0.route.contains("cloud.test/") })
            #expect(!RouteStub.seen.contains { $0.auth != nil && !$0.route.contains("cloudlan.test") })
        }

        /// An address the cloud does not give for the home (one it announced itself, on the
        /// LAN) is never handed an assertion, even signed in to the cloud.
        @Test func anAssertionGoesOnlyToAnAddressTheCloudGives() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, Self.login))
            RouteStub.on("GET cloud.test/v1/homes", .json(200, Self.homes))
            RouteStub.on("GET cloudlan.test/api/discovery/home", .json(200, Self.discovery))
            let app = "test.cloud.lanonly"
            let (s, _) = try session(app)
            defer { cleared(app) }
            try await s.cloudSignIn(email: "sara@example.com", password: "pw")
            #expect(s.cloudServer(for: Self.relay) == "srv-1" && s.cloudServer(for: Self.lan) == nil)
            await s.connect(to: [Self.lan])
            try await Task.sleep(for: .milliseconds(200))
            #expect(s.current?.url == Self.lan && s.phase == .signingIn)
            #expect(!RouteStub.seen.contains { $0.route.contains("/assertion") || $0.route.contains("auth/cloud") })
        }
    }
}
