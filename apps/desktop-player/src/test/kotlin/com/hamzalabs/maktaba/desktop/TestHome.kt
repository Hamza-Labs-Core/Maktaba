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

    val client: ApiClient by lazy { ApiClient(server.url("/"), MemoryTokens(jwt(4_000_000_000L), "r")) }

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
        CoroutineScope(SupervisorJob() + Dispatchers.IO),
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

/** [content] in the Player's theme, in [lang], around [home]. */
@Composable
fun InPlayer(home: DesktopHome, lang: Lang = Lang.En, content: @Composable () -> Unit) {
    PlayerTheme(Texts(lang), big = false) {
        androidx.compose.runtime.CompositionLocalProvider(LocalDesktopHome provides home) { content() }
    }
}
