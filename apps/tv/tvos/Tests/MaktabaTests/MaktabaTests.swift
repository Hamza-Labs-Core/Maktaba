import XCTest
@testable import Maktaba

final class MaktabaTests: XCTestCase {
    func testVideoSummaryProgressFraction() {
        let v = VideoSummary(id: "v1", title: "T", durationSec: 3600, positionSec: 1800, posterURL: nil)
        XCTAssertEqual(v.progressFraction, 0.5, accuracy: 1e-9)
    }

    func testVideoSummaryProgressClampsToOne() {
        let v = VideoSummary(id: "v1", title: "T", durationSec: 100, positionSec: 200, posterURL: nil)
        XCTAssertEqual(v.progressFraction, 1.0)
    }

    func testVideoSummaryProgressIsZeroWhenNoPosition() {
        let v = VideoSummary(id: "v1", title: "T", durationSec: 100, positionSec: nil, posterURL: nil)
        XCTAssertEqual(v.progressFraction, 0)
    }

    func testAppSessionInitsUnpaired() {
        let s = AppSession()
        XCTAssertFalse(s.isPaired)
        XCTAssertNil(s.serverURL)
    }

    func testPairingServiceReturnsCode() async throws {
        let svc = PairingService()
        let code = try await svc.requestCode()
        XCTAssertFalse(code.code.isEmpty)
        XCTAssertGreaterThan(code.expiresAt, Date())
    }
}
