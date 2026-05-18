import XCTest
@testable import Maktaba

/// StubHTTP returns a canned response so the networking layer can be
/// unit-tested without a live server.
final class StubHTTP: HTTPClient {
    var status: Int
    var body: Data
    var lastRequest: URLRequest?

    init(status: Int, body: Data) {
        self.status = status
        self.body = body
    }

    func data(for request: URLRequest) async throws -> (Data, URLResponse) {
        lastRequest = request
        let resp = HTTPURLResponse(
            url: request.url!, statusCode: status,
            httpVersion: nil, headerFields: nil)!
        return (body, resp)
    }
}

final class MaktabaTests: XCTestCase {
    private let base = URL(string: "https://tv.example.com")!

    // MARK: - Model logic

    func testVideoSummaryProgressFraction() {
        let v = VideoSummary(id: "v1", title: "T", durationSec: 3600, positionSec: 1800, posterURL: nil)
        XCTAssertEqual(v.progressFraction, 0.5, accuracy: 1e-9)
    }

    func testVideoSummaryProgressClampsToOne() {
        let v = VideoSummary(id: "v1", title: "T", durationSec: 100, positionSec: 200, posterURL: nil)
        XCTAssertEqual(v.progressFraction, 1.0)
    }

    func testVideoSummaryRemainingSec() {
        let v = VideoSummary(id: "v1", title: "T", durationSec: 600, positionSec: 120, posterURL: nil)
        XCTAssertEqual(v.remainingSec, 480, accuracy: 1e-9)
    }

    func testAppSessionApiConfigNilUntilPaired() {
        let s = AppSession()
        XCTAssertNil(s.apiConfig)
        s.serverURL = base
        s.pairedDeviceID = "dev-1"
        XCTAssertEqual(s.apiConfig?.baseURL, base)
        XCTAssertEqual(s.apiConfig?.deviceToken, "dev-1")
    }

    // MARK: - Pairing (real REST call against stub transport)

    func testPairingRequestDecodesCode() async throws {
        let json = #"{"code":"WXYZ-9999","expiresAt":"2026-05-18T12:05:00Z"}"#
        let http = StubHTTP(status: 201, body: Data(json.utf8))
        let svc = PairingService(config: APIConfig(baseURL: base), http: http)
        let code = try await svc.requestCode()
        XCTAssertEqual(code.code, "WXYZ-9999")
        XCTAssertGreaterThan(code.expiresAt, Date(timeIntervalSince1970: 0))
        XCTAssertEqual(http.lastRequest?.httpMethod, "POST")
        XCTAssertEqual(
            http.lastRequest?.url?.path, "/api/pairing/request")
    }

    func testPairingServerErrorSurfaces() async {
        let http = StubHTTP(status: 503, body: Data())
        let svc = PairingService(config: APIConfig(baseURL: base), http: http)
        do {
            _ = try await svc.requestCode()
            XCTFail("expected error")
        } catch let e as APIError {
            XCTAssertEqual(e, .server(503))
        } catch {
            XCTFail("wrong error \(error)")
        }
    }

    // MARK: - GraphQL rows (real query against stub transport)

    func testContinueWatchingDecodesEnvelope() async throws {
        let json = """
        {"data":{"continueWatching":[
          {"id":"v1","title":"Lecture 1","durationSec":3600,"positionSec":900,"posterUrl":null}
        ]}}
        """
        let http = StubHTTP(status: 200, body: Data(json.utf8))
        let cfg = APIConfig(baseURL: base, deviceToken: "tok")
        let svc = LibraryService(gql: GraphQLClient(config: cfg, http: http))
        let rows = try await svc.continueWatching()
        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(rows[0].id, "v1")
        XCTAssertEqual(rows[0].progressFraction, 0.25, accuracy: 1e-9)
        // The query hits POST /graphql with a bearer token.
        XCTAssertEqual(http.lastRequest?.url?.path, "/graphql")
        XCTAssertEqual(
            http.lastRequest?.value(forHTTPHeaderField: "Authorization"),
            "Bearer tok")
    }

    func testGraphQLErrorEnvelopeThrows() async {
        let json = #"{"errors":[{"message":"authentication required"}]}"#
        let http = StubHTTP(status: 200, body: Data(json.utf8))
        let svc = SearchService(gql: GraphQLClient(config: APIConfig(baseURL: base), http: http))
        do {
            _ = try await svc.query("kalam")
            XCTFail("expected graphql error")
        } catch let e as APIError {
            XCTAssertEqual(e, .graphql("authentication required"))
        } catch {
            XCTFail("wrong error \(error)")
        }
    }

    func testSearchEmptyQueryShortCircuits() async throws {
        let http = StubHTTP(status: 500, body: Data())
        let svc = SearchService(gql: GraphQLClient(config: APIConfig(baseURL: base), http: http))
        let r = try await svc.query("")
        XCTAssertTrue(r.isEmpty)
        XCTAssertNil(http.lastRequest) // never hit the network
    }
}
