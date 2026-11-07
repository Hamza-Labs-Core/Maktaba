import Foundation
import Security
import Testing
@testable import MaktabaKit

/// Story 50.15 §3 (DNS rebinding): a home reached by its LAN address is checked against its
/// own name. A test CA and a leaf naming only the home's LAN name (made once with openssl; the leaf
/// is valid 90 days from 2026-11-07, as Apple caps a TLS certificate's life, so the check runs at a
/// fixed date inside that).
@Suite struct PinningTests {
    static let hid = "0123456789abcdef0123456789abcdef"
    static let name = "127-0-0-1.\(hid).direct-a.example"
    static let caDER = Data(base64Encoded: "MIIBeDCCAR6gAwIBAgIUQay+PUilci6kE7hRzuhwwbaplp8wCgYIKoZIzj0EAwIwGjEYMBYGA1UEAwwPUGlubmluZyBUZXN0IENBMB4XDTI2MTEwNzE5MjMzN1oXDTM2MTEwNDE5MjMzN1owGjEYMBYGA1UEAwwPUGlubmluZyBUZXN0IENBMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEegZLqbY0lmr0VK/FqPhwimoGC9ELZRIGX0CUmhelYeGfTYciBDXd0GKYUPR6fcq3d8YYTib15OCf2ix7F/cLZaNCMEAwHQYDVR0OBBYEFC5idZFzSia/H/OLhScqGzuJYGnzMA8GA1UdEwEB/wQFMAMBAf8wDgYDVR0PAQH/BAQDAgIEMAoGCCqGSM49BAMCA0gAMEUCIF1ny216ZzzHsJGfry7bUpPfV3/Vj1aNG30xjTVhn/cIAiEApuz/kLRft3RQM2eus0AefHNQtou7eaBUUmEg4hxMYp8=")!
    static let leafDER = Data(base64Encoded: "MIIB0DCCAXegAwIBAgIUPNTZnrJG1DL3b1F8fHpGvoGcWhAwCgYIKoZIzj0EAwIwGjEYMBYGA1UEAwwPUGlubmluZyBUZXN0IENBMB4XDTI2MTEwNzE5MjQ0OVoXDTI3MDIwNTE5MjQ0OVowRjFEMEIGA1UEAww7MTI3LTAtMC0xLjAxMjM0NTY3ODlhYmNkZWYwMTIzNDU2Nzg5YWJjZGVmLmRpcmVjdC1hLmV4YW1wbGUwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAAQwRgWmKM/R1EE8GQzY9oev+JbhdAUlTBF65GU2BoSUliXTOmIOytLWRcrIhOX3aKya7FBn2VJpLo2Kqt+zV07Fo28wbTBGBgNVHREEPzA9gjsxMjctMC0wLTEuMDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWYuZGlyZWN0LWEuZXhhbXBsZTATBgNVHSUEDDAKBggrBgEFBQcDATAOBgNVHQ8BAf8EBAMCB4AwCgYIKoZIzj0EAwIDRwAwRAIgTxfev89aTAhmu4O9tGznTskm8VspwvbnPhLczWMfyBgCIEFpHOHCXr0qtGPiQPcN+2TmCja85Ycp3irGNtyKGmnp")!

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
        names.pin(URL(string: "https://127.0.0.1:8444")!, name: Self.name)
        #expect(p.check(Self.trust(), host: "127.0.0.1", port: 8444) == true)
        #expect(p.check(Self.trust(), host: "127.0.0.1", port: 8445) == nil, "another port isn't pinned")
    }

    @Test func aPinToAnotherHomeIsRefused() {
        let names = PinnedNames()
        names.pin(URL(string: "https://127.0.0.1:8444")!, name: "127-0-0-1.ffffffffffffffffffffffffffffffff.direct-a.example")
        #expect(PinnedTrust(names: names).check(Self.trust(), host: "127.0.0.1", port: 8444) == false)
    }

    @Test func onlyHTTPSAddressesArePinned() {
        let names = PinnedNames()
        names.pin(URL(string: "http://192.168.1.20:8444")!, name: Self.name)
        names.pin(URL(string: "https://home.example:8444")!, name: Self.name)
        names.pin(URL(string: "https://192.168.1.20:8444")!, name: " ")
        #expect(names.name(host: "192.168.1.20", port: 8444) == nil)
        #expect(names.name(host: "home.example", port: 8444) == nil)
        names.pin(URL(string: "https://[fd00::7]:8444")!, name: "FD00--7.\(Self.hid).direct-a.example.")
        #expect(names.name(host: "fd00::7", port: 8444) == "fd00--7.\(Self.hid).direct-a.example")
    }

    @Test func aNameForAnotherAddressOrNoHomeIsNotPinned() {
        let names = PinnedNames()
        let url = URL(string: "https://192.168.1.20:8444")!
        for bad in ["192-168-1-21.\(Self.hid).direct-a.example", "bank.example", "192-168-1-20.example",
                    "192-168-1-20..direct-a.example", "localhost.\(Self.hid).direct-a.example"] {
            names.pin(url, name: bad)
        }
        names.pin(URL(string: "https://[fd00::7]:8444")!, name: "fd00--8.\(Self.hid).direct-a.example")
        #expect(names.name(host: "192.168.1.20", port: 8444) == nil)
        #expect(names.name(host: "fd00::7", port: 8444) == nil)
        names.pin(url, name: "192-168-1-20.\(Self.hid).direct-a.example")
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
