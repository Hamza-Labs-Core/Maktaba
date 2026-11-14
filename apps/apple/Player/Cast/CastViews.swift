import AVKit
import MaktabaKit
import SwiftUI

// Story 58.06's screens: the Cast button beside AirPlay, Google's local network screen
// before the first cast, the TV list (AirPlay first, then Google Cast; AirPlay stays the
// default), the cast's panel over the watch screen and the mini controller. English and
// Arabic (Cast.xcstrings), mirrored in Arabic by the layout's leading and trailing edges.

/// The sentences of the Cast screens.
enum CastWords {
    static func castingTo(_ name: String) -> String {
        String(format: String(localized: "cast.castingTo", table: "Cast", comment: "The cast's line: the TV's name"),
               locale: AppLanguage.locale, name)
    }

    static func problem(_ p: CastController.Problem) -> String {
        switch p {
        case .refused(.viaCloud):
            String(localized: "cast.error.viaCloud", table: "Cast", comment: "The home refused the cast grant to a sign-in through Maktaba Cloud")
        case .refused:
            String(localized: "cast.error.refused", table: "Cast", comment: "The home refused the cast grant: nothing is cast")
        case .connect:
            String(localized: "cast.error.connect", table: "Cast", comment: "The Cast session did not start")
        case .failed:
            String(localized: "cast.error.failed", table: "Cast", comment: "Casting failed for another reason")
        case .receiver(.home):
            String(localized: "cast.error.home", table: "Cast", comment: "The receiver could not reach the home")
        case .receiver(.format):
            String(localized: "cast.error.format", table: "Cast", comment: "The receiver cannot play the video's format")
        case .receiver(.grant):
            String(localized: "cast.error.grant", table: "Cast", comment: "The receiver's grant is over")
        case .receiver(.update):
            String(localized: "cast.error.update", table: "Cast", comment: "The home is older than every receiver build")
        }
    }

    static var oldHome: String {
        String(localized: "cast.oldHome", table: "Cast", comment: "A home older than the cast grant: the TV plays a plain stream")
    }
}

/// The Cast button on the watch screen's controls (beside AirPlay).
struct CastButton: View {
    let cast: CastController

    var body: some View {
        Button { cast.castTapped() } label: {
            Label { Text("cast.button", tableName: "Cast", comment: "The Cast button: plays the video on a Google Cast TV") } icon: {
                Image(systemName: "tv.badge.wifi")
            }
            .labelStyle(.iconOnly)
            .font(.title2)
            .frame(minWidth: 44, minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .padding(2)
        .background(.ultraThinMaterial, in: .circle)
        .accessibilityIdentifier("player.cast")
    }
}

extension View {
    /// The local network screen and the TV list for `cast`, casting `video()` from `at()`.
    func castSheets(_ cast: CastController, player: AVPlayer?, video: @escaping () -> Video?, at: @escaping () -> Double) -> some View {
        modifier(CastSheets(cast: cast, player: player, video: video, at: at))
    }
}

private struct CastSheets: ViewModifier {
    @Bindable var cast: CastController
    let player: AVPlayer?
    let video: () -> Video?
    let at: () -> Double

    func body(content: Content) -> some View {
        content
            .sheet(isPresented: $cast.showsInterstitial) {
                CastInterstitial(cast: cast)
                    .presentationDetents([.medium])
            }
            .sheet(isPresented: $cast.showsPicker) {
                CastPicker(cast: cast, player: player) { device in
                    if let v = video() { cast.cast(v, at: at(), to: device) }
                }
                .presentationDetents([.medium, .large])
            }
    }
}

/// Google's required screen before the first cast: why the app looks at the local network,
/// and that iOS asks next.
struct CastInterstitial: View {
    let cast: CastController

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "wifi")
                .font(.system(size: 44))
                .foregroundStyle(Color.mktBrassText)
                .accessibilityHidden(true)
            Text("cast.localNetwork.title", tableName: "Cast", comment: "Google's local network screen before the first cast: its title")
                .font(.maktabaBody(22, weight: .semibold, relativeTo: .title2))
                .multilineTextAlignment(.center)
                .accessibilityAddTraits(.isHeader)
            Text("cast.localNetwork.body", tableName: "Cast", comment: "Google's local network screen before the first cast: why iOS asks next")
                .font(.maktabaBody(16, relativeTo: .body))
                .foregroundStyle(Color.mktMuted)
                .multilineTextAlignment(.center)
            Button { cast.interstitialContinued() } label: {
                Text("cast.localNetwork.continue", tableName: "Cast", comment: "The local network screen's button: iOS asks for local network access next")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .accessibilityIdentifier("cast.interstitial.continue")
            Button { cast.showsInterstitial = false } label: {
                Text("cast.localNetwork.notNow", tableName: "Cast", comment: "The local network screen's button: no cast now")
            }
            .accessibilityIdentifier("cast.interstitial.notNow")
        }
        .padding(24)
        .frame(maxWidth: 520)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("cast.interstitial")
    }
}

