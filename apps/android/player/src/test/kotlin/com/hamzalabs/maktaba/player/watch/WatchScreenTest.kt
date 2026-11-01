@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.watch

import com.hamzalabs.maktaba.player.profile.SubtitleSize
import com.hamzalabs.maktaba.player.profile.StreamQuality
import com.hamzalabs.maktaba.player.profile.PlaybackSettings
import com.hamzalabs.maktaba.player.profile.LocalPlaybackSettings
import android.os.Looper
import androidx.activity.ComponentActivity
import org.robolectric.Shadows.shadowOf
import androidx.annotation.OptIn
import androidx.compose.foundation.layout.Box
import androidx.compose.material3.Button
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.printToString
import androidx.compose.ui.test.hasTestTag
import com.hamzalabs.maktaba.player.R
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.click
import androidx.compose.ui.test.doubleClick
import androidx.compose.ui.test.centerLeft
import androidx.compose.ui.test.centerRight
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.test.utils.FakeClock
import androidx.media3.test.utils.robolectric.CapturingRenderersFactory
import androidx.media3.test.utils.robolectric.ShadowMediaCodecConfig
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * The player as a person meets it (48.32, as 48.23 on Apple), opened as the shell's Watch route
 * against the fake home's streams, which really play (decoders faked on the JVM, the player's
 * clock moved by the test).
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp-xxhdpi")
class WatchScreenTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val codecs: ShadowMediaCodecConfig = ShadowMediaCodecConfig.withAllDefaultSupportedCodecs()
    @get:Rule(order = 2) val compose = createAndroidComposeRule<ComponentActivity>()

    private lateinit var streams: FakeStreams
    private val prefs = PlaybackSettings(MapStore())
    /** The players' clock: it moves only when the test moves it, so a 4-second stream keeps playing. */
    private val clock = FakeClock(false)
    private val app: android.content.Context = androidx.test.core.app.ApplicationProvider.getApplicationContext()

    @Before
    fun setUp() {
        streams = FakeStreams(home)
        streams.install()
        // the series: Omar, Episode 1 (v2), then Lion of the Desert (v3)
        home.answer("/api/me/videos/v2") { FakeHome.json("""{"video_id":"v2","series":{"id":"se1","name":"Omar"}}""") }
        home.answer("/api/series/se1/episodes") {
            FakeHome.json("""{"seasons":[{"season":1,"episodes":[{"video_id":"v2","episode":1,"title":"Omar, Episode 1"},{"video_id":"v3","episode":2,"title":"Lion of the Desert"}]}]}""")
        }
    }

    /** The shell with a Home that opens [watch], and the player built with the test's clock. */
    private fun open(watch: Route.Watch) {
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) {
                Surface {
                    CompositionLocalProvider(
                        LocalPlayerHome provides home.home,
                        LocalPlaybackSettings provides prefs,
                        LocalPlayerBuild provides { b: ExoPlayer.Builder ->
                            b.setClock(clock).setRenderersFactory(CapturingRenderersFactory(app, clock))
                        },
                    ) {
                        PlayerShell(
                            sections = listOf(PlayerSection.Home),
                            adaptiveInfo = Window.Phone.info(),
                            screens = { route, nav ->
                                when (route) {
                                    Route.Home -> Box {
                                        Button(onClick = { nav.open(watch) }, Modifier.testTag("open")) { Text("Watch") }
                                    }
                                    else -> AppScreens(route, nav)
                                }
                            },
                        )
                    }
                }
            }
        }
        compose.onNodeWithTag("open").performClick()
    }

    /** Waits, moving the players' clock on a little each time, until [condition]. */
    private fun until(timeoutMs: Long = 10_000, stepMs: Long = 20, condition: () -> Boolean) = try {
        compose.waitUntil(timeoutMs) {
            clock.advanceTime(stepMs)
            // the player's work runs on the main looper: let it
            shadowOf(Looper.getMainLooper()).idle()
            condition()
        }
    } catch (e: Throwable) {
        // what the home was asked and what the screen shows, to tell why
        println("until timed out; requests: " + home.requests.map { it.method + " " + it.url.encodedPath })
        println(compose.onRoot(useUnmergedTree = false).printToString())
        throw e
    }

    private fun shown(tag: String) = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()

    /** Until the player shows its controls over a stream that plays. */
    private fun untilPlaying() {
        val pause = compose.activity.getString(R.string.watch_pause)
        until { compose.onAllNodes(hasTestTag("play-pause") and hasContentDescription(pause)).fetchSemanticsNodes().isNotEmpty() }
    }

    @Test
    fun opensAtTheResumePositionAndReportsProgress() {
        home.answer("/api/videos/v1") {
            FakeHome.json("""{"id":"v1","library_id":"l1","filename":"v1.mkv","title":"The Message","duration_sec":4,"playback_state":{"position_sec":2.5,"completed":false}}""")
        }
        open(Route.Watch("v1"))
        untilPlaying()
        assertEquals(2, streams.opens().single().int("start_sec"))
        compose.onNodeWithText("The Message").assertIsDisplayed()

        // Pause: the position goes to the home at once
        compose.onNodeWithTag("play-pause").performClick()
        until { streams.progress().isNotEmpty() }
        assertTrue(streams.progress().last().second.int("position_sec")!! >= 2)
    }

    @Test
    fun aFinishedVideoAndAGivenStartOpenWhereAsked() {
        home.answer("/api/videos/v1") {
            FakeHome.json("""{"id":"v1","title":"The Message","duration_sec":4,"playback_state":{"position_sec":3.9,"completed":true}}""")
        }
        open(Route.Watch("v1"))
        until { streams.opens().isNotEmpty() }
        assertNull("a finished video starts again", streams.opens().single().int("start_sec"))
    }

    @Test
    fun thePickerListsTheSessionsRenditionsAndShowsTheChosenOne() {
        open(Route.Watch("v1", startSec = 0))
        untilPlaying()
        compose.onNodeWithTag("tracks-button").performClick()
        compose.onNodeWithTag("picker").assertIsDisplayed()
        compose.onNodeWithText("Off").assertIsSelected()
        compose.onNodeWithText("English").assertIsNotSelected()
        compose.onNodeWithText("العربية").assertIsNotSelected()
        // the stream's sound is listed, and playing
        compose.onNodeWithTag("audio-0").assertIsSelected()

        compose.onNodeWithText("English").performClick()
        until { runCatching { compose.onNodeWithTag("subtitle-0").assertIsSelected() }.isSuccess }
        compose.onNodeWithText("Off").assertIsNotSelected()
        compose.onNodeWithText("العربية").assertIsNotSelected()
    }

    @Test
    fun audioTracksSwitch() {
        streams.direct = true
        open(Route.Watch("v1", startSec = 0))
        untilPlaying()
        compose.onNodeWithTag("tracks-button").performClick()
        compose.onNodeWithText("English").assertIsSelected()
        compose.onNodeWithText("Arabic").performClick()
        until { runCatching { compose.onNodeWithTag("audio-1").assertIsSelected() }.isSuccess }
        compose.onNodeWithTag("audio-0").assertIsNotSelected()
    }

    @Test
    fun qualityReopensAtTheSamePositionWithMaxBitrate() {
        // starts at Auto: Settings' quality is the original on either network (Robolectric's is metered)
        prefs.update { it.copy(qualityWifi = StreamQuality.Original, qualityMobile = StreamQuality.Original) }
        streams.ladder = """[{"height":1080,"width":1920,"bitrate_kbps":6000,"codec":"h264"},{"height":720,"width":1280,"bitrate_kbps":3000,"codec":"h264"}]"""
        open(Route.Watch("v1", startSec = 2))
        untilPlaying()
        compose.onNodeWithTag("quality-button").performClick()
        compose.onNodeWithText("Auto").assertIsSelected()
        compose.onNodeWithText("1080p").assertIsDisplayed()
        compose.onNodeWithText("720p").performClick()
        until { streams.opens().size == 2 }
        val reopened = streams.opens()[1]
        assertEquals(3000, reopened.int("max_bitrate_kbps"))
        assertEquals(2, reopened.int("start_sec"))
    }

    @Test
    fun theVideoStartsAtTheQualityChosenInProfilesSettings() {
        // Profile's Settings writes the store the player reads (one store, 48.33)
        prefs.update { it.copy(qualityWifi = StreamQuality.P480, qualityMobile = StreamQuality.P480, subtitleSize = SubtitleSize.Large) }
        open(Route.Watch("v1", startSec = 0))
        untilPlaying()
        assertEquals(1_500, streams.opens().single().int("max_bitrate_kbps"))
    }

    @Test
    fun controlsHideAfterFourSecondsAndATapShowsThem() {
        open(Route.Watch("v1", startSec = 0))
        untilPlaying()
        compose.onNodeWithTag("controls").assertIsDisplayed()
        compose.mainClock.advanceTimeBy(3_000)
        compose.onNodeWithTag("controls").assertIsDisplayed()
        compose.mainClock.advanceTimeBy(1_500)
        compose.waitUntil(2_000) { !shown("controls") }

        compose.onNodeWithTag("watch-surface").performTouchInput { click(center) }
        compose.mainClock.advanceTimeBy(500)
        compose.waitUntil(2_000) { shown("controls") }
    }

    @Test
    fun aDoubleTapOnTheRightSeeksForwardAndOnTheLeftBack() {
        open(Route.Watch("v1", startSec = 2))
        untilPlaying()
        compose.onNodeWithTag("watch-surface").performTouchInput { doubleClick(centerLeft + androidx.compose.ui.geometry.Offset(40f, 0f)) }
        compose.waitUntil(2_000) { shown("seek-flash") }
        compose.onNodeWithText("Back 10 seconds").assertIsDisplayed()
    }

    @Test
    @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun inArabicTheDoubleTapMirrors() {
        open(Route.Watch("v1", startSec = 2))
        untilPlaying()
        // the right side is where an Arabic video starts: back
        compose.onNodeWithTag("watch-surface").performTouchInput { doubleClick(centerRight - androidx.compose.ui.geometry.Offset(40f, 0f)) }
        compose.waitUntil(2_000) { shown("seek-flash") }
        compose.onNodeWithText("رجوع ١٠ ثوانٍ").assertIsDisplayed()
    }

    @Test
    fun upNextCountsDownAndPlaysTheNextEpisode() {
        open(Route.Watch("v2", startSec = 0))
        untilPlaying()
        // a 4-second episode is in its last 10 seconds from the start
        until { shown("up-next") }
        compose.onNodeWithText("Lion of the Desert").assertIsDisplayed()
        compose.onNodeWithTag("next").assertIsDisplayed()
        compose.mainClock.advanceTimeBy(11_000)
        until { streams.opens().size == 2 }
        assertEquals("v3", streams.opens()[1]["video_id"].toString().trim('"'))
        // the first episode's session was closed
        until { "s1" in streams.closed() }
    }

    @Test
    fun withAutoplayOffItStopsAtTheEndAndNextStays() {
        prefs.update { it.copy(autoplayNext = false) }
        open(Route.Watch("v2", startSec = 0))
        untilPlaying()
        until { shown("next") }
        compose.mainClock.advanceTimeBy(15_000)
        // play it to its end
        until(stepMs = 200) { streams.progress().any { it.second["completed"] != null } }
        compose.mainClock.advanceTimeBy(15_000)
        compose.waitForIdle()
        assertTrue("no Up next", !shown("up-next"))
        assertEquals(1, streams.opens().size)

        // Next still plays the next episode
        compose.onNodeWithTag("watch-surface").performTouchInput { click(center) }
        compose.mainClock.advanceTimeBy(500)
        compose.waitUntil(2_000) { shown("next") }
        compose.onNodeWithTag("next").performClick()
        until { streams.opens().size == 2 }
        assertEquals("v3", streams.opens()[1]["video_id"].toString().trim('"'))
    }
}
