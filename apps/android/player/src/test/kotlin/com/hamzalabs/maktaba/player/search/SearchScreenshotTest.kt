package com.hamzalabs.maktaba.player.search

import android.content.Context
import androidx.activity.ComponentActivity
import coil3.ImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.request.SuccessResult
import kotlinx.coroutines.Dispatchers
import okhttp3.HttpUrl.Companion.toHttpUrl
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.setShell
import com.hamzalabs.maktaba.player.shell.AppScreens
import com.hamzalabs.maktaba.player.shell.PlayerSection
import com.hamzalabs.maktaba.player.ui.components.FakePictures
import com.hamzalabs.maktaba.player.ui.components.LocalHomeImages
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Search's screenshots (NFR-20): dark, English and Arabic, phone and tablet, against the fake
 * home: the recent searches before a search, and the results (Titles, In the transcript) after
 * one. Posters come from [FakePictures]. `recordRoborazziPhoneDebug` writes the references under
 * `src/test/screenshots`; CI's `verifyRoborazziPhoneDebug` fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class SearchScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun shoot(window: Window, name: String, results: Boolean) {
        val ar = compose.activity.resources.configuration.locales[0].language == "ar"
        val l = if (ar) SearchFixtures.AR else SearchFixtures.EN
        SearchFixtures.install(home, l)
        compose.setShell(home, sections = PlayerSection.enabled, window = window, screens = { route, nav ->
            val images = remember { pathPictures(compose.activity) }
            CompositionLocalProvider(LocalHomeImages provides images) { AppScreens(route, nav) }
        })
        compose.onNodeWithTag("section-Search").performClick()
        if (results) {
            val field = compose.onNode(hasSetTextAction())
            field.performClick()
            field.performTextInput(l.query)
            field.performImeAction()
            compose.waitUntil(5_000) { compose.onAllNodesWithTag("passage-v4-0").fetchSemanticsNodes().isNotEmpty() }
        } else {
            compose.waitUntil(5_000) { compose.onAllNodesWithTag("recent").fetchSemanticsNodes().isNotEmpty() }
        }
        compose.waitForIdle()
        val lang = if (ar) "ar" else "en"
        compose.onRoot().captureRoboImage("src/test/screenshots/search_${name}_dark_$lang.png", roborazziOptions = options)
    }

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun phoneResultsEnglish() = shoot(Window.Phone, "results_phone", results = true)

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun phoneResultsArabic() = shoot(Window.Phone, "results_phone", results = true)

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun phoneRecentEnglish() = shoot(Window.Phone, "recent_phone", results = false)

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun phoneRecentArabic() = shoot(Window.Phone, "recent_phone", results = false)

    @Test @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun tabletResultsEnglish() = shoot(Window.Tablet, "results_tablet", results = true)

    @Test @Config(qualifiers = "ar-w1280dp-h800dp-xhdpi")
    fun tabletResultsArabic() = shoot(Window.Tablet, "results_tablet", results = true)

    /**
     * [FakePictures]' pictures keyed by the URL's path: the fake home's port changes every run,
     * and a picture chosen by the whole URL would change colour with it.
     */
    private fun pathPictures(context: Context): ImageLoader = ImageLoader.Builder(context)
        .memoryCache(null)
        .diskCache(null)
        .coroutineContext(Dispatchers.Unconfined)
        .interceptorCoroutineContext(Dispatchers.Unconfined)
        .components {
            add(
                Interceptor { chain ->
                    val path = chain.request.data.toString().toHttpUrl().encodedPath
                    SuccessResult(FakePictures.picture(path).asImage(), chain.request, DataSource.MEMORY)
                },
            )
        }
        .build()

    private companion object {
        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
