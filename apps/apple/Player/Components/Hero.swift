import MaktabaKit
import SwiftUI

/// The top of Home (FR-12): the featured video's backdrop fading into the page, a line above
/// the title ("Featured", "Continue watching" …), the title in Amiri, a detail, the progress
/// and Play (Resume once started). The text sits below the picture's fade, not on it, so it
/// grows with Dynamic Type and nothing is cut (NFR-7). No parallax or scale (NFR-9).
struct Hero: View {
    let title: String
    var eyebrow: Text?
    var detail: String?
    let backdrop: URL?
    var progress: Double?
    let play: () -> Void

    @Environment(\.horizontalSizeClass) private var sizeClass

    private var started: Bool { (progress ?? 0) > 0 }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            art
            VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                if let eyebrow {
                    eyebrow
                        .font(.maktabaBody(13, weight: .semibold, relativeTo: .caption))
                        .foregroundStyle(Color.mktBrassText)
                        .textCase(.uppercase)
                }
                Text(verbatim: title)
                    .font(.maktabaHeading(MaktabaTokens.TypeSize.h1, relativeTo: .largeTitle))
                    .foregroundStyle(Color.mktText)
                    .accessibilityAddTraits(.isHeader)
                    .accessibilityIdentifier("hero.title")
                if let detail {
                    Text(verbatim: detail)
                        .font(.maktabaBody(15, relativeTo: .subheadline))
                        .foregroundStyle(Color.mktMuted)
                }
                if let progress, progress > 0 {
                    ProgressStrip(value: progress)
                        .frame(maxWidth: 240)
                        .padding(.vertical, MaktabaTokens.Spacing.s1)
                }
                Button(action: play) {
                    Label {
                        started
                            ? Text("Resume", tableName: "Components", comment: "The hero's button: go on from where it stopped")
                            : Text("Play", tableName: "Components", comment: "The hero's button: play the video")
                    } icon: {
                        Image(systemName: "play.fill")
                    }
                }
                .buttonStyle(BrassButtonStyle())
                .padding(.top, MaktabaTokens.Spacing.s2)
                .accessibilityIdentifier("hero.play")
            }
            .fixedSize(horizontal: false, vertical: true)
            .multilineTextAlignment(.leading)
            .padding(.horizontal, MaktabaTokens.Spacing.s4)
            .padding(.top, -MaktabaTokens.Spacing.s8)
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("hero")
    }

    /// The backdrop, 16:9 (a wide screen caps its height), fading into the page at its foot.
    private var art: some View {
        CachedImage(url: backdrop) { phase in
            switch phase {
            case let .loaded(image): image.resizable().scaledToFill()
            case .loading: DelayedPlaceholder { Shimmer(cornerRadius: 0) }
            case .failed: Color.brass.opacity(0.18)
            }
        }
        .aspectRatio(16 / 9, contentMode: .fit)
        .frame(maxHeight: sizeClass == .regular ? 420 : nil)
        .frame(maxWidth: .infinity)
        .clipped()
        .overlay {
            LinearGradient(stops: [.init(color: .clear, location: 0.45), .init(color: .mktBackground, location: 1)],
                           startPoint: .top, endPoint: .bottom)
        }
        .accessibilityHidden(true)
    }
}

/// Text on brass (`accentFg` on `accent`, AA), at least 44 pt tall.
struct BrassButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.maktabaBody(16, weight: .semibold, relativeTo: .body))
            .foregroundStyle(MaktabaTokens.Colors.accentFg.color)
            .padding(.horizontal, MaktabaTokens.Spacing.s5)
            .padding(.vertical, MaktabaTokens.Spacing.s3)
            .frame(minHeight: 44)
            .background(Capsule().fill(Color.brass))
            .opacity(configuration.isPressed ? 0.8 : 1)
            .contentShape(Capsule())
    }
}

#if DEBUG
#Preview("Hero, dark") {
    ComponentGallery(piece: .hero).preferredColorScheme(.dark)
}

#Preview("Hero, Arabic, AX3") {
    ComponentGallery(piece: .hero).galleryPreview(arabic: true, large: true)
}
#endif
