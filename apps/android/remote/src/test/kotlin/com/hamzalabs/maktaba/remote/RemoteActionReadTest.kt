package com.hamzalabs.maktaba.remote

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.assertIsOn
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isOn
import androidx.compose.ui.test.isToggleable
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import mockwebserver3.MockResponse
import org.junit.After
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

/**
 * A change made while a place is read again (coming back to it shows what was read last and
 * asks the home again) stays: that read was answered before the change was saved, so it still
 * has the old state and must not put it back on screen while the home has the new one.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w344dp-h882dp-420dpi")
class RemoteActionReadTest {
    @get:Rule(order = 0) val home = FakeRemoteHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    /** The read again on coming back, held until the change is saved and read. */
    private val stale = CountDownLatch(1)
    /** Reads after the change's own read, held to the end: nothing newer hides the old one. */
    private val later = CountDownLatch(1)
    private val holdNext = AtomicBoolean(false)
    private val staleAsked = AtomicBoolean(false)
    private val staleAnswered = AtomicBoolean(false)
    private val readAfterStale = AtomicBoolean(false)
    private val saved = AtomicBoolean(false)
    private val readsAfterSave = AtomicInteger(0)
    @Volatile private var drain = false

    /** A read still held is let go, so the fake home can close. */
    @After fun letGo() {
        stale.countDown()
        later.countDown()
    }

    private fun cluster(drain: Boolean) =
        """{"home":{"name":"Home"},"nodes":[{"id":"m1","name":"Worker","state":"approved","online":true,"drain":$drain}]}"""

    @Test
    fun aSwitchFlippedWhileThePlaceIsReadAgainIsKept() {
        home.answer("/api/cluster") {
            // answered as the home has it when asked
            val now = cluster(drain)
            when {
                holdNext.getAndSet(false) -> {
                    staleAsked.set(true)
                    stale.await(20, TimeUnit.SECONDS)
                    staleAnswered.set(true)
                }
                saved.get() && readsAfterSave.incrementAndGet() > 1 -> later.await(20, TimeUnit.SECONDS)
            }
            FakeRemoteHome.json(now)
        }
        home.answer("/api/cluster/nodes/m1") { r ->
            drain = r.body!!.utf8().contains("true")
            saved.set(true)
            FakeRemoteHome.json("{}")
        }
        // the place reads the processes right after the machines: the held read was applied
        home.answer("/api/system/processes") {
            if (staleAnswered.get()) readAfterStale.set(true)
            MockResponse.Builder().code(404).build()
        }
        compose.setContent { RemoteForTest(home, RemoteWindow.FoldCover.info) }
        compose.onNodeWithTag("place-Machines").performClick()
        compose.settledUntil { compose.onAllNodes(hasText("Worker")).fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithText("Worker").performClick()
        compose.settledUntil { compose.onAllNodes(isToggleable()).fetchSemanticsNodes().isNotEmpty() }

        // another place and back: the machine read last shows, and is read again (held)
        compose.onNodeWithTag("place-Queue").performClick()
        compose.waitForIdle()
        holdNext.set(true)
        compose.onNodeWithTag("place-Machines").performClick()
        compose.settledUntil { staleAsked.get() }
        compose.settledUntil { compose.onAllNodes(isToggleable()).fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(isToggleable()).performClick()
        // saved, read again by the change itself: on
        compose.settledUntil { readsAfterSave.get() >= 1 && compose.onAllNodes(isToggleable() and isOn()).fetchSemanticsNodes().isNotEmpty() }

        stale.countDown()
        compose.settledUntil { readAfterStale.get() }
        compose.waitForIdle()
        compose.onNode(isToggleable()).assertIsOn()
    }
}
