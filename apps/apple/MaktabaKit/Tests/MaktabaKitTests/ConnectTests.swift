import Foundation
import Testing
@testable import MaktabaKit

// Epic 42 stories 42.2 (the home picker) and 42.6 (signing in by code).

@Suite struct ConnectLinks {
    @Test func readsAHomesLink() throws {
        let link = try #require(ConnectLink(string: "maktaba://connect?id=home-1&n=Living%20room&u=http://192.168.1.20:8088&u=https://abc.relay.maktaba.dev/&u=http://192.168.1.20:8088&u=ftp://x&u=notaurl%20at%20all"))
        #expect(link.homeID == "home-1")
        #expect(link.name == "Living room")
        // in the link's order (local first), once each, http(s) only, no trailing slash
        #expect(link.urls.map(\.absoluteString) == ["http://192.168.1.20:8088", "https://abc.relay.maktaba.dev"])
    }

    @Test func keepsAHomesBasePath() throws {
        let link = try #require(ConnectLink(string: "maktaba://connect?u=https%3A%2F%2Fnas.example.com%2Fmaktaba%2F"))
        #expect(link.urls.map(\.absoluteString) == ["https://nas.example.com/maktaba"])
        #expect(link.homeID == nil && link.name == nil)
    }

    @Test(arguments: [
        "maktaba:connect?u=http://10.0.0.5:8088",
        "MAKTABA://CONNECT?u=http://10.0.0.5:8088",
    ])
    func readsEitherForm(text: String) {
        #expect(ConnectLink(string: text)?.urls.first?.absoluteString == "http://10.0.0.5:8088")
    }

    @Test(arguments: [
        "maktaba://connect?id=x",               // no address
        "maktaba://connect?u=ftp://x",
        "maktaba://pair?code=ABCD-1234",
        "https://home.example.com/?u=http://x",
        "",
    ])
    func refuses(text: String) {
        #expect(ConnectLink(string: text) == nil)
    }
}

@Suite struct ScannedCodes {
    @Test func tellsWhatACodeHolds() throws {
        let link = try #require(ConnectLink(string: "maktaba://connect?u=http://h:8088"))
        #expect(ScannedCode.parse("maktaba://connect?u=http://h:8088") == .connect(link))
        #expect(ScannedCode.parse("http://192.168.1.20:8088/pair?code=abcd-1234") == .signInCode("ABCD-1234"))
        #expect(ScannedCode.parse("https://h.example.com/maktaba/pair?code=ABCD1234") == .signInCode("ABCD-1234"))
        #expect(ScannedCode.parse("abcd 1234") == .signInCode("ABCD-1234"))
        #expect(ScannedCode.parse("http://192.168.1.20:8088") == .address(URL(string: "http://192.168.1.20:8088")!))
        // a page with ?code= that is not the approve page is an address
        #expect(ScannedCode.parse("http://h:8088/videos?code=ABCD1234") == .address(URL(string: "http://h:8088")!))
        #expect(ScannedCode.parse("   ") == nil)
    }

    @Test(arguments: [
        ("ABCD-1234", "ABCD-1234"), ("abcd1234", "ABCD-1234"), (" ab cd 12 34 ", "ABCD-1234"),
        ("OILZ-0000", "011Z-0000"),       // O reads as 0, I and L as 1
        ("ABCD-١٢٣٤", "ABCD-1234"),       // Arabic-Indic digits
        ("ABCD-۱۲۳۴", "ABCD-1234"),       // Persian digits
    ])
    func normalizesADeviceCode(raw: String, want: String) {
        #expect(DeviceCode.normalize(raw) == want)
    }

    @Test(arguments: ["", "ABCD-123", "ABCD-12345", "ABCU-1234", "ABCD_1234", "AB#D-1234"])
    func refusesWhatIsNotADeviceCode(raw: String) {
        #expect(DeviceCode.normalize(raw) == nil)
    }
}

@Suite struct Announcements {
    @Test(arguments: [
        ("nas.local.", ["web": "8088", "path": "/maktaba/"], "http://nas.local:8088/maktaba"),
        ("nas.local.", ["web": "8088", "path": "maktaba"], "http://nas.local:8088/maktaba"),
        ("nas.local.", ["web": "8088", "path": "/"], "http://nas.local:8088"),
        ("nas.local.", ["web": "8443", "tls": "true"], "https://nas.local:8443"),
    ])
    func aHomesAddressWithItsPath(host: String, txt: [String: String], want: String) {
        #expect(HomeBrowser.homeURL(host: host, apiPort: 8080, txt: txt)?.absoluteString == want)
    }

