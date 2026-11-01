import CoreGraphics
import CryptoKit
import Foundation
import ImageIO

/// The image cache's limits (Epic 48, NFR-4): decoded pictures in memory up to 50 MB and the
/// fetched files on disk up to 200 MB, each least recently used first out.
struct ImageCacheLimits: Sendable, Equatable {
    var memoryBytes = 50 * 1024 * 1024
    var diskBytes = 200 * 1024 * 1024
    /// How long an answer with no `max-age` stays fresh (a channel logo's route sends none).
    var fallbackFreshness: TimeInterval = 600

    static let standard = ImageCacheLimits()
}

/// A picture decoded at the size it shows (a `CGImage` is immutable, so it crosses actors).
final class DecodedImage: @unchecked Sendable {
    let cgImage: CGImage

    init(_ cgImage: CGImage) { self.cgImage = cgImage }

    var width: Int { cgImage.width }
    var height: Int { cgImage.height }
    /// What it costs in memory: its decoded bytes.
    var cost: Int { cgImage.bytesPerRow * cgImage.height }
}

/// What a fetch answered: the body, the status and the `Cache-Control` header.
struct ImageFetch: Sendable {
    let data: Data
    let status: Int
    let cacheControl: String?
}

enum ImageCacheError: Error, Equatable {
    /// 404: the video has no poster, the channel no logo.
    case notFound
    case status(Int)
    case undecodable
}

