import MaktabaKit
import SwiftUI

/// Subtitles and audio: Off and each of the session's subtitle renditions, then each audio
/// track (when there is more than one to choose from); the selected one is checked.
/// A choice applies and closes the sheet.
struct TracksSheet: View {
    let playback: Playback
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section {
                    row(id: "player.subtitle.off", selected: playback.subtitle == nil) {
                        Text("Off", tableName: "Watch", comment: "No subtitles")
                    } choose: { playback.selectSubtitle(nil) }
                    ForEach(playback.subtitles) { c in
                        row(id: "player.subtitle.\(c.language ?? c.id)", selected: playback.subtitle == c) {
                            Text(verbatim: c.name)
                        } choose: { playback.selectSubtitle(c) }
                    }
                } header: {
                    Text("Subtitles", tableName: "Watch", comment: "The subtitle choices' heading")
                }
                if playback.audioTracks.count > 1 {
                    Section {
                        ForEach(playback.audioTracks) { c in
                            row(id: "player.audio.\(c.language ?? c.id)", selected: playback.audio == c) {
                                Text(verbatim: c.name)
                            } choose: { playback.selectAudio(c) }
                        }
                    } header: {
                        Text("Audio", tableName: "Watch", comment: "The audio track choices' heading")
                    }
                }
            }
            .navigationTitle(Text("Subtitles and audio", tableName: "Watch", comment: "Opens the subtitle and audio choices"))
            .toolbar { DoneButton() }
        }
        .presentationDetents([.medium, .large])
        .accessibilityIdentifier("player.tracksSheet")
    }

    private func row(id: String, selected: Bool, @ViewBuilder title: () -> Text, choose: @escaping () -> Void) -> some View {
        ChoiceRow(id: id, selected: selected, title: title()) {
            choose()
            dismiss()
        }
    }
}

/// Quality: Auto (the home chooses) and each rung of the session's ladder (or the streaming
/// service's own rungs for direct play); a choice reopens at the same position.
struct QualitySheet: View {
    let playback: Playback
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                ChoiceRow(id: "player.quality.auto", selected: playback.maxBitrateKbps == nil,
                          title: Text("Auto", tableName: "Watch", comment: "The home chooses the quality")) {
                    playback.setQuality(nil)
                    dismiss()
                }
                ForEach(WatchLogic.qualities(playback.ladder.map { WatchLogic.Rung(height: $0.height, kbps: $0.bitrateKbps) })) { r in
                    ChoiceRow(id: "player.quality.\(r.kbps)", selected: playback.maxBitrateKbps == r.kbps,
                              title: Text(verbatim: "\(r.height)p")) {
                        playback.setQuality(r.kbps)
                        dismiss()
                    }
                }
            }
            .navigationTitle(Text("Quality", tableName: "Watch", comment: "Opens the quality choices"))
            .toolbar { DoneButton() }
        }
        .presentationDetents([.medium, .large])
        .accessibilityIdentifier("player.qualitySheet")
    }
}

private struct ChoiceRow: View {
    let id: String
    let selected: Bool
    let title: Text
    let choose: () -> Void

    var body: some View {
        Button(action: choose) {
            HStack {
                title
                Spacer()
                if selected { Image(systemName: "checkmark").foregroundStyle(.tint) }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier(id)
    }
}

private struct DoneButton: ToolbarContent {
    @Environment(\.dismiss) private var dismiss

    var body: some ToolbarContent {
        ToolbarItem(placement: .confirmationAction) {
            Button { dismiss() } label: { Text("Done", tableName: "Watch", comment: "Closes the choices") }
        }
    }
}
