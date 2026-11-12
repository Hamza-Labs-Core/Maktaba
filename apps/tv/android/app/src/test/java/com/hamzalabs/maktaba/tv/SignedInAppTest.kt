package com.hamzalabs.maktaba.tv

import android.app.Application
import android.content.Intent
import androidx.activity.ComponentActivity
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.performSemanticsAction
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.tv.ui.SignedInApp
import com.hamzalabs.maktaba.tv.ui.TvTab
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import com.hamzalabs.maktaba.tv.ui.tvTabs
import kotlinx.coroutines.flow.MutableStateFlow
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import java.util.Base64
import java.util.Collections

/**
 * Story 51.11 T12: the TV's tab bar with Live TV off on the home. A viewer has no Live TV tab,
 * even with channels listed; an admin keeps it, and the tab says Live TV is off with the way to
 * turn it on. Against a MockWebServer home; the app's log goes to the report (robolectric.logging).
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w960dp-h540dp-xhdpi")
class SignedInAppTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private lateinit var server: MockWebServer
    private val paths: MutableList<String> = Collections.synchronizedList(mutableListOf())
    private val features = MutableStateFlow(Capabilities.AllOn)

    /**
     * Every call the signed-in app sends, counted ([HomeCalls.ofTheWholeApp]): `waitForIdle()`
     * returns only once each has been answered and what the answer started has run, so a count
     * of what the home was asked, read after it, cannot still be moving.
     */
    private val calls = HomeCalls.ofTheWholeApp()

    private class Tokens(var a: String?, var r: String?) : TokenStore {
        override val access get() = a
        override val refresh get() = r
        override fun save(tokens: AuthTokens) { a = tokens.accessToken; r = tokens.refreshToken }
        override fun clear() { a = null; r = null }
    }

    private val token: String = Base64.getUrlEncoder().withoutPadding().let { enc ->
        enc.encodeToString("""{"alg":"RS256"}""".toByteArray()) + "." +
            enc.encodeToString("""{"exp":${System.currentTimeMillis() / 1000 + 86_400}}""".toByteArray()) + ".sig"
    }

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                paths += request.url.encodedPath
                return when (request.url.encodedPath) {
                    "/api/channels/now" -> json(200, """{"server_time":"2026-11-01T18:00:00Z","channels":[{"channel":{"id":"c1","number":1,"name":"One"}}]}""")
                    else -> json(404, "{}")
                }
            }
        }
        server.start()
        compose.registerIdlingResource(calls.idling)
    }

    @After
    fun stop() = server.close()

    private fun client() = ApiClient(server.url("/"), Tokens(token, "r1"), calls.http)

    @After
    fun unregister() = compose.unregisterIdlingResource(calls.idling)

    private fun signedIn() {
        val app = ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        val client = client()
        compose.setContent {
            MaktabaTvTheme { SignedInApp(app, User(id = "u1", username = "layla"), client, features) }
        }
    }

    private fun shows(text: String) = compose.onAllNodes(hasText(text), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun withEverythingOnAChannelShowsTheLiveTvTab() {
        signedIn()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); shows("Live TV") }
    }

    @Test
    fun aViewerHasNoLiveTvTabWhileItIsOff() {
        features.value = Capabilities(off = setOf(Feature.LIVE_TV))
        signedIn()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); shows("Home") }
        compose.waitForIdle()
        assertFalse("no Live TV tab", shows("Live TV"))
        assertFalse("nothing asks for channels while Live TV is off", "/api/channels/now" in paths)
    }

    @Test
    fun anAdminKeepsTheTabAndReadsWhy() {
        features.value = Capabilities(off = setOf(Feature.LIVE_TV), admin = true)
        signedIn()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); shows("Live TV") }
        // a TV tab is chosen when it takes focus (the D-pad)
        compose.onNodeWithText("Live TV").performSemanticsAction(SemanticsActions.RequestFocus)
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); shows("Live TV is off on this home.") }
        compose.onNodeWithTag("feature-turn-on").assertExists()
        assertTrue(shows("Turn it on in Components"))
    }

    /** The admin's line on the Live TV tab: its button opens the home's Components page in the browser. */
    @Test
    fun theAdminsButtonOpensComponents() {
        features.value = Capabilities(off = setOf(Feature.LIVE_TV), admin = true)
        signedIn()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); shows("Live TV") }
        compose.onNodeWithText("Live TV").performSemanticsAction(SemanticsActions.RequestFocus)
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); shows("Turn it on in Components") }
        compose.onNodeWithTag("feature-turn-on").performSemanticsAction(SemanticsActions.OnClick)
        val started = requireNotNull(shadowOf(ApplicationProvider.getApplicationContext<Application>()).nextStartedActivity)
        assertEquals(Intent.ACTION_VIEW, started.action)
        assertEquals(server.url("/settings/components").toString(), started.dataString)
        compose.waitForIdle()
        assertFalse("nothing asks for channels while Live TV is off", "/api/channels/now" in paths)
    }

    /** The same line in Arabic. */
    @Test
    @Config(qualifiers = "ar-w960dp-h540dp-xhdpi")
    fun theAdminsLineInArabic() {
        features.value = Capabilities(off = setOf(Feature.LIVE_TV), admin = true)
        signedIn()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); shows("البث المباشر") }
        compose.onNodeWithText("البث المباشر").performSemanticsAction(SemanticsActions.RequestFocus)
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); shows("البث المباشر متوقف على الجهاز الرئيسي.") }
        assertTrue(shows("شغّل من صفحة المكوّنات"))
        compose.onNodeWithTag("feature-off-${Feature.LIVE_TV}").assertExists()
    }

    /** The home answers `GET /api/me/parental` with a 13+ limit at [version], and records the read. */
    private fun aLimitedSignIn(version: java.util.concurrent.atomic.AtomicInteger) {
        val inner = server.dispatcher
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse =
                if (request.url.encodedPath == "/api/me/parental") {
                    paths += request.url.encodedPath
                    json(200, """{"restricted":true,"max_tier":13,"allow_only":false,"version":${version.get()}}""")
                } else {
                    inner.dispatch(request)
                }
        }
    }

    private fun homeReads() = synchronized(paths) { paths.count { it == "/api/home" } }
    private fun limitReads() = synchronized(paths) { paths.count { it == "/api/me/parental" } }

    /** The TV goes behind another app and comes back. */
    private fun leaveAndComeBack() {
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.STARTED)
        compose.activityRule.scenario.moveToState(androidx.lifecycle.Lifecycle.State.RESUMED)
    }

    /**
     * Epic 57 story 57.05: a guardian changed the sign-in's limit while the TV showed another
     * app. Back in the app, the limit's version has moved: Home reads again (what it showed was
     * read under the limit before) and the queue's window and the pictures go.
     *
     * Each count is read once the home's answers have all landed ([calls]). The first answer of
     * a limited sign-in this TV has no mark for is itself a change (what was kept before is not
     * trusted), so Home may read twice at the start: that second read has landed too before the
     * count is taken.
     */
    @Test
    fun aChangedParentalLimitReadsHomeAgainAndForgetsTheQueue() {
        val version = java.util.concurrent.atomic.AtomicInteger(1)
        aLimitedSignIn(version)
        signedIn()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); limitReads() >= 1 && homeReads() >= 1 }
        compose.waitForIdle()
        // the same limit when the TV comes back: nothing reads again
        val settled = homeReads()
        val askedFirst = limitReads()
        leaveAndComeBack()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); limitReads() > askedFirst }
        compose.waitForIdle()
        assertEquals("an unchanged limit reloads nothing", settled, homeReads())
        // the guardian lowers it; the TV comes back to the app
        version.set(2)
        val asked = limitReads()
        leaveAndComeBack()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); limitReads() > asked && homeReads() > settled }
        compose.waitForIdle()
        assertEquals("one change of the limit reads Home again once", settled + 1, homeReads())
        // and coming back once more under that limit reads nothing
        val askedAfter = limitReads()
        leaveAndComeBack()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); limitReads() > askedAfter }
        compose.waitForIdle()
        assertEquals("the changed limit, read again, reloads nothing", settled + 1, homeReads())
    }

    /**
     * A sign-in whose limit this TV remembers from the launch before, and it still holds: the
     * first answer of this launch is no change, and Home is read once.
     */
    @Test
    fun aRememberedLimitThatStillHoldsReadsHomeOnce() {
        val version = java.util.concurrent.atomic.AtomicInteger(1)
        aLimitedSignIn(version)
        val context = ApplicationProvider.getApplicationContext<Application>()
        val mark = com.hamzalabs.maktaba.core.parental.KeptLimit.InFile.of(context.filesDir, server.url("/"), "u1")
        kotlinx.coroutines.runBlocking { com.hamzalabs.maktaba.core.parental.ParentalState(client(), mark).refresh() }
        assertTrue("the launch before left its mark", mark.read()!!.limited)
        val asked = limitReads()
        signedIn()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); limitReads() > asked && homeReads() >= 1 }
        compose.waitForIdle()
        assertEquals("the first answer is the remembered limit: nothing reads again", 1, homeReads())
        leaveAndComeBack()
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); limitReads() > asked + 1 }
        compose.waitForIdle()
        assertEquals(1, homeReads())
    }

    @Test
    fun droppingWhatIsKeptForgetsTheQueuesWindow() {
        val queue = com.hamzalabs.maktaba.core.queue.PlayQueueModel({ _, _, _ -> 200 to "{}" }, "tv-test")
        queue.resume(com.hamzalabs.maktaba.core.queue.QueueDoc("q1", com.hamzalabs.maktaba.core.queue.QueueSource("library", "l1")))
        queue.staleSeen = true
        com.hamzalabs.maktaba.tv.ui.dropKeptForLimit(queue)
        assertEquals(null, queue.doc)
        assertFalse(queue.staleSeen)
    }

    /** Story 57.15: the player carries the video's library, and asks for the time with it. */
    @Test
    fun thePlayerOfAQueuedVideoCarriesItsLibrary() {
        val item = com.hamzalabs.maktaba.core.queue.QueueItem(pos = 0, id = "v1", title = "The Message", libraryId = "l1")
        val place = com.hamzalabs.maktaba.tv.ui.playerPlace(item, startMs = 42_000)
        assertEquals("l1", place.libraryId)
        assertEquals(42_000L, place.startMs)
        assertTrue(place.queued)
        assertEquals("no library named: the account's own rules", null, com.hamzalabs.maktaba.tv.ui.playerPlace(item.copy(libraryId = ""), 0).libraryId)
    }

    @Test
    fun thePlayerAsksForTheTimeOfTheVideosLibrary() {
        val timeAsks: MutableList<String?> = Collections.synchronizedList(mutableListOf())
        val inner = server.dispatcher
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when (request.url.encodedPath) {
                "/api/me/parental" -> json(200, """{"restricted":true,"max_tier":13,"allow_only":false,"version":1}""")
                "/api/me/parental/time" -> {
                    timeAsks += request.url.queryParameter("library_id")
                    json(200, """{"allowed":true,"limited":true,"remaining_seconds":1800}""")
                }
                else -> inner.dispatch(request)
            }
        }
        val app = ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        val client = client()
        val parental = com.hamzalabs.maktaba.core.parental.ParentalState(client)
        kotlinx.coroutines.runBlocking { parental.refresh() }
        compose.setContent {
            MaktabaTvTheme {
                com.hamzalabs.maktaba.tv.ui.screens.PlayerScreen(
                    app, client, "v1", "The Message", 0, onBack = {}, signedOut = {}, parental = parental, libraryId = "l1",
                )
            }
        }
        compose.waitUntil(STALL_MS) { compose.waitForIdle(); timeAsks.isNotEmpty() }
        assertEquals("a rule on this library alone counts", "l1", timeAsks.first())
    }

    @Test
    fun theTabListFollowsTheRule() {
        val viewer = Capabilities(off = setOf(Feature.LIVE_TV))
        val admin = Capabilities(off = setOf(Feature.LIVE_TV), admin = true)
        assertEquals(listOf(TvTab.Home, TvTab.Libraries, TvTab.Search, TvTab.Account), tvTabs(hasLive = true, viewer))
        assertEquals(TvTab.entries.toList(), tvTabs(hasLive = false, admin))
        assertEquals(TvTab.entries.toList(), tvTabs(hasLive = true))
    }
}
