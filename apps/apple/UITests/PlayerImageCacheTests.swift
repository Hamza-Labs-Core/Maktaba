import CoreGraphics
import ImageIO
import UniformTypeIdentifiers
import XCTest

/// The components' image cache (Epic 48 story 48.14, NFR-4), compiled into the Player's
/// UI-test targets (project.yml) and tested here without the app: a fake fetch that counts,
/// a fake clock, a scratch folder.
final class PlayerImageCacheTests: XCTestCase {
    private var folder: URL!
    private let clock = FakeClock()

    override func setUp() async throws {
        folder = FileManager.default.temporaryDirectory.appending(path: "image-cache-\(UUID().uuidString)")
    }

    override func tearDown() async throws {
        try? FileManager.default.removeItem(at: folder)
    }

    private func cache(_ server: FakeServer, limits: ImageCacheLimits = .standard, disk: Bool = true) -> ImageCache {
        let clock = self.clock
        return ImageCache(directory: disk ? folder : nil, limits: limits, fetch: server.fetch, now: { clock.now })
    }

    private func request(_ name: String) -> URLRequest {
        URLRequest(url: URL(string: "http://home.test/api/videos/\(name)/poster?size=small")!)
    }

    func testTheLimitsAreNFR4s() {
        XCTAssertEqual(ImageCacheLimits.standard.memoryBytes, 50 * 1024 * 1024)
        XCTAssertEqual(ImageCacheLimits.standard.diskBytes, 200 * 1024 * 1024)
    }

    /// Decoded at the size it shows (rounded up to 64 px), never at the file's own size, and
    /// never enlarged.
    func testDecodesAtTheDisplaySize() async throws {
        let server = FakeServer(png: Self.png(width: 600, height: 900))
        let c = cache(server)
        let small = try await c.image(for: request("v1"), maxPixelSize: 100)
        XCTAssertEqual(small.height, 128, "the longer side at the 64-px bucket above 100")
        XCTAssertEqual(small.width, 85, accuracy: 1)
        let tiny = FakeServer(png: Self.png(width: 32, height: 48))
        let t = try await cache(tiny, disk: false).image(for: request("v2"), maxPixelSize: 512)
        XCTAssertEqual(t.height, 48, "never larger than the file")
        XCTAssertEqual(ImageCache.bucket(1), 64)
        XCTAssertEqual(ImageCache.bucket(129), 192)
    }

    /// A repeat within the server's max-age fetches nothing (from memory, then from disk after
    /// a relaunch); after it, the picture is fetched again.
    func testARepeatVisitWithinMaxAgeFetchesNothing() async throws {
        let server = FakeServer(png: Self.png(width: 64, height: 96), cacheControl: "private, max-age=3600")
        let first = cache(server)
        _ = try await first.image(for: request("v1"), maxPixelSize: 96)
        _ = try await first.image(for: request("v1"), maxPixelSize: 96)
        XCTAssertEqual(server.count, 1, "the second ask is the memory's")

        // a new launch: an empty memory, the same folder
        clock.advance(1800)
        let second = cache(server)
        _ = try await second.image(for: request("v1"), maxPixelSize: 300)
        XCTAssertEqual(server.count, 1, "within the hour: from disk, at another size too")

        clock.advance(1801)
        _ = try await second.image(for: request("v1"), maxPixelSize: 300)
        XCTAssertEqual(server.count, 2, "past max-age: fetched again")
    }

    /// The home out of reach: a stale copy on disk still shows (NFR-15).
    func testAStaleCopyShowsWhenTheHomeIsOutOfReach() async throws {
        let server = FakeServer(png: Self.png(width: 64, height: 96), cacheControl: "max-age=60")
        _ = try await cache(server).image(for: request("v1"), maxPixelSize: 96)
        clock.advance(120)
        server.offline = true
        let image = try await cache(server).image(for: request("v1"), maxPixelSize: 96)
        XCTAssertEqual(image.height, 96)
        XCTAssertEqual(server.count, 2, "it did try the home first")
    }

    func testNoPosterIsNotFound() async {
        let server = FakeServer(png: Data(), status: 404)
        do {
            _ = try await cache(server).image(for: request("missing"), maxPixelSize: 96)
            XCTFail("a 404 is no picture")
        } catch {
            XCTAssertEqual(error as? ImageCacheError, .notFound)
        }
    }

    func testNoStoreStaysOffTheDisk() async throws {
        let server = FakeServer(png: Self.png(width: 64, height: 96), cacheControl: "no-store")
        let c = cache(server)
        _ = try await c.image(for: request("v1"), maxPixelSize: 96)
        let onDisk = await c.diskBytes
        XCTAssertEqual(onDisk, 0)
        XCTAssertNil(ImageCache.freshness("no-store", fallback: 600))
        XCTAssertEqual(ImageCache.freshness("private, max-age=3600", fallback: 600), 3600)
        XCTAssertEqual(ImageCache.freshness("no-cache", fallback: 600), 0)
        XCTAssertEqual(ImageCache.freshness(nil, fallback: 600), 600)
    }

