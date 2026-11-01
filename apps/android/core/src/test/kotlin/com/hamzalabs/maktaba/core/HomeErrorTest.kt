package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.ErrorKind.Busy
import com.hamzalabs.maktaba.core.api.ErrorKind.NoAccess
import com.hamzalabs.maktaba.core.api.ErrorKind.NotFound
import com.hamzalabs.maktaba.core.api.ErrorKind.NotReady
import com.hamzalabs.maktaba.core.api.ErrorKind.Server
import com.hamzalabs.maktaba.core.api.ErrorKind.SignedOut
import com.hamzalabs.maktaba.core.api.ErrorKind.Unreachable
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.api.Problem
import com.hamzalabs.maktaba.core.api.errorKind
import com.hamzalabs.maktaba.core.api.retryAfterSeconds
import com.hamzalabs.maktaba.core.ui.ErrorAction
import com.hamzalabs.maktaba.core.ui.errorActions
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

/** The home's problem JSON as a person reads it: a kind, never the JSON. */
class HomeErrorTest {
    /** What the owner's Player showed raw. */
    private val renditionGone =
        """{"type":"https://maktaba.invalid/problems/rendition-not-found","title":"rendition index not on disk","status":404,"detail":"open /Volumes/x/index.m3u8: no such file or directory"}"""

    private fun kind(code: Int, body: String) = ApiException.Status(code, body).errorKind()

    @Test
    fun readsAProblemWhateverTheHomesDomain() {
        val p = Problem.parse(renditionGone)!!
        assertEquals("rendition-not-found", p.slug)
        assertEquals(404, p.status)
        assertEquals("rendition index not on disk", p.title)
        assertEquals("rendition-not-found", Problem.parse("""{"type":"https://maktaba.tv/problems/rendition-not-found"}""")!!.slug)
        assertEquals("", Problem.parse("""{"type":"about:blank","title":"Not Found"}""")!!.slug)
        assertNull("not JSON", Problem.parse("<html>502 Bad Gateway</html>"))
        assertNull("empty", Problem.parse(""))
        assertNull("JSON but not a problem", Problem.parse("""{"items":[]}"""))
        assertNull("broken JSON", Problem.parse("""{"type": """))
    }

    @Test
    fun eachAnswerIsAKind() {
        assertEquals(NotReady, kind(404, renditionGone))
        assertEquals(NotReady, kind(404, """{"type":"https://maktaba.invalid/problems/manifest-not-found","title":"no manifest","status":404}"""))
        assertEquals(NotFound, kind(404, """{"type":"https://maktaba.tv/problems/not-found","title":"video not found","status":404}"""))
        assertEquals(NotFound, kind(404, "404 page not found"))
        assertEquals(NotFound, kind(410, ""))
        assertEquals(SignedOut, kind(401, """{"type":"https://maktaba.tv/problems/unauthorized","status":401}"""))
        assertEquals(NoAccess, kind(403, """{"type":"https://maktaba.tv/problems/forbidden","status":403}"""))
        assertEquals(Unreachable, kind(503, """{"type":"https://maktaba.tv/problems/unavailable","title":"home unreachable","status":503,"code":"home_unreachable"}"""))
        assertEquals(Unreachable, kind(504, """{"title":"home timeout","status":504,"code":"home_timeout"}"""))
        assertEquals(Unreachable, kind(503, """{"type":"https://maktaba.tv/problems/video-unavailable-offline","status":503}"""))
        assertEquals(Busy, kind(503, """{"type":"https://maktaba.tv/problems/unavailable","status":503,"code":"paused"}"""))
        assertEquals(Busy, kind(429, """{"type":"https://maktaba.tv/problems/rate-limited","status":429}"""))
        assertEquals(Server, kind(500, """{"type":"https://maktaba.tv/problems/internal","status":500}"""))
        assertEquals(Server, kind(502, "<html>bad gateway</html>"))
        assertEquals(Server, kind(409, "{}"))
    }

    @Test
    fun theClientsOwnFailuresAreKindsToo() {
        assertEquals(SignedOut, ApiException.SignedOut.errorKind())
        assertEquals(Unreachable, ApiException.Transport("connect timed out").errorKind())
        assertEquals(NotReady, ApiException.NotReady(404, renditionGone).errorKind())
        assertEquals(Server, ApiException.Decoding("bad JSON").errorKind())
        assertEquals("a stream request's own failure", Unreachable, IOException("reset").errorKind())
        assertEquals("a stream request that could not refresh", SignedOut, IOException("x", ApiException.SignedOut).errorKind())
        assertEquals(Server, IllegalStateException("The home gave no address to play.").errorKind())
    }

    @Test
    fun theMessageNeverCarriesTheHomesAnswer() {
        val e = ApiException.Status(404, renditionGone)
        val message = e.message!!
        assertFalse(message, message.contains("{"))
        assertFalse(message, message.contains("/Volumes"))
        assertFalse(message, message.contains("rendition index"))
        assertEquals("The home answered 404 (rendition-not-found)", message)
        assertEquals("the body stays for Details and the log", renditionGone, e.body)
    }

    @Test
    fun detailsAreTheHomesWordsReadable() {
        val f = Failure.of(ApiException.Status(404, renditionGone))
        assertEquals(NotReady, f.kind)
        val d = f.details!!
        assertTrue(d, d.startsWith("HTTP 404 · rendition-not-found"))
        assertTrue(d, d.contains("rendition index not on disk"))
        assertTrue(d, d.contains("/Volumes/x/index.m3u8"))
        assertFalse("Details are lines, not JSON", d.contains("{"))
        assertEquals("HTTP 502\n<html>bad gateway</html>", Failure.of(ApiException.Status(502, "<html>bad gateway</html>")).details)
    }

    @Test
    fun retryAfterIsKept() {
        assertEquals(30L, retryAfterSeconds("30"))
        assertEquals(0L, retryAfterSeconds(" 0 "))
        assertNull("an HTTP date is not read", retryAfterSeconds("Wed, 21 Oct 2026 07:28:00 GMT"))
        assertNull(retryAfterSeconds(null))
        assertNull(retryAfterSeconds("-5"))
        val f = Failure.of(ApiException.Status(503, """{"status":503,"title":"busy"}""", retryAfterSec = 12))
        assertEquals(Busy, f.kind)
        assertEquals(12L, f.retryAfterSec)
    }

    @Test
    fun theActionsFitTheKind() {
        fun all(k: ErrorKind) = errorActions(k, canRetry = true, canSignIn = true, canChooseHome = true, canGoBack = true)
        assertEquals(listOf(ErrorAction.SignIn, ErrorAction.Back), all(SignedOut))
        assertEquals(listOf(ErrorAction.TryAgain, ErrorAction.OtherHome, ErrorAction.Back), all(Unreachable))
        assertEquals(listOf(ErrorAction.TryAgain, ErrorAction.Back), all(NotReady))
        assertEquals(listOf(ErrorAction.TryAgain, ErrorAction.Back), all(Busy))
        assertEquals(listOf(ErrorAction.TryAgain, ErrorAction.Back), all(Server))
        assertEquals("trying again will not bring it back", listOf(ErrorAction.Back), all(NotFound))
        assertEquals(listOf(ErrorAction.Back), all(NoAccess))
        assertEquals(
            "a list with only Try again keeps it, whatever the kind",
            listOf(ErrorAction.TryAgain),
            errorActions(NotFound, canRetry = true, canSignIn = false, canChooseHome = false, canGoBack = false),
        )
    }
}
