@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.media

import android.app.PictureInPictureParams
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.os.Looper
import android.util.Rational
import androidx.activity.ComponentActivity
import androidx.annotation.OptIn
import androidx.compose.foundation.layout.Box
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Cast
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.printToString
import androidx.lifecycle.Lifecycle
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.test.utils.FakeClock
import androidx.media3.test.utils.robolectric.CapturingRenderersFactory
import androidx.media3.test.utils.robolectric.ShadowMediaCodecConfig
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.watch.FakeStreams
import com.hamzalabs.maktaba.player.watch.LocalPlaybackPrefs
import com.hamzalabs.maktaba.player.watch.LocalPlayerBuild
import com.hamzalabs.maktaba.player.watch.PlaybackPrefs
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * The player outside its screen (48.33), opened as the shell's Watch route against the fake
 * home's streams: picture in picture on Home (FR-37), the sound and the system's controls with
 * the screen off (FR-39), and the Cast button (FR-38). Robolectric runs Android 15 (SDK 35),
 * where Home opens the window by itself (auto-enter).
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp-xxhdpi")
class PipBackgroundCastTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val codecs: ShadowMediaCodecConfig = ShadowMediaCodecConfig.withAllDefaultSupportedCodecs()
    @get:Rule(order = 2) val compose = createAndroidComposeRule<ComponentActivity>()

    private lateinit var streams: FakeStreams
    private val clock = FakeClock(false)
    private val app: android.content.Context = androidx.test.core.app.ApplicationProvider.getApplicationContext()
    /** The picture-in-picture settings the screen gave the activity, in order. */
    private val pip = mutableListOf<PictureInPictureParams>()
    private val routes = FakeCastRoutes()

    @Before
    fun setUp() {
        streams = FakeStreams(home)
        streams.install()
        home.answer("/api/videos/v1") { FakeHome.json("""{"id":"v1","library_id":"l1","filename":"v1.mkv","title":"The Message","duration_sec":4}""") }
        shadowOf(app.packageManager).setSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE, true)
    }

    @After
    fun tearDown() {
        // the route's player is let go with the screen
        compose.activityRule.scenario.moveToState(Lifecycle.State.DESTROYED)
    }

    private fun open() {
        compose.setContent {
            MaktabaTheme(ThemeMode.Dark) {
                Surface {
                    CompositionLocalProvider(
                        LocalPlayerHome provides home.home,
                        LocalPlaybackPrefs provides PlaybackPrefs(MapStore()),
                        LocalPlayerBuild provides { b: ExoPlayer.Builder ->
                            b.setClock(clock).setRenderersFactory(CapturingRenderersFactory(app, clock))
                        },
                        LocalPipParams provides { _, params -> pip += params },
                        LocalCastRoutes provides routes,
                    ) {
                        PlayerShell(
                            sections = listOf(PlayerSection.Home),
                            adaptiveInfo = Window.Phone.info(),
                            screens = { route, nav ->
                                when (route) {
                                    Route.Home -> Box {
                                        Button(onClick = { nav.open(Route.Watch("v1", startSec = 0)) }, Modifier.testTag("open")) { Text("Watch") }
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

    private fun until(timeoutMs: Long = 10_000, condition: () -> Boolean) = try {
        compose.waitUntil(timeoutMs) {
            clock.advanceTime(20)
            shadowOf(Looper.getMainLooper()).idle()
            condition()
        }
    } catch (e: Throwable) {
        println(compose.onRoot(useUnmergedTree = false).printToString())
        throw e
    }

    private fun shown(tag: String) = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty()

    private fun untilPlaying(): Player {
        val pause = compose.activity.getString(R.string.watch_pause)
        until { compose.onAllNodes(hasTestTag("play-pause") and hasContentDescription(pause)).fetchSemanticsNodes().isNotEmpty() }
        return requireNotNull(NowPlaying.player) { "the system's controls have the player" }
    }

    private fun titles(p: PictureInPictureParams) = p.actions.map { it.title.toString() }

    @Test
    fun homeWhilePlayingOpensPictureInPictureWithItsButtons() {
        open()
        val player = untilPlaying()
        until { pip.lastOrNull()?.isAutoEnterEnabled == true }
        val playingParams = pip.last()
        assertEquals("the video's shape (64×36)", Rational(16, 9), playingParams.aspectRatio)
        assertEquals(listOf("Back 10 seconds", "Pause", "Forward 10 seconds"), titles(playingParams))

        // its Pause button pauses; then Home no longer opens the window, and the button is Play
        playingParams.actions[1].actionIntent.send()
        until { !player.playWhenReady }
        until { pip.last().isAutoEnterEnabled == false }
        assertEquals(listOf("Back 10 seconds", "Play", "Forward 10 seconds"), titles(pip.last()))

        // its Play button plays again, and Forward seeks 10 s (to the end of this 4 s stream)
        pip.last().actions[1].actionIntent.send()
        until { player.playWhenReady }
        val at = player.currentPosition
        pip.last().actions[2].actionIntent.send()
        until { player.currentPosition > at }
    }

    @Test
    fun inTheWindowTheVideoShowsAloneAndClosingItPauses() {
        open()
        val player = untilPlaying()
        assertTrue(shown("watch-surface"))

        compose.activity.onPictureInPictureModeChanged(true, Configuration())
        until { !shown("watch-surface") }
        assertFalse("no controls in the window", shown("controls"))
        assertTrue("still playing in the window", player.playWhenReady)

        // the person closes the window: the activity stops, then leaves the mode
        compose.activityRule.scenario.moveToState(Lifecycle.State.CREATED)
        compose.activity.onPictureInPictureModeChanged(false, Configuration())
        until { !player.playWhenReady }
    }

    @Test
    fun leavingTheScreenTurnsAutoEnterOff() {
        open()
        untilPlaying()
        until { pip.lastOrNull()?.isAutoEnterEnabled == true }
        compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
        until { shown("open") }
        assertFalse("Home after the player closed opens no window", pip.last().isAutoEnterEnabled)
        assertNull("the system's controls are let go", NowPlaying.player)
    }

    @Test
    fun theSoundGoesOnWithTheScreenOffAndTheSystemDrivesThePlayer() {
        open()
        val player = untilPlaying()
        // the screen off (the activity stopped): still playing, no pause
        compose.activityRule.scenario.moveToState(Lifecycle.State.CREATED)
        repeat(20) { clock.advanceTime(50); shadowOf(Looper.getMainLooper()).idle() }
        assertTrue("plays on with the screen off", player.playWhenReady)
        assertEquals(Player.STATE_READY, player.playbackState)

        // the media session's service holds the same player: the notification and the lock
        // screen drive it (play / pause, ±10 s)
        val running = Robolectric.buildService(PlaybackService::class.java).create()
        val service = running.get()
        val session = service.sessions.single()
        assertTrue(session.player === player)
        assertTrue(player.isCommandAvailable(Player.COMMAND_SEEK_BACK))
        assertEquals(SEEK_INCREMENT_MS, player.seekBackIncrement)
        assertEquals(SEEK_INCREMENT_MS, player.seekForwardIncrement)
        assertEquals("the notification's title", "The Message", player.mediaMetadata.title?.toString())
        session.player.pause()
        until { !player.playWhenReady }

        // the video closes: the service lets the session go
        compose.activityRule.scenario.moveToState(Lifecycle.State.RESUMED)
        compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
        until { shown("open") }
        assertTrue(service.sessions.isEmpty())
        running.destroy()
    }

    @Test
    fun theCastButtonShowsOnlyWhileAReceiverIsOnTheNetwork() {
        open()
        untilPlaying()
        assertFalse("no receiver: no button", shown(CAST_BUTTON_TAG))
        routes.receiver = true
        until { shown(CAST_BUTTON_TAG) }
        routes.receiver = false
        until { !shown(CAST_BUTTON_TAG) }
    }
}

/** Receivers on the network as a test sets them; the button is a plain one. */
class FakeCastRoutes : CastRoutes {
    var receiver by mutableStateOf(false)

    @Composable
    override fun available(): Boolean = receiver

    @Composable
    override fun Button(modifier: Modifier) {
        IconButton(onClick = {}, modifier = modifier.testTag(CAST_BUTTON_TAG)) { Icon(Icons.Filled.Cast, "Cast") }
    }
}
