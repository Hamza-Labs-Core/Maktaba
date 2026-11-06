package com.hamzalabs.maktaba.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Chapter
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.api.Rendition as Rung
import com.hamzalabs.maktaba.core.api.chapters
import com.hamzalabs.maktaba.core.api.myVideo
import com.hamzalabs.maktaba.core.playback.PlaybackValues
import com.hamzalabs.maktaba.core.playback.QualityOption
import com.hamzalabs.maktaba.core.playback.TrackChoice
import com.hamzalabs.maktaba.core.playback.nextVideo
import com.hamzalabs.maktaba.core.playback.pickTrack
import com.hamzalabs.maktaba.core.playback.plan
import com.hamzalabs.maktaba.core.playback.sameLanguage
import com.hamzalabs.maktaba.desktop.watch.Cue
import com.hamzalabs.maktaba.desktop.watch.Rendition
import com.hamzalabs.maktaba.desktop.watch.UpNextState
import com.hamzalabs.maktaba.desktop.watch.at
import javafx.application.Platform
import javafx.scene.media.AudioTrack
import javafx.scene.media.Media
import javafx.scene.media.MediaPlayer
import javafx.util.Duration
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/** What this player can play: the streaming service's profile for JavaFX (H.264, AAC; MP4 or HLS). */
const val STREAM_PROFILE = "desktop-player"

/**
 * A video to play (story 55.01): [startSec] null is where the person left it (story 55.08 reads
 * it), 0 the start. [then] are the videos after it (a playlist's), which Up next offers in turn.
 */
data class PlayRequest(
    val videoId: String,
    val title: String,
    val durationSec: Double? = null,
    val startSec: Int? = null,
    val then: List<PlayRequest> = emptyList(),
)

/** Where a video resumes (story 55.08): the saved position, unless it was finished or is over 95% in, then 0. */
object ResumePosition {
    fun of(positionSec: Double, completed: Boolean, durationSec: Double?): Int {
        if (completed || positionSec <= 0) return 0
        if (durationSec != null && durationSec > 0 && positionSec / durationSec > 0.95) return 0
        return positionSec.toInt()
    }
}

/** An audio track the Audio menu offers: an HLS rendition (by its NAME) or a direct file's track (by its index). */
data class AudioOption(val name: String?, val language: String?, val rendition: String? = null, val index: Int = 0)

/** The player a video plays in: JavaFX's ([FxVideo]), or a test's. Each call returns at once. */
interface Video {
    fun toggle()
    fun pause()
    fun seek(ms: Long)
    fun sound(volume: Float, muted: Boolean)
    fun dispose()
}

/** What a [Video] tells its [Playback], from any thread. */
interface VideoEvents {
    val volume: Float
    val muted: Boolean
    /** Ready: its length (null: not known) and, for a file, its audio tracks in order. */
    fun ready(durationMs: Long?, audio: List<AudioOption>)
    fun position(ms: Long)
    fun playing(on: Boolean)
    fun failed(why: String)
    fun ended()
}

/** Makes the [Video] for a stream the proxy serves at [url], from [fromMs] (playing at once when [autoplay]). */
fun interface Players {
    suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video
}

/** A JavaFX player, driven on its own thread. */
class FxVideo(val media: MediaPlayer) : Video {
    override fun toggle() = Playback.fx { if (media.status == MediaPlayer.Status.PLAYING) media.pause() else media.play() }
    override fun pause() = Playback.fx { media.pause() }
    override fun seek(ms: Long) = Playback.fx { media.seek(Duration.millis(ms.coerceAtLeast(0).toDouble())) }
    override fun sound(volume: Float, muted: Boolean) = Playback.fx {
        media.volume = volume.toDouble()
        media.isMute = muted
    }
    override fun dispose() = Playback.fx { media.dispose() }
}

