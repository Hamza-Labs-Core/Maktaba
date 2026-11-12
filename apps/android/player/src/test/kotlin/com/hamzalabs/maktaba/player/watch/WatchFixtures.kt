package com.hamzalabs.maktaba.player.watch

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.test.utils.FakeClock
import androidx.media3.test.utils.ThreadTestUtil
import androidx.media3.test.utils.robolectric.CapturingRenderersFactory
import com.hamzalabs.maktaba.player.harness.FakeHome
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import mockwebserver3.RecordedRequest
import okio.Buffer
import org.robolectric.Shadows.shadowOf
import org.robolectric.shadows.ShadowSystemClock
import java.time.Duration
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.concurrent.locks.LockSupport
import java.util.concurrent.atomic.AtomicInteger

/**
 * The player's test streams, made with ffmpeg once and kept in `src/test/resources/media`
 * (4 s, 64×36, H.264 with a 440 Hz AAC tone):
 * - `hls/`: an HLS master with two subtitle renditions (English, العربية) as the home lists
 *   them since 48.4 (`EXT-X-MEDIA TYPE=SUBTITLES`, `DEFAULT=NO,AUTOSELECT=YES`), and a video
 *   rendition of two TS segments, each with video and sound;
 * - `two-audio.mp4`: direct play with two audio tracks, English and Arabic.
 */
object TestStreams {
    val HLS_FILES = listOf(
        "master.m3u8", "video.m3u8", "seg0.ts", "seg1.ts",
        "subs-en/index.m3u8", "subs-en/en.vtt", "subs-ar/index.m3u8", "subs-ar/ar.vtt",
    )
    const val LENGTH_MS = 4_000L

    fun bytes(path: String): ByteArray =
        requireNotNull(TestStreams::class.java.classLoader!!.getResourceAsStream("media/$path")) { "no test stream media/$path" }.readBytes()
}

/**
 * The home's stream routes on [FakeHome]: `POST /api/stream/sessions` opens s1, s2, … each
 * playing [direct] (`/stream/sN/two-audio.mp4`) or else the HLS master (`/stream/sN/master.m3u8`),
 * with [ladder] as its qualities; every stream file is served under every session.
 */
class FakeStreams(private val home: FakeHome, var direct: Boolean = false, var ladder: String = "[]") {
    private val opened = AtomicInteger()
    /** How many opens more answer 503 `transcode_busy` (story 59.05) before one opens. */
    val busy = AtomicInteger()

    fun install(sessions: Int = 4) {
        home.answer("/api/stream/sessions") {
            if (busy.getAndUpdate { if (it > 0) it - 1 else 0 } > 0) {
                return@answer MockResponse.Builder().code(503)
                    .setHeader("Content-Type", "application/problem+json")
                    .setHeader("Retry-After", "10")
                    .body("""{"type":"https://maktaba.tv/problems/transcode-busy","title":"Another video is being converted","status":503,"code":"transcode_busy"}""")
                    .build()
            }
            val n = opened.incrementAndGet()
            val url = if (direct) "/stream/s$n/two-audio.mp4?sig=x" else "/stream/s$n/master.m3u8?sig=x"
            val key = if (direct) "direct_url" else "manifest_url"
            FakeHome.json(
                """{"session_id":"s$n","mode":"${if (direct) "direct" else "transcode"}","$key":"$url","expires_at":"2099-01-01T00:00:00Z","ladder":$ladder}""",
                code = 201,
            )
        }
        for (n in 1..sessions) {
            TestStreams.HLS_FILES.forEach { f -> home.answer("/stream/s$n/$f") { file("hls/$f") } }
            home.answer("/stream/s$n/two-audio.mp4") { file("two-audio.mp4") }
            home.answer("/api/stream/sessions/s$n") { MockResponse.Builder().code(204).build() }
            home.answer("/api/stream/sessions/s$n/progress") { MockResponse.Builder().code(204).build() }
        }
    }

    private fun file(path: String): MockResponse {
        val type = when {
            path.endsWith(".m3u8") -> "application/vnd.apple.mpegurl"
            path.endsWith(".ts") -> "video/mp2t"
            path.endsWith(".vtt") -> "text/vtt"
            else -> "video/mp4"
        }
        return MockResponse.Builder().code(200).setHeader("Content-Type", type).body(Buffer().write(TestStreams.bytes(path))).build()
    }

    /** The bodies of the sessions opened, in order. */
    fun opens(): List<JsonObject> = home.requests
        .filter { it.method == "POST" && it.url.encodedPath == "/api/stream/sessions" }
        .map { it.json() }

    /** The progress reports, in order: session and position. */
    fun progress(): List<Pair<String, JsonObject>> = home.requests
        .filter { it.method == "POST" && it.url.encodedPath.endsWith("/progress") }
        .map { it.url.encodedPath.split('/')[4] to it.json() }

    fun closed(): List<String> = home.requests
        .filter { it.method == "DELETE" && it.url.encodedPath.startsWith("/api/stream/sessions/") }
        .map { it.url.encodedPath.substringAfterLast('/') }
}

