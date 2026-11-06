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
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
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
) {
    var request by mutableStateOf<PlayRequest?>(null)
        private set
    var player by mutableStateOf<MediaPlayer?>(null)
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

    private val proxy by lazy { StreamProxy(client) }
    private var session: String? = null
    private var hls = false
    private var renewAt = Long.MAX_VALUE
    private var openedAt = 0L
    @Volatile private var failed: String? = null
    @Volatile private var cues: List<Cue> = emptyList()
    private var running: Job? = null
    /** The session's options: a direct file's other audio track asks for it as HLS. */
    private var audioTrack: Int? = null

    fun play(r: PlayRequest) {
        if (r.videoId == request?.videoId && problem == null && r.startSec == null) return
        stop()
        request = r
        running = scope.launch { run(r) }
    }

    fun stop() {
        running?.cancel()
        running = null
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
        upNext.reset(null)
        proxy.forget()
    }

    /** The proxy too: the app is closing or leaving this home. */
    fun close() {
        stop()
        proxy.close()
    }

    fun toggle() = fx { player?.let { if (it.status == MediaPlayer.Status.PLAYING) it.pause() else it.play() } }

    fun seekTo(ms: Long) = fx { player?.seek(Duration.millis(ms.coerceAtLeast(0).toDouble())) }

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
        if (r != null) scope.launch { loadCues(r) }
    }

    /** Plays [o]'s audio from the same moment; kept for the next video. */
    fun chooseAudio(o: AudioOption) {
        if (o == audio) return
        choice = choice.copy(audioLanguage = o.language)
        scope.launch { switchAudio(o) }
    }

    /** Reopens the stream capped at [q] from the same moment; kept while this home is signed in. */
    fun chooseQuality(q: QualityOption) {
        if (q == quality) return
        chosenQuality = q
        scope.launch {
            val was = quality
            quality = q
            if (!reopen(kbps = q.maxBitrateKbps)) quality = was
        }
    }

    private suspend fun run(r: PlayRequest) {
        val start = r.startSec ?: resumeAt(r)
        quality = chosenQuality ?: settings().qualityWifi.let { s -> if (s.maxBitrateKbps == null) QualityOption.Auto else QualityOption(s.height, s.maxBitrateKbps) }
        scope.launch { findNext(r) }
        if (features().isOn(Feature.CHAPTERS)) scope.launch { chapters = runCatching { client.chapters(r.videoId) }.getOrDefault(emptyList()) }
        open(fromMs = start * 1000L, kbps = quality.maxBitrateKbps)
        if (player != null) {
            scope.launch { followTracks() }
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

    /** The stream's tracks once known: the person's choice (or the settings) applied. */
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
        reopening = true
        return try {
            val at = positionMs
            val wasPlaying = playing
            val next = onFx { newPlayer(url, at, autoplay = wasPlaying) }
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

    private var currentLocal: String? = null

    /**
     * Opens (or reopens) the stream at [fromMs]. The session in use is closed only once the
     * new one plays; a reopen that fails leaves the old stream playing, with a [note] of why.
     */
    private suspend fun open(fromMs: Long, kbps: Int? = quality.maxBitrateKbps) {
        val r = request ?: return
        val old = session
        try {
            val s = withContext(NonCancellable) {
                client.openStream(
                    r.videoId, (fromMs / 1000).toInt(), STREAM_PROFILE, maxBitrateKbps = kbps,
                    audioTrack = audioTrack, format = if (audioTrack != null) "hls" else null,
                )
            }
            try {
                currentCoroutineContext().ensureActive()
                val url = s.playbackUrl(client.home) ?: throw IllegalStateException("The home gave no address to play.")
                client.waitUntilPlayable(url)
                currentCoroutineContext().ensureActive()
                val local = proxy.local(url) ?: throw IllegalStateException("Not this home's stream.")
                val next = onFx { newPlayer(local, fromMs, autoplay = true) }
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

    /** A JavaFX player for [url], playing from [fromMs] once it is ready. Runs on the FX thread. */
    private fun newPlayer(url: String, fromMs: Long, autoplay: Boolean): MediaPlayer {
        val media = Media(url)
        val p = MediaPlayer(media)
        p.setOnReady {
            durationMs = p.totalDuration.toMillis().takeIf { it.isFinite() }?.toLong() ?: request?.durationSec?.let { (it * 1000).toLong() } ?: 0
            if (fromMs > 0) p.seek(Duration.millis(fromMs.toDouble()))
            p.volume = volume.toDouble()
            p.isMute = muted
            if (autoplay) p.play()
            if (!hls) {
                val tracks = media.tracks.filterIsInstance<AudioTrack>()
                if (tracks.size > 1 && audioOptions.isEmpty()) {
                    audioOptions = tracks.mapIndexed { i, t -> AudioOption(t.name?.takeIf { it.isNotBlank() }, t.language?.takeIf { it.isNotBlank() }, index = i) }
                    audio = audioOptions.getOrNull(audioTrack ?: 0)
                }
            }
        }
        p.currentTimeProperty().addListener { _, _, t ->
            val ms = t.toMillis().toLong()
            positionMs = ms
            if (subtitle != null) caption = cues.at(ms)
        }
        p.statusProperty().addListener { _, _, st -> playing = st == MediaPlayer.Status.PLAYING }
        p.setOnError { failed = p.error?.message ?: "The video stopped playing." }
        p.setOnEndOfMedia { ended = true }
        return p
    }

    private fun applySound() = fx {
        player?.let {
            it.volume = volume.toDouble()
            it.isMute = muted
        }
    }

    /**
     * Every second: Up next; every 10 s while open: the position; the end once; a reopen where
     * it is before the URL ends, or when the player failed (not again within 30 s of opening:
     * then it says why).
     */
    private suspend fun watch() {
        var told = false
        var second = 0
        while (scope.isActive) {
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
                if (!wasPlaying) fx { player?.pause() }
            }
        }
    }

    private fun release(p: MediaPlayer?) {
        if (p != null) fx { p.dispose() }
    }

    internal companion object {
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
