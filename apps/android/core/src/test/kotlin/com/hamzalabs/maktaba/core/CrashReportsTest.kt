package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.diagnostics.ExitReasons
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

// Epic 53 story 53.14 T05: the process exits Android recorded (API 30+) that become crash
// reports, and those that do not.
class CrashReportsTest {
    @Test
    fun anAnrANativeCrashAndALowMemoryKillAreKeptWithTheirTrace() {
        val anr = ExitReasons.kept(ExitReasons.ANR, 1_800_000_000_000, "Input dispatching timed out", "\"main\" prio=5", "0.3.90", "Android 15", "Pixel 9")!!
        assertEquals("ANR", anr.type)
        assertEquals("anr", anr.mechanism)
        assertEquals("\"main\" prio=5", anr.trace)
        assertEquals("NativeCrash", ExitReasons.kept(ExitReasons.CRASH_NATIVE, 1, null, null, null, null, null)!!.type)
        assertEquals("low_memory", ExitReasons.kept(ExitReasons.LOW_MEMORY, 1, null, null, null, null, null)!!.mechanism)
        val big = "x".repeat(300 * 1024)
        assertEquals(256 * 1024, ExitReasons.kept(ExitReasons.ANR, 1, null, big, null, null, null)!!.trace!!.length)
    }

    @Test
    fun theOtherExitsAreNone() {
        // REASON_EXIT_SELF (1), REASON_USER_REQUESTED (10), REASON_CRASH (4: CrashLog has the Java crash)
        listOf(1, 4, 10, 0).forEach { assertNull(ExitReasons.kept(it, 1, null, null, null, null, null)) }
    }
}
