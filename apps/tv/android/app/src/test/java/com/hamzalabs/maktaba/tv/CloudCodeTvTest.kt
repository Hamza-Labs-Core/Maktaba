package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performSemanticsAction
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.jwt
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.screens.CloudCodeContent
import com.hamzalabs.maktaba.tv.ui.screens.CloudCodePanel
import com.hamzalabs.maktaba.tv.ui.screens.CloudCodeUi
import com.hamzalabs.maktaba.tv.ui.screens.HomePickerScreen
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import kotlinx.coroutines.MainScope
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.util.Collections
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * The cloud code on Android TV and Fire TV (story 58.03's TV side, 58.05), on a 1080p TV
 * (960x540 dp) in the TV's one theme (dark), English and Arabic: the home picker opens on a code
 * from Maktaba Cloud, with its QR and where to approve it, the other ways beside it (the homes,
 * an address); an approval signs the TV in at the approving home, against mock servers.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class CloudCodeTvTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val seen: MutableList<Pair<String, RecordedRequest>> = Collections.synchronizedList(mutableListOf())
    private fun seenNow() = synchronized(seen) { seen.toList() }
    private val servers = mutableListOf<MockWebServer>()
    @Volatile private var approved = false

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    private fun server(name: String, route: (RecordedRequest) -> MockResponse) = MockWebServer().apply {
        dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                seen += name to request
                return route(request)
            }
        }
        start()
        servers += this
    }

    private lateinit var cloud: MockWebServer
    private lateinit var home: MockWebServer
    private val homeUrl get() = home.url("/").toString().trimEnd('/')

    @Before
    fun start() {
        home = server("home") { r ->
            when (r.url.encodedPath) {
                // the home lists its relay among its own addresses: the one the cloud gave
                "/api/discovery/home" -> json(200, """{"id":"instance-7","name":"Den","urls":["$homeUrl","$RELAY"],"api_level":1,"api_level_oldest":1}""")
                "/api/pairing/device/token" -> json(200, """{"access_token":"$FRESH","refresh_token":"r1"}""")
                "/api/auth/me" -> if (r.headers["Authorization"] == "Bearer $FRESH") json(200, """{"id":"u1","username":"sara","home_name":"Den"}""") else json(401, "{}")
                else -> json(404, "{}")
            }
        }
        cloud = server("cloud") { r ->
            when (r.url.encodedPath) {
                "/v1/tv/codes" -> json(201, """{"code":"ABCD-2345","poll_secret":"$POLL_SECRET","expires_in":600,"interval":1,"verify_url":"https://maktaba.tv/tv?c=ABCD2345"}""")
                "/v1/tv/codes/poll" -> if (approved) {
                    json(200, """{"status":"approved","home_id":"$CLOUD_ID","home_name":"Den","grant":"$GRANT","connections":[
                        {"url":"$homeUrl","kind":"local"},{"url":"$RELAY","kind":"relay"}]}""")
                } else {
                    json(202, """{"status":"pending"}""")
                }
                else -> json(404, "{}")
            }
        }
    }

    @After
    fun stop() = servers.forEach { it.close() }

    private fun node(tag: String): SemanticsNode =
        compose.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().first()

    private fun shown(tag: String) = compose.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    private fun text(tag: String): String =
        node(tag).config.getOrNull(SemanticsProperties.Text).orEmpty().joinToString("") { it.text }

    /** Whether the text's layout was cut (its lines or width overflowed what was drawn). */
    private fun SemanticsNode.layoutCut(): Boolean {
        val results = mutableListOf<androidx.compose.ui.text.TextLayoutResult>()
        config.getOrNull(androidx.compose.ui.semantics.SemanticsActions.GetTextLayoutResult)?.action?.invoke(results)
        return results.any { it.hasVisualOverflow }
    }

    private fun string(id: Int, vararg args: Any) = compose.activity.getString(id, *args)

    private fun until(what: String, check: () -> Boolean) {
        runCatching { compose.waitUntil(15_000) { compose.waitForIdle(); check() } }
        assertTrue("$what; asked ${seenNow().map { it.first + " " + it.second.url.encodedPath }}", check())
    }

    /** Every listed part whole inside the 1080p screen. */
    private fun assertFits(tags: List<String>) {
        val root = compose.onRoot().fetchSemanticsNode().size
        assertEquals("a 1080p TV", 1920 to 1080, root.width to root.height)
        for (tag in tags) {
            val b = node(tag).boundsInRoot
            assertTrue("$tag inside the screen: $b", b.left >= 0f && b.top >= 0f && b.right <= root.width && b.bottom <= root.height && b.width > 0f)
        }
    }

    // ---------- the home picker, as the TV opens on it ----------

    private fun picker(name: String) {
        val app = ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        app.session.cloudApi = cloud.url("/").toString()
        compose.setContent { MaktabaTvTheme { HomePickerScreen(app) } }
        until("the cloud's code on the picker") { shown("cloud-code") }
        assertEquals("ABCD-2345", text("cloud-code"))
        assertTrue("it names the phones' button", text("cloud-where").contains(string(CoreR.string.approve_tv)))
        assertEquals(string(R.string.signin_code_waiting), text("cloud-status"))
        assertTrue(text("cloud-computer").contains("maktaba.tv/tv"))
        assertFalse("the code drawn whole, not cut", node("cloud-code").layoutCut())
        assertFits(listOf("cloud-code-panel", "cloud-code", "cloud-qr", "cloud-expires", "cloud-status", "cloud-computer", "picker-other-ways", "picker-address"))
        // asked with this build's platform; the poll secret in the body only
        val create = seenNow().first { it.second.url.encodedPath == "/v1/tv/codes" }.second
        assertEquals("""{"platform":"${BuildConfig.TV_PLATFORM}","app_version":"${BuildConfig.VERSION_NAME}"}""", create.body!!.utf8())
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
        assertTrue(seenNow().none { POLL_SECRET in it.second.url.toString() })
    }

    @Test @Config(qualifiers = TV)
    fun thePickerOpensOnTheCloudCodeEnglish() = picker("picker_cloud_code_${BuildConfig.TV_PLATFORM}_dark_en")

    @Test @Config(qualifiers = "ar-$TV")
    fun thePickerOpensOnTheCloudCodeArabic() = picker("picker_cloud_code_${BuildConfig.TV_PLATFORM}_dark_ar")

    // ---------- an approval signs the TV in ----------

    private class MapStore : KeyValueStore {
        val map = mutableMapOf<String, String>()
        override fun get(key: String) = map[key]
        override fun put(key: String, value: String?) {
            if (value == null) map.remove(key) else map[key] = value
        }
    }

    private class MemoryTokens : TokenStore {
        var tokens: AuthTokens? = null
        override val access get() = tokens?.accessToken
        override val refresh get() = tokens?.refreshToken
        override fun save(tokens: AuthTokens) { this.tokens = tokens }
        override fun clear() { tokens = null }
    }

    @Test @Config(qualifiers = TV)
    fun anApprovalSignsTheTvInAtTheApprovingHome() {
        val tokens = mutableMapOf<String, MemoryTokens>()
        val session = AppSession(MapStore(), MainScope(), { tokens.getOrPut(it) { MemoryTokens() } })
        session.cloudApi = cloud.url("/").toString()
        compose.setContent { MaktabaTvTheme { LibraryBackdrop { CloudCodePanel(session, Modifier.fillMaxSize()) } } }
        until("the code") { shown("cloud-code") }
        approved = true
        until("signed in at the home") { session.phase.value is AppSession.Phase.SignedIn }
        assertEquals(homeUrl, session.current.value?.url)
        assertEquals(FRESH, tokens.getValue(homeUrl).access)
        val redeem = seenNow().single { it.second.url.encodedPath == "/api/pairing/device/token" }.second
        val body = redeem.body!!.utf8()
        assertTrue(body, body.startsWith("""{"device_code":"$GRANT","device_label":""""))
        assertTrue("the grant is in no URL", seenNow().none { GRANT in it.second.url.toString() })
    }

    // ---------- the panel's other states ----------

    private val current = mutableStateOf<CloudCodeUi>(CloudCodeUi.Asking)
    private var onTryAgain: () -> Unit = {}
    private var content = false

    private fun state(ui: CloudCodeUi, tryAgain: () -> Unit = {}) {
        current.value = ui
        onTryAgain = tryAgain
        if (!content) {
            content = true
            compose.setContent { MaktabaTvTheme { LibraryBackdrop { CloudCodeContent(current.value, { onTryAgain() }, Modifier.fillMaxSize()) } } }
        }
        compose.waitForIdle()
    }

    @Test @Config(qualifiers = TV)
    fun eachStateSaysWhatTheTvIsDoing() {
        state(CloudCodeUi.Asking)
        assertEquals(string(R.string.cloud_code_asking), text("cloud-status"))
        state(CloudCodeUi.Down)
        assertEquals(string(R.string.cloud_code_down), text("cloud-status"))
        state(CloudCodeUi.Opening("Den"))
        assertEquals(string(R.string.cloud_code_opening, "Den"), text("cloud-status"))
        var again = 0
        state(CloudCodeUi.Unreachable("Den")) { again++ }
        assertEquals(string(R.string.cloud_code_unreachable, "Den"), text("cloud-status"))
        compose.onNode(hasTestTag("cloud-try-again")).performSemanticsAction(SemanticsActions.OnClick)
        compose.waitForIdle()
        assertEquals(1, again)
    }

    @Test @Config(qualifiers = "ar-$TV")
    fun theCodeReadsLeftToRightInArabic() {
        state(CloudCodeUi.Showing("ABCD-2345", "https://maktaba.tv/tv?c=ABCD2345", 598))
        assertEquals("ABCD-2345", text("cloud-code"))
        assertEquals(string(R.string.signin_code_expires, "9:58"), text("cloud-expires"))
        val code = node("cloud-code").boundsInRoot
        val qr = node("cloud-qr").boundsInRoot
        assertTrue("in Arabic the QR is on the left of the code: $qr $code", qr.right <= code.left)
    }

    private companion object {
        const val TV = "w960dp-h540dp-land-television-xhdpi"
        const val RELAY = "https://den.relay.maktaba.tv"
        const val POLL_SECRET = "ps-0123456789abcdefghij"
        const val GRANT = "Gr4nt_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
        const val CLOUD_ID = "7d0c1e9a-5b2f-4c1e-9a7d-3e2f1b0c4d5a"
        val FRESH = jwt(System.currentTimeMillis() / 1000 + 900)
        // half scale: each reference stays well under a megabyte
        val options = RoborazziOptions(
            recordOptions = RoborazziOptions.RecordOptions(resizeScale = 0.5),
            compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f),
        )
    }
}
