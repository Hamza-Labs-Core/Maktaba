import AVKit
import MaktabaKit
import SwiftUI

/// What is playing, for the whole app: the watch screen may be full screen (folded, open
/// flat, the iPad, the Mac) or one half of a half-folded iPhone Duo with the library on the
/// other, so the stream lives here and not in a view.
///
/// Opens a stream session at the resume position, plays its signed URL, reports progress
/// every 10 s, on pause and on stop (and `completed` at the end), reopens before the signed
/// URL ends (the home signs it for the video's length plus an hour, so normally never), when
/// the item fails, or at another quality (`max_bitrate_kbps`), and closes the session when
/// stopped. It lists the item's subtitles and audio and keeps the person's choice for the
/// next video, makes sure a video with sound plays with sound (FR-39a), and finds the
/// series' next episode for Up next (story 48.23).
///
/// Story 48.24: AirPlay takes the video to a receiver, which is handed the session's signed
/// URL (it can't reach the loopback proxy) at the same position, and the proxy again when
/// AirPlay ends; progress keeps reporting from here. Picture in Picture floats the picture
/// (the button, or going home while it plays). Now Playing shows the title, poster and
/// position, and the remote commands (play, pause, ±10 s, next, the scrubber) drive it.
@MainActor @Observable
final class Playback {
    let client: APIClient
    private(set) var video: Video?
    private(set) var player: AVPlayer?
    /// Why it does not play (the watch screen says it and what can be done).
    private(set) var failure: HomeFailure?
    /// The home has no transcode slot free (503 `transcode_busy`, story 59.05), or cannot
    /// read a restricted account's watching time right now (503 `parental_time_unknown`,
    /// story 57.14): no error screen and no time's-up screen, the sentence shown while the
    /// stream is asked for again as the home's `Retry-After` says.
    private(set) var waiting: HomeFailure?
    /// Epic 57: the home stopped this video for a parental reason. Playback stopped, the
    /// home holds the place of the last post it took or refused for time (`savedPosition`),
    /// and nothing is tried again until `resumeAfterStop`.
    private(set) var parentalStop: ParentalStop?
    /// Whether this sign-in is under a parental limit (the session's store): a "not found"
    /// then may be a block.
    @ObservationIgnored var limited: () -> Bool = { false }

    enum ParentalStop: Equatable {
        /// The home no longer gives the video `id` (404 on open or on a renewal, or for the
        /// queue's or the series' next one): the blocked page. `midPlay`: it was playing.
        case blocked(id: String, midPlay: Bool)
        /// 403 `parental-time`: the time's-up screen, with where the video stopped.
        case time(TimeUp, position: Double)
    }

    /// The place the home holds for this video: the position of the last progress post it
    /// took, or of the one it refused because the time is over (the home stores that post's
    /// position before it answers 403), else where the stream was opened. After a stop for
    /// time this is what "saved at" says: another device resumes here.
    private(set) var savedPosition: Double?

    // What the controls show.
    private(set) var position: Double = 0
    private(set) var duration: Double = 0
    private(set) var isPlaying = false
    /// The video played to its end.
    private(set) var ended = false
    /// The item's subtitle renditions and audio tracks, and the ones selected (no subtitle:
    /// off).
    private(set) var subtitles: [TrackChoice] = []
    private(set) var audioTracks: [TrackChoice] = []
    private(set) var subtitle: TrackChoice?
    private(set) var audio: TrackChoice?
    /// The sound the item plays now: its audio codec (nil: no audio track enabled) and
    /// whether the player is audible.
    private(set) var sound = Sound(codec: nil, audible: true)
    /// The qualities the session offers (empty: direct play).
    private(set) var ladder: [Rendition] = []
    /// The quality asked for (nil: Auto, the home chooses).
    private(set) var maxBitrateKbps: Int?
    /// The series' next episode (nil: not an episode, or the last).
    private(set) var next: Episode?
    /// Seconds left before Up next plays `next` (nil: not counting down).
    private(set) var upNextCountdown: Int?
    /// Settings' playback values, read when a video starts.
    private(set) var settings = PlaybackSettings.read()
    /// Story 58.06: the video plays on a Cast receiver, which reports its progress itself with
    /// the cast grant; this device posts none meanwhile (two reporters would double or move
    /// Continue Watching back) and its own stream waits paused.
    private(set) var castActive = false

    struct Sound: Equatable {
        let codec: String?
        let audible: Bool
    }

    private var session: String?
    /// Where the stream was last opened (the home's resume point, or the place asked for).
    private var openedFrom: Double?
    /// When to reopen: just before the signed URL ends.
    private var renewAt = Date.distantFuture
    private var openedAt = Date.distantPast
    private var running: Task<Void, Never>?
    /// Where the player reads the stream from: every request reaches the home with the
    /// user's token as it is now (see `StreamProxy`).
    private var proxy: StreamProxy?
    /// The session's signed URL and the proxy's address for it: a receiver plays the first,
    /// the device the second.
    private var signedURL: URL?
    private var proxiedURL: URL?
    /// Where the picture plays (story 48.24).
    private(set) var routes = RouteSwitch()
    private var externalWatcher: ExternalPlaybackWatcher?
    /// Picture in Picture for the watch screen's picture.
    let pip = PictureInPicture()
    /// What Now Playing was last told, and when (it moves the position on by itself).
    private var nowShown: (item: NowPlayingItem, at: Date)?