    @Test func readsTheTXTRecord() throws {
        let f = try #require(HomeBrowser.found(instance: "Maktaba on nas", host: "nas.local.", apiPort: 8080,
                                               txt: ["id": "home-1", "name": "Living room", "web": "8088", "protocol": "1", "version": "0.40.0", "schema_rev": "90", "tls": "0"]))
        #expect(f.id == "home-1" && f.homeID == "home-1" && f.name == "Living room")
        #expect(f.url.absoluteString == "http://nas.local:8088")
        // an older home: no id or name
        let old = try #require(HomeBrowser.found(instance: "Maktaba on box", host: "box.local.", apiPort: 8080, txt: ["web": "8088"]))
        #expect(old.homeID == nil && old.name == "Maktaba on box" && old.id == "box.local:8088")
        #expect(HomeBrowser.isHome([:]) && HomeBrowser.isHome(["role": "home"]) && !HomeBrowser.isHome(["role": "node"]))
    }

    /// Every machine of a cluster announces; `home` names the home on each, `id` the machine.
    @Test func aHomeIsKnownByItsHomeKeyAndANodeIsNotListed() throws {
        let home = try #require(HomeBrowser.found(instance: "Kitchen", host: "studio.local.", apiPort: 8080,
                                                  txt: ["role": "home", "home": "0A1B2C3D-0000-4000-8000-000000000000", "id": "0a1b2c3d-0000-4000-8000-000000000000", "web": "8088", "tls": "yes"]))
        #expect(home.homeID == "0a1b2c3d-0000-4000-8000-000000000000" && home.url.scheme == "https")
        #expect(HomeBrowser.found(instance: "Basement", host: "basement.local.", apiPort: 8080,
                                  txt: ["role": "node", "home": "0a1b2c3d-0000-4000-8000-000000000000", "id": "node-7", "web": "8088"]) == nil)
        // an announcement with only its own id (35.11): the id it is
        #expect(HomeBrowser.found(instance: "x", host: "x.local.", apiPort: 8080, txt: ["id": "abc"])?.homeID == "abc")
    }

    @Test func oneHomeAnnouncedTwiceIsListedOnce() {
        var homes = FoundHomes()
        let url = URL(string: "http://nas.local:8088")!
        homes.add("nas (2)", .init(name: "Living room", url: URL(string: "http://192.168.1.20:8088")!, homeID: "home-1"))
        homes.add("nas", .init(name: "Living room", url: url, homeID: "home-1"))
        // no id: the same address is the same home
        homes.add("old", .init(name: "Box", url: URL(string: "http://box.local:8088")!, homeID: nil))
        homes.add("old v6", .init(name: "Box", url: URL(string: "http://BOX.local:8088")!, homeID: nil))
        homes.add("other", .init(name: "Attic", url: URL(string: "http://attic.local:8088")!, homeID: "home-2"))
        #expect(homes.list.map(\.name) == ["Attic", "Box", "Living room"])
        // the first instance by name stands for the home
        #expect(homes.list.first { $0.id == "home-1" }?.url == url)
        homes.keep(["nas (2)", "other"])
        #expect(homes.list.map(\.name) == ["Attic", "Living room"])
        #expect(homes.has("nas (2)") && !homes.has("old"))
    }
}

@Suite struct TypedAddresses {
    @Test func triesWhatWasTypedThenTheHostThenTheOtherScheme() {
        #expect(Home.candidates(for: "nas.example.com/maktaba/").map(\.absoluteString)
            == ["https://nas.example.com/maktaba", "https://nas.example.com", "http://nas.example.com/maktaba", "http://nas.example.com"])
        #expect(Home.candidates(for: "192.168.1.20:8088").map(\.absoluteString)
            == ["http://192.168.1.20:8088", "https://192.168.1.20:8088"])
        // a scheme typed is kept
        #expect(Home.candidates(for: "http://10.0.0.5:8088").map(\.absoluteString) == ["http://10.0.0.5:8088"])
        #expect(Home.candidates(for: "  ").isEmpty && Home.candidates(for: "ftp://x").isEmpty)
    }

    @Test func showsAnAddressShort() {
        #expect(Home(url: URL(string: "http://192.168.1.20:8088")!, name: "x").displayAddress == "192.168.1.20:8088")
        #expect(Home(url: URL(string: "https://nas.example.com/maktaba")!, name: "x").displayAddress == "nas.example.com/maktaba")
    }
}

@Suite struct Pairings {
    @Test func theQRCodeHoldsTheHomesOwnAddressWhenItGivesOne() throws {
        let json = #"{"code":"ABCD-1234","device_code":"dc","expires_at":"2099-01-01T00:00:00.5Z","verify_path":"/pair?code=ABCD-1234","interval":2,"verify_url":"https://abc.relay.maktaba.dev/pair?code=ABCD-1234"}"#
        let p = try JSONDecoder().decode(DevicePairing.self, from: Data(json.utf8))
        #expect(p.verifyURL(home: URL(string: "http://192.168.1.20:8088")!)?.absoluteString == "https://abc.relay.maktaba.dev/pair?code=ABCD-1234")
        #expect(p.expiresAt > .now)
    }

    @Test func theApprovePageStaysUnderTheHomesBasePath() throws {
        let json = #"{"code":"ABCD-1234","device_code":"dc","expires_at":"2099-01-01T00:00:00Z","verify_path":"/pair?code=ABCD-1234","interval":2}"#
        let p = try JSONDecoder().decode(DevicePairing.self, from: Data(json.utf8))
        #expect(p.verifyURL(home: URL(string: "https://nas.example.com/maktaba")!)?.absoluteString == "https://nas.example.com/maktaba/pair?code=ABCD-1234")
    }

