package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.ui.clock
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * Story 48.37: no connect, sign-in, restore or retry screen waits behind a bare spinner. Each
 * says its step through `ConnectStatus` (or `StatusLine`, a labelled wait), the only place a
 * spinner is drawn on the start screens.
 */
class ConnectStatusTest {
    // the tests run in the module's folder: apps/android/core
    private val ui = File("src/main/kotlin/com/hamzalabs/maktaba/core/ui")

    @Test
    fun theStartScreensDrawNoSpinnerOfTheirOwn() {
        val screens = listOf("StartScreens.kt", "CloudScreens.kt").map { File(ui, it) }
        screens.forEach { assertTrue("$it is where the start screens are", it.isFile) }
        val bare = screens.filter { "CircularProgressIndicator" in it.readText() }.map { it.name }
        assertTrue("a spinner without its words in $bare: use ConnectStatus or StatusLine", bare.isEmpty())
    }

    @Test
    fun everySpinnerOfConnectStatusSitsBesideItsWords() {
        val text = File(ui, "ConnectStatus.kt").readText()
        // each spinner is in a Row whose next element is a Text (StatusLine), or in the step's
        // icon box beside the headline (ConnectStatus)
        val spinners = Regex("CircularProgressIndicator\\(").findAll(text).count()
        assertEquals("one in ConnectStatus, one in StatusLine", 2, spinners)
        assertTrue(text.contains("headline(progress, left)"))
    }

    @Test
    fun aCountdownReadsAsMinutesAndSeconds() {
        assertEquals("9:58", clock(9 * 60_000L + 57_100))
        assertEquals("0:07", clock(7_000))
        assertEquals("0:01", clock(1))
        assertEquals("never below nothing", "0:00", clock(-5_000))
        assertEquals("12:00", clock(12 * 60_000L))
    }
}
