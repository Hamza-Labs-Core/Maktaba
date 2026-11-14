import Foundation
import Testing
@testable import MaktabaKit

/// Epic 58 story 58.01: the compatibility contract in MaktabaKit.
@Suite struct CompatDecision {
    /// shared/compat/vectors.json, found above this file.
    static func vectorsURL() throws -> URL {
        var dir = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
        while dir.path != "/" {
            let f = dir.appending(path: "shared/compat/vectors.json")
            if FileManager.default.fileExists(atPath: f.path) { return f }
            dir = dir.deletingLastPathComponent()
        }
        throw CocoaError(.fileNoSuchFile)
    }

    // T04: the shared vectors, the same verdict as Go, Kotlin and TypeScript
    @Test func everyVectorReachesItsVerdict() throws {
        let data = try Data(contentsOf: try Self.vectorsURL())
        let root = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        let cases = try #require(root["cases"] as? [[String: Any]])
        #expect(cases.count >= 10)
        var seen = Set<String>()
        for c in cases {
            let h = c["home"] as? [String: Any] ?? [:]
            let a = try #require(c["app"] as? [String: Any])
            let home = CompatHome(apiLevel: h["api_level"] as? Int ?? 0, apiLevelOldest: h["api_level_oldest"] as? Int ?? 0,
                                  clientFloor: h["client_floor"] as? [String: String] ?? [:])
            let app = CompatApp(name: a["name"] as? String ?? "", platform: a["platform"] as? String ?? "",
                                version: a["version"] as? String ?? "", minAPILevel: a["min_api_level"] as? Int ?? 0,
                                builtForAPILevel: a["built_for_api_level"] as? Int ?? 0)
            let got = Compat.decide(home, app)
            #expect(got.rawValue == c["verdict"] as? String, "\(c["name"] ?? "")")
            seen.insert(got.rawValue)
        }
        #expect(seen == ["ok", "update_home", "update_app"])
    }

    @Test func theAppIsReadFromItsClientHeader() {
        #expect(CompatApp(clientHeader: "tv/1.2.0 (tvos; tv)") == CompatApp(name: "tv", platform: "tvos", version: "1.2.0"))
        #expect(CompatApp(clientHeader: "player/dev (ios; phone)").key == "player/ios")
        #expect(CompatApp(clientHeader: "native").platform == "")
    }

    @Test func versionsCompareByNumber() {
        #expect(Compat.compareVersions("1.4", "1.4.0") == 0)
        #expect(Compat.compareVersions("1.10.0", "1.9.0") == 1)
        #expect(Compat.compareVersions("1.3.9-beta", "1.4.0") == -1)
        #expect(Compat.compareVersions("dev", "1.0") == nil)
    }

