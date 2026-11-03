import AVFoundation
import XCTest

/// The player's decisions and its track handling (story 48.23), without the app: the files
/// `Player/Watch/WatchLogic.swift` and `Player/Watch/PlayerTracks.swift` are compiled into
/// the Player's UI-test targets (see project.yml). The track tests really play MockHome's HLS
/// stream (ffmpeg-made 2 s segments: 64 × 36 video with AAC sound, en/ar subtitle renditions
/// as the home serves them since 48.4) with AVPlayer on the simulator.
@MainActor
final class PlayerWatchTests: XCTestCase {
    private var home: MockHome!

    override func setUp() async throws {
        // the tracks tests play sound in this runner: no Player may be left running from a UI
        // test before them, holding the sound (PlayerUITestCase.tearDown closes it)
        XCTAssertEqual(XCUIApplication().state, .notRunning,
                       "a UI test left the Player running: its sound session keeps this test's player from starting its sound")
        home = try MockHome()
        try home.start()
    }

    override func tearDown() async throws {
        home?.stop()
    }

    // MARK: the decisions

    func testAVideoOpensWhereAskedElseAtItsResumePointUnlessFinished() {
        XCTAssertEqual(WatchLogic.start(asked: nil, resume: 1240, completed: false), 1240)
        XCTAssertEqual(WatchLogic.start(asked: nil, resume: 3120, completed: true), 0, "a finished video starts again")
        XCTAssertEqual(WatchLogic.start(asked: nil, resume: nil, completed: false), 0)
        XCTAssertEqual(WatchLogic.start(asked: 90, resume: 1240, completed: false), 90, "the time asked for wins")
        XCTAssertEqual(WatchLogic.start(asked: 0, resume: 1240, completed: false), 0)
    }

    /// A player still waiting 15 s after it was first seen waiting is opened again where it
    /// is; one not waiting, or waiting less, is left alone.
    func testAPlayerWaitingFifteenSecondsIsStuck() {
        let now = Date()
        XCTAssertFalse(WatchLogic.stuck(waitingSince: nil, now: now), "not waiting")
        XCTAssertFalse(WatchLogic.stuck(waitingSince: now.addingTimeInterval(-10), now: now), "waiting 10 s: a slow segment")
        XCTAssertTrue(WatchLogic.stuck(waitingSince: now.addingTimeInterval(-15), now: now))
        XCTAssertTrue(WatchLogic.stuck(waitingSince: now.addingTimeInterval(-40), now: now))
    }

    func testADoubleTapGoesBackOnTheStartSideAndForwardOnTheEndSide() {
        XCTAssertEqual(WatchLogic.doubleTapSeek(onStartSide: true), -10)
        XCTAssertEqual(WatchLogic.doubleTapSeek(onStartSide: false), 10)
        XCTAssertEqual(WatchLogic.controlsHideAfter, .seconds(4))
    }

    func testUpNextIsDueInTheLastTenSecondsOrAtTheEnd() {
        XCTAssertFalse(WatchLogic.upNextDue(position: 100, duration: 3120, ended: false))
        XCTAssertFalse(WatchLogic.upNextDue(position: 3109, duration: 3120, ended: false))
        XCTAssertTrue(WatchLogic.upNextDue(position: 3110, duration: 3120, ended: false))
        XCTAssertTrue(WatchLogic.upNextDue(position: 0, duration: .nan, ended: true))
        XCTAssertFalse(WatchLogic.upNextDue(position: 0, duration: .nan, ended: false), "no length yet")
    }

    /// The home lists a series' episodes newest first in places (the mock's Seerah does);
    /// the next one follows the seasons' order.
    func testTheNextEpisodeFollowsTheSeasonsOrder() {
        let seerah = [WatchLogic.EpisodeKey(id: "v3", season: 1, episode: 12, absolute: nil),
                      WatchLogic.EpisodeKey(id: "v2", season: 1, episode: 11, absolute: nil),
                      WatchLogic.EpisodeKey(id: "v1", season: 1, episode: 10, absolute: nil)]
        XCTAssertEqual(WatchLogic.next(after: "v1", in: seerah), "v2")
        XCTAssertEqual(WatchLogic.next(after: "v2", in: seerah), "v3")
        XCTAssertNil(WatchLogic.next(after: "v3", in: seerah), "the last has none")
        XCTAssertNil(WatchLogic.next(after: "v9", in: seerah), "not an episode of it")
        let seasons = [WatchLogic.EpisodeKey(id: "s2e1", season: 2, episode: 1, absolute: nil),
                       WatchLogic.EpisodeKey(id: "s1e2", season: 1, episode: 2, absolute: nil),
                       WatchLogic.EpisodeKey(id: "s1e1", season: 1, episode: 1, absolute: nil)]
        XCTAssertEqual(WatchLogic.next(after: "s1e2", in: seasons), "s2e1")
        let unnumbered = [WatchLogic.EpisodeKey(id: "a", season: nil, episode: nil, absolute: nil),
                          WatchLogic.EpisodeKey(id: "b", season: nil, episode: nil, absolute: nil)]
        XCTAssertEqual(WatchLogic.next(after: "a", in: unnumbered), "b", "the home's order for the rest")
    }

