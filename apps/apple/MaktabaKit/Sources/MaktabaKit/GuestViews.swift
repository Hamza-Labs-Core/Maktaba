#if !os(tvOS)
import AVFoundation
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

// Story 50.27 §2: Watch a broadcast in the Player on iPhone, iPad and the Mac. A space of its
// own, shown over the app (no home, no account): the saved broadcasts with Remove, an invite
// link pasted or opened (a universal link to `watch.maktaba.tv/i/…`, or a QR code scanned with
// the camera), the publisher's handle with a short code; then the channels and the player,
// where a vertical swipe changes the channel. Apple TV has its own screens (`TVGuestSpace`).

/// The guest space. `invite`: a link the app was opened with, redeemed at once.
public struct GuestSpaceView: View {
    @State private var model: GuestModel
    /// The pass itself is pushed, so a channel list opens even when the Keychain refused to keep it.
    @State private var path: [GuestPass] = []
    @State private var link = ""
    @State private var publisher = ""
    @State private var code = ""
    @State private var scanning = false
    /// Which way in was tried last ("link", "code"; "" for a link the app was opened with): its
    /// refusal shows under its own Watch button, where the person is looking.
    @State private var tried = ""
    private let invite: String?
    @Environment(\.dismiss) private var dismiss

    public init(app: String, invite: String? = nil, client: GuestClient = GuestClient()) {
        _model = State(initialValue: GuestModel(store: GuestPassStore(app: app), client: client))
        self.invite = invite
    }

    public var body: some View {
        NavigationStack(path: $path) {
            Form {
                Section {
                    Text(verbatim: GuestWords.message("guest.intro")).foregroundStyle(.secondary)
                    if tried.isEmpty { problemRow }
                    if model.busy { ProgressView() }
                }
                saved
                byLink
                byCode
                Section {
                    Text(verbatim: GuestWords.message("guest.free")).foregroundStyle(.secondary)
                }
            }
            .formStyle(.grouped)
            .disabled(model.busy)
            .navigationTitle(Text(verbatim: GuestWords.message("guest.watchBroadcast")))
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(GuestWords.message("guest.close")) { dismiss() }
                        .accessibilityIdentifier("guest.close")
                }
            }
            .navigationDestination(for: GuestPass.self) { pass in
                GuestChannelsView(model: model, pass: pass)
            }
        }
        .environment(\.layoutDirection, GuestWords.isRightToLeft ? .rightToLeft : .leftToRight)
        #if os(iOS)
        .sheet(isPresented: $scanning) {
            QRScannerSheet(words: ConnectStrings.forPreferredLanguages()) { text in
                scanning = false
                tried = "link"
                Task { if let p = await model.redeem(invite: text) { path = [p] } }
            }
        }
        #endif
        .task {
            guard let invite else { return }
            if let p = await model.redeem(invite: invite) { path = [p] }
        }
    }

    @ViewBuilder private var problemRow: some View {
        if let problem = model.problem {
            Label { Text(verbatim: problem) } icon: { Image(systemName: "exclamationmark.triangle") }
                .foregroundStyle(.red)
                .accessibilityIdentifier("guest.problem")
        }
    }

    private var saved: some View {
        Section {
            if model.saved.isEmpty {
                Text(verbatim: GuestWords.message("guest.noSaved"))
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("guest.noSaved")
            }
            ForEach(model.saved) { pass in
                NavigationLink(value: pass) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(verbatim: GuestWords.onMaktaba(pass.name.isEmpty ? pass.publisher : pass.name))
                        Text(verbatim: GuestWords.text("guest.until", pass.expiresAt.formatted(date: .abbreviated, time: .omitted)))
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
                .accessibilityIdentifier("guest.saved.\(pass.publisher)")
                .swipeActions {
                    Button(GuestWords.message("guest.remove"), role: .destructive) { model.remove(pass) }
                }
                .contextMenu {
                    Button(GuestWords.message("guest.remove"), systemImage: "trash", role: .destructive) { model.remove(pass) }
                }
            }
            // a visible Remove for each, besides the swipe and the menu
            ForEach(model.saved) { pass in
                Button(role: .destructive) { model.remove(pass) } label: {
                    Label { Text(verbatim: "\(GuestWords.message("guest.remove")): \(pass.name.isEmpty ? pass.publisher : pass.name)") } icon: {
                        Image(systemName: "trash")
                    }
                }
                .accessibilityIdentifier("guest.remove.\(pass.publisher)")
            }
        } header: {
            Text(verbatim: GuestWords.message("guest.saved"))
        }
    }

    private var byLink: some View {
        Section {
            TextField(GuestWords.message("guest.invite"), text: $link)
                .autocorrectionDisabled()
                #if os(iOS)
                .textInputAutocapitalization(.never)
                .keyboardType(.URL)
                #endif
                .environment(\.layoutDirection, .leftToRight)
                .accessibilityIdentifier("guest.link")
            Button(GuestWords.message("guest.watch")) {
                tried = "link"
                Task {
                    if let p = await model.redeem(invite: link) {
                        link = ""
                        path = [p]
                    }
                }
            }
            .disabled(link.trimmingCharacters(in: .whitespaces).isEmpty)
            .accessibilityIdentifier("guest.watchLink")
            if tried == "link" { problemRow }
            #if os(iOS)
            // an invite's QR code (the broadcast's page shows one)
            Button(ConnectStrings.forPreferredLanguages().text(.scanQR), systemImage: "qrcode.viewfinder") { scanning = true }
                .accessibilityIdentifier("guest.scan")
            #endif
        } header: {
            Text(verbatim: GuestWords.message("guest.addInvite"))
        } footer: {
            Text(verbatim: GuestWords.message("guest.inviteHint"))
        }
    }

    private var byCode: some View {
        Section {
            TextField(GuestWords.message("guest.publisher"), text: $publisher)
                .autocorrectionDisabled()
                #if os(iOS)
                .textInputAutocapitalization(.never)
                #endif
                .environment(\.layoutDirection, .leftToRight)
                .accessibilityIdentifier("guest.publisher")
            TextField(GuestWords.message("guest.code"), text: $code)
                .autocorrectionDisabled()
                #if os(iOS)
                .textInputAutocapitalization(.characters)
                #endif
                .environment(\.layoutDirection, .leftToRight)
                .accessibilityIdentifier("guest.code")
            Button(GuestWords.message("guest.watch")) {
                tried = "code"
                Task {
                    if let p = await model.redeem(publisher: publisher, code: code) {
                        code = ""
                        path = [p]
                    }
                }
            }
            .disabled(publisher.isEmpty || code.isEmpty)
            .accessibilityIdentifier("guest.watch")
            if tried == "code" { problemRow }
        } header: {
            Text(verbatim: GuestWords.message("guest.addCode"))
        } footer: {
            Text(verbatim: "\(GuestWords.message("guest.enterCode")). \(GuestWords.message("guest.publisherHint")).")
        }
    }
}

