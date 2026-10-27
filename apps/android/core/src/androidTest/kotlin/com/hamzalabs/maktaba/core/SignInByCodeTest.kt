package com.hamzalabs.maktaba.core

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.LayoutDirection
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.api.PairingRequest
import com.hamzalabs.maktaba.core.ui.ApproveCard
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.QrCode
import com.hamzalabs.maktaba.core.ui.ThemeMode
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** The approve card (story 42.6): what is asking, what approving means, and an explicit Approve. */
@RunWith(AndroidJUnit4::class)
class SignInByCodeTest {
    @get:Rule
    val rule = createComposeRule()

    private val now = 1_800_000_000_000L
    private val request = PairingRequest(
        code = "ABCD-1234",
        clientApp = "player",
        platform = "android",
        device = "phone",
        deviceLabel = "Amina's Pixel",
        expiresAt = "2027-01-15T08:10:00Z",
    )

    private fun show(request: PairingRequest?, rtl: Boolean = false, onApprove: () -> Unit = {}, onCancel: () -> Unit = {}) {
        rule.setContent {
            CompositionLocalProvider(LocalLayoutDirection provides if (rtl) LayoutDirection.Rtl else LayoutDirection.Ltr) {
                MaktabaTheme(ThemeMode.Dark) {
                    ApproveCard("ABCD1234", request, "amina", "Darwish home", busy = false, onApprove = onApprove, onCancel = onCancel, now = { now })
                }
            }
        }
    }

    @Test
    fun theCardSaysWhatIsAskingAndWhoItWillBe() {
        var approved = 0
        show(request, onApprove = { approved++ })
        rule.onNodeWithText("Amina's Pixel").assertIsDisplayed()
        rule.onNodeWithText("Player · Android · Phone").assertIsDisplayed()
        rule.onNodeWithText("Code ABCD-1234").assertIsDisplayed()
        rule.onNodeWithText("amina", substring = true).assertIsDisplayed()
        assertEquals("nothing is approved by looking", 0, approved)
        rule.onNodeWithTag("approve-button").performClick()
        assertEquals(1, approved)
    }

    @Test
    fun anOlderHomeThatCannotSayWhatAsksIsWarnedOf() {
        show(null)
        rule.onNodeWithText("A device").assertIsDisplayed()
        rule.onNodeWithText("cannot say which device is asking", substring = true).assertIsDisplayed()
    }

    @Test
    fun cancelLeavesWithoutApproving() {
        var approved = false
        var cancelled = false
        show(request, rtl = true, onApprove = { approved = true }, onCancel = { cancelled = true })
        rule.onNodeWithText("Cancel").performClick()
        assertEquals(true, cancelled)
        assertEquals(false, approved)
    }

    @Test
    fun theSignInCodesQrIsDrawn() {
        rule.setContent { MaktabaTheme(ThemeMode.Light) { QrCode("http://192.168.1.5:8088/pair?code=ABCD-1234", "QR") } }
        rule.onNodeWithTag("qr-code").assertIsDisplayed()
    }
}