/// The TV list: AirPlay first (the default on Apple TVs and AirPlay TVs), Google Cast beside
/// it, with its empty state when no TV is found.
struct CastPicker: View {
    let cast: CastController
    let player: AVPlayer?
    let choose: (CastDevice) -> Void
    @Environment(\.dismiss) private var dismiss
    /// Discovery has had its time: an empty list now means none is there.
    @State private var looked = false

    var body: some View {
        NavigationStack {
            List {
                Section {
                    if let player {
                        HStack(spacing: 12) {
                            AirPlayButton(player: player, label: String(localized: "cast.picker.airplay", table: "Cast",
                                                                        comment: "The TV list's AirPlay section: an Apple TV or an AirPlay TV"),
                                          identifier: "cast.picker.airplay")
                                .frame(width: 44, height: 44)
                            Text("cast.picker.airplayHint", tableName: "Cast", comment: "Under the AirPlay row: what AirPlay reaches")
                                .foregroundStyle(Color.mktMuted)
                        }
                    }
                } header: {
                    Text("cast.picker.airplay", tableName: "Cast", comment: "The TV list's AirPlay section: an Apple TV or an AirPlay TV")
                }
                Section {
                    if cast.devices.isEmpty {
                        if looked {
                            Text("cast.picker.empty", tableName: "Cast", comment: "No Google Cast device found (a guest network can hide them)")
                                .foregroundStyle(Color.mktMuted)
                                .accessibilityIdentifier("cast.picker.empty")
                        } else {
                            HStack(spacing: 12) {
                                ProgressView()
                                Text("cast.picker.searching", tableName: "Cast", comment: "While the Google Cast devices are looked for")
                            }
                        }
                    }
                    ForEach(cast.devices) { device in
                        Button { choose(device) } label: {
                            HStack(spacing: 12) {
                                Image(systemName: "tv").accessibilityHidden(true)
                                Text(verbatim: device.name)
                                Spacer()
                                if cast.busy { ProgressView() }
                            }
                        }
                        .disabled(cast.busy)
                        .accessibilityIdentifier("cast.device.\(device.id)")
                    }
                    if let p = cast.problem {
                        Text(verbatim: CastWords.problem(p))
                            .foregroundStyle(Color.red)
                            .accessibilityIdentifier("cast.problem")
                    }
                } header: {
                    Text("cast.picker.cast", tableName: "Cast", comment: "The TV list's Google Cast section: Chromecast and TVs with Cast")
                }
            }
            .navigationTitle(Text("cast.picker.title", tableName: "Cast", comment: "The TV list's title: AirPlay and Google Cast devices"))
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button { dismiss() } label: { Text("cast.close", tableName: "Cast", comment: "Closes the TV list") }
                        .accessibilityIdentifier("cast.picker.close")
                }
            }
        }
        .accessibilityIdentifier("cast.picker")
        .task {
            try? await Task.sleep(for: .seconds(5))
            looked = true
        }
    }
}

