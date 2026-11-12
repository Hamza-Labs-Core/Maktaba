package com.hamzalabs.maktaba.player

import android.content.Context
import android.util.Log
import androidx.annotation.OptIn
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.DeviceInfo
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.MimeTypes
import androidx.media3.common.Player
import androidx.media3.common.Tracks
import androidx.media3.common.VideoSize
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.ProgressAnswer
import com.hamzalabs.maktaba.core.api.TimeReason
import com.hamzalabs.maktaba.core.api.TimeRefusal
import com.hamzalabs.maktaba.core.api.timeRefusal
import com.hamzalabs.maktaba.core.api.Rendition
import com.hamzalabs.maktaba.core.api.busyRetryMs
import com.hamzalabs.maktaba.core.api.errorDetails
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.player.media.NowPlaying
import com.hamzalabs.maktaba.player.media.SEEK_INCREMENT_MS
import com.hamzalabs.maktaba.player.media.castable
import com.hamzalabs.maktaba.player.media.isRemote
import com.hamzalabs.maktaba.core.playback.PlaybackValues
import com.hamzalabs.maktaba.core.playback.TrackChoice
import com.hamzalabs.maktaba.player.watch.TrackOption
import com.hamzalabs.maktaba.player.watch.applyChoice
import com.hamzalabs.maktaba.player.watch.choose
import com.hamzalabs.maktaba.player.watch.ensureAudio
import com.hamzalabs.maktaba.player.watch.subtitlesOff
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

/**
 * What is playing, for the whole app: the watch screen may be full screen (folded, open flat,
 * a tablet) or one half of a half-folded foldable with the library on the other, so the stream
 * lives here and not in a screen.
 *
 * Opens a stream session at a position (the resume point, FR-32), plays its signed URL (every
 * `/stream/` request carries the user's token as it is now: `StreamAuthInterceptor`), reports
 * progress every 10 s, on pause and on leaving, and "completed" at the end; reopens before the
 * signed URL ends (the home signs it for the video's length plus an hour, so normally never),
 * when the player fails, or at another quality (FR-35); and closes the session when stopped.
 * The audio and subtitle choice is kept for the next video (FR-34), and a stream with sound
 * always plays with an audio track selected (FR-39a).
 *
 * Epic 57 (stories 57.14, 57.15): the progress post is also the stream's heartbeat for an
 * account with time rules, so it goes out every 10 s while playing and at once on resume, and
 * its answer is read: when the home says the account's time is over (on opening, renewing or a
 * progress post) the video stops and [timeUp] says why; when it says the stream is gone,
 * [endedByHome]. Neither is asked about again until the person does ([checkAgain]).
 *
 * The player is handed to the system (48.33: [NowPlaying], the media session's service), so the
 * sound goes on with the screen off and the notification and lock screen drive it (FR-39); and
 * a Cast receiver can take it over ([control] then plays there, FR-38): it gets the same signed
 * URL, and the progress still goes out from here, read from the receiver.
 */
