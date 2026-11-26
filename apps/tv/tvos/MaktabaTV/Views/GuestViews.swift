import AVFoundation
import MaktabaKit
import SwiftUI
import UIKit

// Epic 50 story 50.27 §2: Watch a broadcast on Apple TV. A space of its own, with no home and
// no account: the saved broadcasts (each with Remove), the publisher's handle and an invite's
// short code typed with the remote, or a QR code a phone opens (`watch.maktaba.tv/pair`) while
// the TV waits for the pass; then the publisher's channels and the player (up and down change
// the channel). Every word is MaktabaKit's `guest.*` / `broadcast.*`, English and Arabic.

/// The guest space, over whatever the app showed (closed with Menu or Close).
struct TVGuestSpace: View {
    @State private var model = GuestModel(store: GuestPassStore(app: Bundle.main.bundleIdentifier ?? "com.hamzalabs.maktaba.tv"))
    /// The pass itself is pushed, so a channel list opens even when the Keychain refused to keep it.
    @State private var path: [GuestPass] = []
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack(path: $path) {
            TVGuestHome(model: model, open: { path = [$0] }, close: { dismiss() })
                .navigationDestination(for: GuestPass.self) { pass in
                    TVGuestChannels(model: model, pass: pass)
                }
        }
        .environment(\.layoutDirection, GuestWords.isRightToLeft ? .rightToLeft : .leftToRight)
    }
}

/// Watch a broadcast over a screen, open only while `session.guestGate` allows (story 50.27):
/// when a parental limit applies, or is not known again, the guest space closes and says why.
struct TVGuestCover: ViewModifier {
    let session: AppSession
    @Binding var isPresented: Bool
    @State private var notice: String?

    func body(content: Content) -> some View {
        content
            .fullScreenCover(isPresented: $isPresented) { TVGuestSpace() }
            .onChange(of: session.guestGate) { _, gate in
                guard !gate.allowed, isPresented else { return }
                isPresented = false
                notice = GuestWords.message(gate == .limited ? "guest.gate.closed" : "guest.gate.checking")
            }
            .alert(Text(verbatim: GuestWords.message("guest.watchBroadcast")),
                   isPresented: Binding(get: { notice != nil }, set: { if !$0 { notice = nil } })) {
                Button(GuestWords.message("guest.close")) { notice = nil }
            } message: {
                Text(verbatim: notice ?? "")
            }
    }
}

extension View {
    /// The guest space over this screen, closed under a parental limit (`TVGuestCover`).
    func guestCover(_ session: AppSession, isPresented: Binding<Bool>) -> some View {
        modifier(TVGuestCover(session: session, isPresented: isPresented))
    }
}

/// A heading, its rows and a line under them (the home picker's look).
private struct GuestSection<Content: View>: View {
    let title: String
    let footer: String?
    @ViewBuilder let content: Content

    init(_ title: String, footer: String? = nil, @ViewBuilder content: () -> Content) {
        self.title = title
        self.footer = footer
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text(verbatim: title)
                .font(Theme.body(29, weight: .semibold))
                .foregroundStyle(Theme.goldText)
            content
            if let footer {
                Text(verbatim: footer)
                    .font(Theme.body(24))
                    .foregroundStyle(Theme.faint)
            }
        }
    }
}

/// The saved broadcasts and the two ways in: a code typed with the remote, or a phone.
private struct TVGuestHome: View {
    let model: GuestModel
    let open: (GuestPass) -> Void
    let close: () -> Void
    @State private var publisher = ""
    @State private var code = ""
    @State private var pairing: GuestPairingModel?