/// Posters, backdrops and channel logos for the Player's components (Epic 48, NFR-4):
/// - each picture is decoded at the size it shows (the longer side in pixels, rounded up to
///   64), never at the file's own size;
/// - the decoded pictures stay in memory up to `memoryBytes`, the fetched files on disk up to
///   `diskBytes`, least recently used out first;
/// - a picture asked again within the server's `max-age` (an hour for posters) is served from
///   memory or disk and fetches nothing; after it, it is fetched again, and when the home
///   cannot be reached the stale copy still shows (NFR-15);
/// - `no-store` is kept in memory only; one fetch serves every view that asks at once.
actor ImageCache {
    typealias Fetch = @Sendable (URLRequest) async throws -> ImageFetch

    /// The app's cache, in the Caches folder (the system may empty it; nothing is lost).
    static let shared = ImageCache(directory: FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first?
        .appending(path: "maktaba-images", directoryHint: .isDirectory))

    let limits: ImageCacheLimits
    private let directory: URL?
    private let fetch: Fetch
    private let now: @Sendable () -> Date

    private struct MemoryKey: Hashable {
        let url: String
        let pixels: Int
    }

    private struct MemoryEntry {
        let image: DecodedImage
        let expires: Date
        var used: UInt64
    }

    private struct DiskEntry {
        var bytes: Int
        var used: Date
    }

    private struct Stored: Sendable {
        let data: Data
        let expires: Date
    }

    private var memory: [MemoryKey: MemoryEntry] = [:]
    private(set) var memoryBytes = 0
    private var tick: UInt64 = 0
    private var disk: [String: DiskEntry]?
    private var inflight: [String: Task<Stored, Error>] = [:]

    init(directory: URL?, limits: ImageCacheLimits = .standard, fetch: @escaping Fetch = ImageCache.network,
         now: @escaping @Sendable () -> Date = { Date() }) {
        self.directory = directory
        self.limits = limits
        self.fetch = fetch
        self.now = now
    }

    /// The picture at `request`'s URL, decoded so its longer side is at most `maxPixelSize`
    /// pixels (`request` carries the home's Bearer header; the URL alone is the key).
    func image(for request: URLRequest, maxPixelSize: Int) async throws -> DecodedImage {
        guard let url = request.url?.absoluteString else { throw ImageCacheError.status(0) }
        let key = MemoryKey(url: url, pixels: Self.bucket(maxPixelSize))
        let at = now()
        if var hit = memory[key], hit.expires > at {
            tick += 1
            hit.used = tick
            memory[key] = hit
            return hit.image
        }
        let onDisk = readDisk(url)
        let stored: Stored
        if let onDisk, onDisk.expires > at {
            stored = onDisk
        } else {
            do {
                stored = try await fetched(url, request)
            } catch let error as ImageCacheError {
                throw error
            } catch {
                // the home is out of reach: yesterday's poster beats none
                guard let onDisk else { throw error }
                stored = onDisk
            }
        }
        let pixels = key.pixels
        let data = stored.data
        guard let decoded = await Task.detached(priority: .userInitiated, operation: { Self.decode(data, maxPixelSize: pixels) }).value
        else { throw ImageCacheError.undecodable }
        let image = DecodedImage(decoded)
        remember(key, image, expires: stored.expires)
        return image
    }

    // MARK: fetching

    /// One fetch per URL however many views ask at once; a fresh answer is written to disk
    /// (unless `no-store`) with when it stops being fresh.
    private func fetched(_ url: String, _ request: URLRequest) async throws -> Stored {
        if let running = inflight[url] { return try await running.value }
        let fetch = self.fetch
        let task = Task { () throws -> Stored in
            let answer = try await fetch(request)
            switch answer.status {
            case 200 ..< 300: break
            case 404, 410: throw ImageCacheError.notFound
            default: throw ImageCacheError.status(answer.status)
            }
            let fresh = Self.freshness(answer.cacheControl, fallback: limits.fallbackFreshness)
            let expires = now().addingTimeInterval(fresh ?? limits.fallbackFreshness)
            if fresh != nil { writeDisk(url, answer.data, expires: expires) }
            return Stored(data: answer.data, expires: expires)
        }
        inflight[url] = task
        defer { inflight[url] = nil }
        return try await task.value
    }

    /// The default fetch: no URL cache of its own (this is the cache), a 15 s timeout (NFR-15).
    static let network: Fetch = { request in
        var r = request
        r.cachePolicy = .reloadIgnoringLocalCacheData
        r.timeoutInterval = 15
        let (data, response) = try await session.data(for: r)
        let http = response as? HTTPURLResponse
        return ImageFetch(data: data, status: http?.statusCode ?? 200, cacheControl: http?.value(forHTTPHeaderField: "Cache-Control"))
    }

    private static let session: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.urlCache = nil
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        return URLSession(configuration: config)
    }()

    // MARK: freshness

    /// How long an answer stays fresh by its `Cache-Control` (nil: `no-store`, never on disk).
    static func freshness(_ cacheControl: String?, fallback: TimeInterval) -> TimeInterval? {
        guard let cacheControl else { return fallback }
        var maxAge: TimeInterval?
        for part in cacheControl.lowercased().split(separator: ",") {
            let p = part.trimmingCharacters(in: .whitespaces)
            if p == "no-store" { return nil }
            if p == "no-cache" { maxAge = 0 }
            if p.hasPrefix("max-age="), let n = TimeInterval(p.dropFirst("max-age=".count)), maxAge == nil {
                maxAge = max(0, n)
            }
        }
        return maxAge ?? fallback
    }

    // MARK: decoding

    /// A display size rounded up to 64 pixels, so near sizes share one decoded picture.
    static func bucket(_ pixels: Int) -> Int {
        max(64, (pixels + 63) / 64 * 64)
    }

    /// `data` decoded with its longer side at most `maxPixelSize` (never enlarged).
    static func decode(_ data: Data, maxPixelSize: Int) -> CGImage? {
        guard let source = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary)
        else { return nil }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceThumbnailMaxPixelSize: maxPixelSize,
        ]
        return CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary)
    }

    // MARK: memory

    private func remember(_ key: MemoryKey, _ image: DecodedImage, expires: Date) {
        guard image.cost <= limits.memoryBytes else { return }
        if let old = memory[key] { memoryBytes -= old.image.cost }
        tick += 1
        memory[key] = MemoryEntry(image: image, expires: expires, used: tick)
        memoryBytes += image.cost
        if memoryBytes > limits.memoryBytes {
            for (k, e) in memory.sorted(by: { $0.value.used < $1.value.used }) where memoryBytes > limits.memoryBytes {
                memory[k] = nil
                memoryBytes -= e.image.cost
            }
        }
    }

    /// The pictures in memory (the tests' view).
    var memoryCount: Int { memory.count }

    /// Empties the memory cache (as the system does under memory pressure); the disk stays.
    func clearMemory() {
        memory = [:]
        memoryBytes = 0
    }

    // MARK: disk

    /// A file per URL: an 8-byte header (when it stops being fresh, seconds since 1970)
    /// then the body as fetched. Its modification date is when it was last used.
    private func file(_ url: String) -> (name: String, url: URL)? {
        guard let directory else { return nil }
        let name = SHA256.hash(data: Data(url.utf8)).map { String(format: "%02x", $0) }.joined()
        return (name, directory.appending(path: name, directoryHint: .notDirectory))
    }

    /// What is on disk, read once and kept up to date after.
    private func diskIndex() -> [String: DiskEntry] {
        if let disk { return disk }
        var index: [String: DiskEntry] = [:]
        if let directory {
            try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            let keys: [URLResourceKey] = [.fileSizeKey, .contentModificationDateKey]
            let files = (try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: keys)) ?? []
            for f in files {
                let v = try? f.resourceValues(forKeys: Set(keys))
                index[f.lastPathComponent] = DiskEntry(bytes: v?.fileSize ?? 0, used: v?.contentModificationDate ?? .distantPast)
            }
        }
        disk = index
        return index
    }

    private func readDisk(_ url: String) -> Stored? {
        guard let (name, path) = file(url) else { return nil }
        var index = diskIndex()
        guard index[name] != nil else { return nil }
        guard let raw = try? Data(contentsOf: path), raw.count > 8 else {
            index[name] = nil
            disk = index
            return nil
        }
        let seconds = raw.prefix(8).withUnsafeBytes { Double(bitPattern: UInt64(littleEndian: $0.loadUnaligned(as: UInt64.self))) }
        let at = now()
        index[name]?.used = at
        disk = index
        try? FileManager.default.setAttributes([.modificationDate: at], ofItemAtPath: path.path)
        return Stored(data: raw.dropFirst(8), expires: Date(timeIntervalSince1970: seconds))
    }

    private func writeDisk(_ url: String, _ data: Data, expires: Date) {
        guard let (name, path) = file(url) else { return }
        var index = diskIndex()
        var raw = Data(count: 8)
        raw.withUnsafeMutableBytes { $0.storeBytes(of: expires.timeIntervalSince1970.bitPattern.littleEndian, as: UInt64.self) }
        raw.append(data)
        guard raw.count <= limits.diskBytes, (try? raw.write(to: path, options: .atomic)) != nil else { return }
        let at = now()
        try? FileManager.default.setAttributes([.modificationDate: at], ofItemAtPath: path.path)
        index[name] = DiskEntry(bytes: raw.count, used: at)
        var total = index.values.reduce(0) { $0 + $1.bytes }
        if total > limits.diskBytes, let directory {
            for (n, e) in index.sorted(by: { $0.value.used < $1.value.used }) where total > limits.diskBytes && n != name {
                try? FileManager.default.removeItem(at: directory.appending(path: n, directoryHint: .notDirectory))
                index[n] = nil
                total -= e.bytes
            }
        }
        disk = index
    }

    /// The bytes on disk (the tests' view).
    var diskBytes: Int { diskIndex().values.reduce(0) { $0 + $1.bytes } }
}
