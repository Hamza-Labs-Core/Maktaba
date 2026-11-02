package com.hamzalabs.maktaba.player.harness

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import org.junit.Assert.assertTrue

/**
 * Waits up to [timeoutMs] for [check], letting the app settle before each look.
 *
 * A bare `waitUntil` only polls: under Robolectric the test runs on the main thread, and an
 * answer from the fake home that lands after the last idle (OkHttp resumes the screen's
 * coroutine on its own thread) is not applied to the screen until something idles the main
 * looper. On a busy runner nothing does for the whole timeout, and the screen stays on its
 * spinner with the answer in hand. `waitForIdle()` in each poll applies it.
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