    var body: some View {
        ZStack {
            LibraryBackdrop()
            ScrollView {
                VStack(alignment: .leading, spacing: 44) {
                    VStack(alignment: .leading, spacing: 8) {
                        SectionTitle(GuestWords.message("guest.watchBroadcast"), size: 64)
                        Text(verbatim: GuestWords.message("guest.intro"))
                            .font(Theme.body(32))
                            .foregroundStyle(Theme.muted)
                    }
                    if let problem = model.problem {
                        Label { Text(verbatim: problem) } icon: { Image(systemName: "exclamationmark.triangle") }
                            .foregroundStyle(.red)
                            .accessibilityIdentifier("guest.problem")
                    }
                    saved
                    byCode
                    byPhone
                    Button(GuestWords.message("guest.close"), action: close)
                        .buttonStyle(.gold)
                        .accessibilityIdentifier("guest.close")
                }
                .disabled(model.busy)
                .readingPanel(padding: 48)
                .frame(maxWidth: 1400)
                .padding(.vertical, 80)
                .frame(maxWidth: .infinity)
            }
        }
        .onExitCommand(perform: close)
        .onAppear { model.reload() }
    }

    private var saved: some View {
        GuestSection(GuestWords.message("guest.saved")) {
            if model.saved.isEmpty {
                Text(verbatim: GuestWords.message("guest.noSaved"))
                    .foregroundStyle(Theme.muted)
                    .accessibilityIdentifier("guest.noSaved")
            }
            ForEach(model.saved) { pass in
                HStack(spacing: 24) {
                    Button { open(pass) } label: {
                        HStack(spacing: 24) {
                            Image(systemName: "dot.radiowaves.left.and.right").font(.system(size: 40))
                            VStack(alignment: .leading, spacing: 4) {
                                Text(verbatim: GuestWords.onMaktaba(pass.name.isEmpty ? pass.publisher : pass.name))
                                    .font(Theme.heading(40))
                                Text(verbatim: GuestWords.text("guest.until", pass.expiresAt.formatted(Date.FormatStyle.dateTime.day().month().year().locale(AppLanguage.locale))))
                                    .font(Theme.body(26))
                                    .opacity(0.75)
                            }
                            Spacer()
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.gold)
                    .accessibilityIdentifier("guest.saved.\(pass.publisher)")
                    Button(GuestWords.message("guest.remove"), systemImage: "trash", role: .destructive) { model.remove(pass) }
                        .accessibilityIdentifier("guest.remove.\(pass.publisher)")
                }
            }
        }
    }

    private var byCode: some View {
        GuestSection(GuestWords.message("guest.addCode"), footer: GuestWords.message("guest.publisherHint")) {
            Text(verbatim: GuestWords.message("guest.enterCode")).foregroundStyle(Theme.muted)
            HStack(spacing: 24) {
                TextField(GuestWords.message("guest.publisher"), text: $publisher)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    .environment(\.layoutDirection, .leftToRight)
                    .frame(maxWidth: 600)
                    .accessibilityIdentifier("guest.publisher")
                TextField(GuestWords.message("guest.code"), text: $code)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.characters)
                    .environment(\.layoutDirection, .leftToRight)
                    .frame(maxWidth: 360)
                    .accessibilityIdentifier("guest.code")
                Button(GuestWords.message("guest.watch")) {
                    Task {
                        if let p = await model.redeem(publisher: publisher, code: code) {
                            code = ""
                            open(p)
                        }
                    }
                }
                .buttonStyle(.gold)
                .disabled(publisher.isEmpty || code.isEmpty)
                .accessibilityIdentifier("guest.watch")
            }
            if model.busy { ProgressView() }
        }
    }

