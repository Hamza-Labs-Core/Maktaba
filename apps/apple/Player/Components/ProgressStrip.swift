import MaktabaKit
import SwiftUI

/// How much of a video was watched: a thin brass bar on a track, filled from the leading
/// edge (the right in Arabic, NFR-11). VoiceOver reads "40% watched", or "Watched" at the end.
struct ProgressStrip: View {
    /// 0 … 1.
    let value: Double
    var height: CGFloat = 4
    /// Over a picture: a dark track that shows on any poster.
    var onArt = false

    private var fraction: Double { min(max(value.isFinite ? value : 0, 0), 1) }

    /// The whole percent VoiceOver reads (never 100 before the end, never 0 once started).
    static func percent(_ value: Double) -> Int {
        let v = min(max(value.isFinite ? value : 0, 0), 1)
        if v >= 1 { return 100 }
        if v <= 0 { return 0 }
        return min(99, max(1, Int((v * 100).rounded())))
    }

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule().fill(onArt ? Color.black.opacity(0.55) : Color.mktBorder)
                Capsule().fill(Color.brass).frame(width: geo.size.width * fraction)
            }
        }
        .frame(height: height)
        .accessibilityElement()
        .accessibilityLabel(label)
        .accessibilityIdentifier("progress.strip")
    }

    private var label: Text { Self.spoken(value) }

    /// What VoiceOver says for `value`: "40% watched", or "Watched" at the end.
    static func spoken(_ value: Double) -> Text {
        let pct = percent(value)
        return pct >= 100
            ? Text("Watched", tableName: "Components", comment: "A progress bar's spoken value: watched to the end")
            : Text("\(pct)% watched", tableName: "Components", comment: "A progress bar's spoken value: the percent watched")
    }
}

#if DEBUG
#Preview("Progress, dark") {
    ComponentGallery(piece: .progress).preferredColorScheme(.dark)
}

#Preview("Progress, Arabic, AX3") {
    ComponentGallery(piece: .progress).galleryPreview(arabic: true, large: true)
}
#endif