/// A publisher's channels with now and next.
struct GuestChannelsView: View {
    let model: GuestModel
    let pass: GuestPass
    @State private var lineup: GuestLineup?
    @State private var problem: GuestError?
    @State private var playing: Int?

    var body: some View {
        List {
            if let problem {
                Text(verbatim: problem.message).foregroundStyle(.red).accessibilityIdentifier("guest.problem")
                if problem.state.retry {
                    Button(GuestWords.message("guest.tryAgain")) { Task { await load() } }
                }
            } else if let lineup {
                if lineup.channels.isEmpty {
                    Text(verbatim: GuestWords.message("guest.noChannels")).foregroundStyle(.secondary)
                }
                Section {
                    ForEach(Array(lineup.channels.enumerated()), id: \.element.id) { i, ch in
                        Button { playing = i } label: {
                            HStack(spacing: 12) {
                                Text(verbatim: ch.number > 0 ? "\(ch.number)" : "•").font(.title3.monospacedDigit()).frame(minWidth: 32)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(verbatim: ch.name).font(.headline)
                                    if let now = ch.now {
                                        Text(verbatim: "\(GuestWords.message("guest.now")): \(now.title)").font(.subheadline)
                                    }
                                    if let next = ch.next {
                                        Text(verbatim: "\(GuestWords.message("guest.next")): \(next.title)").font(.caption).foregroundStyle(.secondary)
                                    }
                                    if ch.state != "live" {
                                        Text(verbatim: GuestViewerState(status: 200, channelState: ch.state).message)
                                            .font(.caption).foregroundStyle(.secondary)
                                    }
                                }
                            }
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("guest.channel.\(ch.id)")
                    }
                } header: {
                    Text(verbatim: GuestWords.message("guest.channels"))
                }
            } else {
                ProgressView()
            }
        }
        .navigationTitle(Text(verbatim: GuestWords.onMaktaba(lineup?.publisher.name.isEmpty == false ? lineup!.publisher.name
                                                              : (pass.name.isEmpty ? pass.publisher : pass.name))))
        .task { await load() }
        #if os(iOS)
        .fullScreenCover(isPresented: Binding(get: { playing != nil }, set: { if !$0 { playing = nil } })) { player }
        #else
        .sheet(isPresented: Binding(get: { playing != nil }, set: { if !$0 { playing = nil } })) {
            player.frame(minWidth: 800, minHeight: 500)
        }
        #endif
    }

