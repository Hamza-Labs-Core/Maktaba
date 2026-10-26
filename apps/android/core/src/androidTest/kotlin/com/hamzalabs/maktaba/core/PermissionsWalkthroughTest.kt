package com.hamzalabs.maktaba.core

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.unit.LayoutDirection
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.permissions.AppPermission
import com.hamzalabs.maktaba.core.permissions.AppPermissions
import com.hamzalabs.maktaba.core.permissions.PermissionRow
import com.hamzalabs.maktaba.core.permissions.PermissionState
import com.hamzalabs.maktaba.core.permissions.PermissionsWalkthrough
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import android.content.Context
import android.os.Build
import androidx.compose.material3.Text
import androidx.test.platform.app.InstrumentationRegistry
import com.hamzalabs.maktaba.core.permissions.PermissionsGate
import org.junit.Assert.assertEquals
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class PermissionsWalkthroughTest {
    @get:Rule
    val rule = createComposeRule()

    private val net = AppPermissions.LocalNetwork

    private fun show(state: PermissionState, rtl: Boolean = false, onAction: (AppPermission) -> Unit = {}, onContinue: () -> Unit = {}) {
        rule.setContent {
            CompositionLocalProvider(LocalLayoutDirection provides if (rtl) LayoutDirection.Rtl else LayoutDirection.Ltr) {
                MaktabaTheme(ThemeMode.Dark) {
                    PermissionsWalkthrough(MaktabaApp.Player, listOf(PermissionRow(net, state)), onAction, onContinue, onLearnMore = null)
                }
            }
        }
    }

    @Test
    fun notAskedOffersAllowAndNotNow() {
        val asked = mutableListOf<AppPermission>()
        var continued = false
        show(PermissionState.NotAsked, onAction = { asked += it }, onContinue = { continued = true })
        rule.onNodeWithText("Before you start").performScrollTo().assertIsDisplayed()
        rule.onNodeWithText("Local network").performScrollTo().assertIsDisplayed()
        rule.onNodeWithText("Required").performScrollTo().assertIsDisplayed()
        rule.onNodeWithTag("permission-local-network-state").performScrollTo().assertIsDisplayed()
        rule.onNodeWithText("Not asked yet").performScrollTo().assertIsDisplayed()
        rule.onNodeWithTag("permission-local-network-ask").performScrollTo().performClick()
        assertEquals(listOf(net), asked)
        rule.onNodeWithText("Not now").performScrollTo().performClick()
        assertEquals(true, continued)
    }

    @Test
    fun refusedForGoodOpensSettingsAndSaysWhatIsLost() {
        val acted = mutableListOf<AppPermission>()
        show(PermissionState.DeniedPermanently, onAction = { acted += it })
        rule.onNodeWithText("Not allowed, and Android will not ask again").performScrollTo().assertIsDisplayed()
        rule.onNodeWithText("Without it, only a home's internet address works.").performScrollTo().assertIsDisplayed()
        rule.onNodeWithTag("permission-local-network-ask").assertDoesNotExist()
        rule.onNodeWithTag("permission-local-network-settings").performScrollTo().performClick()
        assertEquals(listOf(net), acted)
    }

    @Test
    fun allowedSaysSoAndContinues() {
        show(PermissionState.Granted)
        rule.onNodeWithText("Allowed").performScrollTo().assertIsDisplayed()
        rule.onNodeWithTag("permission-local-network-ask").assertDoesNotExist()
        rule.onNodeWithTag("permission-local-network-settings").assertDoesNotExist()
        rule.onNodeWithText("Continue").performScrollTo().assertIsDisplayed()
    }

    @Test
    fun aRowFollowsItsStateWhenItChanges() {
        var state by mutableStateOf(PermissionState.Denied)
        rule.setContent {
            MaktabaTheme(ThemeMode.Light) {
                PermissionsWalkthrough(MaktabaApp.Remote, listOf(PermissionRow(net, state)), {}, {}, onLearnMore = null)
            }
        }
        rule.onNodeWithText("Not allowed").performScrollTo().assertIsDisplayed()
        rule.onNodeWithTag("permission-local-network-ask").performScrollTo().assertIsDisplayed()
        state = PermissionState.Granted
        rule.onNodeWithText("Allowed").performScrollTo().assertIsDisplayed()
        rule.onNodeWithTag("permission-local-network-ask").assertDoesNotExist()
    }

    @Test
    fun rightToLeftLaysItOut() {
        show(PermissionState.Denied, rtl = true)
        rule.onNodeWithTag("permissions-walkthrough").assertIsDisplayed()
        rule.onNodeWithTag("permission-local-network-ask").performScrollTo().assertIsDisplayed()
    }

    @Test
    fun theGateShowsOnFirstRunAndNotNowRevealsTheApp() {
        // Android 17 and later: the local network permission is a runtime one this test app has not been given
        assumeTrue(Build.VERSION.SDK_INT >= 37)
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        context.getSharedPreferences("permissions", Context.MODE_PRIVATE).edit().clear().commit()
        rule.setContent {
            MaktabaTheme(ThemeMode.Light) {
                PermissionsGate(MaktabaApp.Player) { Text("the app") }
            }
        }
        rule.onNodeWithTag("permissions-walkthrough").assertIsDisplayed()
        rule.onNodeWithText("the app").assertDoesNotExist()
        rule.onNodeWithTag("permissions-continue").performScrollTo().performClick()
        rule.onNodeWithTag("permissions-walkthrough").assertDoesNotExist()
        rule.onNodeWithText("the app").assertIsDisplayed()
        assertEquals(
            "seen is kept",
            "1",
            context.getSharedPreferences("permissions", Context.MODE_PRIVATE).getString("permissions.walkthrough_seen", null),
        )
    }

    @Test
    fun theGateStaysAwayWhenThisAndroidHasNoneOfThePermissions() {
        assumeTrue(Build.VERSION.SDK_INT < 37)
        InstrumentationRegistry.getInstrumentation().targetContext
            .getSharedPreferences("permissions", Context.MODE_PRIVATE).edit().clear().commit()
        rule.setContent {
            MaktabaTheme(ThemeMode.Light) {
                PermissionsGate(MaktabaApp.Player) { Text("the app") }
            }
        }
        rule.onNodeWithTag("permissions-walkthrough").assertDoesNotExist()
        rule.onNodeWithText("the app").assertIsDisplayed()
    }
}
