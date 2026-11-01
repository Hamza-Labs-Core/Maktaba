package com.hamzalabs.maktaba.player.ui.components

import android.content.Context
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHeightIsAtLeast
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.longClick
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import com.hamzalabs.maktaba.core.MemoryTokens
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.SeriesName
import com.hamzalabs.maktaba.core.api.VideoCard
import com.hamzalabs.maktaba.core.jwt
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * What the components do (48.16): a card's words, click and long press for TalkBack and touch;
 * its small poster, decoded at the card's size; a missing poster's fallback; the rail's heading
 * and "See all"; the hero's Resume / Play and Details at 48 dp; a channel's logo or number; and
 * the web's 0.4 s rule for placeholders.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(qualifiers = "w411dp-h891dp-mdpi")
class ComponentsTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()
    @get:Rule val folder = TemporaryFolder()

    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val server = MockWebServer()

    @After
    fun stop() = server.close()

    private fun show(images: ImageLoader = FakePictures.loader(context), content: @Composable () -> Unit) = compose.setContent {
        MaktabaTheme(ThemeMode.Dark) {
            Surface { CompositionLocalProvider(LocalHomeImages provides images) { content() } }
        }
    }

    // ---------- PosterCard ----------

    @Test
    fun aCardIsOneButtonThatSaysItsTitleSeriesAndProgress() {
        var clicks = 0
        var longs = 0
        show {
            PosterCard(
                "The Message", posterUrl = "https://home.test/p1", onClick = { clicks++ }, subtitle = "Films", progress = 0.4f,
                onLongClick = { longs++ }, longClickLabel = "Resume", modifier = Modifier.testTag("card"),
            )
        }
        val card = compose.onNodeWithTag("card")
        card.assertIsDisplayed()
        val node = card.fetchSemanticsNode()
        assertEquals(listOf("The Message, Films, 40% watched"), node.config.getOrNull(SemanticsProperties.ContentDescription))
        assertEquals(androidx.compose.ui.semantics.Role.Button, node.config.getOrNull(SemanticsProperties.Role))
        assertEquals("Resume", node.config.getOrNull(SemanticsActions.OnLongClick)?.label)
        card.performClick()
        card.performTouchInput { longClick() }
        assertEquals(1, clicks)
        assertEquals(1, longs)
        card.assertHeightIsAtLeast(48.dp)
    }

    @Test
    fun aWatchedCardSaysWatchedInsteadOfAProgress() {
        show { PosterCard("Omar", posterUrl = null, onClick = {}, watched = true, progress = 1f, modifier = Modifier.testTag("card")) }
        val words = compose.onNodeWithTag("card").fetchSemanticsNode().config.getOrNull(SemanticsProperties.ContentDescription)
        assertEquals(listOf("Omar, Watched"), words)
    }

    @Test
    fun aCardWithoutAPosterShowsItsTitleOnTheBrandColour() {
        show {
            Column {
                PosterCard("Lion of the Desert", posterUrl = "https://home.test/missing", onClick = {})
                PosterCard("The Night Journey", posterUrl = null, onClick = {})
            }
        }
        compose.waitForIdle()
        compose.onAllNodesWithTag("picture-missing", useUnmergedTree = true).assertCountEquals(2)
    }

    @Test
    fun aVideoCardAsksForTheSmallPosterWithTheTokenAndDecodesItAtTheCardsSize() {
        val access = jwt(System.currentTimeMillis() / 1000 + 86_400)
        val asked = mutableListOf<RecordedRequest>()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(asked) { asked += request }
                return MockResponse.Builder().setHeader("Content-Type", "image/png").setHeader("Cache-Control", "private, max-age=3600")
                    .body(Buffer().write(FakePictures.png("v1", 1280, 720))).build()
            }
        }
        server.start()
        val client = ApiClient(server.url("/"), MemoryTokens(access, "refresh"))
        val memory = ImageCache.memoryCache(context)
        val loader = ImageCache.loader(
            context, client, memory = memory, disk = ImageCache.diskCache(folder.root), online = true, decoders = robolectricDecoder,
        )
        val card = VideoCard(id = "v1", title = "The Message", durationSec = 100.0, positionSec = 25.0, series = SeriesName("s1", "Films"))
        show(loader) { PosterCard(card, client, onClick = {}, width = 160.dp, modifier = Modifier.testTag("card")) }
        compose.waitUntil(10_000) { compose.onAllNodesWithTag("picture-shown", useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }

        val r = synchronized(asked) { asked.single() }
        assertEquals("/api/videos/v1/poster", r.url.encodedPath)
        assertEquals("small", r.url.queryParameter("size"))
        assertEquals("Bearer $access", r.headers["Authorization"])
        assertEquals(
            listOf("The Message, Films, 25% watched"),
            compose.onNodeWithTag("card").fetchSemanticsNode().config.getOrNull(SemanticsProperties.ContentDescription),
        )
        // NFR-4: a 1280 px picture in a 160 dp card (160 px at mdpi) is decoded near the card's size
        val key = memory.keys.single()
        val decoded = memory[key]!!.image
        assertTrue("decoded ${decoded.width}×${decoded.height}", decoded.width <= 320 && decoded.height <= 180)
    }

    // ---------- ProgressStrip ----------

    @Test
    fun theStripIsAProgressBarThatSaysHowFarIn() {
        show { Box { ProgressStrip(0.4f, Modifier.testTag("strip")) } }
        val node = compose.onNodeWithTag("strip").fetchSemanticsNode()
        assertEquals(0.4f, node.config.getOrNull(SemanticsProperties.ProgressBarRangeInfo)!!.current)
        assertEquals("40% watched", node.config.getOrNull(SemanticsProperties.StateDescription))
    }

    @Test
    fun percentagesNeverClaimMoreThanWasWatched() {
        assertEquals(0, watchedPercent(0.004f))
        assertEquals(99, watchedPercent(0.999f))
        assertEquals(100, watchedPercent(1f))
        assertEquals(0, watchedPercent(-1f))
    }

    // ---------- Rail ----------

    @Test
    fun aRailHasAHeadingItsCardsAndSeeAll() {
        var seeAll = 0
        show {
            Rail("Continue Watching", onSeeAll = { seeAll++ }) {
                items(3) { n -> PosterCard("Lesson ${n + 1}", posterUrl = null, onClick = {}) }
            }
        }
        compose.onNode(isHeading() and androidx.compose.ui.test.hasText("Continue Watching")).assertIsDisplayed()
        compose.onNodeWithContentDescription("Lesson 1").assertIsDisplayed()
        compose.onNodeWithText("See all").assertHeightIsAtLeast(48.dp).performClick()
        assertEquals(1, seeAll)
    }

    // ---------- Hero ----------

    @Test
    fun theHeroOffersResumeOrPlayAndDetails() {
        var play = 0
        var details = 0
        var resume by mutableStateOf(true)
        show { Hero("Lion of the Desert", backdropUrl = "https://home.test/b", onPlay = { play++ }, onDetails = { details++ }, eyebrow = "Featured", resume = resume) }
        compose.onNode(isHeading() and androidx.compose.ui.test.hasText("Lion of the Desert")).assertIsDisplayed()
        compose.onNodeWithText("Resume").assertIsDisplayed()
        compose.onNodeWithTag("hero-play").assertHeightIsAtLeast(48.dp).performClick()
        compose.onNodeWithTag("hero-details").assertHeightIsAtLeast(48.dp).performClick()
        assertEquals(1, play)
        assertEquals(1, details)
        resume = false
        compose.onNodeWithText("Play").assertIsDisplayed()
    }

    // ---------- ChannelLogo ----------

    @Test
    fun aChannelSaysItsNameAndShowsItsNumberWithoutALogo() {
        show {
            Column {
                ChannelLogo("Al Jazeera", logoUrl = null, number = "12")
                ChannelLogo("Quran TV", logoUrl = "https://home.test/missing-logo")
                ChannelLogo("Sunna", logoUrl = "https://home.test/logo-sunna")
            }
        }
        compose.onNodeWithContentDescription("Al Jazeera").assertIsDisplayed()
        compose.onNodeWithContentDescription("Quran TV").assertIsDisplayed()
        compose.onNodeWithText("12", useUnmergedTree = true).assertIsDisplayed()
        compose.onNodeWithText("QT", useUnmergedTree = true).assertIsDisplayed()
        compose.onAllNodesWithTag("picture-shown", useUnmergedTree = true).assertCountEquals(1)
    }

    @Test
    fun initialsTakeTheFirstLetterOfTwoWords() {
        assertEquals("AJ", initials("Al Jazeera"))
        assertEquals("قا", initials("قناة الفجر"))
        assertEquals("S", initials("sunna"))
        assertEquals("?", initials(" "))
    }

    // ---------- Shimmer: the web's 0.4 s rule ----------

    @Test
    fun aPlaceholderShowsOnlyAfterFourHundredMilliseconds() {
        compose.mainClock.autoAdvance = false
        show { Loading(true) { RailPlaceholder(animate = false) } }
        compose.mainClock.advanceTimeByFrame()
        // the region says it is loading from the start; the shapes wait
        compose.onNode(hasContentDescription("Loading…")).assertExists()
        compose.onAllNodesWithTag("placeholder").assertCountEquals(0)
        compose.mainClock.advanceTimeBy(350)
        compose.onAllNodesWithTag("placeholder").assertCountEquals(0)
        compose.mainClock.advanceTimeBy(100)
        compose.onAllNodesWithTag("placeholder").assertCountEquals(1)
    }

    @Test
    fun aQuickLoadNeverFlashesAPlaceholder() {
        compose.mainClock.autoAdvance = false
        var loading by mutableStateOf(true)
        show { Loading(loading) { RailPlaceholder(animate = false) }; if (!loading) Text("Rows") }
        compose.mainClock.advanceTimeBy(300)
        loading = false
        compose.mainClock.advanceTimeBy(500)
        compose.onAllNodesWithTag("placeholder").assertCountEquals(0)
        compose.onAllNodesWithTag("loading").assertCountEquals(0)
        compose.onNodeWithText("Rows").assertExists()
    }

    @Test
    fun thePlaceholderMatchesTheWeb() = assertEquals(400L, PLACEHOLDER_DELAY_MS)
}