    @ViewBuilder private var player: some View {
        if let lineup, let i = playing {
            GuestPlayerView(pass: pass, name: lineup.publisher.name, channels: lineup.channels, start: i, model: model)
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

/// The player: the picture as the edge sends it, the channel and its programme, a CC BY
/// programme's credit, the state's words (the offline note while the slate plays, no error
/// dialog), Try again where it helps. A vertical swipe changes the channel.
struct GuestPlayerView: View {
    let pass: GuestPass
    /// The publisher's name as the lineup gave it ("" until it has one: the handle shows).
    let name: String
    let channels: [GuestLineup.Channel]
    @State private var index: Int
    @State private var playback: GuestPlayback
    @Environment(\.dismiss) private var dismiss

    init(pass: GuestPass, name: String, channels: [GuestLineup.Channel], start: Int, model: GuestModel) {
        self.pass = pass
        self.name = name.isEmpty ? pass.name : name
        self.channels = channels
        _index = State(initialValue: start)
        _playback = State(initialValue: GuestPlayback(pass: pass, client: model.client, store: model.store))
    }

    private var channel: GuestLineup.Channel { channels[index] }

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            Color.black.ignoresSafeArea()
            GuestVideoSurface(player: playback.player)
                .ignoresSafeArea()
                .accessibilityIdentifier("guest.player")
            VStack(alignment: .leading, spacing: 6) {
                Text(verbatim: channel.number > 0 ? "\(channel.number)  \(channel.name)" : channel.name)
                    .font(.title2.bold())
                    .accessibilityIdentifier("guest.playing")
                if let now = channel.now {
                    Text(verbatim: now.title).font(.headline)
                    if let credit = now.attribution?.line, !credit.isEmpty {
                        Text(verbatim: credit).font(.caption).accessibilityIdentifier("guest.attribution")
                    }
                }
                Text(verbatim: GuestWords.onMaktaba(name.isEmpty ? pass.publisher : name)).font(.caption).opacity(0.8)
                if !playback.message.isEmpty {
                    Text(verbatim: playback.message).font(.callout.weight(.semibold)).accessibilityIdentifier("guest.state")
                }
                if playback.canRetry {
                    Button(GuestWords.message("guest.tryAgain")) { Task { await playback.retry() } }
                        .buttonStyle(.borderedProminent)
                        .accessibilityIdentifier("guest.retry")
                }
                if channels.count > 1 {
                    Text(verbatim: GuestWords.message("guest.switchHint")).font(.caption2).opacity(0.7)
                }
            }
            .foregroundStyle(.white)
            .padding(16)
            .background(.black.opacity(0.55), in: RoundedRectangle(cornerRadius: 12))
            .padding(20)
        }
        .overlay(alignment: .topTrailing) {
            Button { dismiss() } label: {
                Image(systemName: "xmark.circle.fill").font(.title).symbolRenderingMode(.hierarchical).foregroundStyle(.white)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(Text(verbatim: GuestWords.message("guest.close")))
            .accessibilityIdentifier("guest.closePlayer")
            .padding(20)
        }
        .environment(\.colorScheme, .dark)
        .gesture(DragGesture(minimumDistance: 40).onEnded { g in
            guard abs(g.translation.height) > abs(g.translation.width) else { return }
            change(by: g.translation.height < 0 ? 1 : -1)
        })
        .task(id: index) { await playback.play(channel.id) }
        .onDisappear { playback.stop() }
    }

    private func change(by step: Int) {
        guard channels.count > 1 else { return }
        index = (index + step + channels.count) % channels.count
    }
}

#if os(macOS)
private struct GuestVideoSurface: NSViewRepresentable {
    let player: AVPlayer
    func makeNSView(context: Context) -> PlayerHostView { PlayerHostView(player: player) }
    func updateNSView(_ v: PlayerHostView, context: Context) { v.player = player }
}
#else
private struct GuestVideoSurface: UIViewRepresentable {
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
#endif
#endif
