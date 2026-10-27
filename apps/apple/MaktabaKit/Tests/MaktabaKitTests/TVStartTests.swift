import Foundation
import Testing
@testable import MaktabaKit

// Epic 42 story 42.10: what the Apple TV's home picker and sign-in decide.

@Suite struct TVStartTests {
    @Test func offersTheCodeFirst() {
        #expect(TVStart.offered(nil) == [.code, .password])
        #expect(TVStart.offered(SignInMethods(password: true, code: true)) == [.code, .password])
        #expect(TVStart.offered(SignInMethods(password: true, code: false)) == [.password])
        #expect(TVStart.offered(SignInMethods(password: false, code: true)) == [.code])
        // a home that allows neither still gets the password (it will say why)
        #expect(TVStart.offered(SignInMethods(password: false, code: false)) == [.password])
    }

    @Test func listsANearbyHomeOnceByItsExactAddress() throws {
        let kitchen = try #require(URL(string: "http://kitchen.local:8088"))
        let moved = try #require(URL(string: "http://192.168.1.9:8088"))
        let known = [Home(url: kitchen, name: "Kitchen", homeID: "0a1b")]
        let found = [
            HomeBrowser.Found(name: "Kitchen", url: kitchen, homeID: "0a1b"),
            // the same id at another address: not the known entry (its tokens stay where they are)
            HomeBrowser.Found(name: "Kitchen", url: moved, homeID: "0a1b"),
        ]
        #expect(TVStart.nearby(found, known: known).map(\.url) == [moved])
        #expect(TVStart.nearby(found, known: []).count == 2)
    }

    @Test func readsWhatWasTyped() throws {
        #expect(TVStart.typed("  ") == .invalid)
        #expect(TVStart.typed("not an address at all") == .invalid)
        guard case let .addresses(urls) = TVStart.typed("192.168.1.20:8088") else {
            Issue.record("an address")
            return
        }
        #expect(urls.first?.absoluteString == "http://192.168.1.20:8088")
        guard case let .link(link) = TVStart.typed("maktaba://connect?id=h&n=Den&u=http%3A%2F%2F10.0.0.2%3A8088") else {
            Issue.record("a link")
            return
        }
        #expect(link.name == "Den")
        #expect(link.urls.map(\.absoluteString) == ["http://10.0.0.2:8088"])
    }

    @Test func everyWordInBothLanguages() {
        for key in TVConnectStrings.Key.allCases {
            #expect(TVConnectStrings.en[key] != nil, "en \(key)")
            #expect(TVConnectStrings.ar[key] != nil, "ar \(key)")
        }
        #expect(TVConnectStrings.forPreferredLanguages(["ar-SA", "en"]).language == .ar)
        #expect(TVConnectStrings.forPreferredLanguages(["en-US"]).language == .en)
        #expect(TVConnectStrings(.en).text(.codeTitle, "Den") == "Sign in to Den")
        #expect(TVConnectStrings(.ar).text(.codeTitle, "Den") == "تسجيل الدخول إلى Den")
    }
}
