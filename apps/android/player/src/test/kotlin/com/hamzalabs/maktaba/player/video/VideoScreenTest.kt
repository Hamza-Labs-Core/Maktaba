package com.hamzalabs.maktaba.player.video

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.activity.ComponentActivity
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.player.MainActivity
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.ui.components.PictureState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.util.Locale

/**
 * A video's page (48.28, as 48.19; FR-18, FR-9) against the fake home: Resume from
 * `playback_state` and Play from start, chapters, Favorite and Mark watched kept across a reload,
 * a 404's "not available", the poster at its own shape and size, and `maktaba://video/{id}`.
 * The player is a stub that says what it was asked to play.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp-xxhdpi")
class VideoScreenTest {
    @get:Rule(order = 0) val fake = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val videos by lazy { VideoHome(fake) }
    private val pictures by lazy { VideoPictures(compose.activity) }

    private fun open(id: String, window: Window = Window.Phone, driver: LinkDriver = LinkDriver()) {
        videos // the fixtures, before the page asks
        compose.setVideoShell(fake, pictures, VideoLink.of(id), window, driver)
    }

    private fun waitForTag(tag: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty() }

    private fun waitForText(text: String) =
        compose.waitUntil(5_000) { compose.onAllNodes(hasText(text)).fetchSemanticsNodes().isNotEmpty() }

    private fun back() = compose.activityRule.scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }

    private fun posterState(state: PictureState) =
        compose.waitUntil(5_000) {
            compose.onAllNodes(hasTestTag("video-poster") and SemanticsMatcher.expectValue(PosterStateKey, state)).fetchSemanticsNodes().isNotEmpty()
        }

    private fun count(path: String) = fake.paths.count { it == path }

    // ---------- Resume, Play from start, Play ----------

    @Test
    fun resumeShowsThePositionFromPlaybackStateAndPlayFromStartOpensAtZero() {
        open("v1")
        waitForTag("video-resume")
        compose.onNodeWithText("Resume from 12:34").assertIsDisplayed()
        compose.onNodeWithTag("video-resume").performClick()
        waitForTag("watch")
        compose.onNodeWithText("watch v1 754").assertIsDisplayed()

        back()
        waitForTag("video-play-start")
        compose.onNodeWithTag("video-play-start").performClick()
        waitForTag("watch")
        compose.onNodeWithText("watch v1 0").assertIsDisplayed()
    }

    @Test
    fun aVideoNeverStartedOffersPlay() {
        open("v2")
        waitForTag("video-play")
        assertTrue(compose.onAllNodes(hasTestTag("video-resume")).fetchSemanticsNodes().isEmpty())
        compose.onNodeWithTag("video-play").performClick()
        waitForTag("watch")
        compose.onNodeWithText("watch v2 0").assertIsDisplayed()
    }

    // ---------- the page's parts ----------

    @Test
    fun thePageShowsTheSeriesEpisodeFactsDescriptionTranscriptAndNextEpisodes() {
        open("v1")
        waitForText("Omar · Season 1, Episode 2")
        compose.onNodeWithTag("video-title").assertIsDisplayed()
        compose.onNodeWithText("The Night Journey", useUnmergedTree = true).assertIsDisplayed()
        compose.onNodeWithText("45:00 · Mar 11, 2024").assertIsDisplayed()
        compose.onNodeWithText("A night of travel, told slowly.").assertIsDisplayed()
        compose.onNodeWithText("It was a night like no other.").performScrollTo().assertIsDisplayed()
        compose.onNodeWithTag("video-next").performScrollTo().assertIsDisplayed()
        // only what follows this episode
        compose.onNode(hasContentDescription("The Migration", substring = true)).assertExists()
        assertTrue(compose.onAllNodes(hasContentDescription("The Call", substring = true)).fetchSemanticsNodes().isEmpty())
        assertTrue(fake.paths.toString(), "/api/series/s1/episodes" in fake.paths)
    }

    @Test
    fun aChapterOpensThePlayerAtItsTime() {
        open("v1")
        waitForTag("chapter-2")
        compose.onNodeWithTag("chapter-2").performScrollTo().performClick()
        waitForTag("watch")
        compose.onNodeWithText("watch v1 600").assertIsDisplayed()
    }

    @Test
    fun favoriteAndMarkWatchedToggleAndPersistOnReload() {
        open("v1")
        waitForTag("video-favorite")
        compose.onNodeWithTag("video-favorite").assertIsNotSelected().performClick()
        compose.waitUntil(5_000) { videos.favorite }
        compose.onNodeWithTag("video-favorite").assertIsSelected()
        val favorite = fake.requests.last { it.url.encodedPath == "/api/me/favorites" }
        assertEquals("POST", favorite.method)
        assertEquals("""{"video_ids":["v1"],"favorite":true}""", favorite.body?.utf8())

        compose.onNodeWithTag("video-watched").assertIsNotSelected().performClick()
        compose.waitUntil(5_000) { videos.watched }
        compose.onNodeWithTag("video-watched").assertIsSelected()
        // watched: the home starts it over, so Play, not Resume
        waitForTag("video-play")
        assertTrue(compose.onAllNodes(hasTestTag("video-resume")).fetchSemanticsNodes().isEmpty())

        // reload: away (the player) and back reads the home again
        val reads = count("/api/me/videos/v1")
        compose.onNodeWithTag("video-play").performClick()
        waitForTag("watch")
        back()
        compose.waitUntil(5_000) { count("/api/me/videos/v1") > reads }
        waitForTag("video-favorite")
        compose.onNodeWithTag("video-favorite").assertIsSelected()
        compose.onNodeWithTag("video-watched").assertIsSelected()

        // and off again
        compose.onNodeWithTag("video-favorite").performClick()
        compose.waitUntil(5_000) { !videos.favorite }
        compose.onNodeWithTag("video-favorite").assertIsNotSelected()
    }

    @Test
    fun aSaveTheHomeRefusesPutsTheToggleBack() {
        videos
        fake.answer("/api/me/favorites") { FakeHome.json("""{"title":"boom"}""", 500) }
        compose.setVideoShell(fake, pictures, VideoLink.of("v1"))
        waitForTag("video-favorite")
        compose.onNodeWithTag("video-favorite").performClick()
        waitForText("Couldn't save that. Try again.")
        compose.onNodeWithTag("video-favorite").assertIsNotSelected()
    }

    // ---------- not available ----------

    @Test
    fun a404SaysNotAvailable() {
        open("hidden")
        waitForTag("video-not-available")
        compose.onNodeWithText("This video isn't available").assertIsDisplayed()
        assertTrue(fake.paths.toString(), "/api/videos/hidden" in fake.paths)
    }

    // ---------- the poster ----------

    @Test
    fun onAPhoneThePosterIsTheSmallCopyAtTheTopAt16by9() {
        open("v1")
        posterState(PictureState.Shown)
        assertTrue(pictures.urls.toString(), pictures.urls.any { it.endsWith("/api/videos/v1/poster?size=small") })
        val poster = compose.onNodeWithTag("video-poster").fetchSemanticsNode().boundsInRoot
        val title = compose.onNodeWithTag("video-title").fetchSemanticsNode().boundsInRoot
        assertTrue("poster above the title", poster.bottom <= title.top)
        assertEquals(16f / 9f, poster.width / poster.height, 0.02f)
    }

    @Test
    @Config(qualifiers = "w1280dp-h800dp-xhdpi")
    fun onATabletThePosterIsTheFullOne() {
        open("v1", Window.Tablet)
        posterState(PictureState.Shown)
        assertTrue(pictures.urls.toString(), pictures.urls.any { it.endsWith("/api/videos/v1/poster") })
        // the page's own poster; the next episodes' cards keep the small copy
        assertFalse(pictures.urls.toString(), pictures.urls.any { "/v1/poster?size=small" in it })
    }

    @Test
    fun aPortraitPosterKeepsItsShape() {
        open("vportrait")
        posterState(PictureState.Shown)
        val poster = compose.onNodeWithTag("video-poster").fetchSemanticsNode().boundsInRoot
        assertEquals(9f / 16f, poster.width / poster.height, 0.02f)
    }

    @Test
    fun withNoPosterAPlaceholderOfThePostersSizeShowsTheTitle() {
        open("vnoposter")
        posterState(PictureState.Missing)
        val poster = compose.onNodeWithTag("video-poster").fetchSemanticsNode().boundsInRoot
        assertEquals(16f / 9f, poster.width / poster.height, 0.02f)
        compose.onNode(hasTestTag("video-poster-missing")).assertIsDisplayed()
        compose.onNode(hasText("Lesson Without a Poster") and hasAnyAncestor(hasTestTag("video-poster-missing")), useUnmergedTree = true).assertIsDisplayed()
    }

    // ---------- the deep link ----------

    @Test
    fun aVideoLinkIsParsedAndOtherLinksAreNot() {
        assertEquals(Route.Video("v1"), VideoLink.route("maktaba://video/v1"))
        assertEquals(Route.Video("v1"), VideoLink.route("  MAKTABA://Video/v1/ "))
        assertEquals(Route.Video("a b+c"), VideoLink.route(VideoLink.of("a b+c")))
        assertNull(VideoLink.route(null))
        assertNull(VideoLink.route("maktaba://video/"))
        assertNull(VideoLink.route("maktaba://video/v1/extra"))
        assertNull(VideoLink.route("maktaba://series/s1"))
        assertNull(VideoLink.route("maktaba://connect?u=http://192.168.1.5:8088"))
        assertNull(VideoLink.route("https://maktaba.dev/video/v1"))
    }

    @Test
    fun theManifestSendsAVideoLinkToTheApp() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse("maktaba://video/v1"))
            .addCategory(Intent.CATEGORY_BROWSABLE)
            .setPackage(context.packageName)
        val found = context.packageManager.queryIntentActivities(intent, 0).map { it.activityInfo.name }
        assertTrue(found.toString(), MainActivity::class.java.name in found)
    }

    @Test
    fun aLinkWhileTheAppIsOpenOpensThatVideo() {
        val driver = LinkDriver()
        open("v2", driver = driver)
        waitForText("Andalusia, a History")
        compose.runOnIdle { driver.link = VideoLink.route("maktaba://video/hidden") }
        waitForTag("video-not-available")
        // Back returns to the video the first link opened
        back()
        waitForText("Andalusia, a History")
    }

    @Test
    fun datesAreWrittenInTheAppsLanguage() {
        assertEquals("Mar 11, 2024", formatDate("2024-03-11T09:00:00Z", Locale.US))
        assertEquals("Mar 11, 2024", formatDate("2024-03-11", Locale.US))
        assertNull(formatDate("soon", Locale.US))
        assertNull(formatDate(null, Locale.US))
    }
}