    func testQualityOffersTheLadderElseTheServicesRungs() {
        let ladder = [WatchLogic.Rung(height: 720, kbps: 3000), WatchLogic.Rung(height: 1080, kbps: 6000), WatchLogic.Rung(height: 0, kbps: 0)]
        XCTAssertEqual(WatchLogic.qualities(ladder).map(\.height), [1080, 720])
        XCTAssertEqual(WatchLogic.qualities([]).map(\.kbps), [6000, 3000, 1200], "direct play: the service's own rungs")
    }

    /// The sound's codec is any enabled track's known one: around an audio switch the item
    /// lists a track not read yet (no formats) before the one that plays (PR #595's iPad run
    /// reported "audio" for 30 s after a switch to Arabic, the sound on).
    func testTheSoundsCodecIsAnyEnabledTracksKnownOne() {
        XCTAssertNil(PlayerTracks.codec(of: []), "no audio track enabled")
        XCTAssertEqual(PlayerTracks.codec(of: [[kAudioFormatMPEG4AAC]]), "aac")
        XCTAssertEqual(PlayerTracks.codec(of: [[], [kAudioFormatMPEG4AAC]]), "aac", "a track not read yet, then the one that plays")
        XCTAssertEqual(PlayerTracks.codec(of: [[0x7A7A_7A7A], [kAudioFormatEnhancedAC3]]), "eac3")
        XCTAssertEqual(PlayerTracks.codec(of: [[]]), "audio", "a track whose format is not read yet")
        XCTAssertEqual(PlayerTracks.codec(of: [[0x7A7A_7A7A]]), "audio", "a format not listed")
    }

    // MARK: the tracks, on a real stream

    /// FR-39a: a stream with sound (the home's shape: AAC in the video's segments) plays with
    /// an audio track enabled, AAC, the player audible, and the time moving.
    func testAStreamWithSoundPlaysWithSoundSelected() async throws {
        let (player, item) = try await play()
        let audible = await PlayerTracks.groups(of: item).audible
        PlayerTracks.ensureAudio(player: player, item: item, audible: audible)
        XCTAssertNotNil(audible.flatMap { item.currentMediaSelection.selectedMediaOption(in: $0) }, "its sound is selected")
        let sound = PlayerTracks.sound(player: player, item: item)
        XCTAssertEqual(sound.codec, "aac")
        XCTAssertTrue(sound.audible)
        XCTAssertFalse(player.isMuted)
        XCTAssertGreaterThan(player.volume, 0)
        let started = player.currentTime().seconds
        try await wait("the time moves") { player.currentTime().seconds > started + 0.5 }
    }

    /// Sound turned off every way it can be (the track disabled, the player muted, its volume
    /// at 0) comes back on: ensureAudio's work is what makes it audible. (The muxed sound's
    /// group says it allows an empty selection, but AVFoundation keeps its one option
    /// selected; ensureAudio still selects it if it ever is not.)
    func testSoundComesBackOnWhenTurnedOff() async throws {
        let (player, item) = try await play()
        let groups = await PlayerTracks.groups(of: item)
        let audible = try XCTUnwrap(groups.audible, "the muxed sound's group")
        for t in item.tracks where t.assetTrack?.mediaType == .audio { t.isEnabled = false }
        player.isMuted = true
        player.volume = 0
        XCTAssertNil(PlayerTracks.sound(player: player, item: item).codec, "no audio track on before")
        XCTAssertFalse(PlayerTracks.sound(player: player, item: item).audible, "muted before")
        XCTAssertTrue(PlayerTracks.ensureAudio(player: player, item: item, audible: audible))
        XCTAssertNotNil(item.currentMediaSelection.selectedMediaOption(in: audible), "its sound is selected")
        try await wait("an AAC track enabled again") { PlayerTracks.sound(player: player, item: item).codec == "aac" }
        XCTAssertTrue(PlayerTracks.sound(player: player, item: item).audible)
        XCTAssertFalse(PlayerTracks.ensureAudio(player: player, item: item, audible: audible), "nothing more to do")
    }

