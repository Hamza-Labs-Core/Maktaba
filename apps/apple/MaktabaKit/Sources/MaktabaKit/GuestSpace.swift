import AVFoundation
import Foundation
import Observation
import os

// Story 50.27 §2: the guest space every Apple app opens for Watch a broadcast: the saved
// broadcasts, redeeming an invite or a short code, a TV's pairing, a publisher's channels and
// the player. Its words are MaktabaKit's catalog's (`guest.*`, and the edge's own `broadcast.*`
// keys), in English and Arabic. Nothing here touches the app's homes or their accounts.

/// The guest space's words, by key.
public enum GuestWords {
    /// The words of `key` (`guest.saved`, `broadcast.tuning`), in the app's language.
    public static func message(_ key: String) -> String { kitWord(key) ?? key }

    /// `key`'s words with its `%@` filled in.
    public static func text(_ key: String, _ args: String...) -> String {
        String(format: message(key), locale: AppLanguage.locale, arguments: args.map { $0 as CVarArg })
    }

    static func by(_ name: String) -> String { text("guest.by", name) }

    /// "{publisher} on Maktaba".
    public static func onMaktaba(_ publisher: String) -> String { text("guest.onMaktaba", publisher) }

    /// The guest space is right to left in Arabic.
    public static var isRightToLeft: Bool { AppLanguage.isRightToLeft }
}

/// The guest space: the saved broadcasts and the ways in.
@MainActor @Observable
public final class GuestModel {
    public let store: GuestPassStore
    public let client: GuestClient
    public private(set) var saved: [GuestPass] = []
    /// A request is under way (the Watch button waits).
    public private(set) var busy = false
    /// Why the last way in failed, in the guest's words.
    public var problem: String?

    public init(store: GuestPassStore, client: GuestClient = GuestClient()) {
        self.store = store
        self.client = client
        saved = store.list()
    }

    public func reload() { saved = store.list() }

    /// An invite link or token: redeemed, saved, returned.
    public func redeem(invite text: String) async -> GuestPass? {
        guard let token = GuestInput.invite(text) else {
            problem = GuestWords.message("guest.badInvite")
            return nil
        }
        return await keep { try await self.client.redeem(invite: token) }
    }

    /// The publisher's handle and a short code (`MK-` and four letters or digits).
    public func redeem(publisher: String, code: String) async -> GuestPass? {
        guard let pub = GuestInput.publisher(publisher), let code = GuestInput.code(code) else {
            problem = GuestWords.message("guest.badCode")
            return nil
        }
        return await keep { try await self.client.redeem(publisher: pub, code: code) }
    }

    /// A phone's pairing brought this pass.
    public func paired(_ pass: GuestPass) {
        store.save(pass)
        reload()
    }

    public func remove(_ pass: GuestPass) {
        store.remove(pass.publisher)
        reload()
    }

    /// The publisher's channels; its name is kept with the pass.
    public func lineup(_ pass: GuestPass) async -> Result<GuestLineup, GuestError> {
        do {
            let l = try await client.lineup(pass)
            if !l.publisher.name.isEmpty, l.publisher.name != pass.name {
                store.name(pass.publisher, l.publisher.name)
                reload()
            }
            return .success(l)
        } catch let e as GuestError {
            return .failure(e)
        } catch {
            return .failure(.offline)
        }
    }

    private func keep(_ redeem: @escaping () async throws -> GuestPass) async -> GuestPass? {
        busy = true
        problem = nil
        defer { busy = false }
        do {
            let p = try await redeem()
            store.save(p)
            reload()
            return store.pass(for: p.publisher) ?? p
        } catch let e as GuestError {
            problem = e.message
        } catch {}
        return nil
    }
}

/// A TV's pairing: a QR code for a phone, the code to compare, the long-poll until the phone
/// sends a credential (story 50.27 §2, `docs/broadcast-edge.md`). A pairing that ends is
/// replaced by a new one while the screen is open.
@MainActor @Observable
public final class GuestPairingModel {
    public private(set) var pairing: GuestPairing?
    public var problem: String?
    private let client: GuestClient

    public init(client: GuestClient) { self.client = client }

