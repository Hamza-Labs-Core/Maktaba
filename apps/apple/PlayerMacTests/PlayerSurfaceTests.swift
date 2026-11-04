#if os(macOS)
import AppKit
import AVKit
import SwiftUI
import Testing
import MaktabaKit
@testable import Maktaba_Player_Mac

/// The Mac's player surface is AppKit's AVPlayerView in our plain host view, on screen in
/// the app. Launched normally, SwiftUI's VideoPlayer (and AVPlayerView represented
/// directly) abort in `_AVKit_SwiftUI` on macOS 27 as soon as playback appears; a test run
/// does not reproduce that abort, so this guards the shape of the fix instead.
@MainActor @Suite struct MacPlayerSurface {
    @Test func showsInAWindow() throws {
        let player = AVPlayer()
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 640, height: 360),
                              styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        defer { window.close() }
        let hosting = NSHostingView(rootView: PlayerSurface(player: player).frame(width: 640, height: 360))
        window.contentView = hosting
        window.orderFrontRegardless()
        hosting.layoutSubtreeIfNeeded()
        window.displayIfNeeded()
        RunLoop.main.run(until: .now.addingTimeInterval(0.2))
        let host = try #require(Self.find(PlayerHostView.self, in: hosting))
        #expect(host.player === player)
        let view = try #require(Self.find(AVPlayerView.self, in: host))
        #expect(view.player === player)
        // FR-43: the controls offer picture in picture, a window that floats above every app,
        // and full screen
        #expect(view.allowsPictureInPicturePlayback)
        #expect(view.showsFullScreenToggleButton)
    }

    /// The watch and live players play on when the person leaves the app with Picture in
    /// Picture off (the default policy pauses a video drawn in a layer; on iPhone and iPad that
    /// cut the sound on going home). Shared code: checked here, where it runs on every PR.
    @Test func theWatchAndLivePlayersPlayOnWhenTheAppLeaves() {
        #expect(Playback.newPlayer().audiovisualBackgroundPlaybackPolicy == .continuesIfPossible)
        let home = URL(string: "http://127.0.0.1:9")!
        let live = LivePlayback(client: APIClient(home: home, tokens: TokenStore(app: "test.mac.background", home: home)),
                                channel: "ch-1")
        #expect(live.player.audiovisualBackgroundPlaybackPolicy == .continuesIfPossible)
    }

    /// Up next at the end of a video whose next episode the home named only after it ended:
    /// the time observer has stopped, so finding the episode counts down by itself (shared
    /// Player code; on iPad CI a slow answer meant no Up next). Autoplay as on a fresh install.
    @Test func upNextCountsDownForANextEpisodeFoundAfterTheEnd() throws {
        let defaults = UserDefaults.standard
        let kept = defaults.object(forKey: PlaybackSettings.autoplayNextKey)
        defaults.removeObject(forKey: PlaybackSettings.autoplayNextKey)
        defer { defaults.set(kept, forKey: PlaybackSettings.autoplayNextKey) }
        let home = URL(string: "http://127.0.0.1:9")!
        let playback = Playback(client: APIClient(home: home, tokens: TokenStore(app: "test.mac.upnext", home: home)))
        playback.playedToEnd()
        #expect(playback.ended)
        #expect(playback.upNextCountdown == nil, "no next episode yet: nothing to count down to")
        let next = try JSONDecoder().decode(Episode.self, from: Data(#"{"video_id":"v3","title":"Lesson 12","season":1,"episode":12}"#.utf8))
        playback.nextFound(next)
        #expect(playback.upNextCountdown == WatchLogic.upNextSeconds, "Up next counts down once the episode is found")
        playback.cancelUpNext()
    }

    /// The sound read after an audio track's formats loaded (they are not read yet after an
    /// audio switch; PR #595's iPad run said "audio" for 30 s) counts only for the item that
    /// still plays: a load that ends after the video changed is dropped. Shared Player code.
    @Test func aLoadedSoundCountsOnlyForTheItemStillPlaying() throws {
        let home = URL(string: "http://127.0.0.1:9")!
        let playback = Playback(client: APIClient(home: home, tokens: TokenStore(app: "test.mac.sound", home: home)))
        let item = AVPlayerItem(url: try #require(URL(string: "http://127.0.0.1:9/stream/s/manifest.m3u8")))
        let gone = AVPlayer(playerItem: item)
        playback.soundLoaded(Playback.Sound(codec: "aac", audible: true), of: gone, item)
        #expect(playback.sound == Playback.Sound(codec: nil, audible: true), "a player it no longer plays says nothing")
    }

    /// A quality change (or the renew) on a paused video reopens it paused: the reopen is
    /// asked not to play, the new item never plays, and `isPlaying` says paused at once. It
    /// played, then paused, and the paused player's time observer never ticked, so the
    /// controls said playing (#626's quality UI test: playing, its position still). Shared
    /// Player code; no stream: the items are never fetched.
    @Test func aPausedVideoReopensPausedAndSaysSo() async throws {
        let home = URL(string: "http://127.0.0.1:9")!
        let playback = Playback(client: APIClient(home: home, tokens: TokenStore(app: "test.mac.reopen", home: home)))
        defer { playback.stop() }
        func item() throws -> AVPlayerItem { AVPlayerItem(url: try #require(URL(string: "http://127.0.0.1:9/stream/s/manifest.m3u8"))) }
        await playback.load(try item(), at: 0, playing: true)
        let p = try #require(playback.player)
        #expect(p.rate > 0 && playback.isPlaying)
        #expect(playback.reopening(p, fallback: 42).playing, "playing: its reopen plays")
        playback.togglePause()
        #expect(p.rate == 0 && !playback.isPlaying)
        let again = playback.reopening(p, fallback: 42)
        #expect(!again.playing, "paused: its reopen does not play")
        await playback.load(try item(), at: again.at, playing: again.playing)
        #expect(p.rate == 0, "the reopened item never plays")
        #expect(!playback.isPlaying, "and the controls say paused")
    }

    /// A player still waiting 15 s after it was first seen waiting is opened again where it
    /// is (PR #614's iPhone run: stopped at 1257 s for good after an audio switch, its rate
    /// 1); a wait that ends sooner, or one that ends and starts again, is left alone; after
    /// a reopen the count starts over. Driven by its own clock, the watch's looks 10 s apart.
    @Test func aPlayerStuckWaitingFifteenSecondsOpensAgain() {
        let home = URL(string: "http://127.0.0.1:9")!
        let playback = Playback(client: APIClient(home: home, tokens: TokenStore(app: "test.mac.stuck", home: home)))
        let t0 = Date(timeIntervalSince1970: 1_000_000)
        func at(_ s: TimeInterval) -> Date { t0.addingTimeInterval(s) }
        #expect(!playback.stuck(waiting: false, at: at(0)), "playing")
        #expect(!playback.stuck(waiting: true, at: at(10)), "first seen waiting")
        #expect(!playback.stuck(waiting: true, at: at(20)), "waiting 10 s: a slow segment")
        #expect(playback.stuck(waiting: true, at: at(30)), "waiting 20 s: stuck, opened again")
        #expect(!playback.stuck(waiting: true, at: at(40)), "after the reopen the count starts over")
        #expect(!playback.stuck(waiting: false, at: at(50)), "it plays again")
        #expect(!playback.stuck(waiting: true, at: at(60)), "a new wait")
        #expect(!playback.stuck(waiting: true, at: at(70)))
        #expect(playback.stuck(waiting: true, at: at(75)), "15 s since it was first seen waiting")
    }

    /// A UI test's `-maktaba-settings-reset` puts autoplay back on as the app starts (PlayerApp
    /// applies it, not only the Profile), so a test that turned it off never leaves the next
    /// test's player without Up next.
    @Test func theSettingsResetPutsAutoplayBackOn() {
        let defaults = UserDefaults.standard
        defaults.set(false, forKey: PlaybackSettings.autoplayNextKey)
        PlaybackPreferences.shared.reload()
        #expect(!PlaybackSettings.read().autoplayNext)
        PlaybackPreferences.resetIfAsked(arguments: ["-maktaba-settings-reset"])
        #expect(PlaybackSettings.read().autoplayNext, "the player reads autoplay on")
        #expect(PlaybackPreferences.shared.autoplayNext)
    }

    static func find<V: NSView>(_: V.Type, in view: NSView) -> V? {
        if let v = view as? V { return v }
        for sub in view.subviews { if let v = find(V.self, in: sub) { return v } }
        return nil
    }
}
#endif
