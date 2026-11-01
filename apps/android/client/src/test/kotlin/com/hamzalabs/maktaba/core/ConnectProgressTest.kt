package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.DeviceSignIn
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.ConnectLink
import com.hamzalabs.maktaba.core.session.ConnectProgress
import com.hamzalabs.maktaba.core.session.ConnectProgress.AwaitingApproval
import com.hamzalabs.maktaba.core.session.ConnectProgress.Failed
import com.hamzalabs.maktaba.core.session.ConnectProgress.Finding
import com.hamzalabs.maktaba.core.session.ConnectProgress.Found
import com.hamzalabs.maktaba.core.session.ConnectProgress.HomeBusy
import com.hamzalabs.maktaba.core.session.ConnectProgress.Reconnecting
import com.hamzalabs.maktaba.core.session.ConnectProgress.SigningIn
import com.hamzalabs.maktaba.core.session.ConnectProgress.Trying
import com.hamzalabs.maktaba.core.session.ConnectStage
import com.hamzalabs.maktaba.core.session.FailReason
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.HomeProbe
import com.hamzalabs.maktaba.core.session.NotAHome
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalForInheritanceCoroutinesApi
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.IOException
import java.time.Instant
import java.util.Collections

/**
 * The connect-progress state (story 48.37): each step the connect code publishes, on a fake
 * clock and a fake transport. Nothing sleeps: a wait is a latch the test opens.
 */
class ConnectProgressTest {
    /** The fake clock: moves only when a test moves it. */
    @Volatile private var clock = Instant.parse("2026-11-01T10:00:00Z").toEpochMilli()

    /** The fake wait: each one asked is reported in [asked] and lasts until the test calls [wake]. */
    private val asked = Channel<Long>(Channel.UNLIMITED)
    private val gates = Channel<Unit>(Channel.UNLIMITED)
    private suspend fun latchedSleep(ms: Long) {
        asked.send(ms)
        gates.receive()
    }
    private fun wake() {
        gates.trySend(Unit)
    }

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    @After
    fun stop() = scope.cancel()

    /** A [MutableStateFlow] that keeps every value set, in order (a StateFlow's collector may miss some). */
    @OptIn(ExperimentalForInheritanceCoroutinesApi::class)
    private class Recording(private val inner: MutableStateFlow<ConnectProgress?> = MutableStateFlow(null)) :
        MutableStateFlow<ConnectProgress?> by inner {
        val seen: MutableList<ConnectProgress?> = Collections.synchronizedList(mutableListOf())
        override var value: ConnectProgress?
            get() = inner.value
            set(v) {
                seen += v
                inner.value = v
            }
        override fun compareAndSet(expect: ConnectProgress?, update: ConnectProgress?): Boolean =
            inner.compareAndSet(expect, update).also { if (it) seen += update }
        fun steps(): List<ConnectProgress?> = synchronized(seen) { seen.toList() }
    }

    /** An answer of the fake transport: its status, body and Retry-After. */
    private data class Answer(val code: Int, val body: String = "{}", val retryAfter: String? = null)

    /** The fake transport: [route] answers each request; throwing [IOException] is "no answer". */
    private fun transport(route: (Request) -> Answer): OkHttpClient = OkHttpClient.Builder().addInterceptor { chain ->
        val r = chain.request()
        val a = route(r)
        Response.Builder().request(r).protocol(Protocol.HTTP_1_1).code(a.code).message("fake")
            .body(a.body.toResponseBody("application/json".toMediaType()))
            .apply { a.retryAfter?.let { header("Retry-After", it) } }
            .build()
    }.build()

    private fun session(
        progress: Recording,
        tokens: MemoryTokens = MemoryTokens(),
        sleep: suspend (Long) -> Unit = ::latchedSleep,
        route: (Request) -> Answer = { Answer(404) },
    ): AppSession {
        val http = transport(route)
        return AppSession(MapStore(), scope, { tokens }, { url, t -> ApiClient(url, t, http) }, { clock }, sleep, progress)
    }

    /** A probe over a fake `get`: what each address answers. */
    private fun probe(answers: Map<String, () -> Pair<Int, String>>) = HomeProbe { url: HttpUrl ->
        val key = url.host
        (answers[key] ?: throw ApiException.Transport("no route to $key"))()
    }

    private fun home(id: String, name: String) = 200 to """{"id":"$id","name":"$name","version":"1.0"}"""

    private suspend fun AppSession.awaitProgress(p: (ConnectProgress?) -> Boolean): ConnectProgress? =
        withTimeout(5_000) { progress.first(p) }

    // ---------- finding the home ----------

    @Test
    fun connectingSaysFindingEachAddressThenFound() = runBlocking {
        val rec = Recording()
        val s = session(rec)
        val link = ConnectLink("aaaa1111", null, listOf("http://10.0.0.1:8088", "http://10.0.0.2:8088", "http://10.0.0.3:8088"))
        val p = probe(
            mapOf(
                // the first does not answer, the second is another home, the third is it
                "10.0.0.2" to { home("bbbb2222", "Neighbours") },
                "10.0.0.3" to { home("aaaa1111", "Darwish home") },
            ),
        )
        s.connect(link, p)
        assertEquals(
            listOf(
                Finding(1),
                Trying("10.0.0.1:8088", 1, 3),
                Trying("10.0.0.2:8088", 2, 3),
                Trying("10.0.0.3:8088", 3, 3),
                Found("Darwish home"),
            ),
            rec.steps(),
        )
        assertEquals("found stays said over the sign-in", Found("Darwish home"), s.progress.value)
        assertTrue(s.phase.value is AppSession.Phase.SigningIn)
    }

