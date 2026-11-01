package com.hamzalabs.maktaba.player.live

import android.content.Context
import androidx.compose.foundation.Image
import coil3.ImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.request.SuccessResult
import kotlinx.coroutines.Dispatchers
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import androidx.compose.foundation.layout.Box
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.ui.components.FakePictures
import com.hamzalabs.maktaba.player.ui.components.LocalHomeImages
import mockwebserver3.MockResponse
import okhttp3.HttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import java.time.Instant
import java.time.ZoneOffset
import kotlin.concurrent.thread

/** The tests' "now": the home's clock and this device's agree. */
val NOW: Long = Instant.parse("2026-10-31T18:20:00Z").toEpochMilli()

private fun iso(ms: Long) = Instant.ofEpochMilli(ms).toString()
private const val MIN = 60_000L

/**
 * Ten channels, each a run of programmes from 16:00 to 02:00 of a length its own (30, 45, 60 or
 * 90 minutes), starting a little apart so the guide's blocks are staggered. Channel 1's
 * programme 18:00–18:40 is half through at [NOW].
 */
object LiveFixtures {
    data class Ch(val id: String, val number: Int, val name: String, val logo: Boolean)

    val CHANNELS = listOf(
        Ch("c1", 1, "Quran", true),
        Ch("c2", 2, "Seerah", true),
        Ch("c3", 3, "Talks", false),
        Ch("c4", 4, "قناة الأطفال", true),
        Ch("c5", 5, "Documentaries", false),
        Ch("c6", 6, "Recitations", true),
        Ch("c7", 7, "Lectures", false),
        Ch("c8", 8, "News", true),
        Ch("c9", 9, "Films", false),
        Ch("c10", 10, "Nasheed", true),
    )

    private val TITLES = listOf(
        "Tafsir of Al-Baqarah", "The Prophet's Life", "Evening Talk", "قصص الأنبياء", "The Silk Road",
        "Juz' Amma", "Fiqh of Fasting", "World Report", "The Message", "Andalusian Songs",
    )

    data class Block(val channel: String, val start: Long, val stop: Long, val title: String)

    fun blocks(c: Ch): List<Block> {
        if (c.id == "c1") {
            // 18:00–18:40, half through at NOW; then 40-minute blocks either side
            return (-3..12).map { k ->
                val s = Instant.parse("2026-10-31T18:00:00Z").toEpochMilli() + k * 40 * MIN
                Block(c.id, s, s + 40 * MIN, if (k == 0) "Tafsir of Al-Baqarah" else "${TITLES[0]}, part ${k + 4}")
            }
        }
        val length = listOf(30L, 45L, 60L, 90L)[c.number % 4]
        var s = Instant.parse("2026-10-31T16:00:00Z").toEpochMilli() + (c.number * 7 % 30) * MIN
        val out = mutableListOf<Block>()
        var k = 0
        while (s < Instant.parse("2026-11-01T02:00:00Z").toEpochMilli()) {
            out += Block(c.id, s, s + length * MIN, "${TITLES[(c.number - 1) % TITLES.size]} ${k + 1}")
            s += length * MIN
            k++
        }
        return out
    }

    private fun channelJson(c: Ch) =
        """{"id":"${c.id}","number":${c.number},"name":"${c.name}","slug":"${c.id}"${if (c.logo) ""","logo_url":"/api/channels/${c.id}/logo"""" else ""}}"""

    private fun blockJson(b: Block, now: Long) =
        """{"channel_id":"${b.channel}","kind":"program","start":"${iso(b.start)}","stop":"${iso(b.stop)}","title":"${b.title}",""" +
            """"desc":"An episode of ${b.title}.","is_live":${now in b.start until b.stop},"progress":0}"""

    fun nowJson(channels: List<Ch> = CHANNELS, now: Long = NOW): String = channels.joinToString(",", """{"server_time":"${iso(now)}","channels":[""", "]}") { c ->
        val bs = blocks(c)
        val i = bs.indexOfFirst { now in it.start until it.stop }
        val cur = bs.getOrNull(i)?.let { blockJson(it, now) } ?: "null"
        val next = bs.getOrNull(i + 1)?.let { blockJson(it, now) } ?: "null"
        """{"channel":${channelJson(c)},"current":$cur,"next":$next,"on_air":true}"""
    }

    fun guideJson(channels: List<Ch> = CHANNELS, now: Long = NOW): String = channels.joinToString(",", """{"server_time":"${iso(now)}","channels":[""", "]}") { c ->
        """{"channel":${channelJson(c)},"blocks":[${blocks(c).joinToString(",") { blockJson(it, now) }}]}"""
    }

    const val TV_INFO_LAN = """{"stream_base_url":"","hls_path_template":"/stream/channel/{id}/index.m3u8","remote":{"available":true,"via_relay":false}}"""
    const val TV_INFO_RELAY = """{"stream_base_url":"","hls_path_template":"/stream/channel/{id}/index.m3u8","remote":{"available":true,"via_relay":true,"max_streams":2,"token_ttl_minutes":60}}"""

    fun tuneJson(session: String) =
        """{"session_id":"$session","state":"live","hls_url":"https://customer-abc.cloudflarestream.com/tok-$session/manifest/video.m3u8"}"""

    const val MASTER = "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=1280x720\n720p.m3u8\n"
    const val MEDIA = "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:10\n#EXTINF:6.0,\nseg10.ts\n"