    /// Memory over its limit: the least recently used picture goes first.
    func testMemoryKeepsToItsLimitLeastRecentlyUsedOut() async throws {
        let server = FakeServer(png: Self.png(width: 64, height: 64))
        let probe = try await cache(server, disk: false).image(for: request("probe"), maxPixelSize: 64)
        let limits = ImageCacheLimits(memoryBytes: probe.cost * 2 + probe.cost / 2, diskBytes: 0)
        let c = cache(server, limits: limits, disk: false)
        server.count = 0
        _ = try await c.image(for: request("a"), maxPixelSize: 64)
        _ = try await c.image(for: request("b"), maxPixelSize: 64)
        _ = try await c.image(for: request("a"), maxPixelSize: 64) // a is now the most recent
        _ = try await c.image(for: request("c"), maxPixelSize: 64) // b goes
        let (count, bytes) = await (c.memoryCount, c.memoryBytes)
        XCTAssertEqual(count, 2)
        XCTAssertLessThanOrEqual(bytes, limits.memoryBytes)
        XCTAssertEqual(server.count, 3)
        _ = try await c.image(for: request("a"), maxPixelSize: 64)
        XCTAssertEqual(server.count, 3, "a stayed")
        _ = try await c.image(for: request("b"), maxPixelSize: 64)
        XCTAssertEqual(server.count, 4, "b was let go")
    }

    /// Disk over its limit: the least recently used file goes first.
    func testDiskKeepsToItsLimitLeastRecentlyUsedOut() async throws {
        let png = Self.png(width: 64, height: 64)
        let server = FakeServer(png: png, cacheControl: "max-age=86400")
        let file = png.count + 8
        let limits = ImageCacheLimits(memoryBytes: 0, diskBytes: file * 2 + file / 2)
        let c = cache(server, limits: limits)
        for name in ["a", "b"] {
            _ = try await c.image(for: request(name), maxPixelSize: 64)
            clock.advance(1)
        }
        _ = try await c.image(for: request("a"), maxPixelSize: 64) // read from disk: a is used
        clock.advance(1)
        _ = try await c.image(for: request("c"), maxPixelSize: 64) // b goes
        let bytes = await c.diskBytes
        XCTAssertLessThanOrEqual(bytes, limits.diskBytes)
        XCTAssertEqual(server.count, 3)

        let relaunched = cache(server, limits: limits)
        _ = try await relaunched.image(for: request("a"), maxPixelSize: 64)
        _ = try await relaunched.image(for: request("c"), maxPixelSize: 64)
        XCTAssertEqual(server.count, 3, "a and c stayed on disk")
        _ = try await relaunched.image(for: request("b"), maxPixelSize: 64)
        XCTAssertEqual(server.count, 4, "b was let go")
    }

    /// Views asking for the same picture at once share one fetch.
    func testOneFetchServesEveryoneAskingAtOnce() async throws {
        let server = FakeServer(png: Self.png(width: 64, height: 64), latency: 0.2)
        let c = cache(server)
        let r = request("v1")
        try await withThrowingTaskGroup(of: Void.self) { group in
            for _ in 0 ..< 5 {
                group.addTask { _ = try await c.image(for: r, maxPixelSize: 64) }
            }
            try await group.waitForAll()
        }
        XCTAssertEqual(server.count, 1)
    }

    /// The web's rule: no placeholder before 0.4 s.
    func testThePlaceholderWaitsFourTenthsOfASecond() {
        XCTAssertEqual(PlaceholderTiming.delay, .milliseconds(400))
        XCTAssertFalse(PlaceholderTiming.shows(after: .milliseconds(399)))
        XCTAssertTrue(PlaceholderTiming.shows(after: .milliseconds(400)))
    }

    // MARK: fakes

    static func png(width: Int, height: Int) -> Data {
        let ctx = CGContext(data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0,
                            space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.setFillColor(CGColor(red: 0.89, green: 0.7, blue: 0.32, alpha: 1))
        ctx.fill(CGRect(x: 0, y: 0, width: width, height: height))
        let out = NSMutableData()
        let dest = CGImageDestinationCreateWithData(out, UTType.png.identifier as CFString, 1, nil)!
        CGImageDestinationAddImage(dest, ctx.makeImage()!, nil)
        CGImageDestinationFinalize(dest)
        return out as Data
    }
}

/// A clock the test moves.
final class FakeClock: @unchecked Sendable {
    private let lock = NSLock()
    private var current = Date(timeIntervalSince1970: 1_800_000_000)
    var now: Date { lock.withLock { current } }
    func advance(_ seconds: TimeInterval) { lock.withLock { current += seconds } }
}

/// A home that answers every picture with `png` and counts what it was asked.
final class FakeServer: @unchecked Sendable {
    private let lock = NSLock()
    private let png: Data
    private let status: Int
    private let cacheControl: String?
    private let latency: TimeInterval
    private var _count = 0
    private var _offline = false

    init(png: Data, status: Int = 200, cacheControl: String? = "private, max-age=3600", latency: TimeInterval = 0) {
        self.png = png
        self.status = status
        self.cacheControl = cacheControl
        self.latency = latency
    }

    var count: Int {
        get { lock.withLock { _count } }
        set { lock.withLock { _count = newValue } }
    }

    var offline: Bool {
        get { lock.withLock { _offline } }
        set { lock.withLock { _offline = newValue } }
    }

    var fetch: ImageCache.Fetch {
        { [self] _ in
            let offline = lock.withLock { _count += 1; return _offline }
            if latency > 0 { try await Task.sleep(for: .seconds(latency)) }
            if offline { throw URLError(.cannotConnectToHost) }
            return ImageFetch(data: png, status: status, cacheControl: cacheControl)
        }
    }
}
