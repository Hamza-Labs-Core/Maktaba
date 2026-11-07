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

/**
 * Epic 53 on the Android TV (stories 53.14 T10/T12, 53.15 §3): the crash-report question once
 * per home, its answer kept, and Report a problem from Account, a form a remote can send
 * (categories and Send; no time to pick on a TV).
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
        compose.onNodeWithTag("account-report").performSemanticsAction(SemanticsActions.OnClick)
        compose.waitUntil(5_000) { compose.waitForIdle(); shows("What will be sent") }
        compose.onNodeWithTag("report-send", useUnmergedTree = true).assertExists()
        assertFalse("a TV picks no time", shows("Earlier today"))
    }
}
