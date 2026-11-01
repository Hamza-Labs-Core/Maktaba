import MaktabaKit
import SwiftUI

/// Where the components' pictures come from: the cache, and the header a home's image routes
/// need (the Bearer token; `playbackHeaders` refreshes it when it is about to end).
struct ImageSource: Sendable {
    var cache: ImageCache
    var headers: @Sendable (URL) async throws -> [String: String]

    /// No header: the UI tests' gallery, previews.
    static let plain = ImageSource(cache: .shared) { _ in [:] }

    /// A signed-in home's pictures: the token goes only to the home's own `/api/` routes, so a
    /// logo on another host never sees it.
    static func home(_ client: APIClient) -> ImageSource {
        ImageSource(cache: .shared) { url in
            guard HomeImageAuth.isHomeAPI(url, home: client.home) else { return [:] }
            return try await client.playbackHeaders().headers
        }
    }
}

extension EnvironmentValues {
    @Entry var imageSource: ImageSource = .plain
}

/// What a `CachedImage` has to show.
enum ImagePhase {
    case loading
    case loaded(Image)
    /// No picture (404) or none could be had.
    case failed
}

/// A picture from the `ImageCache`, decoded at the size this view is laid out at (times the
/// screen's scale). It fills the space it is given; `content` draws each phase.
struct CachedImage<Content: View>: View {
    let url: URL?
    @ViewBuilder let content: (ImagePhase) -> Content

    @Environment(\.imageSource) private var source
    @Environment(\.displayScale) private var scale
    @State private var pixels = 0
    @State private var phase = ImagePhase.loading

    private struct Load: Equatable {
        let url: URL?
        let pixels: Int
    }

    var body: some View {
        Color.clear
            .overlay { content(phase) }
            .clipped()
            .onGeometryChange(for: CGFloat.self) { geo in
                max(geo.size.width, geo.size.height)
            } action: { side in
                pixels = side > 0 ? ImageCache.bucket(Int((side * scale).rounded(.up))) : 0
            }
            .task(id: Load(url: url, pixels: pixels)) { await load() }
    }

    private func load() async {
        guard let url else {
            phase = .failed
            return
        }
        guard pixels > 0 else { return }
        if case .loaded = phase {} else { phase = .loading }
        do {
            var request = URLRequest(url: url)
            for (k, v) in (try? await source.headers(url)) ?? [:] { request.setValue(v, forHTTPHeaderField: k) }
            let image = try await source.cache.image(for: request, maxPixelSize: pixels)
            guard !Task.isCancelled else { return }
            phase = .loaded(Image(decorative: image.cgImage, scale: scale))
        } catch {
            guard !Task.isCancelled else { return }
            phase = .failed
        }
    }
}
