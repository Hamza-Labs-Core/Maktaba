package com.hamzalabs.maktaba.player.live

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * The live player (FR-23) against the fake home: it plays the mock's HLS (with the user's token),
 * steps channels, offers "Back to live" after a pause, and away from home tunes a remote session
 * that ends when the person leaves. The heartbeat's minute is [ChannelPlayerTest]'s, on a fake clock.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp-xxhdpi")
class ChannelScreenTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val engine = FakeLiveEngine()

    private fun exists(tag: String) = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()
    private fun waitForTag(tag: String) = compose.waitUntil(5_000) { exists(tag) }

    private fun openChannel(id: String, relay: Boolean = false) {
        LiveFixtures.install(home, relay = relay)
        compose.setLive(home, engine)
        compose.onNodeWithTag("section-LiveTv").performClick()
        waitForTag("channel-$id")
        compose.onNodeWithTag("channel-$id").performClick()
        waitForTag("channel-player")
    }

    /** The controls hide after 4 s: a tap shows them again. */
    private fun controls() {
        if (!exists("live-controls")) compose.onNodeWithTag("channel-player").performClick()
        waitForTag("live-controls")
    }

    @Test
    fun itPlaysTheMocksHlsWithTheUsersToken() {
        openChannel("c1")
        compose.waitUntil(5_000) { engine.fetched.isNotEmpty() }
        val (url, code) = engine.fetched.first()
        assertEquals("/stream/channel/c1/index.m3u8", url.encodedPath)
        assertEquals(200, code)
        val asked = home.requests.filter { it.url.encodedPath == "/stream/channel/c1/index.m3u8" }
        assertTrue("the encoder's wait and the player both asked", asked.size >= 2)
        assertTrue(asked.all { it.headers["Authorization"]?.startsWith("Bearer ") == true })
        assertTrue("at home: no remote session", "/api/tv/remote/tune" !in home.paths)
        controls()
        compose.onNodeWithTag("live-channel-name").assertTextEquals("Quran")
        compose.onNodeWithTag("mini-guide").assertIsDisplayed()
    }

    @Test
    fun nextAndPreviousChangeTheChannelAndWrap() {
        openChannel("c1")
        compose.waitUntil(5_000) { engine.played.size == 1 }
        controls()
        compose.onNodeWithTag("live-next").performClick()
        compose.waitUntil(5_000) { engine.played.lastOrNull()?.encodedPath == "/stream/channel/c2/index.m3u8" }
        controls()
        compose.onNodeWithTag("live-previous").performClick()
        compose.waitUntil(5_000) { engine.played.lastOrNull()?.encodedPath == "/stream/channel/c1/index.m3u8" }
        controls()
        compose.onNodeWithTag("live-previous").performClick()
        compose.waitUntil(5_000) { engine.played.lastOrNull()?.encodedPath == "/stream/channel/c10/index.m3u8" }
        controls()
        compose.onNodeWithTag("live-channel-name").assertTextEquals("Nasheed")
    }

    @Test
    fun backToLiveShowsAfterAPauseAndReturnsToTheEdge() {
        openChannel("c1")
        compose.waitUntil(5_000) { engine.played.isNotEmpty() }
        assertTrue(!exists("back-to-live"))
        controls()
        compose.onNodeWithTag("live-pause").performClick()
        waitForTag("back-to-live")
        compose.onNodeWithTag("back-to-live").performClick()
        compose.waitUntil(5_000) { !exists("back-to-live") }
        assertEquals(1, engine.goLives)
    }

    @Test
    fun awayFromHomeItTunesARemoteSessionAndEndsItOnLeave() {
        openChannel("c1", relay = true)
        compose.waitUntil(5_000) { engine.played.isNotEmpty() }
        assertTrue(engine.played.first().host.endsWith(".cloudflarestream.com"))
        val tune = home.requests.single { it.url.encodedPath == "/api/tv/remote/tune" }
        assertEquals("POST", tune.method)
        assertTrue(tune.body?.utf8().orEmpty().contains("\"channel_id\":\"c1\""))

        compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
        compose.waitUntil(5_000) {
            home.requests.any { it.method == "DELETE" && it.url.encodedPath == "/api/tv/remote/sessions/s-1" }
        }
        waitForTag("channel-c1")
        assertTrue(engine.releases >= 1)
    }
}