/// The cast over the watch screen: the TV's name, the title, and the mini controller.
struct CastPanel: View {
    let cast: CastController

    var body: some View {
        VStack(spacing: 20) {
            Spacer()
            Image(systemName: "tv.badge.wifi")
                .font(.system(size: 64))
                .foregroundStyle(.white.opacity(0.8))
                .accessibilityHidden(true)
            if cast.oldHome {
                Text(verbatim: CastWords.oldHome)
                    .font(.maktabaBody(15, relativeTo: .subheadline))
                    .foregroundStyle(.white.opacity(0.8))
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("cast.oldHome")
            }
            if let p = cast.problem {
                Text(verbatim: CastWords.problem(p))
                    .foregroundStyle(.white)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("cast.problem")
            }
            Spacer()
            CastMiniController(cast: cast)
        }
        .padding()
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("cast.panel")
    }
}

/// What the TV plays and its controls: play / pause, ±10 s, stop casting. Shown on the watch
/// screen while it casts, and over the sections when the watch screen is closed.
struct CastMiniController: View {
    let cast: CastController

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 12) {
                Image(systemName: "tv").accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 2) {
                    Text(verbatim: cast.title)
                        .font(.maktabaBody(16, weight: .semibold, relativeTo: .headline))
                        .lineLimit(1)
                    if let d = cast.device {
                        Text(verbatim: CastWords.castingTo(d.name))
                            .font(.maktabaBody(13, relativeTo: .caption))
                            .foregroundStyle(Color.mktMuted)
                            .accessibilityIdentifier("cast.castingTo")
                    }
                }
                Spacer(minLength: 8)
                control("gobackward.10", id: "cast.back") { cast.skip(by: -10) } label: {
                    Text("cast.back", tableName: "Cast", comment: "Seeks the cast 10 seconds back")
                }
                if cast.status?.isPlaying ?? false {
                    control("pause.fill", id: "cast.playPause") { cast.pause() } label: {
                        Text("cast.pause", tableName: "Cast", comment: "Pauses the cast on the TV")
                    }
                } else {
                    control("play.fill", id: "cast.playPause") { cast.play() } label: {
                        Text("cast.play", tableName: "Cast", comment: "Plays the cast on the TV")
                    }
                }
                control("goforward.10", id: "cast.forward") { cast.skip(by: 10) } label: {
                    Text("cast.forward", tableName: "Cast", comment: "Seeks the cast 10 seconds forward")
                }
                control("stop.fill", id: "cast.stop") { cast.stop() } label: {
                    Text("cast.stop", tableName: "Cast", comment: "Ends the cast: the video comes back to this device")
                }
            }
            if let s = cast.status, s.duration > 0 {
                ProgressView(value: min(s.position, s.duration), total: s.duration)
                    .tint(Color.mktBrassText)
            }
        }
        .padding(12)
        .foregroundStyle(Color.mktText)
        .background(Color.mktSurface, in: .rect(cornerRadius: 14))
        .overlay(alignment: .topLeading) { testState }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("cast.mini")
    }

    /// The UI tests' reading of the cast (a Debug build only).
    @ViewBuilder private var testState: some View {
        #if DEBUG
        Color.clear.frame(width: 1, height: 1)
            .accessibilityElement()
            .accessibilityLabel(Text(verbatim: "cast"))
            .accessibilityValue(Text(verbatim: cast.testState))
            .accessibilityIdentifier("cast.state")
        #endif
    }

    private func control(_ symbol: String, id: String, action: @escaping () -> Void, label: () -> Text) -> some View {
        Button(action: action) {
            Label { label() } icon: { Image(systemName: symbol) }
                .labelStyle(.iconOnly)
                .font(.title3)
                .frame(minWidth: 44, minHeight: 44)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier(id)
    }
}
