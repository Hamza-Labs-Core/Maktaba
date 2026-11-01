package com.hamzalabs.maktaba.player.profile

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.shell.PlayerSection
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.time.Instant

/**
 * The Profile's screenshots (NFR-20): dark, English and Arabic, on a phone and a large tablet,
 * against the fake home: the first page, the watch history and a playlist. The shipped sections
 * (Libraries and Profile). `recordRoborazziPhoneDebug` writes the references under
 * `src/test/screenshots`; CI's `verifyRoborazziPhoneDebug` fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ProfileScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    @Before
    fun routes() {
        ProfileHome.install(home)
        // "watched 2 hours ago" stays the same whatever day the test runs
        val now = Instant.now()
        val history = ProfileHome.HISTORY
            .replace("2026-10-31T20:00:00Z", now.minusSeconds(2 * 3600).toString())
            .replace("2026-10-29T18:00:00Z", now.minusSeconds(3 * 86_400).toString())
            .replace("2026-10-20T09:00:00Z", now.minusSeconds(12 * 86_400).toString())
        home.answer("/api/me/history") { json(history) }
    }

    private fun show(window: Window) {
        compose.setProfileShell(
            home,
            AppLanguages(35, FakeLocales(), MapStore()),
            PlaybackSettings(MapStore()),
            window = window,
            sections = PlayerSection.enabled,
            images = pathPictures(ApplicationProvider.getApplicationContext()),
        )
        compose.onNodeWithTag("section-Profile").performClick()
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag("profile-header")).fetchSemanticsNodes().isNotEmpty() }
    }

    private fun shoot(name: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    private fun first(window: Window, name: String) {
        show(window)
        shoot(name)
    }

    private fun history(window: Window, name: String) {
        show(window)
        compose.onNodeWithTag("profile-history").performClick()
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag("history-v4")).fetchSemanticsNodes().isNotEmpty() }
        shoot(name)
    }

    private fun playlist(window: Window, name: String) {
        show(window)
        compose.onNodeWithTag("profile-playlists").performClick()
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag("playlist-p1")).fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithTag("playlist-p1").performClick()
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag("playlist-item-v5")).fetchSemanticsNodes().isNotEmpty() }
        shoot(name)
    }

    private fun settings(window: Window, name: String) {
        show(window)
        compose.onNodeWithTag("profile-root").performScrollToNode(hasTestTag("profile-playback"))
        shoot(name)
    }

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun phoneEnglish() = first(Window.Phone, "profile_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun phoneArabic() = first(Window.Phone, "profile_phone_dark_ar")

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun phoneSettingsEnglish() = settings(Window.Phone, "profile_settings_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun phoneSettingsArabic() = settings(Window.Phone, "profile_settings_phone_dark_ar")

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun phoneHistoryEnglish() = history(Window.Phone, "profile_history_phone_dark_en")

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun phoneHistoryArabic() = history(Window.Phone, "profile_history_phone_dark_ar")

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun tabletEnglish() = first(Window.Tablet, "profile_tablet_dark_en")

    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi")
    fun tabletArabic() = first(Window.Tablet, "profile_tablet_dark_ar")

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun tabletPlaylistEnglish() = playlist(Window.Tablet, "profile_playlist_tablet_dark_en")

    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi")
    fun tabletPlaylistArabic() = playlist(Window.Tablet, "profile_playlist_tablet_dark_ar")

    private companion object {
        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
