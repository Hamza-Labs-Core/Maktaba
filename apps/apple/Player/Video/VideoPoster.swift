import MaktabaKit
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// A video page's poster (FR-18; the owner's TestFlight report on 0.3.44: "no thumbnail on
/// video page"), at the top of the page **at its own shape**: a portrait video's poster stays
/// portrait, never cropped to 16:9. The box takes the picture's own width-to-height once it
/// decodes, as wide as the page allows and at most `maxHeight` tall, centred.
///
/// While it loads the box is 16:9 (the shimmer after 0.4 s); with no poster (a 404, the home
/// unreachable) it stays 16:9 and shows the title on the brand colour (`NoPoster`), so the page
/// never opens on a hole. The picture comes through the components' `CachedImage`, decoded at
/// the box's size (NFR-4). As Android's `VideoPoster` (48.28).
///
/// For the UI tests the poster is one image element named by the title, identified
/// `video.poster` once shown, `video.poster.missing` with none, `video.poster.loading` before.
struct VideoPoster: View {
    let title: String
    let url: URL?
    /// The page's width for it.
    let width: CGFloat
    let maxHeight: CGFloat

    /// The shown picture's width over height; nil until it shows.
    @State private var aspect: CGFloat?
    @State private var state = Shown.loading

    enum Shown: String {
        case loading, shown, missing
    }

    /// While loading and with no poster: 16:9, as the web's VideoPoster.
    static let wide: CGFloat = 16 / 9

    var body: some View {
        let size = Self.box(aspect: state == .shown ? aspect : nil, width: width, maxHeight: maxHeight)
        CachedImage(url: url) { phase in
            switch phase {
            case let .loaded(image):
                image.resizable().scaledToFit()
                    // the picture's own size (it ignores the box): its shape
                    .background {
                        image.fixedSize().hidden()
                            .onGeometryChange(for: CGSize.self) { $0.size } action: { s in
                                if s.width > 0, s.height > 0 { aspect = s.width / s.height }
                            }
                    }
                    .onAppear { state = .shown }
            case .loading:
                DelayedPlaceholder { Shimmer(cornerRadius: 0) }
                    .onAppear { state = .loading }
            case .failed:
                NoPoster(title: title)
                    .onAppear { state = .missing }
            }
        }
        .frame(width: size.width, height: size.height)
        .background(Color.mktSurface)
        .clipShape(RoundedRectangle(cornerRadius: MaktabaTokens.Radius.lg, style: .continuous))
        .frame(maxWidth: .infinity)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(verbatim: title))
        .accessibilityAddTraits(.isImage)
        .accessibilityIdentifier(state == .shown ? "video.poster" : "video.poster.\(state.rawValue)")
    }

    /// The box: `aspect` (16:9 when nil) as wide as `width`, unless that is taller than
    /// `maxHeight` (a portrait poster), then as tall as `maxHeight`.
    static func box(aspect: CGFloat?, width: CGFloat, maxHeight: CGFloat) -> CGSize {
        let ratio = aspect.flatMap { $0.isFinite && $0 > 0 ? $0 : nil } ?? wide
        let w = max(1, min(width, maxHeight * ratio))
        return CGSize(width: w.rounded(), height: (w / ratio).rounded())
    }

    /// The poster's largest height on a page `page` points tall: a little over half of it.
    static func maxHeight(page: CGFloat) -> CGFloat {
        min(max(page * 0.55, 200), 640)
    }

    /// `/api/videos/{id}/poster` on the home, `?size=small` for the small copy (48.5).
    static func url(_ home: URL, video: String, small: Bool) -> URL {
        let u = home.appending(path: "api/videos").appending(path: video).appending(path: "poster")
        return small ? u.appending(queryItems: [URLQueryItem(name: "size", value: "small")]) : u
    }

    /// The small copy on a phone (an iPhone Duo too); the full one on an iPad and the Mac.
    static var smallCopy: Bool {
        #if os(iOS)
        smallCopy(isPhone: UIDevice.current.userInterfaceIdiom == .phone)
        #else
        smallCopy(isPhone: false)
        #endif
    }

    static func smallCopy(isPhone: Bool) -> Bool { isPhone }
}