    @Test func aHomeFromBeforeTheContractReadsAsLevelZero() throws {
        let old = try JSONDecoder().decode(HomeDiscovery.self, from: Data(#"{"id":"h","name":"Kitchen","version":"0.3.100","urls":[]}"#.utf8))
        #expect(old.compat == CompatHome())
        let new = try JSONDecoder().decode(HomeDiscovery.self, from: Data(
            #"{"id":"h","urls":[],"api_level":3,"api_level_oldest":2,"client_floor":{"tv/tvos":"1.4.0"}}"#.utf8))
        #expect(new.compat == CompatHome(apiLevel: 3, apiLevelOldest: 2, clientFloor: ["tv/tvos": "1.4.0"]))
    }

    @Test func theWordsInEnglishAndArabic() {
        let url = URL(string: "http://h.test")!
        let home = CompatBlock(verdict: .updateHome, homeURL: url, homeName: "Kitchen", homeVersion: "0.3.100", admin: nil, platform: "ios")
        let en = home.text(ConnectStrings(.en))
        #expect(en.title == "This home needs an update")
        #expect(en.lines == ["Kitchen runs Maktaba 0.3.100.",
                             "Update Maktaba on Kitchen in Admin → Updates, then come back.",
                             "Ask the person who runs Kitchen to update Maktaba."])
        let ar = home.text(ConnectStrings(.ar))
        #expect(ar.title == "يحتاج هذا الجهاز الرئيسي إلى تحديث")
        #expect(ar.lines.count == 3 && ar.lines.allSatisfy { $0.contains("Kitchen") })
        // an admin is told where; a viewer whom to ask
        let admin = CompatBlock(verdict: .updateHome, homeURL: url, homeName: "Kitchen", homeVersion: nil, admin: true, platform: "ios")
        #expect(admin.text(ConnectStrings(.en)).lines == ["Update Maktaba on Kitchen in Admin → Updates, then come back."])
        let tv = CompatBlock(verdict: .updateApp, homeURL: url, homeName: "Kitchen", homeVersion: nil, admin: nil, platform: "tvos")
        #expect(tv.text(ConnectStrings(.en)).lines.last == "Open the App Store on this Apple TV, find Maktaba and choose Update.")
        #expect(tv.text(ConnectStrings(.ar)).title == "حدِّث هذا التطبيق")
        // every new key has its Arabic
        for key in ConnectStrings.Key.allCases where key.rawValue.hasPrefix("compat") {
            #expect(ConnectStrings.ar[key] != nil, "\(key) has no Arabic")
        }
    }
}

extension AgainstAStubHome {
    @MainActor @Suite struct CompatAtAHome {
        static let me = #"{"id":"u1","username":"tester","is_admin":false}"#
        static func level(_ l: Int, _ oldest: Int) -> String {
            #"{"id":"home-1","name":"Kitchen","version":"9.0.0","urls":[],"api_level":\#(l),"api_level_oldest":\#(oldest),"client_floor":{}}"#
        }

        func session(_ app: CompatApp?) throws -> AppSession {
            let defaults = try #require(UserDefaults(suiteName: "maktabakit.compat.\(UUID().uuidString)"))
            return AppSession(app: "test.compat", defaults: defaults, probe: HomeProbe(session: RouteStub.session(), timeout: 2),
                              asksHomes: false, homeSession: RouteStub.session(), compat: app)
        }

        let player = CompatApp(name: "player", platform: "ios", version: "1.0.0")
        let home = URL(string: "http://kitchen.test:8088")!

        @Test func aHomeAtLevel99ShowsUpdateThisAppAndIsAskedNothingElse() async throws {
            RouteStub.reset()
            RouteStub.on("GET kitchen.test/api/discovery/home", .json(200, Self.level(99, 99)))
            let s = try session(player)
            #expect(await s.connect(to: [home]) == nil)
            #expect(s.compatBlock?.verdict == .updateApp)
            #expect(s.compatBlock?.homeName == "Kitchen")
            #expect(RouteStub.seen.map(\.route) == ["GET kitchen.test/api/discovery/home"])
        }

        @Test func aHomeFromBeforeTheContractLetsTheseAppsCarryOn() async throws {
            RouteStub.reset()
            RouteStub.on("GET kitchen.test/api/discovery/home", .json(200, #"{"id":"home-1","name":"Kitchen","version":"0.3.100","urls":[]}"#))
            let s = try session(player)
            #expect(await s.connect(to: [home]) == nil)
            #expect(s.compatBlock == nil && s.phase == .signingIn)
            // a build that needs level 1 asks for the home to be updated
            let strict = try session(CompatApp(name: "player", platform: "ios", version: "2.0.0", minAPILevel: 1))
            #expect(await strict.connect(to: [home]) == nil)
            #expect(strict.compatBlock?.verdict == .updateHome && strict.compatBlock?.homeVersion == "0.3.100")
        }

        @Test func atStartTheHomeIsAskedBeforeItsSavedSignInIsUsed() async throws {
            RouteStub.reset()
            RouteStub.on("GET kitchen.test/api/discovery/home", .json(200, Self.level(99, 99)), .json(200, Self.level(1, 1)))
            RouteStub.on("GET kitchen.test/api/auth/me", .json(200, Self.me))
            let tokens = TokenStore(app: "test.compat", home: home)
            tokens.save(AuthTokens(accessToken: "saved.access.token", refreshToken: "r", user: nil))
            defer { tokens.clear() }
            let s = try session(player)
            s.use(Home(url: home, name: "Kitchen"))
            for _ in 0..<200 where s.compatBlock == nil { try await Task.sleep(for: .milliseconds(10)) }
            #expect(s.compatBlock?.verdict == .updateApp)
            #expect(!RouteStub.seen.map(\.route).contains("GET kitchen.test/api/auth/me"), "no sign-in was tried")
            // the home moved forward: Try again carries on and signs in with the saved tokens
            s.recheckCompat()
            for _ in 0..<200 where !s.signedIn { try await Task.sleep(for: .milliseconds(10)) }
            #expect(s.signedIn && s.compatBlock == nil)
        }

        @Test func belowTheFloorTheSignInShowsUpdateThisApp() async throws {
            RouteStub.reset()
            RouteStub.on("GET kitchen.test/api/discovery/home", .json(200, Self.level(1, 1)))
            RouteStub.on("POST kitchen.test/api/auth/login", .json(426, #"{"status":426,"code":"client_too_old","floor":"2.0.0"}"#))
            let s = try session(player)
            #expect(await s.connect(to: [home]) == nil)
            await #expect(throws: (any Error).self) { try await s.signIn(username: "tester", password: "pw") }
            #expect(s.compatBlock?.verdict == .updateApp)
            #expect(s.progress == nil)
        }

        @Test func withoutAnAppNothingIsDecided() async throws {
            RouteStub.reset()
            RouteStub.on("GET kitchen.test/api/discovery/home", .json(200, Self.level(99, 99)))
            let s = try session(nil)
            #expect(await s.connect(to: [home]) == nil)
            #expect(s.compatBlock == nil)
        }
    }
}
