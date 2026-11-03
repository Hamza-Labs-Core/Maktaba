package com.hamzalabs.maktaba.remote

import android.Manifest
import android.app.Application
import android.app.NotificationManager
import android.content.Intent
import androidx.activity.ComponentActivity
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.api.PendingSignIn
import com.hamzalabs.maktaba.core.ui.ApproveRequests
import com.hamzalabs.maktaba.core.ui.PendingSignInsState
import com.hamzalabs.maktaba.core.ui.SignInNotifications
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * A TV waiting to sign in on the home: the Remote says so at the top of every place ("A TV
 * is asking to sign in: … Enter code"), and as a notification when the person allows them.
 * The list never carries the code: "Enter code" opens Approve, where it is typed or scanned.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class PendingSignInsTest {
    @get:Rule(order = 0) val home = FakeRemoteHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    @Volatile private var waiting = TV

    private fun answerWaiting() = home.answer("/api/pairing/pending") { FakeRemoteHome.json(waiting) }

    private fun text(): String =
        compose.onAllNodes(hasTestTag("pending-text"), useUnmergedTree = true).fetchSemanticsNodes().single()
            .config.getOrNull(SemanticsProperties.Text)!!.joinToString { it.text }

    private val notifications: NotificationManager
        get() = ApplicationProvider.getApplicationContext<Application>().getSystemService(NotificationManager::class.java)

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun theBannerShowsWhileATvWaitsAndHidesWhenItIsGone() {
        answerWaiting()
        compose.setContent { RemoteForTest(home, RemoteWindow.RazrFoldCover.info) }
        compose.settledUntil { compose.has("pending-banner") }
        assertEquals(compose.activity.getString(CoreR.string.pending_tv_named, "Living room TV"), text())
        assertTrue("the place's bar is still there", compose.has("approve-tv"))
        waiting = NONE
        compose.settledUntil { !compose.has("pending-banner") }
    }

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun dismissLastsUntilANewRequestArrives() {
        answerWaiting()
        compose.setContent { RemoteForTest(home, RemoteWindow.RazrFoldCover.info) }
        compose.settledUntil { compose.has("pending-banner") }
        compose.onNode(hasTestTag("pending-dismiss")).performClick()
        compose.settledUntil { !compose.has("pending-banner") }
        // the same request, asked again: still hidden
        val asked = home.paths.count { it == "/api/pairing/pending" }
        compose.settledUntil { home.paths.count { it == "/api/pairing/pending" } >= asked + 2 }
        assertFalse(compose.has("pending-banner"))
        waiting = TWO
        compose.settledUntil { compose.has("pending-banner") }
        assertEquals(compose.activity.getString(CoreR.string.pending_device_named, "Sara's iPhone"), text())
    }

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun enterCodeOpensApprove() {
        answerWaiting()
        compose.setContent { RemoteForTest(home, RemoteWindow.RazrFoldCover.info) }
        compose.settledUntil { compose.has("pending-enter") }
        compose.onNode(hasTestTag("pending-enter")).performClick()
        compose.settledUntil { compose.has("approve-code") }
    }

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun aTappedNotificationOpensApprove() {
        compose.setContent { RemoteForTest(home, RemoteWindow.RazrFoldCover.info) }
        compose.settledUntil { compose.has("approve-tv") }
        ApproveRequests.handle(Intent().putExtra(ApproveRequests.EXTRA, true))
        compose.settledUntil { compose.has("approve-code") }
    }

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun aNotificationIsPostedWhenAllowed() {
        shadowOf(ApplicationProvider.getApplicationContext<Application>()).grantPermissions(Manifest.permission.POST_NOTIFICATIONS)
        answerWaiting()
        compose.setContent { RemoteForTest(home, RemoteWindow.RazrFoldCover.info) }
        compose.settledUntil { shadowOf(notifications).allNotifications.isNotEmpty() }
        val posted = shadowOf(notifications).allNotifications.single()
        assertEquals(SignInNotifications.CHANNEL, posted.channelId)
        assertEquals(
            compose.activity.getString(CoreR.string.pending_notification_tv) + ": Living room TV",
            posted.extras.getCharSequence("android.title").toString(),
        )
        assertEquals(compose.activity.getString(CoreR.string.pending_channel), notifications.getNotificationChannel(SignInNotifications.CHANNEL).name)
        // tapping it opens the app on Approve
        val tap = shadowOf(posted.contentIntent).savedIntent
        assertTrue(tap.getBooleanExtra(ApproveRequests.EXTRA, false))
    }

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun noNotificationWithoutThePermissionAndNoAsking() {
        shadowOf(ApplicationProvider.getApplicationContext<Application>()).denyPermissions(Manifest.permission.POST_NOTIFICATIONS)
        answerWaiting()
        compose.setContent { RemoteForTest(home, RemoteWindow.RazrFoldCover.info) }
        compose.settledUntil { compose.has("pending-banner") }
        assertTrue("no notification", shadowOf(notifications).allNotifications.isEmpty())
        assertNull("the permission is never asked for", shadowOf(compose.activity).lastRequestedPermission)
    }

    @Test
    fun theBannersStateFollowsTheList() {
        val s = PendingSignInsState()
        val tv = PendingSignIn("a", clientApp = "tv", device = "tv", deviceLabel = "Living room TV")
        val phone = PendingSignIn("b", clientApp = "player", device = "phone")
        assertEquals(listOf(tv), s.update(listOf(tv)))
        assertEquals(tv, s.banner)
        s.dismiss()
        assertNull(s.banner)
        assertEquals("seen once: no second notification", emptyList<PendingSignIn>(), s.update(listOf(tv)))
        assertNull("still dismissed", s.banner)
        assertEquals(listOf(phone), s.update(listOf(phone, tv)))
        assertEquals("a new one shows", phone, s.banner)
        s.update(emptyList())
        assertNull(s.banner)
    }

    private fun shoot(name: String, fontScale: Float = 1f) {
        answerWaiting()
        compose.setContent { RemoteForTest(home, RemoteWindow.RazrFoldCover.info, fontScale) }
        compose.settledUntil { compose.has("pending-banner") && compose.has("group-fingerprint:nas_partial_read") }
        compose.waitForIdle()
        val width = compose.onRoot().fetchSemanticsNode().size.width.toFloat()
        listOf("pending-enter", "pending-dismiss", "approve-tv").forEach { tag ->
            val b = compose.onAllNodes(hasTestTag(tag)).fetchSemanticsNodes().single().boundsInRoot
            assertTrue("$tag inside the screen: $b", b.left >= 0f && b.right <= width)
        }
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun bannerRazrEnglish() = shoot("pending_razrfoldcover_dark_en")

    @Test @Config(qualifiers = "ar-w393dp-h916dp-440dpi")
    fun bannerRazrArabic() = shoot("pending_razrfoldcover_dark_ar")

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun bannerRazrEnglishLargestText() = shoot("pending_razrfoldcover_dark_en_font200", 2f)

    @Test @Config(qualifiers = "ar-w393dp-h916dp-440dpi")
    fun bannerRazrArabicLargestText() = shoot("pending_razrfoldcover_dark_ar_font200", 2f)

    private companion object {
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
        const val TV = """{"count":1,"requests":[{"id":"r1","client_app":"tv","platform":"android","device":"tv","device_label":"Living room TV","created_at":"2026-11-03T10:00:00Z","expires_at":"2026-11-03T10:10:00Z"}]}"""
        const val TWO = """{"count":2,"requests":[{"id":"r2","client_app":"player","platform":"ios","device":"phone","device_label":"Sara's iPhone","created_at":"2026-11-03T10:01:00Z","expires_at":"2026-11-03T10:11:00Z"},{"id":"r1","client_app":"tv","platform":"android","device":"tv","device_label":"Living room TV","created_at":"2026-11-03T10:00:00Z","expires_at":"2026-11-03T10:10:00Z"}]}"""
        const val NONE = """{"count":0,"requests":[]}"""
    }
}