@OptIn(UnstableApi::class)
class Playback(
    private val context: Context,
    val client: ApiClient,
    private val scope: CoroutineScope,
    /** Outlives the screen: a stopped video's last position and its session's close still go out. */
    private val background: CoroutineScope,
    /** The app's sign-out, for a sign-in the home no longer takes (the screens over the player tell it too). */
    val signedOut: () -> Unit,
    /** The player's last touches: a test gives a clock and renderers that run on the JVM. */
    private val build: (ExoPlayer.Builder) -> ExoPlayer.Builder = { it },
    /** The player the controls drive, around this device's: Cast's (a test gives a fake receiver). */
    private val cast: (ExoPlayer) -> Player = { castable(context, it) },
    /** Hands the player to the system's controls (the notification, the lock screen). */
    private val system: Boolean = true,
    /** The time now, in milliseconds: when the stream was opened and when to renew it (a test gives its own clock). */
    private val nowMs: () -> Long = System::currentTimeMillis,
) {
    var video by mutableStateOf<Video?>(null)
        private set
    /** This device's player: it draws the video and its subtitles, and holds the tracks. */
    var player by mutableStateOf<ExoPlayer?>(null)
        private set
    /**
     * The player the controls, the notification and the progress go through: [player] while the
     * video plays here, a Cast receiver's while it plays there.
     */
    var control by mutableStateOf<Player?>(null)
        private set
    /** A Cast receiver plays the video (the phone shows that, not the video). */
    var casting by mutableStateOf(false)
        private set
    /** The video's size as it plays here (the picture-in-picture window takes its shape). */
    var videoSize by mutableStateOf(VideoSize.UNKNOWN)
        private set
    /** Why nothing plays (the error screen): what it means to a person, and the home's own words. */
    var problem by mutableStateOf<Failure?>(null)
        private set
    /**
     * The home has no transcode slot free (503 `transcode_busy`, Epic 59 story 59.05): no error
     * screen, the sentence shown while the stream is asked for again every 10 s.
     */
    var waiting by mutableStateOf<Failure?>(null)
        private set
    /**
     * The home said this account's watching time is over (403 `parental-time`): the video has
     * stopped where [TimeUp.positionMs] says, and the home is not asked again until [checkAgain].
     */
    var timeUp by mutableStateOf<TimeUp?>(null)
        private set
    /**
     * The home ended the stream while it played (a progress post answered 404: the session is
     * gone, or the video is no longer this account's). The video has stopped; nothing asks again.
     */
    var endedByHome by mutableStateOf(false)
        private set
    /** The home could not check the account's time at the last progress post (503); the next one asks again. */
    var cannotCheck by mutableStateOf(false)
        private set
    /** The stream's tracks, as the player last read them (the picker lists them). */
    var tracks by mutableStateOf(Tracks.EMPTY)
        private set
    var isPlaying by mutableStateOf(false)
        private set
    /** The video played to its end. */
    var ended by mutableStateOf(false)
        private set
    /** The qualities the session offers (its `ladder`; empty for direct play). */
    var ladder by mutableStateOf<List<Rendition>>(emptyList())
        private set
    /** The quality asked for (`max_bitrate_kbps`); null: Auto. Kept for the next video. */
    var maxBitrateKbps by mutableStateOf<Int?>(null)
        private set
    /** The audio and subtitle choice, kept for the next video in the series. */
    var choice by mutableStateOf(TrackChoice())
        private set
    /** The Settings the player starts each video with (subtitles on or off, their language). */
    var prefs: PlaybackValues = PlaybackValues()
    /** The person chose a quality in the player: the Settings' no longer apply. */
    private var qualityChosen = false

    private var session: String? = null
    /** Where the video was opened (Try again opens there). */
    private var startedAtSec = 0
    /** When to reopen: just before the signed URL ends. */
    private var renewAt = Long.MAX_VALUE
    private var openedAt = 0L
    /** The stream's work; on the main thread, where the player lives, whatever [scope]'s dispatcher. */
    private var running: Job? = null
    /** One open at a time: the 10-second watch's reopen and a quality change never overlap. */
    private val opening = Mutex()
    /** The reopen after a lapsed lease (`no_heartbeat`), and when the last one took. */
    private var lapseReopen: Job? = null
    private var lapseReopenedAt = 0L
    private val streamHttp by lazy { client.streamHttp() }

    /** Plays [v] from [startSec] (its resume point, or 0 for the start). */
    fun play(v: Video, startSec: Int = 0) {
        if (v.id == video?.id && problem == null && timeUp == null && !endedByHome) return
        stop()
        video = v
        startedAtSec = startSec.coerceAtLeast(0)
        running = scope.launch(Dispatchers.Main.immediate) { run(fromMs = startedAtSec * 1000L) }
    }

    /** Plays the video again after [problem] (Try again), from where it was opened. */
    fun retry() {
        val v = video ?: return
        val from = startedAtSec
        stop()
        play(v, from)
    }

    /**
     * The person's own tap on the time's-up screen (or after a parent gave more time): the same
     * video again, from where it stopped. The home answers as it does any open.
     */
    fun checkAgain() {
        val v = video ?: return
        val from = ((timeUp?.positionMs ?: (startedAtSec * 1000L)) / 1000).toInt()
        stop()
        play(v, from)
    }

    fun stop() {
        running?.cancel()
        running = null
        lapseReopen?.cancel()
        lapseReopen = null
        lapseReopenedAt = 0L
        val s = session
        if (s != null) {
            val at = control?.currentPosition?.div(1000)?.toInt() ?: 0
            val done = ended
            background.launch {
                if (!done && at > 0) client.reportProgress(s, at)
                client.closeStream(s)
            }
        }
        releasePlayers()
        session = null
        problem = null
        waiting = null
        timeUp = null
        endedByHome = false
        cannotCheck = false
        video = null
        tracks = Tracks.EMPTY
        isPlaying = false
        ended = false
        ladder = emptyList()
    }

    /**
     * Lets go of both players, and the system's controls of them. [stopReceiver]: the home
     * stopped the video (its time is over, or it is no longer the account's), so a Cast receiver
     * is told to stop too; let go of only, it could play the signed URL on until the streaming
     * service cuts it.
     */
    private fun releasePlayers(stopReceiver: Boolean = false) {
        val c = control
        val p = player
        if (c != null) {
            if (system) NowPlaying.detach(c)
            if (c !== p) {
                if (stopReceiver) c.stop()
                c.release()
            }
        }
        p?.release()
        control = null
        player = null
        casting = false
        videoSize = VideoSize.UNKNOWN
    }

    /**
     * Plays at another quality (FR-35): the session reopens at the same position with
     * [kbps] as its `max_bitrate_kbps` (null: Auto, the home's own ceiling).
     */
    fun setQuality(kbps: Int?) {
        qualityChosen = true
        if (kbps == maxBitrateKbps) return
        maxBitrateKbps = kbps
        val p = control ?: return
        val at = p.currentPosition
        val wasPlaying = p.playWhenReady
        scope.launch(Dispatchers.Main.immediate) {
            opening.withLock { open(fromMs = at) }
            if (!wasPlaying) control?.playWhenReady = false
        }
    }

    /**
     * The quality a new video starts at, from Settings (FR-31: Wi-Fi's or mobile data's), unless
     * the person chose one in the player, which is kept for the next video.
     */
    fun startAt(kbps: Int?) {
        if (!qualityChosen) maxBitrateKbps = kbps
    }

    /** Plays the subtitle or audio track [option]; kept for the next video. */
    fun select(option: TrackOption) {
        val p = player ?: return
        choice = p.choose(option, choice)
    }

    /** Turns subtitles off; kept for the next video. */
    fun subtitlesOff() {
        val p = player ?: return
        choice = p.subtitlesOff(choice)
    }

    private suspend fun run(fromMs: Long) {
        openWhenFree(fromMs)
        if (player != null) watch()
    }

    /**
     * Opens at [fromMs]; while the home has no transcode slot free ([waiting]), asks again every
     * 10 s (its `Retry-After`) until one is.
     */
    private suspend fun openWhenFree(fromMs: Long) {
        while (true) {
            opening.withLock { open(fromMs) }
            val w = waiting ?: return
            delay(busyRetryMs(w.retryAfterSec))
        }
    }

    /**
     * Opens (or reopens) the stream at [fromMs]. The session in use is closed only once the new
     * one plays; a reopen that fails leaves the old stream playing (it shows why only when there
     * is nothing left to play).
     */
    private suspend fun open(fromMs: Long) {
        val v = video ?: return
        val old = session
        try {
            // a session the home opened is always closed, even when the player stopped meanwhile
            val s = withContext(NonCancellable) {
                client.openStream(v.id, (fromMs / 1000).toInt(), maxBitrateKbps = maxBitrateKbps)
            }
            try {
                currentCoroutineContext().ensureActive()
                // the video stopped for time, or the home ended it, while this open was on its
                // way: nothing starts under that screen (the session is closed below)
                if (stoppedByHome()) throw CancellationException("The video stopped while it was being opened.")
                val url = s.playbackUrl(client.home) ?: throw IllegalStateException("The home gave no address to play.")
                client.waitUntilPlayable(url)
                if (stoppedByHome()) throw CancellationException("The video stopped while it was being opened.")
                val item = MediaItem.Builder()
                    .setUri(url.toString())
                    .apply { if (s.isHls) setMimeType(MimeTypes.APPLICATION_M3U8) }
                    // the notification's, the lock screen's and the receiver's title
                    .setMediaMetadata(MediaMetadata.Builder().setTitle(v.displayTitle).build())
                    .build()
                currentCoroutineContext().ensureActive()
                val p = player ?: newPlayer()
                val c = control ?: newControl(p)
                // while casting, the receiver is handed the new URL (a reopen, another quality)
                c.setMediaItem(item, fromMs)
                c.prepare()
                c.playWhenReady = true
                player = p
                control = c
                if (system) NowPlaying.attach(context, c)
            } catch (e: Throwable) {
                background.launch { client.closeStream(s.sessionId) }
                throw e
            }
            session = s.sessionId
            waiting = null
            ladder = s.ladder
            ended = false
            openedAt = nowMs()
            renewAt = (s.expiresMillis ?: Long.MAX_VALUE).let { if (it == Long.MAX_VALUE) it else it - 60_000 }
            if (old != null && old != s.sessionId) background.launch { client.closeStream(old) }
        } catch (e: CancellationException) {
            // stopped by the home meanwhile: this open ends quietly, its caller goes on
            if (!stoppedByHome()) throw e
        } catch (e: ApiException.SignedOut) {
            stop()
            signedOut()
        } catch (e: Exception) {
            val over = e.timeRefusal()
            if (over != null) {
                // the account's time is over: nothing plays on, not the stream before either
                stopForTime(over, fromMs)
                return
            }
            if (old != null && control?.playerError == null) return
            Log.w("Maktaba", "playback: ${e.errorDetails()}", e)
            releasePlayers()
            val f = Failure.of(e)
            if (f.kind == ErrorKind.TranscodeBusy) {
                waiting = f
            } else {
                waiting = null
                problem = f
            }
        }
    }

    /** The time's-up screen or "ended by the home" shows: nothing may start playing under it. */
    private fun stoppedByHome(): Boolean = timeUp != null || endedByHome

    private fun newPlayer(): ExoPlayer {
        val data = OkHttpDataSource.Factory(streamHttp)
        val p = ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(context).setDataSourceFactory(data))
            .setAudioAttributes(
                AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MOVIE).build(),
                true,
            )
            .setHandleAudioBecomingNoisy(true)
            // ±10 s from the notification and the lock screen too
            .setSeekBackIncrementMs(SEEK_INCREMENT_MS)
            .setSeekForwardIncrementMs(SEEK_INCREMENT_MS)
            // the stream keeps loading with the screen off (FR-39)
            .setWakeMode(C.WAKE_MODE_NETWORK)
            .let(build)
            .build()
        // the kept choice (or the Settings' defaults) for the new video
        p.trackSelectionParameters = p.trackSelectionParameters.buildUpon().applyChoice(choice, prefs).build()
        p.addListener(object : Player.Listener {
            override fun onTracksChanged(t: Tracks) {
                // a stream with sound plays with sound (FR-39a)
                if (p.ensureAudio(t)) Log.i("Maktaba", "player: no audio track was selected; chose the first")
                tracks = t
            }

            override fun onVideoSizeChanged(size: VideoSize) {
                videoSize = size
            }
        })
        return p
    }

    /**
     * The player the controls drive around [local] (Cast's, which moves the video to a receiver
     * and back): playing, paused, the end and where it plays are read here, wherever it plays.
     */
    private fun newControl(local: ExoPlayer): Player {
        val p = cast(local)
        p.addListener(object : Player.Listener {
            override fun onDeviceInfoChanged(deviceInfo: DeviceInfo) {
                casting = isRemote(p)
            }

            override fun onIsPlayingChanged(playing: Boolean) {
                isPlaying = playing
            }

            override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
                // paused: "continue watching" resumes here (FR-32). Resumed: the home hears of it at
                // once, as a time-limited account's stream is served only while it does (57.14)
                if (p.playbackState != Player.STATE_ENDED) report(p, completed = false)
            }

            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_ENDED && !ended) {
                    ended = true
                    report(p, completed = true)
                }
                if (state != Player.STATE_ENDED) ended = false
            }
        })
        casting = isRemote(p)
        return p
    }

    private fun report(p: Player, completed: Boolean) {
        val s = session ?: return
        val at = (p.currentPosition / 1000).toInt()
        if (at <= 0 && !completed) return
        // sent from the scope that outlives the screen; its answer is read where the player lives
        background.launch {
            val answer = client.reportProgress(s, at, completed)
            withContext(Dispatchers.Main) { heard(s, answer, at * 1000L, completed) }
        }
    }

    /** What the home said to a progress post of session [s] at [atMs]. */
    private fun heard(s: String, answer: ProgressAnswer, atMs: Long, completed: Boolean) {
        // a post of a session since closed or replaced says nothing about what plays now
        if (session != s) return
        when (answer) {
            ProgressAnswer.Ok -> cannotCheck = false
            is ProgressAnswer.TimeOver -> refusedForTime(answer.refusal, atMs)
            ProgressAnswer.Ended -> if (!completed && !ended) {
                Log.i("Maktaba", "playback: the home ended the stream")
                session = null
                releasePlayers(stopReceiver = true)
                isPlaying = false
                waiting = null
                endedByHome = true
            }
            is ProgressAnswer.CannotCheck -> cannotCheck = true
            is ProgressAnswer.Failed -> {}
        }
    }

    /**
     * The home refused the stream that plays, for time. `no_heartbeat` is not the time being
     * over: the stream sent no progress for a minute (a long pause) and its lease lapsed. It is
     * opened again once, where it is; the home's answer to that open says whether there is time
     * ([open] stops for it when there is not). A lapse again right after such a reopen stops.
     * Called for a progress post's answer and for the stream itself refused (the player's error).
     */
    internal fun refusedForTime(refusal: TimeRefusal, atMs: Long) {
        val s = session
        val sinceReopen = nowMs() - lapseReopenedAt
        if (refusal.reason != TimeReason.NO_HEARTBEAT || s == null || sinceReopen < LAPSE_REOPEN_ONCE_MS) {
            stopForTime(refusal, atMs)
            return
        }
        if (lapseReopen?.isActive == true) return
        Log.i("Maktaba", "playback: the stream's lease lapsed; opening it again")
        val wasPlaying = control?.playWhenReady == true
        lapseReopen = scope.launch(Dispatchers.Main.immediate) {
            opening.withLock {
                // stopped, or already replaced, while this waited its turn
                if (session != s) return@withLock
                open(fromMs = atMs.coerceAtLeast(0))
                if (session != null && session != s) lapseReopenedAt = nowMs()
            }
            if (!wasPlaying) control?.playWhenReady = false
        }
    }

    /** Stops for [refusal] at [atMs]: the stream is closed and nothing asks the home again by itself. */
    private fun stopForTime(refusal: TimeRefusal, atMs: Long) {
        Log.i("Maktaba", "playback: the account's time is over (${refusal.reason})")
        val s = session
        session = null
        if (s != null) background.launch { client.closeStream(s) }
        releasePlayers(stopReceiver = true)
        isPlaying = false
        waiting = null
        problem = null
        cannotCheck = false
        timeUp = TimeUp(refusal, atMs.coerceAtLeast(0))
    }

    /**
     * Every 10 s while playing: the position, so "continue watching" resumes here; a reopen where
     * it is before the URL ends, or when the player failed (not again within 30 s of opening:
     * then it shows why).
     */
    private suspend fun watch() {
        while (scope.isActive) {
            delay(10_000)
            // the receiver's position while casting
            val p = control ?: return
            session ?: return
            // the end was said once, when it came
            if (p.playbackState == Player.STATE_ENDED) continue
            if (p.isPlaying) report(p, completed = false)
            val failed = p.playerError
            // the streaming service refused the stream itself because the time is over
            failed?.let(::playbackTimeRefusal)?.let {
                refusedForTime(it, p.currentPosition)
                if (control == null) return
                continue
            }
            if (failed != null && nowMs() - openedAt < 30_000) {
                releasePlayers()
                Log.w("Maktaba", "player: ${failed.errorCodeName}", failed)
                problem = playbackFailure(failed)
                return
            }
            if (failed != null || nowMs() >= renewAt) {
                val wasPlaying = p.playWhenReady
                openWhenFree(fromMs = p.currentPosition)
                if (!wasPlaying) control?.playWhenReady = false
            }
        }
    }
}

/** A lease that lapses again this soon after it was reopened for a lapse stops the video. */
const val LAPSE_REOPEN_ONCE_MS = 30_000L

/** Why and where a video stopped for time: the home's refusal and the position it stopped at. */
data class TimeUp(val refusal: TimeRefusal, val positionMs: Long = 0)
