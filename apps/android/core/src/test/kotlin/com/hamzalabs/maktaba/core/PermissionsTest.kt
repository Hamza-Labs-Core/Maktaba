package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.permissions.AppPermission
import com.hamzalabs.maktaba.core.permissions.AppPermissions
import com.hamzalabs.maktaba.core.permissions.Need
import com.hamzalabs.maktaba.core.permissions.PermissionAction
import com.hamzalabs.maktaba.core.permissions.PermissionMemory
import com.hamzalabs.maktaba.core.permissions.PermissionRow
import com.hamzalabs.maktaba.core.permissions.PermissionState
import com.hamzalabs.maktaba.core.permissions.Walkthrough
import com.hamzalabs.maktaba.core.permissions.action
import com.hamzalabs.maktaba.core.permissions.permissionState
import com.hamzalabs.maktaba.core.permissions.permissionsHelpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PermissionsTest {
    private val net = AppPermissions.LocalNetwork
    private val optional = AppPermission("notifications", "android.permission.POST_NOTIFICATIONS", 33, Need.Optional)

    @Test
    fun theStateFollowsWhatAndroidSays() {
        assertEquals(PermissionState.NotApplicable, permissionState(36, net, granted = false, asked = false, rationale = false))
        assertEquals("below its API level the permission is not there, granted or not", PermissionState.NotApplicable, permissionState(36, net, granted = true, asked = true, rationale = true))
        assertEquals(PermissionState.NotAsked, permissionState(37, net, granted = false, asked = false, rationale = false))
        assertEquals(PermissionState.Granted, permissionState(37, net, granted = true, asked = false, rationale = false))
        assertEquals("refused once: Android shows a rationale and can ask again", PermissionState.Denied, permissionState(37, net, granted = false, asked = true, rationale = true))
        assertEquals("asked, refused, no rationale: Android will not ask again", PermissionState.DeniedPermanently, permissionState(37, net, granted = false, asked = true, rationale = false))
        assertEquals("granted wins over an old refusal (allowed in Settings)", PermissionState.Granted, permissionState(38, net, granted = true, asked = true, rationale = false))
    }

    @Test
    fun theButtonAsksWhileAndroidWillElseItOpensSettings() {
        assertEquals(PermissionAction.Ask, PermissionState.NotAsked.action)
        assertEquals(PermissionAction.Ask, PermissionState.Denied.action)
        assertEquals(PermissionAction.OpenSettings, PermissionState.DeniedPermanently.action)
        assertEquals(PermissionAction.None, PermissionState.Granted.action)
        assertEquals(PermissionAction.None, PermissionState.NotApplicable.action)
    }

    @Test
    fun theWalkthroughShowsOnFirstRunAndWhileARequiredOneIsMissing() {
        val notAsked = listOf(PermissionRow(net, PermissionState.NotAsked))
        val granted = listOf(PermissionRow(net, PermissionState.Granted))
        val denied = listOf(PermissionRow(net, PermissionState.Denied))
        val gone = listOf(PermissionRow(net, PermissionState.DeniedPermanently))

        assertTrue("first run", Walkthrough.shouldShow(seen = false, rows = notAsked, dismissed = false))
        assertTrue("first run, even with everything allowed", Walkthrough.shouldShow(seen = false, rows = granted, dismissed = false))
        assertFalse("seen, everything allowed", Walkthrough.shouldShow(seen = true, rows = granted, dismissed = false))
        assertTrue("seen, a required one refused", Walkthrough.shouldShow(seen = true, rows = denied, dismissed = false))
        assertTrue("seen, a required one refused for good", Walkthrough.shouldShow(seen = true, rows = gone, dismissed = false))
        assertTrue("seen, a required one never asked (allowed, then revoked in Settings)", Walkthrough.shouldShow(seen = true, rows = notAsked, dismissed = false))
        assertFalse("put away with Not now", Walkthrough.shouldShow(seen = true, rows = denied, dismissed = true))
    }

    @Test
    fun anOptionalOneDoesNotBringItBack() {
        val rows = listOf(PermissionRow(net, PermissionState.Granted), PermissionRow(optional, PermissionState.DeniedPermanently))
        assertTrue(Walkthrough.shouldShow(seen = false, rows = rows, dismissed = false))
        assertFalse(Walkthrough.shouldShow(seen = true, rows = rows, dismissed = false))
        assertFalse(Walkthrough.missingRequired(rows))
    }

    @Test
    fun anAndroidWithNoneOfThePermissionsHasNoWalkthrough() {
        val rows = listOf(PermissionRow(net, PermissionState.NotApplicable))
        assertFalse(Walkthrough.shouldShow(seen = false, rows = rows, dismissed = false))
        assertTrue(Walkthrough.shown(rows).isEmpty())
        assertFalse("not applicable is not missing", Walkthrough.missingRequired(rows))
    }

    @Test
    fun theListLeavesOutWhatThisAndroidDoesNotHave() {
        val rows = listOf(PermissionRow(net, PermissionState.Denied), PermissionRow(optional, PermissionState.NotApplicable))
        assertEquals(listOf(net), Walkthrough.shown(rows).map { it.permission })
    }

    @Test
    fun theMemoryKeepsSeenAndAsked() {
        val store = MapStore()
        val m = PermissionMemory(store)
        assertFalse(m.seen)
        assertFalse(m.asked(net))
        m.seen = true
        m.markAsked(net)
        val again = PermissionMemory(store)
        assertTrue("seen survives a restart", again.seen)
        assertTrue(again.asked(net))
        assertFalse("asked is per permission", again.asked(optional))
    }

    @Test
    fun thePhoneAppsAskOnlyForTheLocalNetwork() {
        assertEquals(listOf("android.permission.ACCESS_LOCAL_NETWORK"), AppPermissions.phoneApps.map { it.permission })
        assertEquals(Need.Required, net.need)
        assertEquals(37, net.sinceSdk)
    }

    @Test
    fun learnMoreOpensTheHomesHelp() {
        assertEquals("http://192.168.1.20:8088/help/android-apps#permissions", permissionsHelpUrl("http://192.168.1.20:8088/"))
        assertEquals("https://home.example/help/android-tv#permissions", permissionsHelpUrl("https://home.example", "android-tv"))
    }
}
