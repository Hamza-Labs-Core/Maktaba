package com.hamzalabs.maktaba.tv

import android.content.Context
import android.os.Looper
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.api.BUSY_RETRY_SEC
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.busyRetryMs
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.playback.TvPlayback
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import java.time.Duration
import java.util.Base64
import java.util.concurrent.atomic.AtomicInteger

/**
 * Epic 59 story 59.05 on Android TV: a home whose transcode slots are all taken answers 503
 * `transcode_busy`; the TV shows no error screen, keeps the waiting sentence, and asks again
 * after the home's `Retry-After` (10 s), the same wait as the phone Player, until a stream opens.
 * Against a MockWebServer home; the main looper's clock is moved on by hand.
 */
@RunWith(AndroidJUnit4::class)
class TvPlaybackBusyTest {
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var server: MockWebServer
    private val opens = AtomicInteger()
    /** How many opens more answer 503 `transcode_busy` before one opens. */
    private val busy = AtomicInteger()
    private var retryAfter: String? = "10"
    private var playback: TvPlayback? = null

    private class Tokens(var a: String?, var r: String?) : TokenStore {
        override val access get() = a
        override val refresh get() = r
        override fun save(tokens: AuthTokens) { a = tokens.accessToken; r = tokens.refreshToken }
        override fun clear() { a = null; r = null }
    }

    private val token: String = Base64.getUrlEncoder().withoutPadding().let { enc ->
        enc.encodeToString("""{"alg":"RS256"}""".toByteArray()) + "." +
            enc.encodeToString("""{"exp":${System.currentTimeMillis() / 1000 + 86_400}}""".toByteArray()) + ".sig"
    }

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val p = request.url.encodedPath
                return when {
                    p == "/api/stream/sessions" && request.method == "POST" -> {
                        val n = opens.incrementAndGet()
                        if (busy.getAndUpdate { if (it > 0) it - 1 else 0 } > 0) {
                            MockResponse.Builder().code(503)
                                .setHeader("Content-Type", "application/problem+json")
                                .apply { retryAfter?.let { setHeader("Retry-After", it) } }
                                .body("""{"type":"https://maktaba.tv/problems/transcode-busy","title":"Another video is being converted","status":503,"code":"transcode_busy"}""")
                                .build()
                        } else {
                            MockResponse.Builder().code(201).setHeader("Content-Type", "application/json")
                                .body("""{"session_id":"s$n","mode":"direct","direct_url":"/stream/s$n/v1.mp4?sig=x","expires_at":"2099-01-01T00:00:00Z"}""")
                                .build()
                        }
                    }
                    else -> MockResponse.Builder().code(204).build()
                }
            }
        }
        server.start()
    }

    @After
    fun stop() {
        playback?.stop()
        scope.cancel()
        background.cancel()
        server.close()
    }

    /** The client's calls, counted ([settle]). */
    private val calls = HomeCalls()

    /** Lets everything on its way land ([settleLooper]): what the home was asked cannot still be moving. */
    private fun settle() = settleLooper(calls, background, asked = { opens.get() })

    private fun playback(): TvPlayback {
        val client = ApiClient(server.url("/"), Tokens(token, "r1"), calls.http)
        return TvPlayback(context, client, "v1", 0, scope, background, signedOut = {}).also { playback = it }
    }

    /** Runs the main looper's due work (the home's answers land there) until [condition] holds. */
    private fun runUntil(timeoutMs: Long = STALL_MS, condition: () -> Boolean) {
        val until = System.currentTimeMillis() + timeoutMs
        while (!condition()) {
            check(System.currentTimeMillis() < until) { "timed out waiting" }
            shadowOf(Looper.getMainLooper()).idle()
            Thread.sleep(10)
        }
    }

    private fun idleFor(ms: Long) = shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(ms))

    @Test
    fun aHomeWithNoTranscodeSlotFreeIsAskedAgainEveryTenSecondsWithNoErrorScreen() {
        busy.set(2)
        val pb = playback()
        pb.start()
        runUntil { pb.waiting != null }
        assertEquals(ErrorKind.TranscodeBusy, pb.waiting!!.kind)
        assertEquals("the home's Retry-After", 10L, pb.waiting!!.retryAfterSec)
        assertNull("no error screen while it waits", pb.problem)
        assertNull(pb.player)
        assertEquals("asked once so far", 1, opens.get())

        // just short of the wait: not asked again yet
        settle()
        idleFor(9_000)
        settle()
        assertEquals("not before the home's Retry-After", 1, opens.get())

        idleFor(1_000)
        runUntil { opens.get() == 2 && !pb.opening }
        assertNotNull("still waiting", pb.waiting)
        assertNull(pb.problem)

        idleFor(10_000)
        runUntil { pb.player != null }
        assertEquals("asked three times", 3, opens.get())
        assertNull("a stream opened: the sentence is gone", pb.waiting)
        assertNull(pb.problem)
    }

    @Test
    fun aBusyHomeWithNoRetryAfterIsAskedAgainAfterTenSeconds() {
        busy.set(1)
        retryAfter = null
        val pb = playback()
        pb.start()
        runUntil { pb.waiting != null }
        assertNull(pb.waiting!!.retryAfterSec)
        idleFor(BUSY_RETRY_SEC * 1000)
        runUntil { pb.player != null }
        assertEquals(2, opens.get())
        assertNull(pb.waiting)
    }

    @Test
    fun anotherFailureStillShowsTheErrorScreen() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                opens.incrementAndGet()
                return MockResponse.Builder().code(503).setHeader("Content-Type", "application/problem+json")
                    .body("""{"status":503,"code":"draining"}""").build()
            }
        }
        val pb = playback()
        pb.start()
        runUntil { pb.problem != null }
        assertEquals(ErrorKind.Busy, pb.problem!!.kind)
        assertNull("no waiting sentence for another 503", pb.waiting)
        settle()
        idleFor(30_000)
        settle()
        assertEquals("it does not ask again by itself", 1, opens.get())
    }

    /** The wait is the shared client's, kept between 1 s and 60 s: the same as the phone Player. */
    @Test
    fun theWaitIsTheSharedOne() {
        assertEquals(10_000, busyRetryMs(null))
        assertEquals(10_000, busyRetryMs(10))
        assertEquals(1_000, busyRetryMs(0))
        assertEquals(60_000, busyRetryMs(3_600))
        assertTrue(BUSY_RETRY_SEC == 10L)
    }
}
