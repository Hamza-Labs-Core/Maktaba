package com.hamzalabs.maktaba.player.live

import androidx.activity.ComponentActivity
import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeLeft
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.time.Instant

/** Live TV's Now and Guide (FR-21, FR-22) against the fake home, at [NOW] in UTC. */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp-xxhdpi")
class LiveTvScreenTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val engine = FakeLiveEngine()

    private fun waitForTag(tag: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty() }

    private fun openLive(window: Window = Window.Phone) {
        LiveFixtures.install(home)
        compose.setLive(home, engine, window)
        compose.onNodeWithTag("section-LiveTv").performClick()
        waitForTag("channel-c1")
    }

    private fun progress(f: Float) =
        SemanticsMatcher.expectValue(SemanticsProperties.ProgressBarRangeInfo, ProgressBarRangeInfo(f, 0f..1f))

    @Test
    fun nowListsTheMocksChannelsWithWhatIsOnItsProgressAndWhatIsNext() {
        openLive()
        assertTrue("/api/channels/now" in home.paths)
        compose.onNodeWithTag("now-list").assertIsDisplayed()
        compose.onNodeWithText("Quran").assertIsDisplayed()
        compose.onNodeWithText("Seerah").assertIsDisplayed()
        compose.onNodeWithText("Tafsir of Al-Baqarah").assertIsDisplayed()
        // 18:00–18:40 at 18:20: half through
        compose.onAllNodes(progress(0.5f) and hasAnyAncestor(hasTestTag("channel-c1")), useUnmergedTree = true).onFirst().assertIsDisplayed()
        compose.onNodeWithText("Next at 18:40: Tafsir of Al-Baqarah, part 5").assertIsDisplayed()
        // every channel is listed, the last one too
        compose.onNodeWithTag("now-list").performScrollToNode(hasTestTag("channel-c10"))
        compose.onNodeWithText("Nasheed").assertIsDisplayed()
    }

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun onATabletNowIsAGrid() {
        openLive(Window.Tablet)
        compose.onNodeWithTag("now-grid").assertIsDisplayed()
    }

    @Test
    fun tappingAChannelTunesIt() {
        openLive()
        compose.onNodeWithTag("channel-c2").performClick()
        waitForTag("channel-player")
        compose.waitUntil(5_000) { engine.played.isNotEmpty() }
        assertEquals("/stream/channel/c2/index.m3u8", engine.played.first().encodedPath)
    }

    @Test
    fun theGuideHasTheNowLineAtTheRightXAndScrollsBothWays() {
        openLive()
        compose.onNodeWithTag("tab-guide").performClick()
        waitForTag("guide-row-c1")
        // a phone's window: 17:30 to 20:30
        val guide = home.requests.last { it.url.encodedPath == "/api/channels/guide" }.url
        assertEquals("2026-10-31T17:30:00Z", guide.queryParameter("start"))
        assertEquals("2026-10-31T20:30:00Z", guide.queryParameter("end"))

        // 18:20 is 50 minutes in: 200 dp from the track's start (the line is 2 dp wide, centred)
        val track = compose.onNodeWithTag("guide-track").getUnclippedBoundsInRoot()
        val line = compose.onNodeWithTag("now-line").getUnclippedBoundsInRoot()
        assertEquals((GuideDpPerMinute * 50f - 1.dp).value, (line.left - track.left).value, 0.5f)

        // across: a swipe moves the time (the now-line moves toward the start)
        compose.onNodeWithTag("guide-row-c1").performTouchInput { swipeLeft() }
        compose.waitForIdle()
        val after = compose.onNodeWithTag("now-line").getUnclippedBoundsInRoot()
        assertTrue("moved: ${line.left} → ${after.left}", after.left < line.left - 20.dp)

        // down: the last channel's row
        compose.onNodeWithTag("guide-rows").performScrollToNode(hasTestTag("guide-row-c10"))
        compose.onNodeWithTag("guide-row-c10").assertIsDisplayed()
    }

    @Test
    fun theDayPickerAndLaterMoveTheGuidesWindow() {
        openLive()
        compose.onNodeWithTag("tab-guide").performClick()
        waitForTag("guide-row-c1")
        compose.onNodeWithTag("day-1").performClick()
        compose.waitForIdle()
        compose.waitUntil(5_000) {
            home.requests.any { it.url.encodedPath == "/api/channels/guide" && it.url.queryParameter("start") == "2026-11-01T17:30:00Z" }
        }
        // the next day's guide shows, then Later moves it on by the window's three hours
        waitForTag("guide-row-c1")
        compose.onNodeWithTag("guide-later").performClick()
        compose.waitForIdle()
        compose.waitUntil(5_000) {
            home.requests.any { it.url.encodedPath == "/api/channels/guide" && it.url.queryParameter("start") == "2026-11-01T20:30:00Z" }
        }
    }

    @Test
    fun aProgrammesSheetShowsItAndWatchOpensTheChannel() {
        openLive()
        compose.onNodeWithTag("tab-guide").performClick()
        waitForTag("guide-row-c1")
        compose.onNodeWithTag("programme-c1-${Instant.parse("2026-10-31T18:00:00Z")}").performClick()
        waitForTag("programme-sheet")
        compose.onNode(hasText("An episode of Tafsir of Al-Baqarah.")).assertIsDisplayed()
        compose.onNode(hasText("Quran · 18:00 – 18:40")).assertIsDisplayed()
        compose.onNodeWithTag("programme-watch").performClick()
        waitForTag("channel-player")
        compose.waitUntil(5_000) { engine.played.isNotEmpty() }
        assertEquals("/stream/channel/c1/index.m3u8", engine.played.first().encodedPath)
    }

    @Test
    fun aLaterProgrammesSheetHasNoWatch() {
        openLive()
        compose.onNodeWithTag("tab-guide").performClick()
        waitForTag("guide-row-c1")
        compose.onNodeWithTag("programme-c1-${Instant.parse("2026-10-31T18:40:00Z")}").performClick()
        waitForTag("programme-sheet")
        compose.onNode(hasText("An episode of Tafsir of Al-Baqarah, part 5.")).assertIsDisplayed()
        compose.onAllNodes(hasTestTag("programme-watch")).fetchSemanticsNodes().let { assertTrue(it.isEmpty()) }
    }
}