/** JavaFX's players. */
object FxPlayers : Players {
    override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video = Playback.onFx {
        val media = Media(url)
        val p = MediaPlayer(media)
        p.setOnReady {
            if (fromMs > 0) p.seek(Duration.millis(fromMs.toDouble()))
            p.volume = events.volume.toDouble()
            p.isMute = events.muted
            if (autoplay) p.play()
            val tracks = media.tracks.filterIsInstance<AudioTrack>()
                .mapIndexed { i, t -> AudioOption(t.name?.takeIf { it.isNotBlank() }, t.language?.takeIf { it.isNotBlank() }, index = i) }
            events.ready(p.totalDuration.toMillis().takeIf { it.isFinite() }?.toLong(), tracks)
        }
        p.currentTimeProperty().addListener { _, _, t -> events.position(t.toMillis().toLong()) }
        p.statusProperty().addListener { _, _, st -> events.playing(st == MediaPlayer.Status.PLAYING) }
        p.setOnError { events.failed(p.error?.message ?: "The video stopped playing.") }
        p.setOnEndOfMedia { events.ended() }
        FxVideo(p)
    }
}

/**
 * What is playing. As the Android player's: opens a stream session, plays it, reports the
 * position every 10 s (so "continue watching" resumes there), reopens before the signed URL
 * ends or when the player fails (not again within 30 s of opening: then it says why), and
 * closes the session when stopped. The video goes through [StreamProxy] (JavaFX sends no
 * header); JavaFX is driven on its own thread.
 *
 * Story 55.08: it resumes where the person left the video; offers the stream's audio and
 * subtitle tracks (the subtitles drawn by the Player, [caption]) and the session's qualities;
 * keeps the person's track choice for the next video; and offers Up next ([upNext]).
 *
 * A track or quality change runs under the video's job, and every wait in [open] and [reload]
 * is checked against the video's generation ([stop] and [play] move it on): a change that
 * ends after its video was closed or replaced disposes what it made and closes its session.
 */
