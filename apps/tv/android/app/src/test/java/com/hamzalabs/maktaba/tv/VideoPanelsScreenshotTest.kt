package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performScrollTo
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.SurfaceDefaults
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.api.PanelText
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.data.TvApi
import com.hamzalabs.maktaba.tv.ui.screens.VideoScreen
import com.hamzalabs.maktaba.tv.ui.screens.library.LocalTvNav
import com.hamzalabs.maktaba.tv.ui.screens.library.TvNav
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.util.Base64
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * Story 54.20 test T05 on Android TV: the video page's plugin panel, the TV's subset (a
 * key-value row, badges and a marker; never chips), drawn on a 1080p TV in the TV's one theme
 * (dark), in English and in Arabic (right to left), checked against the references in
 * `src/test/screenshots`. One value is longer than the text rules allow: it ends in "…" at
 * [PanelText.MAX_SHOWN] characters and wraps inside the panel. The page as it opens, and
 * scrolled to the marker (the remote's focus scrolls the page there): the panel is never cut.
 * The panel is the phones' core composable: it takes the TV's dark colours, never Material's
 * light defaults.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class VideoPanelsScreenshotTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private lateinit var server: MockWebServer

    private class Tokens(var a: String?, var r: String?) : TokenStore {
        override val access get() = a
        override val refresh get() = r
        override fun save(tokens: AuthTokens) { a = tokens.accessToken; r = tokens.refreshToken }
        override fun clear() { a = null; r = null }
    }

    private val token: String = Base64.getUrlEncoder().withoutPadding().let { enc ->
        enc.encodeToString("""{"alg":"RS256"}""".toByteArray()) + "." +
            enc.encodeToString("""{"exp":${System.currentTimeMillis() / 1000 + 86_400}}""".toByteArray()) + ".sig"
    }

    private fun json(body: String) = MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(body).build()

    /** The plugin's panel in the language the page asked for (`lang=`), as a home answers. */
    private fun panel(ar: Boolean): String {
        val t = if (ar) AR else EN
        return """{"panels":[{"plugin_id":"scenes","plugin":"${t.plugin}","title":"${t.title}","blocks":[""" +
            """{"type":"kv","value":[{"label":"${t.era}","value":"${t.eraValue}"},{"label":"${t.notes}","value":"${t.long}"}]},""" +
            """{"type":"badges","value":[{"text":"${t.badge1}"},{"text":"${t.badge2}"}]},""" +
            """{"type":"chips","value":["${t.chip}"]},""" +
            """{"type":"markers","value":[{"t_ms":754000,"label":"${t.marker}"}]}]}]}"""
    }

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when (request.url.encodedPath) {
                "/api/videos/v1" -> json("""{"id":"v1","library_id":"l1","filename":"v1.mkv","title":"The Night Journey","duration_sec":2700,"state":"ready"}""")
                "/api/panels" -> json(panel(ar = request.url.queryParameter("lang") == "ar"))
                else -> json("{}")
            }
        }
        server.start()
    }

    @After
    fun stop() = server.close()

    private fun texts(tag: String? = null): List<String> =
        compose.onAllNodes(if (tag == null) hasText("", substring = true) else hasTestTag(tag), useUnmergedTree = true)
            .fetchSemanticsNodes().flatMap { n -> n.config.getOrNull(SemanticsProperties.Text).orEmpty().map { it.text } }

    private fun shoot(t: Words, name: String) {
        val api = TvApi(ApiClient(server.url("/"), Tokens(token, "r1")))
        compose.setContent {
            // as MainActivity lays it out
            MaktabaTvTheme {
                Surface(Modifier.fillMaxSize(), colors = SurfaceDefaults.colors(containerColor = MaterialTheme.colorScheme.background)) {
                    CompositionLocalProvider(LocalTvNav provides TvNav(play = { _, _, _ -> })) {
                        VideoScreen(api, "v1", onPlay = { _, _ -> }, signedOut = {}, panels = true)
                    }
                }
            }
        }
        compose.waitUntil(5_000) { compose.waitForIdle(); texts().any { it.contains(t.eraValue) } }
        val shown = texts()

        // the heading in the page's language, and the TV's subset only
        assertTrue("the heading: $shown", shown.contains(compose.activity.getString(CoreR.string.plugin_panels_title)))
        for (w in listOf(t.era, t.eraValue, t.badge1, t.badge2, t.marker)) assertTrue("$w shows: $shown", shown.any { it.contains(w) })
        assertFalse("no chips on the TV: $shown", shown.any { it.contains(t.chip) })

        // the long value cut by the text rules, with an ellipsis
        val long = shown.single { it.startsWith(t.long.take(20)) }
        assertEquals(PanelText.MAX_SHOWN, long.length)
        assertTrue("ends in an ellipsis: $long", long.endsWith("…"))

        // laid out from the reading side
        val root = compose.onRoot().fetchSemanticsNode().size
        assertEquals("a 1080p TV", 1920 to 1080, root.width to root.height)
        val panels = compose.onAllNodes(hasTestTag("plugin-panels"), useUnmergedTree = true).fetchSemanticsNodes().single().boundsInRoot
        val era = compose.onAllNodes(hasText(t.era), useUnmergedTree = true).fetchSemanticsNodes().single().boundsInRoot
        if (t === AR) {
            assertTrue("Arabic starts on the right: $era in $panels", panels.right - era.right < era.left - panels.left)
        } else {
            assertTrue("English starts on the left: $era in $panels", era.left - panels.left < panels.right - era.right)
        }
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)

        // the page scrolls to the marker, below the long value: the panel's end on the screen
        val marker = hasText(t.marker, substring = true) and hasClickAction()
        compose.onNode(marker).performScrollTo()
        compose.waitForIdle()
        val m = compose.onAllNodes(marker).fetchSemanticsNodes().single().boundsInRoot
        assertTrue("the marker on the screen: $m", m.top >= 0f && m.bottom <= root.height && m.height > 0f)
        val badge = compose.onAllNodes(hasText(t.badge1), useUnmergedTree = true).fetchSemanticsNodes().single().boundsInRoot
        assertTrue("the badges on the screen: $badge", badge.top >= 0f && badge.bottom <= root.height && badge.height > 0f)
        compose.onRoot().captureRoboImage("src/test/screenshots/${name.replace("_dark_", "_end_dark_")}.png", roborazziOptions = options)
    }

    /** The phones' core composables (Material 3) drawn on the TV take its dark palette. */
    @Test
    fun theCorePanelTakesTheTvsDarkColours() {
        var surface = Color.Unspecified
        var text = Color.Unspecified
        var muted = Color.Unspecified
        compose.setContent {
            MaktabaTvTheme {
                surface = androidx.compose.material3.MaterialTheme.colorScheme.surface
                text = androidx.compose.material3.MaterialTheme.colorScheme.onSurface
                muted = androidx.compose.material3.MaterialTheme.colorScheme.onSurfaceVariant
            }
        }
        compose.waitForIdle()
        assertEquals(MaktabaPalette.Surface, surface)
        assertEquals(MaktabaPalette.Text, text)
        assertEquals(MaktabaPalette.Muted, muted)
    }

    @Test @Config(qualifiers = TV)
    fun theTvSubsetInEnglish() = shoot(EN, "video_panels_tv_dark_en")

    @Test @Config(qualifiers = "ar-$TV")
    fun theTvSubsetInArabic() = shoot(AR, "video_panels_tv_dark_ar")

    /** The panel's words, as the plugin writes them in each language. */
    private class Words(
        val plugin: String,
        val title: String,
        val era: String,
        val eraValue: String,
        val notes: String,
        val long: String,
        val badge1: String,
        val badge2: String,
        val chip: String,
        val marker: String,
    )

    private companion object {
        const val TV = "w960dp-h540dp-land-television-xhdpi"
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))

        val EN = Words(
            plugin = "Scenes", title = "Scenes", era = "Era", eraValue = "1920s", notes = "Notes",
            long = "A journey north through the desert by night, ".repeat(6).trim(),
            badge1 = "Restored", badge2 = "HD", chip = "History", marker = "The road north",
        )
        val AR = Words(
            plugin = "المشاهد", title = "المشاهد", era = "الحقبة", eraValue = "العشرينيات", notes = "ملاحظات",
            long = "رحلة ليلية نحو الشمال عبر الصحراء الواسعة، ".repeat(6).trim(),
            badge1 = "مرمّم", badge2 = "عالي الدقة", chip = "تاريخ", marker = "الطريق شمالاً",
        )
    }
}
