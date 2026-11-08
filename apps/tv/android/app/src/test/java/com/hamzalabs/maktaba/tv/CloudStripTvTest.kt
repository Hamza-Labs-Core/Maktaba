package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.api.CloudStatusRules
import com.hamzalabs.maktaba.core.api.CloudSummary
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.core.ui.HomeStatusHost
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.net.InetAddress
import java.util.concurrent.atomic.AtomicInteger

/**
 * Epic 50 story 50.11 on Android TV: the TV's screens sit in the shared status strip
 * ([HomeStatusHost]), which shows Maktaba Cloud's line for a home reached through the relay,
 * and reads the feed again at once when a request fails as the relay fails (not 5 minutes
 * later). The feed is the test's; the home (a MockWebServer under the relay's name) says all
 * is well on its own /status.json.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w960dp-h540dp-xhdpi")
class CloudStripTvTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private val server = MockWebServer()
    private val home by lazy { "http://h1.relay.maktaba.tv:${server.port}/".toHttpUrl() }
    private val ok = CloudSummary(components = mapOf("relay" to "operational", "api" to "operational"))
    private val down = CloudSummary(components = mapOf("relay" to "outage", "api" to "operational"))

    private object NoTokens : TokenStore {
        override val access: String? = null
        override val refresh: String? = null
        override fun save(tokens: AuthTokens) {}
        override fun clear() {}
    }

    private lateinit var client: ApiClient

    @Before
    fun setUp() {
        CloudStatusRules.resetRecheck()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest) = MockResponse.Builder()
                .setHeader("Content-Type", "application/json")
                .body("""{"v":1,"mode":"normal","overall":"ok","components":{}}""")
                .build()
        }
        server.start()
        // the relay's name reaches the MockWebServer
        val http = OkHttpClient.Builder().dns { listOf(InetAddress.getLoopbackAddress()) }.build()
        client = ApiClient(home, NoTokens, http = http)
    }

    @After
    fun tearDown() {
        server.close()
        CloudStatusRules.resetRecheck()
    }

    @Test
    fun theCloudsLineShowsOnTheTvAndARelayFailureReadsTheFeedAtOnce() {
        val reads = AtomicInteger()
        compose.setContent {
            MaktabaTvTheme {
                HomeStatusHost(client, admin = false, fetchCloud = { if (reads.incrementAndGet() == 1) ok else down }) {
                    Box(Modifier.fillMaxSize())
                }
            }
        }
        compose.waitUntil(5_000) { reads.get() >= 1 }
        compose.waitForIdle()
        assertEquals("all well: no strip", 0, compose.onAllNodesWithTag("status.strip").fetchSemanticsNodes().size)

        // a request through the relay gets a 502: the strip reads the feed now
        assertTrue(CloudStatusRules.noteRequestFailed(home, 502))
        compose.waitUntil(5_000) { compose.onAllNodesWithTag("strip.relay_outage").fetchSemanticsNodes().isNotEmpty() }
        assertEquals(2, reads.get())
        compose.onNodeWithTag("status.strip").assertExists()
    }
}
