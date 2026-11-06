import Foundation
import Testing
@testable import MaktabaKit

/// Epic 53 story 53.08 T01: one id per action, a UUIDv7, on every request.
@Suite struct RequestIDTests {
    @Test func aUUIDv7() {
        let id = RequestID.v7(Date(timeIntervalSince1970: 1_730_000_000))
        #expect(id.range(of: #"^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"#, options: .regularExpression) != nil)
        #expect(RequestID.short(id).count == 8)
    }

    @Test func callsOfOneActionShareTheID() {
        let t = Date(timeIntervalSince1970: 2_000_000_000)
        let a = RequestID.next(now: t)
        #expect(RequestID.next(now: t.addingTimeInterval(0.3)) == a)
        #expect(RequestID.next(now: t.addingTimeInterval(2)) != a)
        #expect(RequestID.recent().contains(a))
    }

    @Test func everyRequestCarriesIt() async throws {
        let home = URL(string: "http://home.test")!
        let client = APIClient(home: home, tokens: TokenStore(app: "test.requestid", home: home))
        let r = await client.request("GET", "/api/libraries")
        #expect(r.value(forHTTPHeaderField: RequestID.header)?.count == 36)
    }
}