    private var timeObserver: Any?
    private var itemObservers: [NSObjectProtocol] = []
    /// The audio session's interruptions (a call, an alarm, a Screen Time shield): each sends
    /// the position at once (story 57.09).
    private var interruptionObserver: NSObjectProtocol?
    /// The app leaving and taking the front, heard by the playback itself (story 57.09): a
    /// player shown by any screen (the watch screen, the Libraries section's cover) sends
    /// its position and pauses under a limit without that screen having to say so.
    private var sceneObservers: [NSObjectProtocol] = []
    private let center: NotificationCenter
    private let sceneNotes: SceneNotes
    /// Where the app was at the last `sceneChanged` (a screen and the notifications both
    /// say it: the second saying changes nothing).
    private var scene = Scene.active

    private enum Scene { case active, inactive, background }

    /// The notifications that say the app left the front, went to the background, came back.
    struct SceneNotes {
        var inactive: Notification.Name?
        var background: Notification.Name?
        var active: Notification.Name?

        /// The system's: iPhone and iPad (the Mac has no background to be held in; its
        /// watch screen says when the window leaves the front).
        static var platform: SceneNotes {
            #if os(iOS)
            SceneNotes(inactive: UIApplication.willResignActiveNotification,
                       background: UIApplication.didEnterBackgroundNotification,
                       active: UIApplication.didBecomeActiveNotification)
            #else
            SceneNotes()
            #endif
        }
    }
    private var statusObservation: NSKeyValueObservation?
    private var groupsTask: Task<Void, Never>?
    /// Loading the enabled audio tracks' formats, when they were not read yet.
    private var soundTask: Task<Void, Never>?
    private var legible: AVMediaSelectionGroup?
    private var audible: AVMediaSelectionGroup?
    private var nextTask: Task<Void, Never>?
    private var upNextTask: Task<Void, Never>?
    /// The person cancelled Up next for this video.
    private var upNextDismissed = false
    private var reportedCompleted = false
    /// The person's subtitle choice, kept for the next video by language: nil, not chosen
    /// (Settings decide); `.some(nil)`, off.
    private var chosenSubtitle: String??
    /// The person's audio choice, by language.
    private var chosenAudio: String?

    init(client: APIClient, center: NotificationCenter = .default, scene: SceneNotes = .platform) {
        self.client = client
        self.center = center
        sceneNotes = scene
    }

    /// Plays `v` from `start` seconds; nil: where the person left off (a finished video
    /// starts again).
    func play(_ v: Video, from start: Double? = nil) {
        if v.id == video?.id, failure == nil, parentalStop == nil {
            if let start { seek(to: start) }
            return
        }
        stop()
        PlaybackAudio.activate()
        video = v
        settings = PlaybackSettings.read()
        running = Task { await run(v, asked: start) }
    }

    /// Opens the same video again, after it failed.
    func retry() {
        if let v = video { play(v) }
    }

    /// Play or pause what is open (the keyboard's space, the Playback menu, the controls).
    /// Pausing reports the position.
    func togglePause(from source: String = "controls") {
        guard let p = player else {
            Log.ui.info("playback: \(source, privacy: .public) play/pause with no player")
            return
        }
        // each play and pause in the app's log: a UI test's pause that did not hold (an iPhone
        // run of #608 played on after the tap) says there what played it again
        let rate = p.rate, at = p.currentTime().seconds
        Log.ui.info("playback: \(source, privacy: .public) \(rate > 0 ? "pause" : "play", privacy: .public) at \(at, privacy: .public) s")
        if p.rate > 0 {
            p.pause()
            report()
        } else {
            if ended { seek(to: 0) }
            p.play()
            // the stream's heartbeat at once (57.14): the home serves a kept account's stream
            // only while progress is posted, and none was while it was paused
            report()
        }
        isPlaying = p.rate > 0
    }

    /// The app left the front (story 57.09): the position is sent at once, so a Screen Time
    /// shield, downtime or the app's end loses nothing. In the background a sign-in under a
    /// parental limit also pauses and ends Picture in Picture, and nothing here plays again
    /// until the person taps play (a shielded app must not start itself again); other
    /// sign-ins keep playing in the background as before (story 48.24).
    func sceneChanged(active: Bool, background: Bool) {
        let now: Scene = active ? .active : background ? .background : .inactive
        guard now != scene else { return }
        scene = now
        guard !active, let p = player else { return }
        report()
        guard background, limited(), p.rate > 0 || pip.isActive else { return }
        Log.ui.info("playback: paused in the background under a parental limit")
        p.pause()
        pip.stop()
        isPlaying = false
    }

