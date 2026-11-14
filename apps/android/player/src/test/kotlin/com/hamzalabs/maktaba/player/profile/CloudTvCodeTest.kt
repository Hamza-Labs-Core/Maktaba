package com.hamzalabs.maktaba.player.profile

import android.app.Application
import androidx.activity.ComponentActivity
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.core.playback.PlaybackSettings
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import com.hamzalabs.maktaba.player.harness.settledUntil
import mockwebserver3.MockResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * A TV's Maktaba Cloud code (Epic 58 story 58.03) in the Player (the Remote opens the same
 * dialog): "Approve a TV or device" asks the home first, then the cloud through the home, shows
 * the TV's platform and country, and approves only once the person confirms; the Sessions page
 * lists the apps signed in by a code beneath the browsers, and signs one out.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w411dp-h891dp")
class CloudTvCodeTest {
    @get:Rule(order = 0) val home = FakeHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private val app: Application get() = ApplicationProvider.getApplicationContext()
    private val languages = AppLanguages(35, FakeLocales(), MapStore())

    @Before
    fun routes() {
        ProfileHome.install(home)
        // the home holds no such code of its own
        home.answer("/api/pairing/device/ABCD2345") { json("""{"type":"about:blank","title":"not found","status":404}""", 404) }
    }

    private fun open() {
        compose.setProfileShell(home, languages, PlaybackSettings(MapStore()))
        compose.onNodeWithTag("section-Profile").performClick()
        compose.waitForIdle()
    }

