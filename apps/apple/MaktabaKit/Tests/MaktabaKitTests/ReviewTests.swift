import XCTest
@testable import MaktabaKit

/// Epic 52 (story 52.13): the Remote's word review, read tolerantly.
final class ReviewTests: XCTestCase {
    func testAPageWithAnUnknownReasonAndAnOddCandidate() throws {
        let json = #"""
        {"items":[{"id":"a","original":"😀x","context":"قال 😀x خير","char_start":4,"char_len":2,
          "language":"ar","direction":"rtl","reasons":["quran_mismatch","from_the_future"],
          "candidates":[{"text":"x","source":"quran","score":0.9},{"nope":1}],"status":"open",
          "verse":{"surah":1,"aya":2,"label_en":"Al-Faatiha 1:2","expected":"العالمين","kind":"sub"},"extra":true},
          {"no_id":true}],"next_cursor":null}
        """#
        let page = try JSONDecoder.home.decode(ReviewPage.self, from: Data(json.utf8))
        XCTAssertEqual(page.items.count, 1)
        let f = page.items[0]
        XCTAssertEqual(f.reasons, [.quranMismatch, .unknown])
        XCTAssertEqual(f.candidates.map(\.text), ["x"])
        XCTAssertEqual(f.status, .open)
        XCTAssertTrue(f.rightToLeft && f.isQuran)
        XCTAssertEqual(f.verse?.expected, "العالمين")
        // code points, as the API counts: the emoji is one
        XCTAssertEqual(f.parts.word, "😀x")
        XCTAssertEqual(f.parts.before, "قال ")
        XCTAssertEqual(f.parts.after, " خير")
    }

    func testResolveBodyAndConflicts() {
        let b = APIClient.resolveBody(.correct, text: "the", applyToSameKey: true, force: false)
        XCTAssertEqual(b["action"] as? String, "correct")
        XCTAssertEqual(b["apply_to_same_key"] as? Bool, true)
        XCTAssertNil(b["force"])
        XCTAssertEqual(APIError.status(409, #"{"code":"locked","locked_by_name":"a"}"#).reviewConflict, "locked")
        XCTAssertTrue(APIError.status(403, "{}").isReviewForbidden)
        XCTAssertNil(APIError.status(500, "{}").reviewConflict)
    }

    func testTheClipResolvesAgainstTheHome() {
        let home = URL(string: "https://h.local/maktaba/")!
        XCTAssertEqual(APIClient.clipURL("/stream/clip/v?track=1&sig=x", home: home)?.absoluteString,
                       "https://h.local/maktaba/stream/clip/v?track=1&sig=x")
        XCTAssertEqual(APIClient.clipURL("/stream/clip/v?sig=y", home: URL(string: "https://h.local")!)?.absoluteString,
                       "https://h.local/stream/clip/v?sig=y")
    }
}
