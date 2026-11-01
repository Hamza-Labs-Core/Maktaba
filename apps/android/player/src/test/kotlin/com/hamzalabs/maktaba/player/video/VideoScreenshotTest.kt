package com.hamzalabs.maktaba.player.video

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.ui.components.PictureState
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * A video's page in the dark theme, English and Arabic (48.28, as 48.19's poster criterion):
 * with a poster, a portrait poster at its own shape, and without one (the title on the brand's
 * colour), on a phone; with a poster on a tablet. Arabic shots read Arabic fixtures.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class VideoScreenshotTest {
    @get:Rule(order = 0) val fake = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))

    private fun shoot(id: String, name: String, window: Window = Window.Phone) {
        VideoHome(fake)
        val ar = compose.activity.resources.configuration.locales[0].language == "ar"
        if (ar) arabic()
        val pictures = VideoPictures(compose.activity)
        compose.setVideoShell(fake, pictures, VideoLink.of(id), window)
        val shown = if (id == "vnoposter") PictureState.Missing else PictureState.Shown
        compose.waitUntil(5_000) {
            compose.onAllNodes(hasTestTag("video-poster") and SemanticsMatcher.expectValue(PosterStateKey, shown)).fetchSemanticsNodes().isNotEmpty()
        }
        compose.waitForIdle()
        val lang = if (ar) "ar" else "en"
        compose.onRoot().captureRoboImage("src/test/screenshots/video_${name}_dark_$lang.png", roborazziOptions = options)
    }

    /** The fixtures in Arabic, as an Arabic home names them. */
    private fun arabic() {
        fake.answer("/api/videos/v1") { json(VideoHome.detail("v1", "رحلة الليل", "ليلة من السفر، تُروى على مهل.", 2700, 754.0, false)) }
        fake.answer("/api/videos/vportrait") { json(VideoHome.detail("vportrait", "فيلم قصير بالطول", "صُوِّر بالهاتف.", 95, null, false)) }
        fake.answer("/api/videos/vnoposter") { json(VideoHome.detail("vnoposter", "درس بلا ملصق", "ما زال قيد التحضير.", 600, null, false)) }
        fake.answer("/api/me/videos/v1") {
            json("""{"video_id":"v1","position_sec":754,"completed":false,"favorite":true,"playlists":[],"series":{"id":"s1","name":"عمر"}}""")
        }
        fake.answer("/api/videos/v1/chapters") {
            json("""{"items":[{"id":1,"seq":1,"start_sec":0,"title":"البداية"},{"id":2,"seq":2,"start_sec":600,"title":"الطريق شمالًا"},{"id":3,"seq":3,"start_sec":1500,"title":"العودة"}]}""")
        }
        fake.answer("/api/videos/v1/segments") {
            json("""{"items":[{"id":1,"seq":1,"start_sec":2,"end_sec":6,"text":"كانت ليلة لا تشبه غيرها."}]}""")
        }
        fake.answer("/api/series/s1/episodes") {
            json(
                """{"seasons":[{"season":1,"episodes":[{"video_id":"v1","season":1,"episode":2,"title":"رحلة الليل"},""" +
                    """{"video_id":"e3","season":1,"episode":3,"title":"الهجرة"},{"video_id":"e4","season":1,"episode":4,"title":"غزوة بدر"}]}]}""",
            )
        }
    }

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi") fun posterPhoneEn() = shoot("v1", "poster_phone")
    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi") fun posterPhoneAr() = shoot("v1", "poster_phone")

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi") fun portraitPhoneEn() = shoot("vportrait", "portrait_phone")
    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi") fun portraitPhoneAr() = shoot("vportrait", "portrait_phone")

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi") fun noPosterPhoneEn() = shoot("vnoposter", "noposter_phone")
    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi") fun noPosterPhoneAr() = shoot("vnoposter", "noposter_phone")

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi") fun posterTabletEn() = shoot("v1", "poster_tablet", Window.Tablet)
    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi") fun posterTabletAr() = shoot("v1", "poster_tablet", Window.Tablet)
}
