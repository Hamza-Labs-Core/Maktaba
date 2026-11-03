import MaktabaKit
import SwiftUI

/// FR-5 with Live TV (story 48.21): the shell's sections, Live TV only when the person may
/// watch a channel. Asks `GET /api/channels/now` when the app comes to the front (one
/// request, never a poll in the background, NFR-16). Until the home has said, and when it
/// has no channel for this person, the section is not offered (a section that only ever says
/// "no channels" is no choice), as on Android.
///
/// At launch the shell waits for that answer a moment (up to `patience`), so a launch into
/// Live TV lands there; a later change gives the shell its new sections without rebuilding it
/// (each tab keeps its stack, the screen in view stays: `ShellTabs`, story 51.11).
///
/// With Live TV off on the home (`capabilities`, story 51.11) a viewer has no section and an
/// admin keeps it (its screen explains); `content`'s second value says Live TV is left out
/// because it is off, so a link into it can say so.
struct LiveSectionGate<Content: View>: View {
    let client: APIClient
    let capabilities: CapabilitiesStore
    var sections: [PlayerSection] = PlayerSection.visible()
    @ViewBuilder let content: ([PlayerSection], Bool) -> Content

    @State private var hasChannels: Bool?
    @State private var waited = false
    @Environment(\.scenePhase) private var scenePhase

    static var patience: Duration { .seconds(3) }

    var body: some View {
        Group {
            if sections.contains(.liveTV), hasChannels == nil, !waited, !liveTVOff {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Color.mktBackground)
            } else {
                let shown = PlayerSection.shown(sections, hasChannels: hasChannels, liveTVOff: liveTVOff,
                                                admin: capabilities.current.isAdmin)
                let hidden = liveTVOff && !shown.contains(.liveTV)
                content(shown, hidden)
                    // once shown, the shell is never swapped back for the wait
                    .onAppear { waited = true }
                    // built again once, when the home first answers what is on: a launch into
                    // Live TV with it off then lands as it should; later changes keep the
                    // screens open (ShellTabs, story 51.11)
                    .id(capabilities.answered)
            }
        }
        // asked again when Live TV comes back on (51.11): the channels show without a relaunch
        .task(id: LiveCheck(phase: scenePhase, off: liveTVOff)) {
            guard sections.contains(.liveTV), scenePhase == .active, !liveTVOff else { return }
            await check()
        }
        .task {
            try? await Task.sleep(for: Self.patience)
            waited = true
        }
    }

    private var liveTVOff: Bool { !capabilities.current.isOn(.liveTV) }

    private struct LiveCheck: Equatable {
        let phase: ScenePhase
        let off: Bool
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
