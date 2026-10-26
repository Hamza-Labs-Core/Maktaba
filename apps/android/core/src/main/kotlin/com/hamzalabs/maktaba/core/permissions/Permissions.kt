package com.hamzalabs.maktaba.core.permissions

import com.hamzalabs.maktaba.core.session.KeyValueStore

/**
 * The permissions walkthrough's model: which permissions an app asks for, where each stands,
 * what its button does, and when the walkthrough shows. No Android types here, so it is tested
 * on the JVM; `PermissionsWalkthrough.kt` reads the real states and draws it.
 */

/** Whether the app works without a permission. */
enum class Need { Required, Optional }

/** Where a permission stands on this device. */
enum class PermissionState {
    /** The person allowed it. */
    Granted,

    /** Refused, and Android still lets the app ask again. */
    Denied,

    /** Refused, and Android will not ask again ("Don't ask again", or refused twice): only Settings can turn it on. */
    DeniedPermanently,

    /** The app has not asked yet. */
    NotAsked,

    /** This Android version has no such permission (the app has what it needs). */
    NotApplicable,
}

/** Which settings page fixes a permission Android will no longer ask for. */
enum class SettingsPane {
    /** The app's own page (Settings → Apps → the app), where its permissions are. */
    AppDetails,

    /** The app's notifications page. */
    AppNotifications,
}

/** What a permission's button does in its state. */
enum class PermissionAction { Ask, OpenSettings, None }

/**
 * A permission an app asks for: [id] names it in the app and in help (`#permissions`),
 * [permission] is Android's name, [sinceSdk] the first API level that has it as a runtime
 * permission.
 */
data class AppPermission(
    val id: String,
    val permission: String,
    val sinceSdk: Int,
    val need: Need,
    val settings: SettingsPane = SettingsPane.AppDetails,
)

/** The permissions the Maktaba apps ask for. Only what they really use (see help: android-apps#permissions). */
object AppPermissions {
    /**
     * Android 17 (API 37) and later: reaching a device on the local network (a home's address,
     * its mDNS announcement) needs it. Required: a home is on the local network. Android
     * shows and keeps it as «Nearby devices».
     */
    val LocalNetwork = AppPermission(
        id = "local-network",
        permission = "android.permission.ACCESS_LOCAL_NETWORK",
        sinceSdk = 37,
        need = Need.Required,
    )

    /** What Maktaba Player and Maktaba Remote ask for. */
    val phoneApps: List<AppPermission> = listOf(LocalNetwork)
}

/**
 * Where [p] stands, from what Android says: [granted] (`checkSelfPermission`), [asked] (this
 * app asked before: Android does not say), and [rationale] (`shouldShowRequestPermissionRationale`:
 * true only after a refusal Android still lets the app ask again for).
 */
fun permissionState(sdk: Int, p: AppPermission, granted: Boolean, asked: Boolean, rationale: Boolean): PermissionState = when {
    sdk < p.sinceSdk -> PermissionState.NotApplicable
    granted -> PermissionState.Granted
    !asked -> PermissionState.NotAsked
    rationale -> PermissionState.Denied
    // asked, refused, and no rationale: Android will not show its dialog again
    else -> PermissionState.DeniedPermanently
}

/** Asking while Android still shows its dialog; Settings once it will not; nothing when it is in hand. */
val PermissionState.action: PermissionAction
    get() = when (this) {
        PermissionState.NotAsked, PermissionState.Denied -> PermissionAction.Ask
        PermissionState.DeniedPermanently -> PermissionAction.OpenSettings
        PermissionState.Granted, PermissionState.NotApplicable -> PermissionAction.None
    }

/** Whether the app has what [this] state gives it. */
val PermissionState.ok: Boolean get() = this == PermissionState.Granted || this == PermissionState.NotApplicable

/** A permission and where it stands. */
data class PermissionRow(val permission: AppPermission, val state: PermissionState)

object Walkthrough {
    /** The rows the walkthrough lists: the permissions this Android version has. */
    fun shown(rows: List<PermissionRow>): List<PermissionRow> = rows.filter { it.state != PermissionState.NotApplicable }

    /** Whether a required permission is missing. */
    fun missingRequired(rows: List<PermissionRow>): Boolean = rows.any { it.permission.need == Need.Required && !it.state.ok }

    /**
     * Whether the walkthrough shows: never when this Android version has none of the
     * permissions, nor after the person chose Continue until the app next comes back to the
     * foreground ([dismissed]); else on first run (not [seen]) and whenever a required
     * permission is missing.
     */
    fun shouldShow(seen: Boolean, rows: List<PermissionRow>, dismissed: Boolean): Boolean {
        if (shown(rows).isEmpty() || dismissed) return false
        return !seen || missingRequired(rows)
    }
}

/** What the app remembers about the walkthrough: whether it was seen, and which permissions it asked for. */
class PermissionMemory(private val store: KeyValueStore) {
    var seen: Boolean
        get() = store.get(KEY_SEEN) == "1"
        set(value) = store.put(KEY_SEEN, if (value) "1" else null)

    fun asked(p: AppPermission): Boolean = store.get(KEY_ASKED + p.id) == "1"

    fun markAsked(p: AppPermission) = store.put(KEY_ASKED + p.id, "1")

    private companion object {
        const val KEY_SEEN = "permissions.walkthrough_seen"
        const val KEY_ASKED = "permissions.asked."
    }
}
