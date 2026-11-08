import Foundation
import Security
import Testing
@testable import MaktabaKit

/// Story 50.15 §3 (DNS rebinding): a home reached by its LAN address is checked against its
/// own name. A test CA and a leaf naming only the home's LAN name, for server ID "s" (made once
/// with openssl; the leaf is valid 90 days from 2026-11-08, as Apple caps a TLS certificate's
/// life, so the check runs at a fixed date inside that). A pin to another home's name, or an
/// address two homes pin, is refused.
@Suite struct PinningTests {
    static let sid = "s"
    static let hid = PinnedNames.homeID(sid)
    static let name = "127-0-0-1.\(hid).direct-a.example"
    static let caDER = Data(base64Encoded: "MIIBeDCCAR6gAwIBAgIUB8Ff5t4NZ99vroyL79TZLJqt0oUwCgYIKoZIzj0EAwIwGjEYMBYGA1UEAwwPUGlubmluZyBUZXN0IENBMB4XDTI2MTEwODAwNTM1NFoXDTM2MTEwNTAwNTM1NFowGjEYMBYGA1UEAwwPUGlubmluZyBUZXN0IENBMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE/ftaU9b45YV20yDdW53Sdpg95j8fu0VEQlox9KljdCsuQJ8MY5+DOqUwf9oWopYy84VsuSrq4E4olVQ8rzYM/aNCMEAwHQYDVR0OBBYEFK7Iu9UjuKLUfyOoHNcDVYwGzkBBMA8GA1UdEwEB/wQFMAMBAf8wDgYDVR0PAQH/BAQDAgIEMAoGCCqGSM49BAMCA0gAMEUCIH85OgXZRw/Sr3AbBXsmH/wPTG6F5leWYGw42i71TEh/AiEA3zN//3fTfqgky1fVxT8a4cUAqqMYYPpimd21bHwzfPM=")!
    static let leafDER = Data(base64Encoded: "MIIB0TCCAXegAwIBAgIUE/8e6A1tg1+RaPMIq+MMnssP+fgwCgYIKoZIzj0EAwIwGjEYMBYGA1UEAwwPUGlubmluZyBUZXN0IENBMB4XDTI2MTEwODAwNTM1NFoXDTI3MDIwNjAwNTM1NFowRjFEMEIGA1UEAww7MTI3LTAtMC0xLjE4Yzc4YmEyMzhmZjQyOGUyZGRiZGZmMjA0NzdlN2ViLmRpcmVjdC1hLmV4YW1wbGUwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAARBpev9Z4j27hC9tW0JbsjpBDrYUTm/x27zbQ87pIPhEF2saAUBFMlZf9bOnrxRKOoeghrOfoUKOnnYw5BMxesyo28wbTBGBgNVHREEPzA9gjsxMjctMC0wLTEuMThjNzhiYTIzOGZmNDI4ZTJkZGJkZmYyMDQ3N2U3ZWIuZGlyZWN0LWEuZXhhbXBsZTATBgNVHSUEDDAKBggrBgEFBQcDATAOBgNVHQ8BAf8EBAMCB4AwCgYIKoZIzj0EAwIDSAAwRQIhANWlmASFU37DjK2Siet0pkxonro7oUg03pgjmk/KanOiAiARlhpRnkuzTUgTzjiIbqPhLMaZBfmKWOSq1yYGFQdwVg==")!

    /// A trust for the leaf with only the test CA as anchor, at a date the chain is valid.
    static func trust() -> SecTrust {
        let leaf = SecCertificateCreateWithData(nil, leafDER as CFData)!
        let ca = SecCertificateCreateWithData(nil, caDER as CFData)!
        var t: SecTrust?
        SecTrustCreateWithCertificates([leaf, ca] as CFArray, SecPolicyCreateBasicX509(), &t)
        SecTrustSetAnchorCertificates(t!, [ca] as CFArray)
        SecTrustSetAnchorCertificatesOnly(t!, true)
        SecTrustSetVerifyDate(t!, Date(timeIntervalSince1970: 1_796_000_000) as CFDate) // 2026-11-30
        return t!
    }

    @Test func anAddressIsCheckedAgainstItsPinnedName() {
        let names = PinnedNames()
        let p = PinnedTrust(names: names)
        #expect(p.check(Self.trust(), host: "127.0.0.1", port: 8444) == nil, "unpinned: default handling")
        names.pinHome(Self.sid, [(URL(string: "https://127.0.0.1:8444")!, Self.name)])
        #expect(p.check(Self.trust(), host: "127.0.0.1", port: 8444) == true)
        #expect(p.check(Self.trust(), host: "127.0.0.1", port: 8445) == nil, "another port isn't pinned")
    }

    @Test func aPinToAnotherHomeIsRefused() {
        let names = PinnedNames()
        names.pinHome(Self.sid, [(URL(string: "https://127.0.0.1:8444")!, "127-0-0-1.ffffffffffffffffffffffffffffffff.direct-a.example")])
        // not pinned at all: the system's own check, by IP, refuses the home's certificate
        #expect(names.name(host: "127.0.0.1", port: 8444) == nil)
        #expect(PinnedTrust(names: names).check(Self.trust(), host: "127.0.0.1", port: 8444) == nil)
        // this home's name, listed under another home: not pinned either
        names.pinHome("another", [(URL(string: "https://127.0.0.1:8444")!, Self.name)])
        #expect(names.name(host: "127.0.0.1", port: 8444) == nil)
    }