    /// The QR code and the code to compare; the TV waits for the phone while this shows.
    private var byPhone: some View {
        GuestSection(GuestWords.message("guest.pairPhone")) {
            if let pairing, let p = pairing.pairing {
                HStack(alignment: .center, spacing: 48) {
                    QRCodeView(text: p.url)
                        .frame(width: 280, height: 280)
                        .accessibilityIdentifier("guest.qr")
                    VStack(alignment: .leading, spacing: 16) {
                        Text(verbatim: GuestWords.message("guest.scanQr")).font(Theme.body(30))
                        Text(verbatim: GuestWords.text("guest.pairCheck", ""))
                            .font(Theme.body(26))
                            .foregroundStyle(Theme.muted)
                        Text(verbatim: p.code)
                            .font(.system(size: 72, weight: .bold, design: .monospaced))
                            .environment(\.layoutDirection, .leftToRight)
                            .accessibilityIdentifier("guest.pairCode")
                        Button(GuestWords.message("guest.pairNew")) { pairing.renew() }
                            .accessibilityIdentifier("guest.pairNew")
                    }
                }
            } else if let problem = pairing?.problem {
                Text(verbatim: problem).foregroundStyle(.red)
            } else {
                HStack(spacing: 16) {
                    ProgressView()
                    Text(verbatim: GuestWords.message("guest.loading")).foregroundStyle(Theme.muted)
                }
            }
        }
        .task {
            let m = GuestPairingModel(client: model.client)
            pairing = m
            // the phone sent a pass: saved, and its channels open
            if let pass = await m.run() {
                model.paired(pass)
                open(pass)
            }
        }
    }
}

/// A publisher's channels, with now and next; one opens the player.
private struct TVGuestChannels: View {
    let model: GuestModel
    let pass: GuestPass
    @State private var lineup: GuestLineup?
    @State private var problem: GuestError?
    @State private var playing: Int?

    var body: some View {
        ZStack {
            LibraryBackdrop()
            ScrollView {
                VStack(alignment: .leading, spacing: 32) {
                    SectionTitle(GuestWords.onMaktaba(lineup?.publisher.name.nilIfEmpty ?? pass.name.nilIfEmpty ?? pass.publisher), size: 56)
                    if let problem {
                        Text(verbatim: problem.message)
                            .foregroundStyle(.red)
                            .accessibilityIdentifier("guest.problem")
                        if problem.state.retry {
                            Button(GuestWords.message("guest.tryAgain")) { Task { await load() } }
                                .buttonStyle(.gold)
                        }
                    } else if let lineup {
                        if lineup.channels.isEmpty {
                            Text(verbatim: GuestWords.message("guest.noChannels")).foregroundStyle(Theme.muted)
                        }
                        ForEach(Array(lineup.channels.enumerated()), id: \.element.id) { i, ch in
                            Button { playing = i } label: {
                                HStack(spacing: 28) {
                                    Text(verbatim: ch.number > 0 ? "\(ch.number)" : "•")
                                        .font(Theme.heading(44))
                                        .frame(width: 90)
                                    VStack(alignment: .leading, spacing: 6) {
                                        Text(verbatim: ch.name).font(Theme.heading(38))
                                        if let now = ch.now {
                                            Text(verbatim: "\(GuestWords.message("guest.now")): \(now.title)").font(Theme.body(26))
                                        }
                                        if let next = ch.next {
                                            Text(verbatim: "\(GuestWords.message("guest.next")): \(next.title)")
                                                .font(Theme.body(24)).opacity(0.75)
                                        }
                                    }
                                    Spacer()
                                    if ch.state != "live" {
                                        Text(verbatim: GuestViewerState(status: 200, channelState: ch.state).message)
                                            .font(Theme.body(22)).opacity(0.75)
                                    }
                                }
                                .frame(maxWidth: .infinity)
                            }
                            .buttonStyle(.gold)
                            .accessibilityIdentifier("guest.channel.\(ch.id)")
                        }
                    } else {
                        ProgressView()
                    }
                    Text(verbatim: GuestWords.message("guest.free")).font(Theme.body(24)).foregroundStyle(Theme.faint)
                }
                .readingPanel(padding: 48)
                .frame(maxWidth: 1400)
                .padding(.vertical, 80)
                .frame(maxWidth: .infinity)
            }
        }
        .task { await load() }
        .fullScreenCover(isPresented: Binding(get: { playing != nil }, set: { if !$0 { playing = nil } })) {
            if let lineup, let i = playing {
                TVGuestPlayer(pass: pass, name: lineup.publisher.name, channels: lineup.channels, start: i, model: model)
            }
        }
    }

