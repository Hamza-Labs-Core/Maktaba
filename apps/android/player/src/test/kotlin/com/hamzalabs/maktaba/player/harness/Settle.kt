package com.hamzalabs.maktaba.player.harness

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import org.junit.Assert.assertTrue

/**
 * Waits up to [timeoutMs] for [check], letting the app settle before each look.
 *
 * The tests use the v2 compose rules, whose test dispatcher queues the screens' coroutine
 * work for the test's main thread (the v1 rules ran it unconfined: an answer from the fake
 * home resumed the screen on OkHttp's thread and wrote its state from there, and on a busy
 * runner the screen could stay on its spinner with the answer in hand). Queued work runs
 * when the app is settled, so each look settles first; a bare `waitUntil` only polls.
 *
 * Not for waits on something that goes away by itself (controls that hide after a delay):
 * settling advances the clock, and the thing can come and go between two looks.
 */
fun ComposeContentTestRule.settledUntil(timeoutMs: Long, check: () -> Boolean) =
    waitUntil(timeoutMs) {
        waitForIdle()
        check()
    }

/**
 * [settledUntil], then fails with [what], the paths the app asked [home] and what the screen
 * shows, so a miss on a runner can be read from its report.
 */
fun ComposeContentTestRule.settled(home: FakeHome, what: String, timeoutMs: Long = 10_000, check: () -> Boolean) {
    waitForIdle()
    runCatching { settledUntil(timeoutMs, check) }
    assertTrue("$what; asked ${home.paths}; showing ${shown()}", check())
}

/** Every tag and text on the screen, in order. */
fun ComposeContentTestRule.shown(): List<String> =
    onAllNodes(SemanticsMatcher("any") { true }, useUnmergedTree = true).fetchSemanticsNodes().flatMap { n ->
        listOfNotNull(n.config.getOrNull(SemanticsProperties.TestTag)) +
            n.config.getOrNull(SemanticsProperties.Text).orEmpty().map { it.text }
    }
