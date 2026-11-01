import MaktabaKit
import SwiftUI

/// The controls over the video: Close and the title, Subtitles and audio, Quality; back 10 s,
/// play / pause, forward 10 s; the scrubber with the time and the length; Next when the
/// series has one. Every touch keeps them up (`touched`).
struct WatchControls: View {
    let playback: Playback
    let close: () -> Void
    let touched: () -> Void
    let open: (WatchSheet) -> Void

    /// The scrubber's place while it is dragged (nil: the playing position).
    @State private var scrubbing: Double?

    var body: some View {
        ZStack {
            // shade behind the controls only; taps go through to the video
            LinearGradient(colors: [.black.opacity(0.6), .clear, .clear, .black.opacity(0.7)], startPoint: .top, endPoint: .bottom)
                .ignoresSafeArea()
                .allowsHitTesting(false)
            VStack {
                top
                Spacer()
                middle
                Spacer()
                bottom
            }
            .padding()
        }
        .foregroundStyle(.white)
    }

    private var top: some View {
        HStack(spacing: 12) {
            CloseButton(action: close)
            Text(playback.video?.displayTitle ?? "")
                .font(.headline)
                .lineLimit(1)
                .shadow(radius: 4)
            Spacer()
            round("captions.bubble", id: "player.tracks") { open(.tracks) } label: {
                Text("Subtitles and audio", tableName: "Watch", comment: "Opens the subtitle and audio choices")
            }
            round("slider.horizontal.3", id: "player.quality") { open(.quality) } label: {
                Text("Quality", tableName: "Watch", comment: "Opens the quality choices")
            }
        }
    }

    private var middle: some View {
        HStack(spacing: 48) {
            round("gobackward.10", id: "player.back", big: false) { playback.skip(by: -10) } label: {
                Text("Back 10 seconds", tableName: "Watch", comment: "Seeks 10 seconds back")
            }
            round(playback.isPlaying ? "pause.fill" : "play.fill", id: "player.playPause", big: true) { playback.togglePause() } label: {
                playback.isPlaying ? Text("Pause", tableName: "Watch", comment: "Pauses the video")
                    : Text("Play", tableName: "Watch", comment: "Plays the video")
            }
            round("goforward.10", id: "player.forward", big: false) { playback.skip(by: 10) } label: {
                Text("Forward 10 seconds", tableName: "Watch", comment: "Seeks 10 seconds forward")
            }
        }
    }

    private var bottom: some View {
        VStack(spacing: 6) {
            HStack {
                Text(verbatim: WatchTime.text(scrubbing ?? playback.position))
                Spacer()
                if playback.next != nil {
                    Button { touched(); playback.playNext() } label: {
                        Label { Text("Next", tableName: "Watch", comment: "Plays the series' next episode") } icon: { Image(systemName: "forward.end.fill") }
                    }
                    .buttonStyle(.bordered)
                    .tint(.white)
                    .accessibilityIdentifier("player.next")
                }
                Text(verbatim: WatchTime.text(playback.duration))
            }
            .font(.caption.monospacedDigit())
            Slider(value: Binding(get: { scrubbing ?? playback.position }, set: { scrubbing = $0; touched() }),
                   in: 0 ... max(playback.duration, 1)) { editing in
                if !editing, let to = scrubbing {
                    playback.seek(to: to)
                    scrubbing = nil
                }
                touched()
            }
            .tint(.white)
            .accessibilityLabel(Text("Position", tableName: "Watch", comment: "The scrubber: where the video is"))
            .accessibilityValue(Text(verbatim: WatchTime.text(scrubbing ?? playback.position)))
            .accessibilityIdentifier("player.scrubber")
        }
    }

    private func round(_ symbol: String, id: String, big: Bool? = nil, action: @escaping () -> Void,
                       label: () -> Text) -> some View {
        Button {
            action()
            touched()
        } label: {
            Label { label() } icon: { Image(systemName: symbol) }
                .labelStyle(.iconOnly)
                .font(big == true ? .system(size: 44) : big == false ? .system(size: 30) : .title2)
                .frame(minWidth: 44, minHeight: 44)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .padding(big == nil ? 2 : 6)
        .background(big == nil ? AnyShapeStyle(.ultraThinMaterial) : AnyShapeStyle(.clear), in: .circle)
        .accessibilityIdentifier(id)
    }
}

/// A time as the controls show it: 4:05, 1:02:03.
enum WatchTime {
    static func text(_ seconds: Double) -> String {
        let s = Int((seconds.isFinite ? max(0, seconds) : 0).rounded(.down))
        let h = s / 3600, m = (s % 3600) / 60, sec = s % 60
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, sec) : String(format: "%d:%02d", m, sec)
    }
}