    @Test
    fun noAnswerIsSaidWithTheAddressesTried() = runBlocking {
        val rec = Recording()
        val s = session(rec)
        val link = ConnectLink(null, null, listOf("http://10.0.0.1:8088", "https://home.example.com"))
        try {
            s.connect(link, probe(emptyMap()))
            fail("nothing answered")
        } catch (e: NotAHome) {
            assertEquals(FailReason.NoAnswer, e.reason)
        }
        assertEquals(Failed(FailReason.NoAnswer, listOf("10.0.0.1:8088", "home.example.com"), ConnectStage.Connect, 1), s.progress.value)
    }

    @Test
    fun somethingThatIsNotAHomeIsSaid() = runBlocking {
        val rec = Recording()
        val s = session(rec)
        val link = ConnectLink(null, null, listOf("http://10.0.0.1:8088", "http://10.0.0.9"))
        // a router's page: it answers, but not as a home
        val p = probe(mapOf("10.0.0.9" to { 200 to "<html>router</html>" }))
        runCatching { s.connect(link, p) }
        assertEquals(Failed(FailReason.NotAHome, listOf("10.0.0.1:8088", "10.0.0.9"), ConnectStage.Connect, 1), s.progress.value)
    }

    @Test
    fun aDifferentHomeIsSaid() = runBlocking {
        val rec = Recording()
        val s = session(rec)
        val link = ConnectLink("aaaa1111", "Darwish home", listOf("http://192.168.1.20:8088"))
        runCatching { s.connect(link, probe(mapOf("192.168.1.20" to { home("bbbb2222", "Neighbours") }))) }
        assertEquals(Failed(FailReason.OtherHome, listOf("192.168.1.20:8088"), ConnectStage.Connect, 1), s.progress.value)
        assertTrue("never moved to the other home", s.current.value == null)
    }

    @Test
    fun retryCountsTheAttemptUpAndStartsOverAfterSuccess() = runBlocking {
        val rec = Recording()
        val s = session(rec)
        var up = false
        val link = ConnectLink(null, null, listOf("http://10.0.0.1:8088"))
        val p = HomeProbe { if (up) home("aaaa1111", "Darwish home") else throw ApiException.Transport("down") }
        runCatching { s.connect(link, p) }
        assertEquals(1, s.progress.value?.attempt)

        s.retry()
        val second = s.awaitProgress { it is Failed && it.attempt == 2 } as Failed
        assertEquals(FailReason.NoAnswer, second.reason)
        assertTrue("the steps again, as attempt 2", rec.steps().containsAll(listOf(Finding(2), Trying("10.0.0.1:8088", 1, 1, 2))))

        up = true
        s.retry()
        assertEquals(Found("Darwish home", 3), s.awaitProgress { it is Found })

        // something new after a success starts at attempt 1
        s.switchHome()
        s.connect(link, p)
        assertEquals(Found("Darwish home", 1), s.progress.value)
    }

    // ---------- on launch ----------

    @Test
    fun aBusyHomeOnLaunchIsCountedDownThenTriedAgain() = runBlocking {
        val rec = Recording()
        var busy = true
        val s = session(rec, MemoryTokens("a", "r")) { r ->
            when {
                r.url.encodedPath != "/api/auth/me" -> Answer(404)
                busy -> Answer(503, """{"type":"about:blank","title":"starting","status":503}""", retryAfter = "7")
                else -> Answer(200, """{"id":"u1","username":"amina","home_name":"Darwish home"}""")
            }
        }
        s.use(Home("http://192.168.1.20:8088", "Darwish home"))
        assertEquals(Reconnecting("Darwish home", 1), rec.steps().first())

        // 503 with Retry-After: 7 → the countdown to the next try, on the fake clock
        val wait = withTimeout(5_000) { asked.receive() }
        assertEquals(7_000L, wait)
        assertEquals(HomeBusy("Darwish home", clock + 7_000, 1), s.progress.value)

        busy = false
        clock += wait
        wake()
        withTimeout(5_000) { s.phase.first { it is AppSession.Phase.SignedIn } }
        assertEquals(null, s.awaitProgress { it == null })
        assertTrue("the next try says attempt 2", Reconnecting("Darwish home", 2) in rec.steps())
    }

    @Test
    fun aHomeThatStaysBusyFailsAsBusy() = runBlocking {
        val rec = Recording()
        // no Retry-After: the app's own wait; each one opens at once
        val s = session(rec, MemoryTokens("a", "r"), sleep = { clock += it }) { Answer(503) }
        s.use(Home("http://192.168.1.20:8088", "Darwish home"))
        val failed = s.awaitProgress { it is Failed } as Failed
        assertEquals(FailReason.Busy, failed.reason)
        assertEquals(ConnectStage.Restore, failed.stage)
        assertEquals("the first try and five waits", 6, failed.attempt)
        assertEquals(5, rec.steps().count { it is HomeBusy })
    }

