package com.hamzalabs.maktaba.tv

import android.graphics.Bitmap
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.hamzalabs.maktaba.core.permissions.AppPermission
import com.hamzalabs.maktaba.core.permissions.AppPermissions
import com.hamzalabs.maktaba.core.permissions.PermissionRow
import com.hamzalabs.maktaba.core.permissions.PermissionState
import com.hamzalabs.maktaba.tv.permissions.TvPermissionsScreen
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

/** The TV walkthrough with the remote: the button to press has the focus, and the centre key presses it. */
@RunWith(AndroidJUnit4::class)
class TvPermissionsScreenTest {
    @get:Rule
    val rule = createComposeRule()

    private val net = AppPermissions.LocalNetwork

    private fun show(state: PermissionState, onAction: (AppPermission) -> Unit = {}, onContinue: () -> Unit = {}) {
        rule.setContent {
            MaktabaTvTheme { TvPermissionsScreen(listOf(PermissionRow(net, state)), onAction, onContinue) }
        }
        rule.waitForIdle()
    }

    private fun centre() {
        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(android.view.KeyEvent.KEYCODE_DPAD_CENTER)
        rule.waitForIdle()
    }

    private fun down() {
        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(android.view.KeyEvent.KEYCODE_DPAD_DOWN)
        rule.waitForIdle()
    }

    /** Keeps a picture of the screen in the app's files (adb pulls it for the report). */
    private fun keep(name: String) {
        val bitmap = rule.onRoot().captureToImage().asAndroidBitmap()
        val dir = InstrumentationRegistry.getInstrumentation().targetContext.getExternalFilesDir(null) ?: return
        File(dir, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Test
    fun allowHasTheFocusAndTheCentreKeyAsks() {
        val asked = mutableListOf<AppPermission>()
        show(PermissionState.NotAsked, onAction = { asked += it })
        rule.onNodeWithText("Before you start").assertIsDisplayed()
        rule.onNodeWithText("Not asked yet").assertIsDisplayed()
        rule.onNodeWithTag("tv-permission-local-network-ask").assertIsFocused()
        keep("tv-walkthrough-not-asked")
        centre()
        assertEquals(listOf(net), asked)
    }

    @Test
    fun downReachesNotNow() {
        var continued = false
        show(PermissionState.Denied, onContinue = { continued = true })
        rule.onNodeWithText("Without it, only a home's internet address works.").assertIsDisplayed()
        down()
        rule.onNodeWithTag("tv-permissions-continue").assertIsFocused()
        centre()
        assertEquals(true, continued)
    }

    @Test
    fun refusedForGoodOffersSettings() {
        val acted = mutableListOf<AppPermission>()
        show(PermissionState.DeniedPermanently, onAction = { acted += it })
        rule.onNodeWithText("Not allowed, and Android will not ask again").assertIsDisplayed()
        rule.onNodeWithTag("tv-permission-local-network-settings").assertIsFocused()
        keep("tv-walkthrough-denied-permanently")
        centre()
        assertEquals(listOf(net), acted)
    }

    @Test
    fun allowedFocusesContinue() {
        show(PermissionState.Granted)
        rule.onNodeWithText("Allowed").assertIsDisplayed()
        rule.onNodeWithTag("tv-permission-local-network-ask").assertDoesNotExist()
        rule.onNodeWithTag("tv-permissions-continue").assertIsFocused()
        keep("tv-walkthrough-allowed")
    }
}