    @Test func readsWhatAsks() throws {
        let json = #"{"code":"ABCD-1234","client_app":"tv","platform":"tvos","device":"tv","device_label":" Living room ","expires_at":"2099-01-01T00:00:00Z"}"#
        let d = try JSONDecoder().decode(PendingDevice.self, from: Data(json.utf8))
        #expect(d == PendingDevice(code: "ABCD-1234", clientApp: "tv", platform: "tvos", device: "tv", deviceLabel: "Living room",
                                   expiresAt: Date(timeIntervalSince1970: 4070908800)))
        let bare = try JSONDecoder().decode(PendingDevice.self, from: Data(#"{"code":"ABCD-1234","device_label":""}"#.utf8))
        #expect(bare.deviceLabel == nil && bare.clientApp == nil && bare.expiresAt == nil)
    }
}

// MARK: - against a stub home

/// Answers by method, host and path, in turn (the last answer repeats); a route with no
/// answer does not connect. Remembers every request.
final class RouteStub: URLProtocol, @unchecked Sendable {
    struct Answer: Sendable {
        let status: Int
        let body: String
        var contentType = "application/json"
        /// Answer with a redirect to this address instead.
        var redirect: String?
        /// More response headers (`Retry-After`).
        var headers: [String: String] = [:]
        /// The redirect's status (302, or 307 that resends the body).
        var redirectStatus = 302
    }

    struct Seen: Sendable {
        let route: String
        let body: [String: String]?
        let client: String?
        var auth: String? = nil
    }

    nonisolated(unsafe) private static var routes: [String: [Answer]] = [:]
    nonisolated(unsafe) private static var seenList: [Seen] = []
    private static let lock = NSLock()

    static func reset() {
        lock.withLock {
            routes = [:]
            seenList = []
        }
    }

    static func on(_ route: String, _ answers: Answer...) {
        lock.withLock { routes[route, default: []].append(contentsOf: answers) }
    }

    static var seen: [Seen] { lock.withLock { seenList } }