    private fun waitForTag(tag: String) =
        compose.settledUntil(5_000) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isNotEmpty() }

    private fun gone(tag: String) =
        compose.settledUntil(5_000) { compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().isEmpty() }

    private fun typeCode(code: String) {
        compose.onNodeWithTag("profile-approve").performClick()
        waitForTag("approve-code")
        compose.onNodeWithTag("approve-code").performTextInput(code)
        compose.onNodeWithTag("approve-look").performClick()
    }

    private val posted: List<String> get() = home.requests.filter { it.method == "POST" }.map { it.url.encodedPath }

    @Test
    fun aCloudCodeShowsTheTvAndIsApprovedOnlyOnceConfirmed() {
        home.answer("/api/pairing/cloud-code/ABCD2345") {
            json("""{"code":"ABCD-2345","platform":"tizen","app_version":"1.0.0","country":"JO","expires_at":"2099-01-15T08:10:00Z"}""")
        }
        home.answer("/api/pairing/cloud-approve") { json("""{"status":"approved","user_id":"u1","platform":"tizen","country":"JO"}""") }
        open()
        typeCode("abcd-2345")
        waitForTag("approve-cloud-confirm")
        compose.onNodeWithText(app.getString(CoreR.string.approve_cloud_confirm, "Samsung", "Jordan")).assertExists()
        assertEquals("Is this your TV? Samsung, in Jordan.", app.getString(CoreR.string.approve_cloud_confirm, "Samsung", "Jordan"))
        assertTrue("the cloud asked through the home", home.paths.contains("/api/pairing/cloud-code/ABCD2345"))
        assertTrue("nothing approved before Approve", posted.isEmpty())
        compose.onNodeWithTag("approve-button").performClick()
        waitForTag("approve-done")
        compose.onNodeWithText(app.getString(CoreR.string.approve_cloud_done)).assertExists()
        assertEquals(listOf("/api/pairing/cloud-approve"), posted)
        val body = home.requests.last { it.url.encodedPath == "/api/pairing/cloud-approve" }.body!!.utf8()
        assertEquals("""{"code":"ABCD2345"}""", body)
    }

    @Test
    fun aTvWhoseCountryIsUnknownIsNamedByItsPlatform() {
        home.answer("/api/pairing/cloud-code/ABCD2345") { json("""{"code":"ABCD-2345","platform":"webos","country":""}""") }
        open()
        typeCode("ABCD2345")
        waitForTag("approve-cloud-confirm")
        compose.onNodeWithText("Is this your TV? LG.").assertExists()
    }

    @Test
    fun cancellingACloudCodeApprovesNothing() {
        home.answer("/api/pairing/cloud-code/ABCD2345") { json("""{"code":"ABCD-2345","platform":"tizen","country":"JO"}""") }
        open()
        typeCode("ABCD-2345")
        waitForTag("approve-cloud-confirm")
        compose.onNodeWithText(app.getString(CoreR.string.cancel)).performClick()
        waitForTag("approve-code")
        assertTrue(posted.isEmpty())
    }

    @Test
    fun aHomeWithoutRemoteAccessSaysToUseTheHomesCode() {
        home.answer("/api/pairing/cloud-code/ABCD2345") {
            json("""{"type":"about:blank","title":"Conflict","status":409,"code":"cloud_not_linked"}""", 409)
        }
        open()
        typeCode("ABCD-2345")
        waitForTag("approve-problem")
        compose.onNodeWithText(app.getString(CoreR.string.approve_cloud_needs_remote)).assertExists()
        assertTrue(posted.isEmpty())
    }

    @Test
    fun theSessionsPageListsTheAppSignInsAndSignsOneOut() {
        // the app sign-ins answer after the sessions, as on a loaded runner
        home.answer("/api/me/app-sign-ins") {
            Thread.sleep(1_500)
            json(ProfileHome.APP_SIGN_INS)
        }
        open()
        compose.onNodeWithTag("profile-root").performScrollToNode(hasTestTag("profile-sessions"))
        compose.onNodeWithTag("profile-sessions").performClick()
        waitForTag("session-s1")
        // the app sign-ins load after the sessions, and a lazy list holds only the rows on
        // screen: wait until the section can be scrolled to, and scroll to each row before
        // asserting it
        compose.settledUntil(5_000) {
            runCatching { compose.onNodeWithTag("sessions-list").performScrollToNode(hasTestTag("app-sign-ins")) }.isSuccess
        }
        compose.onNodeWithTag("app-sign-ins").assertExists()
        compose.onNodeWithTag("sessions-list").performScrollToNode(hasTestTag("app-sign-in-a2"))
        compose.onNodeWithText(app.getString(R.string.profile_app_via_cloud_code, "Samsung")).assertExists()
        assertEquals("Samsung TV, via Maktaba Cloud code", app.getString(R.string.profile_app_via_cloud_code, "Samsung"))
        compose.onNodeWithTag("sessions-list").performScrollToNode(hasTestTag("app-sign-in-a1"))
        compose.onNodeWithText("Living room TV").assertExists()
        compose.onNodeWithTag("app-sign-out-a1").performClick()
        gone("app-sign-in-a1")
        compose.onNodeWithTag("sessions-list").performScrollToNode(hasTestTag("app-sign-in-a2"))
        compose.onNodeWithTag("app-sign-in-a2").assertExists()
        assertTrue(home.requests.any { it.method == "DELETE" && it.url.encodedPath == "/api/me/app-sign-ins/a1" })
        compose.onNodeWithTag("sessions-list").performScrollToNode(hasTestTag("session-s1"))
        compose.onNodeWithTag("session-s1").assertExists()
    }

    @Test
    fun anOlderHomeShowsNoAppSection() {
        home.answer("/api/me/app-sign-ins") { MockResponse.Builder().code(404).body("404 page not found").build() }
        open()
        compose.onNodeWithTag("profile-root").performScrollToNode(hasTestTag("profile-sessions"))
        compose.onNodeWithTag("profile-sessions").performClick()
        waitForTag("session-s1")
        compose.settledUntil(5_000) { home.paths.contains("/api/me/app-sign-ins") }
        compose.waitForIdle()
        assertFalse(compose.onAllNodes(hasTestTag("app-sign-ins")).fetchSemanticsNodes().isNotEmpty())
        assertFalse(compose.onAllNodes(hasText(app.getString(R.string.profile_app_sign_ins))).fetchSemanticsNodes().isNotEmpty())
    }

    @Test @Config(qualifiers = "ar-w411dp-h891dp")
    fun inArabicTheTvAndItsCountryAreArabic() {
        home.answer("/api/pairing/cloud-code/ABCD2345") { json("""{"code":"ABCD-2345","platform":"tizen","country":"JO"}""") }
        open()
        typeCode("ABCD-2345")
        waitForTag("approve-cloud-confirm")
        compose.onNodeWithText("هل هذا تلفازك؟ Samsung، في الأردن.").assertExists()
    }
}
