import MaktabaKit
import SwiftUI

/// FR-5 with Live TV (story 48.21): the shell's sections, Live TV only when the person may
/// watch a channel. Asks `GET /api/channels/now` when the app comes to the front (one
/// request, never a poll in the background, NFR-16). Until the home has said, and when it
/// has no channel for this person, the section is not offered (a section that only ever says
/// "no channels" is no choice), as on Android.
///
/// At launch the shell waits for that answer a moment (up to `patience`), so a launch into
/// Live TV lands there; a later change rebuilds the shell with the new sections.
struct LiveSectionGate<Content: View>: View {
    let client: APIClient
    var sections: [PlayerSection] = PlayerSection.visible()
    @ViewBuilder let content: ([PlayerSection]) -> Content

    @State private var hasChannels: Bool?
    @State private var waited = false
    @Environment(\.scenePhase) private var scenePhase

    static var patience: Duration { .seconds(3) }

    var body: some View {
        Group {
            if sections.contains(.liveTV), hasChannels == nil, !waited {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Color.mktBackground)
            } else {
                let shown = PlayerSection.shown(sections, hasChannels: hasChannels)
                content(shown).id(shown)
            }
        }
        .task(id: scenePhase) {
            guard sections.contains(.liveTV), scenePhase == .active else { return }
            await check()
        }
        .task {
            try? await Task.sleep(for: Self.patience)
            waited = true
        }
    }

    private func check() async {
        do {
            hasChannels = try await !client.channelsNow().channels.isEmpty
        } catch {
            if Log.isCancellation(error) { return }
            // offline or refused: keep what the home said last; at launch go on without Live TV
            Log.failure("ask for live channels", error)
            waited = true
        }
    }
}