    /// The home allows watching again (a parent gave more time, or allowed the video): the
    /// video opens where it stopped.
    func resumeAfterStop() {
        guard let stop = parentalStop, let current = video else { return }
        // the next video was the blocked one: a parent allowed it, so it plays
        if case let .blocked(id, _) = stop, id != current.id {
            // the stop stays until the home gives the video: one that still cannot be read
            // (still hidden, the home not reached) leaves the blocked page, never a spinner
            Task {
                do {
                    let v = try await client.video(id)
                    guard parentalStop == stop else { return }
                    play(v)
                } catch {
                    Log.failure("open the video a parent allowed", error)
                }
            }
            return
        }
        let at: Double = if case let .time(_, position) = stop { position } else { position }
        parentalStop = nil
        failure = nil
        running?.cancel()
        running = Task {
            await openWhenFree(from: at)
            guard player != nil else { return }
            await watch()
        }
    }

    /// A cast takes the video (story 58.06): the position is sent once, the stream waits
    /// paused and the 10 s reports stop. Returns where it was: the receiver starts there.
    func handOverToCast() -> Double {
        let at = player?.currentTime().seconds ?? position
        report()
        castActive = true
        running?.cancel()
        running = nil
        player?.pause()
        isPlaying = false
        Log.ui.info("playback: handed over to a cast")
        return at.isFinite ? max(0, at) : position
    }

    /// The cast ended: the video is back here, paused where the TV was (`at`). nil: the cast
    /// never began (refused, failed), so it plays on from where this device stopped. It
    /// reports again from here.
    func returnFromCast(at: Double?) {
        guard castActive else { return }
        castActive = false
        let from = at ?? position
        Log.ui.info("playback: back from a cast")
        running?.cancel()
        running = Task {
            await open(from: from, playing: at == nil)
            guard player != nil else { return }
            await watch()
        }
    }

    /// Moves `seconds` forward (back when negative), not before the start nor past the end.
    func skip(by seconds: Double) {
        guard let p = player else { return }
        let now = p.currentTime().seconds
        guard now.isFinite else { return }
        seek(to: now + seconds)
    }

