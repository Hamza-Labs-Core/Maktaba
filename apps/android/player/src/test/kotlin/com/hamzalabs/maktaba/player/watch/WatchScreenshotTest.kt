@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.watch

import com.hamzalabs.maktaba.player.profile.PlaybackSettings
import com.hamzalabs.maktaba.player.profile.LocalPlaybackSettings
import android.content.Context
import android.os.Looper
import androidx.activity.ComponentActivity
import androidx.annotation.OptIn
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.test.utils.FakeClock
import androidx.media3.test.utils.robolectric.CapturingRenderersFactory
import androidx.media3.test.utils.robolectric.ShadowMediaCodecConfig
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route
import androidx.compose.runtime.LaunchedEffect
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The player's screenshots (NFR-20): dark, English and Arabic, on a phone and a tablet, against
 * the fake home's stream, paused at 0:02 of an episode with a next one: the controls, the
 * subtitle and audio picker, and Up next. `recordRoborazziPhoneDebug` writes the references.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class WatchScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val codecs: ShadowMediaCodecConfig = ShadowMediaCodecConfig.withAllDefaultSupportedCodecs()
    @get:Rule(order = 2) val compose = createAndroidComposeRule<ComponentActivity>()

    private val clock = FakeClock(false)
    private val app: Context = ApplicationProvider.getApplicationContext()

    @Before
    fun setUp() {
        FakeStreams(home).install()
        // the catalogue's length is the test stream's (4 s), as a real home's is its video's
        home.answer("/api/videos/v2") { FakeHome.json(FakeHome.video("v2", "Omar, Episode 1").replace("5400", "4")) }
        home.answer("/api/me/videos/v2") { FakeHome.json("""{"video_id":"v2","series":{"id":"se1","name":"Omar"}}""") }
        home.answer("/api/series/se1/episodes") {
            FakeHome.json("""{"seasons":[{"season":1,"episodes":[{"video_id":"v2","title":"Omar, Episode 1"},{"video_id":"v3","title":"Omar, Episode 2"}]}]}""")
        }
        home.answer("/api/videos/v3") { FakeHome.json(FakeHome.video("v3", "Omar, Episode 2")) }
    }

    private fun until(condition: () -> Boolean) = compose.waitUntil(10_000) {
        clock.advanceTime(20)
        shadowOf(Looper.getMainLooper()).idle()
        condition()
    }

    /** The player on [watch], paused once it plays (the controls then stay), Up next per [autoplay]. */
    private fun paused(autoplay: Boolean) {
        val prefs = PlaybackSettings(MapStore()).apply { update { it.copy(autoplayNext = autoplay) } }
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) {
                Surface {
                    CompositionLocalProvider(
                        LocalPlayerHome provides home.home,
                        LocalPlaybackSettings provides prefs,
                        LocalPlayerBuild provides { b: ExoPlayer.Builder -> b.setClock(clock).setRenderersFactory(CapturingRenderersFactory(app, clock)) },
                    ) {
                        PlayerShell(
                            sections = listOf(PlayerSection.Home),
                            screens = { route, nav ->
                                if (route == Route.Home) LaunchedEffect(Unit) { nav.open(Route.Watch("v2", startSec = 2)) } else AppScreens(route, nav)
                            },
                        )
                    }
                }
            }
        }
        val pause = compose.activity.getString(R.string.watch_pause)
        until { compose.onAllNodes(hasTestTag("play-pause") and hasContentDescription(pause)).fetchSemanticsNodes().isNotEmpty() }
        until { compose.onAllNodes(hasTestTag("next")).fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithTag("play-pause").performClick()
        val play = compose.activity.getString(R.string.watch_play)
        until { compose.onAllNodes(hasTestTag("play-pause") and hasContentDescription(play)).fetchSemanticsNodes().isNotEmpty() }
    }

    private fun shoot(name: String) {
        // the controls read the paused player's position again
        compose.mainClock.advanceTimeBy(1_000)
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    private fun controls(name: String) {
        paused(autoplay = false)
        shoot(name)
    }

    private fun picker(name: String) {
        paused(autoplay = false)
        compose.onNodeWithTag("tracks-button").performClick()
        shoot(name)
    }

    private fun upNext(name: String) {
        paused(autoplay = true)
        until { compose.onAllNodes(hasTestTag("up-next")).fetchSemanticsNodes().isNotEmpty() }
        shoot(name)
    }

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun controlsPhoneEnglish() = controls("watch_controls_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun controlsPhoneArabic() = controls("watch_controls_phone_dark_ar")

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun controlsTabletEnglish() = controls("watch_controls_tablet_dark_en")

    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi")
    fun controlsTabletArabic() = controls("watch_controls_tablet_dark_ar")

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun pickerPhoneEnglish() = picker("watch_tracks_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun pickerPhoneArabic() = picker("watch_tracks_phone_dark_ar")

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun upNextPhoneEnglish() = upNext("watch_up_next_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun upNextPhoneArabic() = upNext("watch_up_next_phone_dark_ar")

    private companion object {
        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