    private func load() async {
        problem = nil
        switch await model.lineup(pass) {
        case let .success(l): lineup = l
        case let .failure(e): problem = e
        }
    }
}

/// The player: the picture as the edge sends it (the watermark is burned in), the channel and
/// its programme, a CC BY programme's credit, and the state's words over the picture (the
/// offline note while the slate plays, with no error dialog). Up and down change the channel.
private struct TVGuestPlayer: View {
    let pass: GuestPass
    /// The publisher's name as the lineup gave it ("" until it has one: the handle shows).
    let name: String
    let channels: [GuestLineup.Channel]
    let model: GuestModel
    @State private var index: Int
    @State private var playback: GuestPlayback
    @Environment(\.dismiss) private var dismiss

    init(pass: GuestPass, name: String, channels: [GuestLineup.Channel], start: Int, model: GuestModel) {
        self.pass = pass
        self.name = name.isEmpty ? pass.name : name
        self.channels = channels
        self.model = model
        _index = State(initialValue: start)
        _playback = State(initialValue: GuestPlayback(pass: pass, client: model.client, store: model.store))
    }

    private var channel: GuestLineup.Channel { channels[index] }

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            Color.black.ignoresSafeArea()
            GuestVideoLayer(player: playback.player)
                .ignoresSafeArea()
                .accessibilityIdentifier("guest.player")
            VStack(alignment: .leading, spacing: 10) {
                Text(verbatim: channel.number > 0 ? "\(channel.number)  \(channel.name)" : channel.name)
                    .font(Theme.heading(44))
                    .accessibilityIdentifier("guest.playing")
                if let now = channel.now {
                    Text(verbatim: now.title).font(Theme.body(30))
                    if let credit = now.attribution?.line, !credit.isEmpty {
                        Text(verbatim: credit).font(Theme.body(22)).opacity(0.8)
                            .accessibilityIdentifier("guest.attribution")
                    }
                }
                Text(verbatim: GuestWords.onMaktaba(name.isEmpty ? pass.publisher : name))
                    .font(Theme.body(22)).opacity(0.7)
                if !playback.message.isEmpty {
                    Text(verbatim: playback.message)
                        .font(Theme.body(28, weight: .semibold))
                        .accessibilityIdentifier("guest.state")
                }
                if playback.canRetry {
                    Button(GuestWords.message("guest.tryAgain")) { Task { await playback.retry() } }
                        .buttonStyle(.gold)
                        .accessibilityIdentifier("guest.retry")
                }
                if channels.count > 1 {
                    Text(verbatim: GuestWords.message("guest.switchHintTV")).font(Theme.body(20)).opacity(0.6)
                }
            }
            .padding(36)
            .background(Theme.panelTint, in: RoundedRectangle(cornerRadius: Theme.cornerRadius))
            .padding(80)
        }
        .focusable()
        .onMoveCommand { dir in
            switch dir {
            case .up: change(by: -1)
            case .down: change(by: 1)
            default: break
            }
        }
        .onExitCommand { dismiss() }
        .task(id: index) { await playback.play(channel.id) }
        .onDisappear { playback.stop() }
    }

    private func change(by step: Int) {
        guard channels.count > 1 else { return }
        index = (index + step + channels.count) % channels.count
    }
}

private struct GuestVideoLayer: UIViewRepresentable {
    let player: AVPlayer

    func makeUIView(context: Context) -> LayerView {
        let v = LayerView()
        v.playerLayer.player = player
        v.playerLayer.videoGravity = .resizeAspect
        v.isUserInteractionEnabled = false
        return v
    }

    func updateUIView(_ v: LayerView, context: Context) {
        if v.playerLayer.player !== player { v.playerLayer.player = player }
    }

    final class LayerView: UIView {
        override static var layerClass: AnyClass { AVPlayerLayer.self }
        var playerLayer: AVPlayerLayer { layer as! AVPlayerLayer }
    }
}

private extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}
