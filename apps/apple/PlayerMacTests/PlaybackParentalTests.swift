#if os(macOS)
import AVKit
import Testing
import MaktabaKit
@testable import Maktaba_Player_Mac

/// Epic 57 in the shared player (`Playback` is the iPhone's, the iPad's and the Mac's), checked
/// here where it runs on every PR: what leaving the front does (story 57.09), and that a
/// refused heartbeat stops the video on the time's-up screen and nothing plays again by
/// itself (stories 57.05 and 57.15).
@MainActor @Suite struct PlaybackUnderALimit {
    private func playback(limited: Bool) async -> Playback {
        let home = URL(string: "http://127.0.0.1:9")!
        let p = Playback(client: APIClient(home: home, tokens: TokenStore(app: "test.mac.parental", home: home)))
        p.limited = { limited }
        // an item that never loads: the player is "playing" (its rate is 1) with nothing to show
        await p.load(AVPlayerItem(url: URL(string: "http://127.0.0.1:9/stream/none.m3u8")!), at: 0, playing: true)
        return p
    }

    /// 57.09 T01: under a parental limit the scene going to the background pauses, and
    /// coming back to the front does not play again.
    @Test func aLimitedSignInPausesInTheBackgroundAndDoesNotResumeByItself() async {
        let p = await playback(limited: true)
        #expect(p.isPlaying && (p.player?.rate ?? 0) > 0)
        p.sceneChanged(active: false, background: false) // inactive: the position is sent, it plays on
        #expect(p.isPlaying)
        p.sceneChanged(active: false, background: true)
        #expect(!p.isPlaying && p.player?.rate == 0 && !p.pip.isActive)
        p.sceneChanged(active: true, background: false)
        #expect(!p.isPlaying && p.player?.rate == 0, "nothing resumes until a tap")
        p.togglePause(from: "test")
        #expect(p.isPlaying, "a tap plays")
        p.stop()
    }

    /// Story 48.24 stays: with no limit the sound plays on in the background.
    @Test func anUnlimitedSignInPlaysOnInTheBackground() async {
        let p = await playback(limited: false)
        p.sceneChanged(active: false, background: true)
        #expect(p.isPlaying && (p.player?.rate ?? 0) > 0)
        p.stop()
    }

    /// A heartbeat refused for another session, or for no parental reason, changes nothing;
    /// stopping clears a parental stop.
    @Test func aRefusalThatIsNotThisStreamsChangesNothing() async {
        let p = await playback(limited: true)
        let over = APIError.status(403, #"{"type":"https://maktaba.tv/problems/parental-time","status":403,"code":"parental_time","reason":"limit_reached"}"#)
        p.progressRefused(over, session: "another-session", at: 12)
        #expect(p.parentalStop == nil && p.isPlaying)
        p.stop()
        #expect(p.parentalStop == nil && p.player == nil)
    }
}
#endif