    @Test func theHomeIDIsTheClouds() {
        // direct.HomeID("s") in the cloud (cloud/internal/direct/names.go)
        #expect(Self.hid == "18c78ba238ff428e2ddbdff20477e7eb")
    }

    @Test func anAddressTwoHomesPinIsPinnedToNeither() {
        let names = PinnedNames()
        let p = PinnedTrust(names: names)
        let url = URL(string: "https://127.0.0.1:8444")!
        names.pinHome(Self.sid, [(url, Self.name)])
        #expect(p.check(Self.trust(), host: "127.0.0.1", port: 8444) == true)
        // a second home on the same private address and port (its own LAN): ambiguous, so the
        // first home's certificate is no longer taken there by name; the app moves on
        names.pinHome("x", [(url, "127-0-0-1.\(PinnedNames.homeID("x")).direct-a.example")])
        #expect(p.check(Self.trust(), host: "127.0.0.1", port: 8444) == nil)
        // that home's list no longer has the address: the pin is the first home's again
        names.pinHome("x", [])
        #expect(p.check(Self.trust(), host: "127.0.0.1", port: 8444) == true)
    }

    @Test func aHomesNewListReplacesItsPins() {
        let names = PinnedNames()
        names.pinHome(Self.sid, [(URL(string: "https://192.168.1.20:8444")!, "192-168-1-20.\(Self.hid).direct-a.example")])
        names.pinHome(Self.sid, [(URL(string: "https://192.168.1.21:8444")!, "192-168-1-21.\(Self.hid).direct-a.example")])
        #expect(names.name(host: "192.168.1.20", port: 8444) == nil)
        #expect(names.name(host: "192.168.1.21", port: 8444) == "192-168-1-21.\(Self.hid).direct-a.example")
    }

    @Test func onlyHTTPSAddressesArePinned() {
        let names = PinnedNames()
        names.pinHome(Self.sid, [(URL(string: "http://192.168.1.20:8444")!, Self.name)])
        names.pinHome(Self.sid, [(URL(string: "https://home.example:8444")!, Self.name)])
        names.pinHome(Self.sid, [(URL(string: "https://192.168.1.20:8444")!, " ")])
        #expect(names.name(host: "192.168.1.20", port: 8444) == nil)
        #expect(names.name(host: "home.example", port: 8444) == nil)
        names.pinHome(Self.sid, [(URL(string: "https://[fd00::7]:8444")!, "FD00--7.\(Self.hid).direct-a.example.")])
        #expect(names.name(host: "fd00::7", port: 8444) == "fd00--7.\(Self.hid).direct-a.example")
    }

    @Test func aNameForAnotherAddressOrNoHomeIsNotPinned() {
        let names = PinnedNames()
        let url = URL(string: "https://192.168.1.20:8444")!
        for bad in ["192-168-1-21.\(Self.hid).direct-a.example", "bank.example", "192-168-1-20.example",
                    "192-168-1-20..direct-a.example", "localhost.\(Self.hid).direct-a.example"] {
            names.pinHome(Self.sid, [(url, bad)])
        }
        names.pinHome(Self.sid, [(URL(string: "https://[fd00::7]:8444")!, "fd00--8.\(Self.hid).direct-a.example")])
        #expect(names.name(host: "192.168.1.20", port: 8444) == nil)
        #expect(names.name(host: "fd00::7", port: 8444) == nil)
        names.pinHome(Self.sid, [(url, "192-168-1-20.\(Self.hid).direct-a.example")])
        #expect(names.name(host: "192.168.1.20", port: 8444) == "192-168-1-20.\(Self.hid).direct-a.example")
    }

    @Test func theCloudListPinsItsLocalIPEntriesAfterTheLANName() throws {
        let json = """
        {"server_id":"s","relay_url":"https://x.relay.test","connections":[
          {"url":"https://x.relay.test","kind":"relay"},
          {"url":"https://192.168.1.21:8444","kind":"local_ip","tls_name":"192-168-1-21.\(Self.hid).direct-a.example"},
          {"url":"https://192-168-1-21.\(Self.hid).direct-a.example:8444","kind":"local"},
          {"url":"https://x","kind":"future_kind"}]}
        """
        let home = try JSONDecoder().decode(CloudHome.self, from: Data(json.utf8))
        #expect(home.connections.map(\.kind) == ["local", "local_ip", "relay"])
        #expect(PinnedNames.shared.name(host: "192.168.1.21", port: 8444) == "192-168-1-21.\(Self.hid).direct-a.example")
        // it survives a save and a load (the saved list for a cloud outage)
        let again = try JSONDecoder().decode(CloudHome.self, from: JSONEncoder().encode(home))
        #expect(again.connections.first { $0.kind == "local_ip" }?.tlsName == "192-168-1-21.\(Self.hid).direct-a.example")
    }
}