    @Test
    fun noAnswerOnLaunchTriesTheHomesOtherAddressesThenFails() = runBlocking {
        val rec = Recording()
        val s = session(rec, MemoryTokens("a", "r")) { throw IOException("unreachable") }
        s.probe = HomeProbe { throw ApiException.Transport("unreachable") }
        s.use(Home("http://192.168.1.20:8088", "Darwish home", id = "aaaa1111", addresses = listOf("https://relay.example.com/h")))
        val failed = s.awaitProgress { it is Failed } as Failed
        assertEquals(Failed(FailReason.NoAnswer, listOf("192.168.1.20:8088", "relay.example.com/h"), ConnectStage.Restore, 1), failed)
        assertTrue(Trying("relay.example.com/h", 1, 1) in rec.steps())

        // Retry: the same steps, as attempt 2
        s.retryRestore()
        assertEquals(2, (s.awaitProgress { it is Failed && it.attempt == 2 } as Failed).attempt)
        assertTrue(Reconnecting("Darwish home", 2) in rec.steps())
    }

    // ---------- signing in ----------

    @Test
    fun aWrongPasswordIsSaidAndTheNextTryCountsUp() = runBlocking {
        val rec = Recording()
        var right = false
        val s = session(rec) { r ->
            when (r.url.encodedPath) {
                "/api/auth/login" -> if (right) Answer(200, """{"access_token":"a1","refresh_token":"r1"}""") else Answer(401)
                "/api/auth/me" -> Answer(200, """{"id":"u1","username":"amina"}""")
                else -> Answer(404)
            }
        }
        s.use(Home("http://192.168.1.20:8088", "Darwish home"))
        runCatching { s.signIn("amina", "nope") }
        assertEquals(
            listOf(SigningIn("Darwish home", ConnectStage.Password, 1), Failed(FailReason.WrongPassword, listOf("192.168.1.20:8088"), ConnectStage.Password, 1)),
            rec.steps().filterNotNull(),
        )
        right = true
        s.signIn("amina", "right")
        assertTrue(SigningIn("Darwish home", ConnectStage.Password, 2) in rec.steps())
        assertEquals(null, s.progress.value)
        assertTrue(s.phase.value is AppSession.Phase.SignedIn)
    }

    @Test
    fun aCodeWaitsForApprovalWithItsExpiry() = runBlocking {
        val rec = Recording()
        val expires = clock + 9 * 60_000
        var polls = 0
        val s = session(rec, sleep = { clock += it }) { r ->
            when (r.url.encodedPath) {
                "/api/pairing/device" -> Answer(201, codeJson(Instant.ofEpochMilli(expires).toString()))
                "/api/pairing/device/token" -> if (++polls < 3) Answer(202) else Answer(200, """{"access_token":"a1","refresh_token":"r1"}""")
                "/api/auth/me" -> Answer(200, """{"id":"u1","username":"amina"}""")
                else -> Answer(404)
            }
        }
        s.use(Home("http://192.168.1.20:8088", "Darwish home"))
        s.signInWithCode("Pixel", onCode = {}, renew = false)
        assertEquals(
            listOf(SigningIn("Darwish home", ConnectStage.Code, 1), AwaitingApproval("ABCD-1234", expires, 1), null),
            rec.steps(),
        )
        assertTrue(s.phase.value is AppSession.Phase.SignedIn)
    }

    @Test
    fun anApprovalThatRunsOutIsSaidAndRetryAsksForANewCode() = runBlocking {
        val rec = Recording()
        val expires = clock + 5_000
        val s = session(rec, sleep = { clock += it }) { r ->
            when (r.url.encodedPath) {
                "/api/pairing/device" -> Answer(201, codeJson(Instant.ofEpochMilli(expires).toString()))
                // nobody approves
                "/api/pairing/device/token" -> Answer(202)
                else -> Answer(404)
            }
        }
        s.use(Home("http://192.168.1.20:8088", "Darwish home"))
        try {
            s.signInWithCode("Pixel", onCode = {}, renew = false)
            fail("the code ran out")
        } catch (e: DeviceSignIn.Expired) {
            // expected
        }
        assertEquals(AwaitingApproval("ABCD-1234", expires, 1), rec.steps()[1])
        assertEquals(Failed(FailReason.ApprovalExpired, listOf("192.168.1.20:8088"), ConnectStage.Code, 1), s.progress.value)
        assertTrue("it ran out on the clock", clock >= expires)

        runCatching { s.signInWithCode("Pixel", onCode = {}, renew = false) }
        assertTrue(SigningIn("Darwish home", ConnectStage.Code, 2) in rec.steps())
        assertEquals(2, s.progress.value?.attempt)
    }

    private fun codeJson(expiresAt: String) =
        """{"code":"ABCD-1234","device_code":"scratch-secret","expires_at":"$expiresAt","interval":2}"""
}