    /// Goes to `seconds`, not before the start nor past the end.
    func seek(to seconds: Double) {
        guard let p = player else { return }
        var to = max(0, seconds)
        if let end = p.currentItem?.duration.seconds, end.isFinite { to = min(to, max(0, end - 1)) }
        position = to
        ended = false
        p.seek(to: CMTime(seconds: to, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
    }

    /// Selects a subtitle rendition (nil: off), kept for the next video.
    func selectSubtitle(_ choice: TrackChoice?) {
        guard let item = player?.currentItem else { return }
        PlayerTracks.select(choice, in: legible, of: item)
        chosenSubtitle = .some(choice.map { $0.language ?? $0.name })
        refreshTracks()
    }

    /// Selects an audio track, kept for the next video.
    func selectAudio(_ choice: TrackChoice) {
        guard let p = player, let item = p.currentItem else { return }
        PlayerTracks.select(choice, in: audible, of: item)
        chosenAudio = choice.language ?? choice.name
        PlayerTracks.ensureAudio(player: p, item: item, audible: audible)
        refreshTracks()
    }

    /// Reopens at the same position with the quality capped at `kbps` (nil: Auto), paused if
    /// it was paused.
    func setQuality(_ kbps: Int?) {
        guard kbps != maxBitrateKbps, let p = player else { return }
        maxBitrateKbps = kbps
        let again = reopening(p, fallback: position)
        running?.cancel()
        running = Task {
            await open(from: again.at, playing: again.playing)
            guard player != nil else { return }
            await watch()
        }
    }

    /// Plays the next episode now (Up next's Play now, the Next button).
    func playNext() {
        guard let n = next else { return }
        upNextTask?.cancel()
        upNextTask = nil
        upNextCountdown = nil
        Task {
            do {
                let v = try await client.video(n.videoID)
                PlayerQueues.played(v.id, client)
                play(v)
            } catch {
                // a queue or a series made before the limit tightened: its next video is one
                // the home no longer gives (57.05: the blocked page, not an error)
                if PlaybackVerdict.open(error, limited: limited()) == .blocked {
                    stopForParental(.blocked(id: n.videoID, midPlay: false))
                } else {
                    failure = HomeFailure(error)
                }
            }
        }
    }

    /// Up next's Cancel: this video stops at its end.
    func cancelUpNext() {
        upNextTask?.cancel()
        upNextTask = nil
        upNextCountdown = nil
        upNextDismissed = true
    }

    /// The system interrupted the sound (iPhone and iPad): the position is sent now, not at
    /// the next 10 s, so nothing is lost if the app does not come back.
    private func observeInterruptions() {
        #if os(iOS)
        guard interruptionObserver == nil else { return }
        interruptionObserver = NotificationCenter.default.addObserver(
            forName: AVAudioSession.interruptionNotification, object: nil, queue: .main) { [weak self] note in
            let began = (note.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt) == AVAudioSession.InterruptionType.began.rawValue
            guard began else { return }
            MainActor.assumeIsolated { self?.interrupted() }
        }
        #endif
    }

    private func interrupted() {
        Log.ui.info("playback: interrupted by the system; position sent")
        report()
        if let p = player, p.rate == 0, isPlaying { isPlaying = false }
    }

    private func endInterruptions() {
        if let interruptionObserver { NotificationCenter.default.removeObserver(interruptionObserver) }
        interruptionObserver = nil
        for o in sceneObservers { center.removeObserver(o) }
        sceneObservers = []
    }

    /// Hears the app leave and take the front while a player is open.
    private func observeScene() {
        guard sceneObservers.isEmpty else { return }
        // a player opens with the app at the front: what was heard for the last video (it
        // may have been stopped in the background, unheard since) says nothing of now
        scene = .active
        let heard: [(Notification.Name?, Bool, Bool)] = [
            (sceneNotes.inactive, false, false), (sceneNotes.background, false, true), (sceneNotes.active, true, false),
        ]
        for (name, active, background) in heard {
            guard let name else { continue }
            sceneObservers.append(center.addObserver(forName: name, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.sceneChanged(active: active, background: background) }
            })
        }
    }

    func stop() {
        running?.cancel()
        running = nil
        endInterruptions()
        groupsTask?.cancel()
        nextTask?.cancel()
        upNextTask?.cancel()
        unobserveItem()
        if let timeObserver, let player { player.removeTimeObserver(timeObserver) }
        timeObserver = nil
        player?.pause()
        if let session {
            let at = player?.currentTime().seconds ?? 0
            // while cast, the receiver's position is the one the home keeps
            let reports = CastProgress.phoneReports(casting: castActive)
            Task { [client] in
                if reports, at.isFinite, at > 0 { await client.reportProgress(session: session, positionSec: Int(at)) }
                await client.closeStream(session)
            }
        }
        session = nil
        castActive = false
        openedFrom = nil
        savedPosition = nil
        player = nil
        failure = nil
        waiting = nil
        parentalStop = nil
        video = nil
        proxy?.stop()
        proxy = nil
        signedURL = nil
        proxiedURL = nil
        externalWatcher = nil
        routes = RouteSwitch()
        pip.stop()
        NowPlaying.shared.end(self)
        nowShown = nil
        position = 0
        duration = 0
        isPlaying = false
        ended = false
        subtitles = []
        audioTracks = []
        subtitle = nil
        audio = nil
        sound = Sound(codec: nil, audible: true)
        ladder = []
        maxBitrateKbps = nil
        next = nil
        upNextCountdown = nil
        upNextDismissed = false
        reportedCompleted = false
        legible = nil
        audible = nil
    }

    /// The state the UI tests read (the video's accessibility value in a Debug build).
    var testState: String {
        [
            "position=\(Int(position.isFinite ? position : 0))",
            "playing=\(isPlaying)",
            "sound=\(sound.codec ?? "none")",
            "audible=\(sound.audible)",
            "audio=\(audio?.language ?? audio?.name ?? "-")",
            "subtitle=\(subtitle?.language ?? subtitle?.name ?? "off")",
            "quality=\(maxBitrateKbps.map(String.init) ?? "auto")",
            "ended=\(ended)",
            "route=\(routes.route.rawValue)",
            "pip=\(pip.isActive)",
            "cast=\(castActive)",
            "pipSupported=\(PictureInPicture.isSupported)",
            "pipPossible=\(pip.isPossible)",
            "backgroundAudio=\(PlaybackAudio.declaredInInfoPlist)",
            "backgroundPolicy=\(player?.audiovisualBackgroundPlaybackPolicy == .continuesIfPossible ? "continues" : "pauses")",
        ].joined(separator: ";")
    }

    /// A player that plays on when the person leaves the app without Picture in Picture.
    static func newPlayer() -> AVPlayer {
        let p = AVPlayer()
        p.keepPlayingInBackground()
        return p
    }

    private func run(_ v: Video, asked: Double?) async {
        var state = v.playbackState
        if asked == nil, state == nil {
            // a list's video carries no position: the video's own page does
            state = try? await client.video(v.id).playbackState
        }
        guard !Task.isCancelled else { return }
        let start = WatchLogic.start(asked: asked, resume: state?.positionSec, completed: state?.completed ?? false)
        await openWhenFree(from: start)
        guard player != nil else { return }
        findNext(after: v)
        await watch()
    }

    /// Opens (or reopens) the stream at `start` seconds, through the app's stream proxy: the
    /// home's web port wants the user's token on every `/stream/` request, and AVPlayer
    /// cannot renew one mid-video. A reopen of a paused video stays paused (`playing` false):
    /// it never plays, and `isPlaying` says so (a paused player's time observer does not tick,
    /// so a pause after the open left it saying playing).
    private func open(from start: Double, playing: Bool = true) async {
        guard let video else { return }
        do {
            let s = try await client.openStream(video: video.id, startSec: Int(start), maxBitrateKbps: maxBitrateKbps)
            guard let url = s.playbackURL(home: client.home) else {
                failure = HomeFailure(kind: .server, detail: String(localized: "The home gave no address to play."))
                return
            }
            let auth = try await client.playbackHeaders()
            let proxy = try self.proxy ?? StreamProxy(client: client)
            self.proxy = proxy
            try await proxy.start()
            guard let local = proxy.local(url) else {
                failure = HomeFailure(kind: .server, detail: String(localized: "Could not start playback on this device."))
                return
            }
            if let old = session, old != s.sessionID {
                Task { [client] in await client.closeStream(old) }
            }
            session = s.sessionID
            openedFrom = start
            waiting = nil
            ladder = s.ladder ?? []
            try await client.waitUntilPlayable(url, headers: auth.headers)
            try Task.checkCancellation()
            openedAt = .now
            renewAt = (s.expires ?? .distantFuture).addingTimeInterval(-60)
            signedURL = url
            proxiedURL = local
            await load(AVPlayerItem(url: RouteURL.video(signed: url, proxied: local, route: routes.route)),
                       at: start, playing: playing)
        } catch is CancellationError {
            return
        } catch {
            let f = HomeFailure(error)
            // Epic 57: the account's time is over (403 `parental-time`), or the home no
            // longer gives this video to a limited sign-in (404, at an open or a renewal)
            switch PlaybackVerdict.open(error, limited: limited()) {
            case let .timeUp(up):
                stopForParental(.time(up, position: start))
                return
            case .blocked:
                stopForParental(.blocked(id: video.id, midPlay: player != nil))
                return
            case .other, .reopen:
                break
            }
            if f.kind.waitRetrySeconds != nil {
                // what plays now plays on (a reopen: the 10 s watch asks again); with nothing
                // playing, the sentence shows while `openWhenFree` asks again
                if player != nil { return }
                waiting = f
                return
            }
            player = nil
            waiting = nil
            failure = f
        }
    }

    /// Opens at `start`; while the home has no transcode slot free, or cannot read the
    /// account's watching time (`waiting`), asks again after its `Retry-After` until it can
    /// (stories 59.05 and 57.14).
    private func openWhenFree(from start: Double) async {
        while !Task.isCancelled {
            await open(from: start)
            if failure != nil { waiting = nil }
            guard let after = waiting?.kind.waitRetrySeconds else { return }
            do { try await Task.sleep(for: .seconds(after)) } catch { return }
        }
    }

    /// Where a reopen of what `p` plays starts, and whether it plays: where it is (else
    /// `fallback`), playing only if it was (`setQuality`'s promise, and the renew's).
    func reopening(_ p: AVPlayer, fallback: Double) -> (at: Double, playing: Bool) {
        let at = p.currentTime().seconds
        return (at.isFinite ? at : fallback, p.rate > 0)
    }

    /// Puts `item` in the player at `start`, playing or not (`playing`): a paused reopen
    /// never plays, and `isPlaying` says so from the start (a paused player's time observer
    /// does not tick, so a pause after a play left it saying playing).
    func load(_ item: AVPlayerItem, at start: Double, playing: Bool) async {
        let p = player ?? Self.newPlayer()
        observe(item, of: p)
        p.replaceCurrentItem(with: item)
        if start > 0 { await p.seek(to: CMTime(seconds: start, preferredTimescale: 600)) }
        position = start
        if player == nil {
            player = p
            observeTime(of: p)
            startRoutes(of: p)
            startNowPlaying()
            observeInterruptions()
            observeScene()
        }
        ended = false
        Log.ui.info("playback: opened at \(start, privacy: .public) s, playing \(playing, privacy: .public)")
        if playing { p.play() }
        isPlaying = playing
    }

    /// Every 10 s while playing: the position, so "continue watching" resumes here; a reopen
    /// where it is before the URL ends, or when the item failed (not again within 30 s of
    /// opening: then it shows why).
    private func watch() async {
        waitingSince = nil
        while !Task.isCancelled {
            try? await Task.sleep(for: .seconds(10))
            guard !Task.isCancelled, let p = player, session != nil else { return }
            if p.rate > 0 { report() }
            if stuck(waiting: p.timeControlStatus == .waitingToPlayAtSpecifiedRate, at: .now) {
                let at = p.currentTime().seconds
                await open(from: at.isFinite ? at : position)
                continue
            }
            let failed = p.currentItem?.status == .failed
            if failed, Date.now.timeIntervalSince(openedAt) < 30 {
                p.pause()
                player = nil
                failure = p.currentItem?.error.map(HomeFailure.init) ?? HomeFailure(kind: .server, detail: String(localized: "The video stopped playing."))
                return
            }
            if failed || Date.now >= renewAt {
                let again = reopening(p, fallback: 0)
                await open(from: again.at, playing: again.playing)
            }
        }
    }

    /// When the player was first seen waiting by `stuck` (nil: not waiting at the last look).
    private var waitingSince: Date?

    /// One look at the player (the 10 s watch's): whether it is `waiting` at `now`. True once
    /// it has been waiting `WatchLogic.stuckAfter` since it was first seen waiting, and then
    /// it starts over, for the reopen it calls for. After an audio switch on a loaded CI
    /// simulator the player stopped at 1257 s for good, its rate 1 (PR #614's iPhone run).
    func stuck(waiting: Bool, at now: Date) -> Bool {
        guard waiting else {
            waitingSince = nil
            return false
        }
        let since = waitingSince ?? now
        waitingSince = since
        guard WatchLogic.stuck(waitingSince: since, now: now) else { return false }
        waitingSince = nil
        return true
    }

    /// Reports where it is now (best effort): the resume point, and for an account whose time
    /// the home keeps the stream's heartbeat, posted every 10 s while it plays (the home wants
    /// one at least every 60 s). The home's refusal of it stops the video (`progressRefused`).
    private func report(completed: Bool = false) {
        guard CastProgress.phoneReports(casting: castActive), let session, let p = player else { return }
        var at = p.currentTime().seconds
        if completed, duration > 0 { at = duration }
        guard at.isFinite, at > 0 || completed else { return }
        Task { [client, weak self] in
            do {
                try await client.postProgress(session: session, positionSec: Int(at), completed: completed)
                self?.progressTaken(session: session, at: at)
            } catch {
                self?.progressRefused(error, session: session, at: at, sent: Double(Int(at)))
            }
        }
    }

    /// The home took a progress post of `taken`: its place for this video is `at`.
    func progressTaken(session taken: String, at: Double) {
        guard session == taken else { return }
        savedPosition = at
    }

    /// This playback as if `open` had opened `session` for `video` (the tests' seam: a
    /// stream's refusals are matched by its session, which only the home gives).
    func adopt(session: String, video: Video? = nil, openedFrom: Double? = nil) {
        self.session = session
        self.openedFrom = openedFrom
        if let video { self.video = video }
    }

    /// The home refused a progress post. `parental-time`: the time is over, so the video
    /// stops on the time's-up screen; when the reason is `no_heartbeat` (the stream sent no
    /// progress for a minute: a long pause, the app held in the background) the stream is
    /// opened again once, and the home then says whether there is time. Any other failure is
    /// left to the next post. `sent` is the position the refused post carried (nil: `at`):
    /// the home stored it when it refused for time, so "saved at" says it.
    func progressRefused(_ error: Error, session refused: String, at: Double, sent: Double? = nil) {
        guard session == refused, parentalStop == nil else { return }
        switch PlaybackVerdict.progress(error, limited: limited()) {
        case .other:
            return
        case .blocked:
            // the video was hidden from the account while it played: the stream has ended
            if let id = video?.id { stopForParental(.blocked(id: id, midPlay: true)) }
        case .reopen:
            guard let p = player else { return }
            let again = reopening(p, fallback: at)
            running?.cancel()
            running = Task {
                await open(from: again.at, playing: again.playing)
                guard player != nil else { return }
                await watch()
            }
        case let .timeUp(up):
            if up.keepsRefusedPosition { savedPosition = sent ?? at }
            stopForParental(.time(up, position: at))
        }
    }

    /// Stops for a parental reason: the player goes, and the screen shows the blocked page or
    /// time's up. A stop for time sends no further position: the place the home holds is the
    /// last post it took, or the one it refused for time (`savedPosition`), and the time's-up
    /// screen says that one.
    func stopForParental(_ stop: ParentalStop) {
        let at = player?.currentTime().seconds ?? position
        if let session, at.isFinite, at > 0, case .blocked(_, true) = stop {
            Task { [client] in await client.reportProgress(session: session, positionSec: Int(at)) }
        }
        if savedPosition == nil { savedPosition = openedFrom }
        var kept = stop
        if case let .time(up, start) = stop, at.isFinite, at > start { kept = .time(up, position: at) }
        Log.ui.info("playback: stopped by the home for a parental reason")
        endInterruptions()
        unobserveItem()
        if let timeObserver, let player { player.removeTimeObserver(timeObserver) }
        timeObserver = nil
        player?.pause()
        player = nil
        pip.stop()
        NowPlaying.shared.end(self)
        nowShown = nil
        waiting = nil
        failure = nil
        isPlaying = false
        upNextTask?.cancel()
        upNextCountdown = nil
        parentalStop = kept
    }

    // MARK: the item

    private func observeTime(of p: AVPlayer) {
        timeObserver = p.addPeriodicTimeObserver(forInterval: CMTime(seconds: 0.5, preferredTimescale: 600), queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.tick() }
        }
    }

