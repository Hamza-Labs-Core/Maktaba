import MaktabaKit
import SwiftUI

/// A Live TV channel's logo (`/api/channels/{id}/logo`) on a raised tile; with none, the
/// channel's number in brass. VoiceOver reads "Channel 2, Quran". The tile grows with
/// Dynamic Type, up to twice its size.
struct ChannelLogo: View {
    let name: String
    let number: Int
    let logo: URL?

    @ScaledMetric(relativeTo: .body) private var scaled: CGFloat = 56
    private let base: CGFloat

    init(name: String, number: Int, logo: URL?, size: CGFloat = 56) {
        self.name = name
        self.number = number
        self.logo = logo
        base = size
        _scaled = ScaledMetric(wrappedValue: size, relativeTo: .body)
    }

    private var side: CGFloat { min(scaled, base * 2) }

    var body: some View {
        let tile = RoundedRectangle(cornerRadius: MaktabaTokens.Radius.lg, style: .continuous)
        CachedImage(url: logo) { phase in
            switch phase {
            case let .loaded(image):
                image.resizable().scaledToFit().padding(side * 0.12)
            case .loading:
                DelayedPlaceholder { Shimmer(cornerRadius: 0) }
            case .failed:
                Text(number, format: .number)
                    .font(.maktabaHeading(side * 0.42, relativeTo: .title))
                    .foregroundStyle(Color.mktBrassText)
                    .minimumScaleFactor(0.4)
                    .lineLimit(1)
                    .padding(side * 0.08)
            }
        }
        .frame(width: side, height: side)
        .background(tile.fill(Color.mktRaised))
        .clipShape(tile)
        .overlay { tile.strokeBorder(Color.mktBorder, lineWidth: 0.5) }
        .accessibilityElement()
        .accessibilityLabel(Text("Channel \(number), \(name)", tableName: "Components",
                                 comment: "A channel's logo, spoken: its number and its name"))
        .accessibilityIdentifier("channel.logo")
    }
}

#if DEBUG
#Preview("Channel logos, dark") {
    ComponentGallery(piece: .logo).preferredColorScheme(.dark)
}

#Preview("Channel logos, Arabic, AX3") {
    ComponentGallery(piece: .logo).galleryPreview(arabic: true, large: true)
}
#endif