    static func session() -> URLSession {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [RouteStub.self]
        return URLSession(configuration: c)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var data = request.httpBody
        if data == nil, let s = request.httpBodyStream {
            s.open()
            var d = Data()
            var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable {
                let n = s.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                d.append(buf, count: n)
            }
            s.close()
            data = d
        }
        let url = request.url!
        let route = "\(request.httpMethod ?? "GET") \(url.host() ?? "")\(url.path())"
        let body = data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: String] }
        let answer: Answer? = Self.lock.withLock {
            Self.seenList.append(Seen(route: route, body: body, client: request.value(forHTTPHeaderField: ClientHeader.name),
                                      auth: request.value(forHTTPHeaderField: "Authorization")))
            guard var list = Self.routes[route], !list.isEmpty else { return nil }
            let first = list.count > 1 ? list.removeFirst() : list[0]
            Self.routes[route] = list
            return first
        }
        guard let answer else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        if let to = answer.redirect, let next = URL(string: to) {
            var r = request
            r.url = next
            let moved = HTTPURLResponse(url: url, statusCode: answer.redirectStatus, httpVersion: "HTTP/1.1", headerFields: ["Location": to])!
            client?.urlProtocol(self, wasRedirectedTo: r, redirectResponse: moved)
            // a redirect not followed ends with the 3xx answer itself
            client?.urlProtocol(self, didReceive: moved, cacheStoragePolicy: .notAllowed)
            client?.urlProtocolDidFinishLoading(self)
            return
        }
        let resp = HTTPURLResponse(url: url, statusCode: answer.status, httpVersion: "HTTP/1.1",
                                   headerFields: answer.headers.merging(["Content-Type": answer.contentType]) { a, _ in a })!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(answer.body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

extension RouteStub.Answer {
    static func json(_ status: Int, _ body: String) -> Self { .init(status: status, body: body) }
    static func text(_ status: Int, _ body: String) -> Self { .init(status: status, body: body, contentType: "text/plain") }
}

/// One stub for all of these: they run one at a time.
@Suite(.serialized) struct AgainstAStubHome {
    static let discovery = #"{"id":"home-1","name":"Living room","version":"0.40.0","urls":["http://lan.test:8088","https://relay.test"],"sign_in":{"password":true,"code":true}}"#

    @Suite struct Reaching {
        let probe = HomeProbe(session: RouteStub.session(), timeout: 2)

        @Test func keepsTheFirstAddressThatAnswersInTheLinksOrder() async throws {
            RouteStub.reset()
            // lan.test does not answer (another network); the relay does
            RouteStub.on("GET relay.test/api/discovery/home", .json(200, AgainstAStubHome.discovery))
            let r = try await probe.reach([URL(string: "http://lan.test:8088")!, URL(string: "https://relay.test")!]).get()
            #expect(r.url.absoluteString == "https://relay.test")
            #expect(r.id == "home-1" && r.name == "Living room" && r.signIn == SignInMethods(password: true, code: true))
            #expect(r.addresses.map(\.absoluteString) == ["http://lan.test:8088"])
            #expect(RouteStub.seen.map(\.route) == ["GET lan.test/api/discovery/home", "GET relay.test/api/discovery/home"])
            #expect(RouteStub.seen.allSatisfy { $0.client == ClientHeader.value })
        }

        @Test func anOlderHomeIsKnownByItsVersion() async throws {
            RouteStub.reset()
            RouteStub.on("GET old.test/api/discovery/home", .text(404, "404 page not found"))
            RouteStub.on("GET old.test/api/system/version", .json(200, #"{"version":"0.36.2","schema_revision":80,"role":"home","home_instance_id":"inst-9"}"#))
            let r = try await probe.reach([URL(string: "http://old.test:8088")!]).get()
            #expect(r.id == "inst-9" && r.version == "0.36.2" && r.signIn == nil && r.name == nil)
        }

        @Test func somethingElseIsNotAHome() async {
            RouteStub.reset()
            RouteStub.on("GET router.test/api/discovery/home", .text(200, "<html>router</html>"))
            RouteStub.on("GET printer.test/api/discovery/home", .text(404, "nope"))
            RouteStub.on("GET printer.test/api/system/version", .text(404, "nope"))
            #expect(await probe.reach([URL(string: "http://router.test")!]) == .failure(.notAHome))
            #expect(await probe.reach([URL(string: "http://printer.test")!]) == .failure(.notAHome))
            if case .failure(.unreachable) = await probe.reach([URL(string: "http://gone.test")!]) {} else {
                Issue.record("an address that does not answer is unreachable")
            }
        }

        @Test func anotherHomeThanTheLinkNamesIsSkipped() async throws {
            RouteStub.reset()
            RouteStub.on("GET lan.test/api/discovery/home", .json(200, #"{"id":"neighbour","urls":[]}"#))
            RouteStub.on("GET relay.test/api/discovery/home", .json(200, AgainstAStubHome.discovery))
            let r = try await probe.reach([URL(string: "http://lan.test:8088")!, URL(string: "https://relay.test")!], expectedID: "home-1").get()
            #expect(r.url.absoluteString == "https://relay.test")
            #expect(await probe.reach([URL(string: "http://lan.test:8088")!], expectedID: "home-1") == .failure(.otherHome))
        }
    }

    @MainActor @Suite struct RememberingHomes {
        func session() throws -> (AppSession, UserDefaults) {
            let defaults = try #require(UserDefaults(suiteName: "maktabakit.test.\(UUID().uuidString)"))
            return (AppSession(app: "test.homes", defaults: defaults, probe: HomeProbe(session: RouteStub.session(), timeout: 2), asksHomes: false), defaults)
        }

        @Test func aLinkAddsTheHomeAtTheAddressThatAnswers() async throws {
            RouteStub.reset()
            RouteStub.on("GET relay.test/api/discovery/home", .json(200, AgainstAStubHome.discovery))
            let (s, defaults) = try session()
            let link = try #require(ConnectLink(string: "maktaba://connect?id=home-1&n=Home&u=http://lan.test:8088&u=https://relay.test"))
            #expect(await s.connect(to: link.urls, name: link.name, homeID: link.homeID) == nil)
            #expect(s.current?.url.absoluteString == "https://relay.test")
            #expect(s.current?.name == "Living room" && s.current?.homeID == "home-1")
            #expect(s.current?.addresses?.map(\.absoluteString) == ["http://lan.test:8088"])
            #expect(s.signInMethods == SignInMethods(password: true, code: true))
            #expect(s.phase == .signingIn)
            // remembered across starts
            #expect(AppSession(app: "test.homes", defaults: defaults, asksHomes: false).homes.map(\.homeID) == ["home-1"])
        }

        @Test func aFailedAddressSaysWhy() async throws {
            RouteStub.reset()
            let (s, _) = try session()
            let f = await s.connect(to: [URL(string: "http://gone.test")!])
            #expect(f != nil && s.connectProblem == f && s.current == nil && s.phase == .choosingHome)
        }

        /// An id is what an address says of itself: anyone on the network can announce the
        /// same one. Tokens stay with the address they were issued at.
        @Test func theSameIdAtANewAddressGetsNoTokens() throws {
            let (s, _) = try session()
            let lan = URL(string: "http://lan.test:8088")!, rogue = URL(string: "http://rogue.test:8088")!
            let real = TokenStore(app: "test.homes", home: lan)
            let other = TokenStore(app: "test.homes", home: rogue)
            other.clear()
            real.save(AuthTokens(accessToken: "issued.by.lan", refreshToken: "r", user: nil))
            defer { real.clear(); other.clear() }
            s.use(Home(url: lan, name: "Living room", homeID: "home-1"))
            s.use(Home(url: rogue, name: "Living room", homeID: "home-1"))
            #expect(other.access == nil && other.refresh == nil, "nothing moved to the new address")
            #expect(real.access == "issued.by.lan", "the old address keeps its own")
            #expect(s.homes.map(\.url).sorted { $0.absoluteString < $1.absoluteString } == [lan, rogue])
            #expect(s.phase == .signingIn, "signed in afresh at the new address")
        }

        /// A home saved under a name that no longer resolves (its `.local` name changed) is
        /// asked at its other addresses at launch, and its entry moves to the one that answers.
        @Test func aSavedHomeAtADeadAddressConnectsThroughItsOther() async throws {
            RouteStub.reset()
            RouteStub.on("GET lan.test/api/discovery/home", .json(200, AgainstAStubHome.discovery))
            let dead = URL(string: "http://oldname.test:8088")!, lan = URL(string: "http://lan.test:8088")!
            let defaults = try saved(Home(url: dead, name: "Grandma's", homeID: "home-1", addresses: [lan]), renamed: true)
            let old = TokenStore(app: "test.homes.moved", home: dead), new = TokenStore(app: "test.homes.moved", home: lan)
            new.clear()
            old.save(AuthTokens(accessToken: "issued.by.oldname", refreshToken: "r", user: nil))
            defer { old.clear(); new.clear() }
            let s = launched("test.homes.moved", defaults)
            for _ in 0..<100 where s.current == nil { try await Task.sleep(for: .milliseconds(20)) }
            #expect(s.current?.url == lan, "reached at its other address")
            #expect(s.homes.count == 1, "the same entry, moved")
            #expect(s.homes.first?.addresses?.contains(dead) == true, "the old address among its others")
            #expect(s.homes.first?.name == "Grandma's" && s.homes.first?.renamed == true)
            #expect(defaults.url(forKey: "home") == lan, "the next launch starts there")
            // tokens stay with the address that issued them: signed in afresh at the new one
            #expect(new.access == nil && old.access == "issued.by.oldname")
            #expect(s.phase == .signingIn)
        }

        /// A saved home that answers at none of its addresses, while the app browses: "Looking
        /// for your home on this network…", then asked where its id is announced, the entry
        /// moved there. Another home's id moves nothing.
        @Test func aSavedHomeAtADeadAddressIsFoundAgainThroughDiscovery() async throws {
            RouteStub.reset()
            let dead = URL(string: "http://oldname.test:8088")!, lan = URL(string: "http://lan.test:8088")!
            let defaults = try saved(Home(url: dead, name: "oldname.test", homeID: "home-1"))
            let network = FakeDiscovery()
            let s = launched("test.homes.browsed", defaults, discovery: network)
            s.discover(true)
            for _ in 0..<100 where s.progress?.step != .lookingNearby { try await Task.sleep(for: .milliseconds(20)) }
            #expect(s.progress?.step == .lookingNearby && s.connectProblem == nil, "looked for on the network, no failure yet")
            RouteStub.on("GET lan.test/api/discovery/home", .json(200, AgainstAStubHome.discovery))
            RouteStub.on("GET other.test/api/discovery/home", .json(200, #"{"id":"neighbour","urls":[]}"#))
            network.announce([HomeBrowser.Found(name: "Neighbour", url: URL(string: "http://other.test:8088")!, homeID: "neighbour")])
            try await Task.sleep(for: .milliseconds(100))
            #expect(s.current == nil && !RouteStub.seen.contains { $0.route.contains("other.test") }, "another home is not asked")
            // the announcement's id as the TXT record says it (lower-cased)
            network.announce([HomeBrowser.Found(name: "Studio", url: lan, homeID: "HOME-1".lowercased())])
            for _ in 0..<100 where s.current == nil { try await Task.sleep(for: .milliseconds(20)) }
            #expect(s.current?.url == lan)
            #expect(s.homes.map(\.url) == [lan] && s.homes.first?.addresses?.contains(dead) == true)
            #expect(s.homes.first?.name == "Living room", "the placeholder name gives way to the home's")
            #expect(s.connectProblem == nil && s.phase == .signingIn)
        }

        /// Not announced while it is looked for: why, and Retry. Announced later: asked there.
        @Test func aSavedHomeNotAnnouncedSaysWhyThenIsAskedWhenItIs() async throws {
            RouteStub.reset()
            let dead = URL(string: "http://oldname.test:8088")!, lan = URL(string: "http://lan.test:8088")!
            let defaults = try saved(Home(url: dead, name: "Living room", homeID: "home-1"))
            let network = FakeDiscovery()
            // the look's time passes at once
            let s = launched("test.homes.unannounced", defaults, discovery: network, clock: ConnectClock(now: { Date() }, sleep: { _ in }))
            s.discover(true)
            for _ in 0..<100 where s.connectProblem == nil { try await Task.sleep(for: .milliseconds(20)) }
            #expect(s.progress == ConnectProgress(.failed(reason: .unreachable, tried: ["http://oldname.test:8088"])))
            RouteStub.on("GET lan.test/api/discovery/home", .json(200, AgainstAStubHome.discovery))
            network.announce([HomeBrowser.Found(name: "Studio", url: lan, homeID: "home-1")])
            for _ in 0..<100 where s.current == nil { try await Task.sleep(for: .milliseconds(20)) }
            #expect(s.current?.url == lan && s.homes.count == 1)
        }

        /// Discovery runs while the app is in front: stopped in the background, browsed afresh
        /// when it comes back, and what it hears then still reaches the saved homes.
        @Test func discoveryStartsAgainWhenTheAppComesBack() throws {
            let lan = URL(string: "http://lan.test:8088")!, studio = URL(string: "http://studio.test:8088")!
            let network = FakeDiscovery()
            let s = launched("test.homes.front", try saved(Home(url: lan, name: "Living room", homeID: "home-1")), discovery: network)
            s.switchHome()
            s.discover(true)
            #expect(network.starts == 1)
            network.announce([HomeBrowser.Found(name: "Studio", url: studio, homeID: "home-1")])
            #expect(s.nearby.map(\.url) == [studio])
            s.discover(false)
            #expect(network.stops == 1 && s.nearby.isEmpty && !network.browsing, "stopped in the background")
            s.discover(true)
            #expect(network.starts == 2 && network.browsing, "browsed afresh in front again")
            network.announce([HomeBrowser.Found(name: "Studio", url: studio, homeID: "home-1")])
            #expect(s.nearby.map(\.url) == [studio])
            // a saved home that answers where it is stays there; the announced address is kept among its others
            #expect(s.homes.map(\.url) == [lan] && s.homes.first?.addresses == [studio])
        }

        private func saved(_ home: Home, renamed: Bool = false) throws -> UserDefaults {
            let defaults = try #require(UserDefaults(suiteName: "maktabakit.test.\(UUID().uuidString)"))
            var h = home
            if renamed { h.renamed = true }
            defaults.set(try JSONEncoder().encode([h]), forKey: "homes")
            defaults.set(h.url, forKey: "home")
            return defaults
        }

        private func launched(_ app: String, _ defaults: UserDefaults, discovery: FakeDiscovery = FakeDiscovery(),
                              clock: ConnectClock = .system) -> AppSession {
            AppSession(app: app, defaults: defaults, probe: HomeProbe(session: RouteStub.session(), timeout: 2), asksHomes: false,
                       clock: clock, discovery: discovery)
        }

        @Test func thePersonsNameStays() throws {
            let (s, _) = try session()
            s.use(Home(url: URL(string: "http://lan.test:8088")!, name: "lan.test", homeID: "home-1"))
            s.rename(s.current!, to: "Grandma's")
            // the home's own name does not replace the person's
            s.named("Living room")
            #expect(s.current?.name == "Grandma's" && s.current?.renamed == true)
            // an empty name gives the home's back
            s.rename(s.current!, to: "  ")
            #expect(s.current?.renamed == nil)
            s.named("Living room")
            #expect(s.current?.name == "Living room")
        }

        /// A link opened from outside asks first: nothing is fetched or switched before yes.
        @Test func aLinkFromOutsideNeedsConfirmation() async throws {
            RouteStub.reset()
            RouteStub.on("GET relay.test/api/discovery/home", .json(200, AgainstAStubHome.discovery))
            let (s, _) = try session()
            #expect(s.open(URL(string: "maktaba://connect?id=home-1&u=https://relay.test")!))
            #expect(s.pendingLink?.urls.map(\.absoluteString) == ["https://relay.test"])
            try await Task.sleep(for: .milliseconds(200))
            #expect(RouteStub.seen.isEmpty && s.current == nil && s.phase == .choosingHome)
            s.dismissLink()
            #expect(s.pendingLink == nil && s.current == nil)
            // yes: then it connects
            s.open(URL(string: "maktaba://connect?id=home-1&u=https://relay.test")!)
            s.confirmLink()
            for _ in 0..<50 where s.current == nil { try await Task.sleep(for: .milliseconds(20)) }
            #expect(s.current?.url.absoluteString == "https://relay.test")
            #expect(!s.open(URL(string: "https://example.com")!))
        }

        @Test func theLastUsedComesFirst() throws {
            let (s, _) = try session()
            let a = URL(string: "http://a.test")!, b = URL(string: "http://b.test")!
            s.use(Home(url: a, name: "A"))
            s.use(Home(url: b, name: "B"))
            #expect(s.recentHomes.map(\.name) == ["B", "A"])
            s.use(s.homes.first { $0.url == a }!)
            #expect(s.recentHomes.map(\.name) == ["A", "B"])
            s.forget(s.homes.first { $0.url == a }!)
            #expect(s.homes.map(\.name) == ["B"] && s.phase == .choosingHome)
        }
    }

    @MainActor @Suite struct SigningInByCode {
        let home = URL(string: "http://code.test:8088")!
        static let pairing1 = #"{"code":"AAAA-1111","device_code":"dc1","expires_at":"2099-01-01T00:00:00Z","verify_path":"/pair?code=AAAA-1111","interval":2}"#
        static let pairing2 = #"{"code":"BBBB-2222","device_code":"dc2","expires_at":"2099-01-01T00:00:00Z","verify_path":"/pair?code=BBBB-2222","interval":2}"#

        func flow(_ tokens: TokenStore, finished: @escaping @MainActor () -> Void) -> CodeSignIn {
            let client = APIClient(home: home, tokens: tokens, session: RouteStub.session())
            return CodeSignIn(client: client, label: "Mahmoud's iPhone", finish: { finished() }, sleep: { _ in })
        }

        @Test func showsTheCodeAndPollsUntilApproved() async throws {
            RouteStub.reset()
            RouteStub.on("POST code.test/api/pairing/device", .json(201, Self.pairing1))
            RouteStub.on("POST code.test/api/pairing/device/token",
                         .json(202, #"{"status":"pending"}"#), .json(202, #"{"status":"pending"}"#),
                         .json(200, #"{"access_token":"a.b.c","refresh_token":"r"}"#))
            let tokens = TokenStore(app: "test.code.ok", home: home)
            tokens.clear()
            defer { tokens.clear() }
            var finished = 0
            let f = flow(tokens) { finished += 1 }
            await f.run()
            #expect(f.status == .approved && finished == 1)
            #expect(tokens.access == "a.b.c")
            let routes = RouteStub.seen.map(\.route)
            #expect(routes == ["POST code.test/api/pairing/device"] + Array(repeating: "POST code.test/api/pairing/device/token", count: 3))
            // the device names itself to the person approving it
            #expect(RouteStub.seen.first?.body == ["device_label": "Mahmoud's iPhone"])
            #expect(RouteStub.seen.last?.body == ["device_code": "dc1", "device_label": "Mahmoud's iPhone"])
        }

        @Test func anExpiredCodeIsReplaced() async throws {
            RouteStub.reset()
            RouteStub.on("POST code.test/api/pairing/device", .json(201, Self.pairing1), .json(201, Self.pairing2))
            RouteStub.on("POST code.test/api/pairing/device/token",
                         .json(410, #"{"title":"code expired"}"#), .json(200, #"{"access_token":"x.y.z","refresh_token":"r"}"#))
            let tokens = TokenStore(app: "test.code.expired", home: home)
            tokens.clear()
            defer { tokens.clear() }
            let f = flow(tokens) {}
            await f.run()
            #expect(f.status == .approved)
            #expect(RouteStub.seen.compactMap { $0.body?["device_code"] } == ["dc1", "dc2"])
        }

        @Test func aHomeWithoutCodesSaysSo() async throws {
            RouteStub.reset()
            RouteStub.on("POST code.test/api/pairing/device", .text(404, "404 page not found"))
            let f = flow(TokenStore(app: "test.code.none", home: home)) {}
            await f.run()
            #expect(f.status == .unsupported)
        }
    }

    /// A home's token goes only to the origin it was issued by, redirects included.
    @Suite struct Credentials {
        let home = URL(string: "http://tokens.test:8088")!

        @Test func aRedirectToAnotherOriginDropsTheToken() async throws {
            RouteStub.reset()
            RouteStub.on("GET tokens.test/api/libraries", .init(status: 302, body: "", redirect: "http://evil.test/api/libraries"))
            RouteStub.on("GET evil.test/api/libraries", .json(200, #"{"items":[],"next":null}"#))
            RouteStub.on("GET tokens.test/api/videos/a", .init(status: 302, body: "", redirect: "http://tokens.test:8088/api/videos/b"))
            RouteStub.on("GET tokens.test/api/videos/b", .json(200, #"{"id":"b","library_id":"l","filename":"b.mp4"}"#))
            let tokens = TokenStore(app: "test.redirect", home: home)
            tokens.save(AuthTokens(accessToken: "secret.access.token", refreshToken: "r", user: nil))
            defer { tokens.clear() }
            let c = APIClient(home: home, tokens: tokens, session: RouteStub.session())
            _ = try await c.libraries()
            let seen = RouteStub.seen
            #expect(seen.first { $0.route == "GET tokens.test/api/libraries" }?.auth == "Bearer secret.access.token")
            let evil = try #require(seen.first { $0.route == "GET evil.test/api/libraries" }, "the redirect was followed")
            #expect(evil.auth == nil, "no token to another origin")
            // the same origin keeps it
            _ = try await c.video("a")
            #expect(RouteStub.seen.first { $0.route == "GET tokens.test/api/videos/b" }?.auth == "Bearer secret.access.token")
        }

        @Test func theRuleItself() {
            var r = URLRequest(url: URL(string: "http://h.test:8088/api/x")!)
            r.setValue("Bearer t", forHTTPHeaderField: "Authorization")
            for other in ["https://h.test:8088/api/x", "http://h.test:8089/api/x", "http://evil.test:8088/api/x"] {
                var next = r
                next.url = URL(string: other)!
                #expect(SameOriginRedirects.redirect(from: r, to: next).value(forHTTPHeaderField: "Authorization") == nil, "\(other)")
            }
            var same = r
            same.url = URL(string: "http://H.TEST:8088/api/y")!
            #expect(SameOriginRedirects.redirect(from: r, to: same).value(forHTTPHeaderField: "Authorization") == "Bearer t")
            #expect(SameOriginRedirects.sameOrigin(URL(string: "https://a.test")!, URL(string: "https://a.test:443/x")!))
        }
    }

    @Suite struct Approving {
        let home = URL(string: "http://approve.test:8088")!

        func client() -> APIClient {
            APIClient(home: home, tokens: TokenStore(app: "test.approve", home: home), session: RouteStub.session())
        }

        @Test func looksUpWhatAsksThenApprovesIt() async throws {
            RouteStub.reset()
            RouteStub.on("GET approve.test/api/pairing/device/ABCD1234",
                         .json(200, #"{"code":"ABCD-1234","client_app":"tv","platform":"tvos","device":"tv","device_label":"Living room","expires_at":"2099-01-01T00:00:00Z"}"#))
            RouteStub.on("POST approve.test/api/pairing/approve", .json(200, #"{"status":"approved"}"#))
            let c = client()
            guard case let .found(d) = try await c.pendingDevice(code: "ABCD-1234") else {
                Issue.record("found")
                return
            }
            #expect(d.deviceLabel == "Living room" && d.clientApp == "tv")
            try await c.approveDevice(code: "ABCD-1234")
            #expect(RouteStub.seen.last?.route == "POST approve.test/api/pairing/approve")
            #expect(RouteStub.seen.last?.body == ["code": "ABCD1234"])
        }

        @Test func aCodeNobodyShowsAndAnOlderHome() async throws {
            RouteStub.reset()
            RouteStub.on("GET approve.test/api/pairing/device/ZZZZ9999", .init(status: 404, body: #"{"title":"not found","status":404}"#, contentType: "application/problem+json"))
            RouteStub.on("GET approve.test/api/pairing/device/AAAA1111", .text(404, "404 page not found"))
            RouteStub.on("GET approve.test/api/pairing/device/BBBB2222", .text(405, ""))
            let c = client()
            #expect(try await c.pendingDevice(code: "ZZZZ-9999") == .notFound)
            #expect(try await c.pendingDevice(code: "AAAA-1111") == .unknownOnThisHome)
            #expect(try await c.pendingDevice(code: "BBBB-2222") == .unknownOnThisHome)
        }

        @Test func aSecondApprovalIsRefused() async {
            RouteStub.reset()
            RouteStub.on("POST approve.test/api/pairing/approve", .json(409, #"{"title":"code already approved"}"#))
            await #expect(throws: APIError.self) { try await client().approveDevice(code: "ABCD-1234") }
        }
    }
}

@MainActor @Suite struct CameraPermission {
    @Test func theCameraIsAskedForButNotRequired() async throws {
        let suite = "maktabakit.test.\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        defaults.set(true, forKey: "permissions.walkthroughSeen")
        defaults.set(true, forKey: "permissions.localNetwork.asked")
        // the scripted results give the verdict at once; the timeout only has to outlast a
        // busy runner (500 ms did not on CI, 2026-10-31)
        let quick = PermissionsModel.Timing(check: LocalNetworkVerdict(grace: .milliseconds(50), timeout: .seconds(10)), request: .seconds(10))
        let m = PermissionsModel(kinds: [.localNetwork, .camera], defaults: defaults,
                                 probe: ScriptedProbe([.ready, .results(1)]), camera: FakeCamera(.denied), timing: quick)
        await m.refresh()
        #expect(m.state(of: .camera) == .denied && m.state(of: .localNetwork) == .granted)
        // a refused camera does not bring the walkthrough back: the camera is optional
        #expect(!m.requiredMissing && !m.isPresented)
        #expect(!PermissionKind.camera.isRequired)
    }

    @Test func itsWordsInBothLanguages() {
        for lang in [PermissionsStrings.Language.en, .ar] {
            let w = PermissionsStrings(lang)
            #expect(!w.why(.camera).isEmpty && w.why(.camera) != w.why(.localNetwork))
            #expect(w.deniedHow(.camera, appName: "Maktaba").contains("Maktaba"))
        }
    }
}

@Suite struct ConnectWords {
    @Test func everyWordIsInBothLanguages() {
        for key in ConnectStrings.Key.allCases {
            #expect(ConnectStrings.en[key] != nil, "en \(key)")
            #expect(ConnectStrings.ar[key] != nil, "ar \(key)")
        }
        #expect(ConnectStrings(.ar).isRightToLeft && !ConnectStrings(.en).isRightToLeft)
        #expect(ConnectStrings(.en).text(.unreachable, "timed out") == "Could not reach the home: timed out")
    }
}

/// The homes on the network as a test announces them.
@MainActor final class FakeDiscovery: HomeDiscovering {
    private(set) var starts = 0, stops = 0
    private var found: (@MainActor ([HomeBrowser.Found]) -> Void)?
    var browsing: Bool { found != nil }

    func start(_ found: @escaping @MainActor ([HomeBrowser.Found]) -> Void) {
        starts += 1
        self.found = found
    }

    func stop() {
        stops += 1
        found = nil
    }

    func announce(_ list: [HomeBrowser.Found]) { found?(list) }
}
