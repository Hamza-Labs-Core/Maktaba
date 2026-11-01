import XCTest

/// The Player's components (Epic 48 story 48.14) on their gallery pages (`ComponentGallery`,
/// a Debug build's `-maktaba-gallery <piece>`), against MockHome's pictures: each in the dark
/// theme, in English and Arabic, at the default size and at the third accessibility size
/// (AX3), photographed (`player-<device>-<language>-components-<piece>[-ax3]`).
final class PlayerComponentsUITests: PlayerUITestCase {
    private static let ax3 = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]

    /// Opens one gallery page in `language`, at AX3 when `large`.
    private func gallery(_ piece: String, _ language: String, large: Bool) {
        launch(language, fresh: false,
               extra: ["-maktaba-gallery", piece, "-maktaba-gallery-home", home.url] + (large ? Self.ax3 : []))
        XCTAssertTrue(element("gallery.\(piece)").waitForExistence(timeout: 15), "the \(piece) page")
    }

    /// Every language and size: `check` runs on each, then a screenshot.
    private func eachVariant(_ piece: String, _ check: (_ language: String, _ large: Bool) -> Void) {
        for language in ["en", "ar"] {
            for large in [false, true] {
                gallery(piece, language, large: large)
                check(language, large)
                shot("components-\(piece)\(large ? "-ax3" : "")")
                // NFR-6: labels, roles, contrast, Dynamic Type, clipped text, hit regions
                do {
                    let window = screen
                    try app.performAccessibilityAudit { issue in
                        // a progress bar is a reading, not a control: no 44-pt target
                        if issue.auditType == .hitRegion, issue.element?.identifier == "progress.strip" { return true }
                        // a rail's last card half off the screen's edge: the audit samples
                        // pixels beyond it (the cards fully on screen are still checked)
                        if issue.auditType == .contrast, let f = issue.element?.frame, !window.contains(f) { return true }
                        return false
                    }
                } catch {
                    XCTFail("\(piece) \(language)\(large ? " AX3" : ""): \(error)")
                }
                app.terminate()
            }
        }
    }

    private func all(_ id: String) -> [XCUIElement] {
        app.descendants(matching: .any).matching(identifier: id).allElementsBoundByIndex
    }

    private var screen: CGRect { app.windows.firstMatch.frame }

    func testPosterCards() {
        home.stub("GET", "/api/videos/missing/poster", status: 404)
        var widths: [Bool: CGFloat] = [:]
        eachVariant("poster") { language, large in
            let first = element("poster.card")
            XCTAssertTrue(first.waitForExistence(timeout: 5))
            XCTAssertEqual(all("poster.card").count, 3)
            XCTAssertTrue(first.label.contains(language == "ar" ? "السيرة النبوية" : "The Seerah"), first.label)
            if language == "en" { XCTAssertEqual(first.value as? String, "40% watched") }
            XCTAssertLessThanOrEqual(first.frame.maxX, screen.maxX + 1, "the card fits the screen")
            widths[large] = first.frame.width
            // the posters came small (G4) and loaded fast: no placeholder left
            XCTAssertTrue(home.requests.contains { $0.path == "/api/videos/v1/poster" && $0.value("size") == "small" })
            XCTAssertTrue(waitForNo("shimmer"), "loaded: no shimmer")
        }
        XCTAssertGreaterThan(widths[true] ?? 0, widths[false] ?? .infinity, "AX3: the cards grow")
    }

    func testRails() {
        eachVariant("rail") { language, large in
            let rail = element("rail")
            XCTAssertTrue(rail.waitForExistence(timeout: 5))
            let cards = rail.descendants(matching: .any).matching(identifier: "poster.card").allElementsBoundByIndex
            XCTAssertGreaterThanOrEqual(cards.count, 2)
            if language == "ar" {
                XCTAssertGreaterThan(cards[0].frame.minX, cards[1].frame.minX, "Arabic: the row starts on the right")
            } else {
                XCTAssertLessThan(cards[0].frame.minX, cards[1].frame.minX, "the row starts on the left")
            }
            let seeAll = element("rail.seeAll")
            XCTAssertTrue(seeAll.exists)
            XCTAssertEqual(seeAll.label, language == "ar" ? "عرض الكل" : "See all")
            XCTAssertGreaterThanOrEqual(seeAll.frame.height, 44, "a 44-pt target")
            let title = element("rail.title")
            if large {
                XCTAssertGreaterThanOrEqual(seeAll.frame.minY, title.frame.maxY - 1, "AX3: See all under the title")
            }
            XCTAssertLessThanOrEqual(title.frame.maxX, screen.maxX + 1)
        }
    }

    func testHero() {
        var heights: [Bool: CGFloat] = [:]
        eachVariant("hero") { language, large in
            let title = element("hero.title")
            XCTAssertTrue(title.waitForExistence(timeout: 5))
            XCTAssertEqual(title.label, language == "ar" ? "السيرة النبوية، الدرس الرابع" : "The Seerah, lesson 4")
            let play = app.buttons["hero.play"]
            XCTAssertTrue(play.exists)
            XCTAssertEqual(play.label, language == "ar" ? "استئناف" : "Resume", "started: Resume")
            XCTAssertGreaterThanOrEqual(play.frame.height, 44)
            XCTAssertGreaterThanOrEqual(title.frame.minX, screen.minX - 1)
            XCTAssertLessThanOrEqual(title.frame.maxX, screen.maxX + 1, "the title wraps, not cut")
            heights[large] = title.frame.height
        }
        XCTAssertGreaterThan(heights[true] ?? 0, heights[false] ?? .infinity, "AX3: the title grows")
    }

    func testProgressStrips() {
        eachVariant("progress") { language, _ in
            XCTAssertTrue(element("progress.strip").waitForExistence(timeout: 5))
            let labels = all("progress.strip").map(\.label)
            XCTAssertEqual(labels.count, 5)
            if language == "ar" {
                XCTAssertTrue(labels.allSatisfy { $0.contains("شوهد") }, "\(labels)")
            } else {
                XCTAssertEqual(Array(labels.prefix(4)), ["10% watched", "40% watched", "75% watched", "Watched"])
            }
        }
    }

    func testChannelLogos() {
        var sides: [Bool: CGFloat] = [:]
        eachVariant("logo") { language, large in
            let logo = element("channel.logo")
            XCTAssertTrue(logo.waitForExistence(timeout: 5))
            let labels = all("channel.logo").map(\.label)
            XCTAssertEqual(labels.count, 2)
            if language == "ar" {
                XCTAssertTrue(labels[0].contains("القناة") && labels[0].contains("القرآن"), "\(labels)")
            } else {
                XCTAssertEqual(labels, ["Channel 2, Quran", "Channel 14, Lectures"])
            }
            XCTAssertTrue(home.seen.contains("GET /api/channels/ch-2/logo"))
            sides[large] = logo.frame.width
        }
        XCTAssertGreaterThan(sides[true] ?? 0, sides[false] ?? .infinity, "AX3: the tile grows")
    }

    /// A poster that does not come: the shimmer shows (after 0.4 s), read as "Loading…".
    func testShimmer() {
        home.delay("GET", "/api/videos/slow/poster", by: 60)
        eachVariant("shimmer") { language, _ in
            let shimmer = element("shimmer")
            XCTAssertTrue(shimmer.waitForExistence(timeout: 5))
            XCTAssertEqual(shimmer.label, language == "ar" ? "جارٍ التحميل…" : "Loading…")
            XCTAssertGreaterThanOrEqual(all("shimmer").count, 4, "the blocks and the card still loading")
        }
    }

    /// True once no element `id` is left (within 5 s).
    private func waitForNo(_ id: String) -> Bool {
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "count == 0"),
                                             object: app.descendants(matching: .any).matching(identifier: id))
        return XCTWaiter.wait(for: [gone], timeout: 5) == .completed
    }
}
