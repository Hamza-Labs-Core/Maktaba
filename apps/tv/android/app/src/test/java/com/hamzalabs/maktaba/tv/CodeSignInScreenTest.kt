package com.hamzalabs.maktaba.tv

import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.api.DeviceCode
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.screens.CodeSignInContent
import com.hamzalabs.maktaba.tv.ui.screens.Gutter
import com.hamzalabs.maktaba.tv.ui.screens.shownTimeLeft
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * The TV's sign-in code screen on a 1080p TV (960x540 dp at xhdpi), English and Arabic, in the
 * TV's one theme (dark): it says where to approve the code (the phones' "Approve a TV or
 * device"), shows the code, how long it lasts and a way to a new one, and once a phone read
 * the code, that the phone found this TV. Everything fits on the screen.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class CodeSignInScreenTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private var newCodes = 0

    private fun show(lookedUp: Boolean) {
        compose.setContent {
            MaktabaTvTheme {
                // as SignInScreen lays it out
                LibraryBackdrop {
                    Column(
                        Modifier.fillMaxSize().padding(horizontal = Gutter, vertical = 24.dp),
                        verticalArrangement = Arrangement.Center,
                    ) {
                        CodeSignInContent(
                            homeName = "Darwish home",
                            code = DeviceCode(code = "ABCD1234", deviceCode = "secret", verifyUrl = URL),
                            approveUrl = URL,
                            expiresInSec = 9 * 60 + 58L,
                            lookedUp = lookedUp,
                            renewed = false,
                            problem = null,
                            onNewCode = { newCodes++ },
                            onPassword = {},
                            onOtherHome = {},
                        )
                    }
                }
            }
        }
        compose.waitForIdle()
    }

    private fun node(tag: String): SemanticsNode =
        compose.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().single()

    private fun text(tag: String): String =
        node(tag).config.getOrNull(SemanticsProperties.Text).orEmpty().joinToString("") { it.text }

    private fun string(id: Int, vararg args: Any) = compose.activity.getString(id, *args)

    /** Every part of the screen whole inside the 1080p screen. */
    private fun assertFits() {
        val root = compose.onRoot().fetchSemanticsNode().size
        assertEquals("a 1080p TV", 1920 to 1080, root.width to root.height)
        for (tag in listOf("signin-where", "signin-code", "signin-status", "signin-expires", "signin-computer") + BUTTONS) {
            val b = node(tag).boundsInRoot
            assertTrue("$tag inside the screen: $b", b.left >= 0f && b.top >= 0f && b.right <= root.width && b.bottom <= root.height && b.width > 0f)
        }
        // each button's label on one line: the three are the same height
        val heights = BUTTONS.map { node(it).boundsInRoot.height }
        assertTrue("the buttons on one line each: $heights", heights.max() - heights.min() < 1f)
    }

    private fun shoot(lookedUp: Boolean, name: String) {
        show(lookedUp)
        assertFits()
        val approveTv = string(CoreR.string.approve_tv)
        assertTrue("the instruction names the phones' button: ${text("signin-where")}", text("signin-where").contains(approveTv))
        assertEquals(string(R.string.signin_code_expires, shownTimeLeft(598)), text("signin-expires"))
        val status = if (lookedUp) string(R.string.signin_code_looked_up, string(CoreR.string.approve_button)) else string(R.string.signin_code_waiting)
        assertEquals(status, text("signin-status"))
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    @Test @Config(qualifiers = TV)
    fun waitingEnglish() = shoot(lookedUp = false, "signin_code_tv_dark_en")

    @Test @Config(qualifiers = TV)
    fun phoneFoundItEnglish() = shoot(lookedUp = true, "signin_code_found_tv_dark_en")

    @Test @Config(qualifiers = "ar-$TV")
    fun waitingArabic() = shoot(lookedUp = false, "signin_code_tv_dark_ar")

    @Test @Config(qualifiers = "ar-$TV")
    fun phoneFoundItArabic() = shoot(lookedUp = true, "signin_code_found_tv_dark_ar")

    @Test @Config(qualifiers = TV)
    fun getANewCodeStartsOver() {
        show(lookedUp = false)
        compose.onNode(hasTestTag("signin-renew")).assert(hasText(string(R.string.signin_code_renew))).performSemanticsAction(SemanticsActions.OnClick)
        compose.waitForIdle()
        assertEquals(1, newCodes)
    }

    @Test
    fun timeLeftReadsMinutesAndSeconds() {
        assertEquals("9:58", shownTimeLeft(598))
        assertEquals("0:05", shownTimeLeft(5))
        assertEquals("10:00", shownTimeLeft(600))
    }

    private companion object {
        const val TV = "w960dp-h540dp-land-television-xhdpi"
        val BUTTONS = listOf("signin-renew", "signin-password", "signin-other-home")
        const val URL ="https://home.example/pair?code=ABCD-1234"
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
