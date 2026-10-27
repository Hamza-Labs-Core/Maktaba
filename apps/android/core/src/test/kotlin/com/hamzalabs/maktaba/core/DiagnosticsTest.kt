package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.diagnostics.CrashLog
import com.hamzalabs.maktaba.core.diagnostics.Redacted
import com.hamzalabs.maktaba.core.ui.ErrorAction
import com.hamzalabs.maktaba.core.ui.errorActions
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException
import java.time.Instant

/** What the apps log and keep after a crash: what failed, never a message, address or body. */
class DiagnosticsTest {
    private val problem404 =
        """{"type":"https://maktaba.example/problems/video-not-found","title":"Not Found","status":404,"detail":"no video at /volume1/media/secret.mkv"}"""

    @Test
    fun `a logged failure names its kind, class, status and slug, never its body`() {
        val line = AppLog.describe(ApiException.Status(404, problem404))
        assertEquals("NotFound Status HTTP 404 video-not-found", line)
        assertFalse(line.contains("/volume1"))
    }

    @Test
    fun `a logged transport failure leaves out the home's address`() {
        val line = AppLog.describe(ApiException.Transport("failed to connect to /192.168.1.20 (port 8080)"))
        assertEquals("Unreachable Transport", line)
    }

    @Test
    fun `a redacted throwable keeps classes and frames, not messages`() {
        val cause = IOException("token=abc.def at 10.0.0.5")
        val e = IllegalStateException("user alice not found", cause)
        val r = Redacted.of(e)
        val text = r.stackTraceToString()
        assertTrue(text.startsWith("java.lang.IllegalStateException"))
        assertTrue(text.contains("Caused by: java.io.IOException"))
        assertFalse(text.contains("alice"))
        assertFalse(text.contains("token"))
        assertFalse(text.contains("10.0.0.5"))
        assertEquals(e.stackTrace.toList(), r.stackTrace.toList())
    }

    @Test
    fun `a cause that loops back on itself ends`() {
        val a = RuntimeException("a")
        val b = RuntimeException("b", a)
        a.initCause(b)
        assertTrue(Redacted.of(a).stackTraceToString().contains("Caused by: java.lang.RuntimeException"))
    }

    @Test
    fun `a crash report says what, where and when, without messages`() {
        val report = CrashLog.report(
            NullPointerException("home https://nas.local:8080 user bob"),
            thread = "main",
            app = "com.hamzalabs.maktaba.tv 1.2.3",
            os = "Android 14 (API 34)",
            at = Instant.parse("2026-10-27T10:00:00Z"),
        )
        assertTrue(report.contains("app: com.hamzalabs.maktaba.tv 1.2.3"))
        assertTrue(report.contains("os: Android 14 (API 34)"))
        assertTrue(report.contains("at: 2026-10-27T10:00:00Z"))
        assertTrue(report.contains("thread: main"))
        assertTrue(report.contains("java.lang.NullPointerException"))
        assertFalse(report.contains("nas.local"))
        assertFalse(report.contains("bob"))
    }

    @Test
    fun `failures map to the kind a person reads`() {
        assertEquals(ErrorKind.NotFound, Failure.of(ApiException.Status(404, problem404)).kind)
        assertEquals(ErrorKind.NoAccess, Failure.of(ApiException.Status(403, "")).kind)
        assertEquals(ErrorKind.Busy, Failure.of(ApiException.Status(503, "")).kind)
        assertEquals(ErrorKind.Server, Failure.of(ApiException.Status(500, "<html>oops</html>")).kind)
        assertEquals(ErrorKind.Unreachable, Failure.of(ApiException.Transport("timeout")).kind)
        assertEquals(ErrorKind.SignedOut, Failure.of(ApiException.SignedOut).kind)
        assertEquals(ErrorKind.Server, Failure.of(ApiException.Decoding("bad json")).kind)
        assertEquals(ErrorKind.Unreachable, Failure.of(IOException("reset")).kind)
        assertEquals(ErrorKind.Server, Failure.of(IllegalStateException("x")).kind)
    }

    @Test
    fun `each kind offers what helps`() {
        // the home does not answer: try again first, then another home
        assertEquals(
            listOf(ErrorAction.TryAgain, ErrorAction.OtherHome),
            errorActions(ErrorKind.Unreachable, canRetry = true, canSignIn = false, canChooseHome = true, canGoBack = false),
        )
        // gone: trying again will not help; back when there is somewhere to go
        assertEquals(
            listOf(ErrorAction.Back),
            errorActions(ErrorKind.NotFound, canRetry = true, canSignIn = false, canChooseHome = true, canGoBack = true),
        )
        // nowhere to go back to (a tab): a list can at least ask again
        assertEquals(
            listOf(ErrorAction.TryAgain),
            errorActions(ErrorKind.NotFound, canRetry = true, canSignIn = false, canChooseHome = true, canGoBack = false),
        )
        assertEquals(
            listOf(ErrorAction.TryAgain),
            errorActions(ErrorKind.Server, canRetry = true, canSignIn = false, canChooseHome = true, canGoBack = false),
        )
    }
}
