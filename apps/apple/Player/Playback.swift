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

    struct Sound: Equatable {
        let codec: String?
        let audible: Bool
    }

    private var session: String?
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
    private var statusObservation: NSKeyValueObservation?
    private var groupsTask: Task<Void, Never>?
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

    init(client: APIClient) {
        self.client = client
    }

    /// Plays `v` from `start` seconds; nil: where the person left off (a finished video
    /// starts again).
    func play(_ v: Video, from start: Double? = nil) {
        if v.id == video?.id, failure == nil {
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
    func togglePause() {
        guard let p = player else { return }
        if p.rate > 0 {
            p.pause()
            report()
        } else {
            if ended { seek(to: 0) }
            p.play()
        }
        isPlaying = p.rate > 0
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
        let at = p.currentTime().seconds
        let wasPlaying = p.rate > 0
        running?.cancel()
        running = Task {
            await open(from: at.isFinite ? at : position)
            if !wasPlaying { player?.pause() }
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
                play(v)
            } catch {
                failure = HomeFailure(error)
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

    func stop() {
        running?.cancel()
        running = nil
        groupsTask?.cancel()
        nextTask?.cancel()
        upNextTask?.cancel()
        unobserveItem()
        if let timeObserver, let player { player.removeTimeObserver(timeObserver) }
        timeObserver = nil
        player?.pause()
        if let session {
            let at = player?.currentTime().seconds ?? 0
            Task { [client] in
                if at.isFinite, at > 0 { await client.reportProgress(session: session, positionSec: Int(at)) }
                await client.closeStream(session)
            }
        }
        session = nil
        player = nil
        failure = nil
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
            "pipSupported=\(PictureInPicture.isSupported)",
            "pipPossible=\(pip.isPossible)",
        ].joined(separator: ";")
    }

    private func run(_ v: Video, asked: Double?) async {
        var state = v.playbackState
        if asked == nil, state == nil {
            // a list's video carries no position: the video's own page does
            state = try? await client.video(v.id).playbackState
        }
        guard !Task.isCancelled else { return }
        let start = WatchLogic.start(asked: asked, resume: state?.positionSec, completed: state?.completed ?? false)
        await open(from: start)
        guard player != nil else { return }
        findNext(after: v)
        await watch()
    }

    /// Opens (or reopens) the stream at `start` seconds, through the app's stream proxy: the
    /// home's web port wants the user's token on every `/stream/` request, and AVPlayer
    /// cannot renew one mid-video.
    private func open(from start: Double) async {
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
            ladder = s.ladder ?? []
            try await client.waitUntilPlayable(url, headers: auth.headers)
            try Task.checkCancellation()
            openedAt = .now
            renewAt = (s.expires ?? .distantFuture).addingTimeInterval(-60)
            signedURL = url
            proxiedURL = local
            let item = AVPlayerItem(url: RouteURL.video(signed: url, proxied: local, route: routes.route))
            let p = player ?? AVPlayer()
            observe(item, of: p)
            p.replaceCurrentItem(with: item)
            if start > 0 { await p.seek(to: CMTime(seconds: start, preferredTimescale: 600)) }
            position = start
            if player == nil {
                player = p
                observeTime(of: p)
                startRoutes(of: p)
                startNowPlaying()
            }
            ended = false
            p.play()
            isPlaying = true
        } catch is CancellationError {
            return
        } catch {
            player = nil
            failure = HomeFailure(error)
        }
    }

    /// Every 10 s while playing: the position, so "continue watching" resumes here; a reopen
    /// where it is before the URL ends, or when the item failed (not again within 30 s of
    /// opening: then it shows why).
    private func watch() async {
        while !Task.isCancelled {
            try? await Task.sleep(for: .seconds(10))
            guard !Task.isCancelled, let p = player, session != nil else { return }
            if p.rate > 0 { report() }
            let failed = p.currentItem?.status == .failed
            if failed, Date.now.timeIntervalSince(openedAt) < 30 {
                p.pause()
                player = nil
                failure = p.currentItem?.error.map(HomeFailure.init) ?? HomeFailure(kind: .server, detail: String(localized: "The video stopped playing."))
                return
            }
            if failed || Date.now >= renewAt {
                let at = p.currentTime().seconds
                let wasPlaying = p.rate > 0
                await open(from: at.isFinite ? at : 0)
                if !wasPlaying { player?.pause() }
            }
        }
    }

    /// Reports where it is now (best effort).
    private func report(completed: Bool = false) {
        guard let session, let p = player else { return }
        var at = p.currentTime().seconds
        if completed, duration > 0 { at = duration }
        guard at.isFinite, at > 0 || completed else { return }
        Task { [client] in await client.reportProgress(session: session, positionSec: Int(at), completed: completed) }
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
    }

    private func playedToEnd() {
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
            play: { [weak self] in if self?.isPlaying == false { self?.togglePause() } },
            pause: { [weak self] in if self?.isPlaying == true { self?.togglePause() } },
            toggle: { [weak self] in self?.togglePause() },
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
                guard let series = try await client.myVideo(v.id).series else { return }
                let all = try await client.episodes(series: series.id).episodes
                let keys = all.map { WatchLogic.EpisodeKey(id: $0.videoID, season: $0.season, episode: $0.episode, absolute: $0.absoluteNumber) }
                guard !Task.isCancelled, video?.id == v.id, let id = WatchLogic.next(after: v.id, in: keys) else { return }
                next = all.first { $0.videoID == id }
                NowPlaying.shared.update(self, handlers: remoteHandlers())
            } catch {
                // not an episode, or the home could not say: no Up next
            }
        }
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
}
