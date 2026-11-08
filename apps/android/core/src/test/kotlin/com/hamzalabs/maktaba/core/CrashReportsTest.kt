package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.crash.TombstoneBytes
import com.hamzalabs.maktaba.core.diagnostics.ExitReasons
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
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

    // 53.14 §2: a native crash's tombstone (Android 12+) keeps its signal, scrubbed abort
    // message and the crashed thread's frames, in the Android TV app as in the others
    @Test
    fun aNativeCrashKeepsItsTombstonesFrames() {
        val b = TombstoneBytes.tombstone(signal = "SIGABRT", code = "SI_TKILL",
            abort = "Check failed: /storage/emulated/0/Movies/a.mkv owner a@b.co", threads = mapOf(4321L to TombstoneBytes.CRASHED))
        val c = ExitReasons.kept(ExitReasons.CRASH_NATIVE, 1, "crash", null, "0.3.95", "Android 15", "Chromecast",
            tombstone = b, ownPackage = "com.hamzalabs.maktaba.tv")!!
        assertEquals("SIGABRT", c.type)
        assertEquals("native", c.mechanism)
        assertEquals("Check failed: <path> owner [EMAIL]", c.reason)
        assertEquals(listOf("libc.so", "libmpv.so", "libplayer.so"), c.nativeFrames.map { it.module })
        assertEquals(listOf(false, true, true), c.nativeFrames.map { it.inApp })
        // no abort message: the signal and its code
        val noAbort = ExitReasons.kept(ExitReasons.CRASH_NATIVE, 1, "crash", null, null, null, null,
            tombstone = TombstoneBytes.tombstone(threads = mapOf(4321L to TombstoneBytes.CRASHED)))!!
        assertEquals("SIGSEGV (SEGV_MAPERR)", noAbort.reason)
    }

    @Test
    fun aTombstoneThatDoesNotParseKeepsTheReasonAlone() {
        listOf(ByteArray(0), "not a tombstone".toByteArray(), byteArrayOf(0x0a, 0x7f)).forEach { b ->
            val c = ExitReasons.kept(ExitReasons.CRASH_NATIVE, 1, "crash", null, null, null, null, tombstone = b)!!
            assertEquals("NativeCrash", c.type)
            assertEquals("crash", c.reason)
            assertTrue(c.nativeFrames.isEmpty())
        }
        // an ANR's trace is never read as a tombstone
        val anr = ExitReasons.kept(ExitReasons.ANR, 1, "anr", "t", null, null, null, tombstone = TombstoneBytes.tombstone(threads = mapOf(4321L to TombstoneBytes.CRASHED)))!!
        assertTrue(anr.nativeFrames.isEmpty())
    }
}
