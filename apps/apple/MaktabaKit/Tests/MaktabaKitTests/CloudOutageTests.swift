import Foundation
import Testing
@testable import MaktabaKit

/// Epic 50 story 50.13: the apps ride out a cloud outage (T01, T04, T05, T06).
extension AgainstAStubHome {
    @MainActor @Suite struct RidingOutTheCloud {
        typealias T = ThroughTheCloud
        static let homesWithConnections = #"{"homes":[{"server_id":"srv-1","name":"Kitchen","slug":"kitchen","relay_url":"https://cloudrelay.test","direct_url":"http://cloudlan.test:8088","connections":[{"url":"https://cloudrelay.test","kind":"relay"},{"url":"http://cloudlan.test:8088","kind":"direct"}],"online":true,"linked":true,"owned":false}]}"#

        func session(_ app: String) throws -> (AppSession, UserDefaults) { try T().session(app) }

        // T04: the order apps try, from the list or from the older fields
        @Test func connectionsInTheOrderAppsTry() throws {
            let h = try JSONDecoder().decode(CloudHomes.self, from: Data(Self.homesWithConnections.utf8)).homes[0]
            #expect(h.connections.map(\.kind) == ["direct", "relay"])
            #expect(h.addresses == [URL(string: "http://cloudlan.test:8088")!, URL(string: "https://cloudrelay.test")!])
            let mixed = [CloudConnection(url: URL(string: "https://r.test")!, kind: "relay"),
                         CloudConnection(url: URL(string: "https://h.test")!, kind: "relay_hint"),
                         CloudConnection(url: URL(string: "https://d.test")!, kind: "direct"),
                         CloudConnection(url: URL(string: "http://l.test")!, kind: "local"),
                         CloudConnection(url: URL(string: "https://x.test")!, kind: "carrier_pigeon")]
            #expect(CloudConnection.ordered(mixed).map(\.kind) == ["local", "direct", "relay_hint", "relay"])
            let old = try JSONDecoder().decode(CloudHomes.self, from: Data(T.homes.utf8)).homes[0]
            #expect(old.connections.map(\.kind) == ["relay"], "an older cloud: from relay_url")
        }