    /** The fake home's Live TV: Now, the guide, logos, `tv/info` at home, and each channel's HLS. */
    fun install(home: FakeHome, channels: List<Ch> = CHANNELS, relay: Boolean = false) {
        home.answer("/api/channels/now") { FakeHome.json(nowJson(channels)) }
        home.answer("/api/channels/guide") { FakeHome.json(guideJson(channels)) }
        home.answer("/api/tv/info") { FakeHome.json(if (relay) TV_INFO_RELAY else TV_INFO_LAN) }
        var sessions = 0
        home.answer("/api/tv/remote/tune") { FakeHome.json(tuneJson("s-${++sessions}")) }
        channels.forEach { c ->
            home.answer("/stream/channel/${c.id}/index.m3u8") { playlist(MASTER) }
            home.answer("/stream/channel/${c.id}/720p.m3u8") { playlist(MEDIA) }
        }
        (1..9).forEach { n ->
            home.answer("/api/tv/remote/sessions/s-$n") { MockResponse.Builder().code(204).build() }
            home.answer("/api/tv/remote/sessions/s-$n/heartbeat") { FakeHome.json("""{"state":"live"}""") }
        }
    }

    private fun playlist(body: String) =
        MockResponse.Builder().code(200).setHeader("Content-Type", "application/vnd.apple.mpegurl").body(body).build()
}

/**
 * A player for the tests: it records what it is asked to play and, for the fake home's own
 * address, fetches the playlist through the client it was given (so the token and the mock's
 * answer are part of "it plays"). Paused and behind-live follow the person's taps as Media3's do.
 */
class FakeLiveEngine(private val fetch: Boolean = true) : LiveEngine {
    override var paused by mutableStateOf(false)
        private set
    override var behindLive by mutableStateOf(false)
        private set

    private val lock = Any()
    private val _played = mutableListOf<HttpUrl>()
    private val _fetched = mutableListOf<Pair<HttpUrl, Int>>()
    val played: List<HttpUrl> get() = synchronized(lock) { _played.toList() }
    val fetched: List<Pair<HttpUrl, Int>> get() = synchronized(lock) { _fetched.toList() }
    var releases = 0
        private set
    var goLives = 0
        private set

    /** Every play fails at once with this (a channel that will not start). */
    var failWith: Throwable? = null

    override fun play(url: HttpUrl, http: OkHttpClient, onError: (Throwable) -> Unit) {
        synchronized(lock) { _played += url }
        paused = false
        behindLive = false
        failWith?.let { onError(it); return }
        if (fetch && (url.host == "localhost" || url.host == "127.0.0.1" || url.host.endsWith(".local"))) {
            thread {
                val code = runCatching { http.newCall(Request.Builder().url(url).build()).execute().use { it.code } }.getOrDefault(-1)
                synchronized(lock) { _fetched += url to code }
            }
        }
    }

    override fun pause() {
        paused = true
        behindLive = true
    }

    override fun resume() {
        paused = false
    }

    override fun goLive() {
        goLives++
        paused = false
        behindLive = false
    }

    override fun release() {
        releases++
    }

    @Composable
    override fun Video(modifier: Modifier) {
        val frame = remember { FakePictures.picture("live-frame", 1280, 720).asImageBitmap() }
        Box(modifier.testTag("fake-video")) { Image(frame, null, Modifier.matchParentSize(), contentScale = ContentScale.Crop) }
    }
}

/**
 * [FakePictures]' pictures keyed by the URL's path alone: the fake home's port changes every run,
 * and a picture that followed it would change every screenshot.
 */
fun portlessPictures(context: Context): ImageLoader = ImageLoader.Builder(context)
    .memoryCache(null)
    .diskCache(null)
    .coroutineContext(Dispatchers.Unconfined)
    .interceptorCoroutineContext(Dispatchers.Unconfined)
    .components {
        add(
            Interceptor { chain ->
                val url = chain.request.data.toString()
                val key = url.toHttpUrlOrNull()?.encodedPath ?: url
                SuccessResult(FakePictures.picture(key).asImage(), chain.request, DataSource.MEMORY)
            },
        )
    }
    .build()

/**
 * Live TV's world in the tests: [NOW] in UTC, and [engine] playing. The clock stands still, so
 * the minute's refresh and the progress tick wait a day (a test's clock may run on its own while
 * it waits; a refresh each virtual minute would keep the screen busy).
 */
fun testEnv(engine: LiveEngine) =
    LiveEnv(nowMs = { NOW }, zone = { ZoneOffset.UTC }, engine = { engine }, device = "test", refreshMs = DAY_MS, tickMs = DAY_MS)

private const val DAY_MS = 86_400_000L

/**
 * The Player as MainActivity shows it, for Live TV's tests: the shell with [sections] in the dark
 * theme, the fake home signed in, its pictures from [FakePictures], [engine] playing, and the
 * navigation hidden while a channel plays.
 */
fun ComposeContentTestRule.setLive(
    home: FakeHome,
    engine: LiveEngine = FakeLiveEngine(),
    window: Window = Window.Phone,
    sections: List<PlayerSection> = listOf(PlayerSection.Libraries, PlayerSection.LiveTv),
    content: (@Composable (PlayerHome) -> Unit)? = null,
) {
    setContent {
        var playing by remember { mutableStateOf(false) }
        val fake = home.home
        val signedIn = remember { PlayerHome(fake.session, fake.user, fake.client, fake.themes, onPlaying = { playing = it }) }
        val context = LocalContext.current
        val images = remember { portlessPictures(context) }
        MaktabaTheme(ThemeMode.Dark, forceDark = playing) {
            Surface {
                CompositionLocalProvider(
                    LocalPlayerHome provides signedIn,
                    LocalHomeImages provides images,
                    LocalLiveEnv provides testEnv(engine),
                ) {
                    if (content != null) content(signedIn)
                    else PlayerShell(sections = sections, adaptiveInfo = window.info(), immersive = playing)
                }
            }
        }
    }
}
