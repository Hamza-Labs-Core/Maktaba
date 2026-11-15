package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performSemanticsAction
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.diagnostics.CrashConsent
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.core.ui.DiagnosticsHost
import com.hamzalabs.maktaba.tv.ui.screens.AccountScreen
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import com.hamzalabs.maktaba.core.parental.ParentalState
import kotlinx.coroutines.runBlocking
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer

/**
 * Epic 53 on the Android TV (stories 53.14 T10/T12, 53.15 §3): the crash-report question once
 * per home, its answer kept, and Report a problem from Account, a form a remote can send
 * (categories and Send; no time to pick on a TV). Account's Watch a broadcast (story 50.27), not
 * under a parental limit.
 */
@RunWith(AndroidJUnit4::class)
@Config(qualifiers = "w960dp-h540dp-xhdpi")
class DiagnosticsTvTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private class Tokens : TokenStore {
        override val access: String? = null
        override val refresh: String? = null
        override fun save(tokens: AuthTokens) {}
        override fun clear() {}
    }

    private val home = Home(url = "http://192.168.50.177:8088", name = "Test Home", id = "tv-test-home")
    private val client = ApiClient("http://192.168.50.177:8088/".toHttpUrl(), Tokens())
    private var allowed = false

    @Before
    fun ask() {
        allowed = CrashConsent.questionAllowed
        CrashConsent.questionAllowed = true
    }

    @After
    fun restore() {
        CrashConsent.questionAllowed = allowed
    }

    private fun shows(text: String) = compose.onAllNodes(hasText(text), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun theQuestionIsAskedOnceAndNotNowLeavesItOff() {
        val app = ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        compose.setContent { MaktabaTvTheme { DiagnosticsHost(client, home, tv = true) { AccountScreen(app, User(id = "u1", username = "viewer")) } } }
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Not now") }
        compose.onNodeWithTag("crash-question-no", useUnmergedTree = true).performSemanticsAction(SemanticsActions.OnClick)
        compose.waitUntil(5_000) { compose.waitForIdle(); !shows("Not now") }
        assertEquals(false, CrashConsent(app).answer(home))
    }

    @Test
    fun reportAProblemFromAccountIsARemoteForm() {
        val app = ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        CrashConsent(app).set(false, home)
        compose.setContent { MaktabaTvTheme { DiagnosticsHost(client, home, tv = true) { AccountScreen(app, User(id = "u1", username = "viewer")) } } }
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("Report a problem") }
        // story 58.14: an account with no limit has no managed line in Account
        compose.onNodeWithTag("parental-managed", useUnmergedTree = true).assertDoesNotExist()
        compose.onNodeWithTag("account-report").performSemanticsAction(SemanticsActions.OnClick)
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("What will be sent") }
        compose.onNodeWithTag("report-send", useUnmergedTree = true).assertExists()
        assertFalse("a TV picks no time", shows("Earlier today"))
    }

    @Test
    fun watchABroadcastIsInAccountAndNotUnderAParentalLimit() {
        val app = ApplicationProvider.getApplicationContext<MaktabaTVApp>()
        CrashConsent(app).set(false, home)
        val server = MockWebServer()
        server.enqueue(
            MockResponse.Builder().code(200).addHeader("Content-Type", "application/json")
                .body("""{"restricted":true,"max_tier":9,"allow_only":false,"version":1}""").build(),
        )
        server.start()
        try {
            val free = ParentalState(client)
            val limited = ParentalState(ApiClient(server.url("/"), Tokens()))
            runBlocking { limited.refresh() }
            assertEquals(true, limited.mine.value.limited)
            var parental by androidx.compose.runtime.mutableStateOf(free)
            compose.setContent {
                MaktabaTvTheme { DiagnosticsHost(client, home, tv = true) { AccountScreen(app, User(id = "u1", username = "viewer"), parental = parental) } }
            }
            compose.waitUntil(5_000) { compose.waitForIdle(); shows("Watch a broadcast") }
            compose.onNodeWithTag("account-watch-broadcast").performSemanticsAction(SemanticsActions.OnClick)
            compose.waitForIdle()
            assertEquals(true, app.guestOpen.value)
            app.guestOpen.value = false
            // a broadcast is outside the home's ratings: a limited sign-in has no way in
            parental = limited
            compose.waitUntil(5_000) { compose.waitForIdle(); !shows("Watch a broadcast") }
            compose.onNodeWithTag("account-watch-broadcast").assertDoesNotExist()
        } finally {
            app.guestOpen.value = false
            server.close()
        }
    }
}
