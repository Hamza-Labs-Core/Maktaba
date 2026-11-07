package com.hamzalabs.maktaba.desktop.diagnostics

import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.desktop.TestHome
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import com.hamzalabs.maktaba.desktop.ErrorBox
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.PlayerTheme
import com.hamzalabs.maktaba.desktop.Texts
import org.junit.Test
import org.junit.rules.TemporaryFolder

// Epic 53 on the desktop Player (stories 53.14 and 53.15): a crash is kept without its message,
// asked about once per home, and sent to the home only when the person said yes.
class DiagnosticsTest {
    @get:Rule val tmp = TemporaryFolder()

    private val home = Home(url = "http://192.168.50.177:8088", name = "Test Home", id = "h1")

    @Test
    fun aCrashIsItsDeepestCausesClassAndFramesWithoutItsMessage() {
        val e = RuntimeException("wrap", IllegalStateException("token=s3cr3t for someone@example.com"))
        val c = DesktopCrashes.kept(e, "0.3.90", at = 1_800_000_000_000)
        assertEquals("java.lang.IllegalStateException", c.type)
        assertEquals("", c.reason)
        assertTrue(c.frames.first().startsWith("com.hamzalabs.maktaba.desktop.diagnostics.DiagnosticsTest."))
        assertFalse(c.encode().contains("s3cr3t") || c.encode().contains("someone@example.com"))
    }

    @Test
    fun theQuestionIsAskedOncePerHomeAndStartsOff() {
        val c = CrashConsent(MapStore())
        assertNull(c.answer(home))
        assertFalse(c.isOn(home))
        c.set(false, home)
        assertEquals(false, c.answer(home))
        c.set(true, home)
        assertTrue(c.isOn(home))
        assertNull(c.answer(home.copy(id = "h2", url = "http://192.168.50.178:8088")))
    }

    @Test
    fun offSendsNothingAndOnSendsEachCrashOnceWithTheConsent() = runBlocking {
        TestHome().use { t ->
            val bodies = mutableListOf<String>()
            t.answer("POST /api/diagnostics/envelope") { r ->
                bodies += r.body?.utf8().orEmpty()
                mockwebserver3.MockResponse.Builder().code(200).body("{}").build()
            }
            val outbox = DesktopCrashes.outbox(tmp.root)
            outbox.keep(DesktopCrashes.kept(IllegalStateException("x"), "0.3.90"))
            val consent = CrashConsent(MapStore())
            val header = DesktopCrashes.header("install-1", "0.3.90")
            assertEquals(0, DesktopCrashes.flush(outbox, t.client, home, consent, header))
            assertTrue("off: nothing leaves", bodies.isEmpty())
            consent.set(true, home)
            assertEquals(1, DesktopCrashes.flush(outbox, t.client, home, consent, header))
            assertEquals(0, DesktopCrashes.flush(outbox, t.client, home, consent, header))
            assertEquals(1, bodies.size)
            assertTrue(bodies[0].contains("\"consent\":\"crash_reports_to_home\""))
            assertTrue(bodies[0].contains("\"app\":\"desktop.player\""))
        }
    }

    @Test
    fun everyWordHasItsArabic() {
        val p = DiagnosticsStrings.part
        assertEquals(p.en.keys, p.ar.keys)
        p.ar.values.forEach { v -> assertTrue(v, v.any { it in '؀'..'ۿ' }) }
    }

    /** An error box offers "Report this" under the player's report action (signed in), with that call's reference. */
    @OptIn(ExperimentalTestApi::class)
    @Test
    fun anErrorBoxOffersReportThisWithItsReference() = runComposeUiTest {
        com.hamzalabs.maktaba.core.api.RequestId.next()
        val opened = mutableListOf<String?>()
        setContent {
            PlayerTheme(Texts(Lang.En), big = false) {
                androidx.compose.runtime.CompositionLocalProvider(LocalReportProblem provides { ref: String? -> opened += ref }) {
                    ErrorBox("The library could not load", RuntimeException("x"), onRetry = {})
                }
            }
        }
        onNodeWithText("Report this").performClick()
        assertEquals(1, opened.size)
        assertEquals(lastReference(), opened.single())
        assertEquals(8, opened.single()!!.length)
    }

    @OptIn(ExperimentalTestApi::class)
    @Test
    fun withoutTheActionThereIsNoReportThis() = runComposeUiTest {
        setContent { PlayerTheme(Texts(Lang.Ar), big = false) { ErrorBox("x", null, onRetry = null) } }
        assertTrue(onAllNodes(androidx.compose.ui.test.hasTestTag("error-report")).fetchSemanticsNodes().isEmpty())
    }
}
