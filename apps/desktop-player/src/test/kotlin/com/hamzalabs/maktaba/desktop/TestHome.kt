package com.hamzalabs.maktaba.desktop

import androidx.compose.runtime.Composable
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.jwt
import com.hamzalabs.maktaba.desktop.shell.AppPrefs
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.HomeActions
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * The net under the tests' waits for a state (an answer read, a screen shown): they return as
 * soon as the state holds, and this is far longer than a slow runner needs, so only a real
 * stall meets it.
 */
const val STALL_MS = 60_000L

/**
 * A home for the desktop Player's tests: a MockWebServer answering `"<METHOD> <path>"` routes
 * (404 otherwise), what it was asked, and a [DesktopHome] on it. Scratch data only.
 */
class TestHome : AutoCloseable {
    val server = MockWebServer()
    private val routes = ConcurrentHashMap<String, (RecordedRequest) -> MockResponse>()
    val seen: MutableList<RecordedRequest> = Collections.synchronizedList(mutableListOf())

    init {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += request
                val key = "${request.method} ${request.url.encodedPath}"
                return routes[key]?.invoke(request) ?: json("""{"title":"not found","status":404}""", 404)
            }
        }
        server.start()
    }

    /** The client's calls, on a dispatcher of their own so they can be counted ([idle]). */
    private val calls = okhttp3.Dispatcher()

    val client: ApiClient by lazy {
        ApiClient(server.url("/"), MemoryTokens(jwt(4_000_000_000L), "r"), ApiClient.defaultHttp.newBuilder().dispatcher(calls).build())
    }

    /**
     * Whether no call to this home is queued or running. OkHttp counts a call as running until
     * its callback returns, and the client resumes its caller inside that callback, so once this
     * holds the screen's next step is already queued for the test's thread. A test that moves
     * the clock for a screen's own wait (the 15 seconds between two reads of a request) waits for
     * this and then `waitForIdle()` first: the wait has then begun, and the clock moves it. A
     * clock moved while the answer before it is still on its way moves nothing.
     */
    val idle: Boolean get() = running == 0

    /** How many calls to this home are queued or running (one a test holds on the home counts until it is let go). */
    val running: Int get() = calls.runningCallsCount() + calls.queuedCallsCount()

    /** The scope of [home]'s changes that outlive their page ([DesktopHome.change]). */
    val background = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /**
     * Whether nothing is on its way: no call to this home is queued or running, and no change a
     * page started ([DesktopHome.change]) is still alive. A change is alive until it has taken
     * the home's answer, so once this holds the answer has been applied.
     */
    val settled: Boolean get() = idle && background.coroutineContext[Job]!!.children.none()

    /** Answers [route] ("GET /api/home") with [body]. */
    fun answer(route: String, body: String, code: Int = 200) {
        routes[route] = { json(body, code) }
    }

    fun answer(route: String, respond: (RecordedRequest) -> MockResponse) {
        routes[route] = respond
    }

    /** Answers [route] with [body] only once [gate] opens (a read held open; a minute's cap). */
    fun held(route: String, body: () -> String, gate: CountDownLatch) {
        routes[route] = {
            gate.await(60, TimeUnit.SECONDS)
            json(body())
        }
    }

    fun asked(route: String): List<RecordedRequest> = synchronized(seen) { seen.filter { "${it.method} ${it.url.encodedPath}" == route } }

    val features = MutableStateFlow(Capabilities.AllOn)
    val played = mutableListOf<PlayRequest>()
    val opened = mutableListOf<String>()
    val prefs = AppPrefs(MapStore())

    fun home(admin: Boolean = false): DesktopHome = DesktopHome(
        User("u1", "amina", isAdmin = admin), client, "Kitchen", prefs,
        background,
        capabilities = features,
        posters = Posters(null),
        actions = HomeActions(openUrl = { opened += it }),
        onPlay = { played += it },
    )

    override fun close() = server.close()

    companion object {
        fun json(body: String, code: Int = 200): MockResponse =
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

        /** Features with [off] off; [admin] answers with the admin block. */
        fun caps(vararg off: String, admin: Boolean = false) = Capabilities(off = off.toSet(), admin = admin)
    }
}

/**
 * Lets everything on its way to and from [fake] land before a test says nothing more happened:
 * returns once [TestHome.settled] holds before and after the screen has run what was left for
 * it, and the home was asked nothing new meanwhile. It waits for that state, never for a
 * length of time: [STALL_MS] is the net under a stall.
 */
@OptIn(androidx.compose.ui.test.ExperimentalTestApi::class)
fun androidx.compose.ui.test.ComposeUiTest.settle(fake: TestHome) = waitUntil(timeoutMillis = STALL_MS) {
    val asked = fake.seen.size
    val quiet = fake.settled
    waitForIdle()
    quiet && fake.settled && asked == fake.seen.size
}

/** [content] in the Player's theme, in [lang], around [home]. */
@Composable
fun InPlayer(home: DesktopHome, lang: Lang = Lang.En, content: @Composable () -> Unit) {
    PlayerTheme(Texts(lang), big = false) {
        androidx.compose.runtime.CompositionLocalProvider(LocalDesktopHome provides home) { content() }
    }
}
