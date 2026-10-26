package com.hamzalabs.maktaba.tv.permissions

/**
 * The TV's permissions walkthrough model: the same rules as the phone apps'
 * (`apps/android/core/.../permissions/Permissions.kt`), kept here because this build does not
 * use the phone apps' core yet. Once it does, this file gives way to the core's. No Android
 * types: tested on the JVM.
 */

/** Whether the app works without a permission. */
enum class Need { Required, Optional }

/** Where a permission stands on this TV. */
enum class PermissionState {
    /** The person allowed it. */
    Granted,

    /** Refused, and Android still lets the app ask again. */
    Denied,

    /** Refused, and Android will not ask again: only Settings can turn it on. */
    DeniedPermanently,

    /** The app has not asked yet. */
    NotAsked,

    /** This Android version has no such permission (the app has what it needs). */
    NotApplicable,
}

/** What a permission's button does in its state. */
enum class PermissionAction { Ask, OpenSettings, None }

/** A permission the TV asks for: [id] names it in help (`android-tv#permissions`). */
data class AppPermission(val id: String, val permission: String, val sinceSdk: Int, val need: Need)

object TvPermissions {
    /**
     * Android 17 (API 37) and later: reaching a home on the local network (by its address or its
     * mDNS announcement) needs it. Android shows it as «Nearby devices». Required.
     */
    val LocalNetwork = AppPermission("local-network", "android.permission.ACCESS_LOCAL_NETWORK", 37, Need.Required)

    /** Everything the TV app asks for: no notifications, no background work, no microphone. */
    val all: List<AppPermission> = listOf(LocalNetwork)
}

/** Where [p] stands, from `checkSelfPermission` ([granted]), whether the app [asked], and the [rationale] flag. */
fun permissionState(sdk: Int, p: AppPermission, granted: Boolean, asked: Boolean, rationale: Boolean): PermissionState = when {
    sdk < p.sinceSdk -> PermissionState.NotApplicable
    granted -> PermissionState.Granted
    !asked -> PermissionState.NotAsked
    rationale -> PermissionState.Denied
    else -> PermissionState.DeniedPermanently
}

val PermissionState.action: PermissionAction
    get() = when (this) {
        PermissionState.NotAsked, PermissionState.Denied -> PermissionAction.Ask
        PermissionState.DeniedPermanently -> PermissionAction.OpenSettings
        PermissionState.Granted, PermissionState.NotApplicable -> PermissionAction.None
    }

val PermissionState.ok: Boolean get() = this == PermissionState.Granted || this == PermissionState.NotApplicable

data class PermissionRow(val permission: AppPermission, val state: PermissionState)

object Walkthrough {
    /** The rows the walkthrough lists: the permissions this Android version has. */
    fun shown(rows: List<PermissionRow>): List<PermissionRow> = rows.filter { it.state != PermissionState.NotApplicable }

    fun missingRequired(rows: List<PermissionRow>): Boolean = rows.any { it.permission.need == Need.Required && !it.state.ok }

    /**
     * Never when this Android has none of the permissions, nor after «Not now» until the app
     * next comes to the foreground ([dismissed]); else on first run and while a required one is missing.
     */
    fun shouldShow(seen: Boolean, rows: List<PermissionRow>, dismissed: Boolean): Boolean {
        if (shown(rows).isEmpty() || dismissed) return false
        return !seen || missingRequired(rows)
    }
}

/** Where the walkthrough's memory lives: a string store (SharedPreferences on the TV). */
interface PermissionPrefs {
    fun get(key: String): String?
    fun put(key: String, value: String?)
}

class PermissionMemory(private val prefs: PermissionPrefs) {
    var seen: Boolean
        get() = prefs.get(KEY_SEEN) == "1"
        set(value) = prefs.put(KEY_SEEN, if (value) "1" else null)

    fun asked(p: AppPermission): Boolean = prefs.get(KEY_ASKED + p.id) == "1"

    fun markAsked(p: AppPermission) = prefs.put(KEY_ASKED + p.id, "1")

    private companion object {
        const val KEY_SEEN = "permissions.walkthrough_seen"
        const val KEY_ASKED = "permissions.asked."
    }
}