        // T01: a good list is kept; the cloud down, the saved one shows with its time
        @Test func theSavedListWhenTheCloudIsDown() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, T.login))
            RouteStub.on("GET cloud.test/v1/homes", .json(200, Self.homesWithConnections),
                         .json(503, #"{"error":"unavailable","message":""}"#))
            let app = "test.cloud.saved"
            let (s, defaults) = try session(app)
            defer { T().cleared(app) }
            try await s.cloudSignIn(email: "Sara@Example.com", password: "pw")
            #expect(s.cloudHomesSavedAt == nil && s.cloudHomes.count == 1)
            let saved = try #require(SavedCloudHomes.load(email: "sara@example.com", from: defaults))
            #expect(saved.homes.first?.connections.map(\.kind) == ["direct", "relay"])
            try await s.loadCloudHomes()
            #expect(s.cloudHomesSavedAt != nil, "the saved list, with its time")
            #expect(s.cloudHomes.map(\.serverID) == ["srv-1"] && !s.cloudHomesOld)
            // signing out takes it away
            s.cloudSignOut(forgetSaved: true)
            #expect(SavedCloudHomes.load(email: "sara@example.com", from: defaults) == nil)
        }

        @Test func aSignInTheCloudCantAnswerShowsTheSavedListAndAnOldOneIsMarked() async throws {
            RouteStub.reset()
            // no route for the login: the cloud doesn't connect
            let app = "test.cloud.offline"
            let (s, defaults) = try session(app)
            defer { T().cleared(app) }
            let home = try JSONDecoder().decode(CloudHomes.self, from: Data(Self.homesWithConnections.utf8)).homes
            SavedCloudHomes.save(home, for: CloudUser(id: "cu1", email: "sara@example.com"), in: defaults,
                                 now: .now.addingTimeInterval(-31 * 24 * 3600))
            try await s.cloudSignIn(email: "sara@example.com", password: "pw")
            #expect(s.cloudOffline && s.cloudHomes.map(\.serverID) == ["srv-1"])
            #expect(s.cloudHomesOld, "over 30 days: still shown, marked old")
            // another account's list is never shown
            let (s2, _) = try session("test.cloud.offline2")
            await #expect(throws: CloudError.self) { try await s2.cloudSignIn(email: "omar@example.com", password: "pw") }
            #expect(s2.cloudHomes.isEmpty)
        }

        // T06: the assertion can't be had: the home's own sign-in is offered, in both languages
        @Test func aCloudSignInWhileTheCloudIsDownOffersTheHomesOwn() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloud.test/v1/auth/login", .json(200, T.login))
            RouteStub.on("GET cloud.test/v1/homes", .json(200, T.homes))
            RouteStub.on("GET cloudrelay.test/api/discovery/home", .json(200, T.discovery))
            RouteStub.on("POST cloud.test/v1/homes/srv-1/assertion", .json(504, #"{"error":"timeout","message":""}"#))
            let app = "test.cloud.assertiondown"
            let (s, _) = try session(app)
            defer { T().cleared(app) }
            try await s.cloudSignIn(email: "sara@example.com", password: "pw")
            let kitchen = try #require(s.cloudHomes.first { $0.serverID == "srv-1" })
            await #expect(throws: CloudError.cloudDown) { try await s.signIn(through: kitchen) }
            #expect(s.current?.url == T.relay, "the home is picked: its own sign-in shows")
            #expect(!s.signedIn)
            #expect(ConnectStrings(.en).cloudProblem(CloudError.cloudDown).contains("Sign in to your home with its username and password"))
            #expect(ConnectStrings(.ar).cloudProblem(CloudError.cloudDown).contains("سجّل الدخول إلى الجهاز الرئيسي"))
        }

        // T08 (in the client library): the cloud unreachable, a home's own sign-in and its pages
        // work, and not one request goes to the cloud
        @Test func withTheCloudDownAHomesOwnSignInNeverAsksTheCloud() async throws {
            RouteStub.reset()
            RouteStub.on("POST cloudlan.test/api/auth/login", .json(200, #"{"access_token":"lan.access","refresh_token":"lan.refresh","user":{"id":"u1","username":"sara","is_admin":false}}"#))
            RouteStub.on("GET cloudlan.test/api/libraries", .json(200, #"{"items":[{"id":"l1","name":"Talks"}],"next":null}"#))
            let app = "test.cloud.down.lan"
            defer { TokenStore(app: app, home: T.lan).clear() }
            let c = APIClient(home: T.lan, tokens: TokenStore(app: app, home: T.lan), session: RouteStub.session())
            _ = try await c.signIn(username: "sara", password: "pw")
            #expect(try await c.libraries().map(\.name) == ["Talks"])
            #expect(!RouteStub.seen.contains { $0.route.contains("cloud.test/") }, "nothing went to the cloud")
        }

        // T05: the relay's home_reconnecting three times, then the page
        @Test func aHomeReconnectingIsWaitedFor() async throws {
            RouteStub.reset()
            let reconnecting = RouteStub.Answer(status: 503, body: #"{"error":"home_reconnecting","message":""}"#, headers: ["Retry-After": "5"])
            RouteStub.on("GET cloudrelay.test/api/libraries", reconnecting, reconnecting, reconnecting, .json(200, #"{"items":[]}"#))
            let app = "test.reconnect"
            defer { TokenStore(app: app, home: T.relay).clear() }
            let c = APIClient(home: T.relay, tokens: TokenStore(app: app, home: T.relay), session: RouteStub.session())
            await c.setReconnectSleep { _ in }
            nonisolated(unsafe) var told: [Bool] = []
            let watch = NotificationCenter.default.addObserver(forName: APIClient.reconnectingChanged, object: nil, queue: nil) { n in
                told.append(n.userInfo?["waiting"] as? Bool ?? false)
            }
            defer { NotificationCenter.default.removeObserver(watch) }
            let data = try await c.sendRaw(await c.request("GET", "/api/libraries"), authorized: false)
            #expect(String(decoding: data, as: UTF8.self) == #"{"items":[]}"#)
            #expect(RouteStub.seen.filter { $0.route == "GET cloudrelay.test/api/libraries" }.count == 4)
            #expect(told == [true, false], "the strip is told once it waits, and when it stops")
            #expect(StatusStripRules.lines(nil, online: true, admin: false, reconnecting: true).map(\.text) == [StripWords.reconnecting])
        }

        @Test func serverOfflineIsNotRetried() async throws {
            RouteStub.reset()
            RouteStub.on("GET cloudrelay.test/api/libraries", .json(502, #"{"error":"server_offline","message":""}"#))
            let app = "test.reconnect.offline"
            defer { TokenStore(app: app, home: T.relay).clear() }
            let c = APIClient(home: T.relay, tokens: TokenStore(app: app, home: T.relay), session: RouteStub.session())
            await c.setReconnectSleep { _ in }
            await #expect(throws: APIError.self) { _ = try await c.sendRaw(await c.request("GET", "/api/libraries"), authorized: false) }
            #expect(RouteStub.seen.count == 1)
        }
    }
}
