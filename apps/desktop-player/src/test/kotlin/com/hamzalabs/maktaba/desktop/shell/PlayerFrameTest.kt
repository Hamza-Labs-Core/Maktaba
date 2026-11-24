package com.hamzalabs.maktaba.desktop.shell

import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.PlayerTheme
import com.hamzalabs.maktaba.desktop.Texts
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Story 13.15: the Player's own title bar and its menu (shell/PlayerFrame.kt). */
@OptIn(ExperimentalTestApi::class)
class PlayerFrameTest {
    @Test
    fun theDecoratedFrameNeedsTheJetBrainsRuntimeAndCanBeTurnedOff() {
        // the tests run on another JDK: the system's frame, with the bar as the page's first row
        assertFalse(decoratedFrameAvailable(emptyMap()))
        assertFalse(decoratedFrameAvailable(mapOf("MAKTABA_PLAIN_FRAME" to "1")))
    }

    @Test
    fun theMenuHoldsWhatTheMenuBarHeldAndActs() = runComposeUiTest {
        val done = mutableListOf<String>()
        var big by mutableStateOf(false)
        var lang by mutableStateOf(Lang.En)
        setContent {
            val texts = Texts(lang)
            PlayerTheme(texts, big = false) {
                FrameBar(
                    texts,
                    FrameMenu(
                        signedInLine = "Signed in as haddad on Living room",
                        approve = { done += "approve" },
                        crashReports = "Send crash reports to Living room" to false,
                        onCrashReports = { done += "crash:$it" },
                        signOut = { done += "signOut" },
                        switchHome = { done += "switchHome" },
                        big = big,
                        onBig = { big = it; done += "big:$it" },
                        lang = lang,
                        onLang = { lang = it },
                        help = { done += "help" },
                        updates = { done += "updates" },
                        reportProblem = { done += "report" },
                    ),
                    drawBackground = true,
                )
            }
        }
        onNodeWithText("Maktaba Player").assertExists()
        onNodeWithContentDescription("Menu").performClick()
        onNodeWithText("Signed in as haddad on Living room").assertIsNotEnabled()
        onNodeWithText("Approve a sign-in").performClick()
        onNodeWithContentDescription("Menu").performClick()
        onNodeWithText("Send crash reports to Living room").performClick()
        onNodeWithContentDescription("Menu").performClick()
        onNodeWithText("Check for updates").performClick()
        onNodeWithTag("frame-big-screen").performClick()
        onNodeWithTag("frame-help").performClick()
        assertEquals(listOf("approve", "crash:true", "updates", "big:true", "help"), done)
        // the language: the menu speaks it at once
        onNodeWithContentDescription("Menu").performClick()
        onNodeWithText("العربية").performClick()
        waitForIdle()
        onNodeWithContentDescription("القائمة").assertExists()
    }

    @Test
    fun aSignedOutPlayerOffersNoAccountActions() = runComposeUiTest {
        setContent {
            val texts = Texts(Lang.En)
            PlayerTheme(texts, big = false) { FrameBar(texts, FrameMenu(), drawBackground = true) }
        }
        onNodeWithContentDescription("Menu").performClick()
        onNodeWithText("Sign out").assertDoesNotExist()
        onNodeWithText("Report a problem…").assertDoesNotExist()
        onNodeWithText("Check for updates").assertExists()
    }

    @Test
    fun theDialogShowsItsTitleWordsAndButtonsInItsOwnStyle() = runComposeUiTest {
        var closed = false
        setContent {
            PlayerTheme(Texts(Lang.En), big = false) {
                MaktabaAlertDialog(
                    onDismissRequest = {},
                    title = { Text("Sign out of Living room?") },
                    text = { Text("You can sign in again with your password or a code.") },
                    dismissButton = { TextButton(onClick = {}) { Text("Cancel") } },
                    confirmButton = { Button(onClick = { closed = true }) { Text("Sign out") } },
                )
            }
        }
        onNodeWithTag("maktaba-dialog").assertExists()
        onNodeWithText("Sign out of Living room?").assertExists()
        onNodeWithText("Sign out").performClick()
        assertTrue(closed)
    }
}