    /// Twice a second: the position, the length, playing or not, the sound, Up next.
    private func tick() {
        guard let p = player, let item = p.currentItem else { return }
        let at = p.currentTime().seconds
        if at.isFinite, abs(at - position) >= 0.25 { position = at }
        let length = item.duration.seconds.isFinite ? item.duration.seconds : (video?.durationSec ?? 0)
        if length != duration { duration = length }
        let playing = p.rate > 0
        if playing != isPlaying { isPlaying = playing }
        refreshSound()
        checkUpNext()
        showNowPlaying()
    }

    private func observe(_ item: AVPlayerItem, of p: AVPlayer) {
        unobserveItem()
        statusObservation = item.observe(\.status, options: [.new]) { [weak self] _, _ in
            Task { @MainActor in self?.itemStatusChanged() }
        }
        let center = NotificationCenter.default
        itemObservers = [
            center.addObserver(forName: AVPlayerItem.didPlayToEndTimeNotification, object: item, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.playedToEnd() }
            },
            center.addObserver(forName: AVPlayerItem.mediaSelectionDidChangeNotification, object: item, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.refreshTracks() }
            },
        ]
        legible = nil
        audible = nil
        groupsTask = Task { [weak self] in
            let groups = await PlayerTracks.groups(of: item)
            guard !Task.isCancelled, let self, self.player?.currentItem === item || self.player == nil else { return }
            self.legible = groups.legible
            self.audible = groups.audible
            self.applyChoices(to: item, of: p)
        }
    }

    private func unobserveItem() {
        statusObservation?.invalidate()
        statusObservation = nil
        for o in itemObservers { NotificationCenter.default.removeObserver(o) }
        itemObservers = []
        groupsTask?.cancel()
        groupsTask = nil
        soundTask?.cancel()
        soundTask = nil
    }

    private func itemStatusChanged() {
        guard let p = player, let item = p.currentItem, item.status == .readyToPlay else { return }
        PlayerTracks.ensureAudio(player: p, item: item, audible: audible)
        refreshTracks()
    }

    /// A new item starts with the person's choices (or Settings' subtitles), and with sound.
    private func applyChoices(to item: AVPlayerItem, of p: AVPlayer) {
        let subtitleLanguage: String? = chosenSubtitle ?? (settings.subtitlesOn ? settings.language : nil)
        if let language = subtitleLanguage, let c = PlayerTracks.choice(in: legible, language: language) {
            PlayerTracks.select(c, in: legible, of: item)
        } else {
            PlayerTracks.select(nil, in: legible, of: item)
        }
        if let language = chosenAudio, let c = PlayerTracks.choice(in: audible, language: language) {
            PlayerTracks.select(c, in: audible, of: item)
        }
        PlayerTracks.ensureAudio(player: p, item: item, audible: audible)
        refreshTracks()
    }

    private func refreshTracks() {
        guard let item = player?.currentItem else { return }
        let subs = PlayerTracks.choices(in: legible)
        let auds = PlayerTracks.choices(in: audible)
        if subs != subtitles { subtitles = subs }
        if auds != audioTracks { audioTracks = auds }
        let sub = PlayerTracks.selected(in: legible, of: item)
        let aud = PlayerTracks.selected(in: audible, of: item)
        if sub != subtitle { subtitle = sub }
        if aud != audio { audio = aud }
        if let p = player, item.status == .readyToPlay { PlayerTracks.ensureAudio(player: p, item: item, audible: audible) }
        refreshSound()
    }

    private func refreshSound() {
        guard let p = player, let item = p.currentItem else { return }
        let now = PlayerTracks.sound(player: p, item: item)
        let s = Sound(codec: now.codec, audible: now.audible)
        if s != sound { sound = s }
        // an enabled track whose format is not read yet (after an audio switch or a
        // discontinuity): load it, one load at a time, rather than report "audio" for good
        guard now.codec == "audio", soundTask == nil else { return }
        soundTask = Task { [weak self] in
            let loaded = await PlayerTracks.loadedSound(player: p, item: item)
            // cancelled: the item was left, and unobserveItem cleared soundTask already
            guard let self, !Task.isCancelled else { return }
            self.soundTask = nil
            self.soundLoaded(Sound(codec: loaded.codec, audible: loaded.audible), of: p, item)
        }
    }

    /// The sound read once the formats loaded: kept only while that item still plays here
    /// (a load that ends after the video changed says nothing about the new one).
    func soundLoaded(_ s: Sound, of p: AVPlayer, _ item: AVPlayerItem) {
        guard player === p, p.currentItem === item else { return }
        if s != sound { sound = s }
    }

    func playedToEnd() {
        ended = true
        isPlaying = false
        if let d = player?.currentItem?.duration.seconds, d.isFinite { position = d }
        if !reportedCompleted {
            reportedCompleted = true
            report(completed: true)
        }
        checkUpNext()
    }

    // MARK: AirPlay (story 48.24)

    private func startRoutes(of p: AVPlayer) {
        p.allowAirPlay()
        externalWatcher = ExternalPlaybackWatcher(p) { [weak self] active in self?.externalPlaybackChanged(active) }
    }

    /// AirPlay took the video to a receiver, or gave it back: the item is opened again on the
    /// URL that route can read, at the same position, playing if it played.
    private func externalPlaybackChanged(_ active: Bool) {
        guard let route = routes.update(externalPlaybackActive: active), let p = player,
              let signedURL, let proxiedURL else { return }
        let at = p.currentTime().seconds
        let wasPlaying = p.rate > 0 || isPlaying
        let item = AVPlayerItem(url: RouteURL.video(signed: signedURL, proxied: proxiedURL, route: route))
        observe(item, of: p)
        p.replaceCurrentItem(with: item)
        openedAt = .now
        if at.isFinite, at > 0 { p.seek(to: CMTime(seconds: at, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero) }
        if wasPlaying { p.play() }
        Log.ui.info("playback: route change reopened at \(at, privacy: .public) s, playing \(wasPlaying, privacy: .public)")
        let to = route.rawValue
        Log.ui.info("playback route: \(to, privacy: .public)")
    }

    // MARK: Now Playing and the remote commands (story 48.24)

    private func startNowPlaying() {
        NowPlaying.shared.begin(self, handlers: remoteHandlers())
        nowShown = nil
        showNowPlaying()
        guard let id = video?.id else { return }
        Task { [client, weak self] in
            guard let data = try? await client.poster(video: id), let image = PlatformImage(data: data),
                  let self, self.video?.id == id else { return }
            NowPlaying.shared.show(self, artwork: image)
        }
    }

    private func remoteHandlers() -> RemoteHandlers {
        RemoteHandlers(
            play: { [weak self] in if self?.isPlaying == false { self?.togglePause(from: "remote play") } },
            pause: { [weak self] in if self?.isPlaying == true { self?.togglePause(from: "remote pause") } },
            toggle: { [weak self] in self?.togglePause(from: "remote toggle") },
            skip: { [weak self] in self?.skip(by: $0) },
            next: next == nil ? nil : { [weak self] in self?.playNext() },
            seek: { [weak self] in self?.seek(to: $0) }
        )
    }

    /// Tells Now Playing when playing starts or stops, the length changes, or the position
    /// jumps (a seek): between those the system moves it on by itself.
    private func showNowPlaying() {
        guard let video else { return }
        let item = NowPlayingItem(title: video.displayTitle, duration: duration > 0 ? duration : nil,
                                  position: position, playing: isPlaying)
        if let (last, at) = nowShown, last.playing == item.playing, last.duration == item.duration {
            let expected = last.position + (last.playing ? Date.now.timeIntervalSince(at) : 0)
            if abs(expected - item.position) < 2 { return }
        }
        nowShown = (item, .now)
        NowPlaying.shared.show(self, item)
    }

    // MARK: Up next

    /// The series' next episode: the video's series (`/api/me/videos/{id}`), then its
    /// episodes in order.
    private func findNext(after v: Video) {
        nextTask?.cancel()
        nextTask = Task { [client] in
            do {
                // Epic 56 story 56.12: the queue's next first, when this video is its current one
                if let q = await PlayerQueues.next(after: v.id, client) {
                    guard !Task.isCancelled, video?.id == v.id else { return }
                    nextFound(q)
                    return
                }
                guard let series = try await client.myVideo(v.id).series else { return }
                let all = try await client.episodes(series: series.id).episodes
                let keys = all.map { WatchLogic.EpisodeKey(id: $0.videoID, season: $0.season, episode: $0.episode, absolute: $0.absoluteNumber) }
                guard !Task.isCancelled, video?.id == v.id, let id = WatchLogic.next(after: v.id, in: keys) else { return }
                if let episode = all.first(where: { $0.videoID == id }) { nextFound(episode) }
            } catch {
                // not an episode, or the home could not say: no Up next
            }
        }
    }

    /// The series' next episode, once the home has said. Found after the end (or during a
    /// pause in the last seconds), the time observer no longer ticks: Up next is checked here.
    func nextFound(_ episode: Episode) {
        next = episode
        NowPlaying.shared.update(self, handlers: remoteHandlers())
        checkUpNext()
    }

    /// In the last seconds (or at the end), with autoplay on: count down, then play next.
    private func checkUpNext() {
        guard settings.autoplayNext, next != nil, upNextCountdown == nil, !upNextDismissed,
              WatchLogic.upNextDue(position: position, duration: duration, ended: ended) else { return }
        upNextCountdown = WatchLogic.upNextSeconds
        upNextTask = Task {
            while let left = upNextCountdown, left > 0 {
                try? await Task.sleep(for: .seconds(1))
                guard !Task.isCancelled else { return }
                upNextCountdown = left - 1
            }
            guard !Task.isCancelled else { return }
            playNext()
        }
    }
}

