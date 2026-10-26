package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.tv.permissions.AppPermission
import com.hamzalabs.maktaba.tv.permissions.Need
import com.hamzalabs.maktaba.tv.permissions.PermissionAction
import com.hamzalabs.maktaba.tv.permissions.PermissionMemory
import com.hamzalabs.maktaba.tv.permissions.PermissionPrefs
import com.hamzalabs.maktaba.tv.permissions.PermissionRow
import com.hamzalabs.maktaba.tv.permissions.PermissionState
import com.hamzalabs.maktaba.tv.permissions.TvPermissions
import com.hamzalabs.maktaba.tv.permissions.Walkthrough
import com.hamzalabs.maktaba.tv.permissions.action
import com.hamzalabs.maktaba.tv.permissions.permissionState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TvPermissionsTest {
    private val net = TvPermissions.LocalNetwork
    private val optional = AppPermission("other", "android.permission.RECORD_AUDIO", 23, Need.Optional)

    @Test
    fun theTvAsksOnlyForTheLocalNetworkOnAndroid17AndLater() {
        assertEquals(listOf("android.permission.ACCESS_LOCAL_NETWORK"), TvPermissions.all.map { it.permission })
        assertEquals(37, net.sinceSdk)
        assertEquals(Need.Required, net.need)
    }

    @Test
    fun theStateFollowsWhatAndroidSays() {
        assertEquals(PermissionState.NotApplicable, permissionState(36, net, granted = false, asked = false, rationale = false))
        assertEquals(PermissionState.NotAsked, permissionState(37, net, granted = false, asked = false, rationale = false))
        assertEquals(PermissionState.Granted, permissionState(37, net, granted = true, asked = true, rationale = false))
        assertEquals(PermissionState.Denied, permissionState(37, net, granted = false, asked = true, rationale = true))
        assertEquals(PermissionState.DeniedPermanently, permissionState(37, net, granted = false, asked = true, rationale = false))
    }

    @Test
    fun theButtonAsksWhileAndroidWillElseItOpensSettings() {
        assertEquals(PermissionAction.Ask, PermissionState.NotAsked.action)
        assertEquals(PermissionAction.Ask, PermissionState.Denied.action)
        assertEquals(PermissionAction.OpenSettings, PermissionState.DeniedPermanently.action)
        assertEquals(PermissionAction.None, PermissionState.Granted.action)
    }

    @Test
    fun theWalkthroughShowsOnFirstRunAndWhileARequiredOneIsMissing() {
        fun rows(s: PermissionState) = listOf(PermissionRow(net, s))
        assertTrue(Walkthrough.shouldShow(seen = false, rows = rows(PermissionState.NotAsked), dismissed = false))
        assertTrue(Walkthrough.shouldShow(seen = false, rows = rows(PermissionState.Granted), dismissed = false))
        assertFalse(Walkthrough.shouldShow(seen = true, rows = rows(PermissionState.Granted), dismissed = false))
        assertTrue(Walkthrough.shouldShow(seen = true, rows = rows(PermissionState.Denied), dismissed = false))
        assertTrue(Walkthrough.shouldShow(seen = true, rows = rows(PermissionState.DeniedPermanently), dismissed = false))
        assertFalse("put away with Not now", Walkthrough.shouldShow(seen = true, rows = rows(PermissionState.Denied), dismissed = true))
        assertFalse("an Android TV before 17 has none of them", Walkthrough.shouldShow(seen = false, rows = rows(PermissionState.NotApplicable), dismissed = false))
    }

    @Test
    fun anOptionalOneDoesNotBringItBack() {
        val rows = listOf(PermissionRow(net, PermissionState.Granted), PermissionRow(optional, PermissionState.Denied))
        assertFalse(Walkthrough.shouldShow(seen = true, rows = rows, dismissed = false))
    }

    @Test
    fun theMemoryKeepsSeenAndAsked() {
        val map = mutableMapOf<String, String>()
        val prefs = object : PermissionPrefs {
            override fun get(key: String) = map[key]
            override fun put(key: String, value: String?) {
                if (value == null) map.remove(key) else map[key] = value
            }
        }
        val m = PermissionMemory(prefs)
        assertFalse(m.seen)
        m.seen = true
        m.markAsked(net)
        assertTrue(PermissionMemory(prefs).seen)
        assertTrue(PermissionMemory(prefs).asked(net))
        assertFalse(PermissionMemory(prefs).asked(optional))
    }
}
