#if os(macOS)
import AVKit
import Testing
import MaktabaKit
@testable import Maktaba_Player_Mac

/// Epic 57 in the shared player (`Playback` is the iPhone's, the iPad's and the Mac's), checked
/// here where it runs on every PR: what leaving the front does (story 57.09); that a
/// heartbeat the home refuses for this stream stops the video (on the time's-up screen for
/// time, on the blocked page for a video hidden mid-play) and nothing plays again by itself
/// (stories 57.05 and 57.15); and what the time's-up screen says was saved.
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

    /// 57.09 in a player no screen speaks for (the Libraries section's): the playback hears
    /// the app leave the front itself, sends the position and, under a limit, pauses in the
    /// background. A screen saying the same change again changes nothing.
    @Test func thePlaybackHearsTheAppLeaveTheFrontByItself() async {
        let center = NotificationCenter()
        let notes = Playback.SceneNotes(inactive: .init("test.inactive"), background: .init("test.background"),
                                        active: .init("test.active"))
        let home = URL(string: "http://127.0.0.1:9")!
        let p = Playback(client: APIClient(home: home, tokens: TokenStore(app: "test.mac.parental", home: home)),
                         center: center, scene: notes)
        p.limited = { true }
        await p.load(AVPlayerItem(url: URL(string: "http://127.0.0.1:9/stream/none.m3u8")!), at: 0, playing: true)
        #expect(p.isPlaying)
        center.post(name: notes.inactive!, object: nil)
        #expect(p.isPlaying, "inactive: it plays on")
        center.post(name: notes.background!, object: nil)
        #expect(!p.isPlaying && p.player?.rate == 0, "held in the background under a limit: paused, with no screen's help")
        center.post(name: notes.active!, object: nil)
        #expect(!p.isPlaying, "nothing resumes until a tap")
        p.togglePause(from: "test")
        #expect(p.isPlaying)
        // the watch screen says what the notification already said: once is enough
        center.post(name: notes.background!, object: nil)
        p.togglePause(from: "test")
        #expect(p.isPlaying, "played again by a tap while still held")
        p.sceneChanged(active: false, background: true)
        #expect(p.isPlaying, "the same change said twice does not pause what the person started")
        p.stop()
        // stopped: it no longer listens
        center.post(name: notes.active!, object: nil)
        center.post(name: notes.background!, object: nil)
        #expect(p.player == nil)

        // the next video of the same playback (the Libraries section keeps one): stopped
        // while held in the background, it still hears the next time the app is held
        await p.load(AVPlayerItem(url: URL(string: "http://127.0.0.1:9/stream/none.m3u8")!), at: 0, playing: true)
        #expect(p.isPlaying)
        center.post(name: notes.background!, object: nil)
        #expect(!p.isPlaying, "the second video pauses in the background too")
        p.stop()
    }

    /// With no limit the notifications change nothing that plays (story 48.24).
    @Test func anUnlimitedSignInHearsTheBackgroundAndPlaysOn() async {
        let center = NotificationCenter()
        let notes = Playback.SceneNotes(inactive: nil, background: .init("test.background"), active: nil)
        let home = URL(string: "http://127.0.0.1:9")!
        let p = Playback(client: APIClient(home: home, tokens: TokenStore(app: "test.mac.parental", home: home)),
                         center: center, scene: notes)
        await p.load(AVPlayerItem(url: URL(string: "http://127.0.0.1:9/stream/none.m3u8")!), at: 0, playing: true)
        center.post(name: notes.background!, object: nil)
        #expect(p.isPlaying && (p.player?.rate ?? 0) > 0)
        p.stop()
    }

    private static let timeOver = APIError.status(403, #"{"type":"https://maktaba.tv/problems/parental-time","status":403,"code":"parental_time","reason":"limit_reached"}"#)
    private static let missing = APIError.status(404, #"{"type":"https://maktaba.tv/problems/not-found","title":"not found","status":404}"#)

    /// A heartbeat refused for another session, or for no parental reason, changes nothing;
    /// the same refusal for this stream stops the video on the time's-up screen, and nothing
    /// plays again by itself (57.15). Stopping clears a parental stop.
    @Test func onlyThisStreamsParentalRefusalStopsTheVideo() async {
        let p = await playback(limited: true)
        p.adopt(session: "s1")
        p.progressRefused(Self.timeOver, session: "another-session", at: 12)
        #expect(p.parentalStop == nil && p.isPlaying, "another stream's refusal is not this one's")
        p.progressRefused(APIError.status(500, "{}"), session: "s1", at: 12)
        p.progressRefused(APIError.transport("down"), session: "s1", at: 12)
        #expect(p.parentalStop == nil && p.isPlaying, "a failure that is not parental is left to the next post")
        p.progressRefused(Self.timeOver, session: "s1", at: 12)
        #expect(p.parentalStop == .time(TimeUp(reason: "limit_reached", nextAllowedAt: nil), position: 12))
        #expect(p.player == nil && !p.isPlaying, "the video stopped")
        // refused again (a post that was already on its way): nothing more happens
        p.progressRefused(Self.timeOver, session: "s1", at: 14)
        #expect(p.parentalStop == .time(TimeUp(reason: "limit_reached", nextAllowedAt: nil), position: 12))
        p.stop()
        #expect(p.parentalStop == nil && p.player == nil)
    }

    /// The time's-up screen's "saved at" is the place the home holds: the last post it took
    /// (the home stores nothing from the post it refuses), else where the stream opened.
    /// The video itself goes on, when time is given, from where the picture stopped.
    @Test func aStopForTimeSaysThePlaceTheHomeHolds() async {
        let p = await playback(limited: true)
        p.adopt(session: "s1", openedFrom: 30)
        p.progressTaken(session: "another-session", at: 999)
        #expect(p.savedPosition == nil, "another stream's post says nothing of this one")
        p.progressTaken(session: "s1", at: 100)
        p.progressRefused(Self.timeOver, session: "s1", at: 110)
        #expect(p.parentalStop == .time(TimeUp(reason: "limit_reached", nextAllowedAt: nil), position: 110), "it resumes where it stopped")
        #expect(p.savedPosition == 100, "and says the place the home has")
        p.stop()
        #expect(p.savedPosition == nil)

        // refused before any post was taken: the home has where the stream opened
        let q = await playback(limited: true)
        q.adopt(session: "s2", openedFrom: 30)
        q.progressRefused(Self.timeOver, session: "s2", at: 38)
        #expect(q.savedPosition == 30)
        q.stop()
    }

    /// 57.05 T05: the home answers a progress post of a limited sign-in as a missing
    /// session's (the video was hidden from the account while it played): the video stops
    /// on the blocked page, mid-play. With no limit a 404 is no block.
    @Test func aVideoHiddenWhileItPlaysStopsOnTheBlockedPage() async throws {
        let home = try MockHome()
        try home.start()
        defer { home.stop() }
        let url = try #require(URL(string: home.url))
        let client = APIClient(home: url, tokens: TokenStore(app: "test.mac.parental", home: url))
        let video = try await client.video("v1")

        let free = Playback(client: client)
        await free.load(AVPlayerItem(url: URL(string: "http://127.0.0.1:9/stream/none.m3u8")!), at: 0, playing: true)
        free.adopt(session: "s1", video: video)
        free.progressRefused(Self.missing, session: "s1", at: 40)
        #expect(free.parentalStop == nil && free.isPlaying, "no limit: a 404 is left to the next post")
        free.stop()

        let p = Playback(client: client)
        p.limited = { true }
        await p.load(AVPlayerItem(url: URL(string: "http://127.0.0.1:9/stream/none.m3u8")!), at: 0, playing: true)
        p.adopt(session: "s1", video: video)
        p.progressRefused(Self.missing, session: "s1", at: 40)
        #expect(p.parentalStop == .blocked(id: "v1", midPlay: true))
        #expect(p.player == nil && !p.isPlaying, "the video stopped")
        p.stop()
    }

    /// A blocked next episode that a parent "opened" but the home still does not give (still
    /// hidden, or not reached): the blocked page stays. It used to be cleared before the
    /// video was read, leaving a spinner with only Close.
    @Test func aNextVideoThatStillCannotBeReadKeepsTheBlockedPage() async throws {
        let home = try MockHome()
        try home.start()
        defer { home.stop() }
        home.stub("GET", "/api/videos/v2", status: 404, body: #"{"type":"https://maktaba.tv/problems/not-found","title":"not found","status":404}"#)
        let url = try #require(URL(string: home.url))
        let client = APIClient(home: url, tokens: TokenStore(app: "test.mac.parental", home: url))
        let p = Playback(client: client)
        p.limited = { true }
        p.adopt(session: "s1", video: try await client.video("v1"))
        let stop = Playback.ParentalStop.blocked(id: "v2", midPlay: false)
        p.stopForParental(stop)
        #expect(p.parentalStop == stop)
        let asked = home.seen.filter { $0 == "GET /api/videos/v2" }.count
        p.resumeAfterStop()
        #expect(p.parentalStop == stop, "not cleared before the home gave the video")
        for _ in 0..<100 where home.seen.filter({ $0 == "GET /api/videos/v2" }).count == asked {
            try await Task.sleep(for: .milliseconds(50))
        }
        #expect(home.seen.filter { $0 == "GET /api/videos/v2" }.count == asked + 1, "the home was asked for it")
        try await Task.sleep(for: .milliseconds(300))
        #expect(p.parentalStop == stop, "still the blocked page, not a spinner")
        #expect(p.video?.id == "v1")
        p.stop()
    }
}
#endif
