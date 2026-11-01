package com.hamzalabs.maktaba.player.watch

import android.content.Context
import android.os.Looper
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.test.utils.FakeClock
import androidx.media3.test.utils.robolectric.CapturingRenderersFactory
import androidx.media3.test.utils.robolectric.RobolectricUtil
import com.hamzalabs.maktaba.player.harness.FakeHome
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import mockwebserver3.RecordedRequest
import okio.Buffer
import org.robolectric.Shadows.shadowOf
import java.time.Duration
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

    fun install(sessions: Int = 4) {
        home.answer("/api/stream/sessions") {
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
 */
@OptIn(UnstableApi::class)
fun testPlayer(context: Context): (ExoPlayer.Builder) -> ExoPlayer.Builder = { b ->
    val clock = FakeClock(true)
    b.setClock(clock).setRenderersFactory(CapturingRenderersFactory(context, clock))
}

/** Runs the main looper until [condition] holds (10 s at most). */
fun runUntil(timeoutMs: Long = 10_000, condition: () -> Boolean) {
    RobolectricUtil.runMainLooperUntil(timeoutMs) { condition() }
}

/** Moves the main looper's clock on (the player's 10-second watch, Up next's countdown). */
fun idleFor(ms: Long) = shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(ms))