class Playback(
    val client: ApiClient,
    private val scope: CoroutineScope,
    /** Outlives the screen: a stopped video's last position and its session's close still go out. */
    private val background: CoroutineScope,
    private val signedOut: () -> Unit,
    /** The playback settings now (Profile's Playback, story 55.09). */
    private val settings: () -> PlaybackValues = { PlaybackValues() },
    /** What the home has on now (chapters are read only when they are on). */
    private val features: () -> Capabilities = { Capabilities.AllOn },
    private val players: Players = FxPlayers,
    private val proxyFor: (ApiClient) -> StreamProxy = ::StreamProxy,
) {
    var request by mutableStateOf<PlayRequest?>(null)
        private set
    var player by mutableStateOf<Video?>(null)
        private set
    var problem by mutableStateOf<String?>(null)
        private set
    var playing by mutableStateOf(false)
        private set
    var positionMs by mutableLongStateOf(0L)
        private set
    var durationMs by mutableLongStateOf(0L)
        private set
    var ended by mutableStateOf(false)
        private set

    /** A reopen (a track or a quality) is under way. */
    var reopening by mutableStateOf(false)
        private set

    /** Why the last change did not take; the stream before it plays on. */
    var note by mutableStateOf<String?>(null)
        private set

    var audioOptions by mutableStateOf<List<AudioOption>>(emptyList())
        private set
    var audio by mutableStateOf<AudioOption?>(null)
        private set
    var subtitleOptions by mutableStateOf<List<Rendition>>(emptyList())
        private set
    var subtitle by mutableStateOf<Rendition?>(null)
        private set

    /** The subtitle text at the current time, drawn over the picture. */
    var caption by mutableStateOf<String?>(null)
        private set

    var ladder by mutableStateOf<List<QualityOption>>(emptyList())
        private set
    var quality by mutableStateOf(QualityOption.Auto)
        private set

    var chapters by mutableStateOf<List<Chapter>>(emptyList())
        private set

    var volume by mutableFloatStateOf(1f)
        private set
    var muted by mutableStateOf(false)
        private set

    val upNext = UpNextState()

    /** The audio and subtitle choice, kept for the next video (FR-34). */
    var choice = TrackChoice()
        private set

    /** The quality chosen while this home is signed in (null: the playback settings' default). */
    private var chosenQuality: QualityOption? = null

    private val proxy by lazy { proxyFor(client) }
    private var session: String? = null
    private var hls = false
    private var renewAt = Long.MAX_VALUE
    private var openedAt = 0L
    @Volatile private var failed: String? = null
    @Volatile private var cues: List<Cue> = emptyList()
    private var running: Job? = null
    /** The running video's scope: its changes are its children, so another video or Close stops them. */
    private var video: CoroutineScope? = null
    /** Moves on with each [stop]: work begun for an earlier video sees it and drops what it made. */
    private var generation = 0
    private var currentLocal: String? = null
    /** The session's options: a direct file's other audio track asks for it as HLS. */
    private var audioTrack: Int? = null

    fun play(r: PlayRequest) {
        if (r.videoId == request?.videoId && problem == null && r.startSec == null) return
        stop()
        request = r
        running = scope.launch { run(r) }
    }

    fun stop() {
        generation++
        running?.cancel()
        running = null
        video = null
        val s = session
        if (s != null) {
            val at = (positionMs / 1000).toInt()
            background.launch {
                if (at > 0) client.reportProgress(s, at)
                client.closeStream(s)
            }
        }
        release(player)
        player = null
        session = null
        problem = null
        note = null
        request = null
        playing = false
        ended = false
        positionMs = 0
        durationMs = 0
        audioOptions = emptyList()
        audio = null
        subtitleOptions = emptyList()
        subtitle = null
        caption = null
        cues = emptyList()
        chapters = emptyList()
        ladder = emptyList()
        audioTrack = null
        currentLocal = null
        hls = false
        upNext.reset(null)
        proxy.forget()
    }

    /** The proxy too: the app is closing or leaving this home. */
    fun close() {
        stop()
        proxy.close()
    }

    fun toggle() {
        player?.toggle()
    }

    fun seekTo(ms: Long) {
        player?.seek(ms.coerceAtLeast(0))
    }

    fun skip(deltaMs: Long) = seekTo(positionMs + deltaMs)

    fun changeVolume(v: Float) {
        volume = v.coerceIn(0f, 1f)
        if (volume > 0f) muted = false
        applySound()
    }

    fun toggleMute() {
        muted = !muted
        applySound()
    }

    /** N, Play now, or the countdown: the next video. */
    fun playNext() {
        upNext.playNow()?.let(::play)
    }

    /** S: subtitles off, or on in the last language (else the first). */
    fun toggleSubtitles() {
        if (subtitle != null) chooseSubtitle(null)
        else (subtitleOptions.firstOrNull { o -> choice.textLanguage?.let { o.language?.let { l -> sameLanguage(l, it) } } == true }
            ?: subtitleOptions.firstOrNull())?.let(::chooseSubtitle)
    }

    /** Subtitles in [r], or off (null); kept for the next video. */
    fun chooseSubtitle(r: Rendition?) {
        subtitle = r
        cues = emptyList()
        caption = null
        choice = if (r == null) choice.copy(textOff = true, textLanguage = null) else choice.copy(textOff = false, textLanguage = r.language)
        if (r != null) video?.launch { loadCues(r) }
    }

    /** Plays [o]'s audio from the same moment; kept for the next video. */
    fun chooseAudio(o: AudioOption) {
        if (o == audio) return
        choice = choice.copy(audioLanguage = o.language)
        video?.launch { switchAudio(o) }
    }

    /** Reopens the stream capped at [q] from the same moment; kept while this home is signed in. */
    fun chooseQuality(q: QualityOption) {
        if (q.height == quality.height) return
        chosenQuality = q
        video?.launch {
            val was = quality
            quality = q
            if (!reopen(kbps = q.maxBitrateKbps)) quality = was
        }
    }

    /** The video's work: its side reads are children of its job, so another video or Close stops them. */
    private suspend fun run(r: PlayRequest) = coroutineScope {
        video = this
        val start = r.startSec ?: resumeAt(r)
        quality = chosenQuality ?: settings().qualityWifi.let { s -> if (s.maxBitrateKbps == null) QualityOption.Auto else QualityOption(s.height, s.maxBitrateKbps) }
        launch { findNext(r) }
        if (features().isOn(Feature.CHAPTERS)) launch { chapters = runCatching { client.chapters(r.videoId) }.getOrDefault(emptyList()) }
        open(fromMs = start * 1000L, kbps = quality.maxBitrateKbps)
        if (player != null) {
            launch { followTracks() }
            watch()
        }
    }

    private suspend fun resumeAt(r: PlayRequest): Int = try {
        val my = client.myVideo(r.videoId)
        ResumePosition.of(my.positionSec, my.completed, r.durationSec)
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        0
    }

    private suspend fun findNext(r: PlayRequest) {
        val queued = r.then.firstOrNull()?.let { it.copy(startSec = it.startSec ?: 0, then = r.then.drop(1)) }
        val next = queued ?: client.nextVideo(r.videoId)?.let { PlayRequest(it.id, it.displayTitle, it.durationSec, startSec = 0) }
        upNext.reset(next)
    }

    /** The stream's tracks once known: the person's choice (or the settings) applied (a direct file's audio in [ready]). */
    private suspend fun followTracks() {
        val plan = choice.plan(settings())
        if (hls) {
            val m = withTimeoutOrNull(15_000) { proxy.master.filterNotNull().first() } ?: return
            audioOptions = if (m.audio.size > 1) m.audio.map { AudioOption(it.name.ifBlank { null }, it.language, rendition = it.name) } else emptyList()
            audio = audioOptions.firstOrNull { o -> m.audio.firstOrNull { it.default }?.name == o.rendition } ?: audioOptions.firstOrNull()
            subtitleOptions = m.subtitles.filter { it.uri != null }
            val lang = plan.audioLanguage
            val want = lang?.let { l -> audioOptions.getOrNull(pickTrack(audioOptions.map { it.language }, l) ?: -1) }
            if (lang != null && want != null && want != audio && want.language?.let { sameLanguage(it, lang) } == true) switchAudio(want)
        }
        if (plan.textOn && subtitleOptions.isNotEmpty()) {
            val at = pickTrack(subtitleOptions.map { it.language }, plan.textLanguage) ?: return
            subtitle = subtitleOptions[at]
            loadCues(subtitleOptions[at])
        }
    }

    private suspend fun loadCues(r: Rendition) {
        val c = try {
            proxy.cues(r)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            note = e.message ?: e.toString()
            return
        }
        if (subtitle == r) {
            cues = c
            caption = c.at(positionMs)
        }
    }

    private suspend fun switchAudio(o: AudioOption) {
        if (o.rendition != null) {
            // the same session: the proxy now gives JavaFX that rendition as the only audio
            val was = proxy.audio
            proxy.audio = o.rendition
            if (reload()) audio = o else proxy.audio = was
        } else {
            // a direct file: its first track plays as it is, another one as HLS
            val was = audioTrack
            audioTrack = o.index.takeIf { it > 0 }
            if (reopen(kbps = quality.maxBitrateKbps)) audio = o else audioTrack = was
        }
    }

    /** The same session in a new JavaFX player from the current moment (the master is read again). */
    private suspend fun reload(): Boolean {
        val url = currentLocal ?: return false
        val g = generation
        reopening = true
        return try {
            val at = positionMs
            val wasPlaying = playing
            val next = withContext(NonCancellable) { players.open(url, at, wasPlaying, events) }
            if (g != generation || !currentCoroutineContext().isActive) {
                next.dispose()
                throw CancellationException("The video was closed or replaced.")
            }
            release(player)
            player = next
            note = null
            true
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            note = e.message ?: e.toString()
            false
        } finally {
            reopening = false
        }
    }

    /** A new session from the current moment; the old one plays on when it fails. */
    private suspend fun reopen(kbps: Int?): Boolean {
        reopening = true
        try {
            val at = positionMs
            val before = session
            open(fromMs = at, kbps = kbps)
            return session != before && note == null
        } finally {
            reopening = false
        }
    }

    /**
     * Opens (or reopens) the stream at [fromMs]. The session in use is closed only once the
     * new one plays; a reopen that fails leaves the old stream playing, with a [note] of why.
     */
    private suspend fun open(fromMs: Long, kbps: Int? = quality.maxBitrateKbps) {
        val r = request ?: return
        val g = generation
        /** The video this open is for was closed or replaced (or its job is). */
        suspend fun stale() = g != generation || !currentCoroutineContext().isActive
        val old = session
        try {
            val s = withContext(NonCancellable) {
                client.openStream(
                    r.videoId, (fromMs / 1000).toInt(), STREAM_PROFILE, maxBitrateKbps = kbps,
                    audioTrack = audioTrack, format = if (audioTrack != null) "hls" else null,
                )
            }
            try {
                if (stale()) throw CancellationException("The video was closed or replaced.")
                val url = s.playbackUrl(client.home) ?: throw IllegalStateException("The home gave no address to play.")
                client.waitUntilPlayable(url)
                if (stale()) throw CancellationException("The video was closed or replaced.")
                val local = proxy.local(url) ?: throw IllegalStateException("Not this home's stream.")
                val next = withContext(NonCancellable) { players.open(local, fromMs, true, events) }
                if (stale()) {
                    next.dispose()
                    throw CancellationException("The video was closed or replaced.")
                }
                release(player)
                player = next
                currentLocal = local
            } catch (e: Throwable) {
                background.launch { client.closeStream(s.sessionId) }
                throw e
            }
            session = s.sessionId
            hls = s.isHls
            ladder = QualityOption.of(s.ladder.ifEmpty { emptyList<Rung>() })
            // a default from the settings (720p at its own cap) is the ladder's rung of that height
            if (quality.height != null) ladder.firstOrNull { it.height == quality.height }?.let { quality = it }
            ended = false
            failed = null
            note = null
            openedAt = System.currentTimeMillis()
            renewAt = (s.expiresMillis ?: Long.MAX_VALUE).let { if (it == Long.MAX_VALUE) it else it - 60_000 }
            if (old != null && old != s.sessionId) background.launch { client.closeStream(old) }
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            stop()
            signedOut()
        } catch (e: Exception) {
            if (old != null) {
                // the stream before keeps playing; say why the change did not take
                note = e.message ?: e.toString()
                return
            }
            release(player)
            player = null
            problem = e.message ?: e.toString()
        }
    }

    /** What this video's players tell it. */
    private val events = object : VideoEvents {
        override val volume: Float get() = this@Playback.volume
        override val muted: Boolean get() = this@Playback.muted

        override fun ready(durationMs: Long?, audio: List<AudioOption>) = onReady(durationMs, audio)

        override fun position(ms: Long) {
            positionMs = ms
            if (subtitle != null) caption = cues.at(ms)
        }

        override fun playing(on: Boolean) {
            playing = on
        }

        override fun failed(why: String) {
            failed = why
        }

        override fun ended() {
            ended = true
        }
    }

    /**
     * A player is ready. A direct file's audio tracks are offered once known, and the kept
     * language (or the settings') is switched to when it is not the first track (story 55.08).
     */
    private fun onReady(length: Long?, tracks: List<AudioOption>) {
        durationMs = length ?: request?.durationSec?.let { (it * 1000).toLong() } ?: 0
        if (hls || tracks.size <= 1 || audioOptions.isNotEmpty()) return
        audioOptions = tracks
        audio = tracks.getOrNull(audioTrack ?: 0)
        val lang = choice.plan(settings()).audioLanguage ?: return
        if (audioTrack != null) return
        val at = pickTrack(tracks.map { it.language }, lang) ?: return
        val want = tracks[at]
        if (at > 0 && want.language?.let { sameLanguage(it, lang) } == true) video?.launch { switchAudio(want) }
    }

    private fun applySound() {
        player?.sound(volume, muted)
    }

    /**
     * Every second: Up next; every 10 s while open: the position; the end once; a reopen where
     * it is before the URL ends, or when the player failed (not again within 30 s of opening:
     * then it says why).
     */
    private suspend fun watch() {
        var told = false
        var second = 0
        while (currentCoroutineContext().isActive) {
            delay(1_000)
            if (player == null) return
            upNext.tick(positionMs, durationMs, ended, settings().autoplayNext)?.let {
                play(it)
                return
            }
            if (++second % 10 != 0) continue
            val s = session ?: return
            val at = positionMs
            if (ended) {
                if (!told) client.reportProgress(s, (at / 1000).toInt(), completed = true)
                told = true
                continue
            }
            if (at > 0) client.reportProgress(s, (at / 1000).toInt())
            val why = failed
            if (why != null && System.currentTimeMillis() - openedAt < 30_000) {
                release(player)
                player = null
                problem = why
                return
            }
            if (why != null || System.currentTimeMillis() >= renewAt) {
                // a renewal keeps the track and quality in force (the proxy keeps the audio)
                val wasPlaying = playing || why != null
                open(fromMs = at)
                if (!wasPlaying) player?.pause()
            }
        }
    }

    private fun release(p: Video?) {
        p?.dispose()
    }

    companion object {
        fun fx(block: () -> Unit) = Platform.runLater(block)

        /** Runs [block] on the FX thread and waits for its result. */
        suspend fun <T> onFx(block: () -> T): T {
            val done = kotlinx.coroutines.CompletableDeferred<T>()
            Platform.runLater {
                try {
                    done.complete(block())
                } catch (e: Throwable) {
                    done.completeExceptionally(e)
                }
            }
            return done.await()
        }
    }
}
