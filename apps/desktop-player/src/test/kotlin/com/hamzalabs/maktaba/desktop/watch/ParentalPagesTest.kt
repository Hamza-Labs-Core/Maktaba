package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.runComposeUiTest
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.MyParental
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.STALL_MS
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.settle
import com.hamzalabs.maktaba.desktop.Texts
import com.hamzalabs.maktaba.desktop.home.HomeScreen
import com.hamzalabs.maktaba.desktop.video.VideoPage
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Epic 57 on the desktop Player's pages: Home says a restricted account's time (story 57.15),
 * and a video the home does not show a limited sign-in is "This isn't available", with a parent
 * to ask and a parent's PIN (stories 57.05, 57.06), in English and Arabic.
 */
@OptIn(ExperimentalTestApi::class)
class ParentalPagesTest {
    private val restricted = """{"restricted":true,"max_tier":9,"allow_only":false,"version":2}"""

    private fun TestHome.signedInRestricted() = home().also {
        answer("GET /api/me/parental", restricted)
        runBlocking { it.parental.refresh() }
    }

    @Test
    fun homeSaysTheTimeLeftToday() = TestHome().use { fake ->
        fake.answer("GET /api/me/parental/time", """{"allowed":true,"limited":true,"remaining_seconds":2700}""")
        val home = fake.signedInRestricted()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(1200.dp, 900.dp)) { HomeScreen() } } }
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("parental-home-left")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("45 minutes left today").assertExists()
        }
    }

    @Test
    fun homeSaysTheTimeIsOverAndWhenItOpensAgainInArabic() = TestHome().use { fake ->
        fake.answer("GET /api/me/parental/time", """{"allowed":false,"limited":true,"reason":"limit_reached","next_allowed_at":"2026-11-12T04:00:00Z"}""")
        val home = fake.signedInRestricted()
        runComposeUiTest {
            setContent { InPlayer(home, Lang.Ar) { Box(Modifier.size(1200.dp, 900.dp)) { HomeScreen() } } }
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("parental-home-over")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("انتهى وقت اليوم").assertExists()
        }
    }

    @Test
    fun homeSaysNothingOfTimeToAnAccountWithNoLimit() = TestHome().use { fake ->
        val home = fake.home()
        runBlocking { home.parental.refresh() }
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(1200.dp, 900.dp)) { HomeScreen() } } }
            settle(fake)
            assertEquals(0, onAllNodes(hasTestTag("parental-home-time")).fetchSemanticsNodes().size)
            assertEquals("the home is not asked about time", 0, fake.asked("GET /api/me/parental/time").size)
        }
    }

    @Test
    fun aHiddenVideoIsTheBlockedPageAndAParentsPinOpensIt() = TestHome().use { fake ->
        // the home answers a hidden video as a missing one
        fake.answer("GET /api/me/parental/guardians", """{"guardians":[{"id":"g1","username":"huda"}]}""")
        fake.answer("POST /api/me/parental/requests", """{"request":{"id":"r1","subject_kind":"video","subject_id":"v1","status":"pending"}}""", 201)
        fake.answer("POST /api/me/parental/unlock") {
            // opened: the home now gives the video
            fake.answer("GET /api/videos/v1", """{"id":"v1","title":"Fiqh 7","duration_sec":3000}""")
            TestHome.json("""{"rule":{"id":"x1"}}""")
        }
        val home = fake.signedInRestricted()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(1200.dp, 900.dp)) { VideoPage("v1", null) } } }
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("parental-blocked")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("This isn't available").assertExists()
            onNodeWithText("Your account is limited to 9+.").assertExists()
            assertEquals("not the page of a missing video", 0, onAllNodes(hasTestTag("not-available")).fetchSemanticsNodes().size)
            // the PIN button joins the row once the home has said who holds one, and moves Ask a
            // parent aside: a click sent before that can land beside the button it was aimed at
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("parental-pin")).fetchSemanticsNodes().isNotEmpty() }
            waitForIdle()
            // Ask a parent names the video, not minutes
            onNodeWithText("Ask a parent").performClick()
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("parental-ask-state")).fetchSemanticsNodes().isNotEmpty() }
            assertEquals("""{"subject_kind":"video","subject_id":"v1"}""", fake.asked("POST /api/me/parental/requests").single().body!!.utf8())
            // a parent types their PIN: no minutes to choose, and the page becomes the video's
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("parental-pin")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithTag("parental-pin").performClick()
            assertEquals(0, onAllNodes(hasTestTag("parental-pin-minutes-30")).fetchSemanticsNodes().size)
            onNodeWithTag("parental-pin-field").performTextInput("123456")
            onNodeWithTag("parental-pin-submit").performClick()
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("video-page")).fetchSemanticsNodes().isNotEmpty() }
            assertEquals(
                """{"guardian_id":"g1","pin":"123456","subject_kind":"video","subject_id":"v1"}""",
                fake.asked("POST /api/me/parental/unlock").single().body!!.utf8(),
            )
            onNodeWithText("Fiqh 7").assertExists()
        }
    }

    @Test
    fun theBlockedPageInArabicAndTheMissingPageForAnAccountWithNoLimit() = TestHome().use { fake ->
        val limited = fake.signedInRestricted()
        runComposeUiTest {
            setContent { InPlayer(limited, Lang.Ar) { Box(Modifier.size(1200.dp, 900.dp)) { VideoPage("v1", null) } } }
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("parental-blocked")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("هذا غير متاح").assertExists()
            onNodeWithText("اطلب من أحد الوالدين").assertExists()
        }
        TestHome().use { other ->
            val free = other.home()
            runComposeUiTest {
                setContent { InPlayer(free) { Box(Modifier.size(1200.dp, 900.dp)) { VideoPage("v1", null) } } }
                waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("not-available")).fetchSemanticsNodes().isNotEmpty() }
                assertEquals(0, onAllNodes(hasTestTag("parental-blocked")).fetchSemanticsNodes().size)
            }
        }
    }

    @Test
    fun theBlockedPageReadsAgainWhenTheWindowComesBackToTheFront() = TestHome().use { fake ->
        // a parent approves from their own device while the child looks at another window (story 57.06)
        val home = fake.signedInRestricted()
        val focused = androidx.compose.runtime.mutableStateOf(true)
        val window = object : androidx.compose.ui.platform.WindowInfo {
            override val isWindowFocused: Boolean get() = focused.value
        }
        runComposeUiTest {
            setContent {
                androidx.compose.runtime.CompositionLocalProvider(androidx.compose.ui.platform.LocalWindowInfo provides window) {
                    InPlayer(home) { Box(Modifier.size(1200.dp, 900.dp)) { VideoPage("v1", null) } }
                }
            }
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("parental-blocked")).fetchSemanticsNodes().isNotEmpty() }
            val before = fake.asked("GET /api/videos/v1").size
            focused.value = false
            settle(fake)
            assertEquals("leaving reads nothing", before, fake.asked("GET /api/videos/v1").size)
            fake.answer("GET /api/videos/v1", """{"id":"v1","title":"Fiqh 7","duration_sec":3000}""")
            focused.value = true
            waitUntil(timeoutMillis = STALL_MS) { onAllNodes(hasTestTag("video-page")).fetchSemanticsNodes().isNotEmpty() }
            onNodeWithText("Fiqh 7").assertExists()
        }
    }

    @Test
    fun theLimitIsNamedInBothLanguages() {
        val en = Texts(Lang.En)
        val ar = Texts(Lang.Ar)
        assertEquals("Your account is limited to 13+.", limitLine(MyParental(restricted = true, maxTier = 13), en))
        assertEquals("This device is limited to All ages.", limitLine(MyParental(deviceLimit = 0), en))
        assertEquals("Your account is limited to Nothing unless allowed.", limitLine(MyParental(restricted = true, maxTier = MyParental.NOTHING_ALLOWED), en))
        assertEquals("حسابك مقيَّد بـ ‎+13.", limitLine(MyParental(restricted = true, maxTier = 13), ar))
        assertNull(limitLine(MyParental.None, en))
    }
}
