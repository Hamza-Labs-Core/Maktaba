import MaktabaKit
import SwiftUI

/// A video or a series in a row or a grid: its poster (the shimmer after 0.4 s while it
/// loads; with none, the title on the brand colour, the same size), the progress over its
/// foot, and the title and a detail below. Titles wrap in full, never cut, and the card
/// grows with Dynamic Type (NFR-7; the accessibility audit's clipped-text check); in Arabic it reads from the right. One element for VoiceOver: the title and
/// detail, with the progress as its value.
struct PosterCard: View {
    enum Shape {
        /// 16:9, a video's poster (as the web's VideoPoster).
        case wide
        /// 2:3, a series' cover.
        case tall

        var ratio: CGFloat { self == .wide ? 16 / 9 : 2 / 3 }
    }

    let title: String
    var detail: String?
    let poster: URL?
    var progress: Double?
    var shape = Shape.wide

    @ScaledMetric(relativeTo: .body) private var scaledWidth: CGFloat = 168
    private let baseWidth: CGFloat

    init(title: String, detail: String? = nil, poster: URL?, progress: Double? = nil, shape: Shape = .wide,
         width: CGFloat? = nil) {
        self.title = title
        self.detail = detail
        self.poster = poster
        self.progress = progress
        self.shape = shape
        let base = width ?? (shape == .wide ? 168 : 120)
        baseWidth = base
        _scaledWidth = ScaledMetric(wrappedValue: base, relativeTo: .body)
    }

    /// Wider as the text grows, up to 1.75 × (a row still shows more than one card).
    private var width: CGFloat { min(scaledWidth, baseWidth * 1.75) }

    var body: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
            PosterArt(url: poster, title: title, progress: progress)
                .aspectRatio(shape.ratio, contentMode: .fit)
            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: title)
                    .font(.maktabaBody(15, weight: .medium, relativeTo: .subheadline))
                    .foregroundStyle(Color.mktText)
                if let detail {
                    Text(verbatim: detail)
                        .font(.maktabaBody(13, relativeTo: .caption))
                        .foregroundStyle(Color.mktMuted)
                }
            }
            .multilineTextAlignment(.leading)
            .fixedSize(horizontal: false, vertical: true)
        }
        .frame(width: width, alignment: .leading)
        .contentShape(Rectangle())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(verbatim: [title, detail].compactMap { $0 }.joined(separator: ", ")))
        .accessibilityValue(progress.map { $0 > 0 ? ProgressStrip.spoken($0) : Text(verbatim: "") } ?? Text(verbatim: ""))
        .accessibilityIdentifier("poster.card")
    }
}

/// A poster filling its frame, its progress over its foot.
struct PosterArt: View {
    let url: URL?
    let title: String
    var progress: Double?

    var body: some View {
        CachedImage(url: url) { phase in
            switch phase {
            case let .loaded(image):
                image.resizable().scaledToFill()
            case .loading:
                DelayedPlaceholder { Shimmer(cornerRadius: 0) }
            case .failed:
                NoPoster(title: title)
            }
        }
        .overlay(alignment: .bottom) {
            if let progress, progress > 0 {
                ProgressStrip(value: progress, onArt: true)
                    .padding(.horizontal, MaktabaTokens.Spacing.s2)
                    .padding(.bottom, MaktabaTokens.Spacing.s2)
            }
        }
        .background(Color.mktSurface)
        .clipShape(RoundedRectangle(cornerRadius: MaktabaTokens.Radius.md, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: MaktabaTokens.Radius.md, style: .continuous)
                .strokeBorder(Color.mktBorder, lineWidth: 0.5)
        }
    }
}

/// No poster (404, or none could be had): the title on the brand colour, the poster's size.
struct NoPoster: View {
    let title: String

    var body: some View {
        ZStack {
            Color.mktSurface
            Color.brass.opacity(0.22)
            Text(verbatim: title)
                .font(.maktabaHeading(17, relativeTo: .headline))
                .foregroundStyle(Color.mktBrassText)
                .multilineTextAlignment(.center)
                .minimumScaleFactor(0.5)
                .padding(MaktabaTokens.Spacing.s2)
        }
        .accessibilityHidden(true)
    }
}

#if DEBUG
#Preview("Poster cards, dark") {
    ComponentGallery(piece: .poster).preferredColorScheme(.dark)
}

#Preview("Poster cards, Arabic, AX3") {
    ComponentGallery(piece: .poster).galleryPreview(arabic: true, large: true)
}
#endif
