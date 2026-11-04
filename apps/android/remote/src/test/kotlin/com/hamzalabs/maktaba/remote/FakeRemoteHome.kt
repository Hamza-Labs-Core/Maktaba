package com.hamzalabs.maktaba.remote

import androidx.compose.material3.Surface
import androidx.compose.material3.adaptive.Posture
import androidx.compose.material3.adaptive.WindowAdaptiveInfo
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.test.IdlingResource
import androidx.compose.ui.unit.Density
import androidx.window.core.layout.WindowSizeClass
import androidx.window.core.layout.computeWindowSizeClass
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.Capabilities
import kotlinx.coroutines.flow.MutableStateFlow
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.jwt
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.PendingSignInsHost
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.core.ui.ThemeStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.ConnectionPool
import okhttp3.Dispatcher as CallDispatcher
import org.junit.rules.ExternalResource

/**
 * A home for the Remote's screen tests: a MockWebServer that answers the admin routes with
 * fixtures, and a signed-in [Remote] on it. Nothing leaves the machine.
 */
class FakeRemoteHome : ExternalResource() {
    private val server = MockWebServer()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val routes = linkedMapOf<String, (RecordedRequest) -> MockResponse>()
    private val recorded = mutableListOf<RecordedRequest>()
    private val calls = CallDispatcher()

    val user = User(id = "u1", username = "layla", isAdmin = true)
    lateinit var remote: Remote
        private set
    lateinit var session: AppSession
        private set
    val themes = ThemeStore(MapStore())

    /** What the home has on (story 51.11): every feature on until a test says otherwise. */
    val features = MutableStateFlow(Capabilities.AllOn)

    /** The paths asked so far, in order. */
    val paths: List<String> get() = synchronized(recorded) { recorded.map { it.url.encodedPath } }

    /** Idle when no call is queued or running (see the Player's FakeHome). */
    val idling = object : IdlingResource {
        override val isIdleNow: Boolean get() = calls.runningCallsCount() == 0 && calls.queuedCallsCount() == 0
    }

    init {
        answer("/api/issues") { json(ISSUES) }
    }

    fun answer(path: String, respond: (RecordedRequest) -> MockResponse) {
        routes[path] = respond
    }

    override fun before() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(recorded) { recorded += request }
                return routes[request.url.encodedPath]?.invoke(request) ?: MockResponse.Builder().code(404).build()
            }
        }
        server.start()
        val tokens = MemoryTokens(jwt(System.currentTimeMillis() / 1000 + 86_400), "refresh")
        val client = ApiClient(server.url("/"), tokens, ApiClient.defaultHttp.newBuilder().dispatcher(calls).connectionPool(ConnectionPool()).build())
        session = AppSession(MapStore(), scope, { MemoryTokens() })
        remote = Remote(client, session, features)
    }

    override fun after() {
        scope.cancel()
        server.close()
    }

    companion object {
        /**
         * Shaped like a home's Needs attention on build 0.3.62 (2026-11-03): what the home sent, with the
         * generated causes ("kind:normalised message") and an ffmpeg tail with a repr's `\n`.
         */
        val ISSUES = """{"groups":[
            {"stage":"fingerprint","cause":"nas_partial_read","label":"NAS returned a partial read","count":256,"action":"retry","sample_message":"read failed: head, tail"},
            {"stage":"probe","cause":"ffmpeg_decode","label":"Could not decode the media file","count":3,"action":"retry","sample_message":"ffprobe exit=1: [mov,mp4,m4a,3gp,3g2,mj2 @ 000001facc7850c0] moov atom not found"},
            {"stage":"plex_groups","cause":"plex_unreachable","label":"Plex is unreachable","count":2,"action":"open_settings","sample_message":"relay: no answer from the home"},
            {"stage":"chapter_thumbs","cause":"ffmpeg_rc","label":"ffmpeg failed on the file","count":1,"action":"retry","sample_message":"ing thread with return code -22 (Invalid argument)\\n[out#0/image2 @ 0x6000]"},
            {"stage":"check_clips","cause":"model_unavailable:<n> of <n> clips got no answer from the model","label":"model_unavailable:<n> of <n> clips got no answer from the model","count":1,"action":"retry","sample_message":"4 of 4 clips got no answer from the model"},
            {"stage":"subtitle_gen","cause":"nas_timeout:<path> محاضرات/سلسلة التاريخ..الجزء الثاني<n> | دروس عامة <n> | مكتبة البيت.mp<n>>: no answer","label":"nas_timeout:<path> محاضرات/سلسلة التاريخ..الجزء الثاني<n> | دروس عامة <n> | مكتبة البيت.mp<n>>: no answer","count":1,"action":"retry","sample_message":"/Volumes/Media/محاضرات/سلسلة التاريخ..الجزء الثاني 2 | دروس عامة 3 | مكتبة البيت.mp4: no answer"}
        ]}"""

        fun json(body: String, code: Int = 200): MockResponse =
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()
    }
}

/** The windows the Remote is laid out in, in dp, with the Robolectric qualifiers that match. */
enum class RemoteWindow(val widthDp: Int, val heightDp: Int, val qualifiers: String) {
    /** Galaxy Z Fold 5/6 cover: 904 × 2316 px at 420 dpi. */
    FoldCover(344, 882, "w344dp-h882dp-420dpi"),
    /**
     * Motorola Razr Fold (2026) cover: 1080 × 2520 px, 6.6" (~415 ppi). Its density is not
     * published: 440 dpi (2.75) is an estimate from a device screenshot (the bar's 80 dp and
     * the list's 20 sp lines measure ~2.7 px per dp).
     */
    RazrFoldCover(393, 916, "w393dp-h916dp-440dpi"),
    /** Pixel 9 Pro Fold outer: 1080 × 2424 px at 420 dpi. */
    PixelFoldCover(411, 923, "w411dp-h923dp-420dpi"),
    /** Pixel 9 Pro Fold inner, open flat. */
    PixelFoldOpen(673, 841, "w673dp-h841dp-xhdpi"),
    ;

    val info: WindowAdaptiveInfo get() = adaptiveInfo(widthDp, heightDp)
}

fun adaptiveInfo(widthDp: Int, heightDp: Int) = WindowAdaptiveInfo(
    WindowSizeClass.BREAKPOINTS_V2.computeWindowSizeClass(widthDp.toFloat(), heightDp.toFloat()),
    Posture(),
)

/** The Remote's frame as MainActivity shows it, dark, at [fontScale], for [home]. */
@Composable
fun RemoteForTest(home: FakeRemoteHome, info: WindowAdaptiveInfo, fontScale: Float = 1f, pendingEveryMs: Long = 1_000) {
    val density = LocalDensity.current
    CompositionLocalProvider(LocalDensity provides Density(density.density, fontScale)) {
        MaktabaTheme(ThemeMode.Dark) {
            Surface {
                // as RemoteHome: the waiting-devices banner above the frame
                PendingSignInsHost(home.session, home.user, home.remote.client, intervalMs = pendingEveryMs) {
                    RemoteFrame(home.remote, account = { RemoteAccount(home.session, home.user, home.themes, home.remote.client) }, adaptiveInfo = info)
                }
            }
        }
    }
}
