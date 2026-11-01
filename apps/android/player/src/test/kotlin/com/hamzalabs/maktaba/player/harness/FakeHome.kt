package com.hamzalabs.maktaba.player.harness

import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.jwt
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.ui.ThemeStore
import com.hamzalabs.maktaba.player.shell.PlayerHome
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.rules.ExternalResource

/**
 * A home for the Player's UI tests (NFR-19): an OkHttp MockWebServer that answers the API
 * routes the screens read with fixed fixtures, and a signed-in [PlayerHome] on it. Nothing
 * leaves the machine; no real home, database or token is involved.
 *
 * A screen story adds its routes with [answer] in its own tests (or a fixture here):
 * ```
 * @get:Rule val home = FakeHome()
 * home.answer("/api/home") { json(HOME_FIXTURE) }
 * ```
 * Every request is kept: [requests] is a copy of them, taken under the lock the server's
 * thread appends with, to assert what a screen asked for.
 */
class FakeHome : ExternalResource() {
    private val server = MockWebServer()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val routes = linkedMapOf<String, (RecordedRequest) -> MockResponse>()

    private val recorded = mutableListOf<RecordedRequest>()

    /**
     * What the screens asked so far, in order: a copy, so a test can read it while the server's
     * thread records more (iterating the live list would race the dispatcher).
     */
    val requests: List<RecordedRequest> get() = synchronized(recorded) { recorded.toList() }

    /** The paths asked so far, in order (no query). */
    val paths: List<String> get() = requests.map { it.url.encodedPath }

    /** Who is signed in. */
    val user = User(id = "u1", username = "layla")

    lateinit var home: PlayerHome
        private set

    init {
        answer("/api/libraries") { json(LIBRARIES) }
        answer("/api/videos") { json(VIDEOS) }
        VIDEO_LIST.forEach { (id, title) -> answer("/api/videos/$id") { json(video(id, title)) } }
    }

    /** Answers [path] (exactly, without the query) with [respond]; replaces an earlier answer. */
    fun answer(path: String, respond: (RecordedRequest) -> MockResponse) {
        routes[path] = respond
    }

    override fun before() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(recorded) { recorded += request }
                val path = request.url.encodedPath
                return routes[path]?.invoke(request) ?: MockResponse.Builder().code(404).build()
            }
        }
        server.start()
        // a token good for a day: the client never asks to refresh it
        val tokens = MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 86_400), "refresh")
        val client = ApiClient(server.url("/"), tokens)
        val session = AppSession(MapStore(), scope, { MemoryTokens() })
        home = PlayerHome(session, user, client, ThemeStore(MapStore()))
    }

    override fun after() {
        scope.cancel()
        server.close()
    }

    companion object {
        /** The fixtures' videos: id and title. */
        val VIDEO_LIST = listOf(
            "v1" to "The Message",
            "v2" to "Omar, Episode 1",
            "v3" to "Lion of the Desert",
            "v4" to "The Night Journey",
            "v5" to "Andalusia, a History",
        )

        val LIBRARIES = """{"items":[{"id":"l1","name":"Films"},{"id":"l2","name":"Series"}]}"""

        fun video(id: String, title: String) =
            """{"id":"$id","library_id":"l1","filename":"$id.mkv","title":"$title","description":"A film about $title.","duration_sec":5400}"""

        val VIDEOS = """{"items":[${VIDEO_LIST.joinToString(",") { (id, t) -> video(id, t) }}],"next":null}"""

        fun json(body: String, code: Int = 200): MockResponse =
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()
    }
}