/// The Playback menu (the Mac's menu bar, an iPad's with a keyboard): space plays and
/// pauses, the arrows skip 10 s, Command-period stops. Each is off while nothing plays, so
/// the keys stay the search field's and the lists'.
struct PlaybackCommands: Commands {
    @FocusedValue(\.playback) private var playback

    var body: some Commands {
        CommandMenu("Playback") {
            Button("Play/Pause") { playback?.togglePause() }
                .keyboardShortcut(.space, modifiers: [])
                .disabled(playback?.player == nil)
            Button("Skip Forward 10 Seconds") { playback?.skip(by: 10) }
                .keyboardShortcut(.rightArrow, modifiers: [])
                .disabled(playback?.player == nil)
            Button("Skip Back 10 Seconds") { playback?.skip(by: -10) }
                .keyboardShortcut(.leftArrow, modifiers: [])
                .disabled(playback?.player == nil)
            Divider()
            Button("Stop") { playback?.stop() }
                .keyboardShortcut(".", modifiers: .command)
                .disabled(playback?.video == nil)
        }
    }
}

extension FocusedValues {
    /// What plays in the focused window.
    @Entry var playback: Playback?
}

/// What plays here plays with sound: a video and a live channel alike.
enum PlaybackAudio {
    static func activate() {
        #if os(iOS)
        // the app declares background audio: with the playback category the sound goes on
        // when the screen locks or the app goes to the background, and it ignores the mute
        // switch (the default category is silent with the switch on: live TV had no sound)
        try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .moviePlayback)
        try? AVAudioSession.sharedInstance().setActive(true)
        #endif
    }

    /// The app declares the audio background mode in the Info.plist it shipped with (without
    /// it iOS stops the sound in the background and Picture in Picture never becomes possible).
    static var declaredInInfoPlist: Bool {
        BackgroundAudio.isDeclared(in: Bundle.main.infoDictionary)
    }
}
