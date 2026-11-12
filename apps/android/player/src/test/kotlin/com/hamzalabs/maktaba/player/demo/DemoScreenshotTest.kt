package com.hamzalabs.maktaba.player.demo

import android.content.Context
import android.graphics.BitmapFactory
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.request.ErrorResult
import coil3.request.ImageResult
import coil3.request.SuccessResult
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.DemoLibrary
import com.hamzalabs.maktaba.core.browse.KEY_CONTINUE
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.harness.settled
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.shell.PlayerShell
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.LocalHomeImages
import com.hamzalabs.maktaba.player.ui.components.PictureState
import com.hamzalabs.maktaba.player.video.PosterStateKey
import com.hamzalabs.maktaba.player.video.VideoLink
import kotlinx.coroutines.Dispatchers
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import org.junit.After
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.IOException
import java.util.TimeZone

/**
 * The Android phone's product screenshots for maktaba.tv (story 60.09): Home and a film's page
 * (Sintel), dark, in English and Arabic, on the demo library (`tests/fixtures/demo-library`),
 * every answer and picture read from its route table, its posters the films' own art. Written
 * to `src/test/screenshots/demo/android-phone-<screen>-<lang>.png`; CI's `verifyRoborazziDebug`
 * fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class DemoScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome { DemoLibrary.load(lang()) }
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val zone: TimeZone = TimeZone.getDefault()

    @After
    fun restoreZone() = TimeZone.setDefault(zone)

    private fun lang() = if (RuntimeEnvironment.getQualifiers().split('-').contains("ar")) "ar" else "en"

    /** The app on the demo home, opened on Home and then on [link]'s screen when there is one. */
    private fun open(link: String? = null) {
        // times as the library's clock zone shows them: the same on every machine
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
        val demo = requireNotNull(home.demo)
        val activity = compose.activity
        compose.setContent {
            var pending by remember { mutableStateOf(VideoLink.route(link)) }
            val pictures = remember { demoPictures(activity, demo) }
            MaktabaTheme(ThemeMode.Dark) {
                Surface(Modifier.fillMaxSize()) {
                    CompositionLocalProvider(LocalPlayerHome provides home.home, LocalHomeImages provides pictures) {
                        PlayerShell(
                            sections = PlayerSection.enabled,
                            adaptiveInfo = Window.Phone.info(),
                            screens = { route, nav -> AppScreens(route, nav) },
                            link = pending,
                            onLinkOpened = { pending = null },
                        )
                    }
                }
            }
        }
    }

    /** Every picture on screen has arrived (none still loading), and at least [atLeast] are shown. */
    private fun picturesShown(atLeast: Int): Boolean {
        val loading = compose.onAllNodes(hasTestTag("picture-loading"), useUnmergedTree = true).fetchSemanticsNodes().size
        val shown = compose.onAllNodes(hasTestTag("picture-shown"), useUnmergedTree = true).fetchSemanticsNodes().size
        return loading == 0 && shown >= atLeast
    }

    private fun shoot(screen: String) {
        compose.waitForIdle()
        compose.onRoot().captureRoboImage("src/test/screenshots/demo/android-phone-$screen-${lang()}.png", roborazziOptions = options)
    }

    private fun homeScreen() {
        open()
        compose.settled(home, "Home's Continue Watching") {
            compose.onAllNodes(hasTestTag("row-$KEY_CONTINUE")).fetchSemanticsNodes().isNotEmpty()
        }
        compose.settled(home, "Home's posters") { picturesShown(atLeast = 3) }
        shoot("home")
    }

    private fun filmScreen() {
        open(VideoLink.of(SINTEL))
        val title = if (lang() == "ar") "سينتل" else "Sintel"
        compose.settled(home, "Sintel's page") { compose.onAllNodes(hasText(title), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        compose.settled(home, "Sintel's poster") {
            compose.onAllNodes(hasTestTag("video-poster") and SemanticsMatcher.expectValue(PosterStateKey, PictureState.Shown)).fetchSemanticsNodes().isNotEmpty()
        }
        shoot("film")
    }

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun homeEnglish() = homeScreen()

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun homeArabic() = homeScreen()

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun filmEnglish() = filmScreen()

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun filmArabic() = filmScreen()

    private companion object {
        const val SINTEL = "v-sintel"

        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}

/**
 * The demo library's pictures: each URL's path answered from [demo]'s route table (the film's
 * own JPEG), decoded as the app would; a path the table has no picture for is a 404. Nothing is
 * cached, so every shot decodes the same bytes.
 */
fun demoPictures(context: Context, demo: DemoLibrary): ImageLoader = ImageLoader.Builder(context)
    .memoryCache(null)
    .diskCache(null)
    .coroutineContext(Dispatchers.Unconfined)
    .interceptorCoroutineContext(Dispatchers.Unconfined)
    .components {
        add(
            Interceptor { chain ->
                val path = chain.request.data.toString().toHttpUrlOrNull()?.encodedPath
                val answer = path?.let { demo.answer("GET", it) }?.takeIf { it.status == 200 && it.contentType.startsWith("image/") }
                val bitmap = answer?.let { BitmapFactory.decodeByteArray(it.body, 0, it.body.size) }
                val result: ImageResult = if (bitmap != null) {
                    SuccessResult(bitmap.asImage(), chain.request, DataSource.MEMORY)
                } else {
                    ErrorResult(null, chain.request, IOException("404 $path"))
                }
                result
            },
        )
    }
    .build()
