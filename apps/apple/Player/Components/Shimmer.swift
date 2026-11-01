import MaktabaKit
import SwiftUI

/// A loading block in the shape of what loads (the web's Skeleton): the surface colour with a
/// soft light passing over it every 1.4 s, plain and still under Reduce Motion (NFR-9). One element for
/// VoiceOver, "Loading…", as the web's busy region.
struct Shimmer: View {
    var cornerRadius: CGFloat = MaktabaTokens.Radius.md

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// One pass of the light, as the web's `mk-skeleton-shimmer`.
    static let period: TimeInterval = 1.4

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
        // drawn per frame (no view moves, so the element keeps its own frame); paused and
        // plain under Reduce Motion
        TimelineView(.animation(minimumInterval: nil, paused: reduceMotion)) { context in
            if reduceMotion {
                shape.fill(Color.mktSurface)
            } else {
                let t = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: Self.period) / Self.period
                let x = -0.4 + t * 1.8
                shape.fill(LinearGradient(stops: [.init(color: .mktSurface, location: 0),
                                                  .init(color: .mktRaised, location: 0.5),
                                                  .init(color: .mktSurface, location: 1)],
                                          startPoint: UnitPoint(x: x - 0.4, y: 0.5), endPoint: UnitPoint(x: x + 0.4, y: 0.5)))
            }
        }
        .accessibilityElement()
        .accessibilityLabel(Text("Loading…", tableName: "Components", comment: "A placeholder while a picture or a row loads"))
        .accessibilityIdentifier("shimmer")
    }
}

/// `placeholder` once a load has taken 0.4 s (`PlaceholderTiming`), nothing before: a quick
/// load never flashes it.
struct DelayedPlaceholder<Placeholder: View>: View {
    @ViewBuilder let placeholder: () -> Placeholder
    @State private var late = false

    var body: some View {
        ZStack {
            if late { placeholder() }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task {
            try? await Task.sleep(for: PlaceholderTiming.delay)
            late = true
        }
    }
}

#if DEBUG
#Preview("Shimmer, dark") {
    ComponentGallery(piece: .shimmer).preferredColorScheme(.dark)
}

#Preview("Shimmer, Arabic, AX3") {
    ComponentGallery(piece: .shimmer).galleryPreview(arabic: true, large: true)
}
#endif