    /// Runs until the phone pairs (the pass) or the task is cancelled (nil).
    public func run() async -> GuestPass? {
        while !Task.isCancelled {
            do {
                if pairing == nil { pairing = try await client.pair() }
                guard let p = pairing else { continue }
                switch try await client.poll(p) {
                case .waiting: continue
                case .gone: pairing = nil
                case let .paired(pass): return pass
                }
                problem = nil
            } catch is CancellationError {
                return nil
            } catch let e as GuestError {
                problem = e.message
                try? await Task.sleep(for: .seconds(5))
            } catch {
                try? await Task.sleep(for: .seconds(5))
            }
        }
        return nil
    }

    /// A new code now (Get a new code).
    public func renew() { pairing = nil }
}

/// One guest channel playing: the play, the heartbeat every minute (each new viewer token goes
/// to the proxy, a renewed pass to the store), and the state the guest sees. The player reads
/// the edge through `StreamProxy(live:)`, so AVPlayer's addresses never hold a token.
@MainActor @Observable
public final class GuestPlayback {
    public private(set) var pass: GuestPass
    public private(set) var channel: String?
    /// The channel's state; nil while starting.
    public private(set) var state: GuestViewerState?
    /// A refusal (the player stopped), with its words.
    public private(set) var problem: GuestError?
    @ObservationIgnored public private(set) lazy var player = AVPlayer()

    private let client: GuestClient
    private let store: GuestPassStore
    private let session: URLSession
    @ObservationIgnored private var proxy: StreamProxy?
    @ObservationIgnored private var beats: Task<Void, Never>?
    /// How often the heartbeat goes (the edge ends a session after 150 s without one).
    static let beatEvery: Duration = .seconds(60)
    private static let log = Logger(subsystem: "com.hamzalabs.maktaba", category: "guest")

    public init(pass: GuestPass, client: GuestClient, store: GuestPassStore, session: URLSession = .shared) {
        self.pass = pass
        self.client = client
        self.store = store
        self.session = session
    }

    /// The words the guest reads over the picture ("" while live).
    public var message: String { problem?.message ?? state?.message ?? "" }
    /// Try again helps.
    public var canRetry: Bool { problem?.state.retry ?? state?.retry ?? false }

    /// Plays `channel` (a channel change ends the last one's session: the edge keeps one per credential).
    public func play(_ channel: String) async {
        stop()
        self.channel = channel
        state = nil
        problem = nil
        do {
            let p = try await client.play(pass, channel: channel)
            guard self.channel == channel else { return }
            if let renewed = p.renewedPass {
                store.renew(pass.publisher, credential: renewed)
                pass = store.pass(for: pass.publisher) ?? pass
            }
            state = p.state
            guard p.state.plays else { return }
            guard let live = GuestLive(master: p.masterURL) else { throw GuestError(status: 502, code: "bad_answer") }
            let proxy = try StreamProxy(live: live, session: session)
            try await proxy.start()
            guard self.channel == channel, let local = proxy.localLive() else { proxy.stop(); return }
            self.proxy = proxy
            player.replaceCurrentItem(with: AVPlayerItem(url: local))
            player.play()
            beat(session: p.session, key: p.beatKey, live: live)
        } catch is CancellationError {
        } catch let e as GuestError {
            if self.channel == channel { problem = e }
        } catch {
            Self.log.error("guest play: \(error.localizedDescription, privacy: .public)")
            if self.channel == channel { problem = GuestError(status: 500, code: "internal") }
        }
    }

    /// Try again: the same channel from the start.
    public func retry() async {
        if let channel { await play(channel) }
    }

    /// Stops the picture, the heartbeat and the proxy.
    public func stop() {
        beats?.cancel()
        beats = nil
        player.pause()
        player.replaceCurrentItem(with: nil)
        proxy?.stop()
        proxy = nil
    }

    private func beat(session: String, key: String, live: GuestLive) {
        beats = Task { [weak self, client, pass] in
            while !Task.isCancelled {
                try? await Task.sleep(for: Self.beatEvery)
                if Task.isCancelled { return }
                do {
                    let b = try await client.beat(pass, session: session, beatKey: key)
                    if let m = b.masterURL { live.update(m) }
                    self?.state = b.state
                    if !b.state.plays { self?.player.pause() }
                } catch let e as GuestError where e.status == 0 {
                    // no network for a moment: the next beat tries again (150 s before the edge ends it)
                    continue
                } catch let e as GuestError {
                    // the session ended (an expired invite, a withdrawn pass): the state shows at once
                    self?.problem = e
                    self?.player.pause()
                    return
                } catch {
                    return
                }
            }
        }
    }
}
