package com.hamzalabs.maktaba.player.harness

import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertNotNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.shadows.ShadowLog

/**
 * The app's log reaches every JVM test's report: Robolectric drops android.util.Log unless
 * `robolectric.logging` asks for it (set for every module in the Gradle root), and a test that
 * fails on a runner without it leaves no clue why.
 */
@RunWith(AndroidJUnit4::class)
class LogsKeptTest {
    @Test
    fun theAppsLogGoesToTheTestsOutput() {
        assertNotNull("robolectric.logging is not set: the app's log is dropped", ShadowLog.stream)
    }
}
