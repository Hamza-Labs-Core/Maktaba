package com.hamzalabs.maktaba.player.search

import android.content.Context
import androidx.activity.ComponentActivity
import coil3.ImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.request.SuccessResult
import com.hamzalabs.maktaba.player.harness.settledUntil
import kotlinx.coroutines.Dispatchers
import okhttp3.HttpUrl.Companion.toHttpUrl
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
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
import com.hamzalabs.maktaba.core.AiStreams
import mockwebserver3.MockResponse
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Search's screenshots (NFR-20): dark, English and Arabic, phone and tablet, against the fake
 * home: the recent searches before a search, and the results (Titles, In the transcript) after
 * one, and AI mode's answer (story 49.14). Posters come from [FakePictures]. `recordRoborazziDebug` writes the references under
 * `src/test/screenshots`; CI's `verifyRoborazziDebug` fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class SearchScreenshotTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun shoot(window: Window, name: String, results: Boolean, refined: Boolean = false, ai: Boolean = false) {
        val ar = compose.activity.resources.configuration.locales[0].language == "ar"
        val l = if (ar) SearchFixtures.AR else SearchFixtures.EN
        SearchFixtures.install(home, l)
        // story 49.14: AI mode's answer, the passages and the sentences citing them
        home.answer("/api/search/ai") {
            val (passages, sentences) = if (ar) AiStreams.AR_PASSAGES to AiStreams.AR_SENTENCES else AiStreams.EN_PASSAGES to AiStreams.EN_SENTENCES
            MockResponse.Builder().code(200).setHeader("Content-Type", "text/event-stream").body(AiStreams.answer(l.query, passages, sentences)).build()
        }
        compose.setShell(home, sections = PlayerSection.enabled, window = window, screens = { route, nav ->
            val images = remember { pathPictures(compose.activity) }
            CompositionLocalProvider(LocalHomeImages provides images) { AppScreens(route, nav) }
        })
        compose.onNodeWithTag("section-Search").performClick()
        if (results) {
            val field = compose.onNode(hasSetTextAction())
            field.performClick()
            // refined (story 49.11): a misspelling, so Did you mean shows, narrowed to a library and a length
            field.performTextInput(if (refined) l.misspelt else l.query)
            field.performImeAction()
            compose.settledUntil(5_000) { compose.onAllNodesWithTag("title-v1").fetchSemanticsNodes().isNotEmpty() }
            if (refined) {
                compose.onNodeWithTag("search-filters").performClick()
                compose.settledUntil(5_000) { compose.onAllNodesWithTag("facet-library-${SearchFixtures.LIB}").fetchSemanticsNodes().isNotEmpty() }
                compose.onNodeWithTag("facet-library-${SearchFixtures.LIB}").performClick()
                compose.settledUntil(5_000) { home.paths.count { it == "/api/search" } == 2 }
                compose.onNodeWithTag("facet-duration-20m_60m").performScrollTo().performClick()
                compose.settledUntil(5_000) { home.paths.count { it == "/api/search" } == 3 }
                compose.onNodeWithTag("facets-done").performScrollTo().performClick()
                compose.settledUntil(5_000) { compose.onAllNodesWithTag("search-facets").fetchSemanticsNodes().isEmpty() }
                compose.settledUntil(5_000) { compose.onAllNodesWithTag("active-duration-20m_60m").fetchSemanticsNodes().isNotEmpty() }
                // the narrowed answer is in: no progress line under the bar
                compose.settledUntil(5_000) { compose.onAllNodesWithTag("search-refining").fetchSemanticsNodes().isEmpty() }
            } else {
                compose.settledUntil(5_000) { compose.onAllNodesWithTag("passage-v4-0").fetchSemanticsNodes().isNotEmpty() }
            }
            if (ai) {
                compose.onNodeWithTag("tab-ai").performClick()
                compose.settledUntil(5_000) { compose.onAllNodesWithTag("ai-sentence-1-1").fetchSemanticsNodes().isNotEmpty() }
            }
        } else {
            compose.settledUntil(5_000) { compose.onAllNodesWithTag("recent").fetchSemanticsNodes().isNotEmpty() }
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

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun phoneRefinedEnglish() = shoot(Window.Phone, "refined_phone", results = true, refined = true)

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun phoneRefinedArabic() = shoot(Window.Phone, "refined_phone", results = true, refined = true)

    @Test @Config(qualifiers = "w411dp-h891dp-xxhdpi")
    fun phoneAiModeEnglish() = shoot(Window.Phone, "ai_phone", results = true, ai = true)

    @Test @Config(qualifiers = "ar-w411dp-h891dp-xxhdpi")
    fun phoneAiModeArabic() = shoot(Window.Phone, "ai_phone", results = true, ai = true)

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
