package com.hamzalabs.maktaba.core

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.LayoutDirection
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The error screen a player shows for the home's raw answer the owner saw: the plain title and
 * sentence and the actions that fit, the home's words only under Details, mirrored right to left.
 */
@RunWith(AndroidJUnit4::class)
class ErrorStateTest {
    @get:Rule
    val rule = createComposeRule()

    private val renditionGone =
        """{"type":"https://maktaba.invalid/problems/rendition-not-found","title":"rendition index not on disk","status":404,"detail":"open /Volumes/x/index.m3u8: no such file or directory"}"""

    private var retried = 0
    private var signedIn = 0

    private fun show(failure: Failure, rtl: Boolean = false) {
        rule.setContent {
            CompositionLocalProvider(LocalLayoutDirection provides if (rtl) LayoutDirection.Rtl else LayoutDirection.Ltr) {
                MaktabaTheme(ThemeMode.Dark) {
                    ErrorState(
                        failure,
                        title = "This video can't play right now",
                        video = true,
                        onRetry = { retried++ },
                        onSignIn = { signedIn++ },
                        onChooseHome = {},
                        onBack = {},
                    )
                }
            }
        }
    }

    /** Every text on screen, as a person would read it. */
    private fun shownText(): String = rule.onAllNodesWithText("", substring = true, useUnmergedTree = true)
        .fetchSemanticsNodes()
        .flatMap { it.config.getOrNull(SemanticsProperties.Text).orEmpty() }
        .joinToString("\n") { it.text }

    @Test
    fun aStreamNotReadyReadsPlainlyWithNoJson() {
        show(Failure.of(ApiException.Status(404, renditionGone)))
        rule.onNodeWithText("This video can't play right now").assertIsDisplayed()
        rule.onNodeWithText("Your home is still preparing this video. Try again in a moment.").assertIsDisplayed()
        val text = shownText()
        assertFalse(text, text.contains("{"))
        assertFalse(text, text.contains("rendition index"))
        assertFalse(text, text.contains("/Volumes"))
        assertFalse(text, text.contains("The home answered"))

        rule.onNodeWithTag("error-action-TryAgain").performClick()
        assertEquals(1, retried)
        rule.onNodeWithTag("error-action-Back").assertIsDisplayed()

        // the home's own words, only when asked for
        rule.onNodeWithTag("error-details-toggle").performClick()
        rule.onNodeWithTag("error-details").assertIsDisplayed()
        assertTrue(shownText().contains("rendition index not on disk"))
    }

    @Test
    fun aSignInThatEndedOffersSigningInFirst() {
        show(Failure.of(ApiException.SignedOut))
        rule.onNodeWithText("Your sign-in has ended. Sign in again to keep watching.").assertIsDisplayed()
        rule.onNodeWithTag("error-action-SignIn").performClick()
        assertEquals(1, signedIn)
        assertTrue(rule.onAllNodesWithText("Try again").fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun anUnreachableHomeOffersAnother() {
        show(Failure.of(ApiException.Transport("connect timed out")))
        rule.onNodeWithTag("error-action-TryAgain").assertIsDisplayed()
        rule.onNodeWithTag("error-action-OtherHome").assertIsDisplayed()
        assertFalse(shownText().contains("connect timed out"))
    }

    @Test
    fun rightToLeftThePrimaryActionIsOnTheRight() {
        show(Failure.of(ApiException.Status(500, "{}")), rtl = true)
        val tryAgain = rule.onNodeWithTag("error-action-TryAgain").getBoundsInRoot()
        val back = rule.onNodeWithTag("error-action-Back").getBoundsInRoot()
        assertTrue("Try again ($tryAgain) is to the right of Back ($back)", tryAgain.left > back.left)
    }
}