    /// The legible group is the home's subtitle renditions, by their names; one is chosen and
    /// read back; Off selects none.
    func testTheSubtitleRenditionsAreListedAndAChoiceIsReadBack() async throws {
        let (_, item) = try await play()
        let groups = await PlayerTracks.groups(of: item)
        let subs = PlayerTracks.choices(in: groups.legible)
        XCTAssertEqual(subs.map(\.name), ["English", "Arabic"], "named in the app's language")
        XCTAssertEqual(subs.map(\.language), ["en", "ar"])
        XCTAssertEqual(PlayerTracks.choices(in: groups.audible).count, 1, "the sound is in the video's segments: one audio option")
        let ar = try XCTUnwrap(PlayerTracks.choice(in: groups.legible, language: "ar"))
        PlayerTracks.select(ar, in: groups.legible, of: item)
        XCTAssertEqual(PlayerTracks.selected(in: groups.legible, of: item), ar)
        PlayerTracks.select(nil, in: groups.legible, of: item)
        XCTAssertNil(PlayerTracks.selected(in: groups.legible, of: item))
    }

    /// Two audio renditions: both listed, one selected at start; switching selects the other
    /// and the sound stays on.
    func testAudioTracksSwitchAndTheSoundStaysOn() async throws {
        home.streamMaster = "master-two-audio"
        let (player, item) = try await play()
        let groups = await PlayerTracks.groups(of: item)
        let audio = PlayerTracks.choices(in: groups.audible)
        XCTAssertEqual(audio.map(\.language), ["en", "ar"])
        PlayerTracks.ensureAudio(player: player, item: item, audible: groups.audible)
        XCTAssertNotNil(PlayerTracks.selected(in: groups.audible, of: item), "an audio track is selected")
        let ar = try XCTUnwrap(PlayerTracks.choice(in: groups.audible, language: "ar"))
        PlayerTracks.select(ar, in: groups.audible, of: item)
        PlayerTracks.ensureAudio(player: player, item: item, audible: groups.audible)
        XCTAssertEqual(PlayerTracks.selected(in: groups.audible, of: item)?.language, "ar")
        // as the app does: the selection's change notification makes sure of the sound again
        try await wait("the switch lands", timeout: 20) { item.tracks.contains { $0.isEnabled && $0.assetTrack?.mediaType == .audio } }
        PlayerTracks.ensureAudio(player: player, item: item, audible: groups.audible)
        var codec: String?
        let end = Date().addingTimeInterval(20)
        while codec != "aac", Date() < end {
            codec = await PlayerTracks.loadedSound(player: player, item: item).codec
            if codec != "aac" { try await Task.sleep(for: .milliseconds(100)) }
        }
        XCTAssertEqual(codec, "aac", "the Arabic sound plays")
        XCTAssertTrue(PlayerTracks.sound(player: player, item: item).audible)
    }

    // MARK: helpers

    /// MockHome's stream, playing.
    private func play() async throws -> (AVPlayer, AVPlayerItem) {
        let url = try XCTUnwrap(URL(string: "\(home.url)/stream/ss-1/manifest.m3u8?sig=uitest-signature"))
        let item = AVPlayerItem(url: url)
        let player = AVPlayer(playerItem: item)
        addTeardownBlock { @MainActor in player.pause() }
        player.play()
        try await wait("the stream is ready", timeout: 20) { item.status == .readyToPlay || item.status == .failed }
        XCTAssertEqual(item.status, .readyToPlay, item.error.map { "\($0)" } ?? "")
        try await wait("its tracks are known", timeout: 20) { item.tracks.contains { $0.assetTrack?.mediaType == .audio } }
        return (player, item)
    }

    private func wait(_ what: String, timeout: TimeInterval = 10, until done: () -> Bool) async throws {
        let end = Date().addingTimeInterval(timeout)
        while !done() {
            if Date() > end {
                XCTFail("timed out: \(what)")
                return
            }
            try await Task.sleep(for: .milliseconds(100))
        }
    }
}
