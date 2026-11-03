package com.hamzalabs.maktaba.remote

import androidx.activity.ComponentActivity
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToIndex
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * Switching places, and folding, shows the page read last at once, where it was scrolled to,
 * while the home is asked again (the owner: "pages should be cached while waiting updates when
 * switching"); the answer then updates it in place.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w344dp-h882dp-420dpi")
class RemoteCacheTest {
    @get:Rule(order = 0) val home = FakeRemoteHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun issues(count: Int) = """{"groups":[${(0 until 40).joinToString(",") { i ->
        """{"stage":"stage$i","cause":"nas_timeout","label":"NAS read timed out","count":${if (i == 20) count else 1},"action":"retry","sample_message":"read $i"}"""
    }}]}"""

    /** The second and later reads of Needs attention wait for [release]. */
    private val release = CountDownLatch(1)
    private var reads = 0

    private fun answerSlowly(after: Int) {
        home.answer("/api/issues") {
            reads++
            if (reads > 1) release.await(20, TimeUnit.SECONDS)
            FakeRemoteHome.json(issues(if (reads > 1) after else 7))
        }
    }

    /** A read still held is let go, so the fake home can close. */
    @After fun letGo() = release.countDown()

    private fun shows(text: String) = compose.onAllNodes(hasText(text, substring = true), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun anotherPlaceAndBackShowsTheLastPageWhereItWasThenItsUpdate() {
        answerSlowly(after = 99)
        compose.setContent { RemoteForTest(home, RemoteWindow.FoldCover.info) }
        compose.settledUntil { compose.has("group-stage0:nas_timeout") }
        compose.onNodeWithTag("rows").performScrollToIndex(20)
        compose.settledUntil { compose.has("group-stage20:nas_timeout") }

        compose.onNodeWithTag("place-Queue").performClick()
        compose.waitForIdle()
        compose.onNodeWithTag("place-Attention").performClick()
        compose.waitForIdle()

        // at once: the rows read last, scrolled where they were, the bar saying they are read again
        assertTrue("the kept rows", compose.has("group-stage20:nas_timeout"))
        assertFalse("not from the top", compose.has("group-stage0:nas_timeout"))
        assertFalse("never Loading", shows(compose.activity.getString(R.string.loading)))
        assertTrue("the revalidating bar", compose.has("revalidating"))
        assertTrue(shows("7 NAS read timed out"))

        // the answer: in place, at the same row
        release.countDown()
        compose.settledUntil { shows("99 NAS read timed out") }
        assertTrue(compose.has("group-stage20:nas_timeout"))
        compose.settledUntil { !compose.has("revalidating") }
    }

    @Test
    fun foldUnfoldFoldKeepsThePageAndItsPosition() {
        answerSlowly(after = 7)
        release.countDown()
        var info by mutableStateOf(RemoteWindow.FoldCover.info)
        compose.setContent { RemoteForTest(home, info) }
        compose.settledUntil { compose.has("group-stage0:nas_timeout") }
        compose.onNodeWithTag("rows").performScrollToIndex(20)
        compose.waitForIdle()

        // open: the rail and the list beside the page
        info = RemoteWindow.PixelFoldOpen.info
        compose.waitForIdle()
        assertTrue("open: the same row", compose.has("group-stage20:nas_timeout"))
        assertFalse("open: never Loading", shows(compose.activity.getString(R.string.loading)))

        // closed again: the bar, its labels lined up, the same row
        info = RemoteWindow.FoldCover.info
        compose.waitForIdle()
        assertTrue("closed: the same row", compose.has("group-stage20:nas_timeout"))
        assertFalse("closed: not from the top", compose.has("group-stage0:nas_timeout"))
        assertTrue(compose.onAllNodes(hasTestTag("place-Attention")).fetchSemanticsNodes().isNotEmpty())
        compose.assertBarLabelsAligned(Place.entries.associate { "place-${it.name}" to compose.activity.getString(it.short) })
    }
}