fun RecordedRequest.json(): JsonObject = Json.parseToJsonElement(body!!.utf8()).jsonObject

fun JsonObject.int(key: String): Int? = this[key]?.jsonPrimitive?.int

/**
 * The player as the tests build it: a clock that runs as fast as the stream allows and the
 * decoders faked on the JVM (Robolectric has none), so a stream really plays: tracks are read,
 * renderers enabled, samples fed.
 *
 * The clock moves in step with the test: only as the test runs the main looper ([runUntil],
 * [idleFor], [settle]), [IN_STEP_MS] of the stream at a time. Left to itself it runs the whole
 * 4-second stream on the playback thread while the test is between two lines, on a slow machine
 * to its end, and what the test does next (a pause that posts the position) then meets an ended
 * video.
 *
 * Never idle the main looper whole with such a player (`ShadowLooper.idle()`, `idleFor()`): the
 * clock hands the looper its next message as the last one is handled, so the looper never runs
 * dry and the call never returns. [idleFor] and [runDue] here stop at a mark.
 */
@OptIn(UnstableApi::class)
fun testPlayer(context: Context): (ExoPlayer.Builder) -> ExoPlayer.Builder = { b ->
    val clock = FakeClock(true)
    // a message of the clock's own for the main looper: the clock hands out one message at a
    // time and waits for it to be handled, so it stops here until the test's thread runs it
    val main = clock.createHandler(Looper.getMainLooper(), null)
    main.postDelayed(object : Runnable {
        override fun run() {
            // a breath for the other threads: with nothing else to run, the clock would hand
            // this straight back, and the test's wait would spin on it
            LockSupport.parkNanos(50_000)
            main.postDelayed(this, IN_STEP_MS)
        }
    }, IN_STEP_MS)
    b.setClock(clock).setRenderersFactory(CapturingRenderersFactory(context, clock))
}

/** How far the player's clock runs between two looks of the test. */
const val IN_STEP_MS = 20L

/** The safety net of every wait here: far longer than any wait takes, so only a real stall meets it. */
const val STALL_MS = 60_000L

/**
 * The time [Playback] is given in these tests: the main looper's clock (which only [idleFor]
 * moves) from a fixed date, so "30 seconds since the stream opened" is the test's to decide.
 */
val looperTime: () -> Long = { 1_794_000_000_000L + SystemClock.uptimeMillis() }

private val mainLooper get() = shadowOf(Looper.getMainLooper())

/**
 * Runs the main looper's due work, a task at a time, until [condition] holds. It waits for the
 * state, not for a length of time: [timeoutMs] is only the net under a stall. The looper's clock
 * is not moved (the 10-second watch does not tick; [idleFor] moves it), and nothing due means
 * the answer is still on another thread, which is given the processor.
 */
fun runUntil(timeoutMs: Long = STALL_MS, what: String? = null, condition: () -> Boolean) {
    val end = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMs)
    while (!condition()) {
        if (Thread.interrupted()) throw InterruptedException("interrupted while waiting" + (what?.let { " for $it" } ?: ""))
        if (System.nanoTime() > end) throw TimeoutException("still waiting after $timeoutMs ms" + (what?.let { " for $it" } ?: ""))
        ThreadTestUtil.unblockThreadsWaitingForProgressOnCurrentLooper()
        if (mainLooper.isIdle) LockSupport.parkNanos(200_000) else mainLooper.runOneTask()
    }
}

/**
 * Runs what is on the main looper and due now, up to a mark put behind it. (Idling the looper
 * whole never returns once a player's test clock hands it message after message: a player let
 * go of inside such a call, by a refusal that landed there, stalled the test and the whole job.)
 */
fun runDue() {
    var reached = false
    Handler(Looper.getMainLooper()).post { reached = true }
    runUntil(what = "the main looper to run what is due") { reached }
}

/** Moves the main looper's clock on (the player's 10-second watch, Up next's countdown) and runs what is due then. */
fun idleFor(ms: Long) {
    ShadowSystemClock.advanceBy(Duration.ofMillis(ms))
    runDue()
}

/**
 * Lets everything on its way land: returns once no call to [home] is queued or running, no
 * coroutine of [scopes] (the playback's background work) is alive, the home was asked nothing
 * new, and the main looper has run what those left it. After it "nothing more was posted" is a
 * fact, not a guess about how long an answer takes.
 */
fun settle(home: FakeHome, vararg scopes: CoroutineScope) {
    fun quiet() = home.idling.isIdleNow && scopes.all { it.coroutineContext[Job]!!.children.none() }
    val end = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(STALL_MS)
    while (true) {
        // a call or coroutine that ends from here on has already queued its next step on the
        // main looper, before the mark; one that starts is seen below, by itself or by its ask
        val asked = home.asked
        val before = quiet()
        runDue()
        if (before && quiet() && asked == home.asked) return
        if (System.nanoTime() > end) throw TimeoutException("the home's answers never settled; asked ${home.paths.takeLast(20)}")
        LockSupport.parkNanos(200_000)
    }
}
