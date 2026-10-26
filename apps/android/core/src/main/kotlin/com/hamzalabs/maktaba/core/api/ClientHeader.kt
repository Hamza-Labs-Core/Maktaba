package com.hamzalabs.maktaba.core.api

import android.content.Context

/**
 * `X-Maktaba-Client` (Epic 44 story 44.2): every request says which app sent it,
 * `<app>/<version> (<platform>; <device>)`, e.g. `player/1.3.0 (android; phone)`. The home
 * reads it as a label (analytics, its use per app), never for rights; any app but the web's
 * still signs in for tokens, as `native` did. Until the app calls [init] it is `native`.
 */
object ClientHeader {
    const val NAME = "X-Maktaba-Client"

    @Volatile
    var value: String = "native"
        private set

    /** Works out this app's value from its package (once, at start). */
    fun init(context: Context) {
        val version = runCatching {
            context.packageManager.getPackageInfo(context.packageName, 0).versionName
        }.getOrNull()
        value = make(context.packageName, version)
    }

    /**
     * The value for a package: the app from its id (`…remote…` the Remote, else the Player),
     * the device from its flavour's suffix (`.tablet`, `.foldable`, else a phone).
     */
    fun make(packageName: String, version: String?): String {
        val parts = packageName.lowercase().split('.')
        val app = if ("remote" in parts) "remote" else "player"
        val device = when (parts.lastOrNull()) {
            "tablet" -> "tablet"
            "foldable" -> "foldable"
            else -> "phone"
        }
        return "$app/${version?.takeIf { it.isNotBlank() } ?: "dev"} (android; $device)"
    }
}
