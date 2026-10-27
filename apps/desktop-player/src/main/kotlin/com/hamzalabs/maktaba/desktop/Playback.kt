package com.hamzalabs.maktaba.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Video
import javafx.application.Platform
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
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** What this player can play: the streaming service's profile for JavaFX (H.264, AAC; MP4 or HLS). */
const val STREAM_PROFILE = "desktop-player"

/**
 * What is playing. As the Android player's: opens a stream session, plays it, reports the
 * position every 10 s (so "continue watching" resumes there), reopens before the signed URL
 * ends or when the player fails (not again within 30 s of opening: then it says why), and
 * closes the session when stopped. The video goes through [StreamProxy] (JavaFX sends no
 * header); JavaFX is driven on its own thread.
 */
class Playback(
    val client: ApiClient,
    private val scope: CoroutineScope,
    /** Outlives the screen: a stopped video's last position and its session's close still go out. */
    private val background: CoroutineScope,
    private val signedOut: () -> Unit,
) {
    var video by mutableStateOf<Video?>(null)
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

    private val proxy by lazy { StreamProxy(client) }
    private var session: String? = null
    private var renewAt = Long.MAX_VALUE
    private var openedAt = 0L
    private var ended = false
    @Volatile private var failed: String? = null
    private var running: Job? = null

    fun play(v: Video) {
        if (v.id == video?.id && problem == null) return
        stop()
        video = v
        running = scope.launch { run() }
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
        video = null
        playing = false
        positionMs = 0
        durationMs = 0
    }

    /** The proxy too: the app is closing or leaving this home. */
    fun close() {
        stop()
        proxy.close()
    }

    fun toggle() = fx { player?.let { if (it.status == MediaPlayer.Status.PLAYING) it.pause() else it.play() } }

    fun seekTo(ms: Long) = fx { player?.seek(Duration.millis(ms.coerceAtLeast(0).toDouble())) }

    fun skip(deltaMs: Long) = seekTo(positionMs + deltaMs)

    private suspend fun run() {
        open(fromMs = 0)
        if (player != null) watch()
    }

    /**
     * Opens (or reopens) the stream at [fromMs]. The session in use is closed only once the
     * new one plays; a reopen that fails leaves the old stream playing.
     */
    private suspend fun open(fromMs: Long) {
        val v = video ?: return
        val old = session
        try {
            val s = withContext(NonCancellable) { client.openStream(v.id, (fromMs / 1000).toInt(), STREAM_PROFILE) }
            try {
                currentCoroutineContext().ensureActive()
                val url = s.playbackUrl(client.home) ?: throw IllegalStateException("The home gave no address to play.")
                client.waitUntilPlayable(url)
                currentCoroutineContext().ensureActive()
                val local = proxy.local(url) ?: throw IllegalStateException("Not this home's stream.")
                val next = onFx { newPlayer(local, fromMs) }
                release(player)
                player = next
            } catch (e: Throwable) {
                background.launch { client.closeStream(s.sessionId) }
                throw e
            }
            session = s.sessionId
            ended = false
            failed = null
            openedAt = System.currentTimeMillis()
            renewAt = (s.expiresMillis ?: Long.MAX_VALUE).let { if (it == Long.MAX_VALUE) it else it - 60_000 }
            if (old != null && old != s.sessionId) background.launch { client.closeStream(old) }
        } catch (e: CancellationException) {
            throw e
        } catch (e: ApiException.SignedOut) {
            stop()
            signedOut()
        } catch (e: Exception) {
            if (old != null && failed == null) return
            release(player)
            player = null
            problem = e.message ?: e.toString()
        }
    }

    /** A JavaFX player for [url], playing from [fromMs] once it is ready. Runs on the FX thread. */
    private fun newPlayer(url: String, fromMs: Long): MediaPlayer {
        val p = MediaPlayer(Media(url))
        p.setOnReady {
            durationMs = p.totalDuration.toMillis().takeIf { it.isFinite() }?.toLong() ?: video?.durationSec?.let { (it * 1000).toLong() } ?: 0
            if (fromMs > 0) p.seek(Duration.millis(fromMs.toDouble()))
            p.play()
        }
        p.currentTimeProperty().addListener { _, _, t -> positionMs = t.toMillis().toLong() }
        p.statusProperty().addListener { _, _, st -> playing = st == MediaPlayer.Status.PLAYING }
        p.setOnError { failed = p.error?.message ?: "The video stopped playing." }
        p.setOnEndOfMedia { ended = true }
        return p
    }

    /**
     * Every 10 s while open: the position; the end once; a reopen where it is before the URL
     * ends, or when the player failed (not again within 30 s of opening: then it says why).
     */
    private suspend fun watch() {
        var told = false
        while (scope.isActive) {
            delay(10_000)
            if (player == null) return
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
                val wasPlaying = playing || why != null
                open(fromMs = at)
                if (!wasPlaying) fx { player?.pause() }
            }
        }
    }

    private fun release(p: MediaPlayer?) {
        if (p != null) fx { p.dispose() }
    }

    private companion object {
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
